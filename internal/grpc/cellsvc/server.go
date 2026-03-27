package cellsvc

import (
	"context"
	"strings"
	"time"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/cellsim"
	"mmo/internal/ecs"
)

// Server — Cell gRPC: Ping, Join (минимальная регистрация игрока в ECS мире).
type Server struct {
	cellv1.UnimplementedCellServer
	CellID string
	Sim    *cellsim.Runtime
}

func (s *Server) Ping(_ context.Context, req *cellv1.PingRequest) (*cellv1.PingResponse, error) {
	_ = req
	return &cellv1.PingResponse{
		CellId:           s.CellID,
		ServerTimeUnixMs: time.Now().UnixMilli(),
	}, nil
}

// Join создаёт сущность с дефолтной позицией (заглушка до полноценного gateway).
func (s *Server) Join(_ context.Context, req *cellv1.JoinRequest) (*cellv1.JoinResponse, error) {
	pid := strings.TrimSpace(req.GetPlayerId())
	if pid == "" {
		return &cellv1.JoinResponse{Ok: false, CellId: s.CellID, Message: "empty player_id"}, nil
	}
	if s.Sim == nil || s.Sim.World == nil {
		return &cellv1.JoinResponse{Ok: true, CellId: s.CellID, Message: "no_sim"}, nil
	}
	e := s.Sim.World.CreateEntity()
	s.Sim.World.SetPosition(e, ecs.Position{X: 0, Y: 0, Z: 0})
	s.Sim.World.SetVelocity(e, ecs.Velocity{})
	s.Sim.World.SetHealth(e, ecs.Health{HP: 100, MaxHP: 100})
	return &cellv1.JoinResponse{
		Ok:        true,
		CellId:    s.CellID,
		Message:   "spawned",
		EntityId:  uint64(e),
	}, nil
}
