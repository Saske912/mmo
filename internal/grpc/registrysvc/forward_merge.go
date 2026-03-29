package registrysvc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/cellsim/snapshot"
	"mmo/internal/discovery"
	"mmo/internal/partition"
)

const forwardMergeHandoffTimeout = 90 * time.Second

type mergeWorkflowEvent struct {
	ParentCellID string `json:"parent_cell_id"`
	Stage        string `json:"stage"`
	Message      string `json:"message"`
	ChildCellID  string `json:"child_cell_id,omitempty"`
	AtUnixMs     int64  `json:"at_unix_ms"`
	Successful   bool   `json:"successful"`
}

func (s *Server) publishMergeWorkflowEvent(evt mergeWorkflowEvent) {
	if s.NATS == nil {
		return
	}
	b, err := json.Marshal(evt)
	if err != nil {
		return
	}
	_ = s.NATS.Publish(natsbus.SubjectGridMergeWorkflow, b)
	_ = s.NATS.Flush()
}

func uniqueCellIDs(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ForwardMergeHandoff экспортирует NPC со списка child и импортирует объединённый persist в parent.
// MVP: topology switch/teardown child остаётся операторским шагом после handoff.
func (s *Server) ForwardMergeHandoff(ctx context.Context, req *cellv1.ForwardMergeHandoffRequest) (*cellv1.ForwardMergeHandoffResponse, error) {
	start := time.Now()
	defer observeMergeWorkflowDuration(start)
	defer func() { observeRPCDuration("ForwardMergeHandoff", start) }()
	ctx, span := otel.Tracer("mmo/grid-manager").Start(ctx, "Registry.ForwardMergeHandoff")
	defer span.End()
	if req == nil {
		e := status.Error(codes.InvalidArgument, "empty request")
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}
	parentID := strings.TrimSpace(req.GetParentCellId())
	childIDs := uniqueCellIDs(req.GetChildCellIds())
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		reason = "ForwardMergeHandoff"
	}
	span.SetAttributes(attribute.String("parent_cell_id", parentID), attribute.Int("child_count", len(childIDs)))
	logOpStart("ForwardMergeHandoff", "parent_cell_id", parentID, "child_count", len(childIDs), "reason", reason)
	s.publishMergeWorkflowEvent(mergeWorkflowEvent{
		ParentCellID: parentID,
		Stage:        "detected",
		Message:      "merge workflow detected",
		AtUnixMs:     time.Now().UnixMilli(),
	})
	if parentID == "" {
		e := status.Error(codes.InvalidArgument, "parent_cell_id required")
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: e.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, e
	}
	if len(childIDs) != 4 {
		e := status.Errorf(codes.InvalidArgument, "need exactly 4 unique child_cell_ids, got %d", len(childIDs))
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: e.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, e
	}
	if s.Store == nil {
		e := status.Error(codes.FailedPrecondition, "no catalog")
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: e.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, e
	}

	hctx, cancel := context.WithTimeout(ctx, forwardMergeHandoffTimeout)
	defer cancel()
	parentSpec, ok, err := discovery.FindCellByID(hctx, s.Store, parentID)
	if err != nil {
		incRPC("ForwardMergeHandoff", err)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	if !ok || parentSpec == nil || parentSpec.GetBounds() == nil {
		e := status.Errorf(codes.NotFound, "parent not found or has no bounds: %s", parentID)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}
	children := make([]*cellv1.CellSpec, 0, len(childIDs))
	for _, cid := range childIDs {
		cs, found, ferr := discovery.FindCellByID(hctx, s.Store, cid)
		if ferr != nil {
			incRPC("ForwardMergeHandoff", ferr)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			return nil, status.Error(codes.Unavailable, ferr.Error())
		}
		if !found || cs == nil {
			e := status.Errorf(codes.NotFound, "child not found: %s", cid)
			incRPC("ForwardMergeHandoff", e)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			return nil, e
		}
		children = append(children, cs)
	}
	if err := partition.ValidateMergeChildren(parentSpec.GetBounds(), parentSpec.GetLevel(), children); err != nil {
		e := status.Errorf(codes.FailedPrecondition, "merge geometry invalid: %v", err)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}

	// Включаем drain на child и гарантируем best-effort rollback.
	for _, cid := range childIDs {
		_, _ = s.doForwardCellUpdate(hctx, cid, &cellv1.UpdateRequest{
			Payload: &cellv1.UpdateRequest_SetSplitDrain{
				SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: true},
			},
		})
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{
			ParentCellID: parentID,
			ChildCellID:  cid,
			Stage:        "child_drain_enabled",
			Message:      "split_drain enabled",
			AtUnixMs:     time.Now().UnixMilli(),
		})
	}
	defer func() {
		for _, cid := range childIDs {
			_, _ = s.doForwardCellUpdate(context.Background(), cid, &cellv1.UpdateRequest{
				Payload: &cellv1.UpdateRequest_SetSplitDrain{
					SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: false},
				},
			})
		}
	}()

	merged := &gamev1.CellPersist{SchemaVersion: snapshot.SchemaVersion}
	totalNPC := 0
	for _, cid := range childIDs {
		exp, err := s.doForwardCellUpdate(hctx, cid, &cellv1.UpdateRequest{
			Payload: &cellv1.UpdateRequest_ExportNpcPersist{
				ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: reason + "/export/" + cid},
			},
		})
		if err != nil {
			incRPC("ForwardMergeHandoff", err)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, ChildCellID: cid, Stage: "failed", Message: err.Error(), AtUnixMs: time.Now().UnixMilli()})
			return nil, err
		}
		if exp == nil || !exp.GetOk() || strings.TrimSpace(exp.GetNpcExportJson()) == "" {
			e := status.Errorf(codes.FailedPrecondition, "child export failed: %s (%s)", cid, exp.GetMessage())
			incRPC("ForwardMergeHandoff", e)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, ChildCellID: cid, Stage: "failed", Message: e.Error(), AtUnixMs: time.Now().UnixMilli()})
			return nil, e
		}
		var p gamev1.CellPersist
		if err := protojson.Unmarshal([]byte(exp.GetNpcExportJson()), &p); err != nil {
			e := status.Errorf(codes.Internal, "child export json decode %s: %v", cid, err)
			incRPC("ForwardMergeHandoff", e)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			return nil, e
		}
		totalNPC += len(p.GetEntities())
		merged.Entities = append(merged.Entities, p.GetEntities()...)
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{
			ParentCellID: parentID,
			ChildCellID:  cid,
			Stage:        "child_exported",
			Message:      "child export ok",
			AtUnixMs:     time.Now().UnixMilli(),
		})
	}
	raw, err := protojson.Marshal(merged)
	if err != nil {
		e := status.Errorf(codes.Internal, "merge marshal: %v", err)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}
	imp, err := s.doForwardCellUpdate(hctx, parentID, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_ImportNpcPersist{
			ImportNpcPersist: &cellv1.CellUpdateImportNpcPersist{
				NpcImportJson: string(raw),
				Reason:        reason + "/import_parent",
			},
		},
	})
	if err != nil {
		incRPC("ForwardMergeHandoff", err)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: err.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, err
	}
	if imp == nil || !imp.GetOk() {
		e := status.Errorf(codes.FailedPrecondition, "parent import failed: %s", imp.GetMessage())
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: e.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, e
	}
	incRPC("ForwardMergeHandoff", nil)
	mergeWorkflowRunsTotal.WithLabelValues("ok").Inc()
	s.publishMergeWorkflowEvent(mergeWorkflowEvent{
		ParentCellID: parentID,
		Stage:        "done",
		Message:      "merge handoff completed",
		AtUnixMs:     time.Now().UnixMilli(),
		Successful:   true,
	})
	logOpDone("ForwardMergeHandoff", "parent_cell_id", parentID, "child_count", len(childIDs), "npc_entities", totalNPC)
	return &cellv1.ForwardMergeHandoffResponse{
		Ok:             true,
		Message:        "merge handoff ok: children exported and parent imported",
		ChildCount:     int32(len(childIDs)),
		NpcEntityCount: int32(totalNPC),
	}, nil
}
