package snapshot

import (
	"testing"

	"mmo/internal/cellsim"
	"mmo/internal/ecs"
)

func TestRoundTripTickAndOneEntity(t *testing.T) {
	src := cellsim.NewRuntime()
	e := src.World.CreateEntity()
	src.World.SetPosition(e, ecs.Position{X: 1, Y: 2, Z: 3})
	src.World.SetVelocity(e, ecs.Velocity{VX: 0.5, VY: 0, VZ: -0.25})
	src.World.SetHealth(e, ecs.Health{HP: 80, MaxHP: 100})

	src.Loop.Stats.TickCount = 42
	src.Loop.TPS = 30

	p := Encode(src, nil)
	if p.Tick != 42 || p.LoopTps != 30 {
		t.Fatalf("encode meta: tick=%d tps=%f", p.Tick, p.LoopTps)
	}
	if len(p.Entities) != 1 {
		t.Fatalf("entities: %d", len(p.Entities))
	}

	dst := cellsim.NewRuntime()
	if err := Decode(dst.World, dst.Loop, p); err != nil {
		t.Fatal(err)
	}
	if dst.Loop.Stats.TickCount != 42 {
		t.Fatalf("tick: got %d", dst.Loop.Stats.TickCount)
	}
	if dst.Loop.TPS != 30 {
		t.Fatalf("tps: got %v", dst.Loop.TPS)
	}
	alive := 0
	var got ecs.Entity
	for e2 := range dst.World.Alive() {
		alive++
		got = e2
	}
	if alive != 1 {
		t.Fatalf("alive: %d", alive)
	}
	pos, ok := dst.World.Position(got)
	if !ok {
		t.Fatal("position")
	}
	if pos.X != 1 || pos.Y != 2 || pos.Z != 3 {
		t.Fatalf("pos %+v", pos)
	}
	vel, _ := dst.World.Velocity(got)
	if vel.VX != 0.5 || vel.VZ != -0.25 {
		t.Fatalf("vel %+v", vel)
	}
	h, ok := dst.World.Health(got)
	if !ok || h.HP != 80 || h.MaxHP != 100 {
		t.Fatalf("health %+v ok=%v", h, ok)
	}
}

func TestDecodeWrongSchema(t *testing.T) {
	r := cellsim.NewRuntime()
	p := Encode(r, nil)
	p.SchemaVersion = 99
	if err := Decode(r.World, r.Loop, p); err == nil {
		t.Fatal("want error")
	}
}
