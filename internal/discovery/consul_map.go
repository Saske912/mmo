package discovery

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/hashicorp/consul/api"

	cellv1 "mmo/gen/cellv1"
)

func cellSpecToAgentRegistration(spec *cellv1.CellSpec) (*api.AgentServiceRegistration, error) {
	if spec == nil || spec.Id == "" || spec.Bounds == nil {
		return nil, fmt.Errorf("invalid cell spec")
	}
	b := spec.Bounds
	if b.XMin >= b.XMax || b.ZMin >= b.ZMax {
		return nil, fmt.Errorf("invalid bounds")
	}
	host, portStr, err := net.SplitHostPort(spec.GrpcEndpoint)
	if err != nil {
		return nil, fmt.Errorf("grpc_endpoint host:port: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 {
		return nil, fmt.Errorf("invalid port in grpc_endpoint")
	}

	meta := map[string]string{
		MetaCellLogicalID: spec.Id,
		MetaLevel:         strconv.Itoa(int(spec.Level)),
		MetaXMin:          formatFloat(b.XMin),
		MetaXMax:          formatFloat(b.XMax),
		MetaZMin:          formatFloat(b.ZMin),
		MetaZMax:          formatFloat(b.ZMax),
		MetaStatus:        "active",
	}

	return &api.AgentServiceRegistration{
		ID:      spec.Id,
		Name:    ServiceNameMMOCell,
		Address: host,
		Port:    port,
		Meta:    meta,
	}, nil
}

func agentServiceToCellSpec(s *api.AgentService) (*cellv1.CellSpec, error) {
	if s == nil {
		return nil, fmt.Errorf("nil service")
	}
	endpoint := net.JoinHostPort(s.Address, strconv.Itoa(s.Port))
	bounds, err := metaToBounds(s.Meta)
	if err != nil {
		return nil, err
	}
	level, err := metaInt32(s.Meta, MetaLevel)
	if err != nil {
		return nil, err
	}
	logical := strings.TrimSpace(s.Meta[MetaCellLogicalID])
	if logical == "" {
		logical = s.ID
	}
	spec := &cellv1.CellSpec{
		Id:           logical,
		Level:        level,
		Bounds:       bounds,
		GrpcEndpoint: endpoint,
	}
	return spec, nil
}

func metaToBounds(meta map[string]string) (*cellv1.Bounds, error) {
	xmin, err := metaFloat64(meta, MetaXMin)
	if err != nil {
		return nil, err
	}
	xmax, err := metaFloat64(meta, MetaXMax)
	if err != nil {
		return nil, err
	}
	zmin, err := metaFloat64(meta, MetaZMin)
	if err != nil {
		return nil, err
	}
	zmax, err := metaFloat64(meta, MetaZMax)
	if err != nil {
		return nil, err
	}
	return &cellv1.Bounds{XMin: xmin, XMax: xmax, ZMin: zmin, ZMax: zmax}, nil
}

func metaFloat64(meta map[string]string, key string) (float64, error) {
	s, ok := meta[key]
	if !ok {
		return 0, fmt.Errorf("meta %q missing", key)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("meta %q: %w", key, err)
	}
	return v, nil
}

func metaInt32(meta map[string]string, key string) (int32, error) {
	s, ok := meta[key]
	if !ok {
		return 0, fmt.Errorf("meta %q missing", key)
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("meta %q: %w", key, err)
	}
	return int32(v), nil
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
