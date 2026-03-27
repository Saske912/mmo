package registrysvc

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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

func incRPC(method string, err error) {
	code := "ok"
	if err != nil {
		code = "error"
	}
	rpcTotal.WithLabelValues(method, code).Inc()
}
