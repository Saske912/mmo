package main

import (
	"flag"
	"log"
	"net"

	"google.golang.org/grpc"
	cellv1 "mmo/gen/cellv1"
	"mmo/internal/grpc/registrysvc"
	"mmo/internal/registry"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:9100", "gRPC listen address")
	flag.Parse()

	mem := registry.NewMemory()
	srv := grpc.NewServer()
	cellv1.RegisterRegistryServer(srv, &registrysvc.Server{Mem: mem})

	lis, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("grid-manager registry listening on %s", lis.Addr())
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
