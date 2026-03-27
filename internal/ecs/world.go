package ecs

// World контейнер сущностей и покомпонентное хранилище.
type World struct {
	nextID Entity
	alive  map[Entity]struct{}

	positions  map[Entity]Position
	velocities map[Entity]Velocity
	healths    map[Entity]Health
}

// NewWorld пустой мир.
func NewWorld() *World {
	return &World{
		alive:      make(map[Entity]struct{}),
		positions:  make(map[Entity]Position),
		velocities: make(map[Entity]Velocity),
		healths:    make(map[Entity]Health),
	}
}

// CreateEntity создаёт сущность без компонентов.
func (w *World) CreateEntity() Entity {
	w.nextID++
	e := w.nextID
	w.alive[e] = struct{}{}
	return e
}

// DestroyEntity удаляет сущность и все компоненты.
func (w *World) DestroyEntity(e Entity) {
	delete(w.alive, e)
	delete(w.positions, e)
	delete(w.velocities, e)
	delete(w.healths, e)
}

// Alive множество живых сущностей (копия ключей не делается — только для итерации из одного места).
func (w *World) Alive() map[Entity]struct{} {
	return w.alive
}

func (w *World) SetPosition(e Entity, p Position) {
	w.positions[e] = p
}

func (w *World) Position(e Entity) (Position, bool) {
	p, ok := w.positions[e]
	return p, ok
}

func (w *World) RemovePosition(e Entity) {
	delete(w.positions, e)
}

func (w *World) SetVelocity(e Entity, v Velocity) {
	w.velocities[e] = v
}

func (w *World) Velocity(e Entity) (Velocity, bool) {
	v, ok := w.velocities[e]
	return v, ok
}

func (w *World) RemoveVelocity(e Entity) {
	delete(w.velocities, e)
}

func (w *World) SetHealth(e Entity, h Health) {
	w.healths[e] = h
}

func (w *World) Health(e Entity) (Health, bool) {
	h, ok := w.healths[e]
	return h, ok
}

func (w *World) RemoveHealth(e Entity) {
	delete(w.healths, e)
}

// VisitMovement сущности с Position и Velocity.
func (w *World) VisitMovement(fn func(e Entity, pos Position, vel Velocity)) {
	for e := range w.alive {
		p, okP := w.positions[e]
		v, okV := w.velocities[e]
		if okP && okV {
			fn(e, p, v)
		}
	}
}

// VisitHealth сущности с Health.
func (w *World) VisitHealth(fn func(e Entity, h Health)) {
	for e := range w.alive {
		h, ok := w.healths[e]
		if ok {
			fn(e, h)
		}
	}
}

// EntityCount число живых сущностей.
func (w *World) EntityCount() int {
	return len(w.alive)
}
