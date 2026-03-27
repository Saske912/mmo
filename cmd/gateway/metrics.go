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
)
