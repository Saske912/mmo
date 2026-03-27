package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	cellv1 "mmo/gen/cellv1"
)

func main() {
	regAddr := flag.String("registry", "127.0.0.1:9100", "Registry address")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: mmoctl [-registry host:port] <list|resolve x z|ping endpoint>")
		os.Exit(2)
	}

	cmd := flag.Arg(0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	switch cmd {
	case "list":
		conn, err := grpc.NewClient(*regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewRegistryClient(conn)
		resp, err := cl.ListCells(ctx, &cellv1.ListCellsRequest{})
		if err != nil {
			log.Fatal(err)
		}
		for _, c := range resp.Cells {
			b := c.Bounds
			fmt.Printf("%s level=%d endpoint=%s bounds=[%.0f,%.0f]x[%.0f,%.0f]\n",
				c.Id, c.Level, c.GrpcEndpoint, b.XMin, b.XMax, b.ZMin, b.ZMax)
		}
	case "resolve":
		if flag.NArg() != 3 {
			log.Fatal("resolve: need x z")
		}
		x, err := strconv.ParseFloat(flag.Arg(1), 64)
		if err != nil {
			log.Fatal(err)
		}
		z, err := strconv.ParseFloat(flag.Arg(2), 64)
		if err != nil {
			log.Fatal(err)
		}
		conn, err := grpc.NewClient(*regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewRegistryClient(conn)
		resp, err := cl.ResolvePosition(ctx, &cellv1.ResolvePositionRequest{X: x, Z: z})
		if err != nil {
			log.Fatal(err)
		}
		if !resp.Found {
			fmt.Println("not found")
			return
		}
		c := resp.Cell
		b := c.Bounds
		fmt.Printf("%s level=%d endpoint=%s bounds=[%.0f,%.0f]x[%.0f,%.0f]\n",
			c.Id, c.Level, c.GrpcEndpoint, b.XMin, b.XMax, b.ZMin, b.ZMax)
	case "ping":
		if flag.NArg() != 2 {
			log.Fatal("ping: need host:port")
		}
		ep := flag.Arg(1)
		conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewCellClient(conn)
		p, err := cl.Ping(ctx, &cellv1.PingRequest{ClientId: "mmoctl"})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("cell_id=%s time_ms=%d\n", p.CellId, p.ServerTimeUnixMs)
	default:
		log.Fatalf("unknown command %q", cmd)
	}
}
