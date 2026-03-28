package ecs

// World контейнер сущностей и покомпонентное хранилище.
type World struct {
	nextID Entity
	alive  map[Entity]struct{}

	positions  map[Entity]Position
	velocities map[Entity]Velocity
	healths    map[Entity]Health
	colliders  map[Entity]Collider
	triggerZones   map[Entity]TriggerZone
	triggerSensors map[Entity]TriggerSensor

	dirty map[Entity]struct{} // для репликации (позиция/здоровье менялись)
}

// NewWorld пустой мир.
func NewWorld() *World {
	return &World{
		alive:      make(map[Entity]struct{}),
		positions:  make(map[Entity]Position),
		velocities: make(map[Entity]Velocity),
		healths:    make(map[Entity]Health),
		colliders:  make(map[Entity]Collider),
		triggerZones:   make(map[Entity]TriggerZone),
		triggerSensors: make(map[Entity]TriggerSensor),
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
	delete(w.colliders, e)
	delete(w.triggerZones, e)
	delete(w.triggerSensors, e)
	delete(w.dirty, e)
}

func (w *World) markDirty(e Entity) {
	if w.dirty == nil {
		w.dirty = make(map[Entity]struct{})
	}
	w.dirty[e] = struct{}{}
}

// Alive множество живых сущностей (копия ключей не делается — только для итерации из одного места).
func (w *World) Alive() map[Entity]struct{} {
	return w.alive
}

func (w *World) SetPosition(e Entity, p Position) {
	w.positions[e] = p
	w.markDirty(e)
}

func (w *World) Position(e Entity) (Position, bool) {
	p, ok := w.positions[e]
	return p, ok
}

func (w *World) RemovePosition(e Entity) {
	delete(w.positions, e)
	delete(w.dirty, e)
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
	w.markDirty(e)
}

func (w *World) Health(e Entity) (Health, bool) {
	h, ok := w.healths[e]
	return h, ok
}

func (w *World) RemoveHealth(e Entity) {
	delete(w.healths, e)
	delete(w.dirty, e)
}

func (w *World) SetCollider(e Entity, c Collider) {
	w.colliders[e] = c
}

func (w *World) Collider(e Entity) (Collider, bool) {
	c, ok := w.colliders[e]
	return c, ok
}

func (w *World) RemoveCollider(e Entity) {
	delete(w.colliders, e)
}

func (w *World) SetTriggerZone(e Entity, z TriggerZone) {
	w.triggerZones[e] = z
}

func (w *World) TriggerZone(e Entity) (TriggerZone, bool) {
	z, ok := w.triggerZones[e]
	return z, ok
}

func (w *World) RemoveTriggerZone(e Entity) {
	delete(w.triggerZones, e)
}

func (w *World) SetTriggerSensor(e Entity, s TriggerSensor) {
	w.triggerSensors[e] = s
}

func (w *World) TriggerSensor(e Entity) (TriggerSensor, bool) {
	s, ok := w.triggerSensors[e]
	return s, ok
}

func (w *World) RemoveTriggerSensor(e Entity) {
	delete(w.triggerSensors, e)
}

// TakeDirtyEntities возвращает сущности с несинхронизированными полями и очищает набор dirty.
func (w *World) TakeDirtyEntities() []Entity {
	if len(w.dirty) == 0 {
		return nil
	}
	out := make([]Entity, 0, len(w.dirty))
	for e := range w.dirty {
		out = append(out, e)
	}
	w.dirty = make(map[Entity]struct{})
	return out
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

// VisitCollidable сущности с Position и Collider.
func (w *World) VisitCollidable(fn func(e Entity, pos Position, col Collider)) {
	for e := range w.alive {
		p, okP := w.positions[e]
		c, okC := w.colliders[e]
		if okP && okC {
			fn(e, p, c)
		}
	}
}

// VisitTriggerZones сущности с Position и TriggerZone.
func (w *World) VisitTriggerZones(fn func(e Entity, pos Position, zone TriggerZone)) {
	for e := range w.alive {
		p, okP := w.positions[e]
		z, okZ := w.triggerZones[e]
		if okP && okZ {
			fn(e, p, z)
		}
	}
}

// VisitTriggerSensors сущности с Position и TriggerSensor.
func (w *World) VisitTriggerSensors(fn func(e Entity, pos Position, sensor TriggerSensor)) {
	for e := range w.alive {
		p, okP := w.positions[e]
		s, okS := w.triggerSensors[e]
		if okP && okS {
			fn(e, p, s)
		}
	}
}

// EntityCount число живых сущностей.
func (w *World) EntityCount() int {
	return len(w.alive)
}

// RestorePersistEntity восстанавливает сущность с явным id (персист). Поднимает nextID, чтобы CreateEntity не коллизил.
func (w *World) RestorePersistEntity(id Entity, pos Position, vel Velocity, h Health, hasHealth bool) {
	w.alive[id] = struct{}{}
	w.positions[id] = pos
	w.velocities[id] = vel
	if hasHealth {
		w.healths[id] = h
	} else {
		delete(w.healths, id)
	}
	if id >= w.nextID {
		w.nextID = id + 1
	}
}
