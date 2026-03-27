package registrysvc

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var rpcDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Namespace: "mmo",
		Subsystem: "grid_registry",
		Name:      "rpc_duration_seconds",
		Help:      "Registry gRPC handler wall time",
		Buckets:   prometheus.DefBuckets,
	},
	[]string{"method"},
)

func observeRPCDuration(method string, start time.Time) {
	rpcDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
}
