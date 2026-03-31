package main

import (
	"context"
	"testing"
)

func TestParseSplitWorkflowConfig_Guards(t *testing.T) {
	t.Setenv("MMO_GRID_AUTO_SPLIT_WORKFLOW", "true")
	t.Setenv("MMO_GRID_SPLIT_MAX_LEVEL", "2")
	t.Setenv("MMO_GRID_SPLIT_MAX_CONCURRENT_WORKFLOWS", "3")
	t.Setenv("MMO_GRID_SPLIT_WORKFLOW_BLOCKLIST", "cell_root, cell_q3 ,")

	cfg := parseSplitWorkflowConfig()
	if cfg.maxLevel != 2 {
		t.Fatalf("maxLevel=%d want 2", cfg.maxLevel)
	}
	if cfg.maxConcurrent != 3 {
		t.Fatalf("maxConcurrent=%d want 3", cfg.maxConcurrent)
	}
	if _, ok := cfg.blockedCells["cell_root"]; !ok {
		t.Fatal("blocked cell cell_root not parsed")
	}
	if _, ok := cfg.blockedCells["cell_q3"]; !ok {
		t.Fatal("blocked cell cell_q3 not parsed")
	}
}

func TestSplitWorkflowMaybeStart_BlockedCell(t *testing.T) {
	rt := &splitWorkflowRuntime{
		cfg: splitWorkflowConfig{
			enabled:      true,
			blockedCells: map[string]struct{}{"cell_root": {}},
		},
		active: make(map[string]struct{}),
	}
	rt.maybeStart(context.Background(), "cell_root")
	if len(rt.active) != 0 {
		t.Fatalf("active=%d want 0", len(rt.active))
	}
}

func TestSplitWorkflowMaybeStart_MaxLevel(t *testing.T) {
	rt := &splitWorkflowRuntime{
		cfg: splitWorkflowConfig{
			enabled:  true,
			maxLevel: 2,
		},
		active: make(map[string]struct{}),
	}
	rt.maybeStart(context.Background(), "cell_q1_q3")
	if len(rt.active) != 0 {
		t.Fatalf("active=%d want 0", len(rt.active))
	}
}

func TestSplitWorkflowMaybeStart_MaxConcurrent(t *testing.T) {
	rt := &splitWorkflowRuntime{
		cfg: splitWorkflowConfig{
			enabled:       true,
			maxConcurrent: 1,
		},
		active: map[string]struct{}{
			"cell_q0": {},
		},
	}
	rt.maybeStart(context.Background(), "cell_q3")
	if len(rt.active) != 1 {
		t.Fatalf("active=%d want 1", len(rt.active))
	}
	if _, ok := rt.active["cell_q3"]; ok {
		t.Fatal("second workflow must be rejected by maxConcurrent")
	}
}
