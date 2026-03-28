package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"mmo/internal/cellsim"
	"mmo/internal/grpc/cellsvc"

	"go.opentelemetry.io/otel"
)

func wirePrometheus(addr string, cellSvc *cellsvc.Server, sim *cellsim.Runtime) {
	if addr == "" {
		return
	}

	ticks := promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "cell",
		Name:      "ticks_total",
		Help:      "ECS simulation steps completed",
	})
	applyIn := promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "mmo",
		Subsystem: "cell",
		Name:      "apply_input_total",
		Help:      "ApplyInput RPC outcomes",
	}, []string{"status"})
	tickDur := promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "mmo",
		Subsystem: "cell",
		Name:      "tick_step_duration_seconds",
		Help:      "ECS GameLoop Step wall time",
		Buckets:   prometheus.DefBuckets,
	})

	tickOtel := strings.EqualFold(strings.TrimSpace(os.Getenv("MMO_CELL_OTEL_TICK_SPAN")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("MMO_CELL_OTEL_TICK_SPAN")), "true")
	sim.OnTick = func() {
		if tickOtel {
			_, sp := otel.Tracer("mmo/cell").Start(context.Background(), "Cell.GameLoop.Tick")
			defer sp.End()
		}
		ticks.Inc()
		if sim.Loop != nil {
			tickDur.Observe(sim.Loop.Stats.LastTickDur.Seconds())
		}
	}
	cellSvc.SetApplyInputHook(func(ok bool) {
		if ok {
			applyIn.WithLabelValues("ok").Inc()
		} else {
			applyIn.WithLabelValues("err").Inc()
		}
	})

	_ = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "mmo",
		Subsystem: "cell",
		Name:      "players",
		Help:      "Players currently joined on this cell",
	}, func() float64 {
		return float64(cellSvc.PlayerCount())
	})

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Printf("cell metrics http://%s/metrics", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("metrics listen: %v", err)
		}
	}()
}
