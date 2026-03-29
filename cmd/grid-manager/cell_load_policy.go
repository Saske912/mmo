package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
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

type loadPolicyConfig struct {
	minBreachDuration time.Duration
	cooldown          time.Duration
	autoSplitDrain    bool
}

type cellPolicyState struct {
	breachSince time.Time
	lastAction  time.Time
}

type policySample struct {
	cellID     string
	endpoint   string
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
		natsSubj: natsbus.SubjectGridCommands,
		split:    newSplitWorkflowRuntime(cat),
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
