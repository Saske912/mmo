// Smoke: POST тестовый Transfer на web3-indexer и проверка строки в mmo_chain_tx_event.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"mmo/internal/db"
	"mmo/internal/web3ingest"
)

func main() {
	log.SetFlags(0)
	indexerURL := flag.String("indexer-url", "", "например http://127.0.0.1:8091 или из port-forward")
	chainID := flag.Int64("chain-id", 11155111, "chain id в теле и проверке БД")
	apiKey := flag.String("api-key", os.Getenv("WEB3_INDEXER_INGEST_API_KEY"), "X-MMO-Ingest-Key")
	hmacSecret := flag.String("hmac-secret", os.Getenv("WEB3_INDEXER_INGEST_HMAC_SECRET"), "секрет HMAC")
	dbURL := flag.String("database-url", firstNonEmpty(os.Getenv("DATABASE_URL_RW"), os.Getenv("DATABASE_URL")), "для проверки INSERT")
	timeout := flag.Duration("timeout", 30*time.Second, "")
	flag.Parse()

	if strings.TrimSpace(*indexerURL) == "" {
		log.Fatal("need -indexer-url")
	}
	if strings.TrimSpace(*dbURL) == "" {
		log.Fatal("need -database-url или DATABASE_URL_RW")
	}

	txHash := fmt.Sprintf("0xsmoke%d", time.Now().UnixNano())
	body := map[string]any{
		"chain_id": *chainID,
		"events": []map[string]any{
			{
				"block_number":     999001,
				"block_hash":       "0xsmokeblock",
				"tx_hash":          txHash,
				"log_index":        0,
				"contract_address": "0xBetTokenSmoke",
				"event_name":       "Transfer",
				"player_id":        "smoke-player",
				"wallet_address":   "0xWalletSmoke",
				"payload":          map[string]any{"from": "0x0", "to": "0xWalletSmoke", "value": "1"},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		log.Fatal(err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, strings.TrimRight(*indexerURL, "/")+"/v1/indexer/ingest", bytes.NewReader(raw))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(*apiKey); k != "" {
		req.Header.Set("X-MMO-Ingest-Key", k)
	}
	if s := strings.TrimSpace(*hmacSecret); s != "" {
		req.Header.Set("X-MMO-Ingest-Signature", web3ingest.ComputeHMACSignatureHex(s, raw))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("ingest HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	pool, err := db.OpenPool(ctx, *dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	var n int
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mmo_chain_tx_event WHERE chain_id = $1 AND tx_hash = $2 AND log_index = $3`,
		*chainID, txHash, 0,
	).Scan(&n)
	if err != nil {
		log.Fatal(err)
	}
	if n != 1 {
		log.Fatalf("expected 1 row in mmo_chain_tx_event, got %d (tx_hash=%s)", n, txHash)
	}
	fmt.Printf("OK: ingest + DB row for tx_hash=%s chain_id=%d\n", txHash, *chainID)
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}
