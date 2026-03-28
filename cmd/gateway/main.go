package main

import (
	"context"
	"encoding/json"
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
	"google.golang.org/grpc/credentials/insecure"
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
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

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
		registryAddr: *registry,
		jwtSecret:    jwtBytes,
		resolveX:     *resX,
		resolveZ:     *resZ,
		db:           pgPool,
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
	registryAddr string
	jwtSecret    []byte
	resolveX     float64
	resolveZ     float64
	db           *pgxpool.Pool
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

	out := map[string]any{"resolve_x": rx, "resolve_z": rz}
	if res != nil && res.Found && res.Cell != nil && res.Cell.GrpcEndpoint != "" {
		out["resolved"] = map[string]any{
			"found": true, "cell_id": res.Cell.GetId(), "grpc_endpoint": res.Cell.GrpcEndpoint,
		}
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
			if res != nil && res.Found && res.Cell != nil {
				resolvedID = res.Cell.GetId()
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
	ep := res.Cell.GrpcEndpoint
	cellID := res.Cell.GetId()
	slog.InfoContext(rctx, "registry_resolve_ok", "cell_id", cellID, "grpc_endpoint", ep)

	if g.db != nil {
		lctx, lcancel := context.WithTimeout(ctx, 2*time.Second)
		rec, rerr := db.GetPlayerLastCellRecord(lctx, g.db, playerID)
		lcancel()
		if rerr != nil {
			log.Printf("ws handoff check: %v", rerr)
		} else if rec != nil && cellID != "" && rec.CellID != "" && rec.CellID != cellID {
			gatewayCellHandoffMismatch.Inc()
			w.Header().Set("X-MMO-Last-Cell-Id", rec.CellID)
			w.Header().Set("X-MMO-Resolved-Cell-Id", cellID)
			http.Error(w, "cell handoff required: call POST /v1/session with updated resolve_x/resolve_z (or GET /v1/me/resolve-preview), then reconnect WebSocket", http.StatusConflict)
			return
		}
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	inLim := rate.NewLimiter(rate.Limit(100), 50)

	cellCC, err := grpc.NewClient(ep,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		log.Printf("cell dial %s: %v", ep, err)
		return
	}
	defer cellCC.Close()
	cell := cellv1.NewCellClient(cellCC)

	jctx, jspan := tr.Start(ctx, "Cell.Join")
	defer jspan.End()
	joinStart := time.Now()
	jres, err := cell.Join(jctx, &cellv1.JoinRequest{PlayerId: playerID})
	joinResult := "ok"
	if err != nil || jres == nil || !jres.Ok {
		joinResult = "err"
	}
	gatewayCellJoinDuration.WithLabelValues(joinResult).Observe(time.Since(joinStart).Seconds())
	if err != nil || jres == nil || !jres.Ok {
		log.Printf("join: %v res=%+v", err, jres)
		return
	}
	slog.InfoContext(jctx, "cell_join_ok", "player_id", playerID, "entity_id", jres.EntityId)
	log.Printf("ws join player=%s entity_id=%d", playerID, jres.EntityId)
	gatewayWsSessions.Inc()

	defer func() {
		if g.db != nil && cellID != "" {
			uctx, ucancel := context.WithTimeout(context.Background(), 2*time.Second)
			if uerr := db.UpsertPlayerLastCell(uctx, g.db, playerID, cellID, rx, rz); uerr != nil {
				log.Printf("last_cell persist: %v", uerr)
			}
			ucancel()
		}
		lctx, lcancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer lcancel()
		if _, lerr := cell.Leave(lctx, &cellv1.LeaveRequest{PlayerId: playerID}); lerr != nil {
			log.Printf("leave: %v", lerr)
		}
	}()

	sub, err := cell.SubscribeDeltas(ctx, &cellv1.SubscribeDeltasRequest{})
	if err != nil {
		log.Printf("subscribe: %v", err)
		return
	}

	go streamDeltasToWS(sub, conn)

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
		aCtx, acancel := context.WithTimeout(ctx, 2*time.Second)
		applyStart := time.Now()
		ares, aerr := cell.ApplyInput(aCtx, &cellv1.ApplyInputRequest{PlayerId: playerID, Input: &in})
		applyDur := time.Since(applyStart).Seconds()
		acancel()
		if aerr != nil || ares == nil || !ares.Ok {
			gatewayCellApplyInputDuration.WithLabelValues("err").Observe(applyDur)
			gatewayApplyInput.WithLabelValues("err").Inc()
			if aerr != nil {
				log.Printf("apply_input: %v", aerr)
			}
			continue
		}
		gatewayCellApplyInputDuration.WithLabelValues("ok").Observe(applyDur)
		gatewayApplyInput.WithLabelValues("ok").Inc()
	}
}

func streamDeltasToWS(sub cellv1.Cell_SubscribeDeltasClient, conn *websocket.Conn) {
	defer conn.Close()
	for {
		chunk, err := sub.Recv()
		if err != nil {
			log.Printf("SubscribeDeltas recv: %v", err)
			return
		}
		b, err := proto.Marshal(chunk)
		if err != nil {
			continue
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
			return
		}
	}
}
