package discovery

import (
	"context"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/registry"
)

// MemoryCatalog реализует Catalog поверх in-memory registry (локальная разработка без Consul).
type MemoryCatalog struct {
	mem *registry.Memory
}

func NewMemoryCatalog(mem *registry.Memory) *MemoryCatalog {
	return &MemoryCatalog{mem: mem}
}

func (m *MemoryCatalog) RegisterCell(ctx context.Context, spec *cellv1.CellSpec) error {
	return m.mem.Register(ctx, spec)
}

func (m *MemoryCatalog) Deregister(ctx context.Context, serviceID string) error {
	return m.mem.Deregister(ctx, serviceID)
}

func (m *MemoryCatalog) List(ctx context.Context) ([]*cellv1.CellSpec, error) {
	return m.mem.List(ctx), nil
}

func (m *MemoryCatalog) ResolveMostSpecific(ctx context.Context, x, z float64) (*cellv1.CellSpec, bool, error) {
	c, ok := m.mem.ResolveMostSpecific(ctx, x, z)
	return c, ok, nil
}
