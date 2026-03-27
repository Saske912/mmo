package cellsvc

import (
	"context"
	"time"

	cellv1 "mmo/gen/cellv1"
)

// Server — минимальный Cell gRPC (Ping).
type Server struct {
	cellv1.UnimplementedCellServer
	CellID string
}

func (s *Server) Ping(_ context.Context, req *cellv1.PingRequest) (*cellv1.PingResponse, error) {
	_ = req
	return &cellv1.PingResponse{
		CellId:           s.CellID,
		ServerTimeUnixMs: time.Now().UnixMilli(),
	}, nil
}
