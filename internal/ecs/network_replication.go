package ecs

// WorldSpatialIndex пространственный индекс для AOI/replication (реализуется *aoi.SpatialGrid).
type WorldSpatialIndex interface {
	RebuildFromWorld(w *World)
}

// NetworkReplicationSystem пересобирает индекс после остальных систем симуляции (движение, здоровье, …).
type NetworkReplicationSystem struct {
	Index WorldSpatialIndex
}

func (s NetworkReplicationSystem) Update(w *World, dt float64) {
	if s.Index != nil {
		s.Index.RebuildFromWorld(w)
	}
}
