package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
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
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	registry := flag.String("registry", "127.0.0.1:9100", "grid-manager Registry host:port")
	jwtSecret := flag.String("jwt-secret", "dev-insecure-change-me", "HMAC ключ для session JWT")
	resX := flag.Float64("resolve-x", 0, "координата для ResolvePosition (выбор соты)")
	resZ := flag.Float64("resolve-z", 0, "")
	flag.Parse()

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
		sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
		err = db.EnsureSchema(sctx, p)
		scancel()
		if err != nil {
			p.Close()
			log.Fatalf("database schema: %v", err)
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
	cancel()
	if err != nil {
		log.Printf("readyz: %v", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
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
		PlayerID string `json:"player_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.PlayerID) == "" {
		http.Error(w, `need {"player_id":"..."}`, http.StatusBadRequest)
		return
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   body.PlayerID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
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
		icancel()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": signed})
}

func (g *gateway) ws(w http.ResponseWriter, r *http.Request) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	tok, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (interface{}, error) {
		return g.jwtSecret, nil
	})
	if err != nil || !tok.Valid {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims, ok := tok.Claims.(*jwt.RegisteredClaims)
	if !ok || claims.Subject == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	playerID := claims.Subject

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	regCC, err := grpc.NewClient(g.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("registry dial: %v", err)
		http.Error(w, "registry unavailable", http.StatusBadGateway)
		return
	}
	defer regCC.Close()
	reg := cellv1.NewRegistryClient(regCC)
	res, err := reg.ResolvePosition(ctx, &cellv1.ResolvePositionRequest{X: g.resolveX, Z: g.resolveZ})
	if err != nil {
		log.Printf("resolve: %v", err)
		http.Error(w, "registry resolve failed", http.StatusBadGateway)
		return
	}
	if !res.Found || res.Cell == nil || res.Cell.GrpcEndpoint == "" {
		log.Printf("no cell for (%.f, %.f)", g.resolveX, g.resolveZ)
		http.Error(w, "no cell for resolve position", http.StatusServiceUnavailable)
		return
	}
	ep := res.Cell.GrpcEndpoint

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	inLim := rate.NewLimiter(rate.Limit(100), 50)

	cellCC, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("cell dial %s: %v", ep, err)
		return
	}
	defer cellCC.Close()
	cell := cellv1.NewCellClient(cellCC)

	jres, err := cell.Join(ctx, &cellv1.JoinRequest{PlayerId: playerID})
	if err != nil || jres == nil || !jres.Ok {
		log.Printf("join: %v res=%+v", err, jres)
		return
	}
	log.Printf("ws join player=%s entity_id=%d", playerID, jres.EntityId)
	gatewayWsSessions.Inc()

	defer func() {
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
		ares, aerr := cell.ApplyInput(aCtx, &cellv1.ApplyInputRequest{PlayerId: playerID, Input: &in})
		acancel()
		if aerr != nil || ares == nil || !ares.Ok {
			gatewayApplyInput.WithLabelValues("err").Inc()
			if aerr != nil {
				log.Printf("apply_input: %v", aerr)
			}
			continue
		}
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
