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
	"strconv"
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
	sxStr := flag.String("session-x", "", "опционально: resolve_x для POST /v1/session (вместе с -session-z)")
	szStr := flag.String("session-z", "", "опционально: resolve_z для POST /v1/session")
	secSXStr := flag.String("second-session-x", "", "для -second-player: resolve_x (вместе с -second-session-z)")
	secSZStr := flag.String("second-session-z", "", "для -second-player: resolve_z")
	n := flag.Int("n", 5, "сколько кадров WorldChunk вывести и выйти")
	inputs := flag.Int("inputs", 0, "после первого snapshot отправить столько ClientInput (вперёд, mask=1)")
	displayName := flag.String("display-name", "", "опционально: display_name в POST /v1/session (пишется в mmo_player_profile при DATABASE_URL_RW)")
	questComplete := flag.Bool("quest-complete-tutorial", false, "после session: POST /v1/me/quest-progress tutorial_intro progress=3 (награды при наличии БД)")
	verbose := flag.Bool("verbose", false, "печать позиций из дельт")
	flag.Parse()

	base := strings.TrimSuffix(*gw, "/")
	rx, rz, use, err := parseCoordPair(*sxStr, *szStr, "-session-x/-session-z")
	if err != nil {
		log.Fatal(err)
	}
	var firstRx, firstRz *float64
	if use {
		firstRx, firstRz = &rx, &rz
	}

	dn := displayText(*displayName)
	if err := runOnce(base, *player, dn, *n, *inputs, *verbose, firstRx, firstRz, *questComplete); err != nil {
		log.Fatal(err)
	}
	sp := strings.TrimSpace(*second)
	if sp != "" {
		srx, srz, sUse, sErr := parseCoordPair(*secSXStr, *secSZStr, "-second-session-x/-second-session-z")
		if sErr != nil {
			log.Fatal(sErr)
		}
		var secRx, secRz *float64
		if sUse {
			secRx, secRz = &srx, &srz
		}
		fmt.Printf("--- second player %q ---\n", sp)
		if err := runOnce(base, sp, nil, *n, *inputs, *verbose, secRx, secRz, *questComplete); err != nil {
			log.Fatal(err)
		}
	}
}

func displayText(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func parseCoordPair(xStr, zStr, label string) (x, z float64, use bool, err error) {
	xStr = strings.TrimSpace(xStr)
	zStr = strings.TrimSpace(zStr)
	haveX := xStr != ""
	haveZ := zStr != ""
	if haveX != haveZ {
		return 0, 0, false, fmt.Errorf("%s: задайте оба поля или ни одного", label)
	}
	if !haveX {
		return 0, 0, false, nil
	}
	x, err1 := strconv.ParseFloat(xStr, 64)
	z, err2 := strconv.ParseFloat(zStr, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false, fmt.Errorf("%s: неверное число", label)
	}
	return x, z, true, nil
}

func runOnce(base, player string, displayName *string, n, inputs int, verbose bool, sessionRX, sessionRZ *float64, questCompleteTutorial bool) error {
	sess, err := sessionToken(base, player, displayName, sessionRX, sessionRZ)
	if err != nil {
		return err
	}
	token := sess.Token
	if sess.Stats != nil {
		fmt.Printf("[%s] session stats: level=%d xp=%d\n", player, sess.Stats.Level, sess.Stats.XP)
	}
	if sess.Wallet != nil {
		fmt.Printf("[%s] session wallet: gold=%d\n", player, sess.Wallet.Gold)
	}
	if len(sess.Inventory) > 0 {
		fmt.Printf("[%s] session inventory: %s\n", player, string(sess.Inventory))
	}
	if len(sess.Quests) > 0 {
		fmt.Printf("[%s] session quests: %+v\n", player, sess.Quests)
	}
	if len(sess.Items) > 0 {
		fmt.Printf("[%s] session items: %+v\n", player, sess.Items)
	}
	if questCompleteTutorial {
		if err := postQuestProgress(base, token, "tutorial_intro", 3); err != nil {
			return err
		}
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

// sessionInfo — поля /v1/session (при наличии БД у gateway).
type sessionInfo struct {
	Token string `json:"token"`
	Stats *struct {
		Level int   `json:"level"`
		XP    int64 `json:"xp"`
	} `json:"stats"`
	Wallet *struct {
		Gold int64 `json:"gold"`
	} `json:"wallet"`
	Inventory json.RawMessage `json:"inventory"`
	Quests    []struct {
		QuestID        string `json:"quest_id"`
		State          string `json:"state"`
		Progress       int    `json:"progress"`
		TargetProgress int    `json:"target_progress"`
	} `json:"quests"`
	Items []struct {
		ItemID      string `json:"item_id"`
		Quantity    int    `json:"quantity"`
		DisplayName string `json:"display_name"`
	} `json:"items"`
}

func postQuestProgress(base, bearerToken, questID string, progress int) error {
	body, err := json.Marshal(map[string]any{"quest_id": questID, "progress": progress})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/me/quest-progress", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("quest-progress: %s", resp.Status)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	fmt.Printf("[%s] quest-progress response: %+v\n", questID, out)
	return nil
}

func sessionToken(base, player string, displayName *string, resolveX, resolveZ *float64) (sessionInfo, error) {
	m := map[string]any{"player_id": player}
	if displayName != nil && *displayName != "" {
		m["display_name"] = *displayName
	}
	if resolveX != nil {
		m["resolve_x"] = *resolveX
		m["resolve_z"] = *resolveZ
	}
	body, err := json.Marshal(m)
	if err != nil {
		return sessionInfo{}, err
	}
	resp, err := http.Post(base+"/v1/session", "application/json", bytes.NewReader(body))
	if err != nil {
		return sessionInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return sessionInfo{}, fmt.Errorf("session: %s", resp.Status)
	}
	var out sessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return sessionInfo{}, err
	}
	if out.Token == "" {
		return sessionInfo{}, fmt.Errorf("session: empty token")
	}
	return out, nil
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
