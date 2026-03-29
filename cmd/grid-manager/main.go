package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	cellv1 "mmo/gen/cellv1"
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/config"
	"mmo/internal/discovery"
	"mmo/internal/grpc/registrysvc"
	"mmo/internal/logging"
	"mmo/internal/registry"
	"mmo/internal/tracing"
)

func main() {
	logging.SetupFromEnv()
	shutdownTrace, err := tracing.Init(context.Background(), "grid-manager")
	if err != nil {
		log.Fatalf("tracing: %v", err)
	}
	defer func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = shutdownTrace(ctx)
	}()
	addr := flag.String("listen", "127.0.0.1:9100", "gRPC listen address")
	metricsListen := flag.String("metrics-listen", "", "HTTP listen для /metrics (например 0.0.0.0:9091); пусто — выкл")
	backend := flag.String("backend", "auto", "catalog backend: auto | memory | consul")
	consulAddr := flag.String("consul-addr", "", "Consul HTTP address host:port (default: CONSUL_HTTP_ADDR)")
	flag.Parse()

	wireMetricsHTTP(*metricsListen)

	ctxProbe, cancelProbe := context.WithCancel(context.Background())
	defer cancelProbe()
	store, err := openCatalog(*backend, *consulAddr)
	if err != nil {
		log.Fatal(err)
	}

	if *metricsListen != "" {
		startCellLoadProbe(ctxProbe, store)
	}

	var natsCli *natsbus.Client
	if natsURL := config.FromEnv().NATSURL; natsURL != "" {
		cli, err := natsbus.ConnectResilient(natsURL, natsbus.DefaultReconnectConfig())
		if err != nil {
			log.Printf("registry nats connect failed: %v", err)
		} else {
			natsCli = cli
			defer natsCli.Close()
		}
	}

	srv := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	cellv1.RegisterRegistryServer(srv, &registrysvc.Server{Store: store, NATS: natsCli})

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("grid-manager registry listening on %s (backend=%s)", lis.Addr(), catalogDesc(store))
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func openCatalog(backend, consulAddrFlag string) (discovery.Catalog, error) {
	addr := consulAddrFlag
	if addr == "" {
		addr = discovery.ConsulAddrFromEnv()
	}

	switch backend {
	case "memory":
		return discovery.NewMemoryCatalog(registry.NewMemory()), nil
	case "consul":
		if addr == "" {
			return nil, fmt.Errorf("consul backend requires -consul-addr or CONSUL_HTTP_ADDR")
		}
		return discovery.NewConsulCatalog(addr, discovery.ConsulTokenFromEnv())
	case "auto":
		if addr != "" {
			log.Printf("using Consul catalog at %s", addr)
			return discovery.NewConsulCatalog(addr, discovery.ConsulTokenFromEnv())
		}
		log.Printf("using in-memory catalog (set CONSUL_HTTP_ADDR for Consul)")
		return discovery.NewMemoryCatalog(registry.NewMemory()), nil
	default:
		return nil, fmt.Errorf("backend must be auto, memory, or consul")
	}
}

func catalogDesc(store discovery.Catalog) string {
	switch store.(type) {
	case *discovery.ConsulCatalog:
		return "consul"
	case *discovery.MemoryCatalog:
		return "memory"
	default:
		return "unknown"
	}
}
