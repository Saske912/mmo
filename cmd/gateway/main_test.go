package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/cellsim"
	"mmo/internal/grpc/cellsvc"
)

func TestLeaveDownstream_RemovesPlayerFromOldCell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cellClient, conn, shutdown := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_q0_q3",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdown()

	joinResp, err := cellClient.Join(ctx, &cellv1.JoinRequest{PlayerId: "player-1"})
	if err != nil || joinResp == nil || !joinResp.GetOk() {
		t.Fatalf("join failed: resp=%+v err=%v", joinResp, err)
	}
	before, err := cellClient.Ping(ctx, &cellv1.PingRequest{ClientId: "before"})
	if err != nil {
		t.Fatalf("ping before leave: %v", err)
	}
	if before.GetPlayerCount() != 1 {
		t.Fatalf("expected player_count=1 before cleanup, got %d", before.GetPlayerCount())
	}

	g := &gateway{}
	ds := &gatewayDownstream{
		cellID: "cell_q0_q3",
		conn:   conn,
		client: cellClient,
	}
	g.leaveDownstream(ctx, ds, "player-1", "switch_old", "cell_q0_q2")

	after, err := cellClient.Ping(ctx, &cellv1.PingRequest{ClientId: "after"})
	if err != nil {
		t.Fatalf("ping after leave: %v", err)
	}
	if after.GetPlayerCount() != 0 {
		t.Fatalf("expected player_count=0 after cleanup, got %d", after.GetPlayerCount())
	}
}

func TestCloseDownstreamConn_NoPanic(t *testing.T) {
	t.Parallel()
	_, conn, shutdown := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_close",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdown()

	g := &gateway{}
	ds := &gatewayDownstream{cellID: "cell_close", conn: conn}
	g.closeDownstreamConn(ds, "switch_old")
}

func TestTrySwitchDownstreamByPosition_SwitchesWithoutUnknownPlayer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldClient, oldConn, shutdownOld := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_q1",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdownOld()
	_, newConn, shutdownNew := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_q0",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdownNew()
	defer newConn.Close()

	if _, err := oldClient.Join(ctx, &cellv1.JoinRequest{PlayerId: "player-1"}); err != nil {
		t.Fatalf("old join: %v", err)
	}
	regClient, shutdownReg := startTestRegistryServer(t, map[string]string{
		"cell_q1": oldConn.Target(),
		"cell_q0": newConn.Target(),
	})
	defer shutdownReg()

	session := &gatewaySession{playerID: "player-1"}
	session.setDownstream(&gatewayDownstream{
		cellID: "cell_q1",
		conn:   oldConn,
		client: oldClient,
	})
	session.setPosition(200, 0) // registry test server maps x >= 0 to q0

	g := &gateway{
		positionSwitchEnabled:       true,
		positionSwitchMinInterval:   0,
		positionSwitchMinMoveMeters: 0,
	}
	nextDS, switched, err := g.trySwitchDownstreamByPosition(ctx, otel.Tracer("test"), regClient, session)
	if err != nil {
		t.Fatalf("proactive switch error: %v", err)
	}
	if !switched || nextDS == nil {
		t.Fatalf("expected switched downstream, got switched=%v next=%v", switched, nextDS)
	}
	if nextDS.cellID != "cell_q0" {
		t.Fatalf("expected switch to cell_q0, got %s", nextDS.cellID)
	}
}

// После grid split игрок уже на дочерней соте, а WS-сессия гейтвея ещё смотрит на родителя — проактивный switch должен
// подцепиться через Join(already_joined), не вызывая Prepare на родителе (там unknown_player).
func TestTrySwitchDownstreamByPosition_AlreadyOnTargetCellAfterSplitHandoff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldClient, oldConn, shutdownOld := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_q1",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdownOld()
	_, newConn, shutdownNew := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_q0",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdownNew()

	if _, err := oldClient.Join(ctx, &cellv1.JoinRequest{PlayerId: "player-1"}); err != nil {
		t.Fatalf("old join: %v", err)
	}
	regClient, shutdownReg := startTestRegistryServer(t, map[string]string{
		"cell_q1": oldConn.Target(),
		"cell_q0": newConn.Target(),
	})
	defer shutdownReg()

	handoff, err := regClient.ForwardPlayerHandoff(ctx, &cellv1.ForwardPlayerHandoffRequest{
		ParentCellId: "cell_q1",
		ChildCellId:  "cell_q0",
		PlayerId:     "player-1",
		HandoffToken: "test-grid-handoff-token",
		Reason:       "test_split",
	})
	if err != nil || handoff == nil || !handoff.GetOk() || handoff.GetChildEntityId() == 0 {
		t.Fatalf("forward handoff: %+v err=%v", handoff, err)
	}

	session := &gatewaySession{playerID: "player-1"}
	session.setDownstream(&gatewayDownstream{
		cellID: "cell_q1",
		conn:   oldConn,
		client: oldClient,
	})
	session.setPosition(200, 0) // x >= 0 -> cell_q0 в тестовом registry

	g := &gateway{
		positionSwitchEnabled:         true,
		positionSwitchMinInterval:     0,
		positionSwitchMinMoveMeters:   0,
	}
	nextDS, switched, err := g.trySwitchDownstreamByPosition(ctx, otel.Tracer("test"), regClient, session)
	if err != nil {
		t.Fatalf("proactive switch error: %v", err)
	}
	if !switched || nextDS == nil {
		t.Fatalf("expected switched downstream, got switched=%v next=%v", switched, nextDS)
	}
	if nextDS.cellID != "cell_q0" {
		t.Fatalf("expected switch to cell_q0, got %s", nextDS.cellID)
	}
	if nextDS.entityID != handoff.GetChildEntityId() {
		t.Fatalf("entity_id: want %d got %d", handoff.GetChildEntityId(), nextDS.entityID)
	}
}

func TestTrySwitchDownstreamByPosition_NoSwitchWhenSameCell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldClient, oldConn, shutdownOld := startTestCellServer(t, &cellsvc.Server{
		CellID: "cell_q1",
		Sim:    cellsim.NewRuntime(),
	})
	defer shutdownOld()
	regClient, shutdownReg := startTestRegistryServer(t, map[string]string{
		"cell_q1": oldConn.Target(),
	})
	defer shutdownReg()

	session := &gatewaySession{playerID: "player-1"}
	session.setDownstream(&gatewayDownstream{
		cellID: "cell_q1",
		conn:   oldConn,
		client: oldClient,
	})
	session.setPosition(-10, 0) // registry test server maps x < 0 to q1

	g := &gateway{
		positionSwitchEnabled:       true,
		positionSwitchMinInterval:   0,
		positionSwitchMinMoveMeters: 0,
	}
	nextDS, switched, err := g.trySwitchDownstreamByPosition(ctx, otel.Tracer("test"), regClient, session)
	if err != nil {
		t.Fatalf("proactive switch error: %v", err)
	}
	if switched || nextDS != nil {
		t.Fatalf("expected no switch, got switched=%v next=%v", switched, nextDS)
	}
}

type testRegistryServer struct {
	cellv1.UnimplementedRegistryServer
	endpoints map[string]string
}

func (s *testRegistryServer) ResolvePosition(_ context.Context, req *cellv1.ResolvePositionRequest) (*cellv1.ResolvePositionResponse, error) {
	cellID := "cell_q1"
	if req.GetX() >= 0 {
		cellID = "cell_q0"
	}
	ep, ok := s.endpoints[cellID]
	if !ok {
		return nil, fmt.Errorf("missing endpoint for %s", cellID)
	}
	return &cellv1.ResolvePositionResponse{
		Found: true,
		Cell: &cellv1.CellSpec{
			Id:           cellID,
			GrpcEndpoint: ep,
			Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		},
	}, nil
}

func (s *testRegistryServer) ForwardPlayerHandoff(ctx context.Context, req *cellv1.ForwardPlayerHandoffRequest) (*cellv1.ForwardPlayerHandoffResponse, error) {
	parentID := strings.TrimSpace(req.GetParentCellId())
	childID := strings.TrimSpace(req.GetChildCellId())
	playerID := strings.TrimSpace(req.GetPlayerId())
	token := strings.TrimSpace(req.GetHandoffToken())
	if parentID == "" || childID == "" || playerID == "" || token == "" {
		return nil, fmt.Errorf("invalid handoff request")
	}
	parentEndpoint, ok := s.endpoints[parentID]
	if !ok {
		return nil, fmt.Errorf("missing parent endpoint for %s", parentID)
	}
	childEndpoint, ok := s.endpoints[childID]
	if !ok {
		return nil, fmt.Errorf("missing child endpoint for %s", childID)
	}
	parentConn, err := grpc.NewClient(parentEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer parentConn.Close()
	childConn, err := grpc.NewClient(childEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer childConn.Close()
	parentCl := cellv1.NewCellClient(parentConn)
	childCl := cellv1.NewCellClient(childConn)
	prep, err := parentCl.PreparePlayerHandoff(ctx, &cellv1.PreparePlayerHandoffRequest{
		PlayerId:     playerID,
		TargetCellId: childID,
		HandoffToken: token,
	})
	if err != nil {
		return nil, err
	}
	if prep == nil || !prep.GetOk() || prep.GetPayload() == nil {
		return nil, fmt.Errorf("prepare failed")
	}
	acc, err := childCl.AcceptPlayerHandoff(ctx, &cellv1.AcceptPlayerHandoffRequest{Payload: prep.GetPayload()})
	if err != nil {
		return nil, err
	}
	if acc == nil || !acc.GetOk() || acc.GetEntityId() == 0 {
		return nil, fmt.Errorf("accept failed")
	}
	fin, err := parentCl.FinalizePlayerHandoff(ctx, &cellv1.FinalizePlayerHandoffRequest{
		PlayerId:     playerID,
		HandoffToken: token,
	})
	if err != nil {
		return nil, err
	}
	if fin == nil || !fin.GetOk() {
		return nil, fmt.Errorf("finalize failed")
	}
	return &cellv1.ForwardPlayerHandoffResponse{
		Ok:            true,
		Message:       "ok",
		ChildEntityId: acc.GetEntityId(),
	}, nil
}

func startTestCellServer(t *testing.T, srv cellv1.CellServer) (cellv1.CellClient, *grpc.ClientConn, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	cellv1.RegisterCellServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcSrv.Stop()
		_ = lis.Close()
		t.Fatalf("dial cell: %v", err)
	}

	client := cellv1.NewCellClient(conn)
	shutdown := func() {
		_ = conn.Close()
		stopped := make(chan struct{})
		go func() {
			grpcSrv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			grpcSrv.Stop()
		}
		_ = lis.Close()
	}
	return client, conn, shutdown
}

func startTestRegistryServer(t *testing.T, endpoints map[string]string) (cellv1.RegistryClient, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("registry listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	cellv1.RegisterRegistryServer(grpcSrv, &testRegistryServer{endpoints: endpoints})
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcSrv.Stop()
		_ = lis.Close()
		t.Fatalf("registry dial: %v", err)
	}
	client := cellv1.NewRegistryClient(conn)
	shutdown := func() {
		_ = conn.Close()
		stopped := make(chan struct{})
		go func() {
			grpcSrv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			grpcSrv.Stop()
		}
		_ = lis.Close()
	}
	return client, shutdown
}
