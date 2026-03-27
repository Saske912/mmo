package ecs

import (
	"context"
	"time"
)

// LoopStats простые метрики такта (чеклист: время тика, без Prometheus).
type LoopStats struct {
	TickCount      uint64
	LastTickDur    time.Duration
	WorstTickDur   time.Duration
	TotalWallDur   time.Duration // накопленная «настенная» длительность шага (для оценки lag)
}

// GameLoop фиксированный шаг 1/TPS секунд.
type GameLoop struct {
	TPS     float64
	World   *World
	Systems []System
	Stats   LoopStats
}

// NewGameLoop TPS по умолчанию 20.
func NewGameLoop(w *World, tps float64, systems ...System) *GameLoop {
	if tps <= 0 {
		tps = 20
	}
	return &GameLoop{
		TPS:     tps,
		World:   w,
		Systems: systems,
	}
}

// FixedDT секунды одного игрового шага.
func (g *GameLoop) FixedDT() float64 {
	return 1.0 / g.TPS
}

// Step один логический тик (блокирует до завершения всех систем).
func (g *GameLoop) Step() {
	dt := g.FixedDT()
	start := time.Now()
	for _, sys := range g.Systems {
		sys.Update(g.World, dt)
	}
	elapsed := time.Since(start)
	g.Stats.TickCount++
	g.Stats.LastTickDur = elapsed
	if elapsed > g.Stats.WorstTickDur {
		g.Stats.WorstTickDur = elapsed
	}
	g.Stats.TotalWallDur += elapsed
}

// Run блокируется до отмены ctx; шаги по реальному таймеру ~TPS.
func (g *GameLoop) Run(ctx context.Context) error {
	interval := time.Duration(float64(time.Second) / g.TPS)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			g.Step()
		}
	}
}

// RunSteps для тестов: выполнить count шагов подряд без ожидания wall-clock.
func (g *GameLoop) RunSteps(count int) {
	for i := 0; i < count; i++ {
		g.Step()
	}
}
