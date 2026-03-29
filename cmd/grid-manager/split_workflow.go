package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/config"
	"mmo/internal/discovery"
	"mmo/internal/splitcontrol"
)

var (
	splitWorkflowRunsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "split_workflow_runs_total",
			Help:      "Total split workflow runs by result",
		},
		[]string{"result"},
	)
	splitWorkflowDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "split_workflow_duration_seconds",
			Help:      "Split workflow duration in seconds",
			Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 40, 80},
		},
	)
)

type splitWorkflowConfig struct {
	enabled        bool
	registryAddr   string
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	waitChildren   time.Duration
}

type splitWorkflowRuntime struct {
	cfg   splitWorkflowConfig
	cat   discovery.Catalog
	nats  *natsbus.Client
	store *splitWorkflowStateStore

	mu     sync.Mutex
	active map[string]struct{}
}

type splitWorkflowStateStore struct {
	rdb *redis.Client
}

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

func parseSplitWorkflowConfig() splitWorkflowConfig {
	cfg := splitWorkflowConfig{
		enabled:        envBool("MMO_GRID_AUTO_SPLIT_WORKFLOW"),
		registryAddr:   firstNonEmpty(strings.TrimSpace(os.Getenv("MMO_GRID_REGISTRY_ADDR")), "127.0.0.1:9100"),
		maxRetries:     4,
		initialBackoff: 1 * time.Second,
		maxBackoff:     12 * time.Second,
		waitChildren:   90 * time.Second,
	}
	if n := parseIntWithDefault(os.Getenv("MMO_GRID_SPLIT_WORKFLOW_MAX_RETRIES"), cfg.maxRetries); n >= 1 {
		cfg.maxRetries = n
	}
	if d := parseDurationWithDefault(os.Getenv("MMO_GRID_SPLIT_WORKFLOW_BACKOFF"), cfg.initialBackoff); d > 0 {
		cfg.initialBackoff = d
	}
	if d := parseDurationWithDefault(os.Getenv("MMO_GRID_SPLIT_WORKFLOW_MAX_BACKOFF"), cfg.maxBackoff); d > 0 {
		cfg.maxBackoff = d
	}
	if d := parseDurationWithDefault(os.Getenv("MMO_GRID_SPLIT_WORKFLOW_WAIT_CHILDREN"), cfg.waitChildren); d > 0 {
		cfg.waitChildren = d
	}
	return cfg
}

func newSplitWorkflowRuntime(cat discovery.Catalog) *splitWorkflowRuntime {
	cfg := parseSplitWorkflowConfig()
	rt := &splitWorkflowRuntime{
		cfg:    cfg,
		cat:    cat,
		active: make(map[string]struct{}),
	}
	env := config.FromEnv()
	if env.RedisAddr != "" {
		rt.store = &splitWorkflowStateStore{
			rdb: redis.NewClient(&redis.Options{
				Addr:     env.RedisAddr,
				Password: env.RedisPassword,
				DB:       0,
			}),
		}
	}
	if env.NATSURL != "" {
		cli, err := natsbus.ConnectResilient(env.NATSURL, natsbus.DefaultReconnectConfig())
		if err != nil {
			slog.Warn("split workflow: nats connect failed", "err", err)
		} else {
			rt.nats = cli
		}
	}
	return rt
}

func (r *splitWorkflowRuntime) close() {
	if r.nats != nil {
		r.nats.Close()
	}
	if r.store != nil && r.store.rdb != nil {
		_ = r.store.rdb.Close()
	}
}

func (r *splitWorkflowRuntime) maybeStart(ctx context.Context, cellID string) {
	cellID = strings.TrimSpace(cellID)
	if !r.cfg.enabled || cellID == "" {
		return
	}
	r.mu.Lock()
	if _, ok := r.active[cellID]; ok {
		r.mu.Unlock()
		return
	}
	r.active[cellID] = struct{}{}
	r.mu.Unlock()
	go r.run(ctx, cellID)
}

func (r *splitWorkflowRuntime) run(ctx context.Context, cellID string) {
	start := time.Now()
	slog.Info("split workflow: start", "cell_id", cellID, "max_retries", r.cfg.maxRetries)
	defer func() {
		splitWorkflowDurationSeconds.Observe(time.Since(start).Seconds())
		r.mu.Lock()
		delete(r.active, cellID)
		r.mu.Unlock()
	}()
	r.publish(splitWorkflowEvent{
		CellID:   cellID,
		Stage:    "detected",
		Message:  "workflow started",
		AtUnixMs: time.Now().UnixMilli(),
	})
	backoff := r.cfg.initialBackoff
	var lastErr error
	for attempt := 1; attempt <= r.cfg.maxRetries; attempt++ {
		err := r.runOnce(ctx, cellID, attempt)
		if err == nil {
			slog.Info("split workflow: done", "cell_id", cellID, "attempt", attempt)
			splitWorkflowRunsTotal.WithLabelValues("ok").Inc()
			r.publish(splitWorkflowEvent{
				CellID:     cellID,
				Stage:      "done",
				Attempt:    attempt,
				Message:    "workflow completed",
				Successful: true,
				AtUnixMs:   time.Now().UnixMilli(),
			})
			if r.store != nil {
				_ = r.store.save(ctx, cellID, splitWorkflowEvent{
					CellID:     cellID,
					Stage:      "done",
					Attempt:    attempt,
					Message:    "workflow completed",
					Successful: true,
					AtUnixMs:   time.Now().UnixMilli(),
				})
			}
			return
		}
		lastErr = err
		slog.Warn("split workflow: retry", "cell_id", cellID, "attempt", attempt, "err", err)
		r.publish(splitWorkflowEvent{
			CellID:   cellID,
			Stage:    "retrying",
			Attempt:  attempt,
			Message:  err.Error(),
			AtUnixMs: time.Now().UnixMilli(),
		})
		if r.store != nil {
			_ = r.store.save(ctx, cellID, splitWorkflowEvent{
				CellID:   cellID,
				Stage:    "retrying",
				Attempt:  attempt,
				Message:  err.Error(),
				AtUnixMs: time.Now().UnixMilli(),
			})
		}
		select {
		case <-ctx.Done():
			slog.Warn("split workflow: cancelled", "cell_id", cellID)
			splitWorkflowRunsTotal.WithLabelValues("cancelled").Inc()
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > r.cfg.maxBackoff {
			backoff = r.cfg.maxBackoff
		}
	}
	splitWorkflowRunsTotal.WithLabelValues("failed").Inc()
	slog.Error("split workflow: failed", "cell_id", cellID, "err", lastErr)
	r.publish(splitWorkflowEvent{
		CellID:   cellID,
		Stage:    "failed",
		Message:  fmt.Sprintf("workflow failed after retries: %v", lastErr),
		AtUnixMs: time.Now().UnixMilli(),
	})
}

func (r *splitWorkflowRuntime) runOnce(ctx context.Context, parentCellID string, attempt int) error {
	r.publish(splitWorkflowEvent{
		CellID:   parentCellID,
		Stage:    "draining",
		Attempt:  attempt,
		Message:  "ensure split_drain=true",
		AtUnixMs: time.Now().UnixMilli(),
	})

	children, err := r.planSplitChildren(ctx, parentCellID)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return fmt.Errorf("no child cells found for parent %s", parentCellID)
	}
	r.publish(splitWorkflowEvent{
		CellID:   parentCellID,
		Stage:    "children_creating",
		Attempt:  attempt,
		Message:  fmt.Sprintf("request create children=%d", len(children)),
		AtUnixMs: time.Now().UnixMilli(),
	})
	for _, ch := range children {
		if err := r.requestChildCreate(ctx, parentCellID, ch, attempt); err != nil {
			return err
		}
	}
	readyChildren, err := r.waitChildrenReady(ctx, children)
	if err != nil {
		return err
	}
	r.publish(splitWorkflowEvent{
		CellID:   parentCellID,
		Stage:    "children_wait_ready",
		Attempt:  attempt,
		Message:  fmt.Sprintf("children ready=%d", len(readyChildren)),
		AtUnixMs: time.Now().UnixMilli(),
		Attrs:    map[string]string{"children": strings.Join(readyChildren, ",")},
	})

	if err := r.runMigrationDryRun(ctx, parentCellID); err != nil {
		return err
	}

	for _, child := range readyChildren {
		r.publish(splitWorkflowEvent{
			CellID:    parentCellID,
			ChildCell: child,
			Stage:     "handoff_running",
			Attempt:   attempt,
			Message:   "forward npc handoff",
			AtUnixMs:  time.Now().UnixMilli(),
		})
		if err := r.forwardHandoff(ctx, parentCellID, child, fmt.Sprintf("auto-split-attempt-%d", attempt)); err != nil {
			r.publish(splitWorkflowEvent{
				CellID:    parentCellID,
				ChildCell: child,
				Stage:     "handoff_failed",
				Attempt:   attempt,
				Message:   err.Error(),
				AtUnixMs:  time.Now().UnixMilli(),
			})
			continue
		}
		_ = r.setSplitDrain(ctx, parentCellID, false)
		r.publish(splitWorkflowEvent{
			CellID:     parentCellID,
			ChildCell:  child,
			Stage:      "parent_retiring",
			Attempt:    attempt,
			Message:    "handoff ok; parent marked for retire",
			AtUnixMs:   time.Now().UnixMilli(),
			Successful: true,
		})
		return nil
	}
	return fmt.Errorf("handoff failed for all child candidates")
}

func (r *splitWorkflowRuntime) planSplitChildren(ctx context.Context, parentCellID string) ([]splitcontrol.ChildCellSpec, error) {
	parent, ok, err := discovery.FindCellByID(ctx, r.cat, parentCellID)
	if err != nil {
		return nil, err
	}
	if !ok || parent == nil {
		return nil, fmt.Errorf("parent cell not found: %s", parentCellID)
	}
	childrenFromPlan, err := r.planSplit(ctx, parent.GetGrpcEndpoint())
	if err != nil {
		return nil, err
	}
	if len(childrenFromPlan) == 0 {
		return nil, fmt.Errorf("PlanSplit returned no children for %s", parentCellID)
	}
	return childrenFromPlan, nil
}

func (r *splitWorkflowRuntime) planSplit(ctx context.Context, endpoint string) ([]splitcontrol.ChildCellSpec, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cl := cellv1.NewCellClient(conn)
	resp, err := cl.PlanSplit(cctx, &cellv1.PlanSplitRequest{Reason: "grid-auto-split"})
	if err != nil {
		return nil, err
	}
	out := make([]splitcontrol.ChildCellSpec, 0, len(resp.GetChildren()))
	for _, ch := range resp.GetChildren() {
		id := strings.TrimSpace(ch.GetId())
		if id == "" || ch.GetBounds() == nil {
			continue
		}
		b := ch.GetBounds()
		out = append(out, splitcontrol.ChildCellSpec{
			ID:    id,
			Level: ch.GetLevel(),
			XMin:  b.GetXMin(),
			XMax:  b.GetXMax(),
			ZMin:  b.GetZMin(),
			ZMax:  b.GetZMax(),
		})
	}
	return out, nil
}

func (r *splitWorkflowRuntime) requestChildCreate(ctx context.Context, parentCellID string, ch splitcontrol.ChildCellSpec, attempt int) error {
	if r.nats == nil {
		return fmt.Errorf("nats client is required for child create request")
	}
	req := splitcontrol.ChildCellCreateRequest{
		ParentCellID: parentCellID,
		RequestID:    fmt.Sprintf("%s-a%d-%d", parentCellID, attempt, time.Now().UnixMilli()),
		Child:        ch,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := r.nats.Publish(natsbus.SubjectCellControl, raw); err != nil {
		return err
	}
	return r.nats.FlushTimeout(400 * time.Millisecond)
}

func (r *splitWorkflowRuntime) waitChildrenReady(ctx context.Context, children []splitcontrol.ChildCellSpec) ([]string, error) {
	deadline := time.Now().Add(r.cfg.waitChildren)
	want := make(map[string]splitcontrol.ChildCellSpec, len(children))
	for _, ch := range children {
		want[ch.ID] = ch
	}
	for {
		cells, err := r.cat.List(ctx)
		if err != nil {
			return nil, err
		}
		ready := make([]string, 0, len(children))
		for _, c := range cells {
			if c == nil {
				continue
			}
			id := c.GetId()
			if _, ok := want[id]; !ok {
				continue
			}
			if endpoint := strings.TrimSpace(c.GetGrpcEndpoint()); endpoint != "" && isCellReachable(ctx, endpoint) {
				ready = append(ready, id)
			}
		}
		if len(ready) == len(children) {
			return ready, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("children not ready in time: have=%d want=%d", len(ready), len(children))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func isCellReachable(ctx context.Context, endpoint string) bool {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer conn.Close()
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cl := cellv1.NewCellClient(conn)
	_, err = cl.Ping(pctx, &cellv1.PingRequest{ClientId: "grid-workflow"})
	return err == nil
}

func (r *splitWorkflowRuntime) runMigrationDryRun(ctx context.Context, parentCellID string) error {
	conn, err := grpc.NewClient(r.cfg.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cl := cellv1.NewRegistryClient(conn)
	_, err = cl.ForwardCellUpdate(cctx, &cellv1.ForwardCellUpdateRequest{
		CellId: parentCellID,
		Update: &cellv1.UpdateRequest{
			Payload: &cellv1.UpdateRequest_ExportNpcPersist{
				ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: "grid-auto-split-dry-run"},
			},
		},
	})
	return err
}

func (r *splitWorkflowRuntime) forwardHandoff(ctx context.Context, parentCellID, childCellID, reason string) error {
	conn, err := grpc.NewClient(r.cfg.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cl := cellv1.NewRegistryClient(conn)
	resp, err := cl.ForwardNpcHandoff(cctx, &cellv1.ForwardNpcHandoffRequest{
		ParentCellId: parentCellID,
		ChildCellId:  childCellID,
		Reason:       reason,
	})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("handoff not ok: %s", resp.GetMessage())
	}
	return nil
}

func (r *splitWorkflowRuntime) setSplitDrain(ctx context.Context, parentCellID string, enabled bool) error {
	conn, err := grpc.NewClient(r.cfg.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cl := cellv1.NewRegistryClient(conn)
	_, err = cl.ForwardCellUpdate(cctx, &cellv1.ForwardCellUpdateRequest{
		CellId: parentCellID,
		Update: &cellv1.UpdateRequest{
			Payload: &cellv1.UpdateRequest_SetSplitDrain{
				SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: enabled},
			},
		},
	})
	return err
}

func (r *splitWorkflowRuntime) publish(event splitWorkflowEvent) {
	if r.nats == nil {
		return
	}
	if event.AtUnixMs == 0 {
		event.AtUnixMs = time.Now().UnixMilli()
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	if err := r.nats.Publish(natsbus.SubjectGridSplitWorkflow, raw); err != nil {
		slog.Warn("split workflow publish failed", "err", err)
		return
	}
	_ = r.nats.FlushTimeout(250 * time.Millisecond)
}

func (s *splitWorkflowStateStore) save(ctx context.Context, cellID string, event splitWorkflowEvent) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, "mmo:grid:split:"+cellID, raw, 24*time.Hour).Err()
}

func parseDurationWithDefault(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func parseIntWithDefault(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
