package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cellv1 "mmo/gen/cellv1"
	"mmo/internal/discovery"
)

const cellPingTimeout = 3 * time.Second

var (
	cellProbePlayers = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_players",
			Help:      "Players on cell (from Cell.Ping), keyed by cell_id",
		},
		[]string{"cell_id"},
	)
	cellProbeEntities = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_entities",
			Help:      "ECS entities on cell (from Cell.Ping)",
		},
		[]string{"cell_id"},
	)
	cellProbeLastTickSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_last_tick_seconds",
			Help:      "Last GameLoop step wall time on cell (from Cell.Ping)",
		},
		[]string{"cell_id"},
	)
	cellProbeTps = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_configured_tps",
			Help:      "Configured TPS on cell (from Cell.Ping)",
		},
		[]string{"cell_id"},
	)
	cellProbeReachable = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_reachable",
			Help:      "1 if Cell.Ping succeeded, else 0",
		},
		[]string{"cell_id"},
	)
	cellWithinHardLimits = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_within_hard_limits",
			Help:      "1 if cell is reachable and probed load is within MMO_GRID_THRESHOLD_* bounds, else 0",
		},
		[]string{"cell_id"},
	)
	cellThresholdViolation = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "mmo",
			Subsystem: "grid_manager",
			Name:      "cell_threshold_violation",
			Help:      "1 if this violation kind is active for the cell (from grid-manager probe vs hardcoded thresholds)",
		},
		[]string{"cell_id", "kind"},
	)
)

var cellThresholdViolationKinds = []string{"players", "entities", "tick", "unreachable"}

func cellProbeInterval() time.Duration {
	s := strings.TrimSpace(os.Getenv("MMO_GRID_CELL_PROBE_INTERVAL"))
	if s == "" {
		return 10 * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Second {
		return 10 * time.Second
	}
	return d
}

func deleteCellProbeLabels(cellID string) {
	for _, v := range []*prometheus.GaugeVec{
		cellProbePlayers,
		cellProbeEntities,
		cellProbeLastTickSeconds,
		cellProbeTps,
		cellProbeReachable,
		cellWithinHardLimits,
	} {
		v.DeleteLabelValues(cellID)
	}
	for _, k := range cellThresholdViolationKinds {
		cellThresholdViolation.DeleteLabelValues(cellID, k)
	}
}

// startCellLoadProbe периодически опрашивает соты из каталога (Cell.Ping) и выставляет mmo_grid_manager_cell_*.
func startCellLoadProbe(ctx context.Context, cat discovery.Catalog) {
	interval := cellProbeInterval()
	th := parseLoadThresholds()
	log.Printf("cell load probe every %s (MMO_GRID_CELL_PROBE_INTERVAL); thresholds max_players=%d max_entities=%d max_tick_sec=%g (MMO_GRID_THRESHOLD_*)",
		interval, th.maxPlayers, th.maxEntities, th.maxTickSec)
	var mu sync.Mutex
	tracked := make(map[string]struct{})

	t := time.NewTicker(interval)
	go func() {
		defer t.Stop()
		runRound := func() {
			mu.Lock()
			defer mu.Unlock()
			probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			cells, err := cat.List(probeCtx)
			if err != nil {
				log.Printf("cell probe: catalog list: %v", err)
				return
			}
			inCatalog := make(map[string]struct{}, len(cells))
			for _, spec := range cells {
				if spec == nil {
					continue
				}
				id := strings.TrimSpace(spec.GetId())
				if id == "" {
					continue
				}
				inCatalog[id] = struct{}{}
				ep := strings.TrimSpace(spec.GetGrpcEndpoint())
				if ep == "" {
					deleteCellProbeLabels(id)
					delete(tracked, id)
					continue
				}
				ok, players, entities, tickSec, tps := pingCell(probeCtx, ep)
				if ok {
					cellProbeReachable.WithLabelValues(id).Set(1)
					cellProbePlayers.WithLabelValues(id).Set(players)
					cellProbeEntities.WithLabelValues(id).Set(entities)
					cellProbeLastTickSeconds.WithLabelValues(id).Set(tickSec)
					cellProbeTps.WithLabelValues(id).Set(tps)
				} else {
					cellProbeReachable.WithLabelValues(id).Set(0)
					cellProbePlayers.WithLabelValues(id).Set(0)
					cellProbeEntities.WithLabelValues(id).Set(0)
					cellProbeLastTickSeconds.WithLabelValues(id).Set(0)
					cellProbeTps.WithLabelValues(id).Set(0)
				}
				within, viol := evalLoadThresholds(ok, players, entities, tickSec, th)
				cellWithinHardLimits.WithLabelValues(id).Set(within)
				for _, k := range cellThresholdViolationKinds {
					cellThresholdViolation.WithLabelValues(id, k).Set(viol[k])
				}
				tracked[id] = struct{}{}
			}
			for id := range tracked {
				if _, still := inCatalog[id]; !still {
					deleteCellProbeLabels(id)
					delete(tracked, id)
				}
			}
		}
		runRound()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				runRound()
			}
		}
	}()
}

func pingCell(ctx context.Context, endpoint string) (ok bool, players, entities, tickSec, tps float64) {
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false, 0, 0, 0, 0
	}
	defer conn.Close()
	pctx, cancel := context.WithTimeout(ctx, cellPingTimeout)
	defer cancel()
	cl := cellv1.NewCellClient(conn)
	res, err := cl.Ping(pctx, &cellv1.PingRequest{ClientId: "grid-manager-probe"})
	if err != nil || res == nil {
		return false, 0, 0, 0, 0
	}
	return true,
		float64(res.GetPlayerCount()),
		float64(res.GetEntityCount()),
		res.GetLastTickDurationSeconds(),
		res.GetConfiguredTps()
}
