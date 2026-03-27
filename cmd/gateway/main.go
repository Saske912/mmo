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
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	cellv1 "mmo/gen/cellv1"
	gamev1 "mmo/gen/gamev1"
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
	cellGRPC := flag.String("cell-grpc", "", "если задан host:port — подключаться к соте напрямую, без ResolvePosition (K8s single-cell)")
	flag.Parse()

	jwtBytes := []byte(*jwtSecret)
	if v := strings.TrimSpace(os.Getenv("GATEWAY_JWT_SECRET")); v != "" {
		jwtBytes = []byte(v)
	}
	cellEp := strings.TrimSpace(*cellGRPC)
	if v := strings.TrimSpace(os.Getenv("GATEWAY_CELL_GRPC")); v != "" {
		cellEp = v
	}

	g := &gateway{
		registryAddr: *registry,
		jwtSecret:    jwtBytes,
		resolveX:     *resX,
		resolveZ:     *resZ,
		cellGRPC:     cellEp,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/session", g.session)
	mux.HandleFunc("/v1/ws", g.ws)

	if cellEp != "" {
		log.Printf("gateway http://%s cell=%s (direct)", *listen, cellEp)
	} else {
		log.Printf("gateway http://%s registry=%s resolve=(%.1f,%.1f)", *listen, *registry, *resX, *resZ)
	}
	log.Fatal(http.ListenAndServe(*listen, mux))
}

type gateway struct {
	registryAddr string
	jwtSecret    []byte
	resolveX     float64
	resolveZ     float64
	cellGRPC     string
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

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	inLim := rate.NewLimiter(rate.Limit(100), 50)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	ep := g.cellGRPC
	if ep == "" {
		regCC, err := grpc.NewClient(g.registryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Printf("registry dial: %v", err)
			return
		}
		defer regCC.Close()
		reg := cellv1.NewRegistryClient(regCC)
		res, err := reg.ResolvePosition(ctx, &cellv1.ResolvePositionRequest{X: g.resolveX, Z: g.resolveZ})
		if err != nil {
			log.Printf("resolve: %v", err)
			return
		}
		if !res.Found || res.Cell == nil || res.Cell.GrpcEndpoint == "" {
			log.Printf("no cell for (%.f, %.f)", g.resolveX, g.resolveZ)
			return
		}
		ep = res.Cell.GrpcEndpoint
	}

	cellCC, err := grpc.NewClient(ep, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("cell dial %s: %v", ep, err)
		return
	}
	defer cellCC.Close()
	cell := cellv1.NewCellClient(cellCC)

	if _, err := cell.Join(ctx, &cellv1.JoinRequest{PlayerId: playerID}); err != nil {
		log.Printf("join: %v", err)
		return
	}

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
		_ = in.Seq
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
