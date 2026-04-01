package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/jackc/pgx/v5/pgxpool"
	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"

	"mmo/internal/config"
	"mmo/internal/db"
	"mmo/internal/logging"
	"mmo/internal/tracing"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const debugLogPath = "/home/pfile/MMO/.cursor/debug.log"

func main() {
	logging.SetupFromEnv()
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	registry := flag.String("registry", "127.0.0.1:9100", "grid-manager Registry host:port")
	jwtSecret := flag.String("jwt-secret", "dev-insecure-change-me", "HMAC ключ для session JWT")
	resX := flag.Float64("resolve-x", 0, "координата для ResolvePosition (выбор соты)")
	resZ := flag.Float64("resolve-z", 0, "")
	flag.Parse()

	shutdownTrace, err := tracing.Init(context.Background(), "gateway")
	if err != nil {
		log.Fatalf("tracing: %v", err)
	}
	defer func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = shutdownTrace(ctx)
	}()

	jwtBytes := []byte(*jwtSecret)
	if v := strings.TrimSpace(os.Getenv("GATEWAY_JWT_SECRET")); v != "" {
		jwtBytes = []byte(v)
	}

	cfg := config.FromEnv()
	var pgPool *pgxpool.Pool
	if strings.TrimSpace(cfg.DatabaseURLRW) != "" {
		dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		p, err := db.OpenPool(dctx, cfg.DatabaseURLRW)
		dcancel()
		if err != nil {
			log.Fatalf("database pool: %v", err)
		}
		if cfg.GatewaySkipDBMigrations {
			log.Printf("database: GATEWAY_SKIP_DB_MIGRATIONS set — не применяем goose (ожидается Job /migrate)")
		} else {
			sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
			err = db.RunMigrations(sctx, cfg.DatabaseURLRW)
			scancel()
			if err != nil {
				p.Close()
				log.Fatalf("database migrations: %v", err)
			}
		}
		pgPool = p
		defer pgPool.Close()
		log.Printf("database: connected (session audit + /readyz)")
	}

	g := &gateway{
		registryAddr:          *registry,
		jwtSecret:             jwtBytes,
		resolveX:              *resX,
		resolveZ:              *resZ,
		db:                    pgPool,
		positionSwitchEnabled: envBoolWithDefault("GATEWAY_POSITION_SWITCH_ENABLED", true),
		positionSwitchMinInterval: parseDurationWithDefault(
			os.Getenv("GATEWAY_POSITION_SWITCH_MIN_INTERVAL"),
			1500*time.Millisecond,
		),
		positionSwitchMinMoveMeters: parseFloatWithDefault(
			os.Getenv("GATEWAY_POSITION_SWITCH_MIN_MOVE_METERS"),
			0.5,
		),
		allowCellIDMismatch: strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH")), "1") ||
			strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH")), "true") ||
			strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH")), "yes"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", g.readyz)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/v1/session", g.session)
	mux.HandleFunc("/v1/me/quests", g.meQuests)
	mux.HandleFunc("/v1/me/quest-progress", g.questProgress)
	mux.HandleFunc("/v1/me/items/add", g.itemsAdd)
	mux.HandleFunc("/v1/me/items/remove", g.itemsRemove)
	mux.HandleFunc("/v1/me/items/transfer", g.itemsTransfer)
	mux.HandleFunc("/v1/me/resolve-preview", g.resolvePreview)
	mux.HandleFunc("/v1/me/last-cell", g.lastCell)
	mux.HandleFunc("/v1/ws", g.ws)

	log.Printf("gateway http://%s registry=%s resolve=(%.1f,%.1f)", *listen, *registry, *resX, *resZ)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

type gateway struct {
	registryAddr                string
	jwtSecret                   []byte
	resolveX                    float64
	resolveZ                    float64
	db                          *pgxpool.Pool
	allowCellIDMismatch         bool
	positionSwitchEnabled       bool
	positionSwitchMinInterval   time.Duration
	positionSwitchMinMoveMeters float64
}

type gatewayDownstream struct {
	cellID   string
	endpoint string
	conn     *grpc.ClientConn
	client   cellv1.CellClient
	entityID uint64
}

type gatewaySession struct {
	playerID              string
	mu                    sync.RWMutex
	ds                    *gatewayDownstream
	lastX                 float64
	lastZ                 float64
	hasPos                bool
	lastPositionResolveAt time.Time
	lastResolveX          float64
	lastResolveZ          float64
	hasResolvedPos        bool
}

func (s *gatewaySession) downstream() *gatewayDownstream {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ds
}

func (s *gatewaySession) setDownstream(ds *gatewayDownstream) {
	s.mu.Lock()
	s.ds = ds
	s.mu.Unlock()
}

func (s *gatewaySession) setPosition(x, z float64) {
	s.mu.Lock()
	s.lastX = x
	s.lastZ = z
	s.hasPos = true
	s.mu.Unlock()
}

func (s *gatewaySession) positionOr(fallbackX, fallbackZ float64) (float64, float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.hasPos {
		return s.lastX, s.lastZ
	}
	return fallbackX, fallbackZ
}

func (s *gatewaySession) position() (float64, float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastX, s.lastZ, s.hasPos
}

func (s *gatewaySession) markPositionResolveAttempt(now time.Time, x, z, minMoveMeters float64, minInterval time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPos {
		return false
	}
	if !s.hasResolvedPos {
		s.hasResolvedPos = true
		s.lastResolveX = x
		s.lastResolveZ = z
		s.lastPositionResolveAt = now
		return true
	}
	distance := math.Hypot(x-s.lastResolveX, z-s.lastResolveZ)
	elapsed := now.Sub(s.lastPositionResolveAt)
	if elapsed < minInterval && distance < minMoveMeters {
		return false
	}
	s.lastResolveX = x
	s.lastResolveZ = z
	s.lastPositionResolveAt = now
	return true
}

func questAPIMap(q db.PlayerQuestAPIRow) map[string]any {
	m := map[string]any{
		"quest_id": q.QuestID, "state": q.State, "progress": q.Progress, "target_progress": q.TargetProgress,
	}
	if q.PrerequisiteQuestID != "" {
		m["prerequisite_quest_id"] = q.PrerequisiteQuestID
	}
	return m
}

// sessionClaims: координаты resolve для WebSocket (новые токены); без mmo_has_resolve — как старые JWT, берём дефолт gateway.
type sessionClaims struct {
	jwt.RegisteredClaims
	MmoHasResolve bool    `json:"mmo_has_resolve,omitempty"`
	MmoRX         float64 `json:"mmo_rx,omitempty"`
	MmoRZ         float64 `json:"mmo_rz,omitempty"`
}

var sessionLimiters sync.Map // IP -> *rate.Limiter

func limiterForIP(ip string) *rate.Limiter {
	v, _ := sessionLimiters.LoadOrStore(ip, rate.NewLimiter(rate.Limit(100), 30))
	return v.(*rate.Limiter)
}

func peerHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *gateway) readyz(w http.ResponseWriter, r *http.Request) {
	if g.db == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	err := g.db.Ping(ctx)
	if err != nil {
		cancel()
		log.Printf("readyz: %v", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	ver, verr := db.LatestAppliedGooseVersion(ctx, g.db)
	cancel()
	if verr != nil {
		log.Printf("readyz goose version: %v", verr)
	} else if ver > 0 {
		w.Header().Set("X-MMO-Goose-Version", fmt.Sprintf("%d", ver))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (g *gateway) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if !limiterForIP(peerHost(r)).Allow() {
		http.Error(w, "rate limit", http.StatusTooManyRequests)
		return
	}
	var body struct {
		PlayerID    string   `json:"player_id"`
		DisplayName string   `json:"display_name,omitempty"`
		ResolveX    *float64 `json:"resolve_x,omitempty"`
		ResolveZ    *float64 `json:"resolve_z,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PlayerID) == "" {
		http.Error(w, `need {"player_id":"..."}`, http.StatusBadRequest)
		return
	}
	bodyHasX := body.ResolveX != nil
	bodyHasZ := body.ResolveZ != nil
	if bodyHasX != bodyHasZ {
		http.Error(w, "provide both resolve_x and resolve_z or omit both", http.StatusBadRequest)
		return
	}
	var rx, rz float64
	if bodyHasX {
		rx, rz = *body.ResolveX, *body.ResolveZ
		if math.IsNaN(rx) || math.IsNaN(rz) {
			http.Error(w, "resolve_x and resolve_z must be valid numbers", http.StatusBadRequest)
			return
		}
	} else {
		rx, rz = g.resolveX, g.resolveZ
		if g.db != nil {
			lctx, lcancel := context.WithTimeout(r.Context(), 2*time.Second)
			if lx, lz, ok, lerr := db.GetPlayerLastCellCoords(lctx, g.db, body.PlayerID); lerr != nil {
				log.Printf("session last_cell: %v", lerr)
			} else if ok {
				rx, rz = lx, lz
			}
			lcancel()
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   body.PlayerID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		MmoHasResolve: true,
		MmoRX:         rx,
		MmoRZ:         rz,
	})
	signed, err := tok.SignedString(g.jwtSecret)
	if err != nil {
		http.Error(w, "token", http.StatusInternalServerError)
		return
	}
	if g.db != nil {
		ictx, icancel := context.WithTimeout(r.Context(), 2*time.Second)
		if ierr := db.RecordSessionIssue(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session audit: %v", ierr)
		}
		if ierr := db.UpsertPlayerProfile(ictx, g.db, body.PlayerID, body.DisplayName); ierr != nil {
			log.Printf("session profile: %v", ierr)
		}
		if ierr := db.EnsurePlayerStats(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session stats ensure: %v", ierr)
		}
		if ierr := db.EnsurePlayerWallet(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session wallet ensure: %v", ierr)
		}
		if ierr := db.EnsurePlayerInventory(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session inventory ensure: %v", ierr)
		}
		if ierr := db.EnsurePlayerQuestSeed(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session quest seed: %v", ierr)
		}
		if _, ierr := db.EnsureUnlockedQuestsForPlayer(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session quest unlock: %v", ierr)
		}
		if ierr := db.EnsureStarterPlayerItems(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session starter items: %v", ierr)
		}
		if ierr := db.SyncPlayerInventoryJSONB(ictx, g.db, body.PlayerID); ierr != nil {
			log.Printf("session inventory sync: %v", ierr)
		}
		icancel()
	}

	out := map[string]any{"token": signed}
	if g.db != nil {
		sctx, scancel := context.WithTimeout(r.Context(), 2*time.Second)
		lvl, xpv, ok, serr := db.GetPlayerStats(sctx, g.db, body.PlayerID)
		if serr != nil {
			log.Printf("session stats read: %v", serr)
		} else if ok {
			out["stats"] = map[string]any{"level": lvl, "xp": xpv}
		}
		gold, wok, werr := db.GetPlayerWallet(sctx, g.db, body.PlayerID)
		if werr != nil {
			log.Printf("session wallet read: %v", werr)
		} else if wok {
			out["wallet"] = map[string]any{"gold": gold}
		}
		inv, iok, ierr := db.GetPlayerInventoryItems(sctx, g.db, body.PlayerID)
		if ierr != nil {
			log.Printf("session inventory read: %v", ierr)
		} else if iok {
			out["inventory"] = inv
		}
		qrows, qerr := db.ListPlayerQuestsForAPI(sctx, g.db, body.PlayerID)
		if qerr != nil {
			log.Printf("session quests read: %v", qerr)
		} else if len(qrows) > 0 {
			qarr := make([]map[string]any, 0, len(qrows))
			for _, q := range qrows {
				qarr = append(qarr, questAPIMap(q))
			}
			out["quests"] = qarr
		}
		items, ierr := db.ListPlayerItemsNormalized(sctx, g.db, body.PlayerID)
		if ierr != nil {
			log.Printf("session items read: %v", ierr)
		} else if len(items) > 0 {
			iarr := make([]map[string]any, 0, len(items))
			for _, it := range items {
				iarr = append(iarr, map[string]any{
					"item_id": it.ItemID, "quantity": it.Quantity, "display_name": it.DisplayName,
				})
			}
			out["items"] = iarr
		}
		scancel()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (g *gateway) meQuests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	qctx, qcancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer qcancel()
	rows, err := db.ListPlayerQuestsForAPI(qctx, g.db, claims.Subject)
	if err != nil {
		log.Printf("me quests: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	qarr := make([]map[string]any, 0, len(rows))
	for _, q := range rows {
		qarr = append(qarr, questAPIMap(q))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"quests": qarr})
}

func (g *gateway) questProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		QuestID  string `json:"quest_id"`
		Progress int    `json:"progress"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.QuestID) == "" {
		http.Error(w, `need {"quest_id":"...","progress":n}`, http.StatusBadRequest)
		return
	}
	qctx, qcancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer qcancel()
	res, err := db.ApplyPlayerQuestProgress(qctx, g.db, claims.Subject, body.QuestID, body.Progress)
	if err != nil {
		log.Printf("quest progress: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	out := map[string]any{
		"ok": true, "completed": res.Completed, "progress": res.Progress, "target_progress": res.TargetProgress,
		"already_complete": res.AlreadyComplete,
	}
	if res.Completed {
		out["gold_reward"] = res.GoldReward
		if len(res.ItemsRewarded) > 0 {
			ir := make([]map[string]any, 0, len(res.ItemsRewarded))
			for _, it := range res.ItemsRewarded {
				ir = append(ir, map[string]any{"item_id": it.ItemID, "quantity": it.Quantity, "display_name": it.DisplayName})
			}
			out["items_rewarded"] = ir
		}
		if len(res.NewlyUnlockedQuests) > 0 {
			out["newly_unlocked_quests"] = res.NewlyUnlockedQuests
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (g *gateway) itemsAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		ItemID   string `json:"item_id"`
		Quantity int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ItemID) == "" || body.Quantity <= 0 {
		http.Error(w, `need {"item_id":"...","quantity":n}`, http.StatusBadRequest)
		return
	}
	actx, acancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer acancel()
	if err := db.AddPlayerItemQuantity(actx, g.db, claims.Subject, body.ItemID, body.Quantity); err != nil {
		log.Printf("items add: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (g *gateway) itemsRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		ItemID   string `json:"item_id"`
		Quantity int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ItemID) == "" || body.Quantity <= 0 {
		http.Error(w, `need {"item_id":"...","quantity":n}`, http.StatusBadRequest)
		return
	}
	actx, acancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer acancel()
	if err := db.RemovePlayerItemQuantity(actx, g.db, claims.Subject, body.ItemID, body.Quantity); err != nil {
		log.Printf("items remove: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (g *gateway) itemsTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if g.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		ToPlayerID string `json:"to_player_id"`
		ItemID     string `json:"item_id"`
		Quantity   int    `json:"quantity"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.ToPlayerID) == "" || strings.TrimSpace(body.ItemID) == "" || body.Quantity <= 0 {
		http.Error(w, `need {"to_player_id":"...","item_id":"...","quantity":n}`, http.StatusBadRequest)
		return
	}
	actx, acancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer acancel()
	if err := db.TransferPlayerItems(actx, g.db, claims.Subject, body.ToPlayerID, body.ItemID, body.Quantity); err != nil {
		log.Printf("items transfer: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (g *gateway) resolvePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	qx := r.URL.Query().Get("resolve_x")
	qz := r.URL.Query().Get("resolve_z")
	if (qx == "") != (qz == "") {
		http.Error(w, "provide both resolve_x and resolve_z", http.StatusBadRequest)
		return
	}
	var rx, rz float64
	if qx != "" {
		rx, err = strconv.ParseFloat(qx, 64)
		if err != nil {
			http.Error(w, "invalid resolve_x", http.StatusBadRequest)
			return
		}
		rz, err = strconv.ParseFloat(qz, 64)
		if err != nil {
			http.Error(w, "invalid resolve_z", http.StatusBadRequest)
			return
		}
		if math.IsNaN(rx) || math.IsNaN(rz) {
			http.Error(w, "resolve_x and resolve_z must be valid numbers", http.StatusBadRequest)
			return
		}
	} else {
		rx, rz = g.resolveX, g.resolveZ
		if g.db != nil {
			lctx, lcancel := context.WithTimeout(r.Context(), 2*time.Second)
			if lx, lz, ok, lerr := db.GetPlayerLastCellCoords(lctx, g.db, claims.Subject); lerr != nil {
				log.Printf("resolve-preview last_cell: %v", lerr)
			} else if ok {
				rx, rz = lx, lz
			}
			lcancel()
		}
	}

	pctx, pcancel := context.WithTimeout(r.Context(), 5*time.Second)
	regCC, err := grpc.NewClient(g.registryAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		pcancel()
		log.Printf("resolve-preview dial: %v", err)
		http.Error(w, "registry unavailable", http.StatusBadGateway)
		return
	}
	defer regCC.Close()
	reg := cellv1.NewRegistryClient(regCC)
	res, err := reg.ResolvePosition(pctx, &cellv1.ResolvePositionRequest{X: rx, Z: rz})
	pcancel()
	if err != nil {
		log.Printf("resolve-preview: %v", err)
		http.Error(w, "registry resolve failed", http.StatusBadGateway)
		return
	}

	resolvedForJSON := res.Cell
	if resolvedForJSON != nil && resolvedForJSON.GetBounds() == nil && strings.TrimSpace(resolvedForJSON.GetId()) != "" {
		eCtx, eCancel := context.WithTimeout(r.Context(), 3*time.Second)
		resolvedForJSON = enrichCellSpecBoundsFromRegistryList(eCtx, reg, resolvedForJSON)
		eCancel()
	}

	out := map[string]any{"resolve_x": rx, "resolve_z": rz}
	if res != nil && res.Found && resolvedForJSON != nil && resolvedForJSON.GetGrpcEndpoint() != "" {
		out["resolved"] = resolvedCellJSON(resolvedForJSON)
	} else {
		out["resolved"] = map[string]any{"found": false}
	}

	var cellMismatch bool
	if g.db != nil {
		lctx, lcancel := context.WithTimeout(r.Context(), 2*time.Second)
		rec, lerr := db.GetPlayerLastCellRecord(lctx, g.db, claims.Subject)
		lcancel()
		if lerr != nil {
			log.Printf("resolve-preview last record: %v", lerr)
		} else if rec != nil {
			out["last_cell"] = map[string]any{
				"found": true, "cell_id": rec.CellID, "resolve_x": rec.ResolveX, "resolve_z": rec.ResolveZ,
			}
			resolvedID := ""
			if res != nil && res.Found && resolvedForJSON != nil {
				resolvedID = resolvedForJSON.GetId()
			}
			if resolvedID != "" && rec.CellID != "" && resolvedID != rec.CellID {
				cellMismatch = true
			}
		} else {
			out["last_cell"] = map[string]any{"found": false}
		}
	}
	out["cell_id_mismatch"] = cellMismatch

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (g *gateway) lastCell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if g.db == nil {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	tokenStr := bearerOrQueryToken(r)
	if tokenStr == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	lctx, lcancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer lcancel()
	rec, err := db.GetPlayerLastCellRecord(lctx, g.db, claims.Subject)
	if err != nil {
		log.Printf("last cell: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if rec == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"found": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"found": true, "cell_id": rec.CellID, "resolve_x": rec.ResolveX, "resolve_z": rec.ResolveZ,
	})
}

func bearerOrQueryToken(r *http.Request) string {
	tokenStr := r.Header.Get("Authorization")
	if strings.HasPrefix(tokenStr, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(tokenStr, "Bearer "))
	}
	return r.URL.Query().Get("token")
}

// enrichCellSpecBoundsFromRegistryList подставляет bounds из ListCells, если ResolvePosition
// вернул CellSpec без границ (старый билд registry, особенности gRPC/Consul-парсера и т.п.).
func enrichCellSpecBoundsFromRegistryList(ctx context.Context, reg cellv1.RegistryClient, cell *cellv1.CellSpec) *cellv1.CellSpec {
	if cell == nil || cell.GetBounds() != nil {
		return cell
	}
	id := strings.TrimSpace(cell.GetId())
	if id == "" {
		return cell
	}
	list, err := reg.ListCells(ctx, &cellv1.ListCellsRequest{})
	if err != nil {
		slog.Debug("gateway enrich bounds: ListCells failed", "cell_id", id, "err", err)
		return cell
	}
	if list == nil {
		return cell
	}
	for _, c := range list.Cells {
		if c == nil || strings.TrimSpace(c.GetId()) != id || c.GetBounds() == nil {
			continue
		}
		out := proto.Clone(cell).(*cellv1.CellSpec)
		out.Bounds = proto.Clone(c.GetBounds()).(*cellv1.Bounds)
		slog.Debug("gateway enrich bounds: ok from ListCells", "cell_id", id)
		return out
	}
	return cell
}

// boundsToJSON сериализует границы соты (XZ) для ответов /v1/me/resolve-preview и 409 /v1/ws.
func boundsToJSON(b *cellv1.Bounds) map[string]any {
	if b == nil {
		return nil
	}
	return map[string]any{
		"x_min": b.GetXMin(),
		"x_max": b.GetXMax(),
		"z_min": b.GetZMin(),
		"z_max": b.GetZMax(),
	}
}

func resolvedCellJSON(cell *cellv1.CellSpec) map[string]any {
	if cell == nil {
		return map[string]any{"found": false}
	}
	if cell.GetGrpcEndpoint() == "" {
		return map[string]any{"found": false}
	}
	out := map[string]any{
		"found": true, "cell_id": cell.GetId(), "grpc_endpoint": cell.GetGrpcEndpoint(),
	}
	if bj := boundsToJSON(cell.GetBounds()); bj != nil {
		out["bounds"] = bj
	}
	return out
}

func (g *gateway) ws(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	claims := &sessionClaims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	playerID := claims.Subject
	rx, rz := g.resolveX, g.resolveZ
	if claims.MmoHasResolve {
		rx, rz = claims.MmoRX, claims.MmoRZ
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	regCC, err := grpc.NewClient(g.registryAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		log.Printf("registry dial: %v", err)
		http.Error(w, "registry unavailable", http.StatusBadGateway)
		return
	}
	defer regCC.Close()
	reg := cellv1.NewRegistryClient(regCC)
	tr := otel.Tracer("mmo/gateway")
	rctx, rspan := tr.Start(ctx, "Registry.ResolvePosition")
	resolveStart := time.Now()
	res, err := reg.ResolvePosition(rctx, &cellv1.ResolvePositionRequest{X: rx, Z: rz})
	rspan.End()
	gatewayRegistryResolveDuration.Observe(time.Since(resolveStart).Seconds())
	if err != nil {
		log.Printf("resolve: %v", err)
		http.Error(w, "registry resolve failed", http.StatusBadGateway)
		return
	}
	if !res.Found || res.Cell == nil || res.Cell.GrpcEndpoint == "" {
		log.Printf("no cell for (%.f, %.f)", rx, rz)
		http.Error(w, "no cell for resolve position", http.StatusServiceUnavailable)
		return
	}
	cellResolved := res.Cell
	if cellResolved.GetBounds() == nil && strings.TrimSpace(cellResolved.GetId()) != "" {
		bCtx, bCancel := context.WithTimeout(ctx, 3*time.Second)
		cellResolved = enrichCellSpecBoundsFromRegistryList(bCtx, reg, cellResolved)
		bCancel()
	}
	ep := cellResolved.GetGrpcEndpoint()
	cellID := cellResolved.GetId()
	slog.InfoContext(rctx, "registry_resolve_ok", "cell_id", cellID, "grpc_endpoint", ep)

	if g.db != nil {
		lctx, lcancel := context.WithTimeout(ctx, 2*time.Second)
		rec, rerr := db.GetPlayerLastCellRecord(lctx, g.db, playerID)
		lcancel()
		if rerr != nil {
			log.Printf("ws handoff check: %v", rerr)
		} else if rec != nil && cellID != "" && rec.CellID != "" && rec.CellID != cellID {
			if g.allowCellIDMismatch {
				slog.WarnContext(ctx, "ws_cell_id_mismatch_allowed",
					"player_id", playerID,
					"last_cell_id", rec.CellID,
					"resolved_cell_id", cellID,
				)
			} else {
				gatewayCellHandoffMismatch.Inc()
				w.Header().Set("X-MMO-Last-Cell-Id", rec.CellID)
				w.Header().Set("X-MMO-Resolved-Cell-Id", cellID)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				resolved409 := resolvedCellJSON(cellResolved)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error":   "cell_handoff_required",
					"message": "last_cell in DB does not match registry resolve for this session; update resolve in POST /v1/session (or GET /v1/me/resolve-preview), then reconnect WebSocket",
					"last_cell": map[string]any{
						"cell_id": rec.CellID, "resolve_x": rec.ResolveX, "resolve_z": rec.ResolveZ,
					},
					"resolved":          resolved409,
					"session_resolve_x": rx,
					"session_resolve_z": rz,
					"hint":              "Prefer coordinates from last_cell or from resolved cell for the desired shard; JWT mmo_rx/mmo_rz must match before /v1/ws",
				})
				return
			}
		}
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	inLim := rate.NewLimiter(rate.Limit(100), 50)
	session := &gatewaySession{playerID: playerID}
	initialDS, _, err := g.attachToCell(ctx, tr, playerID, cellID, ep)
	if err != nil {
		log.Printf("initial attach: %v", err)
		return
	}
	session.setDownstream(initialDS)
	recordAttachedCellAttach(initialDS.cellID)
	recordCellTransition("", initialDS.cellID, "ws_connect", "ok")
	gatewayWsSessions.Inc()
	log.Printf("ws join player=%s entity_id=%d cell=%s", playerID, initialDS.entityID, initialDS.cellID)

	streamCtx, streamCancel := context.WithCancel(ctx)
	streamDone := g.streamDeltasToWS(streamCtx, initialDS, conn, session)
	defer func() {
		streamCancel()
		<-streamDone
		ds := session.downstream()
		if ds != nil {
			recordAttachedCellDetach(ds.cellID)
			recordCellTransition(ds.cellID, "", "disconnect", "ok")
			g.leaveDownstream(context.Background(), ds, playerID, "disconnect", "")
			g.closeDownstreamConn(ds, "disconnect")
			if g.db != nil && ds.cellID != "" {
				px, pz := session.positionOr(rx, rz)
				uctx, ucancel := context.WithTimeout(context.Background(), 2*time.Second)
				if uerr := db.UpsertPlayerLastCell(uctx, g.db, playerID, ds.cellID, px, pz); uerr != nil {
					log.Printf("last_cell persist: %v", uerr)
				}
				ucancel()
			}
		}
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !inLim.Allow() {
			continue
		}
		var in gamev1.ClientInput
		if err := proto.Unmarshal(data, &in); err != nil {
			continue
		}
		current := session.downstream()
		if current == nil {
			return
		}
		if g.positionSwitchEnabled {
			nextDS, switched, serr := g.trySwitchDownstreamByPosition(ctx, tr, reg, session)
			if serr != nil {
				gatewayDownstreamSwitchTotal.WithLabelValues("position_resolve", "error").Inc()
				log.Printf("position switch failed: %v", serr)
			}
			if switched && nextDS != nil {
				gatewayDownstreamSwitchTotal.WithLabelValues("position_resolve", "ok").Inc()
				streamCtx, streamCancel, streamDone = g.applyDownstreamSwitch(
					ctx,
					session,
					streamCancel,
					streamDone,
					conn,
					current,
					nextDS,
					playerID,
					rx,
					rz,
					"switch_position",
				)
				continue
			}
		}
		aCtx, acancel := context.WithTimeout(ctx, 2*time.Second)
		applyStart := time.Now()
		ares, aerr := current.client.ApplyInput(aCtx, &cellv1.ApplyInputRequest{PlayerId: playerID, Input: &in})
		applyDur := time.Since(applyStart).Seconds()
		acancel()
		if aerr != nil || ares == nil || !ares.Ok {
			gatewayCellApplyInputDuration.WithLabelValues("err").Observe(applyDur)
			gatewayApplyInput.WithLabelValues("err").Inc()
			if aerr != nil {
				log.Printf("apply_input: %v", aerr)
			}
			// После split handoff игрок уже на дочерней соте, а сессия ещё на родителе:
			// unknown_player | entity_gone ИЛИ handoff-freeze на родителе — пробуем сменить downstream.
			shouldTrySwitch := (ares != nil && shouldSwitchOnApplyInputMessage(ares.GetMessage())) || shouldSwitchDownstreamOnTransport(aerr)
			if shouldTrySwitch {
				nextDS, switched, serr := g.trySwitchDownstream(ctx, tr, reg, session, rx, rz)
				if serr != nil {
					gatewayDownstreamSwitchTotal.WithLabelValues("apply_input_error", "error").Inc()
					log.Printf("handoff switch failed: %v", serr)
				}
				if switched && nextDS != nil {
					gatewayDownstreamSwitchTotal.WithLabelValues("apply_input_error", "ok").Inc()
					streamCtx, streamCancel, streamDone = g.applyDownstreamSwitch(
						ctx,
						session,
						streamCancel,
						streamDone,
						conn,
						current,
						nextDS,
						playerID,
						rx,
						rz,
						"switch_old",
					)
					continue
				}
			}
			continue
		}
		gatewayCellApplyInputDuration.WithLabelValues("ok").Observe(applyDur)
		gatewayApplyInput.WithLabelValues("ok").Inc()
	}
}

// JSON text кадр до первого WorldChunk: клиент выставляет локального игрока даже без поля viewer_entity_id в protobuf (старые билды / парсеры).
func writeWsEntityMeta(conn *websocket.Conn, entityID uint64, attachedCellID string) {
	if conn == nil || entityID == 0 {
		return
	}
	b, err := json.Marshal(map[string]any{
		"mmo_ws_meta": map[string]any{
			"entity_id": entityID,
			"cell_id":   strings.TrimSpace(attachedCellID),
		},
	})
	if err != nil {
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
		log.Printf("ws entity meta: %v", err)
	}
}

func (g *gateway) streamDeltasToWS(ctx context.Context, ds *gatewayDownstream, conn *websocket.Conn, session *gatewaySession) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		writeWsEntityMeta(conn, ds.entityID, ds.cellID)
		sub, err := ds.client.SubscribeDeltas(ctx, &cellv1.SubscribeDeltasRequest{ViewerEntityId: ds.entityID})
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("subscribe: %v", err)
			}
			return
		}
		entityID := ds.entityID
		for {
			chunk, err := sub.Recv()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if status.Code(err) != codes.Canceled && !errors.Is(err, context.Canceled) {
					log.Printf("SubscribeDeltas recv: %v", err)
				}
				return
			}
			chunk.ViewerEntityId = entityID
			g.observeSelfPosition(session, entityID, chunk)
			b, err := proto.Marshal(chunk)
			if err != nil {
				continue
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
				return
			}
		}
	}()
	return done
}

func (g *gateway) observeSelfPosition(session *gatewaySession, entityID uint64, chunk *cellv1.WorldChunk) {
	if chunk == nil || entityID == 0 {
		return
	}
	if snap := chunk.GetSnapshot(); snap != nil {
		for _, ent := range snap.GetEntities() {
			if ent != nil && ent.GetEntityId() == entityID && ent.GetPosition() != nil {
				session.setPosition(float64(ent.GetPosition().GetX()), float64(ent.GetPosition().GetZ()))
				return
			}
		}
		return
	}
	if delta := chunk.GetDelta(); delta != nil {
		for _, ent := range delta.GetChanged() {
			if ent != nil && ent.GetEntityId() == entityID && ent.GetPosition() != nil {
				session.setPosition(float64(ent.GetPosition().GetX()), float64(ent.GetPosition().GetZ()))
				return
			}
		}
	}
}

func (g *gateway) attachToCell(ctx context.Context, tr trace.Tracer, playerID, cellID, endpoint string) (*gatewayDownstream, string, error) {
	cellCC, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, "", fmt.Errorf("cell dial %s: %w", endpoint, err)
	}
	cellClient := cellv1.NewCellClient(cellCC)
	jctx, jspan := tr.Start(ctx, "Cell.Join")
	defer jspan.End()
	joinStart := time.Now()
	jres, err := cellClient.Join(jctx, &cellv1.JoinRequest{PlayerId: playerID})
	joinResult := "ok"
	if err != nil || jres == nil || !jres.Ok {
		joinResult = "err"
	}
	gatewayCellJoinDuration.WithLabelValues(joinResult).Observe(time.Since(joinStart).Seconds())
	if err != nil || jres == nil || !jres.Ok {
		_ = cellCC.Close()
		return nil, "", fmt.Errorf("join failed: err=%v res=%+v", err, jres)
	}
	joinMsg := strings.TrimSpace(jres.GetMessage())
	slog.InfoContext(jctx, "cell_join_ok", "player_id", playerID, "entity_id", jres.EntityId, "cell_id", cellID, "message", joinMsg)
	return &gatewayDownstream{
		cellID:   cellID,
		endpoint: endpoint,
		conn:     cellCC,
		client:   cellClient,
		entityID: jres.EntityId,
	}, joinMsg, nil
}

func (g *gateway) trySwitchDownstream(ctx context.Context, tr trace.Tracer, reg cellv1.RegistryClient, session *gatewaySession, fallbackX, fallbackZ float64) (*gatewayDownstream, bool, error) {
	cur := session.downstream()
	if cur == nil {
		return nil, false, nil
	}
	px, pz := session.positionOr(fallbackX, fallbackZ)
	rctx, rspan := tr.Start(ctx, "Registry.ResolvePosition.Switch")
	res, err := reg.ResolvePosition(rctx, &cellv1.ResolvePositionRequest{X: px, Z: pz})
	rspan.End()
	if err != nil {
		return nil, false, err
	}
	if !res.GetFound() || res.GetCell() == nil || strings.TrimSpace(res.GetCell().GetGrpcEndpoint()) == "" {
		return nil, false, fmt.Errorf("resolve switch target not found")
	}
	nextCell := res.GetCell()
	if nextCell.GetId() == cur.cellID {
		fallbackDS, switched, ferr := g.tryAttachAlreadyJoinedFromCatalog(ctx, tr, reg, session.playerID, cur.cellID)
		// #region agent log
		debugLogGateway("H2", "cmd/gateway/main.go:trySwitchDownstream.same_cell", "resolve remained on current cell in fallback switch", map[string]any{
			"playerId":         session.playerID,
			"currentCellId":    cur.cellID,
			"resolvedCellId":   nextCell.GetId(),
			"fallbackSwitched": switched,
			"fallbackCellId": func() string {
				if fallbackDS != nil {
					return fallbackDS.cellID
				}
				return ""
			}(),
			"fallbackErr": func() string {
				if ferr != nil {
					return ferr.Error()
				}
				return ""
			}(),
			"x": px,
			"z": pz,
		})
		// #endregion
		if ferr != nil {
			return nil, false, ferr
		}
		if switched && fallbackDS != nil {
			return fallbackDS, true, nil
		}
		return nil, false, nil
	}
	gatewayResolvedCellMismatchTotal.WithLabelValues(metricCellLabel(cur.cellID), metricCellLabel(nextCell.GetId())).Inc()
	// В split/handoff сценарии обычный Join может создать новую сущность в (0,0,0) (spawned),
	// что выглядит как телепорт. Для безопасного switch принимаем только already_joined
	// или полноценный ForwardPlayerHandoff.
	probe, joinMsg, probeErr := g.attachToCell(ctx, tr, session.playerID, nextCell.GetId(), nextCell.GetGrpcEndpoint())
	if probeErr == nil && joinMsg == "already_joined" {
		return probe, true, nil
	}
	if probeErr == nil && probe != nil {
		g.leaveDownstream(ctx, probe, session.playerID, "join_probe_not_existing", cur.cellID)
		g.closeDownstreamConn(probe, "join_probe_not_existing")
	}
	next, err := g.forwardPlayerHandoffSwitch(ctx, tr, reg, session.playerID, cur.cellID, nextCell)
	if err != nil {
		if isParentCellMissingErr(err) {
			recoveredDS, recovered, recoverErr := g.tryRecoverFromMissingParentSwitch(ctx, tr, session.playerID, cur.cellID, nextCell)
			if recoverErr == nil && recovered && recoveredDS != nil {
				return recoveredDS, true, nil
			}
		}
		fallbackDS, switched, ferr := g.tryAttachAlreadyJoinedFromCatalog(ctx, tr, reg, session.playerID, cur.cellID)
		// #region agent log
		debugLogGateway("H3", "cmd/gateway/main.go:trySwitchDownstream.forward_error", "forward handoff failed in fallback switch", map[string]any{
			"playerId":         session.playerID,
			"fromCellId":       cur.cellID,
			"toCellId":         nextCell.GetId(),
			"forwardErr":       err.Error(),
			"fallbackSwitched": switched,
			"fallbackCellId": func() string {
				if fallbackDS != nil {
					return fallbackDS.cellID
				}
				return ""
			}(),
			"fallbackErr": func() string {
				if ferr != nil {
					return ferr.Error()
				}
				return ""
			}(),
		})
		// #endregion
		if ferr == nil && switched && fallbackDS != nil {
			return fallbackDS, true, nil
		}
		return nil, false, err
	}
	return next, true, nil
}

func (g *gateway) tryAttachAlreadyJoinedFromCatalog(
	ctx context.Context,
	tr trace.Tracer,
	reg cellv1.RegistryClient,
	playerID string,
	currentCellID string,
) (*gatewayDownstream, bool, error) {
	lctx, lcancel := context.WithTimeout(ctx, 3*time.Second)
	defer lcancel()
	list, err := reg.ListCells(lctx, &cellv1.ListCellsRequest{})
	if err != nil || list == nil {
		if err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}
	currentCellID = strings.TrimSpace(currentCellID)
	cells := list.GetCells()
	tryCandidate := func(spec *cellv1.CellSpec) (*gatewayDownstream, bool) {
		if spec == nil {
			return nil, false
		}
		id := strings.TrimSpace(spec.GetId())
		ep := strings.TrimSpace(spec.GetGrpcEndpoint())
		if id == "" || ep == "" || id == currentCellID {
			return nil, false
		}
		probe, joinMsg, probeErr := g.attachToCell(ctx, tr, playerID, id, ep)
		if probeErr == nil && joinMsg == "already_joined" {
			// #region agent log
			debugLogGateway("H4", "cmd/gateway/main.go:tryAttachAlreadyJoinedFromCatalog.hit", "catalog probe found already_joined", map[string]any{
				"playerId":      playerID,
				"currentCellId": currentCellID,
				"candidateCell": id,
			})
			// #endregion
			return probe, true
		}
		if probeErr == nil && probe != nil {
			g.leaveDownstream(ctx, probe, playerID, "join_probe_not_existing", currentCellID)
			g.closeDownstreamConn(probe, "join_probe_not_existing")
		}
		return nil, false
	}
	// Сначала пробуем потомков текущей соты (split-путь), затем остальные.
	if currentCellID != "" {
		prefix := currentCellID + "_"
		for _, spec := range cells {
			if spec == nil || !strings.HasPrefix(strings.TrimSpace(spec.GetId()), prefix) {
				continue
			}
			if ds, ok := tryCandidate(spec); ok {
				return ds, true, nil
			}
		}
	}
	for _, spec := range cells {
		if ds, ok := tryCandidate(spec); ok {
			return ds, true, nil
		}
	}
	// #region agent log
	debugLogGateway("H4", "cmd/gateway/main.go:tryAttachAlreadyJoinedFromCatalog.miss", "catalog probe found no already_joined candidate", map[string]any{
		"playerId":      playerID,
		"currentCellId": currentCellID,
		"catalogSize":   len(cells),
	})
	// #endregion
	return nil, false, nil
}

func (g *gateway) trySwitchDownstreamByPosition(ctx context.Context, tr trace.Tracer, reg cellv1.RegistryClient, session *gatewaySession) (*gatewayDownstream, bool, error) {
	cur := session.downstream()
	if cur == nil {
		gatewayPositionSwitchSkippedTotal.WithLabelValues("no_downstream").Inc()
		return nil, false, nil
	}
	px, pz, hasPos := session.position()
	if !hasPos {
		gatewayPositionSwitchSkippedTotal.WithLabelValues("no_position").Inc()
		return nil, false, nil
	}
	now := time.Now()
	if !session.markPositionResolveAttempt(now, px, pz, g.positionSwitchMinMoveMeters, g.positionSwitchMinInterval) {
		gatewayPositionSwitchSkippedTotal.WithLabelValues("throttled").Inc()
		return nil, false, nil
	}
	rctx, rspan := tr.Start(ctx, "Registry.ResolvePosition.ProactiveSwitch")
	res, err := reg.ResolvePosition(rctx, &cellv1.ResolvePositionRequest{X: px, Z: pz})
	rspan.End()
	if err != nil {
		return nil, false, err
	}
	if !res.GetFound() || res.GetCell() == nil || strings.TrimSpace(res.GetCell().GetGrpcEndpoint()) == "" {
		return nil, false, fmt.Errorf("proactive resolve switch target not found")
	}
	nextCell := res.GetCell()
	// #region agent log
	debugLogGateway("H1", "cmd/gateway/main.go:trySwitchDownstreamByPosition.resolve", "proactive resolve result", map[string]any{
		"playerId":       session.playerID,
		"currentCellId":  cur.cellID,
		"resolvedCellId": nextCell.GetId(),
		"x":              px,
		"z":              pz,
	})
	// #endregion
	if nextCell.GetId() == cur.cellID {
		fallbackDS, switched, ferr := g.tryAttachAlreadyJoinedFromCatalog(ctx, tr, reg, session.playerID, cur.cellID)
		// #region agent log
		debugLogGateway("H1", "cmd/gateway/main.go:trySwitchDownstreamByPosition.same_cell", "proactive resolve stayed on current cell", map[string]any{
			"playerId":         session.playerID,
			"currentCellId":    cur.cellID,
			"resolvedCellId":   nextCell.GetId(),
			"fallbackSwitched": switched,
			"fallbackCellId": func() string {
				if fallbackDS != nil {
					return fallbackDS.cellID
				}
				return ""
			}(),
			"fallbackErr": func() string {
				if ferr != nil {
					return ferr.Error()
				}
				return ""
			}(),
		})
		// #endregion
		if ferr != nil {
			return nil, false, ferr
		}
		if switched && fallbackDS != nil {
			return fallbackDS, true, nil
		}
		gatewayPositionSwitchSkippedTotal.WithLabelValues("same_cell").Inc()
		return nil, false, nil
	}
	gatewayResolvedCellMismatchTotal.WithLabelValues(metricCellLabel(cur.cellID), metricCellLabel(nextCell.GetId())).Inc()
	slog.InfoContext(ctx,
		"gateway_position_switch_candidate",
		"player_id", session.playerID,
		"from_cell_id", cur.cellID,
		"to_cell_id", nextCell.GetId(),
		"x", px,
		"z", pz,
	)
	// Игрок уже на целевой соте (например grid split): Join → already_joined, без Prepare на родителе.
	probe, joinMsg, probeErr := g.attachToCell(ctx, tr, session.playerID, nextCell.GetId(), nextCell.GetGrpcEndpoint())
	if probeErr == nil && joinMsg == "already_joined" {
		return probe, true, nil
	}
	if probeErr == nil && probe != nil {
		g.leaveDownstream(ctx, probe, session.playerID, "join_probe_not_existing", cur.cellID)
		g.closeDownstreamConn(probe, "join_probe_not_existing")
	}
	next, err := g.forwardPlayerHandoffSwitch(ctx, tr, reg, session.playerID, cur.cellID, nextCell)
	if err != nil {
		if isParentCellMissingErr(err) {
			recoveredDS, recovered, recoverErr := g.tryRecoverFromMissingParentSwitch(ctx, tr, session.playerID, cur.cellID, nextCell)
			if recoverErr == nil && recovered && recoveredDS != nil {
				return recoveredDS, true, nil
			}
		}
		fallbackDS, switched, ferr := g.tryAttachAlreadyJoinedFromCatalog(ctx, tr, reg, session.playerID, cur.cellID)
		if ferr == nil && switched && fallbackDS != nil {
			return fallbackDS, true, nil
		}
		return nil, false, err
	}
	return next, true, nil
}

func (g *gateway) forwardPlayerHandoffSwitch(
	ctx context.Context,
	tr trace.Tracer,
	reg cellv1.RegistryClient,
	playerID, fromCellID string,
	nextCell *cellv1.CellSpec,
) (*gatewayDownstream, error) {
	if nextCell == nil || strings.TrimSpace(nextCell.GetId()) == "" || strings.TrimSpace(nextCell.GetGrpcEndpoint()) == "" {
		return nil, fmt.Errorf("forward handoff switch: invalid target cell")
	}
	handoffToken := fmt.Sprintf("gw-pos-%s-%d", playerID, time.Now().UnixNano())
	hctx, hspan := tr.Start(ctx, "Registry.ForwardPlayerHandoff.Switch")
	resp, err := reg.ForwardPlayerHandoff(hctx, &cellv1.ForwardPlayerHandoffRequest{
		ParentCellId: fromCellID,
		ChildCellId:  nextCell.GetId(),
		PlayerId:     playerID,
		HandoffToken: handoffToken,
		Reason:       "gateway_position_switch",
	})
	hspan.End()
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.GetOk() || resp.GetChildEntityId() == 0 {
		return nil, fmt.Errorf("forward handoff switch failed: %+v", resp)
	}
	cellCC, err := grpc.NewClient(
		nextCell.GetGrpcEndpoint(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return nil, fmt.Errorf("cell dial %s: %w", nextCell.GetGrpcEndpoint(), err)
	}
	slog.InfoContext(
		ctx,
		"gateway_position_switch_handoff_ok",
		"player_id", playerID,
		"from_cell_id", fromCellID,
		"to_cell_id", nextCell.GetId(),
		"entity_id", resp.GetChildEntityId(),
	)
	return &gatewayDownstream{
		cellID:   nextCell.GetId(),
		endpoint: nextCell.GetGrpcEndpoint(),
		conn:     cellCC,
		client:   cellv1.NewCellClient(cellCC),
		entityID: resp.GetChildEntityId(),
	}, nil
}

func isParentCellMissingErr(err error) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok && st != nil {
		if st.Code() == codes.NotFound && strings.Contains(strings.ToLower(st.Message()), "cell not found") {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cell not found")
}

func (g *gateway) tryRecoverFromMissingParentSwitch(
	ctx context.Context,
	tr trace.Tracer,
	playerID string,
	fromCellID string,
	nextCell *cellv1.CellSpec,
) (*gatewayDownstream, bool, error) {
	if nextCell == nil || strings.TrimSpace(nextCell.GetId()) == "" || strings.TrimSpace(nextCell.GetGrpcEndpoint()) == "" {
		return nil, false, nil
	}
	recovered, joinMsg, err := g.attachToCell(ctx, tr, playerID, nextCell.GetId(), nextCell.GetGrpcEndpoint())
	if err != nil {
		return nil, false, err
	}
	if recovered == nil {
		return nil, false, nil
	}
	if joinMsg != "already_joined" && joinMsg != "spawned" {
		g.leaveDownstream(ctx, recovered, playerID, "join_probe_not_existing", fromCellID)
		g.closeDownstreamConn(recovered, "join_probe_not_existing")
		return nil, false, nil
	}
	slog.WarnContext(
		ctx,
		"gateway_position_switch_parent_missing_recover",
		"player_id", playerID,
		"from_cell_id", fromCellID,
		"to_cell_id", nextCell.GetId(),
		"join_msg", joinMsg,
	)
	// #region agent log
	debugLogGateway("H5", "cmd/gateway/main.go:tryRecoverFromMissingParentSwitch", "recovered switch after missing parent cell", map[string]any{
		"playerId":   playerID,
		"fromCellId": fromCellID,
		"toCellId":   nextCell.GetId(),
		"joinMsg":    joinMsg,
	})
	// #endregion
	return recovered, true, nil
}

func (g *gateway) applyDownstreamSwitch(
	ctx context.Context,
	session *gatewaySession,
	streamCancel context.CancelFunc,
	streamDone <-chan struct{},
	conn *websocket.Conn,
	old *gatewayDownstream,
	next *gatewayDownstream,
	playerID string,
	rx float64,
	rz float64,
	phase string,
) (context.Context, context.CancelFunc, <-chan struct{}) {
	fromCellID := ""
	if old != nil {
		fromCellID = old.cellID
	}
	toCellID := ""
	if next != nil {
		toCellID = next.cellID
	}
	streamCancel()
	<-streamDone
	recordAttachedCellDetach(fromCellID)
	g.leaveDownstream(ctx, old, playerID, phase, next.cellID)
	session.setDownstream(next)
	recordAttachedCellAttach(toCellID)
	recordCellTransition(fromCellID, toCellID, phase, "ok")
	nextStreamCtx, nextStreamCancel := context.WithCancel(ctx)
	nextStreamDone := g.streamDeltasToWS(nextStreamCtx, next, conn, session)
	g.closeDownstreamConn(old, phase)
	if g.db != nil {
		px, pz := session.positionOr(rx, rz)
		uctx, ucancel := context.WithTimeout(ctx, 2*time.Second)
		if uerr := db.UpsertPlayerLastCell(uctx, g.db, playerID, next.cellID, px, pz); uerr != nil {
			log.Printf("last_cell persist switch: %v", uerr)
		}
		ucancel()
	}
	return nextStreamCtx, nextStreamCancel, nextStreamDone
}

func (g *gateway) leaveDownstream(ctx context.Context, ds *gatewayDownstream, playerID, phase, nextCellID string) {
	if ds == nil {
		return
	}
	lctx, lcancel := context.WithTimeout(ctx, 3*time.Second)
	defer lcancel()
	resp, err := ds.client.Leave(lctx, &cellv1.LeaveRequest{PlayerId: playerID})
	result := "ok"
	switch {
	case err != nil:
		result = "rpc_error"
	case resp == nil:
		result = "empty_response"
	case !resp.GetOk():
		result = "not_ok"
	}
	gatewayCellLeaveTotal.WithLabelValues(phase, result).Inc()
	if result != "ok" {
		slog.WarnContext(ctx, "gateway_downstream_leave_failed",
			"phase", phase,
			"player_id", playerID,
			"cell_id", ds.cellID,
			"next_cell_id", nextCellID,
			"result", result,
			"err", err,
		)
	}
}

func (g *gateway) closeDownstreamConn(ds *gatewayDownstream, phase string) {
	if ds == nil || ds.conn == nil {
		return
	}
	if err := ds.conn.Close(); err != nil {
		gatewayDownstreamCloseTotal.WithLabelValues(phase, "error").Inc()
		slog.Warn("gateway_downstream_close_failed", "phase", phase, "cell_id", ds.cellID, "err", err)
		return
	}
	gatewayDownstreamCloseTotal.WithLabelValues(phase, "ok").Inc()
}

func shouldSwitchOnApplyInputMessage(msg string) bool {
	switch strings.TrimSpace(msg) {
	case "unknown_player", "player_handoff_frozen", "entity_gone":
		return true
	default:
		return false
	}
}

func shouldSwitchDownstreamOnTransport(err error) bool {
	if err == nil {
		return false
	}
	if status.Code(err) == codes.Unavailable {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "transport: error while dialing") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection error") ||
		strings.Contains(msg, "i/o timeout")
}

func envBoolWithDefault(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	return strings.EqualFold(raw, "1") ||
		strings.EqualFold(raw, "true") ||
		strings.EqualFold(raw, "yes")
}

func parseDurationWithDefault(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return fallback
	}
	return d
}

func parseFloatWithDefault(raw string, fallback float64) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func metricCellLabel(cellID string) string {
	cellID = strings.TrimSpace(cellID)
	if cellID == "" {
		return "unknown"
	}
	return cellID
}

func recordAttachedCellAttach(cellID string) {
	gatewayAttachedCellPlayers.WithLabelValues(metricCellLabel(cellID)).Inc()
}

func recordAttachedCellDetach(cellID string) {
	gatewayAttachedCellPlayers.WithLabelValues(metricCellLabel(cellID)).Dec()
}

func recordCellTransition(fromCellID, toCellID, phase, result string) {
	gatewayCellTransitionTotal.WithLabelValues(
		metricCellLabel(fromCellID),
		metricCellLabel(toCellID),
		strings.TrimSpace(phase),
		strings.TrimSpace(result),
	).Inc()
}

func debugLogGateway(hypothesisID, location, message string, data map[string]any) {
	entry := map[string]any{
		"id":           fmt.Sprintf("gw_%d", time.Now().UnixNano()),
		"runId":        "run1",
		"hypothesisId": hypothesisID,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().UnixMilli(),
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(debugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}
