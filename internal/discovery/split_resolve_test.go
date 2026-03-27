package discovery

import (
	"context"
	"testing"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/registry"
)

// B2: родитель level 0 и дочерняя сота SW-квадранта level 1 — Resolve выбирает больший level.
func TestResolveMostSpecific_childWinsInSWQuadrant(t *testing.T) {
	ctx := context.Background()
	cat := NewMemoryCatalog(registry.NewMemory())

	parent := &cellv1.CellSpec{
		Id:           "cell_0_0_0",
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: "parent:50051",
	}
	childSW := &cellv1.CellSpec{
		Id:           "cell_-1_-1_1",
		Level:        1,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 0, ZMin: -1000, ZMax: 0},
		GrpcEndpoint: "child-sw:50051",
	}
	if err := cat.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := cat.RegisterCell(ctx, childSW); err != nil {
		t.Fatal(err)
	}

	got, ok, err := cat.ResolveMostSpecific(ctx, -500, -500)
	if err != nil || !ok {
		t.Fatalf("resolve SW: ok=%v err=%v", ok, err)
	}
	if got.Id != "cell_-1_-1_1" || got.Level != 1 {
		t.Fatalf("SW quadrant: got id=%s level=%d want cell_-1_-1_1 level=1", got.Id, got.Level)
	}

	// NE квадрант покрывает только родитель (дочерняя не зарегистрирована).
	gotNE, okNE, err := cat.ResolveMostSpecific(ctx, 500, 500)
	if err != nil || !okNE {
		t.Fatalf("resolve NE: ok=%v err=%v", okNE, err)
	}
	if gotNE.Id != "cell_0_0_0" || gotNE.Level != 0 {
		t.Fatalf("NE quadrant: got id=%s level=%d want parent", gotNE.Id, gotNE.Level)
	}
}

func TestPickBestCell_sameLevelsAmbiguous(t *testing.T) {
	// Две соты одного level на границе mid: Contains закрытый — обе могут содержать (0,0).
	// PickBestCell берёт максимальный level; при равенстве оставляет первую попавшуюся
	// при обходе слайса — фиксируем детерминизм для документирования риска.
	a := &cellv1.CellSpec{
		Id: "a", Level: 1,
		Bounds: &cellv1.Bounds{XMin: -1000, XMax: 0, ZMin: -1000, ZMax: 0},
	}
	b := &cellv1.CellSpec{
		Id: "b", Level: 1,
		Bounds: &cellv1.Bounds{XMin: 0, XMax: 1000, ZMin: -1000, ZMax: 0},
	}
	cells := []*cellv1.CellSpec{a, b}
	_, ok := PickBestCell(cells, 0, -500)
	if !ok {
		t.Fatal("expected hit on edge x=0")
	}
	// Оба с level 1 содержат (0,-500)? a: x>=-1000 && x<=0 -> 0 ok. b: x>=0 && x<=1000 -> 0 ok.
	got, ok := PickBestCell(cells, 0, -500)
	if !ok || got == nil {
		t.Fatal("expected cell")
	}
	if got.Level != 1 {
		t.Fatalf("level %d", got.Level)
	}
}
