package replic

import (
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/ecs"
)

// FlagEntityPlayer бит в EntityState.flags (приоритет игрока над NPC).
const FlagEntityPlayer uint32 = 1

// EntityState строит protobuf-состояние сущности (нужны Position; Health опционально).
func EntityState(w *ecs.World, e ecs.Entity, isPlayer bool) *gamev1.EntityState {
	p, ok := w.Position(e)
	if !ok {
		return nil
	}
	st := &gamev1.EntityState{
		EntityId: uint64(e),
		Position: &gamev1.Vec3F{X: float32(p.X), Y: float32(p.Y), Z: float32(p.Z)},
	}
	if isPlayer {
		st.Flags = FlagEntityPlayer
	}
	if h, ok := w.Health(e); ok && h.MaxHP > 0 {
		st.HealthPct = float32(h.HP / h.MaxHP)
	}
	return st
}

// BuildSnapshot полный снимок всех сущностей с позицией.
func BuildSnapshot(w *ecs.World, tick uint64, isPlayer func(ecs.Entity) bool) *gamev1.Snapshot {
	out := &gamev1.Snapshot{Tick: tick}
	for e := range w.Alive() {
		ip := false
		if isPlayer != nil {
			ip = isPlayer(e)
		}
		if st := EntityState(w, e, ip); st != nil {
			out.Entities = append(out.Entities, st)
		}
	}
	return out
}

// BuildDelta по списку изменившихся сущностей (after World.TakeDirtyEntities).
func BuildDelta(w *ecs.World, tick, fromTick uint64, changed []ecs.Entity, isPlayer func(ecs.Entity) bool) *gamev1.Delta {
	d := &gamev1.Delta{Tick: tick, FromTick: fromTick}
	for _, e := range changed {
		ip := false
		if isPlayer != nil {
			ip = isPlayer(e)
		}
		if st := EntityState(w, e, ip); st != nil {
			d.Changed = append(d.Changed, st)
		}
	}
	return d
}
