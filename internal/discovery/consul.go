package discovery

import (
	"context"
	"fmt"
	"log"
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
	checkID := ttlCheckID(spec.Id)
	reg.Check = &api.AgentServiceCheck{
		CheckID:                        checkID,
		TTL:                            "30s",
		DeregisterCriticalServiceAfter: "90s",
	}
	if err := c.client.Agent().ServiceRegister(reg); err != nil {
		return err
	}
	return c.passTTL(spec.Id)
}

// ttlCheckID без ":" — иначе PUT /v1/agent/check/update/<id> на стороне Consul
// трактует путь неверно и UpdateTTL не находит check (см. лог agent.http Unknown check ID).
func ttlCheckID(serviceID string) string {
	return "mmo-cell-ttl-" + serviceID
}

func (c *ConsulCatalog) passTTL(serviceID string) error {
	return c.client.Agent().UpdateTTL(ttlCheckID(serviceID), "ok", api.HealthPassing)
}

// MaintainTTL периодически обновляет TTL check (нужно для статуса passing).
func (c *ConsulCatalog) MaintainTTL(ctx context.Context, serviceID string) {
	checkID := ttlCheckID(serviceID)
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
	var droppedParse int
	for _, e := range entries {
		if e.Service == nil {
			continue
		}
		spec, err := agentServiceToCellSpec(e.Service)
		if err != nil {
			sid := e.Service.ID
			log.Printf("consul catalog: skip service %q: %v", sid, err)
			droppedParse++
			continue
		}
		out = append(out, spec)
	}
	if droppedParse > 0 {
		log.Printf("consul catalog: dropped %d mmo-cell instance(s) with invalid meta", droppedParse)
	}
	if len(entries) > 0 && len(out) == 0 && droppedParse == 0 {
		log.Printf("consul catalog: %d health row(s) for mmo-cell but none parsed (unexpected)", len(entries))
	}
	if len(out) == 0 {
		all, _, err := c.client.Health().Service(ServiceNameMMOCell, "", false, q)
		if err == nil && len(all) > 0 {
			log.Printf("consul catalog: no passing mmo-cell, but %d instance(s) exist (check TTL/health)", len(all))
		}
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
