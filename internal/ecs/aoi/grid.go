package aoi

import (
	"math"

	"mmo/internal/ecs"
)

// SpatialGrid равномерная сетка по XZ для AOI (ячейка cellSize мировых единиц).
type SpatialGrid struct {
	cellSize float64
	// ключ: координата ячейки (ix, iz)
	buckets map[[2]int][]ecs.Entity
	// последняя известная ячейка сущности (для удаления)
	entityCell map[ecs.Entity][2]int
}

// NewSpatialGrid cellSize > 0 (чеклист: ячейки 50x50).
func NewSpatialGrid(cellSize float64) *SpatialGrid {
	if cellSize <= 0 {
		cellSize = 50
	}
	return &SpatialGrid{
		cellSize:   cellSize,
		buckets:    make(map[[2]int][]ecs.Entity),
		entityCell: make(map[ecs.Entity][2]int),
	}
}

func (g *SpatialGrid) key(x, z float64) [2]int {
	return [2]int{int(math.Floor(x / g.cellSize)), int(math.Floor(z / g.cellSize))}
}

// UpdateEntity переставляет сущность в корзину по позиции; позиция должна соответствовать живой сущности.
func (g *SpatialGrid) UpdateEntity(w *ecs.World, e ecs.Entity) {
	p, ok := w.Position(e)
	if !ok {
		g.RemoveEntity(e)
		return
	}
	k := g.key(p.X, p.Z)
	if old, ok := g.entityCell[e]; ok && old == k {
		return
	}
	g.RemoveEntity(e)
	g.entityCell[e] = k
	g.buckets[k] = append(g.buckets[k], e)
}

// RemoveEntity убирает из индекса.
func (g *SpatialGrid) RemoveEntity(e ecs.Entity) {
	old, ok := g.entityCell[e]
	if !ok {
		return
	}
	delete(g.entityCell, e)
	sl := g.buckets[old]
	for i, id := range sl {
		if id == e {
			last := len(sl) - 1
			sl[i] = sl[last]
			g.buckets[old] = sl[:last]
			if len(g.buckets[old]) == 0 {
				delete(g.buckets, old)
			}
			return
		}
	}
}

// QueryRadius возвращает сущности в круге (cx, cz) радиуса r в XZ; требуют Position в World.
func (g *SpatialGrid) QueryRadius(w *ecs.World, cx, cz, r float64) []ecs.Entity {
	if r < 0 {
		return nil
	}
	r2 := r * r
	minIx := int(math.Floor((cx - r) / g.cellSize))
	maxIx := int(math.Floor((cx + r) / g.cellSize))
	minIz := int(math.Floor((cz - r) / g.cellSize))
	maxIz := int(math.Floor((cz + r) / g.cellSize))

	seen := make(map[ecs.Entity]struct{})
	out := make([]ecs.Entity, 0)
	for ix := minIx; ix <= maxIx; ix++ {
		for iz := minIz; iz <= maxIz; iz++ {
			for _, e := range g.buckets[[2]int{ix, iz}] {
				if _, ok := seen[e]; ok {
					continue
				}
				p, ok := w.Position(e)
				if !ok {
					continue
				}
				dx := p.X - cx
				dz := p.Z - cz
				if dx*dx+dz*dz <= r2 {
					seen[e] = struct{}{}
					out = append(out, e)
				}
			}
		}
	}
	return out
}

// NeighborCellKeys возвращает ключи ячеек вокруг позиции (радиус в ячейках 1 = соседи включая диагонали).
func NeighborCellKeys(ix, iz, ring int) [][2]int {
	if ring < 0 {
		ring = 0
	}
	out := make([][2]int, 0, (2*ring+1)*(2*ring+1))
	for dx := -ring; dx <= ring; dx++ {
		for dz := -ring; dz <= ring; dz++ {
			out = append(out, [2]int{ix + dx, iz + dz})
		}
	}
	return out
}
