package cellsvc

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
	"mmo/internal/cellsim"
	"mmo/internal/ecs"
	"mmo/internal/partition"
	"mmo/internal/replic"
)

// Server — Cell gRPC: Ping, Join, Leave, ApplyInput, SubscribeDeltas (репликация из ECS).
type Server struct {
	cellv1.UnimplementedCellServer
	CellID string
	Sim    *cellsim.Runtime
	// Bounds и Level — границы соты в каталоге (для PlanSplit и согласованности с Resolve).
	Bounds *cellv1.Bounds
	Level  int32

	playerMu     sync.Mutex
	players      map[uint64]struct{}
	playerByID   map[string]ecs.Entity
	onApplyInput func(ok bool) // опционально: метрики (устанавливает cell-node)
	// splitDrain: новые Join отклоняются (оператор / grid-manager через Update set_split_drain).
	splitDrain atomic.Bool
}

// SetApplyInputHook вызывается из cell-node для Prometheus; не обязателен.
func (s *Server) SetApplyInputHook(fn func(ok bool)) {
	s.playerMu.Lock()
	s.onApplyInput = fn
	s.playerMu.Unlock()
}

// PlayerCount число игроков, зарегистрированных через Join.
func (s *Server) PlayerCount() int {
	s.playerMu.Lock()
	n := len(s.playerByID)
	s.playerMu.Unlock()
	return n
}

func (s *Server) isPlayer(e ecs.Entity) bool {
	if s.players == nil {
		return false
	}
	s.playerMu.Lock()
	_, ok := s.players[uint64(e)]
	s.playerMu.Unlock()
	return ok
}

// IsPlayer сущность зарегистрирована как игрок (для персиста — такие пропускаем).
func (s *Server) IsPlayer(e ecs.Entity) bool {
	return s.isPlayer(e)
}

// Join создаёт сущность игрока в ECS. Повторный Join с тем же player_id идемпотентен.
func (s *Server) Join(_ context.Context, req *cellv1.JoinRequest) (*cellv1.JoinResponse, error) {
	pid := strings.TrimSpace(req.GetPlayerId())
	if pid == "" {
		return &cellv1.JoinResponse{Ok: false, CellId: s.CellID, Message: "empty player_id"}, nil
	}
	if s.Sim == nil || s.Sim.World == nil {
		return &cellv1.JoinResponse{Ok: true, CellId: s.CellID, Message: "no_sim"}, nil
	}

	s.playerMu.Lock()
	if s.playerByID == nil {
		s.playerByID = make(map[string]ecs.Entity)
	}
	if existing, ok := s.playerByID[pid]; ok {
		s.playerMu.Unlock()
		return &cellv1.JoinResponse{
			Ok:       true,
			CellId:   s.CellID,
			Message:  "already_joined",
			EntityId: uint64(existing),
		}, nil
	}
	if s.splitDrain.Load() {
		s.playerMu.Unlock()
		return &cellv1.JoinResponse{Ok: false, CellId: s.CellID, Message: "split_drain: new joins disabled"}, nil
	}
	s.playerMu.Unlock()

	s.Sim.Mu.Lock()
	e := s.Sim.World.CreateEntity()
	s.Sim.World.SetPosition(e, ecs.Position{X: 0, Y: 0, Z: 0})
	s.Sim.World.SetVelocity(e, ecs.Velocity{})
	s.Sim.World.SetHealth(e, ecs.Health{HP: 100, MaxHP: 100})
	s.Sim.Mu.Unlock()

	s.playerMu.Lock()
	if s.players == nil {
		s.players = make(map[uint64]struct{})
	}
	s.playerByID[pid] = e
	s.players[uint64(e)] = struct{}{}
	s.playerMu.Unlock()

	return &cellv1.JoinResponse{
		Ok:       true,
		CellId:   s.CellID,
		Message:  "spawned",
		EntityId: uint64(e),
	}, nil
}

// Leave удаляет игрока из мира. Неизвестный player_id — ok (идемпотентно).
func (s *Server) Leave(_ context.Context, req *cellv1.LeaveRequest) (*cellv1.LeaveResponse, error) {
	pid := strings.TrimSpace(req.GetPlayerId())
	if pid == "" {
		return &cellv1.LeaveResponse{Ok: false, Message: "empty player_id"}, nil
	}
	if s.Sim == nil || s.Sim.World == nil {
		return &cellv1.LeaveResponse{Ok: true, Message: "no_sim"}, nil
	}

	s.playerMu.Lock()
	e, ok := s.playerByID[pid]
	if ok {
		delete(s.playerByID, pid)
		delete(s.players, uint64(e))
	}
	s.playerMu.Unlock()

	if !ok {
		return &cellv1.LeaveResponse{Ok: true, Message: "noop"}, nil
	}

	s.Sim.Mu.Lock()
	s.Sim.World.DestroyEntity(e)
	s.Sim.Mu.Unlock()

	return &cellv1.LeaveResponse{Ok: true, Message: "left"}, nil
}

// ApplyInput применяет ClientInput к сущности игрока (скорость в XZ).
func (s *Server) ApplyInput(_ context.Context, req *cellv1.ApplyInputRequest) (*cellv1.ApplyInputResponse, error) {
	pid := strings.TrimSpace(req.GetPlayerId())
	if pid == "" {
		s.reportApplyInput(false)
		return &cellv1.ApplyInputResponse{Ok: false, Message: "empty player_id"}, nil
	}
	in := req.GetInput()
	if in == nil {
		in = &gamev1.ClientInput{}
	}
	if s.Sim == nil || s.Sim.World == nil {
		s.reportApplyInput(false)
		return &cellv1.ApplyInputResponse{Ok: false, Message: "no_sim"}, nil
	}

	s.playerMu.Lock()
	e, ok := s.playerByID[pid]
	s.playerMu.Unlock()

	if !ok {
		s.reportApplyInput(false)
		return &cellv1.ApplyInputResponse{Ok: false, Message: "unknown_player"}, nil
	}

	vel := velocityFromClientInput(in)
	s.Sim.Mu.Lock()
	if _, hasPos := s.Sim.World.Position(e); !hasPos {
		s.Sim.Mu.Unlock()
		s.reportApplyInput(false)
		return &cellv1.ApplyInputResponse{Ok: false, Message: "entity_gone"}, nil
	}
	s.Sim.World.SetVelocity(e, vel)
	s.Sim.Mu.Unlock()

	s.reportApplyInput(true)
	return &cellv1.ApplyInputResponse{Ok: true, Message: "ok"}, nil
}

func (s *Server) reportApplyInput(ok bool) {
	s.playerMu.Lock()
	fn := s.onApplyInput
	s.playerMu.Unlock()
	if fn != nil {
		fn(ok)
	}
}

// Update команды от grid-manager / админки (noop, смена TPS).
func (s *Server) Update(_ context.Context, req *cellv1.UpdateRequest) (*cellv1.UpdateResponse, error) {
	if req == nil {
		return &cellv1.UpdateResponse{Ok: false, Message: "nil request"}, nil
	}
	switch p := req.Payload.(type) {
	case nil:
		return &cellv1.UpdateResponse{Ok: true, Message: "noop"}, nil
	case *cellv1.UpdateRequest_Noop:
		_ = p
		return &cellv1.UpdateResponse{Ok: true, Message: "noop"}, nil
	case *cellv1.UpdateRequest_SetTargetTps:
		tps := p.SetTargetTps
		if tps < 1 || tps > 120 {
			return &cellv1.UpdateResponse{Ok: false, Message: "target_tps out of range [1,120]"}, nil
		}
		if s.Sim == nil || s.Sim.Loop == nil {
			return &cellv1.UpdateResponse{Ok: false, Message: "no_sim"}, nil
		}
		s.Sim.Mu.Lock()
		s.Sim.Loop.TPS = float64(tps)
		s.Sim.Mu.Unlock()
		return &cellv1.UpdateResponse{Ok: true, Message: "ok"}, nil
	case *cellv1.UpdateRequest_SplitPrepare:
		if s.Bounds == nil {
			return &cellv1.UpdateResponse{Ok: false, Message: "no bounds on cell server"}, nil
		}
		children := partition.ChildSpecsForSplit(s.Bounds, s.Level)
		reason := ""
		if p.SplitPrepare != nil {
			reason = strings.TrimSpace(p.SplitPrepare.GetReason())
		}
		players := s.PlayerCount()
		entities := 0
		if s.Sim != nil && s.Sim.World != nil {
			entities = s.Sim.World.EntityCount()
		}
		msg := fmt.Sprintf("split_prepare ok children=%d players=%d entities=%d",
			len(children), players, entities)
		if reason != "" {
			msg += " reason=" + reason
		}
		msg += " child_ids="
		for i, ch := range children {
			if i > 0 {
				msg += ","
			}
			msg += ch.GetId()
		}
		return &cellv1.UpdateResponse{Ok: true, Message: msg}, nil
	case *cellv1.UpdateRequest_SetSplitDrain:
		en := false
		if p.SetSplitDrain != nil {
			en = p.SetSplitDrain.GetEnabled()
		}
		s.splitDrain.Store(en)
		if en {
			return &cellv1.UpdateResponse{Ok: true, Message: "split_drain enabled"}, nil
		}
		return &cellv1.UpdateResponse{Ok: true, Message: "split_drain disabled"}, nil
	default:
		return &cellv1.UpdateResponse{Ok: true, Message: "noop"}, nil
	}
}

func (s *Server) PlanSplit(_ context.Context, _ *cellv1.PlanSplitRequest) (*cellv1.PlanSplitResponse, error) {
	if s.Bounds == nil {
		return nil, status.Error(codes.FailedPrecondition, "no bounds on cell server")
	}
	return &cellv1.PlanSplitResponse{Children: partition.ChildSpecsForSplit(s.Bounds, s.Level)}, nil
}

func (s *Server) Ping(_ context.Context, req *cellv1.PingRequest) (*cellv1.PingResponse, error) {
	_ = req
	return &cellv1.PingResponse{
		CellId:           s.CellID,
		ServerTimeUnixMs: time.Now().UnixMilli(),
	}, nil
}

// SubscribeDeltas поток снапшота и дельт с мира.
func (s *Server) SubscribeDeltas(_ *cellv1.SubscribeDeltasRequest, stream cellv1.Cell_SubscribeDeltasServer) error {
	if s.Sim == nil || s.Sim.World == nil {
		return status.Errorf(codes.FailedPrecondition, "no simulation")
	}
	ctx := stream.Context()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	isPlayer := func(e ecs.Entity) bool { return s.isPlayer(e) }

	var lastSentTick uint64
	first := true

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.Sim.Mu.Lock()
			tick := s.Sim.Loop.Stats.TickCount
			if first {
				snap := replic.BuildSnapshot(s.Sim.World, tick, isPlayer)
				s.Sim.Mu.Unlock()
				if err := stream.Send(&cellv1.WorldChunk{Kind: &cellv1.WorldChunk_Snapshot{Snapshot: snap}}); err != nil {
					return err
				}
				first = false
				lastSentTick = tick
				continue
			}
			dirty := s.Sim.World.TakeDirtyEntities()
			delta := replic.BuildDelta(s.Sim.World, tick, lastSentTick, dirty, isPlayer)
			s.Sim.Mu.Unlock()

			if len(delta.Changed) > 0 {
				if err := stream.Send(&cellv1.WorldChunk{Kind: &cellv1.WorldChunk_Delta{Delta: delta}}); err != nil {
					return err
				}
			}
			lastSentTick = tick
		}
	}
}
