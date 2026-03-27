package discovery

import (
	"testing"

	"github.com/hashicorp/consul/api"

	cellv1 "mmo/gen/cellv1"
)

func TestAgentServiceRoundTrip(t *testing.T) {
	spec := &cellv1.CellSpec{
		Id:           "cell_0_0_0",
		Level:        1,
		GrpcEndpoint: "127.0.0.1:5005",
		Bounds:       &cellv1.Bounds{XMin: -100, XMax: 100, ZMin: -50, ZMax: 50},
	}
	reg, err := cellSpecToAgentRegistration(spec)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Name != ServiceNameMMOCell || reg.ID != spec.Id {
		t.Fatalf("registration: %+v", reg)
	}
	got, err := agentServiceToCellSpec(&api.AgentService{
		ID:      reg.ID,
		Service: reg.Name,
		Address: reg.Address,
		Port:    reg.Port,
		Meta:    reg.Meta,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != spec.Id || got.Level != spec.Level || got.GrpcEndpoint != spec.GrpcEndpoint {
		t.Fatalf("got %+v want %+v", got, spec)
	}
	if got.Bounds.XMin != spec.Bounds.XMin || got.Bounds.XMax != spec.Bounds.XMax {
		t.Fatalf("bounds %+v", got.Bounds)
	}
}

func TestAgentServiceLogicalIDFromMeta(t *testing.T) {
	spec := &cellv1.CellSpec{
		Id:           "cell_0_0_0",
		Level:        0,
		GrpcEndpoint: "cell.example:50051",
		Bounds:       &cellv1.Bounds{XMin: -1, XMax: 1, ZMin: -1, ZMax: 1},
	}
	reg, err := cellSpecToAgentRegistration(spec)
	if err != nil {
		t.Fatal(err)
	}
	reg.ID = "cell_0_0_0-my-pod"
	got, err := agentServiceToCellSpec(&api.AgentService{
		ID:      reg.ID,
		Service: reg.Name,
		Address: reg.Address,
		Port:    reg.Port,
		Meta:    reg.Meta,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Id != "cell_0_0_0" {
		t.Fatalf("logical id: got %q", got.Id)
	}
}

func TestPickBestCell(t *testing.T) {
	parent := &cellv1.CellSpec{
		Id: "p", Level: 0,
		Bounds: &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
	}
	child := &cellv1.CellSpec{
		Id: "c", Level: 1,
		Bounds: &cellv1.Bounds{XMin: -1000, XMax: 0, ZMin: -1000, ZMax: 0},
	}
	got, ok := PickBestCell([]*cellv1.CellSpec{parent, child}, -100, -100)
	if !ok || got.Id != "c" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}
