package registrysvc

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/discovery"
	"mmo/internal/partition"
	"mmo/internal/registry"
)

type mergeCell struct {
	cellv1.UnimplementedCellServer
	exportJSON string
	lastImport string
}

func (m *mergeCell) Ping(_ context.Context, _ *cellv1.PingRequest) (*cellv1.PingResponse, error) {
	return &cellv1.PingResponse{CellId: "test", PlayerCount: 0, EntityCount: 0}, nil
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
		Id:           "cell_0_0_0",
		Level:        0,
		Bounds:       &cellv1.Bounds{XMin: -1000, XMax: 1000, ZMin: -1000, ZMax: 1000},
		GrpcEndpoint: parentLis.Addr().String(),
	}
	if err := mem.RegisterCell(ctx, parent); err != nil {
		t.Fatal(err)
	}

	childrenPlan := partition.ChildSpecsForSplit(parent.GetBounds(), parent.GetLevel())
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
