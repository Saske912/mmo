package registrysvc

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/status"
)

var rpcTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "grid_registry",
		Name:      "rpc_total",
		Help:      "Registry gRPC calls",
	},
	[]string{"method", "code"},
)

var forwardPlayerHandoffStageTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "grid_registry",
		Name:      "forward_player_handoff_stage_total",
		Help:      "ForwardPlayerHandoff stage outcomes by stage/result/grpc_code",
	},
	[]string{"stage", "result", "grpc_code"},
)

func incRPC(method string, err error) {
	code := "ok"
	if err != nil {
		code = "error"
	}
	rpcTotal.WithLabelValues(method, code).Inc()
}

func observeForwardPlayerHandoffStage(stage string, err error) {
	result := "ok"
	grpcCode := "OK"
	if err != nil {
		result = "error"
		grpcCode = status.Code(err).String()
	}
	forwardPlayerHandoffStageTotal.WithLabelValues(stage, result, grpcCode).Inc()
}
