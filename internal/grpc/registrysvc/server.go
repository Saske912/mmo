package registrysvc

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cellv1 "mmo/gen/cellv1"
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/discovery"
)

const forwardCellDialTimeout = 5 * time.Second

// Server реализует cellv1.RegistryServer поверх Catalog (память или Consul).
type Server struct {
	cellv1.UnimplementedRegistryServer
	Store discovery.Catalog
	NATS  *natsbus.Client
}

var errBadRequest = errors.New("bad request")

func (s *Server) Register(ctx context.Context, req *cellv1.RegisterRequest) (*cellv1.RegisterResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("Register", start) }()
	if req != nil && req.Cell != nil {
		logOpStart("Register", "cell_id", req.Cell.GetId(), "level", req.Cell.GetLevel())
	} else {
		logOpStart("Register")
	}
	if req == nil || req.Cell == nil {
		incRPC("Register", errBadRequest)
		logOpError("Register", errBadRequest)
		return &cellv1.RegisterResponse{Ok: false, ErrorMessage: "empty request"}, nil
	}
	if err := s.Store.RegisterCell(ctx, req.Cell); err != nil {
		incRPC("Register", err)
		logOpError("Register", err, "cell_id", req.Cell.GetId())
		return &cellv1.RegisterResponse{Ok: false, ErrorMessage: err.Error()}, nil
	}
	incRPC("Register", nil)
	logOpDone("Register", "cell_id", req.Cell.GetId())
	return &cellv1.RegisterResponse{Ok: true}, nil
}

func (s *Server) ListCells(ctx context.Context, _ *cellv1.ListCellsRequest) (*cellv1.ListCellsResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ListCells", start) }()
	logOpStart("ListCells")
	cells, err := s.Store.List(ctx)
	if err != nil {
		incRPC("ListCells", err)
		logOpError("ListCells", err)
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	incRPC("ListCells", nil)
	logOpDone("ListCells", "count", len(cells))
	return &cellv1.ListCellsResponse{Cells: cells}, nil
}

func (s *Server) ResolvePosition(ctx context.Context, req *cellv1.ResolvePositionRequest) (*cellv1.ResolvePositionResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ResolvePosition", start) }()
	if req != nil {
		logOpStart("ResolvePosition", "x", req.GetX(), "z", req.GetZ())
	} else {
		logOpStart("ResolvePosition")
	}
	if req == nil {
		incRPC("ResolvePosition", errBadRequest)
		logOpError("ResolvePosition", errBadRequest)
		return nil, status.Error(codes.InvalidArgument, "empty request")
	}
	c, ok, err := s.Store.ResolveMostSpecific(ctx, req.X, req.Z)
	if err != nil {
		incRPC("ResolvePosition", err)
		logOpError("ResolvePosition", err, "x", req.GetX(), "z", req.GetZ())
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	if !ok {
		incRPC("ResolvePosition", nil)
		logOpDone("ResolvePosition", "found", false, "x", req.GetX(), "z", req.GetZ())
		return &cellv1.ResolvePositionResponse{Found: false}, nil
	}
	incRPC("ResolvePosition", nil)
	logOpDone("ResolvePosition", "found", true, "cell_id", c.GetId(), "level", c.GetLevel())
	return &cellv1.ResolvePositionResponse{Cell: c, Found: true}, nil
}

func (s *Server) ForwardCellUpdate(ctx context.Context, req *cellv1.ForwardCellUpdateRequest) (*cellv1.ForwardCellUpdateResponse, error) {
	start := time.Now()
	defer func() { observeRPCDuration("ForwardCellUpdate", start) }()
	ctx, span := otel.Tracer("mmo/grid-manager").Start(ctx, "Registry.ForwardCellUpdate")
	defer span.End()
	if req != nil {
		span.SetAttributes(attribute.String("cell_id", req.GetCellId()))
		logOpStart("ForwardCellUpdate", "cell_id", req.GetCellId(), "update_kind", updateKind(req.GetUpdate()))
	} else {
		logOpStart("ForwardCellUpdate")
	}
	if req == nil || req.Update == nil {
		e := status.Error(codes.InvalidArgument, "empty request or update")
		incRPC("ForwardCellUpdate", e)
		logOpError("ForwardCellUpdate", e)
		return nil, e
	}
	if req.CellId == "" {
		e := status.Error(codes.InvalidArgument, "empty cell_id")
		incRPC("ForwardCellUpdate", e)
		logOpError("ForwardCellUpdate", e, "update_kind", updateKind(req.GetUpdate()))
		return nil, e
	}
	out, err := s.doForwardCellUpdate(ctx, req.CellId, req.Update)
	if err != nil {
		incRPC("ForwardCellUpdate", err)
		logOpError("ForwardCellUpdate", err, "cell_id", req.CellId, "update_kind", updateKind(req.GetUpdate()))
		return nil, err
	}
	incRPC("ForwardCellUpdate", nil)
	logOpDone("ForwardCellUpdate", "cell_id", req.CellId, "update_kind", updateKind(req.GetUpdate()), "ok", out.GetOk())
	return out, nil
}
