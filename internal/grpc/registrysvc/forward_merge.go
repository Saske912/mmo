package registrysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
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
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/cellsim/snapshot"
	"mmo/internal/config"
	"mmo/internal/discovery"
	"mmo/internal/partition"
	"mmo/internal/splitcontrol"
)

const forwardMergeHandoffTimeout = 90 * time.Second
const mergeAutomationRedisTTL = 7 * 24 * time.Hour

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

func mergePlayerHandoffEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("MMO_GRID_MERGE_PLAYER_HANDOFF"))
	return strings.EqualFold(raw, "1") || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes")
}

func mergePlayerHandoffMaxPlayers() int {
	raw := strings.TrimSpace(os.Getenv("MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS"))
	if raw == "" {
		return 32
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 32
	}
	return n
}

type mergePlayerCandidate struct {
	childID  string
	playerID string
}

func (s *Server) pingPlayerCount(ctx context.Context, cellID string) (int32, error) {
	spec, ok, err := discovery.FindCellByID(ctx, s.Store, cellID)
	if err != nil {
		return 0, status.Error(codes.Unavailable, err.Error())
	}
	if !ok || spec == nil || strings.TrimSpace(spec.GetGrpcEndpoint()) == "" {
		return 0, status.Errorf(codes.NotFound, "cell not found or endpoint empty: %s", cellID)
	}
	conn, err := grpc.NewClient(spec.GetGrpcEndpoint(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return 0, status.Errorf(codes.Unavailable, "dial %s: %v", cellID, err)
	}
	defer conn.Close()
	pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	res, err := cellv1.NewCellClient(conn).Ping(pctx, &cellv1.PingRequest{ClientId: "grid-merge-guard"})
	if err != nil {
		return 0, err
	}
	return res.GetPlayerCount(), nil
}

func (s *Server) saveMergeAutomationState(ctx context.Context, parentID string, childIDs []string, movedPlayers int, handoffEnabled bool) error {
	cfg := config.FromEnv()
	if cfg.RedisAddr == "" {
		return nil
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       0,
	})
	defer rdb.Close()
	state := map[string]any{
		"phase":                   splitcontrol.RetireStatePhaseAutomationComplete,
		"parent_cell_id":          parentID,
		"removed_children":        childIDs,
		"topology_switched":       true,
		"runtime_teardown_queued": true,
		"player_handoff_enabled":  handoffEnabled,
		"player_handoff_count":    movedPlayers,
		"at_unix_ms":              time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return status.Errorf(codes.Internal, "merge state json: %v", err)
	}
	key := "mmo:grid:merge:state:" + strings.TrimSpace(parentID)
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Set(wctx, key, raw, mergeAutomationRedisTTL).Err(); err != nil {
		return status.Errorf(codes.Unavailable, "redis merge state: %v", err)
	}
	return nil
}

func (s *Server) listMergePlayerCandidates(ctx context.Context, childID string) ([]mergePlayerCandidate, error) {
	conn, cl, _, err := s.dialCellClient(ctx, childID)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	resp, err := cl.ListMigrationCandidates(cctx, &cellv1.ListMigrationCandidatesRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]mergePlayerCandidate, 0, len(resp.GetCandidates()))
	for _, cand := range resp.GetCandidates() {
		if cand == nil || !cand.GetIsPlayer() {
			continue
		}
		pid := strings.TrimSpace(cand.GetPlayerId())
		if pid == "" {
			continue
		}
		out = append(out, mergePlayerCandidate{childID: childID, playerID: pid})
	}
	return out, nil
}

func (s *Server) runMergePlayerHandoffs(ctx context.Context, parentID string, childIDs []string, reason string) (int, error) {
	candidates := make([]mergePlayerCandidate, 0, 16)
	for _, childID := range childIDs {
		cands, err := s.listMergePlayerCandidates(ctx, childID)
		if err != nil {
			return 0, err
		}
		candidates = append(candidates, cands...)
	}
	if len(candidates) == 0 {
		return 0, nil
	}
	s.publishMergeWorkflowEvent(mergeWorkflowEvent{
		ParentCellID: parentID,
		Stage:        "player_handoffs_running",
		Message:      fmt.Sprintf("player handoffs started: %d", len(candidates)),
		AtUnixMs:     time.Now().UnixMilli(),
	})
	for _, c := range candidates {
		token := fmt.Sprintf("merge:%s:%s:%d", c.childID, c.playerID, time.Now().UnixNano())
		_, err := s.ForwardPlayerHandoff(ctx, &cellv1.ForwardPlayerHandoffRequest{
			ParentCellId: c.childID,
			ChildCellId:  parentID,
			PlayerId:     c.playerID,
			HandoffToken: token,
			Reason:       reason + "/player_handoff/" + c.childID,
		})
		if err != nil {
			s.publishMergeWorkflowEvent(mergeWorkflowEvent{
				ParentCellID: parentID,
				ChildCellID:  c.childID,
				Stage:        "player_handoff_failed",
				Message:      err.Error(),
				AtUnixMs:     time.Now().UnixMilli(),
			})
			return 0, err
		}
	}
	s.publishMergeWorkflowEvent(mergeWorkflowEvent{
		ParentCellID: parentID,
		Stage:        "player_handoffs_done",
		Message:      fmt.Sprintf("player handoffs completed: %d", len(candidates)),
		AtUnixMs:     time.Now().UnixMilli(),
		Successful:   true,
	})
	return len(candidates), nil
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
	if !s.tryAcquireMerge(parentID) {
		e := status.Errorf(codes.FailedPrecondition, "merge already in progress for parent: %s", parentID)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "skipped_reentry", Message: e.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, e
	}
	defer s.releaseMerge(parentID)
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
	playerHandoffEnabled := mergePlayerHandoffEnabled()
	playerHandoffMax := mergePlayerHandoffMaxPlayers()
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
	allCells, lerr := s.Store.List(hctx)
	if lerr != nil {
		incRPC("ForwardMergeHandoff", lerr)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, status.Error(codes.Unavailable, lerr.Error())
	}
	children, err := partition.CatalogMergeChildren(parentSpec, allCells)
	if err != nil {
		e := status.Errorf(codes.FailedPrecondition, "merge children resolve: %v", err)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}
	resolvedSet := make(map[string]struct{}, len(children))
	orderedChildIDs := make([]string, 0, len(children))
	for _, ch := range children {
		id := strings.TrimSpace(ch.GetId())
		resolvedSet[id] = struct{}{}
		orderedChildIDs = append(orderedChildIDs, id)
	}
	for _, id := range childIDs {
		if _, ok := resolvedSet[id]; !ok {
			e := status.Errorf(codes.InvalidArgument, "child_cell_ids mismatch catalog merge quadrants: %s", id)
			incRPC("ForwardMergeHandoff", e)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			return nil, e
		}
	}
	if err := partition.ValidateMergeChildren(parentSpec.GetBounds(), parentSpec.GetLevel(), children); err != nil {
		e := status.Errorf(codes.FailedPrecondition, "merge geometry invalid: %v", err)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}
	parentPlayers, err := s.pingPlayerCount(hctx, parentID)
	if err != nil {
		incRPC("ForwardMergeHandoff", err)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, err
	}
	if parentPlayers > 0 {
		e := status.Errorf(codes.FailedPrecondition, "merge blocked: parent has active players=%d", parentPlayers)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}
	totalChildPlayers := 0
	for _, cid := range orderedChildIDs {
		players, err := s.pingPlayerCount(hctx, cid)
		if err != nil {
			incRPC("ForwardMergeHandoff", err)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			return nil, err
		}
		totalChildPlayers += int(players)
		if !playerHandoffEnabled && players > 0 {
			e := status.Errorf(codes.FailedPrecondition, "merge blocked: child %s has active players=%d", cid, players)
			incRPC("ForwardMergeHandoff", e)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			return nil, e
		}
	}
	if playerHandoffEnabled && playerHandoffMax > 0 && totalChildPlayers > playerHandoffMax {
		e := status.Errorf(codes.FailedPrecondition, "merge player handoff blocked: active players=%d > max=%d", totalChildPlayers, playerHandoffMax)
		incRPC("ForwardMergeHandoff", e)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		return nil, e
	}

	// Parent также переводим в drain на merge-окно.
	_, _ = s.doForwardCellUpdate(hctx, parentID, &cellv1.UpdateRequest{
		Payload: &cellv1.UpdateRequest_SetSplitDrain{
			SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: true},
		},
	})
	defer func() {
		_, _ = s.doForwardCellUpdate(context.Background(), parentID, &cellv1.UpdateRequest{
			Payload: &cellv1.UpdateRequest_SetSplitDrain{
				SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: false},
			},
		})
	}()

	// Включаем drain на child и гарантируем best-effort rollback.
	for _, cid := range orderedChildIDs {
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
		for _, cid := range orderedChildIDs {
			_, _ = s.doForwardCellUpdate(context.Background(), cid, &cellv1.UpdateRequest{
				Payload: &cellv1.UpdateRequest_SetSplitDrain{
					SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: false},
				},
			})
		}
	}()

	merged := &gamev1.CellPersist{SchemaVersion: snapshot.SchemaVersion}
	totalNPC := 0
	for _, cid := range orderedChildIDs {
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
	movedPlayers := 0
	if playerHandoffEnabled && totalChildPlayers > 0 {
		movedPlayers, err = s.runMergePlayerHandoffs(hctx, parentID, orderedChildIDs, reason)
		if err != nil {
			incRPC("ForwardMergeHandoff", err)
			mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
			s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: err.Error(), AtUnixMs: time.Now().UnixMilli()})
			return nil, err
		}
	}
	if err := s.saveMergeAutomationState(hctx, parentID, orderedChildIDs, movedPlayers, playerHandoffEnabled); err != nil {
		incRPC("ForwardMergeHandoff", err)
		mergeWorkflowRunsTotal.WithLabelValues("error").Inc()
		s.publishMergeWorkflowEvent(mergeWorkflowEvent{ParentCellID: parentID, Stage: "failed", Message: err.Error(), AtUnixMs: time.Now().UnixMilli()})
		return nil, err
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
	logOpDone("ForwardMergeHandoff", "parent_cell_id", parentID, "child_count", len(orderedChildIDs), "npc_entities", totalNPC, "moved_players", movedPlayers)
	return &cellv1.ForwardMergeHandoffResponse{
		Ok:             true,
		Message:        fmt.Sprintf("merge handoff ok: children exported/imported, moved_players=%d", movedPlayers),
		ChildCount:     int32(len(orderedChildIDs)),
		NpcEntityCount: int32(totalNPC),
	}, nil
}
