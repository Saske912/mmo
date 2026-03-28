package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	cellv1 "mmo/gen/cellv1"
	"mmo/internal/cellsim"
	"mmo/internal/config"
	"mmo/internal/discovery"
	"mmo/internal/grpc/cellsvc"
	"mmo/internal/logging"
	"mmo/internal/tracing"
)

func main() {
	logging.SetupFromEnv()
	shutdownTrace, err := tracing.Init(context.Background(), "cell-node")
	if err != nil {
		log.Fatalf("tracing: %v", err)
	}
	defer func() {
		c, cn := context.WithTimeout(context.Background(), 5*time.Second)
		defer cn()
		_ = shutdownTrace(c)
	}()
	listen := flag.String("listen", "127.0.0.1:0", "cell gRPC listen address (0 picks free port)")
	grpcAdvertise := flag.String("grpc-advertise", "", "host:port для регистрации в Consul/memory (K8s DNS); иначе env MMO_CELL_GRPC_ADVERTISE / CELL_GRPC_ENDPOINT; иначе адрес listen")
	registryAddr := flag.String("registry", "127.0.0.1:9100", "grid-manager Registry address (used if Consul is not configured)")
	consulAddr := flag.String("consul-addr", "", "Consul HTTP host:port (default: CONSUL_HTTP_ADDR)")
	cellID := flag.String("id", "", "cell id (required)")
	level := flag.Int("level", 0, "subdivision level")
	xmin := flag.Float64("xmin", -1000, "bounds x_min")
	xmax := flag.Float64("xmax", 1000, "bounds x_max")
	zmin := flag.Float64("zmin", -1000, "bounds z_min")
	zmax := flag.Float64("zmax", 1000, "bounds z_max")
	demoNPCs := flag.Int("demo-npcs", 0, "если > 0 — заспавнить столько демо-NPC в ECS (один раз)")
	metricsListen := flag.String("metrics-listen", "", "HTTP listen для /metrics (например 0.0.0.0:9090); пусто — выкл")
	persistSnapshot := flag.Bool("persist-snapshot", true, "при REDIS_ADDR — загрузка/сохранение ECS в Redis (ключ mmo:cell:<id>:state); -persist-snapshot=false выкл")
	flag.Parse()

	cfg := config.FromEnv()
	usePersist := *persistSnapshot && cfg.RedisAddr != ""
	rdb := openRedis(cfg.RedisAddr, cfg.RedisPassword)
	if rdb != nil {
		defer func() { _ = rdb.Close() }()
	}

	if *cellID == "" {
		fmt.Fprintln(os.Stderr, "cell-node: -id is required")
		os.Exit(2)
	}
	if *xmin >= *xmax || *zmin >= *zmax {
		log.Fatalf("invalid bounds")
	}

	caddr := *consulAddr
	if caddr == "" {
		caddr = discovery.ConsulAddrFromEnv()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	listenAddr := lis.Addr().String()
	endpoint := grpcEndpointForRegistry(*grpcAdvertise, listenAddr)

	spec := &cellv1.CellSpec{
		Id:           *cellID,
		Level:        int32(*level),
		GrpcEndpoint: endpoint,
		Bounds:       &cellv1.Bounds{XMin: *xmin, XMax: *xmax, ZMin: *zmin, ZMax: *zmax},
	}

	sim := cellsim.NewRuntime()
	tryLoadAndMaybeSpawnNPCs(ctx, rdb, redisStateKey(*cellID), sim, *demoNPCs, usePersist)

	cellSvc := &cellsvc.Server{
		CellID: *cellID,
		Sim:    sim,
		Bounds: spec.Bounds,
		Level:  int32(*level),
	}
	wirePrometheus(*metricsListen, cellSvc, sim)

	srv := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	cellv1.RegisterCellServer(srv, cellSvc)

	errServe := make(chan error, 1)
	go func() { errServe <- srv.Serve(lis) }()
	go func() {
		if err := sim.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("ecs loop: %v", err)
		}
	}()

	var consulCat *discovery.ConsulCatalog
	if caddr != "" {
		consulCat, err = discovery.NewConsulCatalog(caddr, discovery.ConsulTokenFromEnv())
		if err != nil {
			log.Fatalf("consul client: %v", err)
		}
		regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = consulCat.RegisterCell(regCtx, spec)
		cancel()
		if err != nil {
			log.Fatalf("consul register: %v", err)
		}
		log.Printf("cell %q registered in Consul (http=%s), advertise gRPC %s (listen %s)", *cellID, caddr, endpoint, listenAddr)
	}

	if caddr == "" {
		conn, err := grpc.NewClient(*registryAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		)
		if err != nil {
			log.Fatalf("registry dial: %v", err)
		}
		defer conn.Close()
		reg := cellv1.NewRegistryClient(conn)
		regCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = reg.Register(regCtx, &cellv1.RegisterRequest{Cell: spec})
		cancel()
		if err != nil {
			log.Fatalf("registry register: %v", err)
		}
		log.Printf("cell %q registered at %s listen %s (registry=%s)", *cellID, endpoint, listenAddr, *registryAddr)
	}

	select {
	case <-ctx.Done():
		log.Printf("shutdown: %v", ctx.Err())
	case err := <-errServe:
		if err != nil {
			log.Printf("serve: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	saveOnShutdown(shutdownCtx, rdb, redisStateKey(*cellID), sim, cellSvc, usePersist)
	if consulCat != nil {
		if err := consulCat.Deregister(shutdownCtx, discovery.ConsulServiceInstanceID(*cellID)); err != nil {
			log.Printf("consul deregister: %v", err)
		}
	}
	srv.GracefulStop()
}

func grpcEndpointForRegistry(flagAdvertise, listenAddr string) string {
	for _, s := range []string{
		strings.TrimSpace(flagAdvertise),
		strings.TrimSpace(os.Getenv("MMO_CELL_GRPC_ADVERTISE")),
		strings.TrimSpace(os.Getenv("CELL_GRPC_ENDPOINT")),
	} {
		if s != "" {
			return s
		}
	}
	return listenAddr
}
