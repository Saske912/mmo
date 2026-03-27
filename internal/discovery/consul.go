package discovery

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

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
	// Уникальный Agent ID на инстанс: при rollout старый pod делает Deregister и не снимает
	// регистрацию нового (раньше все использовали один и тот же id соты).
	reg.ID = ConsulServiceInstanceID(spec.Id)
	// Без health/TTL: в этом окружении UpdateTTL по HTTP не находит check; сервис без checks
	// считается passing.
	if err := c.client.Agent().ServiceRegister(reg); err != nil {
		return err
	}
	return nil
}

// ConsulServiceInstanceID — id сервиса на локальном Consul-агенте. В Kubernetes HOSTNAME = имя pod.
func ConsulServiceInstanceID(logicalCellID string) string {
	if logicalCellID == "" {
		return ""
	}
	if h := strings.TrimSpace(os.Getenv("HOSTNAME")); h != "" {
		return logicalCellID + "-" + h
	}
	return logicalCellID
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
