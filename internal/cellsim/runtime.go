package cellsim

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"mmo/internal/ecs"
)

const defaultTPS = 25

// Runtime игровой цикл соты: World + фиксированный тик и базовые системы.
type Runtime struct {
	Mu    sync.RWMutex // Step и чтение мира из gRPC
	World *ecs.World
	Loop  *ecs.GameLoop

	npcStarted atomic.Bool
	// OnTick вызывается после каждого Step (удобно для метрик); держится коротким.
	OnTick func()
}

// NewRuntime создаёт мир с Movement и HealthRegen.
func NewRuntime() *Runtime {
	w := ecs.NewWorld()
	loop := ecs.NewGameLoop(w, defaultTPS,
		ecs.MovementSystem{},
		ecs.HealthRegenSystem{RegenPerSec: 2},
	)
	return &Runtime{World: w, Loop: loop}
}

// Run блокирующий цикл до отмены ctx (шаг под Mu, чтобы SubscribeDeltas без гонок).
func (r *Runtime) Run(ctx context.Context) error {
	g := r.Loop
	interval := time.Duration(float64(time.Second) / g.TPS)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.Mu.Lock()
			g.Step()
			if r.OnTick != nil {
				r.OnTick()
			}
			r.Mu.Unlock()
		}
	}
}

// SpawnDemoNPCs добавляет n сущностей с позицией/скоростью/здоровьем (идемпотентно один раз).
func (r *Runtime) SpawnDemoNPCs(n int) {
	if n <= 0 {
		return
	}
	if !r.npcStarted.CompareAndSwap(false, true) {
		return
	}
	// Детерминированный «квадрат» без math/rand в проде hot path — линейная раскладка.
	step := 12.0
	i := 0
	for row := 0; i < n; row++ {
		for col := 0; col < 10 && i < n; col++ {
			e := r.World.CreateEntity()
			x := float64(col)*step - 5*step
			z := float64(row)*step - 5*step
			r.World.SetPosition(e, ecs.Position{X: x, Y: 0, Z: z})
			r.World.SetVelocity(e, ecs.Velocity{VX: 0.2, VY: 0, VZ: -0.15})
			r.World.SetHealth(e, ecs.Health{HP: 100, MaxHP: 100})
			i++
		}
	}
}
