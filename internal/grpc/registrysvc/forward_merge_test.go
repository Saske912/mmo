package registrysvc

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/discovery"
	"mmo/internal/partition"
	"mmo/internal/registry"
)

type mergeCell struct {
	cellv1.UnimplementedCellServer
	exportJSON string
	lastImport string
	mu         sync.Mutex
	players    map[string]uint64
	prepared   map[string]string
	nextEntity uint64
}

func (m *mergeCell) Ping(_ context.Context, _ *cellv1.PingRequest) (*cellv1.PingResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &cellv1.PingResponse{CellId: "test", PlayerCount: int32(len(m.players)), EntityCount: 0}, nil
}

func (m *mergeCell) ListMigrationCandidates(_ context.Context, _ *cellv1.ListMigrationCandidatesRequest) (*cellv1.ListMigrationCandidatesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*cellv1.MigrationCandidate, 0, len(m.players))
	for pid, eid := range m.players {
		out = append(out, &cellv1.MigrationCandidate{
			EntityId: eid,
			IsPlayer: true,
			PlayerId: pid,
		})
	}
	return &cellv1.ListMigrationCandidatesResponse{Candidates: out}, nil
}

func (m *mergeCell) PreparePlayerHandoff(_ context.Context, req *cellv1.PreparePlayerHandoffRequest) (*cellv1.PreparePlayerHandoffResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.players == nil {
		m.players = make(map[string]uint64)
	}
	if m.prepared == nil {
		m.prepared = make(map[string]string)
	}
	pid := req.GetPlayerId()
	if _, ok := m.players[pid]; !ok {
		return &cellv1.PreparePlayerHandoffResponse{Ok: false, Message: "unknown_player"}, nil
	}
	m.prepared[req.GetHandoffToken()] = pid
	return &cellv1.PreparePlayerHandoffResponse{
		Ok: true,
		Payload: &gamev1.PlayerHandoffState{
			PlayerId:     pid,
			HandoffToken: req.GetHandoffToken(),
			TargetCellId: req.GetTargetCellId(),
		},
	}, nil
}

func (m *mergeCell) AcceptPlayerHandoff(_ context.Context, req *cellv1.AcceptPlayerHandoffRequest) (*cellv1.AcceptPlayerHandoffResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.players == nil {
		m.players = make(map[string]uint64)
	}
	if m.nextEntity == 0 {
		m.nextEntity = 100
	}
	p := req.GetPayload()
	if p == nil || p.GetPlayerId() == "" {
		return &cellv1.AcceptPlayerHandoffResponse{Ok: false, Message: "empty payload"}, nil
	}
	if eid, ok := m.players[p.GetPlayerId()]; ok {
		return &cellv1.AcceptPlayerHandoffResponse{Ok: true, Message: "already_accepted", EntityId: eid}, nil
	}
	m.nextEntity++
	m.players[p.GetPlayerId()] = m.nextEntity
	return &cellv1.AcceptPlayerHandoffResponse{Ok: true, Message: "accepted", EntityId: m.nextEntity}, nil
}

func (m *mergeCell) FinalizePlayerHandoff(_ context.Context, req *cellv1.FinalizePlayerHandoffRequest) (*cellv1.FinalizePlayerHandoffResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid, ok := m.prepared[req.GetHandoffToken()]
	if ok && pid == req.GetPlayerId() {
		delete(m.prepared, req.GetHandoffToken())
		delete(m.players, pid)
		return &cellv1.FinalizePlayerHandoffResponse{Ok: true, Message: "finalized"}, nil
	}
	delete(m.players, req.GetPlayerId())
	return &cellv1.FinalizePlayerHandoffResponse{Ok: true, Message: "already_finalized"}, nil
}

func (m *mergeCell) Update(_ context.Context, req *cellv1.UpdateRequest) (*cellv1.UpdateResponse, error) {
	switch p := req.Payload.(type) {
	case *cellv1.UpdateRequest_ExportNpcPersist:
		_ = p
		return &cellv1.UpdateResponse{Ok: true, Message: "export ok", NpcExportJson: m.exportJSON}, nil
	case *cellv1.UpdateRequest_ImportNpcPersist:
		m.lastImport = p.ImportNpcPersist.GetNpcImportJson()
		return &cellv1.UpdateResponse{Ok: true, Message: "import ok"}, nil
	case *cellv1.UpdateRequest_SetSplitDrain:
		return &cellv1.UpdateResponse{Ok: true, Message: "split_drain ok"}, nil
	default:
		return &cellv1.UpdateResponse{Ok: true, Message: "noop"}, nil
	}
}

func (m *mergeCell) PlayerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.players)
}

func TestForwardMergeHandoff(t *testing.T) {
	ctx := context.Background()
	mem := discovery.NewMemoryCatalog(registry.NewMemory())

	parentLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer parentLis.Close()
	parentSrv := grpc.NewServer()
	parentCell := &mergeCell{exportJSON: `{"entities":[]}`}
	cellv1.RegisterCellServer(parentSrv, parentCell)
	go parentSrv.Serve(parentLis)
	defer parentSrv.Stop()

	parent := &cellv1.CellSpec{
		Id:           partition.RootCellID(),
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: parentLis.Addr().String(),
	}
	if err := mem.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}

	childrenPlan, err := partition.ChildSpecsForSplit(parent.GetId(), parent.GetBounds(), parent.GetLevel())
	if err != nil {
		t.Fatal(err)
	}
	childIDs := make([]string, 0, len(childrenPlan))
	for _, cp := range childrenPlan {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer lis.Close()
		srv := grpc.NewServer()
		cellv1.RegisterCellServer(srv, &mergeCell{exportJSON: `{"entities":[]}`})
		go srv.Serve(lis)
		defer srv.Stop()
		spec := &cellv1.CellSpec{
			Id:           cp.GetId(),
			Level:        cp.GetLevel(),
			Bounds:       cp.GetBounds(),
			GrpcEndpoint: lis.Addr().String(),
		}
		if err := mem.RegisterCell(ctx, spec); err != nil {
			t.Fatal(err)
		}
		childIDs = append(childIDs, cp.GetId())
	}

	regLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer regLis.Close()
	regGrpc := grpc.NewServer()
	cellv1.RegisterRegistryServer(regGrpc, &Server{Store: mem})
	go regGrpc.Serve(regLis)
	defer regGrpc.Stop()

	conn, err := grpc.NewClient(regLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := cellv1.NewRegistryClient(conn)
	resp, err := cl.ForwardMergeHandoff(ctx, &cellv1.ForwardMergeHandoffRequest{
		ParentCellId: parent.GetId(),
		ChildCellIds: childIDs,
		Reason:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetOk() {
		t.Fatalf("response not ok: %+v", resp)
	}
	if resp.GetChildCount() != 4 {
		t.Fatalf("child count: got=%d want=4", resp.GetChildCount())
	}
	if parentCell.lastImport == "" {
		t.Fatal("expected parent import call")
	}
}

func TestForwardMergeHandoff_WithPlayerHandoffEnabled(t *testing.T) {
	t.Setenv("MMO_GRID_MERGE_PLAYER_HANDOFF", "true")
	t.Setenv("MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS", "8")

	ctx := context.Background()
	mem := discovery.NewMemoryCatalog(registry.NewMemory())

	parentLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer parentLis.Close()
	parentSrv := grpc.NewServer()
	parentCell := &mergeCell{exportJSON: `{"entities":[]}`, players: map[string]uint64{}}
	cellv1.RegisterCellServer(parentSrv, parentCell)
	go parentSrv.Serve(parentLis)
	defer parentSrv.Stop()

	parent := &cellv1.CellSpec{
		Id:           partition.RootCellID(),
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: parentLis.Addr().String(),
	}
	if err := mem.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}

	childrenPlan, err := partition.ChildSpecsForSplit(parent.GetId(), parent.GetBounds(), parent.GetLevel())
	if err != nil {
		t.Fatal(err)
	}
	childIDs := make([]string, 0, len(childrenPlan))
	var sourceChild *mergeCell
	for i, cp := range childrenPlan {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer lis.Close()
		srv := grpc.NewServer()
		cell := &mergeCell{exportJSON: `{"entities":[]}`, players: map[string]uint64{}}
		if i == 0 {
			cell.players["p-1"] = 1
			cell.players["p-2"] = 2
			sourceChild = cell
		}
		cellv1.RegisterCellServer(srv, cell)
		go srv.Serve(lis)
		defer srv.Stop()
		spec := &cellv1.CellSpec{
			Id:           cp.GetId(),
			Level:        cp.GetLevel(),
			Bounds:       cp.GetBounds(),
			GrpcEndpoint: lis.Addr().String(),
		}
		if err := mem.RegisterCell(ctx, spec); err != nil {
			t.Fatal(err)
		}
		childIDs = append(childIDs, cp.GetId())
	}

	regLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer regLis.Close()
	regGrpc := grpc.NewServer()
	cellv1.RegisterRegistryServer(regGrpc, &Server{Store: mem})
	go regGrpc.Serve(regLis)
	defer regGrpc.Stop()

	conn, err := grpc.NewClient(regLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cl := cellv1.NewRegistryClient(conn)
	resp, err := cl.ForwardMergeHandoff(ctx, &cellv1.ForwardMergeHandoffRequest{
		ParentCellId: parent.GetId(),
		ChildCellIds: childIDs,
		Reason:       "test-live-players",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.GetOk() {
		t.Fatalf("response not ok: %+v", resp)
	}
	if parentCell.PlayerCount() != 2 {
		t.Fatalf("expected players moved to parent, got %d", parentCell.PlayerCount())
	}
	if sourceChild.PlayerCount() != 0 {
		t.Fatalf("expected source child players removed, got %d", sourceChild.PlayerCount())
	}
}
