package registry

import (
	"context"
	"sync"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/partition"
)

// Memory — in-memory реестр сот (итерация 1: без Consul/etcd).
type Memory struct {
	mu    sync.RWMutex
	cells map[string]*cellv1.CellSpec // по id
}

func NewMemory() *Memory {
	return &Memory{cells: make(map[string]*cellv1.CellSpec)}
}

func (m *Memory) Register(_ context.Context, spec *cellv1.CellSpec) error {
	if spec == nil || spec.Id == "" || spec.Bounds == nil {
		return errInvalidSpec
	}
	b := spec.Bounds
	if b.XMin >= b.XMax || b.ZMin >= b.ZMax {
		return errInvalidBounds
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := cloneSpec(spec)
	m.cells[spec.Id] = cp
	return nil
}

func (m *Memory) List(_ context.Context) []*cellv1.CellSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*cellv1.CellSpec, 0, len(m.cells))
	for _, c := range m.cells {
		out = append(out, cloneSpec(c))
	}
	return out
}

// ResolveMostSpecific возвращает соту с максимальным level среди содержащих точку.
func (m *Memory) ResolveMostSpecific(_ context.Context, x, z float64) (*cellv1.CellSpec, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var best *cellv1.CellSpec
	for _, c := range m.cells {
		if !partition.Contains(c.Bounds, x, z) {
			continue
		}
		if best == nil || c.Level > best.Level {
			best = c
		}
	}
	if best == nil {
		return nil, false
	}
	return cloneSpec(best), true
}

// Deregister удаляет соту по id (для graceful shutdown и gRPC Register отката).
func (m *Memory) Deregister(_ context.Context, id string) error {
	if id == "" {
		return errInvalidSpec
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cells, id)
	return nil
}

func cloneSpec(s *cellv1.CellSpec) *cellv1.CellSpec {
	if s == nil {
		return nil
	}
	out := *s
	if s.Bounds != nil {
		b := *s.Bounds
		out.Bounds = &b
	}
	return &out
}
