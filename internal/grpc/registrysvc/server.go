package registrysvc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/registry"
)

// Server реализует cellv1.RegistryServer поверх in-memory реестра.
type Server struct {
	cellv1.UnimplementedRegistryServer
	Mem *registry.Memory
}

func (s *Server) Register(ctx context.Context, req *cellv1.RegisterRequest) (*cellv1.RegisterResponse, error) {
	if req == nil || req.Cell == nil {
		return &cellv1.RegisterResponse{Ok: false, ErrorMessage: "empty request"}, nil
	}
	if err := s.Mem.Register(ctx, req.Cell); err != nil {
		return &cellv1.RegisterResponse{Ok: false, ErrorMessage: err.Error()}, nil
	}
	return &cellv1.RegisterResponse{Ok: true}, nil
}

func (s *Server) ListCells(ctx context.Context, _ *cellv1.ListCellsRequest) (*cellv1.ListCellsResponse, error) {
	cells := s.Mem.List(ctx)
	return &cellv1.ListCellsResponse{Cells: cells}, nil
}

func (s *Server) ResolvePosition(ctx context.Context, req *cellv1.ResolvePositionRequest) (*cellv1.ResolvePositionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	c, ok := s.Mem.ResolveMostSpecific(ctx, req.X, req.Z)
	if !ok {
		return &cellv1.ResolvePositionResponse{Found: false}, nil
	}
	return &cellv1.ResolvePositionResponse{Cell: c, Found: true}, nil
}
