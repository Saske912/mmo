package ecs

// Query — сущности, имеющие и Position, и Velocity (типичный запрос симуляции движения).
func QueryMovement(w *World) []Entity {
	out := make([]Entity, 0)
	for e := range w.alive {
		if _, okP := w.positions[e]; okP {
			if _, okV := w.velocities[e]; okV {
				out = append(out, e)
			}
		}
	}
	return out
}

// QueryHealth сущности с компонентом Health.
func QueryHealth(w *World) []Entity {
	out := make([]Entity, 0)
	for e := range w.alive {
		if _, ok := w.healths[e]; ok {
			out = append(out, e)
		}
	}
	return out
}

// QueryCollidable сущности с Position и Collider.
func QueryCollidable(w *World) []Entity {
	out := make([]Entity, 0)
	for e := range w.alive {
		if _, okP := w.positions[e]; okP {
			if _, okC := w.colliders[e]; okC {
				out = append(out, e)
			}
		}
	}
	return out
}

// QueryTriggerZones сущности с Position и TriggerZone.
func QueryTriggerZones(w *World) []Entity {
	out := make([]Entity, 0)
	for e := range w.alive {
		if _, okP := w.positions[e]; okP {
			if _, okZ := w.triggerZones[e]; okZ {
				out = append(out, e)
			}
		}
	}
	return out
}

// QueryTriggerSensors сущности с Position и TriggerSensor.
func QueryTriggerSensors(w *World) []Entity {
	out := make([]Entity, 0)
	for e := range w.alive {
		if _, okP := w.positions[e]; okP {
			if _, okS := w.triggerSensors[e]; okS {
				out = append(out, e)
			}
		}
	}
	return out
}
