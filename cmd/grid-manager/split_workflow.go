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
	"mmo/internal/partition"
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
	// Идемпотентно фиксируем drain (не только от load policy).
	if err := r.setSplitDrain(ctx, parentCellID, true); err != nil {
		return fmt.Errorf("set split_drain true: %w", err)
	}

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

	// Политика спринта: multi-child — все handoff обязаны завершиться успешно (без partial-success).
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
			return fmt.Errorf("handoff failed for child %s: %w", child, err)
		}
	}

	if err := r.setSplitDrain(ctx, parentCellID, false); err != nil {
		return fmt.Errorf("clear split_drain after handoffs: %w", err)
	}

	joined := strings.Join(readyChildren, ",")
	childSpecOrder := make([]splitcontrol.ChildCellSpec, 0, len(readyChildren))
	byID := make(map[string]splitcontrol.ChildCellSpec, len(children))
	for _, ch := range children {
		byID[ch.ID] = ch
	}
	for _, id := range readyChildren {
		if sp, ok := byID[id]; ok {
			childSpecOrder = append(childSpecOrder, sp)
		}
	}
	r.publish(splitWorkflowEvent{
		CellID:     parentCellID,
		Stage:      splitcontrol.StageRetireReady,
		Attempt:    attempt,
		Message:    "all handoffs ok; split_drain cleared; retire_ready; running post_handoff orchestration (preflight + automation_complete) unless MMO_GRID_AUTO_POST_HANDOFF_ORCHESTRATION=false",
		AtUnixMs:   time.Now().UnixMilli(),
		Successful: true,
		Attrs:      map[string]string{"handoff_children": joined},
	})
	if r.store != nil {
		_ = r.store.saveRetireReady(ctx, parentCellID, readyChildren)
	}
	if err := r.runPostHandoffOrchestration(ctx, parentCellID, attempt, readyChildren, childSpecOrder); err != nil {
		return err
	}

	if envBool("MMO_GRID_SPLIT_TEARDOWN_RUNTIME_CHILDREN") {
		reason := fmt.Sprintf("grid-auto-split-teardown parent=%s", parentCellID)
		for _, cid := range readyChildren {
			if err := r.requestChildDelete(ctx, cid, reason); err != nil {
				slog.Warn("split workflow: teardown child failed", "cell_id", cid, "err", err)
			}
		}
	}

	return nil
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

func (r *splitWorkflowRuntime) requestChildDelete(ctx context.Context, cellID, reason string) error {
	if r.nats == nil {
		return fmt.Errorf("nats client is required for child delete")
	}
	req := splitcontrol.RuntimeCellDeleteRequest{
		Op:     splitcontrol.OpDeleteRuntimeChild,
		CellID: cellID,
		Reason: reason,
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
	return s.rdb.Set(ctx, "mmo:grid:split:"+cellID, raw, maxWorkflowRedisTTL()).Err()
}

func maxWorkflowRedisTTL() time.Duration {
	return 7 * 24 * time.Hour
}

func retireStateRedisKey(parentID string) string {
	return "mmo:grid:split:retire_state:" + strings.TrimSpace(parentID)
}

func (s *splitWorkflowStateStore) getRetireStateMap(ctx context.Context, parentID string) (map[string]any, error) {
	if s == nil || s.rdb == nil {
		return nil, nil
	}
	raw, err := s.rdb.Get(ctx, retireStateRedisKey(parentID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *splitWorkflowStateStore) setRetireStateMap(ctx context.Context, parentID string, m map[string]any) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, retireStateRedisKey(parentID), raw, maxWorkflowRedisTTL()).Err()
}

func (s *splitWorkflowStateStore) saveRetireReady(ctx context.Context, parentID string, children []string) error {
	if s == nil || s.rdb == nil {
		return nil
	}
	payload := map[string]any{
		"phase":            splitcontrol.RetireStatePhaseRetireReady,
		"parent_cell_id":   parentID,
		"handoff_children": children,
		"at_unix_ms":       time.Now().UnixMilli(),
		"next_action":      splitcontrol.NextActionOperatorFinalRetire,
		"next_step":        "После успешного post-handoff в Redis — только операторский §5 (runbook) для вывода baseline parent; Terraform primary не удаляется автоматически.",
		"optional_env":     "MMO_GRID_SPLIT_TEARDOWN_RUNTIME_CHILDREN=true удаляет только runtime child Deployment/Service после успешного workflow.",
	}
	return s.setRetireStateMap(ctx, parentID, payload)
}

func postHandoffOrchestrationEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("MMO_GRID_AUTO_POST_HANDOFF_ORCHESTRATION")))
	if v == "" {
		return true
	}
	return v == "1" || v == "true" || v == "yes"
}

func (r *splitWorkflowRuntime) runPostHandoffOrchestration(ctx context.Context, parentID string, attempt int, readyChildren []string, childSpecs []splitcontrol.ChildCellSpec) error {
	if !postHandoffOrchestrationEnabled() {
		slog.Info("post-handoff orchestration disabled", "cell_id", parentID, "env", "MMO_GRID_AUTO_POST_HANDOFF_ORCHESTRATION=false")
		return nil
	}
	if r.store == nil || r.store.rdb == nil {
		slog.Warn("post-handoff orchestration skipped: redis store not configured", "cell_id", parentID)
		return nil
	}
	st, err := r.store.getRetireStateMap(ctx, parentID)
	if err != nil {
		return fmt.Errorf("post-handoff: read retire state: %w", err)
	}
	if st != nil {
		if ph, _ := st["phase"].(string); ph == splitcontrol.RetireStatePhaseAutomationComplete {
			r.publish(splitWorkflowEvent{
				CellID:     parentID,
				Stage:      splitcontrol.StageAutomationComplete,
				Attempt:    attempt,
				Message:    "idempotent skip: retire_state already automation_complete",
				Successful: true,
				AtUnixMs:   time.Now().UnixMilli(),
			})
			return nil
		}
	}
	blocked := postRetirePreflightReasons(ctx, r.cat, parentID, readyChildren, childSpecs)
	if len(blocked) > 0 {
		if st == nil {
			st = map[string]any{}
		}
		st["phase"] = splitcontrol.RetireStatePhasePreflightBlocked
		st["preflight_blocked_reasons"] = blocked
		st["preflight_at_unix_ms"] = time.Now().UnixMilli()
		if err := r.store.setRetireStateMap(ctx, parentID, st); err != nil {
			return fmt.Errorf("post-handoff: save preflight_blocked: %w", err)
		}
		r.publish(splitWorkflowEvent{
			CellID:     parentID,
			Stage:      splitcontrol.StagePostHandoffPreflightFailed,
			Attempt:    attempt,
			Message:    fmt.Sprintf("preflight blocked: %s", strings.Join(blocked, "; ")),
			Successful: false,
			AtUnixMs:   time.Now().UnixMilli(),
			Attrs:      map[string]string{"blocked_count": strconv.Itoa(len(blocked))},
		})
		return nil
	}
	opID := fmt.Sprintf("%s-a%d-%d", parentID, attempt, time.Now().UnixMilli())
	if st == nil {
		st = map[string]any{}
	}
	st["phase"] = splitcontrol.RetireStatePhaseAutomationComplete
	st["parent_cell_id"] = parentID
	st["handoff_children"] = readyChildren
	st["operation_id"] = opID
	st["preflight_ok"] = true
	st["preflight_gates"] = []string{"parent_catalog_ping", "children_catalog_ping", "resolve_child_centers"}
	st["next_action"] = splitcontrol.NextActionOperatorFinalRetire
	st["next_step"] = "Оператор: финальный вывод baseline parent — backend/runbooks/cold-cell-split.md §5 и при необходимости cell_instances + tofu apply. Baseline Terraform primary не удаляется автоматически; runtime child — опционально MMO_GRID_SPLIT_TEARDOWN_RUNTIME_CHILDREN."
	st["at_unix_ms_automation_complete"] = time.Now().UnixMilli()
	if err := r.store.setRetireStateMap(ctx, parentID, st); err != nil {
		return fmt.Errorf("post-handoff: save automation_complete: %w", err)
	}
	r.publish(splitWorkflowEvent{
		CellID:     parentID,
		Stage:      splitcontrol.StageAutomationComplete,
		Attempt:    attempt,
		Message:    "post_handoff preflight ok; automation_complete; next: operator_final_retire (§5)",
		Successful: true,
		AtUnixMs:   time.Now().UnixMilli(),
		Attrs:      map[string]string{"operation_id": opID, "handoff_children": strings.Join(readyChildren, ",")},
	})
	if r.store != nil {
		_ = r.store.save(ctx, parentID, splitWorkflowEvent{
			CellID:     parentID,
			Stage:      splitcontrol.StageAutomationComplete,
			Attempt:    attempt,
			Message:    "automation_complete",
			Successful: true,
			Attrs:      map[string]string{"operation_id": opID},
			AtUnixMs:   time.Now().UnixMilli(),
		})
	}
	return nil
}

func postRetirePreflightReasons(ctx context.Context, cat discovery.Catalog, parentID string, childIDs []string, specs []splitcontrol.ChildCellSpec) []string {
	var reasons []string
	if len(childIDs) == 0 {
		return []string{"no_handoff_children"}
	}
	parent, ok, err := discovery.FindCellByID(ctx, cat, parentID)
	if err != nil {
		return []string{"catalog_parent:" + err.Error()}
	}
	if !ok || parent == nil {
		reasons = append(reasons, "parent_missing_in_catalog")
	} else {
		if ep := strings.TrimSpace(parent.GetGrpcEndpoint()); ep == "" || !isCellReachable(ctx, ep) {
			reasons = append(reasons, "parent_unreachable")
		}
	}
	for _, id := range childIDs {
		cs, ok, err := discovery.FindCellByID(ctx, cat, id)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("child_%s_catalog:%v", id, err))
			continue
		}
		if !ok || cs == nil {
			reasons = append(reasons, fmt.Sprintf("child_%s_missing_in_catalog", id))
			continue
		}
		if ep := strings.TrimSpace(cs.GetGrpcEndpoint()); ep == "" || !isCellReachable(ctx, ep) {
			reasons = append(reasons, fmt.Sprintf("child_%s_unreachable", id))
		}
	}
	cells, lerr := cat.List(ctx)
	if lerr != nil {
		reasons = append(reasons, "catalog_list:"+lerr.Error())
		return dedupeReasons(reasons)
	}
	reasons = append(reasons, resolveChildProbeReasons(ctx, cat, specs, cells)...)
	return dedupeReasons(reasons)
}

func pointInForeignDeeperCell(cx, cz float64, selfID string, selfLevel int32, cells []*cellv1.CellSpec) bool {
	for _, c := range cells {
		if c == nil || c.Bounds == nil {
			continue
		}
		if c.Id == selfID {
			continue
		}
		if c.Level <= selfLevel {
			continue
		}
		if partition.Contains(c.Bounds, cx, cz) {
			return true
		}
	}
	return false
}

func pickResolveProbe(sp splitcontrol.ChildCellSpec, cells []*cellv1.CellSpec) (cx, cz float64, ok bool) {
	id := strings.TrimSpace(sp.ID)
	xw := sp.XMax - sp.XMin
	zw := sp.ZMax - sp.ZMin
	if xw <= 0 || zw <= 0 || id == "" {
		return 0, 0, false
	}
	fr := []float64{0.12, 0.28, 0.44, 0.58, 0.72, 0.88}
	for _, fx := range fr {
		for _, fz := range fr {
			cx := sp.XMin + xw*fx
			cz := sp.ZMin + zw*fz
			if !partition.Contains(&cellv1.Bounds{XMin: sp.XMin, XMax: sp.XMax, ZMin: sp.ZMin, ZMax: sp.ZMax}, cx, cz) {
				continue
			}
			if pointInForeignDeeperCell(cx, cz, id, sp.Level, cells) {
				continue
			}
			return cx, cz, true
		}
	}
	return 0, 0, false
}

func resolveChildProbeReasons(ctx context.Context, cat discovery.Catalog, specs []splitcontrol.ChildCellSpec, cells []*cellv1.CellSpec) []string {
	var reasons []string
	for _, sp := range specs {
		id := strings.TrimSpace(sp.ID)
		if id == "" {
			continue
		}
		cx, cz, found := pickResolveProbe(sp, cells)
		if !found {
			reasons = append(reasons, fmt.Sprintf("resolve_%s:no_probe_point", id))
			continue
		}
		got, ok, err := cat.ResolveMostSpecific(ctx, cx, cz)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("resolve_%s:%v", id, err))
			continue
		}
		if !ok || got == nil {
			reasons = append(reasons, fmt.Sprintf("resolve_%s:no_match", id))
			continue
		}
		if got.GetId() != id {
			reasons = append(reasons, fmt.Sprintf("resolve_%s:want_got_%s", id, got.GetId()))
		}
	}
	return reasons
}

func dedupeReasons(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
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
