package aoi

import (
	"testing"

	"mmo/internal/ecs"
)

func TestSpatialGridRadius(t *testing.T) {
	w := ecs.NewWorld()
	g := NewSpatialGrid(50)
	a := w.CreateEntity()
	w.SetPosition(a, ecs.Position{X: 10, Z: 10})
	g.UpdateEntity(w, a)
	b := w.CreateEntity()
	w.SetPosition(b, ecs.Position{X: 100, Z: 100})
	g.UpdateEntity(w, b)

	q := g.QueryRadius(w, 10, 10, 5)
	if len(q) != 1 || q[0] != a {
		t.Fatalf("near got %v", q)
	}
	q2 := g.QueryRadius(w, 10, 10, 200)
	if len(q2) != 2 {
		t.Fatalf("wide got %d", len(q2))
	}
}

func TestSpatialGridMoveBucket(t *testing.T) {
	w := ecs.NewWorld()
	g := NewSpatialGrid(10)
	e := w.CreateEntity()
	w.SetPosition(e, ecs.Position{X: 0, Z: 0})
	g.UpdateEntity(w, e)
	w.SetPosition(e, ecs.Position{X: 100, Z: 0})
	g.UpdateEntity(w, e)
	if len(g.QueryRadius(w, 0, 0, 5)) != 0 {
		t.Fatal("old cell should be empty")
	}
	if len(g.QueryRadius(w, 100, 0, 5)) != 1 {
		t.Fatal("new cell")
	}
}

func TestNeighborCellKeys(t *testing.T) {
	k := NeighborCellKeys(0, 0, 1)
	if len(k) != 9 {
		t.Fatalf("neighbors 3x3 =9 got %d", len(k))
	}
}
