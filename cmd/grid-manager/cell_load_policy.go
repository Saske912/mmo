package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/config"
	"mmo/internal/discovery"
	"mmo/internal/partition"
)

const (
	loadPolicyActionLogOnly    = "log_only"
	loadPolicyActionSplitDrain = "split_drain_enable"
	loadPolicyResultOK         = "ok"
	loadPolicyResultSkip       = "skip"
	loadPolicyResultErr        = "error"
)

var loadPolicyActionsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "grid_manager",
		Name:      "load_policy_actions_total",
		Help:      "Total load-policy actions emitted by grid-manager",
	},
	[]string{"action", "cell_id", "result"},
)

var mergePolicyDecisionsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "grid_manager",
		Name:      "merge_policy_decisions_total",
		Help:      "Total merge policy decisions emitted by grid-manager",
	},
	[]string{"decision", "parent_cell_id", "reason"},
)

type loadPolicyConfig struct {
	minBreachDuration      time.Duration
	cooldown               time.Duration
	autoSplitDrain         bool
	autoMergeWorkflow      bool
	mergePlayerHandoff     bool
	mergeLowLoadFor        time.Duration
	mergeCooldown          time.Duration
	mergeMaxPlayers        float64
	mergeHandoffMaxPlayers float64
	mergeMaxEntities       float64
	mergeMaxTickSec        float64
}

type cellPolicyState struct {
	breachSince time.Time
	lastAction  time.Time
}

type policySample struct {
	cellID     string
	endpoint   string
	level      int32
	reachable  bool
	players    float64
	entities   float64
	tickSec    float64
	violations map[string]float64
}

type loadPolicyEvent struct {
	CellID            string             `json:"cell_id"`
	Action            string             `json:"action"`
	Reachable         bool               `json:"reachable"`
	Players           float64            `json:"players"`
	Entities          float64            `json:"entities"`
	TickSeconds       float64            `json:"tick_seconds"`
	Violations        map[string]float64 `json:"violations"`
	MinBreachDuration string             `json:"min_breach_duration"`
	Cooldown          string             `json:"cooldown"`
	AtUnixMs          int64              `json:"at_unix_ms"`
}

type loadPolicyRuntime struct {
	cfg      loadPolicyConfig
	state    map[string]cellPolicyState
	merge    *mergeWorkflowRuntime
	mgState  map[string]cellPolicyState
	nats     *natsbus.Client
	natsSubj string
	split    *splitWorkflowRuntime
}

func parseLoadPolicyConfig() loadPolicyConfig {
	cfg := loadPolicyConfig{
		minBreachDuration: 20 * time.Second,
		cooldown:          2 * time.Minute,
	}
	if s := strings.TrimSpace(os.Getenv("MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION")); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d >= 0 {
			cfg.minBreachDuration = d
		}
	}
	if s := strings.TrimSpace(os.Getenv("MMO_GRID_LOAD_POLICY_COOLDOWN")); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d >= 0 {
			cfg.cooldown = d
		}
	}
	cfg.autoSplitDrain = envBool("MMO_GRID_AUTO_SPLIT_DRAIN")
	cfg.autoMergeWorkflow = envBool("MMO_GRID_AUTO_MERGE_WORKFLOW")
	cfg.mergePlayerHandoff = envBool("MMO_GRID_MERGE_PLAYER_HANDOFF")
	cfg.mergeLowLoadFor = parseDurationWithDefault(os.Getenv("MMO_GRID_MERGE_MIN_LOW_LOAD_DURATION"), 3*time.Minute)
	cfg.mergeCooldown = parseDurationWithDefault(os.Getenv("MMO_GRID_MERGE_COOLDOWN"), 6*time.Minute)
	cfg.mergeMaxPlayers = parseFloat64WithDefault(os.Getenv("MMO_GRID_MERGE_THRESHOLD_MAX_PLAYERS"), 0)
	cfg.mergeHandoffMaxPlayers = parseFloat64WithDefault(os.Getenv("MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS"), 32)
	cfg.mergeMaxEntities = parseFloat64WithDefault(os.Getenv("MMO_GRID_MERGE_THRESHOLD_MAX_ENTITIES"), 300)
	cfg.mergeMaxTickSec = parseFloat64WithDefault(os.Getenv("MMO_GRID_MERGE_THRESHOLD_MAX_TICK_SECONDS"), 0.01)
	return cfg
}

func envBool(key string) bool {
	s := strings.TrimSpace(os.Getenv(key))
	return strings.EqualFold(s, "1") || strings.EqualFold(s, "true") || strings.EqualFold(s, "yes")
}

func newLoadPolicyRuntime(cat discovery.Catalog) *loadPolicyRuntime {
	cfg := parseLoadPolicyConfig()
	rt := &loadPolicyRuntime{
		cfg:      cfg,
		state:    make(map[string]cellPolicyState),
		mgState:  make(map[string]cellPolicyState),
		natsSubj: natsbus.SubjectGridCommands,
		split:    newSplitWorkflowRuntime(cat),
		merge:    newMergeWorkflowRuntime(cat),
	}
	if subj := strings.TrimSpace(os.Getenv("MMO_GRID_POLICY_NATS_SUBJECT")); subj != "" {
		rt.natsSubj = subj
	}
	natsURL := config.FromEnv().NATSURL
	if natsURL != "" {
		cli, err := natsbus.ConnectResilient(natsURL, natsbus.DefaultReconnectConfig())
		if err != nil {
			log.Printf("load policy: nats connect failed: %v", err)
		} else {
			rt.nats = cli
		}
	}
	return rt
}

func (r *loadPolicyRuntime) close() {
	if r.nats != nil {
		r.nats.Close()
	}
	if r.split != nil {
		r.split.close()
	}
	if r.merge != nil {
		r.merge.close()
	}
}

func (r *loadPolicyRuntime) forget(cellID string) {
	delete(r.state, cellID)
}

func (r *loadPolicyRuntime) observe(ctx context.Context, sample policySample, within float64) {
	now := time.Now()
	st := r.state[sample.cellID]
	if within >= 1 {
		if !st.breachSince.IsZero() {
			slog.Info("grid load recovered",
				"cell_id", sample.cellID,
				"reachable", sample.reachable,
				"players", sample.players,
				"entities", sample.entities,
				"tick_seconds", sample.tickSec,
			)
		}
		st.breachSince = time.Time{}
		r.state[sample.cellID] = st
		return
	}
	if st.breachSince.IsZero() {
		st.breachSince = now
		r.state[sample.cellID] = st
		return
	}
	if now.Sub(st.breachSince) < r.cfg.minBreachDuration {
		r.state[sample.cellID] = st
		return
	}
	if !st.lastAction.IsZero() && now.Sub(st.lastAction) < r.cfg.cooldown {
		r.state[sample.cellID] = st
		return
	}

	action := loadPolicyActionLogOnly
	result := loadPolicyResultOK
	if r.cfg.autoSplitDrain && sample.reachable {
		action = loadPolicyActionSplitDrain
		if err := setCellSplitDrain(ctx, sample.endpoint, true); err != nil {
			result = loadPolicyResultErr
			slog.Error("grid load policy split_drain failed",
				"cell_id", sample.cellID,
				"endpoint", sample.endpoint,
				"err", err,
			)
		}
	} else if !sample.reachable {
		result = loadPolicyResultSkip
	}
	if action == loadPolicyActionSplitDrain && result == loadPolicyResultOK && r.split != nil {
		r.split.maybeStart(ctx, sample.cellID)
	}

	slog.Warn("grid load policy action",
		"cell_id", sample.cellID,
		"action", action,
		"result", result,
		"reachable", sample.reachable,
		"players", sample.players,
		"entities", sample.entities,
		"tick_seconds", sample.tickSec,
		"violations", violationKinds(sample.violations),
		"auto_split_drain", r.cfg.autoSplitDrain,
	)
	loadPolicyActionsTotal.WithLabelValues(action, sample.cellID, result).Inc()
	r.publish(ctx, loadPolicyEvent{
		CellID:            sample.cellID,
		Action:            action,
		Reachable:         sample.reachable,
		Players:           sample.players,
		Entities:          sample.entities,
		TickSeconds:       sample.tickSec,
		Violations:        sample.violations,
		MinBreachDuration: r.cfg.minBreachDuration.String(),
		Cooldown:          r.cfg.cooldown.String(),
		AtUnixMs:          now.UnixMilli(),
	})
	st.lastAction = now
	r.state[sample.cellID] = st
}

func (r *loadPolicyRuntime) observeMergeCandidates(ctx context.Context, cells []*cellv1.CellSpec, samples map[string]policySample) {
	if !r.cfg.autoMergeWorkflow || r.merge == nil {
		return
	}
	now := time.Now()
	seenParent := make(map[string]struct{})
	specByID := make(map[string]*cellv1.CellSpec, len(cells))
	for _, c := range cells {
		if c == nil {
			continue
		}
		specByID[strings.TrimSpace(c.GetId())] = c
	}
	for _, parent := range cells {
		if parent == nil || parent.GetBounds() == nil {
			continue
		}
		parentID := strings.TrimSpace(parent.GetId())
		if parentID == "" {
			continue
		}
		seenParent[parentID] = struct{}{}
		children, err := partition.CatalogMergeChildren(parent, cells)
		if err != nil {
			st := r.mgState[parentID]
			st.breachSince = time.Time{}
			r.mgState[parentID] = st
			mergePolicyDecisionsTotal.WithLabelValues("skip", parentID, "catalog_merge_children").Inc()
			continue
		}
		low, reason, childrenCSV, aggPlayers, aggEntities := r.mergeGroupLowLoad(cells, specByID, samples, parent.GetLevel(), children)
		st := r.mgState[parentID]
		if !low {
			st.breachSince = time.Time{}
			r.mgState[parentID] = st
			mergePolicyDecisionsTotal.WithLabelValues("skip", parentID, reason).Inc()
			continue
		}
		if st.breachSince.IsZero() {
			st.breachSince = now
			r.mgState[parentID] = st
			mergePolicyDecisionsTotal.WithLabelValues("candidate", parentID, "low_load_started").Inc()
			continue
		}
		if now.Sub(st.breachSince) < r.cfg.mergeLowLoadFor {
			r.mgState[parentID] = st
			mergePolicyDecisionsTotal.WithLabelValues("candidate", parentID, "low_load_window").Inc()
			continue
		}
		if !st.lastAction.IsZero() && now.Sub(st.lastAction) < r.cfg.mergeCooldown {
			r.mgState[parentID] = st
			mergePolicyDecisionsTotal.WithLabelValues("skip", parentID, "cooldown").Inc()
			continue
		}
		r.merge.maybeStart(ctx, parentID)
		mergePolicyDecisionsTotal.WithLabelValues("start", parentID, "ok").Inc()
		r.publish(ctx, loadPolicyEvent{
			CellID:            parentID,
			Action:            "merge_start",
			Reachable:         true,
			Players:           aggPlayers,
			Entities:          aggEntities,
			TickSeconds:       0,
			Violations:        map[string]float64{"unreachable": 0},
			MinBreachDuration: r.cfg.mergeLowLoadFor.String(),
			Cooldown:          r.cfg.mergeCooldown.String(),
			AtUnixMs:          now.UnixMilli(),
		})
		slog.Info("grid merge policy action",
			"parent_cell_id", parentID,
			"children", childrenCSV,
			"players_total", aggPlayers,
			"entities_total", aggEntities,
		)
		st.lastAction = now
		st.breachSince = time.Time{}
		r.mgState[parentID] = st
	}
	for parentID := range r.mgState {
		if _, ok := seenParent[parentID]; !ok {
			delete(r.mgState, parentID)
		}
	}
}

func (r *loadPolicyRuntime) mergeGroupLowLoad(
	cells []*cellv1.CellSpec,
	specByID map[string]*cellv1.CellSpec,
	samples map[string]policySample,
	parentLevel int32,
	children []*cellv1.CellSpec,
) (ok bool, reason string, childrenCSV string, playersTotal, entitiesTotal float64) {
	ids := make([]string, 0, len(children))
	childLevel := parentLevel + 1
	for _, ch := range children {
		id := strings.TrimSpace(ch.GetId())
		if id == "" {
			return false, "child_id_empty", "", 0, 0
		}
		ids = append(ids, id)
		spec := specByID[id]
		if spec == nil || spec.GetBounds() == nil {
			return false, "child_missing", strings.Join(ids, ","), 0, 0
		}
		if spec.GetLevel() != childLevel {
			return false, "child_level_mismatch", strings.Join(ids, ","), 0, 0
		}
		smp, has := samples[id]
		if !has || !smp.reachable {
			return false, "child_unreachable", strings.Join(ids, ","), 0, 0
		}
		if !r.cfg.mergePlayerHandoff {
			if r.cfg.mergeMaxPlayers >= 0 && smp.players > r.cfg.mergeMaxPlayers {
				return false, "players_high", strings.Join(ids, ","), 0, 0
			}
		} else if r.cfg.mergeHandoffMaxPlayers >= 0 && smp.players > r.cfg.mergeHandoffMaxPlayers {
			return false, "players_high_for_handoff", strings.Join(ids, ","), 0, 0
		}
		if r.cfg.mergeMaxEntities >= 0 && smp.entities > r.cfg.mergeMaxEntities {
			return false, "entities_high", strings.Join(ids, ","), 0, 0
		}
		if r.cfg.mergeMaxTickSec >= 0 && smp.tickSec > r.cfg.mergeMaxTickSec {
			return false, "tick_high", strings.Join(ids, ","), 0, 0
		}
		playersTotal += smp.players
		entitiesTotal += smp.entities
	}
	if r.cfg.mergePlayerHandoff && r.cfg.mergeHandoffMaxPlayers >= 0 && playersTotal > r.cfg.mergeHandoffMaxPlayers {
		return false, "players_total_high_for_handoff", strings.Join(ids, ","), 0, 0
	}
	for _, c := range cells {
		if c == nil {
			continue
		}
		id := strings.TrimSpace(c.GetId())
		lv, ok := partition.CellPathLevel(id)
		if !ok || lv <= childLevel {
			continue
		}
		for _, childID := range ids {
			cl, childOK := partition.CellPathLevel(childID)
			if childOK && cl == childLevel && partition.IsDescendantPath(childID, id) {
				return false, "child_has_descendants", strings.Join(ids, ","), 0, 0
			}
		}
	}
	return true, "ok", strings.Join(ids, ","), playersTotal, entitiesTotal
}

func (r *loadPolicyRuntime) publish(_ context.Context, event loadPolicyEvent) {
	if r.nats == nil {
		return
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	if err := r.nats.Publish(r.natsSubj, raw); err != nil {
		slog.Warn("grid load policy publish failed", "subject", r.natsSubj, "err", err)
		return
	}
	_ = r.nats.FlushTimeout(300 * time.Millisecond)
}

func parseFloat64WithDefault(raw string, fallback float64) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func setCellSplitDrain(ctx context.Context, endpoint string, enabled bool) error {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	updCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	cl := cellv1.NewCellClient(conn)
	_, err = cl.Update(updCtx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetSplitDrain{
			SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: enabled},
		},
	})
	return err
}

func violationKinds(v map[string]float64) string {
	if len(v) == 0 {
		return ""
	}
	out := make([]string, 0, len(v))
	for _, k := range cellThresholdViolationKinds {
		if v[k] > 0 {
			out = append(out, k)
		}
	}
	return strings.Join(out, ",")
}
