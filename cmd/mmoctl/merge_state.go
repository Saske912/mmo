package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"mmo/internal/config"
)

// runMergeState печатает JSON из Redis mmo:grid:merge:state:<parent_id>.
func runMergeState(args []string) {
	if len(args) < 1 {
		log.Fatal("merge-state: need <parent_cell_id>")
	}
	parent := strings.TrimSpace(args[0])
	if parent == "" {
		log.Fatal("merge-state: empty parent_cell_id")
	}
	cfg := config.FromEnv()
	if cfg.RedisAddr == "" {
		log.Fatal("merge-state: REDIS_ADDR is required")
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       0,
	})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := "mmo:grid:merge:state:" + parent
	val, err := rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		fmt.Printf("{}\n")
		return
	}
	if err != nil {
		log.Fatalf("merge-state: redis: %v", err)
	}
	fmt.Println(val)
}
