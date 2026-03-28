package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	gatewayWsSessions = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "ws_sessions_total",
		Help:      "WebSocket sessions that completed Join to a cell",
	})
	gatewayApplyInput = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "apply_input_total",
		Help:      "ClientInput forwarded to cell via ApplyInput",
	}, []string{"status"})

	gatewayRegistryResolveDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "registry_resolve_duration_seconds",
		Help:      "Duration of Registry.ResolvePosition gRPC call",
		Buckets:   prometheus.DefBuckets,
	})
	gatewayCellJoinDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "cell_join_duration_seconds",
		Help:      "Duration of Cell.Join gRPC call",
		Buckets:   prometheus.DefBuckets,
	}, []string{"result"})
	gatewayCellApplyInputDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "cell_apply_input_duration_seconds",
		Help:      "Duration of Cell.ApplyInput gRPC call",
		Buckets:   prometheus.DefBuckets,
	}, []string{"result"})

	// Счётчик: последняя сота в БД не совпала с результатом Registry.ResolvePosition (смена покрытия / handoff).
	gatewayCellHandoffMismatch = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "cell_handoff_mismatch_total",
		Help:      "WebSocket connects where mmo_player_last_cell.cell_id differed from resolved cell id",
	})
)
