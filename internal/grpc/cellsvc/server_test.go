package cellsvc

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/cellsim"
	"mmo/internal/partition"
)

func TestJoinApplyInputMovesPlayer(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "t00", Sim: sim}
	ctx := context.Background()

	j, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "p1"})
	if err != nil || !j.Ok || j.EntityId == 0 {
		t.Fatalf("Join: %+v err=%v", j, err)
	}

	_, err = srv.ApplyInput(ctx, &cellv1.ApplyInputRequest{
		PlayerId: "p1",
		Input: &gamev1.ClientInput{
			InputMask: InputForward,
			Seq:       1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Несколько игровых шагов: движение по +Z
	for range 10 {
		sim.Mu.Lock()
		sim.Loop.Step()
		sim.Mu.Unlock()
	}

	sim.Mu.Lock()
	defer sim.Mu.Unlock()
	e := srv.playerByID["p1"]
	p, ok := sim.World.Position(e)
	if !ok {
		t.Fatal("no position")
	}
	if p.Z < 0.1 {
		t.Fatalf("expected positive Z after forward velocity, got %+v", p)
	}
}

func TestJoinIdempotent(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "t01", Sim: sim}
	ctx := context.Background()

	j1, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "p2"})
	if err != nil || !j1.Ok {
		t.Fatal(err)
	}
	j2, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "p2"})
	if err != nil || !j2.Ok || j2.EntityId != j1.EntityId {
		t.Fatalf("second join: %+v want entity %d", j2, j1.EntityId)
	}
}

func TestUpdateNoop(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "t03", Sim: sim}
	ctx := context.Background()

	res, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_Noop{Noop: &cellv1.CellUpdateNoop{}},
	})
	if err != nil || res == nil || !res.Ok {
		t.Fatalf("Update noop: %+v err=%v", res, err)
	}
}

func TestUpdateSetTargetTps(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "t04", Sim: sim}
	ctx := context.Background()

	res, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetTargetTps{SetTargetTps: 40},
	})
	if err != nil || !res.Ok {
		t.Fatalf("Update: %+v err=%v", res, err)
	}
	sim.Mu.Lock()
	got := sim.Loop.TPS
	sim.Mu.Unlock()
	if got != 40 {
		t.Fatalf("TPS=%v", got)
	}

	bad, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetTargetTps{SetTargetTps: 200},
	})
	if err != nil || bad.Ok {
		t.Fatalf("expected range error, got %+v err=%v", bad, err)
	}
}

func TestUpdateSetSplitDrain(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "t_drain", Sim: sim}
	ctx := context.Background()

	off, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetSplitDrain{SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: false}},
	})
	if err != nil || !off.Ok {
		t.Fatalf("disable drain: %+v err=%v", off, err)
	}
	on, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetSplitDrain{SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: true}},
	})
	if err != nil || !on.Ok {
		t.Fatalf("enable drain: %+v err=%v", on, err)
	}
	j, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "new_while_drain"})
	if err != nil {
		t.Fatal(err)
	}
	if j.Ok || j.Message == "" {
		t.Fatalf("join should fail under drain: %+v", j)
	}
	_, err = srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetSplitDrain{SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	j2, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "after_undrain"})
	if err != nil || !j2.Ok {
		t.Fatalf("join after undrain: %+v err=%v", j2, err)
	}
}

func TestUpdateExportNpcPersist(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "exp1", Sim: sim}
	ctx := context.Background()
	res, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ExportNpcPersist{
			ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: "test"},
		},
	})
	if err != nil || res == nil || !res.Ok {
		t.Fatalf("export: %+v err=%v", res, err)
	}
	if res.NpcExportJson == "" {
		t.Fatal("expected npc_export_json")
	}
	// protojson опускает пустой repeated entities.
	if !strings.Contains(res.NpcExportJson, "schemaVersion") {
		t.Fatalf("unexpected json: %s", res.NpcExportJson)
	}
}

func TestUpdateImportNpcPersist_roundtripFromExport(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv1 := &Server{CellID: "imp_src", Sim: sim}
	ctx := context.Background()
	exp, err := srv1.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ExportNpcPersist{
			ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: "src"},
		},
	})
	if err != nil || exp == nil || !exp.Ok || exp.NpcExportJson == "" {
		t.Fatalf("export: %+v err=%v", exp, err)
	}
	sim2 := cellsim.NewRuntime()
	srv2 := &Server{CellID: "imp_dst", Sim: sim2}
	im, err := srv2.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ImportNpcPersist{
			ImportNpcPersist: &cellv1.CellUpdateImportNpcPersist{
				NpcImportJson: exp.NpcExportJson,
				Reason:        "dst",
			},
		},
	})
	if err != nil || im == nil || !im.Ok {
		t.Fatalf("import: %+v err=%v", im, err)
	}
}

func TestUpdateImportNpcPersist_rejectsWithPlayers(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "imp_blk", Sim: sim}
	ctx := context.Background()
	if _, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "in_world"}); err != nil {
		t.Fatal(err)
	}
	exp, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ExportNpcPersist{
			ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: "snap"},
		},
	})
	if err != nil || exp == nil || !exp.Ok {
		t.Fatal(exp, err)
	}
	im, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ImportNpcPersist{
			ImportNpcPersist: &cellv1.CellUpdateImportNpcPersist{
				NpcImportJson: exp.NpcExportJson,
				Reason:        "no",
			},
		},
	})
	if err != nil || im == nil || im.Ok {
		t.Fatalf("expected import blocked with players, got %+v err=%v", im, err)
	}
}

func TestListMigrationCandidates(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "mig1", Sim: sim}
	ctx := context.Background()
	resp, err := srv.ListMigrationCandidates(ctx, &cellv1.ListMigrationCandidatesRequest{Reason: "test"})
	if err != nil || len(resp.Candidates) != 0 {
		t.Fatalf("empty world: %+v err=%v", resp, err)
	}
	if _, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "p_mig"}); err != nil {
		t.Fatal(err)
	}
	resp2, err := srv.ListMigrationCandidates(ctx, &cellv1.ListMigrationCandidatesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp2.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(resp2.Candidates))
	}
	c := resp2.Candidates[0]
	if !c.IsPlayer || c.EntityId == 0 || c.Position == nil {
		t.Fatalf("unexpected candidate: %+v", c)
	}
}

func TestUpdateSplitPrepare(t *testing.T) {
	sim := cellsim.NewRuntime()
	parent := &cellv1.Bounds{XMin: -100, XMax: 100, ZMin: -100, ZMax: 100}
	srv := &Server{CellID: "cell_x", Sim: sim, Bounds: parent, Level: 1}
	ctx := context.Background()

	res, err := srv.Update(ctx, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SplitPrepare{
			SplitPrepare: &cellv1.CellUpdateSplitPrepare{Reason: "test"},
		},
	})
	if err != nil || res == nil || !res.Ok {
		t.Fatalf("split_prepare: %+v err=%v", res, err)
	}
	if len(res.Message) < 10 {
		t.Fatalf("short message: %q", res.Message)
	}
}

func TestLeaveIdempotent(t *testing.T) {
	sim := cellsim.NewRuntime()
	srv := &Server{CellID: "t02", Sim: sim}
	ctx := context.Background()

	_, err := srv.Join(ctx, &cellv1.JoinRequest{PlayerId: "p3"})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := srv.Leave(ctx, &cellv1.LeaveRequest{PlayerId: "p3"})
	if err != nil || !l1.Ok {
		t.Fatalf("Leave: %+v", l1)
	}
	l2, err := srv.Leave(ctx, &cellv1.LeaveRequest{PlayerId: "p3"})
	if err != nil || !l2.Ok {
		t.Fatalf("second Leave: %+v", l2)
	}
}

func TestPlanSplit_level0(t *testing.T) {
	sim := cellsim.NewRuntime()
	parent := &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000}
	srv := &Server{CellID: "cell_0_0_0", Sim: sim, Bounds: parent, Level: 0}
	ctx := context.Background()

	resp, err := srv.PlanSplit(ctx, &cellv1.PlanSplitRequest{})
	if err != nil {
		t.Fatal(err)
	}
	wantSpecs := partition.ChildSpecsForSplit(parent, 0)
	if len(wantSpecs) != len(resp.Children) {
		t.Fatalf("partition vs PlanSplit len: %d %d", len(wantSpecs), len(resp.Children))
	}
	for i, w := range wantSpecs {
		if !proto.Equal(w, resp.Children[i]) {
			t.Fatalf("child[%d]: PlanSplit %+v partition %+v", i, resp.Children[i], w)
		}
	}
	if len(resp.Children) != 4 {
		t.Fatalf("children: %d", len(resp.Children))
	}
	mx, mz := partition.Mid(parent)
	wantIDs := []string{
		partition.ChildID(0, 0, 1),
		partition.ChildID(1, 0, 1),
		partition.ChildID(0, 1, 1),
		partition.ChildID(1, 1, 1),
	}
	for i, ch := range resp.Children {
		if ch.Id != wantIDs[i] {
			t.Errorf("child[%d] id: got %s want %s", i, ch.Id, wantIDs[i])
		}
		if ch.Level != 1 {
			t.Errorf("child[%d] level: %d", i, ch.Level)
		}
		b := ch.Bounds
		if b == nil {
			t.Fatalf("child[%d] nil bounds", i)
		}
		switch i {
		case 0:
			if b.XMin != parent.XMin || b.XMax != mx || b.ZMin != parent.ZMin || b.ZMax != mz {
				t.Errorf("quad0 bounds %+v mid (%v,%v)", b, mx, mz)
			}
			if !partition.Contains(b, -500, -500) {
				t.Errorf("quad0 should contain (-500,-500)")
			}
		case 3:
			if b.XMin != mx || b.XMax != parent.XMax || b.ZMin != mz || b.ZMax != parent.ZMax {
				t.Errorf("quad3 bounds %+v", b)
			}
		}
	}
}

func TestPlanSplit_noBounds(t *testing.T) {
	srv := &Server{CellID: "x", Sim: cellsim.NewRuntime(), Bounds: nil, Level: 0}
	_, err := srv.PlanSplit(context.Background(), &cellv1.PlanSplitRequest{})
	if err == nil {
		t.Fatal("want error")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code: %v", err)
	}
}
