package ecs

import "testing"

func TestWorldCreateDestroy(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	if e == 0 {
		t.Fatal("entity id")
	}
	if w.EntityCount() != 1 {
		t.Fatal("count")
	}
	w.SetPosition(e, Position{X: 1, Y: 0, Z: 2})
	w.SetVelocity(e, Velocity{VX: 1, VY: 0, VZ: 0})
	p, ok := w.Position(e)
	if !ok || p.X != 1 || p.Z != 2 {
		t.Fatalf("position %+v", p)
	}
	w.DestroyEntity(e)
	if w.EntityCount() != 0 {
		t.Fatal("after destroy")
	}
	if _, ok := w.Position(e); ok {
		t.Fatal("position should be gone")
	}
}

func TestQueryMovement(t *testing.T) {
	w := NewWorld()
	e1 := w.CreateEntity()
	w.SetPosition(e1, Position{})
	w.SetVelocity(e1, Velocity{VX: 1})
	e2 := w.CreateEntity()
	w.SetPosition(e2, Position{})
	qs := QueryMovement(w)
	if len(qs) != 1 {
		t.Fatalf("got %d want 1", len(qs))
	}
	if qs[0] != e1 {
		t.Fatal("wrong entity")
	}
}
