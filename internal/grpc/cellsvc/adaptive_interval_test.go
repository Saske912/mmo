package cellsvc

import (
	"testing"

	"mmo/internal/ecs"
)

func TestPickAdaptiveReplicationInterval(t *testing.T) {
	w := ecs.NewWorld()
	viewer := w.CreateEntity()
	near := w.CreateEntity()
	far := w.CreateEntity()
	w.SetPosition(viewer, ecs.Position{X: 0, Z: 0})
	w.SetPosition(near, ecs.Position{X: 5, Z: 0})
	w.SetPosition(far, ecs.Position{X: 40, Z: 0})

	if got := pickAdaptiveReplicationInterval(w, viewer, []ecs.Entity{viewer, near, far}); got != replicationIntervalNear {
		t.Fatalf("near interval=%v", got)
	}
	if got := pickAdaptiveReplicationInterval(w, viewer, []ecs.Entity{viewer, far}); got != replicationIntervalDefault {
		t.Fatalf("default interval=%v", got)
	}
	if got := pickAdaptiveReplicationInterval(w, viewer, []ecs.Entity{viewer}); got != replicationIntervalFar {
		t.Fatalf("far interval=%v", got)
	}
}
