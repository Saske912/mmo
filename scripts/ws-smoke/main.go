// Пример клиента Phase 0.3: сессия → WebSocket → бинарные WorldChunk (protobuf).
// Требуются запущенные registry, cell-node и gateway; адрес соты должен быть доступен gateway.
//
//	go run ./scripts/ws-smoke -gateway http://127.0.0.1:8080
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	cellv1 "mmo/gen/cellv1"
)

func main() {
	gw := flag.String("gateway", "http://127.0.0.1:8080", "базовый URL gateway (http)")
	player := flag.String("player", "ws-smoke", "player_id для Join")
	n := flag.Int("n", 5, "сколько кадров WorldChunk вывести и выйти")
	flag.Parse()

	base := strings.TrimSuffix(*gw, "/")
	token, err := sessionToken(base, *player)
	if err != nil {
		log.Fatal(err)
	}

	wsURL, err := wsDialURL(base, token)
	if err != nil {
		log.Fatal(err)
	}

	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := d.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("ws dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	for i := range *n {
		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Fatalf("read %d: %v", i, err)
		}
		var chunk cellv1.WorldChunk
		if err := proto.Unmarshal(data, &chunk); err != nil {
			log.Fatalf("unmarshal %d: %v", i, err)
		}
		if s := chunk.GetSnapshot(); s != nil {
			fmt.Printf("[%d] snapshot tick=%d entities=%d\n", i, s.Tick, len(s.Entities))
			continue
		}
		if d := chunk.GetDelta(); d != nil {
			fmt.Printf("[%d] delta tick=%d changed=%d\n", i, d.Tick, len(d.Changed))
			continue
		}
		fmt.Printf("[%d] empty chunk\n", i)
	}
}

func sessionToken(base, player string) (string, error) {
	body, err := json.Marshal(map[string]string{"player_id": player})
	if err != nil {
		return "", err
	}
	resp, err := http.Post(base+"/v1/session", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session: %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("session: empty token")
	}
	return out.Token, nil
}

func wsDialURL(httpBase, token string) (string, error) {
	u, err := url.Parse(httpBase)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	u.Path = "/v1/ws"
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
