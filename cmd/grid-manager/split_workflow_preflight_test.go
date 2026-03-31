package main

import (
	"context"
	"testing"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/discovery"
	"mmo/internal/partition"
	"mmo/internal/registry"
	"mmo/internal/splitcontrol"
)

func TestResolveChildProbeReasons_parentChildOverlap(t *testing.T) {
	ctx := context.Background()
	cat := discovery.NewMemoryCatalog(registry.NewMemory())
	parent := &cellv1.CellSpec{
		Id:           partition.RootCellID(),
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: "parent:50051",
	}
	child := &cellv1.CellSpec{
		Id:           "cell_q0",
		Level:        1,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 0, ZMin: -1000, ZMax: 0},
		GrpcEndpoint: "child-sw:50051",
	}
	if err := cat.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterCell(ctx, child); err != nil {
		t.Fatal(err)
	}

	specs := []splitcontrol.ChildCellSpec{{
		ID:    "cell_q0",
		Level: 1,
		XMin:  -1000, XMax: 0, ZMin: -1000, ZMax: 0,
	}}
	cells, err := cat.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveChildProbeReasons(ctx, cat, specs, cells)
	if len(got) != 0 {
		t.Fatalf("expected no resolve errors, got %v", got)
	}
}

// Level-2 из «другой ветки» пересекает квадрант level-1 (не покрывает его целиком):
// прежняя одна точка у края часто попадала под чужой shard; перебор проб находит зону только под целевым L1.
func TestResolveChildProbeReasons_avoidsForeignDeeperOverlap(t *testing.T) {
	ctx := context.Background()
	cat := discovery.NewMemoryCatalog(registry.NewMemory())
	cells := []*cellv1.CellSpec{
		{
			Id:           partition.RootCellID(),
			Level:        0,
			Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
			GrpcEndpoint: "r0:50051",
		},
		{
			Id:           "cell_q1",
			Level:        1,
			Bounds:       &cellv1.Bounds{XMin: 0, XMax: 1000, ZMin: -1000, ZMax: 0},
			GrpcEndpoint: "r1:50051",
		},
		{
			Id:     "cell_q0_q3",
			Level:  2,
			Bounds: &cellv1.Bounds{XMin: 50, XMax: 450, ZMin: -450, ZMax: -150},
			// Пересечение с квадрантом cell_q1; id как «чужой» deeper level.
			GrpcEndpoint: "deep:50051",
		},
	}
	for _, c := range cells {
		if err := cat.RegisterCell(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	specs := []splitcontrol.ChildCellSpec{{
		ID:    "cell_q1",
		Level: 1,
		XMin:  0, XMax: 1000, ZMin: -1000, ZMax: 0,
	}}
	listed, err := cat.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveChildProbeReasons(ctx, cat, specs, listed)
	if len(got) != 0 {
		t.Fatalf("expected no resolve errors, got %v", got)
	}
}

// Та же ветка (1,-1): точка внутри L2 — Resolve возвращает более глубокий id, это допустимо.
func TestResolveChildProbeReasons_sameBranchDeeperWinnerOK(t *testing.T) {
	ctx := context.Background()
	cat := discovery.NewMemoryCatalog(registry.NewMemory())
	for _, c := range []*cellv1.CellSpec{
		{
			Id:           partition.RootCellID(),
			Level:        0,
			Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
			GrpcEndpoint: "r0:50051",
		},
		{
			Id:           "cell_q1",
			Level:        1,
			Bounds:       &cellv1.Bounds{XMin: 0, XMax: 1000, ZMin: -1000, ZMax: 0},
			GrpcEndpoint: "r1:50051",
		},
		{
			Id:           "cell_q1_q1",
			Level:        2,
			Bounds:       &cellv1.Bounds{XMin: 500, XMax: 1000, ZMin: -500, ZMax: 0},
			GrpcEndpoint: "r2:50051",
		},
	} {
		if err := cat.RegisterCell(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	specs := []splitcontrol.ChildCellSpec{{
		ID:    "cell_q1",
		Level: 1,
		XMin:  0, XMax: 1000, ZMin: -1000, ZMax: 0,
	}}
	listed, err := cat.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveChildProbeReasons(ctx, cat, specs, listed)
	if len(got) != 0 {
		t.Fatalf("expected no resolve errors, got %v", got)
	}
}

func TestResolveChildProbeReasons_wrongWinner(t *testing.T) {
	ctx := context.Background()
	cat := discovery.NewMemoryCatalog(registry.NewMemory())
	parent := &cellv1.CellSpec{
		Id:           partition.RootCellID(),
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: "parent:50051",
	}
	if err := cat.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}

	specs := []splitcontrol.ChildCellSpec{{
		ID:    "cell_q0",
		Level: 1,
		XMin:  -1000, XMax: 0, ZMin: -1000, ZMax: 0,
	}}
	cells, err := cat.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveChildProbeReasons(ctx, cat, specs, cells)
	if len(got) == 0 {
		t.Fatal("expected resolve error when child missing from catalog")
	}
}

func TestDedupeReasons(t *testing.T) {
	in := []string{"a", "a", "", "b"}
	out := dedupeReasons(in)
	if len(out) != 2 || out[0] != "a" || out[1] != "b" {
		t.Fatalf("got %v", out)
	}
}
