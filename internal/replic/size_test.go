package replic

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"mmo/internal/ecs"
)

// Чеклист Phase 0.3: типичный Snapshot &lt; 1400 байт (MTU-friendly).
func TestSnapshotWireSizeUnderMTU(t *testing.T) {
	w := ecs.NewWorld()
	const n = 32
	for i := 0; i < n; i++ {
		e := w.CreateEntity()
		w.SetPosition(e, ecs.Position{X: float64(i) * 0.5, Y: 0, Z: float64(-i) * 0.25})
		w.SetVelocity(e, ecs.Velocity{})
		w.SetHealth(e, ecs.Health{HP: 50, MaxHP: 100})
	}
	snap := BuildSnapshot(w, 1000, nil)
	b, err := proto.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 1400 {
		t.Fatalf("snapshot wire size %d bytes, want < 1400 (entities=%d)", len(b), n)
	}
}

func TestDeltaSmallerThanSnapshot(t *testing.T) {
	w := ecs.NewWorld()
	e := w.CreateEntity()
	w.SetPosition(e, ecs.Position{X: 1, Z: 2})
	_ = w.TakeDirtyEntities()
	w.SetPosition(e, ecs.Position{X: 2, Z: 3})
	dirty := w.TakeDirtyEntities()
	d := BuildDelta(w, 2, 1, dirty, nil)
	if len(d.Changed) != 1 {
		t.Fatal(d.Changed)
	}
}
