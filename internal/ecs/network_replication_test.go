package ecs

import "testing"

type spySpatialIndex struct {
	rebuilds int
}

func (s *spySpatialIndex) RebuildFromWorld(_ *World) {
	s.rebuilds++
}

func TestNetworkReplicationSystemRebuildsIndex(t *testing.T) {
	w := NewWorld()
	spy := &spySpatialIndex{}
	sys := NetworkReplicationSystem{Index: spy}
	loop := NewGameLoop(w, 60, sys)
	loop.RunSteps(3)
	if spy.rebuilds != 3 {
		t.Fatalf("rebuilds=%d want 3", spy.rebuilds)
	}
}
