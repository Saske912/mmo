package main

import (
	"context"
	"testing"
	"time"
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
