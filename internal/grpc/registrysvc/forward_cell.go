package registrysvc

import (
	"context"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/discovery"
)

const forwardNpcHandoffTimeout = 45 * time.Second

// doForwardCellUpdate выполняет Update на соте по cell_id (для переиспользования из ForwardCellUpdate и ForwardNpcHandoff).
func (s *Server) doForwardCellUpdate(ctx context.Context, cellID string, upd *cellv1.UpdateRequest) (*cellv1.ForwardCellUpdateResponse, error) {
	if upd == nil {
		return nil, status.Error(codes.InvalidArgument, "nil update")
	}
	if cellID == "" {
		return nil, status.Error(codes.InvalidArgument, "empty cell_id")
	}
	if s.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, "no catalog")
	}
	spec, ok, err := discovery.FindCellByID(ctx, s.Store, cellID)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	if !ok || spec == nil {
		return nil, status.Errorf(codes.NotFound, "cell not found: %s", cellID)
	}
	ep := spec.GetGrpcEndpoint()
	if ep == "" {
		return nil, status.Error(codes.FailedPrecondition, "cell has empty grpc_endpoint")
	}
	conn, err := grpc.NewClient(ep,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "dial cell: %v", err)
	}
	defer conn.Close()
	cellCtx, cellCancel := context.WithTimeout(ctx, forwardCellDialTimeout)
	defer cellCancel()
	cellClient := cellv1.NewCellClient(conn)
	updResp, err := cellClient.Update(cellCtx, upd)
	if err != nil {
		return nil, err
	}
	if updResp == nil {
		return nil, status.Error(codes.Internal, "nil UpdateResponse")
	}
	return &cellv1.ForwardCellUpdateResponse{
		Ok:            updResp.Ok,
		Message:       updResp.Message,
		NpcExportJson: updResp.GetNpcExportJson(),
	}, nil
}

// ForwardNpcHandoff экспортирует NPC с parent и импортирует на child.
func (s *Server) ForwardNpcHandoff(ctx context.Context, req *cellv1.ForwardNpcHandoffRequest) (*cellv1.ForwardNpcHandoffResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ForwardNpcHandoff", start) }()
	ctx, span := otel.Tracer("mmo/grid-manager").Start(ctx, "Registry.ForwardNpcHandoff")
	defer span.End()
	if req == nil {
		e := status.Error(codes.InvalidArgument, "empty request")
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}
	parent := req.GetParentCellId()
	child := req.GetChildCellId()
	span.SetAttributes(attribute.String("parent_cell_id", parent), attribute.String("child_cell_id", child))
	reason := req.GetReason()
	if reason == "" {
		reason = "ForwardNpcHandoff"
	}
	if parent == "" || child == "" {
		e := status.Error(codes.InvalidArgument, "parent_cell_id and child_cell_id required")
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}
	if parent == child {
		e := status.Error(codes.InvalidArgument, "parent_cell_id must differ from child_cell_id")
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}

	hctx, cancel := context.WithTimeout(ctx, forwardNpcHandoffTimeout)
	defer cancel()

	expResp, err := s.doForwardCellUpdate(hctx, parent, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ExportNpcPersist{
			ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: reason},
		},
	})
	if err != nil {
		incRPC("ForwardNpcHandoff", err)
		return nil, err
	}
	if expResp == nil || !expResp.Ok {
		pmsg := ""
		if expResp != nil {
			pmsg = expResp.GetMessage()
		}
		e := status.Errorf(codes.FailedPrecondition, "parent export failed: %s", pmsg)
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}
	json := expResp.GetNpcExportJson()
	if json == "" {
		e := status.Error(codes.FailedPrecondition, "empty npc_export_json from parent")
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}
	var p gamev1.CellPersist
	if err := protojson.Unmarshal([]byte(json), &p); err != nil {
		e := status.Errorf(codes.Internal, "parent export json: %v", err)
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}
	nEnt := int32(len(p.Entities))

	impResp, err := s.doForwardCellUpdate(hctx, child, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ImportNpcPersist{
			ImportNpcPersist: &cellv1.CellUpdateImportNpcPersist{
				NpcImportJson: json,
				Reason:        reason + "/import",
			},
		},
	})
	if err != nil {
		incRPC("ForwardNpcHandoff", err)
		return nil, err
	}
	if impResp == nil || !impResp.Ok {
		cmsg := ""
		if impResp != nil {
			cmsg = impResp.GetMessage()
		}
		e := status.Errorf(codes.FailedPrecondition, "child import failed: %s", cmsg)
		incRPC("ForwardNpcHandoff", e)
		return nil, e
	}

	msg := "handoff: parent export ok; child import ok (" + expResp.Message + " -> " + impResp.Message + ")"
	incRPC("ForwardNpcHandoff", nil)
	return &cellv1.ForwardNpcHandoffResponse{
		Ok:               true,
		Message:          msg,
		NpcEntityCount: nEnt,
	}, nil
}
