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
	gatewayCellLeaveTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "cell_leave_total",
		Help:      "Cell.Leave attempts when disconnecting or switching downstream",
	}, []string{"phase", "result"})
	gatewayDownstreamCloseTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "downstream_close_total",
		Help:      "Downstream gRPC connection closes by phase",
	}, []string{"phase", "result"})
	gatewayDownstreamSwitchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "downstream_switch_total",
		Help:      "Downstream switch attempts by reason and result",
	}, []string{"reason", "result"})
	gatewayAttachedCellPlayers = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "attached_cell_players",
		Help:      "Online players currently attached to cell_id in gateway sessions",
	}, []string{"cell_id"})
	gatewayResolvedCellMismatchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "resolved_cell_mismatch_total",
		Help:      "ResolvePosition result differs from currently attached cell in active gateway session",
	}, []string{"attached_cell_id", "resolved_cell_id"})
	gatewayCellTransitionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "cell_transition_total",
		Help:      "Gateway cell transitions between attached cells",
	}, []string{"from_cell_id", "to_cell_id", "phase", "result"})
	gatewayPositionSwitchSkippedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "position_switch_skipped_total",
		Help:      "Proactive position-based switch skipped by reason",
	}, []string{"reason"})

	// Счётчик: последняя сота в БД не совпала с результатом Registry.ResolvePosition (смена покрытия / handoff).
	gatewayCellHandoffMismatch = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "gateway",
		Name:      "cell_handoff_mismatch_total",
		Help:      "WebSocket connects where mmo_player_last_cell.cell_id differed from resolved cell id",
	})
)
