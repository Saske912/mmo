package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/_pingtmp <host:port>")
		os.Exit(2)
	}
	conn, err := grpc.NewClient(os.Args[1], grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := cellv1.NewCellClient(conn).Ping(ctx, &cellv1.PingRequest{ClientId: "pingtmp"})
	if err != nil {
		panic(err)
	}
	fmt.Printf("cell_id=%s players=%d entities=%d\n", r.CellId, r.PlayerCount, r.EntityCount)
}
