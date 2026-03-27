package ecs

import (
	"testing"
	"time"
)

func TestGameLoopSteps(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	w.SetPosition(e, Position{X: 0, Z: 0})
	w.SetVelocity(e, Velocity{VX: 10, VY: 0, VZ: 0})
	loop := NewGameLoop(w, 30, MovementSystem{})
	const n = 60
	loop.RunSteps(n)
	if loop.Stats.TickCount != n {
		t.Fatalf("ticks %d", loop.Stats.TickCount)
	}
	p, _ := w.Position(e)
	wantX := 10.0 * float64(n) / 30.0
	if p.X < wantX-1e-6 || p.X > wantX+1e-6 {
		t.Fatalf("X=%v want %v", p.X, wantX)
	}
}

func TestGameLoopWorstTickBounded(t *testing.T) {
	w := NewWorld()
	loop := NewGameLoop(w, 60, MovementSystem{}, HealthRegenSystem{RegenPerSec: 1})
	const ticks = 2000
	start := time.Now()
	loop.RunSteps(ticks)
	elapsed := time.Since(start)
	if loop.Stats.TickCount != ticks {
		t.Fatal(loop.Stats.TickCount)
	}
	// Нет накопления логического долга в синхронном Step: wall идёт быстрее «игровых» 2000/60 сек
	if loop.Stats.WorstTickDur > 50*time.Millisecond {
		t.Fatalf("worst tick too slow: %v", loop.Stats.WorstTickDur)
	}
	// Симуляция 2000 шагов на desktop должна уложиться без «минутной» блокировки
	if elapsed > 2*time.Second {
		t.Fatalf("wall elapsed %v too long for %d steps", elapsed, ticks)
	}
}
