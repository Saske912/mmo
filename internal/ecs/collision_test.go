package ecs

import "testing"

func TestPhysicsCollisionSystemSeparatesTwoNPCs(t *testing.T) {
	w := NewWorld()
	left := w.CreateEntity()
	right := w.CreateEntity()

	w.SetPosition(left, Position{X: -1, Z: 0})
	w.SetVelocity(left, Velocity{VX: 2, VZ: 0})
	w.SetCollider(left, Collider{HalfX: 0.5, HalfY: 0.5, HalfZ: 0.5})

	w.SetPosition(right, Position{X: 1, Z: 0})
	w.SetVelocity(right, Velocity{VX: -2, VZ: 0})
	w.SetCollider(right, Collider{HalfX: 0.5, HalfY: 0.5, HalfZ: 0.5})

	loop := NewGameLoop(w, 20, MovementSystem{}, PhysicsCollisionSystem{})
	loop.RunSteps(20)

	lp, _ := w.Position(left)
	rp, _ := w.Position(right)
	minGap := 0.5 + 0.5
	if (rp.X - lp.X) < minGap-1e-9 {
		t.Fatalf("entities overlap after collision resolve: left=%v right=%v gap=%v", lp.X, rp.X, rp.X-lp.X)
	}
	if lp.X > rp.X {
		t.Fatalf("entities crossed each other: left=%v right=%v", lp.X, rp.X)
	}
}
