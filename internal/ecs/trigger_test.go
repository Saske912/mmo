package ecs

import "testing"

func TestTriggerSystemEnterExit(t *testing.T) {
	w := NewWorld()

	zone := w.CreateEntity()
	w.SetPosition(zone, Position{X: 0, Y: 0, Z: 0})
	w.SetTriggerZone(zone, TriggerZone{
		ID:    "safe_zone",
		HalfX: 1, HalfY: 1, HalfZ: 1,
	})

	player := w.CreateEntity()
	w.SetPosition(player, Position{X: -3, Y: 0, Z: 0})
	w.SetVelocity(player, Velocity{VX: 10, VY: 0, VZ: 0})
	w.SetTriggerSensor(player, TriggerSensor{})

	triggers := NewTriggerSystem()
	loop := NewGameLoop(w, 10, MovementSystem{}, triggers)

	seenEnter := false
	seenExit := false
	for i := 0; i < 8; i++ {
		loop.RunSteps(1)
		events := triggers.DrainEvents()
		for _, ev := range events {
			if ev.ZoneID != "safe_zone" || ev.Sensor != player {
				continue
			}
			if ev.Type == TriggerEnter {
				seenEnter = true
			}
			if ev.Type == TriggerExit {
				seenExit = true
			}
		}
	}

	if !seenEnter {
		t.Fatal("expected trigger enter event")
	}
	if !seenExit {
		t.Fatal("expected trigger exit event")
	}
}
