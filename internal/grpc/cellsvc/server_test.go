package cellsvc

import (
	"context"
	"testing"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/cellsim"
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
