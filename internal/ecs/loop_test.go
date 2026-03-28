package ecs

import (
	"context"
	"errors"
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

func TestGameLoopPauseResume(t *testing.T) {
	w := NewWorld()
	loop := NewGameLoop(w, 120, MovementSystem{})
	loop.Pause()
	if !loop.IsPaused() {
		t.Fatal("loop should be paused")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := loop.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run error=%v", err)
	}
	if loop.Stats.TickCount != 0 {
		t.Fatalf("ticks while paused: %d", loop.Stats.TickCount)
	}

	loop.Resume()
	if loop.IsPaused() {
		t.Fatal("loop should be resumed")
	}
	loop.RunSteps(3)
	if loop.Stats.TickCount != 3 {
		t.Fatalf("ticks after resume: %d", loop.Stats.TickCount)
	}
}

func TestGameLoopRunRealTimeNoSignificantDrift(t *testing.T) {
	w := NewWorld()
	loop := NewGameLoop(w, 30, MovementSystem{}, HealthRegenSystem{RegenPerSec: 1})

	const runFor = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), runFor)
	defer cancel()
	start := time.Now()
	err := loop.Run(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run error=%v", err)
	}

	expected := int(elapsed.Seconds() * loop.TPS)
	got := int(loop.Stats.TickCount)
	// Допускаем небольшой дрейф таймера/планировщика ОС.
	if got < expected-3 || got > expected+3 {
		t.Fatalf("tick drift too large: got=%d expected~=%d elapsed=%v", got, expected, elapsed)
	}
	if loop.Stats.WorstTickDur > 50*time.Millisecond {
		t.Fatalf("worst tick too slow: %v", loop.Stats.WorstTickDur)
	}
}
