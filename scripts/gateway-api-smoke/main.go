// gateway-api-smoke: сессия, GET /v1/me/resolve-preview, опционально items/transfer (staging).
//
//	STAGING_VERIFY_TLS_INSECURE=1 — для самоподписанного Ingress.
//	go run ./scripts/gateway-api-smoke -gateway https://mmo.example.com
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func newHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if os.Getenv("STAGING_VERIFY_TLS_INSECURE") == "1" {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // только для смоука staging
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

func main() {
	gw := flag.String("gateway", "", "базовый URL gateway (https://...)")
	player := flag.String("player", "gw-api-smoke", "player_id")
	peer := flag.String("transfer-to", "", "если задан — пробуем POST items/transfer 1 coin_copper на этого игрока (нужны две сессии)")
	flag.Parse()
	base := strings.TrimSuffix(strings.TrimSpace(*gw), "/")
	if base == "" {
		fmt.Fprintln(os.Stderr, "need -gateway")
		os.Exit(2)
	}

	cli := newHTTPClient()

	token, err := postSession(cli, base, *player)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("session OK, token len=%d\n", len(token))

	if err := getResolvePreview(cli, base, token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("resolve-preview OK")

	if p := strings.TrimSpace(*peer); p != "" {
		if _, err := postSession(cli, base, p); err != nil {
			fmt.Fprintln(os.Stderr, "peer session:", err)
			os.Exit(1)
		}
		tok2, err := postSession(cli, base, *player)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := tryTransfer(cli, base, tok2, p); err != nil {
			fmt.Fprintf(os.Stderr, "transfer: %v\n", err)
		} else {
			fmt.Println("items/transfer OK (1 coin_copper)")
		}
	}
}

func authHeader(token string) string { return "Bearer " + token }

func postSession(cli *http.Client, base, player string) (string, error) {
	body, _ := json.Marshal(map[string]string{"player_id": player})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/session", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("session: %s %s", res.Status, string(b))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("session: empty token")
	}
	return out.Token, nil
}

func getResolvePreview(cli *http.Client, base, token string) error {
	req, err := http.NewRequest(http.MethodGet, base+"/v1/me/resolve-preview", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader(token))
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("resolve-preview: %s %s", res.Status, string(b))
	}
	var prev map[string]any
	if err := json.Unmarshal(b, &prev); err != nil {
		return err
	}
	fmt.Printf("resolve-preview: %+v\n", prev)
	return nil
}

func tryTransfer(cli *http.Client, base, fromToken, toPlayer string) error {
	body, _ := json.Marshal(map[string]any{
		"to_player_id": toPlayer,
		"item_id":      "coin_copper",
		"quantity":     1,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/me/items/transfer", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", authHeader(fromToken))
	req.Header.Set("Content-Type", "application/json")
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s", res.Status, string(b))
	}
	return nil
}
