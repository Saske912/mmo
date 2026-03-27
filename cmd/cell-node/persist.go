package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	gamev1 "mmo/gen/gamev1"
	"mmo/internal/cellsim"
	"mmo/internal/cellsim/snapshot"
	"mmo/internal/grpc/cellsvc"
)

func redisStateKey(cellID string) string {
	return fmt.Sprintf("mmo:cell:%s:state", cellID)
}

func openRedis(addr, password string) *redis.Client {
	if addr == "" {
		return nil
	}
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
}

func loadPersistedState(ctx context.Context, rdb *redis.Client, key string, sim *cellsim.Runtime) bool {
	if rdb == nil {
		return false
	}
	b, err := rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false
	}
	if err != nil {
		log.Printf("redis get %s: %v", key, err)
		return false
	}
	var p gamev1.CellPersist
	if err := proto.Unmarshal(b, &p); err != nil {
		log.Printf("persist unmarshal: %v", err)
		return false
	}
	sim.Mu.Lock()
	err = snapshot.Decode(sim.World, sim.Loop, &p)
	sim.Mu.Unlock()
	if err != nil {
		log.Printf("persist decode: %v", err)
		return false
	}
	log.Printf("restored cell state from redis key=%s tick=%d tps=%.1f entities=%d", key, p.Tick, sim.Loop.TPS, len(p.Entities))
	return true
}

func savePersistedState(ctx context.Context, rdb *redis.Client, key string, sim *cellsim.Runtime, cellSvc *cellsvc.Server) {
	if rdb == nil || cellSvc == nil {
		return
	}
	sim.Mu.Lock()
	p := snapshot.Encode(sim, cellSvc.IsPlayer)
	sim.Mu.Unlock()
	b, err := proto.Marshal(p)
	if err != nil {
		log.Printf("persist marshal: %v", err)
		return
	}
	if err := rdb.Set(ctx, key, b, 0).Err(); err != nil {
		log.Printf("redis set %s: %v", key, err)
		return
	}
	log.Printf("saved cell state to redis key=%s tick=%d entities=%d", key, p.Tick, len(p.Entities))
}

// tryLoadAndMaybeSpawnNPCs: при успешном restore демо-NPC не спавним повторно.
func tryLoadAndMaybeSpawnNPCs(
	ctx context.Context,
	rdb *redis.Client,
	key string,
	sim *cellsim.Runtime,
	demoNPCs int,
	usePersist bool,
) bool {
	if !usePersist || rdb == nil {
		if demoNPCs > 0 {
			sim.SpawnDemoNPCs(demoNPCs)
			log.Printf("ECS demo: spawned %d NPCs", demoNPCs)
		}
		return false
	}
	loadCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if loadPersistedState(loadCtx, rdb, key, sim) {
		return true
	}
	if demoNPCs > 0 {
		sim.SpawnDemoNPCs(demoNPCs)
		log.Printf("ECS demo: spawned %d NPCs", demoNPCs)
	}
	return false
}

func saveOnShutdown(shutdownCtx context.Context, rdb *redis.Client, key string, sim *cellsim.Runtime, cellSvc *cellsvc.Server, usePersist bool) {
	if !usePersist || rdb == nil {
		return
	}
	saveCtx, cancel := context.WithTimeout(shutdownCtx, 5*time.Second)
	defer cancel()
	savePersistedState(saveCtx, rdb, key, sim, cellSvc)
}
