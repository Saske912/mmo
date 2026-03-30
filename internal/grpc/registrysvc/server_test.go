package registrysvc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/discovery"
	"mmo/internal/registry"
)

type recordingCell struct {
	cellv1.UnimplementedCellServer
	last           *cellv1.UpdateRequest
	lastPrepare    *cellv1.PreparePlayerHandoffRequest
	lastAccept     *cellv1.AcceptPlayerHandoffRequest
	lastFinalize   *cellv1.FinalizePlayerHandoffRequest
	acceptEntityID uint64
	prepareFail    bool
	acceptFail     bool
	finalizeFail   bool
}

func (r *recordingCell) Update(_ context.Context, req *cellv1.UpdateRequest) (*cellv1.UpdateResponse, error) {
	r.last = cloneUpdateReq(req)
	if tps := req.GetSetTargetTps(); tps == 999 {
		return &cellv1.UpdateResponse{Ok: false, Message: "no"}, nil
	}
	return &cellv1.UpdateResponse{Ok: true, Message: "ok"}, nil
}

func (r *recordingCell) PreparePlayerHandoff(_ context.Context, req *cellv1.PreparePlayerHandoffRequest) (*cellv1.PreparePlayerHandoffResponse, error) {
	r.lastPrepare = req
	if r.prepareFail {
		return &cellv1.PreparePlayerHandoffResponse{Ok: false, Message: "prepare_fail"}, nil
	}
	return &cellv1.PreparePlayerHandoffResponse{
		Ok:      true,
		Message: "prepared",
		Payload: &gamev1.PlayerHandoffState{
			PlayerId:       req.GetPlayerId(),
			HandoffToken:   req.GetHandoffToken(),
			TargetCellId:   req.GetTargetCellId(),
			SourceEntityId: 42,
			Position:       &gamev1.Vec3F{X: 10, Z: 20},
			Velocity:       &gamev1.Vec3F{},
			HpCur:          100,
			HpMax:          100,
		},
	}, nil
}

func (r *recordingCell) AcceptPlayerHandoff(_ context.Context, req *cellv1.AcceptPlayerHandoffRequest) (*cellv1.AcceptPlayerHandoffResponse, error) {
	r.lastAccept = req
	if r.acceptFail {
		return &cellv1.AcceptPlayerHandoffResponse{Ok: false, Message: "accept_fail"}, nil
	}
	entityID := r.acceptEntityID
	if entityID == 0 {
		entityID = 1001
	}
	return &cellv1.AcceptPlayerHandoffResponse{Ok: true, Message: "accepted", EntityId: entityID}, nil
}

func (r *recordingCell) FinalizePlayerHandoff(_ context.Context, req *cellv1.FinalizePlayerHandoffRequest) (*cellv1.FinalizePlayerHandoffResponse, error) {
	r.lastFinalize = req
	if r.finalizeFail {
		return &cellv1.FinalizePlayerHandoffResponse{Ok: false, Message: "finalize_fail"}, nil
	}
	return &cellv1.FinalizePlayerHandoffResponse{Ok: true, Message: "finalized"}, nil
}

func cloneUpdateReq(req *cellv1.UpdateRequest) *cellv1.UpdateRequest {
	if req == nil {
		return nil
	}
	switch p := req.Payload.(type) {
	case *cellv1.UpdateRequest_Noop:
		return &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_Noop{Noop: p.Noop}}
	case *cellv1.UpdateRequest_SetTargetTps:
		return &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_SetTargetTps{SetTargetTps: p.SetTargetTps}}
	default:
		return nil
	}
}

func TestForwardCellUpdate(t *testing.T) {
	ctx := context.Background()

	cellLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cellLis.Close() })

	rec := &recordingCell{}
	cellGrpc := grpc.NewServer()
	cellv1.RegisterCellServer(cellGrpc, rec)
	go func() {
		if err := cellGrpc.Serve(cellLis); err != nil {
			t.Logf("cell serve: %v", err)
		}
	}()
	t.Cleanup(cellGrpc.Stop)

	mem := discovery.NewMemoryCatalog(registry.NewMemory())
	spec := &cellv1.CellSpec{
		Id:           "alpha",
		Level:        0,
		GrpcEndpoint: cellLis.Addr().String(),
		Bounds:       &cellv1.Bounds{XMin: -500, XMax: 500, ZMin: -500, ZMax: 500},
	}
	if err := mem.RegisterCell(ctx, spec); err != nil {
		t.Fatal(err)
	}

	regLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = regLis.Close() })
	regGrpc := grpc.NewServer()
	cellv1.RegisterRegistryServer(regGrpc, &Server{Store: mem})
	go func() {
		if err := regGrpc.Serve(regLis); err != nil {
			t.Logf("registry serve: %v", err)
		}
	}()
	t.Cleanup(regGrpc.Stop)

	conn, err := grpc.NewClient(regLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	regCl := cellv1.NewRegistryClient(conn)

	t.Run("noop", func(t *testing.T) {
		rec.last = nil
		resp, err := regCl.ForwardCellUpdate(ctx, &cellv1.ForwardCellUpdateRequest{
			CellId: "alpha",
			Update: &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_Noop{Noop: &cellv1.CellUpdateNoop{}}},
		})
		if err != nil || resp == nil || !resp.Ok {
			t.Fatalf("noop: %+v err=%v", resp, err)
		}
		if rec.last.GetNoop() == nil {
			t.Fatalf("cell got %#v", rec.last)
		}
	})

	t.Run("tps", func(t *testing.T) {
		rec.last = nil
		resp, err := regCl.ForwardCellUpdate(ctx, &cellv1.ForwardCellUpdateRequest{
			CellId: "alpha",
			Update: &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_SetTargetTps{SetTargetTps: 31}},
		})
		if err != nil || !resp.Ok {
			t.Fatalf("tps: %+v err=%v", resp, err)
		}
		if rec.last.GetSetTargetTps() != 31 {
			t.Fatalf("cell got %#v", rec.last)
		}
	})

	t.Run("cell_returns_ok_false", func(t *testing.T) {
		resp, err := regCl.ForwardCellUpdate(ctx, &cellv1.ForwardCellUpdateRequest{
			CellId: "alpha",
			Update: &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_SetTargetTps{SetTargetTps: 999}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Ok || resp.Message != "no" {
			t.Fatalf("want ok false: %+v", resp)
		}
	})

	t.Run("unknown_cell", func(t *testing.T) {
		clientCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, err := regCl.ForwardCellUpdate(clientCtx, &cellv1.ForwardCellUpdateRequest{
			CellId: "nope",
			Update: &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_Noop{Noop: &cellv1.CellUpdateNoop{}}},
		})
		if err == nil {
			t.Fatal("want error")
		}
		if status.Code(err) != codes.NotFound {
			t.Fatalf("want NotFound, got %v", err)
		}
	})
}

func TestForwardPlayerHandoff(t *testing.T) {
	ctx := context.Background()
	mem := discovery.NewMemoryCatalog(registry.NewMemory())

	startCell := func(id string, rec *recordingCell) (string, func()) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := grpc.NewServer()
		cellv1.RegisterCellServer(srv, rec)
		go func() { _ = srv.Serve(lis) }()
		spec := &cellv1.CellSpec{
			Id:           id,
			Level:        1,
			GrpcEndpoint: lis.Addr().String(),
			Bounds:       &cellv1.Bounds{XMin: -100, XMax: 100, ZMin: -100, ZMax: 100},
		}
		if err := mem.RegisterCell(ctx, spec); err != nil {
			t.Fatal(err)
		}
		return lis.Addr().String(), func() {
			srv.Stop()
			_ = lis.Close()
		}
	}

	parentRec := &recordingCell{}
	childRec := &recordingCell{acceptEntityID: 777}
	_, stopParent := startCell("parent", parentRec)
	defer stopParent()
	_, stopChild := startCell("child", childRec)
	defer stopChild()

	regLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer regLis.Close()
	regSrv := grpc.NewServer()
	cellv1.RegisterRegistryServer(regSrv, &Server{Store: mem})
	go func() { _ = regSrv.Serve(regLis) }()
	defer regSrv.Stop()

	conn, err := grpc.NewClient(regLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := cellv1.NewRegistryClient(conn)

	resp, err := cl.ForwardPlayerHandoff(ctx, &cellv1.ForwardPlayerHandoffRequest{
		ParentCellId: "parent",
		ChildCellId:  "child",
		PlayerId:     "player-a",
		HandoffToken: "tok-a",
		Reason:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetOk() || resp.GetChildEntityId() != 777 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if parentRec.lastPrepare == nil || parentRec.lastPrepare.GetPlayerId() != "player-a" {
		t.Fatalf("prepare not called as expected: %+v", parentRec.lastPrepare)
	}
	if childRec.lastAccept == nil || childRec.lastAccept.GetPayload() == nil || childRec.lastAccept.GetPayload().GetHandoffToken() != "tok-a" {
		t.Fatalf("accept not called as expected: %+v", childRec.lastAccept)
	}
	if parentRec.lastFinalize == nil || parentRec.lastFinalize.GetHandoffToken() != "tok-a" {
		t.Fatalf("finalize not called as expected: %+v", parentRec.lastFinalize)
	}
}
