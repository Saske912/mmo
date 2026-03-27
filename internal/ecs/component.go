package ecs

// Component маркерный интерфейс для типов компонентов (чеклист Phase 0.2).
type Component interface {
	isComponent()
}

// Position XZ-плоскость соты; Y для будущей высоты.
type Position struct {
	X, Y, Z float64
}

func (Position) isComponent() {}

// Velocity м/с в мировых осях.
type Velocity struct {
	VX, VY, VZ float64
}

func (Velocity) isComponent() {}

// Health HP в рамках одной соты.
type Health struct {
	HP    float64
	MaxHP float64
}

func (Health) isComponent() {}
