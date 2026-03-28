// Package logging настраивает slog по переменной окружения (удобно для Loki).
package logging

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// traceAttrsHandler добавляет trace_id и span_id из контекста OTel к записи (хэндлер JSON).
type traceAttrsHandler struct {
	inner slog.Handler
}

func (h *traceAttrsHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *traceAttrsHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

func (h *traceAttrsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceAttrsHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceAttrsHandler) WithGroup(name string) slog.Handler {
	return &traceAttrsHandler{inner: h.inner.WithGroup(name)}
}

// SetupFromEnv: при MMO_LOG_FORMAT=json пишет structured JSON в stdout для корреляции в Loki;
// при вызове slog с контекстом активного span в поля попадут trace_id и span_id (корреляция с Tempo).
func SetupFromEnv() {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MMO_LOG_FORMAT")), "json") {
		base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(&traceAttrsHandler{inner: base}))
		log.SetOutput(os.Stdout)
	}
}
