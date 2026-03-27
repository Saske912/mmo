package registry

import (
	"context"
	"testing"

	cellv1 "mmo/gen/cellv1"
)

func TestResolveMostSpecific_PrefersDeeperLevel(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	parent := &cellv1.CellSpec{
		Id: "p", Level: 0,
		Bounds: &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
	}
	child := &cellv1.CellSpec{
		Id: "c", Level: 1,
		Bounds: &cellv1.Bounds{XMin: -1000, XMax: 0, ZMin: -1000, ZMax: 0},
	}
	_ = m.Register(ctx, parent)
	_ = m.Register(ctx, child)

	got, ok := m.ResolveMostSpecific(ctx, -500, -500)
	if !ok || got.Id != "c" {
		t.Fatalf("expected child c, got %+v ok=%v", got, ok)
	}
}

func TestDeregister(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	_ = m.Register(ctx, &cellv1.CellSpec{
		Id: "x", Level: 0,
		Bounds: &cellv1.Bounds{XMin: 0, XMax: 1, ZMin: 0, ZMax: 1},
	})
	if err := m.Deregister(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	_, ok := m.ResolveMostSpecific(ctx, 0.5, 0.5)
	if ok {
		t.Fatal("expected no cell after deregister")
	}
}
