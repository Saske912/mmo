package registrysvc

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
)

const forwardPlayerHandoffTimeout = 30 * time.Second

func (s *Server) ForwardPlayerHandoff(ctx context.Context, req *cellv1.ForwardPlayerHandoffRequest) (*cellv1.ForwardPlayerHandoffResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ForwardPlayerHandoff", start) }()
	ctx, span := otel.Tracer("mmo/grid-manager").Start(ctx, "Registry.ForwardPlayerHandoff")
	defer span.End()
	if req == nil {
		e := status.Error(codes.InvalidArgument, "empty request")
		incRPC("ForwardPlayerHandoff", e)
		observeForwardPlayerHandoffStage("validate", e)
		logOpError("ForwardPlayerHandoff", e)
		return nil, e
	}
	parentID := strings.TrimSpace(req.GetParentCellId())
	childID := strings.TrimSpace(req.GetChildCellId())
	playerID := strings.TrimSpace(req.GetPlayerId())
	token := strings.TrimSpace(req.GetHandoffToken())
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		reason = "ForwardPlayerHandoff"
	}
	span.SetAttributes(
		attribute.String("parent_cell_id", parentID),
		attribute.String("child_cell_id", childID),
		attribute.String("player_id", playerID),
	)
	logOpStart("ForwardPlayerHandoff", "parent_cell_id", parentID, "child_cell_id", childID, "player_id", playerID, "reason", reason)
	if parentID == "" || childID == "" || playerID == "" || token == "" {
		e := status.Error(codes.InvalidArgument, "parent_cell_id, child_cell_id, player_id, handoff_token required")
		incRPC("ForwardPlayerHandoff", e)
		observeForwardPlayerHandoffStage("validate", e)
		logOpError("ForwardPlayerHandoff", e)
		return nil, e
	}
	if parentID == childID {
		e := status.Error(codes.InvalidArgument, "parent_cell_id must differ from child_cell_id")
		incRPC("ForwardPlayerHandoff", e)
		observeForwardPlayerHandoffStage("validate", e)
		logOpError("ForwardPlayerHandoff", e)
		return nil, e
	}
	observeForwardPlayerHandoffStage("validate", nil)

	hctx, cancel := context.WithTimeout(ctx, forwardPlayerHandoffTimeout)
	defer cancel()

	parentConn, parentCell, parentSpec, err := s.dialCellClient(hctx, parentID)
	if err != nil {
		incRPC("ForwardPlayerHandoff", err)
		observeForwardPlayerHandoffStage("dial_parent", err)
		logOpError("ForwardPlayerHandoff", err, "stage", "dial_parent", "parent_cell_id", parentID)
		return nil, err
	}
	observeForwardPlayerHandoffStage("dial_parent", nil)
	defer parentConn.Close()
	childConn, childCell, childSpec, err := s.dialCellClient(hctx, childID)
	if err != nil {
		incRPC("ForwardPlayerHandoff", err)
		observeForwardPlayerHandoffStage("dial_child", err)
		logOpError("ForwardPlayerHandoff", err, "stage", "dial_child", "child_cell_id", childID)
		return nil, err
	}
	observeForwardPlayerHandoffStage("dial_child", nil)
	defer childConn.Close()

	prepResp, err := parentCell.PreparePlayerHandoff(hctx, &cellv1.PreparePlayerHandoffRequest{
		PlayerId:     playerID,
		TargetCellId: childID,
		HandoffToken: token,
	})
	if err != nil {
		incRPC("ForwardPlayerHandoff", err)
		observeForwardPlayerHandoffStage("prepare", err)
		logOpError("ForwardPlayerHandoff", err, "stage", "prepare", "parent_cell_id", parentID, "player_id", playerID)
		return nil, err
	}
	if prepResp == nil || !prepResp.GetOk() || prepResp.GetPayload() == nil {
		msg := ""
		if prepResp != nil {
			msg = prepResp.GetMessage()
		}
		e := status.Errorf(codes.FailedPrecondition, "prepare failed: %s", msg)
		incRPC("ForwardPlayerHandoff", e)
		observeForwardPlayerHandoffStage("prepare", e)
		logOpError("ForwardPlayerHandoff", e, "stage", "prepare", "parent_cell_id", parentID, "player_id", playerID)
		return nil, e
	}
	observeForwardPlayerHandoffStage("prepare", nil)

	payload := prepResp.GetPayload()
	payload.SourceCellId = parentSpec.GetId()
	payload.TargetCellId = childSpec.GetId()
	payload.HandoffToken = token
	acceptResp, err := childCell.AcceptPlayerHandoff(hctx, &cellv1.AcceptPlayerHandoffRequest{Payload: payload})
	if err != nil {
		incRPC("ForwardPlayerHandoff", err)
		observeForwardPlayerHandoffStage("accept", err)
		logOpError("ForwardPlayerHandoff", err, "stage", "accept", "child_cell_id", childID, "player_id", playerID)
		return nil, err
	}
	if acceptResp == nil || !acceptResp.GetOk() || acceptResp.GetEntityId() == 0 {
		msg := ""
		if acceptResp != nil {
			msg = acceptResp.GetMessage()
		}
		e := status.Errorf(codes.FailedPrecondition, "accept failed: %s", msg)
		incRPC("ForwardPlayerHandoff", e)
		observeForwardPlayerHandoffStage("accept", e)
		logOpError("ForwardPlayerHandoff", e, "stage", "accept", "child_cell_id", childID, "player_id", playerID)
		return nil, e
	}
	observeForwardPlayerHandoffStage("accept", nil)

	finalResp, err := parentCell.FinalizePlayerHandoff(hctx, &cellv1.FinalizePlayerHandoffRequest{
		PlayerId:     playerID,
		HandoffToken: token,
	})
	if err != nil {
		incRPC("ForwardPlayerHandoff", err)
		observeForwardPlayerHandoffStage("finalize", err)
		logOpError("ForwardPlayerHandoff", err, "stage", "finalize", "parent_cell_id", parentID, "player_id", playerID)
		return nil, err
	}
	if finalResp == nil || !finalResp.GetOk() {
		msg := ""
		if finalResp != nil {
			msg = finalResp.GetMessage()
		}
		e := status.Errorf(codes.FailedPrecondition, "finalize failed: %s", msg)
		incRPC("ForwardPlayerHandoff", e)
		observeForwardPlayerHandoffStage("finalize", e)
		logOpError("ForwardPlayerHandoff", e, "stage", "finalize", "parent_cell_id", parentID, "player_id", playerID)
		return nil, e
	}
	observeForwardPlayerHandoffStage("finalize", nil)

	incRPC("ForwardPlayerHandoff", nil)
	logOpDone("ForwardPlayerHandoff", "parent_cell_id", parentID, "child_cell_id", childID, "player_id", playerID, "child_entity_id", acceptResp.GetEntityId())
	return &cellv1.ForwardPlayerHandoffResponse{
		Ok:            true,
		Message:       "player handoff completed",
		ChildEntityId: acceptResp.GetEntityId(),
	}, nil
}
