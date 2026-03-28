package cellsvc

import (
	"testing"

	"mmo/internal/ecs"
	"mmo/internal/ecs/aoi"
)

func TestVisibleEntitiesAOIExcludesDistantEntity(t *testing.T) {
	w := ecs.NewWorld()
	grid := aoi.NewSpatialGrid(aoi.ReplicationDefaultCellSize)
	viewer := w.CreateEntity()
	w.SetPosition(viewer, ecs.Position{X: 0, Z: 0})
	near := w.CreateEntity()
	w.SetPosition(near, ecs.Position{X: 10, Z: 0})
	far := w.CreateEntity()
	w.SetPosition(far, ecs.Position{X: 200, Z: 0})

	grid.RebuildFromWorld(w)
	vis := visibleEntitiesAOI(w, grid, viewer, replicationAOIRadius)
	seen := visibleSetFromSlice(vis)
	if _, ok := seen[far]; ok {
		t.Fatal("far entity must not be in AOI")
	}
	if _, ok := seen[near]; !ok {
		t.Fatal("near entity must be in AOI")
	}
	if _, ok := seen[viewer]; !ok {
		t.Fatal("viewer must always be included")
	}
}
