package main

import (
	"encoding/json"
	"testing"
)

func TestResolvePreviewShape(t *testing.T) {
	const body = `{"resolve_x":0,"resolve_z":0,"resolved":{"found":true,"cell_id":"cell_0_0_0","grpc_endpoint":"x"},"last_cell":{"found":false},"cell_id_mismatch":false}`
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if mismatch, _ := m["cell_id_mismatch"].(bool); mismatch {
		t.Fatal("expected mismatch false")
	}
	res, _ := m["resolved"].(map[string]any)
	if res["cell_id"] != "cell_0_0_0" {
		t.Fatalf("resolved.cell_id: %#v", res["cell_id"])
	}
}
