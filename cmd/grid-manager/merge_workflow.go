package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
	mergeAutoWorkflowRunsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "merge_auto_workflow_runs_total",
			Help:      "Total merge auto-workflow runs by result",
		},
		[]string{"result"},
	)
	mergeAutoWorkflowDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "merge_auto_workflow_duration_seconds",
			Help:      "Merge auto-workflow duration in seconds",
			Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 40, 80},
		},
	)
)

type mergeWorkflowConfig struct {
	enabled        bool
	registryAddr   string
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	blockedParents map[string]struct{}
	playerHandoff  bool
	maxPlayersMove int
}

type mergeWorkflowRuntime struct {
	cfg   mergeWorkflowConfig
	cat   discovery.Catalog
	nats  *natsbus.Client
	store *redis.Client

	mu     sync.Mutex
	active map[string]struct{}
}

type mergeWorkflowEvent struct {
	ParentCellID string            `json:"parent_cell_id"`
	Stage        string            `json:"stage"`
	Attempt      int               `json:"attempt"`
	Message      string            `json:"message"`
	Children     string            `json:"children,omitempty"`
	Attrs        map[string]string `json:"attrs,omitempty"`
	AtUnixMs     int64             `json:"at_unix_ms"`
	Successful   bool              `json:"successful"`
}

func parseMergeWorkflowConfig() mergeWorkflowConfig {
	cfg := mergeWorkflowConfig{
		enabled:        envBool("MMO_GRID_AUTO_MERGE_WORKFLOW"),
		registryAddr:   firstNonEmpty(strings.TrimSpace(os.Getenv("MMO_GRID_REGISTRY_ADDR")), "127.0.0.1:9100"),
		maxRetries:     3,
		initialBackoff: 1 * time.Second,
		maxBackoff:     10 * time.Second,
		blockedParents: parseCellIDSet(os.Getenv("MMO_GRID_MERGE_WORKFLOW_BLOCKLIST")),
		playerHandoff:  envBool("MMO_GRID_MERGE_PLAYER_HANDOFF"),
		maxPlayersMove: 32,
	}
	if n := parseIntWithDefault(os.Getenv("MMO_GRID_MERGE_WORKFLOW_MAX_RETRIES"), cfg.maxRetries); n >= 1 {
		cfg.maxRetries = n
	}
	if d := parseDurationWithDefault(os.Getenv("MMO_GRID_MERGE_WORKFLOW_BACKOFF"), cfg.initialBackoff); d > 0 {
		cfg.initialBackoff = d
	}
	if d := parseDurationWithDefault(os.Getenv("MMO_GRID_MERGE_WORKFLOW_MAX_BACKOFF"), cfg.maxBackoff); d > 0 {
		cfg.maxBackoff = d
	}
	if n := parseIntWithDefault(os.Getenv("MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS"), cfg.maxPlayersMove); n > 0 {
		cfg.maxPlayersMove = n
	}
	return cfg
}

func newMergeWorkflowRuntime(cat discovery.Catalog) *mergeWorkflowRuntime {
	cfg := parseMergeWorkflowConfig()
	rt := &mergeWorkflowRuntime{
		cfg:    cfg,
		cat:    cat,
		active: make(map[string]struct{}),
	}
	env := config.FromEnv()
	if env.RedisAddr != "" {
		rt.store = redis.NewClient(&redis.Options{
			Addr:     env.RedisAddr,
			Password: env.RedisPassword,
			DB:       0,
		})
	}
	if env.NATSURL != "" {
		cli, err := natsbus.ConnectResilient(env.NATSURL, natsbus.DefaultReconnectConfig())
		if err != nil {
			slog.Warn("merge workflow: nats connect failed", "err", err)
		} else {
			rt.nats = cli
		}
	}
	return rt
}

func (r *mergeWorkflowRuntime) close() {
	if r.nats != nil {
		r.nats.Close()
	}
	if r.store != nil {
		_ = r.store.Close()
	}
}

func (r *mergeWorkflowRuntime) maybeStart(ctx context.Context, parentCellID string) {
	parentCellID = strings.TrimSpace(parentCellID)
	if !r.cfg.enabled || parentCellID == "" {
		return
	}
	if _, blocked := r.cfg.blockedParents[parentCellID]; blocked {
		slog.Info("merge workflow: blocked by config", "parent_cell_id", parentCellID, "env", "MMO_GRID_MERGE_WORKFLOW_BLOCKLIST")
		return
	}
	r.mu.Lock()
	if _, ok := r.active[parentCellID]; ok {
		r.mu.Unlock()
		mergeAutoWorkflowRunsTotal.WithLabelValues("skipped_reentry").Inc()
		slog.Info("merge workflow: skip duplicate run", "parent_cell_id", parentCellID)
		return
	}
	r.active[parentCellID] = struct{}{}
	r.mu.Unlock()
	go r.run(ctx, parentCellID)
}

func (r *mergeWorkflowRuntime) run(ctx context.Context, parentCellID string) {
	start := time.Now()
	defer func() {
		mergeAutoWorkflowDurationSeconds.Observe(time.Since(start).Seconds())
		r.mu.Lock()
		delete(r.active, parentCellID)
		r.mu.Unlock()
	}()
	r.publish(mergeWorkflowEvent{
		ParentCellID: parentCellID,
		Stage:        "detected",
		Message:      "auto merge workflow started",
		AtUnixMs:     time.Now().UnixMilli(),
	})
	backoff := r.cfg.initialBackoff
	var lastErr error
	for attempt := 1; attempt <= r.cfg.maxRetries; attempt++ {
		if err := r.runOnce(ctx, parentCellID, attempt); err == nil {
			mergeAutoWorkflowRunsTotal.WithLabelValues("ok").Inc()
			r.publish(mergeWorkflowEvent{
				ParentCellID: parentCellID,
				Stage:        splitcontrol.StageAutomationComplete,
				Attempt:      attempt,
				Message:      "merge workflow automation_complete",
				AtUnixMs:     time.Now().UnixMilli(),
				Successful:   true,
			})
			return
		} else {
			lastErr = err
			r.publish(mergeWorkflowEvent{
				ParentCellID: parentCellID,
				Stage:        "retrying",
				Attempt:      attempt,
				Message:      err.Error(),
				AtUnixMs:     time.Now().UnixMilli(),
			})
		}
		select {
		case <-ctx.Done():
			mergeAutoWorkflowRunsTotal.WithLabelValues("cancelled").Inc()
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > r.cfg.maxBackoff {
			backoff = r.cfg.maxBackoff
		}
	}
	mergeAutoWorkflowRunsTotal.WithLabelValues("failed").Inc()
	r.publish(mergeWorkflowEvent{
		ParentCellID: parentCellID,
		Stage:        "failed",
		Message:      fmt.Sprintf("workflow failed after retries: %v", lastErr),
		AtUnixMs:     time.Now().UnixMilli(),
	})
}

func (r *mergeWorkflowRuntime) runOnce(ctx context.Context, parentCellID string, attempt int) error {
	parent, childIDs, childrenCSV, err := r.planMergeGroup(ctx, parentCellID)
	if err != nil {
		return err
	}
	r.publish(mergeWorkflowEvent{
		ParentCellID: parentCellID,
		Stage:        "preflight",
		Attempt:      attempt,
		Message:      "validate child readiness and player guards",
		Children:     childrenCSV,
		AtUnixMs:     time.Now().UnixMilli(),
	})
	childPlayers, err := r.preflightGroup(ctx, parent, childIDs)
	if err != nil {
		return err
	}
	if r.cfg.playerHandoff && childPlayers > 0 {
		r.publish(mergeWorkflowEvent{
			ParentCellID: parentCellID,
			Stage:        "player_handoffs_running",
			Attempt:      attempt,
			Message:      fmt.Sprintf("player handoff enabled; expected players=%d", childPlayers),
			Children:     childrenCSV,
			AtUnixMs:     time.Now().UnixMilli(),
			Attrs:        map[string]string{"expected_player_count": fmt.Sprintf("%d", childPlayers)},
		})
	}
	if err := r.setSplitDrain(ctx, parentCellID, true); err != nil {
		return fmt.Errorf("set parent split_drain true: %w", err)
	}
	defer func() {
		_ = r.setSplitDrain(context.Background(), parentCellID, false)
	}()
	resp, err := r.forwardMerge(ctx, parentCellID, childIDs, fmt.Sprintf("auto-merge-attempt-%d", attempt))
	if err != nil {
		return err
	}
	if r.cfg.playerHandoff && childPlayers > 0 {
		r.publish(mergeWorkflowEvent{
			ParentCellID: parentCellID,
			Stage:        "player_handoffs_done",
			Attempt:      attempt,
			Message:      "player handoffs completed",
			Children:     childrenCSV,
			AtUnixMs:     time.Now().UnixMilli(),
			Successful:   true,
			Attrs: map[string]string{
				"expected_player_count": fmt.Sprintf("%d", childPlayers),
				"registry_message":      resp.GetMessage(),
			},
		})
	}
	r.publish(mergeWorkflowEvent{
		ParentCellID: parentCellID,
		Stage:        "handoff_done",
		Attempt:      attempt,
		Message:      "children exported and imported into parent",
		Children:     childrenCSV,
		AtUnixMs:     time.Now().UnixMilli(),
		Successful:   true,
	})
	if err := r.switchTopologyAndTeardown(ctx, parentCellID, childIDs); err != nil {
		return err
	}
	return r.saveAutomationState(ctx, parentCellID, childIDs, childPlayers)
}

func (r *mergeWorkflowRuntime) planMergeGroup(ctx context.Context, parentCellID string) (*cellv1.CellSpec, []string, string, error) {
	parent, ok, err := discovery.FindCellByID(ctx, r.cat, parentCellID)
	if err != nil {
		return nil, nil, "", err
	}
	if !ok || parent == nil || parent.GetBounds() == nil {
		return nil, nil, "", fmt.Errorf("parent not found or invalid: %s", parentCellID)
	}
	cells, err := r.cat.List(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	resolved, err := partition.CatalogMergeChildren(parent, cells)
	if err != nil {
		return nil, nil, "", err
	}
	childIDs := make([]string, 0, 4)
	for _, spec := range resolved {
		childIDs = append(childIDs, strings.TrimSpace(spec.GetId()))
	}
	return parent, childIDs, strings.Join(childIDs, ","), nil
}

func (r *mergeWorkflowRuntime) preflightGroup(ctx context.Context, parent *cellv1.CellSpec, childIDs []string) (int, error) {
	parentPing, err := pingCellStats(ctx, parent.GetGrpcEndpoint())
	if err != nil {
		return 0, fmt.Errorf("parent ping failed: %w", err)
	}
	if parentPing.players > 0 {
		return 0, fmt.Errorf("parent has active players: %d", parentPing.players)
	}
	childPlayers := 0
	for _, childID := range childIDs {
		spec, ok, err := discovery.FindCellByID(ctx, r.cat, childID)
		if err != nil {
			return 0, err
		}
		if !ok || spec == nil {
			return 0, fmt.Errorf("child missing: %s", childID)
		}
		ping, err := pingCellStats(ctx, spec.GetGrpcEndpoint())
		if err != nil {
			return 0, fmt.Errorf("child ping failed %s: %w", childID, err)
		}
		childPlayers += ping.players
		if !r.cfg.playerHandoff && ping.players > 0 {
			return 0, fmt.Errorf("child %s has active players: %d", childID, ping.players)
		}
	}
	if r.cfg.playerHandoff && r.cfg.maxPlayersMove > 0 && childPlayers > r.cfg.maxPlayersMove {
		return 0, fmt.Errorf("merge player handoff guard: child players %d > max %d", childPlayers, r.cfg.maxPlayersMove)
	}
	return childPlayers, nil
}

func (r *mergeWorkflowRuntime) forwardMerge(ctx context.Context, parentCellID string, childIDs []string, reason string) (*cellv1.ForwardMergeHandoffResponse, error) {
	conn, err := grpc.NewClient(r.cfg.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cl := cellv1.NewRegistryClient(conn)
	resp, err := cl.ForwardMergeHandoff(cctx, &cellv1.ForwardMergeHandoffRequest{
		ParentCellId: parentCellID,
		ChildCellIds: childIDs,
		Reason:       reason,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetOk() {
		return nil, fmt.Errorf("forward merge not ok: %s", resp.GetMessage())
	}
	return resp, nil
}

func (r *mergeWorkflowRuntime) switchTopologyAndTeardown(ctx context.Context, parentCellID string, childIDs []string) error {
	for _, childID := range childIDs {
		if err := discovery.DeregisterLogicalCell(ctx, r.cat, childID); err != nil {
			return fmt.Errorf("deregister child %s: %w", childID, err)
		}
	}
	if err := r.verifyResolveToParent(ctx, parentCellID); err != nil {
		return err
	}
	if r.nats != nil {
		for _, childID := range childIDs {
			req := splitcontrol.RuntimeCellDeleteRequest{
				Op:     splitcontrol.OpDeleteRuntimeChild,
				CellID: childID,
				Reason: "auto-merge topology switched",
			}
			raw, err := json.Marshal(req)
			if err != nil {
				continue
			}
			_ = r.nats.Publish(natsbus.SubjectCellControl, raw)
		}
		_ = r.nats.FlushTimeout(400 * time.Millisecond)
	}
	r.publish(mergeWorkflowEvent{
		ParentCellID: parentCellID,
		Stage:        splitcontrol.StageTopologySwitched,
		Message:      "children deregistered and teardown requested",
		Children:     strings.Join(childIDs, ","),
		AtUnixMs:     time.Now().UnixMilli(),
		Successful:   true,
	})
	return nil
}

func (r *mergeWorkflowRuntime) verifyResolveToParent(ctx context.Context, parentCellID string) error {
	parent, ok, err := discovery.FindCellByID(ctx, r.cat, parentCellID)
	if err != nil {
		return err
	}
	if !ok || parent == nil || parent.GetBounds() == nil {
		return fmt.Errorf("parent missing after topology switch")
	}
	children, err := partition.ChildSpecsForSplit(parentCellID, parent.GetBounds(), parent.GetLevel())
	if err != nil {
		return err
	}
	for _, ch := range children {
		b := ch.GetBounds()
		cx := (b.GetXMin() + b.GetXMax()) / 2
		cz := (b.GetZMin() + b.GetZMax()) / 2
		got, found, err := r.cat.ResolveMostSpecific(ctx, cx, cz)
		if err != nil {
			return err
		}
		if !found || got == nil {
			return fmt.Errorf("resolve miss at child center for %s", ch.GetId())
		}
		if got.GetId() != parentCellID {
			return fmt.Errorf("resolve mismatch at %s: got=%s want=%s", ch.GetId(), got.GetId(), parentCellID)
		}
	}
	return nil
}

func (r *mergeWorkflowRuntime) saveAutomationState(ctx context.Context, parentCellID string, childIDs []string, movedPlayers int) error {
	if r.store == nil {
		return nil
	}
	state := map[string]any{
		"phase":                   splitcontrol.RetireStatePhaseAutomationComplete,
		"parent_cell_id":          parentCellID,
		"removed_children":        childIDs,
		"topology_switched":       true,
		"runtime_teardown_queued": true,
		"player_handoff_enabled":  r.cfg.playerHandoff,
		"player_handoff_count":    movedPlayers,
		"at_unix_ms":              time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	key := "mmo:grid:merge:state:" + strings.TrimSpace(parentCellID)
	return r.store.Set(ctx, key, raw, maxWorkflowRedisTTL()).Err()
}

func (r *mergeWorkflowRuntime) setSplitDrain(ctx context.Context, cellID string, enabled bool) error {
	conn, err := grpc.NewClient(r.cfg.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cl := cellv1.NewRegistryClient(conn)
	_, err = cl.ForwardCellUpdate(cctx, &cellv1.ForwardCellUpdateRequest{
		CellId: cellID,
		Update: &cellv1.UpdateRequest{
			Payload: &cellv1.UpdateRequest_SetSplitDrain{
				SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: enabled},
			},
		},
	})
	return err
}

func (r *mergeWorkflowRuntime) publish(event mergeWorkflowEvent) {
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
	if err := r.nats.Publish(natsbus.SubjectGridMergeWorkflow, raw); err != nil {
		slog.Warn("merge workflow publish failed", "err", err)
		return
	}
	_ = r.nats.FlushTimeout(250 * time.Millisecond)
}

type pingStats struct {
	players  int
	entities int
}

func pingCellStats(ctx context.Context, endpoint string) (pingStats, error) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return pingStats{}, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cl := cellv1.NewCellClient(conn)
	resp, err := cl.Ping(cctx, &cellv1.PingRequest{ClientId: "grid-merge-workflow"})
	if err != nil {
		return pingStats{}, err
	}
	return pingStats{
		players:  int(resp.GetPlayerCount()),
		entities: int(resp.GetEntityCount()),
	}, nil
}
