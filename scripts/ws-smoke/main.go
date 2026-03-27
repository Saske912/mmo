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
	gamev1 "mmo/gen/gamev1"
)

func main() {
	gw := flag.String("gateway", "http://127.0.0.1:8080", "базовый URL gateway (http)")
	player := flag.String("player", "ws-smoke", "player_id для Join")
	second := flag.String("second-player", "", "если непусто — после первого прогона второй session+ws с этим player_id")
	n := flag.Int("n", 5, "сколько кадров WorldChunk вывести и выйти")
	inputs := flag.Int("inputs", 0, "после первого snapshot отправить столько ClientInput (вперёд, mask=1)")
	verbose := flag.Bool("verbose", false, "печать позиций из дельт")
	flag.Parse()

	base := strings.TrimSuffix(*gw, "/")
	if err := runOnce(base, *player, *n, *inputs, *verbose); err != nil {
		log.Fatal(err)
	}
	sp := strings.TrimSpace(*second)
	if sp != "" {
		fmt.Printf("--- second player %q ---\n", sp)
		if err := runOnce(base, sp, *n, *inputs, *verbose); err != nil {
			log.Fatal(err)
		}
	}
}

func runOnce(base, player string, n, inputs int, verbose bool) error {
	token, err := sessionToken(base, player)
	if err != nil {
		return err
	}

	wsURL, err := wsDialURL(base, token)
	if err != nil {
		return err
	}

	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := d.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	sentMove := false
	for i := range n {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read %d: %w", i, err)
		}
		var chunk cellv1.WorldChunk
		if err := proto.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("unmarshal %d: %w", i, err)
		}
		if s := chunk.GetSnapshot(); s != nil {
			fmt.Printf("[%s %d] snapshot tick=%d entities=%d\n", player, i, s.Tick, len(s.Entities))
			if !sentMove && inputs > 0 {
				for j := range inputs {
					in := &gamev1.ClientInput{Seq: uint32(j + 1), InputMask: 1}
					b, mErr := proto.Marshal(in)
					if mErr != nil {
						return mErr
					}
					if wErr := conn.WriteMessage(websocket.BinaryMessage, b); wErr != nil {
						return fmt.Errorf("write input: %w", wErr)
					}
				}
				sentMove = true
				fmt.Printf("[%s] sent %d ClientInput (forward)\n", player, inputs)
			}
			continue
		}
		if d := chunk.GetDelta(); d != nil {
			fmt.Printf("[%s %d] delta tick=%d changed=%d\n", player, i, d.Tick, len(d.Changed))
			if verbose {
				for _, e := range d.Changed {
					if e.Position != nil {
						fmt.Printf("    entity=%d pos=(%.2f,%.2f,%.2f)\n", e.EntityId, e.Position.X, e.Position.Y, e.Position.Z)
					}
				}
			}
			continue
		}
		fmt.Printf("[%s %d] empty chunk\n", player, i)
	}
	return nil
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
