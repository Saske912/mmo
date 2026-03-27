package discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/consul/api"

	cellv1 "mmo/gen/cellv1"
)

// ConsulCatalog читает и пишет каталог через Consul HTTP API.
type ConsulCatalog struct {
	client *api.Client
}

// NewConsulCatalog создаёт клиента; addr — host:port (например из CONSUL_HTTP_ADDR).
func NewConsulCatalog(addr, token string) (*ConsulCatalog, error) {
	cfg := api.DefaultConfig()
	cfg.Address = NormalizeConsulHTTPAddr(addr)
	if token != "" {
		cfg.Token = token
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ConsulCatalog{client: client}, nil
}

func (c *ConsulCatalog) RegisterCell(ctx context.Context, spec *cellv1.CellSpec) error {
	_ = ctx
	reg, err := cellSpecToAgentRegistration(spec)
	if err != nil {
		return err
	}
	reg.Check = &api.AgentServiceCheck{
		TTL:                            "30s",
		DeregisterCriticalServiceAfter: "90s",
	}
	if err := c.client.Agent().ServiceRegister(reg); err != nil {
		return err
	}
	return c.passTTL(spec.Id)
}

func (c *ConsulCatalog) passTTL(serviceID string) error {
	checkID := "service:" + serviceID
	return c.client.Agent().UpdateTTL(checkID, "ok", api.HealthPassing)
}

// MaintainTTL периодически обновляет TTL check (нужно для статуса passing).
func (c *ConsulCatalog) MaintainTTL(ctx context.Context, serviceID string) {
	checkID := "service:" + serviceID
	tick := time.NewTicker(8 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = c.client.Agent().UpdateTTL(checkID, "ok", api.HealthPassing)
		}
	}
}

func (c *ConsulCatalog) Deregister(ctx context.Context, serviceID string) error {
	_ = ctx
	if serviceID == "" {
		return fmt.Errorf("empty service id")
	}
	return c.client.Agent().ServiceDeregister(serviceID)
}

func (c *ConsulCatalog) List(ctx context.Context) ([]*cellv1.CellSpec, error) {
	q := &api.QueryOptions{}
	if ctx != nil {
		q = q.WithContext(ctx)
	}
	entries, _, err := c.client.Health().Service(ServiceNameMMOCell, "", true, q)
	if err != nil {
		return nil, err
	}
	out := make([]*cellv1.CellSpec, 0, len(entries))
	for _, e := range entries {
		if e.Service == nil {
			continue
		}
		spec, err := agentServiceToCellSpec(e.Service)
		if err != nil {
			continue
		}
		out = append(out, spec)
	}
	return out, nil
}

func (c *ConsulCatalog) ResolveMostSpecific(ctx context.Context, x, z float64) (*cellv1.CellSpec, bool, error) {
	cells, err := c.List(ctx)
	if err != nil {
		return nil, false, err
	}
	spec, ok := PickBestCell(cells, x, z)
	return spec, ok, nil
}
