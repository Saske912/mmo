package main

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	natsbus "mmo/internal/bus/nats"
	"mmo/internal/config"
	"mmo/internal/logging"
)

type splitWorkflowEvent struct {
	CellID     string            `json:"cell_id"`
	Stage      string            `json:"stage"`
	Attempt    int               `json:"attempt"`
	Message    string            `json:"message"`
	ChildCell  string            `json:"child_cell,omitempty"`
	Attrs      map[string]string `json:"attrs,omitempty"`
	AtUnixMs   int64             `json:"at_unix_ms"`
	Successful bool              `json:"successful"`
}

func main() {
	logging.SetupFromEnv()
	log.SetFlags(0)

	cfg := config.FromEnv()
	if cfg.NATSURL == "" {
		log.Fatal("NATS_URL is required for cell-controller")
	}
	client, err := natsbus.ConnectResilient(cfg.NATSURL, natsbus.DefaultReconnectConfig())
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	var rdb *redis.Client
	if cfg.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       0,
		})
		defer rdb.Close()
	}

	subj := strings.TrimSpace(os.Getenv("MMO_CELL_CONTROLLER_SUBJECT"))
	if subj == "" {
		subj = natsbus.SubjectGridSplitWorkflow
	}
	_, err = client.Subscribe(subj, func(msg *nats.Msg) {
		handleEvent(msg.Data, rdb)
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := client.FlushTimeout(2 * time.Second); err != nil {
		log.Fatal(err)
	}
	log.Printf("cell-controller subscribed: %s", subj)
	select {}
}

func handleEvent(raw []byte, rdb *redis.Client) {
	var ev splitWorkflowEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		slog.Warn("cell-controller: bad event", "err", err)
		return
	}
	if ev.CellID == "" {
		return
	}
	slog.Info("cell-controller event", "cell_id", ev.CellID, "stage", ev.Stage, "attempt", ev.Attempt)
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Foundation storage for controller decisions/observability.
	_ = rdb.Set(ctx, "mmo:cell-controller:last:"+ev.CellID, raw, 24*time.Hour).Err()
	if ev.Stage == "parent_retiring" && ev.Successful {
		_ = rdb.Set(ctx, "mmo:cell-controller:retire:"+ev.CellID, "1", 24*time.Hour).Err()
	}
}
