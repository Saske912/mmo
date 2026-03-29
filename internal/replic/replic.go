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
	ents := make([]ecs.Entity, 0, len(w.Alive()))
	for e := range w.Alive() {
		if _, ok := w.Position(e); ok {
			ents = append(ents, e)
		}
	}
	SortEntitiesReplicationPriority(ents, isPlayer)
	out := &gamev1.Snapshot{Tick: tick}
	for _, e := range ents {
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

// BuildSnapshotEntities снимок только перечисленных сущностей (AOI / interest management).
func BuildSnapshotEntities(w *ecs.World, tick uint64, entities []ecs.Entity, isPlayer func(ecs.Entity) bool) *gamev1.Snapshot {
	ordered := append([]ecs.Entity(nil), entities...)
	SortEntitiesReplicationPriority(ordered, isPlayer)
	out := &gamev1.Snapshot{Tick: tick}
	for _, e := range ordered {
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
	ordered := append([]ecs.Entity(nil), changed...)
	SortEntitiesReplicationPriority(ordered, isPlayer)
	d := &gamev1.Delta{Tick: tick, FromTick: fromTick}
	for _, e := range ordered {
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
