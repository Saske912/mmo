package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"mmo/internal/db"
	"mmo/internal/logging"
)

type ingestRequest struct {
	ChainID int64         `json:"chain_id"`
	Events  []ingestEvent `json:"events"`
}

type ingestEvent struct {
	BlockNumber     int64           `json:"block_number"`
	BlockHash       string          `json:"block_hash"`
	TxHash          string          `json:"tx_hash"`
	LogIndex        int32           `json:"log_index"`
	ContractAddress string          `json:"contract_address"`
	EventName       string          `json:"event_name"`
	PlayerID        string          `json:"player_id,omitempty"`
	WalletAddress   string          `json:"wallet_address,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

func main() {
	logging.SetupFromEnv()
	log.SetFlags(0)

	addr := firstNonEmpty(os.Getenv("WEB3_INDEXER_LISTEN"), ":8091")
	dbURL := firstNonEmpty(os.Getenv("DATABASE_URL_RW"), os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		log.Fatal("DATABASE_URL_RW or DATABASE_URL required")
	}
	defaultChainID := parseInt64(os.Getenv("WEB3_INDEXER_CHAIN_ID"), 0)
	redisAddr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	redisPass := os.Getenv("REDIS_PASSWORD")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.OpenPool(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	var rdb *redis.Client
	if redisAddr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass, DB: 0})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := pingDB(w, pool); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if rdb != nil {
			if err := rdb.Ping(context.Background()).Err(); err != nil {
				http.Error(w, "redis ping failed: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/v1/indexer/ingest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req ingestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
			return
		}
		chainID := req.ChainID
		if chainID <= 0 {
			chainID = defaultChainID
		}
		if chainID <= 0 {
			http.Error(w, "chain_id required", http.StatusBadRequest)
			return
		}
		if len(req.Events) == 0 {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ingested":0}`))
			return
		}
		rows := make([]db.ChainEventRow, 0, len(req.Events))
		var maxBlock int64
		var maxBlockHash string
		for _, ev := range req.Events {
			rows = append(rows, db.ChainEventRow{
				ChainID:         chainID,
				BlockNumber:     ev.BlockNumber,
				BlockHash:       ev.BlockHash,
				TxHash:          ev.TxHash,
				LogIndex:        ev.LogIndex,
				ContractAddress: ev.ContractAddress,
				EventName:       ev.EventName,
				Payload:         ev.Payload,
			})
			if ev.BlockNumber >= maxBlock {
				maxBlock = ev.BlockNumber
				maxBlockHash = ev.BlockHash
			}
			if err := db.UpsertPlayerWalletAddress(r.Context(), pool, ev.PlayerID, ev.WalletAddress, chainID); err != nil {
				http.Error(w, "wallet upsert failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if err := db.InsertChainEvents(r.Context(), pool, rows); err != nil {
			http.Error(w, "event insert failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if maxBlock > 0 {
			if err := db.UpsertChainCursor(r.Context(), pool, chainID, maxBlock, maxBlockHash); err != nil {
				http.Error(w, "cursor upsert failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if rdb != nil {
				key := "mmo:web3:cursor:" + strconv.FormatInt(chainID, 10)
				if err := rdb.Set(r.Context(), key, strconv.FormatInt(maxBlock, 10), 24*time.Hour).Err(); err != nil {
					http.Error(w, "redis cursor write failed: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ingested":  len(rows),
			"chain_id":  chainID,
			"max_block": maxBlock,
		})
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("web3-indexer listening on %s", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func parseInt64(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func pingDB(w http.ResponseWriter, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return pool.Ping(ctx)
}
