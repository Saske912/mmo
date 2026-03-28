package ecs

import "math"

type triggerPair struct {
	zone   Entity
	sensor Entity
}

// TriggerEventType тип события триггера.
type TriggerEventType string

const (
	TriggerEnter TriggerEventType = "enter"
	TriggerExit  TriggerEventType = "exit"
)

// TriggerEvent событие входа/выхода сенсора в зону.
type TriggerEvent struct {
	Type     TriggerEventType
	Zone     Entity
	ZoneID   string
	Sensor   Entity
}

// TriggerSystem генерирует enter/exit при пересечении TriggerSensor и TriggerZone.
type TriggerSystem struct {
	active map[triggerPair]struct{}
	events []TriggerEvent
}

func NewTriggerSystem() *TriggerSystem {
	return &TriggerSystem{
		active: make(map[triggerPair]struct{}),
	}
}

func (s *TriggerSystem) Update(w *World, _ float64) {
	zones := QueryTriggerZones(w)
	sensors := QueryTriggerSensors(w)
	current := make(map[triggerPair]struct{})

	for _, zEnt := range zones {
		zPos, okPos := w.Position(zEnt)
		zone, okZone := w.TriggerZone(zEnt)
		if !okPos || !okZone {
			continue
		}
		for _, sEnt := range sensors {
			sPos, okSPos := w.Position(sEnt)
			sensor, okSensor := w.TriggerSensor(sEnt)
			if !okSPos || !okSensor {
				continue
			}
			pair := triggerPair{zone: zEnt, sensor: sEnt}
			if overlapsAABB(zPos, zone.HalfX, zone.HalfY, zone.HalfZ, sPos, sensor.HalfX, sensor.HalfY, sensor.HalfZ) {
				current[pair] = struct{}{}
				if _, was := s.active[pair]; !was {
					s.events = append(s.events, TriggerEvent{
						Type:   TriggerEnter,
						Zone:   zEnt,
						ZoneID: zone.ID,
						Sensor: sEnt,
					})
				}
			}
		}
	}

	for pair := range s.active {
		if _, still := current[pair]; !still {
			zone, _ := w.TriggerZone(pair.zone)
			s.events = append(s.events, TriggerEvent{
				Type:   TriggerExit,
				Zone:   pair.zone,
				ZoneID: zone.ID,
				Sensor: pair.sensor,
			})
		}
	}
	s.active = current
}

// DrainEvents отдаёт накопленные события за предыдущие тики и очищает буфер.
func (s *TriggerSystem) DrainEvents() []TriggerEvent {
	if len(s.events) == 0 {
		return nil
	}
	out := make([]TriggerEvent, len(s.events))
	copy(out, s.events)
	s.events = s.events[:0]
	return out
}

func overlapsAABB(aPos Position, aHalfX, aHalfY, aHalfZ float64, bPos Position, bHalfX, bHalfY, bHalfZ float64) bool {
	return math.Abs(aPos.X-bPos.X) <= (aHalfX+bHalfX) &&
		math.Abs(aPos.Y-bPos.Y) <= (aHalfY+bHalfY) &&
		math.Abs(aPos.Z-bPos.Z) <= (aHalfZ+bHalfZ)
}
