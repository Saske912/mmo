package main

import (
	"context"
	"testing"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/discovery"
	"mmo/internal/registry"
	"mmo/internal/splitcontrol"
)

func TestResolveChildCenterReasons_parentChildOverlap(t *testing.T) {
	ctx := context.Background()
	cat := discovery.NewMemoryCatalog(registry.NewMemory())
	parent := &cellv1.CellSpec{
		Id:           "cell_0_0_0",
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: "parent:50051",
	}
	child := &cellv1.CellSpec{
		Id:           "cell_-1_-1_1",
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
		ID:    "cell_-1_-1_1",
		Level: 1,
		XMin:  -1000, XMax: 0, ZMin: -1000, ZMax: 0,
	}}
	got := resolveChildCenterReasons(ctx, cat, specs)
	if len(got) != 0 {
		t.Fatalf("expected no resolve errors, got %v", got)
	}
}

func TestResolveChildCenterReasons_wrongWinner(t *testing.T) {
	ctx := context.Background()
	cat := discovery.NewMemoryCatalog(registry.NewMemory())
	parent := &cellv1.CellSpec{
		Id:           "cell_0_0_0",
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: "parent:50051",
	}
	if err := cat.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}

	specs := []splitcontrol.ChildCellSpec{{
		ID:    "cell_-1_-1_1",
		Level: 1,
		XMin:  -1000, XMax: 0, ZMin: -1000, ZMax: 0,
	}}
	got := resolveChildCenterReasons(ctx, cat, specs)
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
