package cellsvc

import (
	"context"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/cellsim"
	"mmo/internal/ecs"
	"mmo/internal/replic"
)

// Server — Cell gRPC: Ping, Join, SubscribeDeltas (репликация из ECS).
type Server struct {
	cellv1.UnimplementedCellServer
	CellID string
	Sim    *cellsim.Runtime

	playerMu sync.Mutex
	players  map[uint64]struct{}
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

func (s *Server) addPlayerEntity(e ecs.Entity) {
	s.playerMu.Lock()
	if s.players == nil {
		s.players = make(map[uint64]struct{})
	}
	s.players[uint64(e)] = struct{}{}
	s.playerMu.Unlock()
}

func (s *Server) Ping(_ context.Context, req *cellv1.PingRequest) (*cellv1.PingResponse, error) {
	_ = req
	return &cellv1.PingResponse{
		CellId:           s.CellID,
		ServerTimeUnixMs: time.Now().UnixMilli(),
	}, nil
}

// Join создаёт сущность игрока в ECS.
func (s *Server) Join(_ context.Context, req *cellv1.JoinRequest) (*cellv1.JoinResponse, error) {
	pid := strings.TrimSpace(req.GetPlayerId())
	if pid == "" {
		return &cellv1.JoinResponse{Ok: false, CellId: s.CellID, Message: "empty player_id"}, nil
	}
	if s.Sim == nil || s.Sim.World == nil {
		return &cellv1.JoinResponse{Ok: true, CellId: s.CellID, Message: "no_sim"}, nil
	}
	s.Sim.Mu.Lock()
	e := s.Sim.World.CreateEntity()
	s.Sim.World.SetPosition(e, ecs.Position{X: 0, Y: 0, Z: 0})
	s.Sim.World.SetVelocity(e, ecs.Velocity{})
	s.Sim.World.SetHealth(e, ecs.Health{HP: 100, MaxHP: 100})
	s.Sim.Mu.Unlock()
	s.addPlayerEntity(e)
	return &cellv1.JoinResponse{
		Ok:        true,
		CellId:    s.CellID,
		Message:   "spawned",
		EntityId:  uint64(e),
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
