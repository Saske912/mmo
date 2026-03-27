package ecs

// MovementSystem обновляет Position по Velocity (фиксированный dt).
type MovementSystem struct{}

func (MovementSystem) Update(w *World, dt float64) {
	w.VisitMovement(func(e Entity, pos Position, vel Velocity) {
		pos.X += vel.VX * dt
		pos.Y += vel.VY * dt
		pos.Z += vel.VZ * dt
		w.SetPosition(e, pos)
	})
}
