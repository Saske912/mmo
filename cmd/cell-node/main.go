package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	cellv1 "mmo/gen/cellv1"
	"mmo/internal/grpc/cellsvc"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:0", "cell gRPC listen address (0 picks free port)")
	registryAddr := flag.String("registry", "127.0.0.1:9100", "grid-manager Registry address")
	cellID := flag.String("id", "", "cell id (required)")
	level := flag.Int("level", 0, "subdivision level")
	xmin := flag.Float64("xmin", -1000, "bounds x_min")
	xmax := flag.Float64("xmax", 1000, "bounds x_max")
	zmin := flag.Float64("zmin", -1000, "bounds z_min")
	zmax := flag.Float64("zmax", 1000, "bounds z_max")
	flag.Parse()

	if *cellID == "" {
		fmt.Fprintln(os.Stderr, "cell-node: -id is required")
		os.Exit(2)
	}
	if *xmin >= *xmax || *zmin >= *zmax {
		log.Fatalf("invalid bounds")
	}

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	endpoint := lis.Addr().String()

	srv := grpc.NewServer()
	cellv1.RegisterCellServer(srv, &cellsvc.Server{CellID: *cellID})

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Fatalf("serve: %v", err)
		}
	}()

	conn, err := grpc.NewClient(*registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("registry dial: %v", err)
	}
	defer conn.Close()

	reg := cellv1.NewRegistryClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = reg.Register(ctx, &cellv1.RegisterRequest{
		Cell: &cellv1.CellSpec{
			Id:           *cellID,
			Level:        int32(*level),
			GrpcEndpoint: endpoint,
			Bounds:       &cellv1.Bounds{XMin: *xmin, XMax: *xmax, ZMin: *zmin, ZMax: *zmax},
		},
	})
	if err != nil {
		log.Fatalf("register: %v", err)
	}

	log.Printf("cell %q registered at %s (registry=%s)", *cellID, endpoint, *registryAddr)
	select {}
}
