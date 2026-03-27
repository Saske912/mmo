package main

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"mmo/internal/cellsim"
	"mmo/internal/ecs"
	"mmo/internal/grpc/cellsvc"
)

func TestRedisPersistRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	rdb := openRedis(mr.Addr(), "")
	if rdb == nil {
		t.Fatal("openRedis")
	}
	defer rdb.Close()

	sim := cellsim.NewRuntime()
	e := sim.World.CreateEntity()
	sim.World.SetPosition(e, ecs.Position{X: 7, Y: 8, Z: 9})
	sim.World.SetVelocity(e, ecs.Velocity{VX: 1, VY: 0, VZ: -1})
	sim.Loop.Stats.TickCount = 99

	svc := &cellsvc.Server{CellID: "k8s-test", Sim: sim}
	ctx := context.Background()
	key := redisStateKey("k8s-test")

	savePersistedState(ctx, rdb, key, sim, svc)

	sim2 := cellsim.NewRuntime()
	if loadPersistedState(ctx, rdb, key, sim2) != true {
		t.Fatal("expected restore")
	}
	if sim2.Loop.Stats.TickCount != 99 {
		t.Fatalf("tick %d", sim2.Loop.Stats.TickCount)
	}
	n := 0
	for e2 := range sim2.World.Alive() {
		n++
		pos, _ := sim2.World.Position(e2)
		if pos.X != 7 || pos.Y != 8 || pos.Z != 9 {
			t.Fatalf("pos %+v", pos)
		}
		vel, _ := sim2.World.Velocity(e2)
		if vel.VX != 1 || vel.VZ != -1 {
			t.Fatalf("vel %+v", vel)
		}
	}
	if n != 1 {
		t.Fatalf("entities %d", n)
	}
}
