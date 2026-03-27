// Package snapshot — сериализация cell-node в Redis (tick, TPS, NPC; игроки не сохраняются).
package snapshot

import (
	"fmt"

	gamev1 "mmo/gen/gamev1"
	"mmo/internal/cellsim"
	"mmo/internal/ecs"
)

const SchemaVersion = 1

// Encode строит CellPersist: сущности без игроков (player_id мапа после рестарта пустая).
func Encode(r *cellsim.Runtime, skipEntity func(ecs.Entity) bool) *gamev1.CellPersist {
	if r == nil || r.World == nil || r.Loop == nil {
		return &gamev1.CellPersist{SchemaVersion: SchemaVersion}
	}
	p := &gamev1.CellPersist{
		SchemaVersion: SchemaVersion,
		Tick:            r.Loop.Stats.TickCount,
		LoopTps:         float32(r.Loop.TPS),
	}
	for e := range r.World.Alive() {
		if skipEntity != nil && skipEntity(e) {
			continue
		}
		pos, ok := r.World.Position(e)
		if !ok {
			continue
		}
		vel, _ := r.World.Velocity(e)
		ent := &gamev1.CellPersistEntity{
			EntityId: uint64(e),
			Position: &gamev1.Vec3F{X: float32(pos.X), Y: float32(pos.Y), Z: float32(pos.Z)},
			Velocity: &gamev1.Vec3F{X: float32(vel.VX), Y: float32(vel.VY), Z: float32(vel.VZ)},
			Flags:    0,
		}
		if h, okH := r.World.Health(e); okH && h.MaxHP > 0 {
			ent.HpCur = float32(h.HP)
			ent.HpMax = float32(h.MaxHP)
		}
		p.Entities = append(p.Entities, ent)
	}
	return p
}

// Decode восстанавливает мир и счётчик тика. loop.World должен совпадать с w.
func Decode(w *ecs.World, loop *ecs.GameLoop, p *gamev1.CellPersist) error {
	if p == nil || w == nil || loop == nil {
		return fmt.Errorf("nil persist or world or loop")
	}
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported persist schema %d", p.SchemaVersion)
	}
	if loop.World != w {
		return fmt.Errorf("loop world mismatch")
	}
	ids := make([]ecs.Entity, 0, len(w.Alive()))
	for e := range w.Alive() {
		ids = append(ids, e)
	}
	for _, e := range ids {
		w.DestroyEntity(e)
	}
	if p.LoopTps > 0 {
		loop.TPS = float64(p.LoopTps)
	}
	loop.Stats.TickCount = p.Tick

	for _, ent := range p.Entities {
		if ent == nil || ent.EntityId == 0 {
			continue
		}
		id := ecs.Entity(ent.EntityId)
		pos := ecs.Position{X: float64(ent.Position.GetX()), Y: float64(ent.Position.GetY()), Z: float64(ent.Position.GetZ())}
		vel := ecs.Velocity{
			VX: float64(ent.Velocity.GetX()),
			VY: float64(ent.Velocity.GetY()),
			VZ: float64(ent.Velocity.GetZ()),
		}
		var h ecs.Health
		hasHealth := ent.HpMax > 0
		if hasHealth {
			h = ecs.Health{HP: float64(ent.HpCur), MaxHP: float64(ent.HpMax)}
		}
		w.RestorePersistEntity(id, pos, vel, h, hasHealth)
	}
	return nil
}
