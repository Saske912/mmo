package ecs

import "math"

// PhysicsCollisionSystem простой AABB-resolve в XZ плоскости.
// Используется как базовое «не проходить друг сквозь друга».
type PhysicsCollisionSystem struct{}

func (PhysicsCollisionSystem) Update(w *World, _ float64) {
	entities := QueryCollidable(w)
	for i := 0; i < len(entities); i++ {
		a := entities[i]
		pa, okPosA := w.Position(a)
		ca, okColA := w.Collider(a)
		if !okPosA || !okColA {
			continue
		}
		for j := i + 1; j < len(entities); j++ {
			b := entities[j]
			pb, okPosB := w.Position(b)
			cb, okColB := w.Collider(b)
			if !okPosB || !okColB {
				continue
			}

			dx := pb.X - pa.X
			dz := pb.Z - pa.Z
			overlapX := (ca.HalfX + cb.HalfX) - math.Abs(dx)
			overlapZ := (ca.HalfZ + cb.HalfZ) - math.Abs(dz)
			if overlapX <= 0 || overlapZ <= 0 {
				continue
			}

			// Выталкиваем по оси минимального проникновения.
			if overlapX <= overlapZ {
				dir := 1.0
				if dx < 0 {
					dir = -1.0
				} else if dx == 0 && a > b {
					// Детерминированность при полном совпадении.
					dir = -1.0
				}
				shift := overlapX * 0.5
				pa.X -= dir * shift
				pb.X += dir * shift
				w.SetPosition(a, pa)
				w.SetPosition(b, pb)
				zeroVelocityXOnImpact(w, a, b)
			} else {
				dir := 1.0
				if dz < 0 {
					dir = -1.0
				} else if dz == 0 && a > b {
					dir = -1.0
				}
				shift := overlapZ * 0.5
				pa.Z -= dir * shift
				pb.Z += dir * shift
				w.SetPosition(a, pa)
				w.SetPosition(b, pb)
				zeroVelocityZOnImpact(w, a, b)
			}
		}
	}
}

func zeroVelocityXOnImpact(w *World, a, b Entity) {
	va, okA := w.Velocity(a)
	vb, okB := w.Velocity(b)
	if okA {
		va.VX = 0
		w.SetVelocity(a, va)
	}
	if okB {
		vb.VX = 0
		w.SetVelocity(b, vb)
	}
}

func zeroVelocityZOnImpact(w *World, a, b Entity) {
	va, okA := w.Velocity(a)
	vb, okB := w.Velocity(b)
	if okA {
		va.VZ = 0
		w.SetVelocity(a, va)
	}
	if okB {
		vb.VZ = 0
		w.SetVelocity(b, vb)
	}
}
