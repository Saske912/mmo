package registrysvc

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var mergeWorkflowRunsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "grid_manager",
		Name:      "merge_workflow_runs_total",
		Help:      "Total merge workflow runs by result",
	},
	[]string{"result"},
)

var mergeWorkflowDurationSeconds = promauto.NewHistogram(
	prometheus.HistogramOpts{
		Namespace: "mmo",
		Subsystem: "grid_manager",
		Name:      "merge_workflow_duration_seconds",
		Help:      "Merge workflow duration in seconds",
		Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 40, 80},
	},
)

func observeMergeWorkflowDuration(start time.Time) {
	mergeWorkflowDurationSeconds.Observe(time.Since(start).Seconds())
}
