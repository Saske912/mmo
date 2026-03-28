package cellsvc

import (
	"mmo/internal/ecs"
	"mmo/internal/ecs/aoi"
)

// replicationAOIRadius метров XZ вокруг viewer (см. SpatialGrid.QueryRadius).
const replicationAOIRadius = 50.0

func visibleSetFromSlice(list []ecs.Entity) map[ecs.Entity]struct{} {
	m := make(map[ecs.Entity]struct{}, len(list))
	for _, e := range list {
		m[e] = struct{}{}
	}
	return m
}

// visibleEntitiesAOI возвращает сущности в круге (viewer всегда включается, если есть позиция).
// Индекс должен быть актуален: его обновляет ecs.NetworkReplicationSystem на каждом тике симуляции.
func visibleEntitiesAOI(w *ecs.World, grid *aoi.SpatialGrid, viewer ecs.Entity, radius float64) []ecs.Entity {
	if w == nil || grid == nil {
		return nil
	}
	p, ok := w.Position(viewer)
	if !ok {
		return nil
	}
	list := grid.QueryRadius(w, p.X, p.Z, radius)
	seen := make(map[ecs.Entity]struct{}, len(list)+1)
	for _, e := range list {
		seen[e] = struct{}{}
	}
	seen[viewer] = struct{}{}
	out := make([]ecs.Entity, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	return out
}
