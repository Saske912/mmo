package main

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	cellv1 "mmo/gen/cellv1"
)

func TestParseLoadPolicyConfigDefaults(t *testing.T) {
	t.Setenv("MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION", "")
	t.Setenv("MMO_GRID_LOAD_POLICY_COOLDOWN", "")
	t.Setenv("MMO_GRID_AUTO_SPLIT_DRAIN", "")
	cfg := parseLoadPolicyConfig()
	if cfg.minBreachDuration != 20*time.Second {
		t.Fatalf("min breach: %v", cfg.minBreachDuration)
	}
	if cfg.cooldown != 2*time.Minute {
		t.Fatalf("cooldown: %v", cfg.cooldown)
	}
	if cfg.autoSplitDrain {
		t.Fatal("auto split drain must be disabled by default")
	}
}

func TestLoadPolicyObserve_BreachAndRecovery(t *testing.T) {
	rt := &loadPolicyRuntime{
		cfg: loadPolicyConfig{
			minBreachDuration: 5 * time.Millisecond,
			cooldown:          20 * time.Millisecond,
			autoSplitDrain:    false,
		},
		state: make(map[string]cellPolicyState),
	}
	s := policySample{
		cellID:    "cell-a",
		reachable: true,
		players:   250,
		entities:  1000,
		tickSec:   0.2,
		violations: map[string]float64{
			"players": 1,
		},
	}
	rt.observe(context.Background(), s, 0)
	st := rt.state["cell-a"]
	if st.breachSince.IsZero() {
		t.Fatal("expected breachSince to be set")
	}
	if !st.lastAction.IsZero() {
		t.Fatal("must not emit action immediately")
	}
	time.Sleep(8 * time.Millisecond)
	rt.observe(context.Background(), s, 0)
	st = rt.state["cell-a"]
	if st.lastAction.IsZero() {
		t.Fatal("expected action after breach duration")
	}
	lastAction := st.lastAction
	// cooldown blocks repeated action.
	rt.observe(context.Background(), s, 0)
	if rt.state["cell-a"].lastAction != lastAction {
		t.Fatal("action must be throttled by cooldown")
	}
	// Recovery clears breach timestamp.
	rt.observe(context.Background(), policySample{cellID: "cell-a", reachable: true}, 1)
	if !rt.state["cell-a"].breachSince.IsZero() {
		t.Fatal("breach must be reset on recovery")
	}
}

type countingCellServer struct {
	cellv1.UnimplementedCellServer
	updateCalls atomic.Int32
}

func (s *countingCellServer) Update(_ context.Context, _ *cellv1.UpdateRequest) (*cellv1.UpdateResponse, error) {
	s.updateCalls.Add(1)
	return &cellv1.UpdateResponse{Ok: true, Message: "ok"}, nil
}

func startCountingCellServer(t *testing.T) (string, *countingCellServer) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	cell := &countingCellServer{}
	cellv1.RegisterCellServer(srv, cell)
	go func() {
		_ = srv.Serve(lis)
	}()
	time.Sleep(10 * time.Millisecond)
	t.Cleanup(func() {
		srv.GracefulStop()
		_ = lis.Close()
	})
	return lis.Addr().String(), cell
}

func TestLoadPolicyObserve_SkipsEarlyDrainWhenAutoSplitWorkflowEnabled(t *testing.T) {
	endpoint, cell := startCountingCellServer(t)
	rt := &loadPolicyRuntime{
		cfg: loadPolicyConfig{
			minBreachDuration: 0,
			cooldown:          0,
			autoSplitDrain:    true,
		},
		state: make(map[string]cellPolicyState),
		split: &splitWorkflowRuntime{
			cfg: splitWorkflowConfig{enabled: true},
		},
	}
	sample := policySample{
		cellID:    "",
		endpoint:  endpoint,
		reachable: true,
	}

	rt.observe(context.Background(), sample, 0)
	rt.observe(context.Background(), sample, 0)

	if got := cell.updateCalls.Load(); got != 0 {
		t.Fatalf("expected no early split_drain update when auto workflow is enabled, got %d calls", got)
	}
}

func TestLoadPolicyObserve_UsesEarlyDrainWhenAutoSplitWorkflowDisabled(t *testing.T) {
	endpoint, cell := startCountingCellServer(t)
	rt := &loadPolicyRuntime{
		cfg: loadPolicyConfig{
			minBreachDuration: 0,
			cooldown:          0,
			autoSplitDrain:    true,
		},
		state: make(map[string]cellPolicyState),
		split: &splitWorkflowRuntime{
			cfg: splitWorkflowConfig{enabled: false},
		},
	}
	sample := policySample{
		cellID:    "",
		endpoint:  endpoint,
		reachable: true,
	}

	rt.observe(context.Background(), sample, 0)
	rt.observe(context.Background(), sample, 0)

	if got := cell.updateCalls.Load(); got < 1 {
		t.Fatalf("expected split_drain update when auto workflow is disabled, got %d calls", got)
	}
}
