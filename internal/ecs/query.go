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
