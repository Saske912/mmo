package registrysvc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/discovery"
)

// Server реализует cellv1.RegistryServer поверх Catalog (память или Consul).
type Server struct {
	cellv1.UnimplementedRegistryServer
	Store discovery.Catalog
}

var errBadRequest = errors.New("bad request")

func (s *Server) Register(ctx context.Context, req *cellv1.RegisterRequest) (*cellv1.RegisterResponse, error) {
	if req == nil || req.Cell == nil {
		incRPC("Register", errBadRequest)
		return &cellv1.RegisterResponse{Ok: false, ErrorMessage: "empty request"}, nil
	}
	if err := s.Store.RegisterCell(ctx, req.Cell); err != nil {
		incRPC("Register", err)
		return &cellv1.RegisterResponse{Ok: false, ErrorMessage: err.Error()}, nil
	}
	incRPC("Register", nil)
	return &cellv1.RegisterResponse{Ok: true}, nil
}

func (s *Server) ListCells(ctx context.Context, _ *cellv1.ListCellsRequest) (*cellv1.ListCellsResponse, error) {
	cells, err := s.Store.List(ctx)
	if err != nil {
		incRPC("ListCells", err)
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	incRPC("ListCells", nil)
	return &cellv1.ListCellsResponse{Cells: cells}, nil
}

func (s *Server) ResolvePosition(ctx context.Context, req *cellv1.ResolvePositionRequest) (*cellv1.ResolvePositionResponse, error) {
	if req == nil {
		incRPC("ResolvePosition", errBadRequest)
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	c, ok, err := s.Store.ResolveMostSpecific(ctx, req.X, req.Z)
	if err != nil {
		incRPC("ResolvePosition", err)
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	if !ok {
		incRPC("ResolvePosition", nil)
		return &cellv1.ResolvePositionResponse{Found: false}, nil
	}
	incRPC("ResolvePosition", nil)
	return &cellv1.ResolvePositionResponse{Cell: c, Found: true}, nil
}
