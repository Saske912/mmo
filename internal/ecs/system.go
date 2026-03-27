package ecs

// System обновляет World за игровой такт (детерминированный шаг с фиксированным dt).
type System interface {
	Update(w *World, dt float64)
}
