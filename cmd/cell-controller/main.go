package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	natsbus "mmo/internal/bus/nats"
	"mmo/internal/config"
	"mmo/internal/logging"
	"mmo/internal/splitcontrol"
)

type splitWorkflowEvent struct {
	CellID     string            `json:"cell_id"`
	Stage      string            `json:"stage"`
	Attempt    int               `json:"attempt"`
	Message    string            `json:"message"`
	ChildCell  string            `json:"child_cell,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
	AtUnixMs   int64             `json:"at_unix_ms"`
	Successful bool              `json:"successful"`
}

func main() {
	logging.SetupFromEnv()
	log.SetFlags(0)

	cfg := config.FromEnv()
	if cfg.NATSURL == "" {
		log.Fatal("NATS_URL is required for cell-controller")
	}
	k8s, namespace, err := newK8sClient()
	if err != nil {
		log.Fatal(err)
	}
	client, err := natsbus.ConnectResilient(cfg.NATSURL, natsbus.DefaultReconnectConfig())
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	var rdb *redis.Client
	if cfg.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       0,
		})
		defer rdb.Close()
	}

	subj := strings.TrimSpace(os.Getenv("MMO_CELL_CONTROLLER_SUBJECT"))
	if subj == "" {
		subj = natsbus.SubjectGridSplitWorkflow
	}
	_, err = client.Subscribe(subj, func(msg *nats.Msg) {
		handleWorkflowEvent(msg.Data, rdb)
	})
	if err != nil {
		log.Fatal(err)
	}
	ctrlSubj := strings.TrimSpace(os.Getenv("MMO_CELL_CONTROLLER_CONTROL_SUBJECT"))
	if ctrlSubj == "" {
		ctrlSubj = natsbus.SubjectCellControl
	}
	_, err = client.Subscribe(ctrlSubj, func(msg *nats.Msg) {
		handleControlEvent(msg.Data, rdb, client, k8s, namespace)
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := client.FlushTimeout(2 * time.Second); err != nil {
		log.Fatal(err)
	}
	log.Printf("cell-controller subscribed: workflow=%s control=%s namespace=%s", subj, ctrlSubj, namespace)
	select {}
}

func handleWorkflowEvent(raw []byte, rdb *redis.Client) {
	var ev splitWorkflowEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		slog.Warn("cell-controller: bad event", "err", err)
		return
	}
	if ev.CellID == "" {
		return
	}
	slog.Info("cell-controller event", "cell_id", ev.CellID, "stage", ev.Stage, "attempt", ev.Attempt)
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Foundation storage for controller decisions/observability.
	_ = rdb.Set(ctx, "mmo:cell-controller:last:"+ev.CellID, raw, 24*time.Hour).Err()
	if ev.Stage == "parent_retiring" && ev.Successful {
		_ = rdb.Set(ctx, "mmo:cell-controller:retire:"+ev.CellID, "1", 24*time.Hour).Err()
		slog.Info("cell-controller retire_ready_set", "cell_id", ev.CellID)
	}
}

func handleControlEvent(raw []byte, rdb *redis.Client, nc *natsbus.Client, k8s *kubernetes.Clientset, ns string) {
	var req splitcontrol.ChildCellCreateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		slog.Warn("cell-controller: bad control event", "err", err)
		return
	}
	if strings.TrimSpace(req.Child.ID) == "" {
		return
	}
	serviceName, deployName := namesForCell(req.Child.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	image, err := resolveCellImage(ctx, k8s, ns)
	if err != nil {
		publishCellLifecycle(nc, splitcontrol.CellLifecycleEvent{
			CellID:   req.Child.ID,
			Action:   "cell.failed",
			OK:       false,
			Message:  "resolve image: " + err.Error(),
			ParentID: req.ParentCellID,
			AtUnixMs: time.Now().UnixMilli(),
		})
		return
	}
	if err := ensureCellService(ctx, k8s, ns, serviceName, deployName); err != nil {
		publishCellLifecycle(nc, splitcontrol.CellLifecycleEvent{
			CellID:   req.Child.ID,
			Action:   "cell.failed",
			OK:       false,
			Message:  "service: " + err.Error(),
			ParentID: req.ParentCellID,
			AtUnixMs: time.Now().UnixMilli(),
		})
		return
	}
	if err := ensureCellDeployment(ctx, k8s, ns, deployName, serviceName, image, req.Child); err != nil {
		publishCellLifecycle(nc, splitcontrol.CellLifecycleEvent{
			CellID:   req.Child.ID,
			Action:   "cell.failed",
			OK:       false,
			Message:  "deployment: " + err.Error(),
			ParentID: req.ParentCellID,
			AtUnixMs: time.Now().UnixMilli(),
		})
		return
	}
	publishCellLifecycle(nc, splitcontrol.CellLifecycleEvent{
		CellID:   req.Child.ID,
		Action:   "cell.created",
		OK:       true,
		Message:  fmt.Sprintf("deployment=%s service=%s", deployName, serviceName),
		ParentID: req.ParentCellID,
		AtUnixMs: time.Now().UnixMilli(),
	})
	if rdb != nil {
		_ = rdb.Set(ctx, "mmo:cell-controller:created:"+req.Child.ID, serviceName, 24*time.Hour).Err()
	}
}

func newK8sClient() (*kubernetes.Clientset, string, error) {
	ns := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if ns == "" {
		ns = "mmo"
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, "", err
	}
	cli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, "", err
	}
	return cli, ns, nil
}

func resolveCellImage(ctx context.Context, k8s *kubernetes.Clientset, ns string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("MMO_CELL_IMAGE")); v != "" {
		return v, nil
	}
	dep, err := k8s.AppsV1().Deployments(ns).Get(ctx, "cell-node", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(dep.Spec.Template.Spec.Containers) == 0 {
		return "", fmt.Errorf("cell-node deployment has no containers")
	}
	return dep.Spec.Template.Spec.Containers[0].Image, nil
}

var nonRFC1123 = regexp.MustCompile(`[^a-z0-9-]+`)

func namesForCell(cellID string) (serviceName, deployName string) {
	s := strings.ToLower(strings.TrimSpace(cellID))
	s = strings.ReplaceAll(s, "_", "-")
	s = nonRFC1123.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 30 {
		s = s[:30]
	}
	if s == "" {
		s = "auto"
	}
	serviceName = "mmo-cell-auto-" + s
	deployName = "cell-node-auto-" + s
	return serviceName, deployName
}

func ensureCellService(ctx context.Context, k8s *kubernetes.Clientset, ns, serviceName, deployName string) error {
	svcClient := k8s.CoreV1().Services(ns)
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: ns,
			Labels: map[string]string{
				"app":        "cell-node",
				"cell_shard": deployName,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":        "cell-node",
				"cell_shard": deployName,
			},
			Ports: []corev1.ServicePort{
				{Name: "grpc", Port: 50051, TargetPort: intstrFromInt(50051)},
				{Name: "metrics", Port: 9090, TargetPort: intstrFromInt(9090)},
			},
		},
	}
	cur, err := svcClient.Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = svcClient.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		return err
	}
	cur.Labels = desired.Labels
	cur.Spec.Selector = desired.Spec.Selector
	cur.Spec.Ports = desired.Spec.Ports
	_, err = svcClient.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

func ensureCellDeployment(ctx context.Context, k8s *kubernetes.Clientset, ns, deployName, serviceName, image string, ch splitcontrol.ChildCellSpec) error {
	depClient := k8s.AppsV1().Deployments(ns)
	replicas := int32(1)
	labels := map[string]string{
		"app":        "cell-node",
		"cell_shard": deployName,
	}
	advertise := fmt.Sprintf("%s.%s.svc.cluster.local:50051", serviceName, ns)
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "cell-node",
							Image:           image,
							ImagePullPolicy: corev1.PullAlways,
							Command:         []string{"/cell-node"},
							Args: []string{
								"-listen", "0.0.0.0:50051",
								"-id", ch.ID,
								"-level", strconv.Itoa(int(ch.Level)),
								"-xmin", fmt.Sprintf("%g", ch.XMin),
								"-xmax", fmt.Sprintf("%g", ch.XMax),
								"-zmin", fmt.Sprintf("%g", ch.ZMin),
								"-zmax", fmt.Sprintf("%g", ch.ZMax),
								"-metrics-listen", "0.0.0.0:9090",
							},
							Ports: []corev1.ContainerPort{
								{Name: "grpc", ContainerPort: 50051},
								{Name: "metrics", ContainerPort: 9090},
							},
							Env: []corev1.EnvVar{
								{Name: "MMO_CELL_GRPC_ADVERTISE", Value: advertise},
								{Name: "MMO_LOG_FORMAT", Value: "json"},
							},
							EnvFrom: []corev1.EnvFromSource{
								{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "mmo-backend"}}},
							},
						},
					},
				},
			},
		},
	}
	cur, err := depClient.Get(ctx, deployName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = depClient.Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		return err
	}
	cur.Labels = desired.Labels
	cur.Spec = desired.Spec
	_, err = depClient.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

func publishCellLifecycle(nc *natsbus.Client, ev splitcontrol.CellLifecycleEvent) {
	if nc == nil {
		return
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if err := nc.Publish(natsbus.SubjectCellEvents, raw); err != nil {
		slog.Warn("cell-controller publish failed", "err", err)
		return
	}
	_ = nc.FlushTimeout(300 * time.Millisecond)
}

func intstrFromInt(v int) intstr.IntOrString {
	return intstr.IntOrString{Type: intstr.Int, IntVal: int32(v)}
}
