package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	cellv1 "mmo/gen/cellv1"
	natsbus "mmo/internal/bus/nats"
	"mmo/internal/config"
	"mmo/internal/partition"
)

func main() {
	regAddr := flag.String("registry", "127.0.0.1:9100", "Registry address")
	natsURLOverride := flag.String("nats-url", "", "override NATS_URL")

	log.SetFlags(0)
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		usage()
	}

	cmd := args[0]
	switch cmd {
	case "infra-print":
		printInfra()
	case "partition-plan":
		runPartitionPlan(args[1:])
	case "nats":
		if len(args) < 2 {
			log.Fatal("nats: need subcommand pub|sub")
		}
		sub := args[1]
		fs := flag.NewFlagSet("nats "+sub, flag.ExitOnError)
		urlFlag := fs.String("url", "", "NATS URL (default: NATS_URL / config from env)")
		switch sub {
		case "pub":
			_ = fs.Parse(args[2:])
			pos := fs.Args()
			if len(pos) < 2 {
				log.Fatal("nats pub: need <subject> <body>")
			}
			nurl := firstNonEmpty(*urlFlag, *natsURLOverride, config.FromEnv().NATSURL)
			runNATSPub(nurl, pos[0], []byte(pos[1]))
		case "sub":
			wait := fs.Int("wait", 1, "messages to receive")
			timeout := fs.Duration("timeout", 5*time.Second, "per-message wait")
			_ = fs.Parse(args[2:])
			pos := fs.Args()
			if len(pos) < 1 {
				log.Fatal("nats sub: need <subject>")
			}
			nurl := firstNonEmpty(*urlFlag, *natsURLOverride, config.FromEnv().NATSURL)
			runNATSSub(nurl, pos[0], *wait, *timeout)
		default:
			log.Fatalf("nats: unknown %q", sub)
		}
	default:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		runRegistryOrPing(ctx, cmd, args[1:], *regAddr)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  mmoctl infra-print
  mmoctl partition-plan [-id s] [-level n] -xmin f -xmax f -zmin f -zmax f [-format text|json] [-tfvars-skeleton]
  mmoctl nats pub  [-url u] <subject> <body>
  mmoctl nats sub  [-url u] [-wait n] [-timeout d] <subject>
  mmoctl [-registry host:port] list
  mmoctl [-registry host:port] resolve <x> <z>
  mmoctl [-registry host:port] forward-update <cell_id> noop
  mmoctl [-registry host:port] forward-update <cell_id> tps <int>
  mmoctl [-registry host:port] forward-update <cell_id> split-prepare [reason]
  mmoctl [-registry host:port] forward-update <cell_id> split-drain <true|false>
  mmoctl [-registry host:port] forward-update <cell_id> export-npc-persist [reason]
  mmoctl [-registry host:port] forward-update <cell_id> import-npc-persist <path|-> [reason]
  mmoctl [-registry host:port] forward-npc-handoff <parent_cell_id> <child_cell_id> [reason]
  mmoctl [-registry host:port] migration-dry-run <cell_id>
  mmoctl plansplit <host:port>
  mmoctl migration-candidates <host:port> [reason]
  mmoctl ping <host:port>
  mmoctl join <host:port> <player_id>
`)
	os.Exit(2)
}

func runPartitionPlan(args []string) {
	fs := flag.NewFlagSet("partition-plan", flag.ExitOnError)
	id := fs.String("id", "", "parent cell id (для подписи в выводе)")
	level := fs.Int("level", 0, "parent subdivision level")
	xmin := fs.Float64("xmin", 0, "parent bounds")
	xmax := fs.Float64("xmax", 0, "")
	zmin := fs.Float64("zmin", 0, "")
	zmax := fs.Float64("zmax", 0, "")
	format := fs.String("format", "text", "text или json")
	tfvarsSkel := fs.Bool("tfvars-skeleton", false, "добавить каркас HCL для детей в cell_instances")
	_ = fs.Parse(args)

	if *xmin >= *xmax || *zmin >= *zmax {
		log.Fatal("partition-plan: нужны xmin < xmax и zmin < zmax")
	}
	b := &cellv1.Bounds{XMin: *xmin, XMax: *xmax, ZMin: *zmin, ZMax: *zmax}
	children := partition.ChildSpecsForSplit(b, int32(*level))
	if len(children) != 4 {
		log.Fatal("partition-plan: внутренняя ошибка — не 4 ребёнка")
	}

	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "json":
		type boundsJSON struct {
			XMin float64 `json:"x_min"`
			XMax float64 `json:"x_max"`
			ZMin float64 `json:"z_min"`
			ZMax float64 `json:"z_max"`
		}
		type childJSON struct {
			ID     string     `json:"id"`
			Level  int32      `json:"level"`
			Bounds boundsJSON `json:"bounds"`
		}
		chOut := make([]childJSON, 0, len(children))
		for _, ch := range children {
			cb := ch.GetBounds()
			cj := childJSON{ID: ch.Id, Level: ch.Level}
			if cb != nil {
				cj.Bounds = boundsJSON{XMin: cb.XMin, XMax: cb.XMax, ZMin: cb.ZMin, ZMax: cb.ZMax}
			}
			chOut = append(chOut, cj)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		parent := struct {
			ParentID string      `json:"parent_id"`
			Level    int         `json:"level"`
			Bounds   boundsJSON  `json:"bounds"`
			Children []childJSON `json:"children"`
		}{
			ParentID: strings.TrimSpace(*id),
			Level:    *level,
			Bounds:   boundsJSON{XMin: b.XMin, XMax: b.XMax, ZMin: b.ZMin, ZMax: b.ZMax},
			Children: chOut,
		}
		if err := enc.Encode(parent); err != nil {
			log.Fatal(err)
		}
	default:
		if strings.TrimSpace(*id) != "" {
			fmt.Printf("parent_id=%s level=%d bounds=[%.0f,%.0f]x[%.0f,%.0f]\n",
				*id, *level, b.XMin, b.XMax, b.ZMin, b.ZMax)
		}
		for _, ch := range children {
			cb := ch.GetBounds()
			if cb == nil {
				fmt.Printf("%s level=%d bounds=<nil>\n", ch.Id, ch.Level)
				continue
			}
			fmt.Printf("%s level=%d bounds=[%.0f,%.0f]x[%.0f,%.0f]\n",
				ch.Id, ch.Level, cb.XMin, cb.XMax, cb.ZMin, cb.ZMax)
		}
	}

	if *tfvarsSkel {
		fmt.Fprintf(os.Stdout, "\n# Каркас для cell_instances (дополните ключи, grpc_advertise, ресурсы):\n")
		for i, ch := range children {
			cb := ch.GetBounds()
			key := fmt.Sprintf("child_%d", i)
			fmt.Fprintf(os.Stdout, "  %s = { # %s\n", key, ch.Id)
			fmt.Fprintf(os.Stdout, "    id    = %q\n", ch.Id)
			fmt.Fprintf(os.Stdout, "    level = %d\n", ch.Level)
			fmt.Fprintf(os.Stdout, "    xmin  = %g\n", cb.XMin)
			fmt.Fprintf(os.Stdout, "    xmax  = %g\n", cb.XMax)
			fmt.Fprintf(os.Stdout, "    zmin  = %g\n", cb.ZMin)
			fmt.Fprintf(os.Stdout, "    zmax  = %g\n", cb.ZMax)
			fmt.Fprintf(os.Stdout, "  }\n")
		}
	}
}

func printInfra() {
	c := config.FromEnv()
	fmt.Printf("CONSUL_HTTP_ADDR=%s\n", valOrUnset(c.ConsulHTTPAddr))
	fmt.Printf("CONSUL_DNS_ADDR=%s\n", valOrUnset(c.ConsulDNSAddr))
	fmt.Printf("CONSUL_HTTP_TOKEN=%s\n", maskSecret(c.ConsulHTTPToken))
	fmt.Printf("NATS_URL=%s\n", maskNATSURL(c.NATSURL))
	fmt.Printf("DATABASE_URL_RW=%s\n", maskDSN(c.DatabaseURLRW))
	fmt.Printf("REDIS_ADDR=%s\n", valOrUnset(c.RedisAddr))
	fmt.Printf("REDIS_PASSWORD=%s\n", maskSecret(c.RedisPassword))
	fmt.Printf("MMO_CELL_GRPC_ADVERTISE/CELL_GRPC_ENDPOINT=%s\n", valOrUnset(c.CellGRPCAdvertise))
	fmt.Printf("HARBOR_REGISTRY=%s\n", valOrUnset(c.HarborRegistry))
	fmt.Printf("HARBOR_USER=%s\n", valOrUnset(c.HarborUser))
	fmt.Printf("HARBOR_PASSWORD=%s\n", maskSecret(c.HarborPassword))
}

func valOrUnset(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

func maskSecret(s string) string {
	if s == "" {
		return "(unset)"
	}
	return "***"
}

func maskDSN(s string) string {
	if s == "" {
		return "(unset)"
	}
	return fmt.Sprintf("(set, %d bytes)", len(s))
}

func maskNATSURL(raw string) string {
	if raw == "" {
		return "(unset)"
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	user := u.User.Username()
	_, hasPass := u.User.Password()
	if !hasPass {
		return raw
	}
	u2 := *u
	u2.User = url.UserPassword(user, "***")
	return u2.String()
}

func runNATSPub(nurl, subject string, body []byte) {
	c, err := natsbus.Connect(nurl)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	if err := c.Publish(subject, body); err != nil {
		log.Fatal(err)
	}
	if err := c.FlushTimeout(2 * time.Second); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("published to %s (%d bytes)\n", subject, len(body))
}

func runNATSSub(nurl, subject string, wait int, timeout time.Duration) {
	c, err := natsbus.Connect(nurl)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	sub, err := c.Conn().SubscribeSync(subject)
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx := context.Background()
	for i := 0; i < wait; i++ {
		ctxMsg, cancel := context.WithTimeout(ctx, timeout)
		msg, err := sub.NextMsgWithContext(ctxMsg)
		cancel()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("[%d] %s\n", i+1, string(msg.Data))
	}
}

func runRegistryOrPing(ctx context.Context, cmd string, rest []string, regAddr string) {
	switch cmd {
	case "list":
		conn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
		if len(rest) != 2 {
			log.Fatal("resolve: need x z")
		}
		x, err := strconv.ParseFloat(rest[0], 64)
		if err != nil {
			log.Fatal(err)
		}
		z, err := strconv.ParseFloat(rest[1], 64)
		if err != nil {
			log.Fatal(err)
		}
		conn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	case "forward-npc-handoff":
		if len(rest) < 2 {
			log.Fatal("forward-npc-handoff: need <parent_cell_id> <child_cell_id> [reason]")
		}
		parentID := rest[0]
		childID := rest[1]
		reason := "mmoctl"
		if len(rest) >= 3 {
			reason = rest[2]
		}
		conn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewRegistryClient(conn)
		resp, err := cl.ForwardNpcHandoff(ctx, &cellv1.ForwardNpcHandoffRequest{
			ParentCellId: parentID,
			ChildCellId:  childID,
			Reason:       reason,
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ok=%v npc_entities=%d %s\n", resp.GetOk(), resp.GetNpcEntityCount(), resp.GetMessage())
	case "forward-update":
		if len(rest) < 2 {
			log.Fatal("forward-update: need <cell_id> noop | tps | split-prepare | split-drain | export-npc-persist | import-npc-persist ...")
		}
		cellID := rest[0]
		var upd *cellv1.UpdateRequest
		switch rest[1] {
		case "noop":
			upd = &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_Noop{Noop: &cellv1.CellUpdateNoop{}}}
		case "tps":
			if len(rest) != 3 {
				log.Fatal("forward-update: tps needs integer")
			}
			v, err := strconv.ParseInt(rest[2], 10, 32)
			if err != nil {
				log.Fatal(err)
			}
			upd = &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_SetTargetTps{SetTargetTps: int32(v)}}
		case "split-prepare":
			reason := "mmoctl"
			if len(rest) >= 3 {
				reason = rest[2]
			}
			upd = &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_SplitPrepare{
				SplitPrepare: &cellv1.CellUpdateSplitPrepare{Reason: reason},
			}}
		case "split-drain":
			if len(rest) != 3 {
				log.Fatal("forward-update: split-drain needs true or false")
			}
			var en bool
			switch rest[2] {
			case "true", "1", "yes":
				en = true
			case "false", "0", "no":
				en = false
			default:
				log.Fatalf("forward-update: split-drain: use true or false, got %q", rest[2])
			}
			upd = &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_SetSplitDrain{
				SetSplitDrain: &cellv1.CellUpdateSetSplitDrain{Enabled: en},
			}}
		case "export-npc-persist":
			reason := "mmoctl"
			if len(rest) >= 3 {
				reason = rest[2]
			}
			upd = &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_ExportNpcPersist{
				ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: reason},
			}}
		case "import-npc-persist":
			if len(rest) < 3 {
				log.Fatal("forward-update: import-npc-persist needs <path|-> [reason]")
			}
			path := rest[2]
			reason := "mmoctl"
			if len(rest) >= 4 {
				reason = rest[3]
			}
			var body []byte
			var rerr error
			if path == "-" {
				body, rerr = io.ReadAll(os.Stdin)
			} else {
				body, rerr = os.ReadFile(path)
			}
			if rerr != nil {
				log.Fatal(rerr)
			}
			upd = &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_ImportNpcPersist{
				ImportNpcPersist: &cellv1.CellUpdateImportNpcPersist{
					NpcImportJson: string(body),
					Reason:        reason,
				},
			}}
		default:
			log.Fatalf("forward-update: unknown mode %q", rest[1])
		}
		conn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewRegistryClient(conn)
		resp, err := cl.ForwardCellUpdate(ctx, &cellv1.ForwardCellUpdateRequest{CellId: cellID, Update: upd})
		if err != nil {
			log.Fatal(err)
		}
		if resp.GetNpcExportJson() != "" {
			fmt.Printf("ok=%v %s npc_export_json_bytes=%d\n", resp.Ok, resp.Message, len(resp.GetNpcExportJson()))
		} else {
			fmt.Printf("ok=%v %s\n", resp.Ok, resp.Message)
		}
	case "migration-dry-run":
		if len(rest) < 1 {
			log.Fatal("migration-dry-run: need cell_id")
		}
		cellID := strings.TrimSpace(rest[0])
		rconn, err := grpc.NewClient(regAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer rconn.Close()
		rcl := cellv1.NewRegistryClient(rconn)
		list, err := rcl.ListCells(ctx, &cellv1.ListCellsRequest{})
		if err != nil {
			log.Fatal(err)
		}
		var spec *cellv1.CellSpec
		for _, c := range list.GetCells() {
			if c.GetId() == cellID {
				spec = c
				break
			}
		}
		if spec == nil {
			log.Fatalf("migration-dry-run: cell %q not in registry", cellID)
		}
		ep := spec.GetGrpcEndpoint()
		if ep == "" {
			log.Fatal("migration-dry-run: empty grpc_endpoint")
		}
		cconn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer cconn.Close()
		ccl := cellv1.NewCellClient(cconn)
		mc, err := ccl.ListMigrationCandidates(ctx, &cellv1.ListMigrationCandidatesRequest{Reason: "migration-dry-run"})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ListMigrationCandidates total=%d (cell %s direct gRPC)\n", len(mc.GetCandidates()), cellID)
		exp, err := rcl.ForwardCellUpdate(ctx, &cellv1.ForwardCellUpdateRequest{
			CellId: cellID,
			Update: &cellv1.UpdateRequest{Payload: &cellv1.UpdateRequest_ExportNpcPersist{
				ExportNpcPersist: &cellv1.CellUpdateExportNpcPersist{Reason: "migration-dry-run"},
			}},
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ForwardCellUpdate export ok=%v %s json_bytes=%d\n", exp.Ok, exp.Message, len(exp.GetNpcExportJson()))
	case "plansplit":
		if len(rest) != 1 {
			log.Fatal("plansplit: need host:port")
		}
		ep := rest[0]
		conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewCellClient(conn)
		resp, err := cl.PlanSplit(ctx, &cellv1.PlanSplitRequest{Reason: "mmoctl"})
		if err != nil {
			log.Fatal(err)
		}
		for _, ch := range resp.Children {
			b := ch.GetBounds()
			if b == nil {
				fmt.Printf("%s level=%d bounds=<nil>\n", ch.Id, ch.Level)
				continue
			}
			fmt.Printf("%s level=%d bounds=[%.0f,%.0f]x[%.0f,%.0f]\n",
				ch.Id, ch.Level, b.XMin, b.XMax, b.ZMin, b.ZMax)
		}
	case "migration-candidates":
		if len(rest) < 1 {
			log.Fatal("migration-candidates: need host:port")
		}
		ep := rest[0]
		reason := "mmoctl"
		if len(rest) >= 2 {
			reason = rest[1]
		}
		conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewCellClient(conn)
		resp, err := cl.ListMigrationCandidates(ctx, &cellv1.ListMigrationCandidatesRequest{Reason: reason})
		if err != nil {
			log.Fatal(err)
		}
		for _, c := range resp.Candidates {
			pos := c.GetPosition()
			if pos == nil {
				fmt.Printf("entity=%d is_player=%v position=<nil>\n", c.EntityId, c.IsPlayer)
				continue
			}
			fmt.Printf("entity=%d is_player=%v pos=(%.3f,%.3f,%.3f)\n",
				c.EntityId, c.IsPlayer, pos.X, pos.Y, pos.Z)
		}
		fmt.Printf("total=%d\n", len(resp.Candidates))
	case "ping":
		if len(rest) != 1 {
			log.Fatal("ping: need host:port")
		}
		ep := rest[0]
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
	case "join":
		if len(rest) != 2 {
			log.Fatal("join: need host:port player_id")
		}
		ep := rest[0]
		playerID := rest[1]
		conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Fatal(err)
		}
		defer conn.Close()
		cl := cellv1.NewCellClient(conn)
		j, err := cl.Join(ctx, &cellv1.JoinRequest{PlayerId: playerID})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ok=%v cell_id=%s entity_id=%d msg=%s\n", j.Ok, j.CellId, j.EntityId, j.Message)
	default:
		log.Fatalf("unknown command %q", cmd)
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
