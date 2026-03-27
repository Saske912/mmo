// Package logging настраивает slog по переменной окружения (удобно для Loki).
package logging

import (
	"log"
	"os"
	"strings"

	"log/slog"
)

// SetupFromEnv: при MMO_LOG_FORMAT=json пишет structured JSON в stdout для корреляции в Loki.
func SetupFromEnv() {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("MMO_LOG_FORMAT")), "json") {
		h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(h))
		log.SetOutput(os.Stdout)
	}
}
