// Программа однократного применения goose-миграций (для Kubernetes Job вместо RunMigrations в gateway).
package main

import (
	"context"
	"log"
	"time"

	"mmo/internal/config"
	"mmo/internal/db"
)

func main() {
	log.SetFlags(0)
	cfg := config.FromEnv()
	if cfg.DatabaseURLRW == "" {
		log.Fatal("DATABASE_URL_RW or DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.RunMigrations(ctx, cfg.DatabaseURLRW); err != nil {
		log.Fatal(err)
	}
	log.Print("migrations: ok")
}
