package main

import (
	"context"
	"net"
	"testing"
	"time"

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
