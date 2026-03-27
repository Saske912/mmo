package discovery

import (
	"context"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/partition"
)

// ServiceNameMMOCell — имя сервиса в каталоге Consul (чеклист 0.1).
const ServiceNameMMOCell = "mmo-cell"

// Meta keys для Consul ServiceRegistration.Meta (строки).
const (
	MetaLevel         = "level"
	MetaXMin          = "x_min"
	MetaXMax          = "x_max"
	MetaZMin          = "z_min"
	MetaZMax          = "z_max"
	MetaStatus        = "status"
	MetaCellLogicalID = "mmo_cell_id" // логический id соты; AgentService.ID в K8s = этот + "-" + HOSTNAME
)

// Catalog — обнаружение сот (память или Consul).
type Catalog interface {
	RegisterCell(ctx context.Context, spec *cellv1.CellSpec) error
	Deregister(ctx context.Context, serviceID string) error
	List(ctx context.Context) ([]*cellv1.CellSpec, error)
	ResolveMostSpecific(ctx context.Context, x, z float64) (*cellv1.CellSpec, bool, error)
}

// PickBestCell возвращает соту с максимальным level среди содержащих точку (как registry.Memory).
// FindCellByID ищет соту по CellSpec.id (сканирование каталога через List).
func FindCellByID(ctx context.Context, c Catalog, id string) (*cellv1.CellSpec, bool, error) {
	if id == "" {
		return nil, false, nil
	}
	cells, err := c.List(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, s := range cells {
		if s != nil && s.Id == id {
			return cloneSpec(s), true, nil
		}
	}
	return nil, false, nil
}

func PickBestCell(cells []*cellv1.CellSpec, x, z float64) (*cellv1.CellSpec, bool) {
	var best *cellv1.CellSpec
	for _, c := range cells {
		if c == nil || c.Bounds == nil {
			continue
		}
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
