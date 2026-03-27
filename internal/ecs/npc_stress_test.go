package ecs

import (
	"math/rand"
	"testing"
)

// Сотни NPC движутся в разных направлениях (чеклист Phase 0.2).
func TestMovementManyNPCs(t *testing.T) {
	w := NewWorld()
	rng := rand.New(rand.NewSource(42))
	const n = 100
	for i := 0; i < n; i++ {
		e := w.CreateEntity()
		w.SetPosition(e, Position{X: rng.Float64()*100 - 50, Z: rng.Float64()*100 - 50})
		w.SetVelocity(e, Velocity{
			VX: rng.Float64()*2 - 1,
			VZ: rng.Float64()*2 - 1,
		})
		w.SetHealth(e, Health{HP: 100, MaxHP: 100})
	}
	ms := MovementSystem{}
	hr := HealthRegenSystem{RegenPerSec: 0.5}
	const steps = 120
	for i := 0; i < steps; i++ {
		dt := 1.0 / 25.0
		ms.Update(w, dt)
		hr.Update(w, dt)
	}
	if w.EntityCount() != n {
		t.Fatal(w.EntityCount())
	}
}
