package registrysvc

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/discovery"
)

const forwardCellDialTimeout = 5 * time.Second

// Server реализует cellv1.RegistryServer поверх Catalog (память или Consul).
type Server struct {
	cellv1.UnimplementedRegistryServer
	Store discovery.Catalog
}

var errBadRequest = errors.New("bad request")

func (s *Server) Register(ctx context.Context, req *cellv1.RegisterRequest) (*cellv1.RegisterResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("Register", start) }()
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
	start := time.Now()
	defer func() { observeRPCDuration("ListCells", start) }()
	cells, err := s.Store.List(ctx)
	if err != nil {
		incRPC("ListCells", err)
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	incRPC("ListCells", nil)
	return &cellv1.ListCellsResponse{Cells: cells}, nil
}

func (s *Server) ResolvePosition(ctx context.Context, req *cellv1.ResolvePositionRequest) (*cellv1.ResolvePositionResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ResolvePosition", start) }()
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

func (s *Server) ForwardCellUpdate(ctx context.Context, req *cellv1.ForwardCellUpdateRequest) (*cellv1.ForwardCellUpdateResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ForwardCellUpdate", start) }()
	if req == nil || req.Update == nil {
		e := status.Error(codes.InvalidArgument, "empty request or update")
		incRPC("ForwardCellUpdate", e)
		return nil, e
	}
	if req.CellId == "" {
		e := status.Error(codes.InvalidArgument, "empty cell_id")
		incRPC("ForwardCellUpdate", e)
		return nil, e
	}
	if s.Store == nil {
		e := status.Error(codes.FailedPrecondition, "no catalog")
		incRPC("ForwardCellUpdate", e)
		return nil, e
	}

	spec, ok, err := discovery.FindCellByID(ctx, s.Store, req.CellId)
	if err != nil {
		incRPC("ForwardCellUpdate", err)
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	if !ok || spec == nil {
		e := status.Errorf(codes.NotFound, "cell not found: %s", req.CellId)
		incRPC("ForwardCellUpdate", e)
		return nil, e
	}
	ep := spec.GetGrpcEndpoint()
	if ep == "" {
		e := status.Error(codes.FailedPrecondition, "cell has empty grpc_endpoint")
		incRPC("ForwardCellUpdate", e)
		return nil, e
	}

	conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		incRPC("ForwardCellUpdate", err)
		return nil, status.Errorf(codes.Unavailable, "dial cell: %v", err)
	}
	defer conn.Close()

	cellCtx, cellCancel := context.WithTimeout(ctx, forwardCellDialTimeout)
	defer cellCancel()
	cellClient := cellv1.NewCellClient(conn)
	updResp, err := cellClient.Update(cellCtx, req.Update)
	if err != nil {
		incRPC("ForwardCellUpdate", err)
		return nil, err
	}
	if updResp == nil {
		e := status.Error(codes.Internal, "nil UpdateResponse")
		incRPC("ForwardCellUpdate", e)
		return nil, e
	}
	incRPC("ForwardCellUpdate", nil)
	return &cellv1.ForwardCellUpdateResponse{Ok: updResp.Ok, Message: updResp.Message}, nil
}
