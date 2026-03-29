package replic

import (
	"testing"

	"mmo/internal/ecs"
)

func TestBuildSnapshotPlayersBeforeNPCs(t *testing.T) {
	w := ecs.NewWorld()
	npc := w.CreateEntity()
	w.SetPosition(npc, ecs.Position{X: 1, Z: 0})
	w.SetHealth(npc, ecs.Health{HP: 10, MaxHP: 10})
	player := w.CreateEntity()
	w.SetPosition(player, ecs.Position{X: 2, Z: 0})
	w.SetHealth(player, ecs.Health{HP: 100, MaxHP: 100})

	isPlayer := func(e ecs.Entity) bool { return e == player }
	snap := BuildSnapshot(w, 1, isPlayer)
	if len(snap.Entities) != 2 {
		t.Fatalf("entities=%d", len(snap.Entities))
	}
	if snap.Entities[0].EntityId != uint64(player) {
		t.Fatalf("first entity want player %d got %d", player, snap.Entities[0].EntityId)
	}
	if snap.Entities[1].EntityId != uint64(npc) {
		t.Fatalf("second entity want npc %d got %d", npc, snap.Entities[1].EntityId)
	}
}

func TestBuildDeltaPlayersBeforeNPCs(t *testing.T) {
	w := ecs.NewWorld()
	npc := w.CreateEntity()
	w.SetPosition(npc, ecs.Position{X: 1, Z: 0})
	player := w.CreateEntity()
	w.SetPosition(player, ecs.Position{X: 2, Z: 0})
	isPlayer := func(e ecs.Entity) bool { return e == player }
	// Намеренно в обратном порядке в слайсе изменений
	changed := []ecs.Entity{npc, player}
	d := BuildDelta(w, 2, 1, changed, isPlayer)
	if len(d.Changed) != 2 {
		t.Fatal(len(d.Changed))
	}
	if d.Changed[0].EntityId != uint64(player) || d.Changed[1].EntityId != uint64(npc) {
		t.Fatalf("order: %+v", d.Changed)
	}
}
