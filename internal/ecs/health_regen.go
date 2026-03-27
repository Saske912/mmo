package ecs

// HealthRegenSystem восстанавливает HP до MaxHP с фиксированной скоростью (единиц HP в секунду).
type HealthRegenSystem struct {
	RegenPerSec float64
}

func (s HealthRegenSystem) Update(w *World, dt float64) {
	if s.RegenPerSec <= 0 {
		return
	}
	w.VisitHealth(func(e Entity, h Health) {
		if h.HP >= h.MaxHP {
			return
		}
		h.HP += s.RegenPerSec * dt
		if h.HP > h.MaxHP {
			h.HP = h.MaxHP
		}
		w.SetHealth(e, h)
	})
}
