// gateway-session-burst: параллельные POST /v1/session для лёгкой проверки gateway под нагрузкой.
//
//	GATEWAY_PUBLIC_URL / -gateway — базовый URL (https://...).
//	STAGING_VERIFY_TLS_INSECURE=1 — самоподписанный Ingress.
//
//	go run ./scripts/gateway-session-burst -gateway https://mmo.example.com -n 100 -j 20
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
	"sync"
	"sync/atomic"
	"time"
)

func newHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if os.Getenv("STAGING_VERIFY_TLS_INSECURE") == "1" {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // staging smoke only
	}
	return &http.Client{Timeout: 45 * time.Second, Transport: tr}
}

func postSession(cli *http.Client, base, player string) error {
	body, _ := json.Marshal(map[string]string{"player_id": player})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/session", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", res.Status, string(b))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	if out.Token == "" {
		return fmt.Errorf("empty token")
	}
	return nil
}

func main() {
	gw := flag.String("gateway", "", "базовый URL gateway (или GATEWAY_PUBLIC_URL)")
	n := flag.Int("n", 50, "число сессий")
	j := flag.Int("j", 10, "параллельных воркеров")
	prefix := flag.String("player-prefix", "burst", "префикс player_id (добавляется индекс)")
	flag.Parse()

	base := strings.TrimSuffix(strings.TrimSpace(*gw), "/")
	if base == "" {
		base = strings.TrimSuffix(strings.TrimSpace(os.Getenv("GATEWAY_PUBLIC_URL")), "/")
	}
	if base == "" {
		fmt.Fprintln(os.Stderr, "need -gateway or GATEWAY_PUBLIC_URL")
		os.Exit(2)
	}

	cli := newHTTPClient()
	var ok, fail int32
	var wg sync.WaitGroup
	tasks := make(chan int, *j)
	start := time.Now()

	for w := 0; w < *j; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range tasks {
				player := fmt.Sprintf("%s-%d-%d", *prefix, time.Now().UnixNano(), i)
				if err := postSession(cli, base, player); err != nil {
					atomic.AddInt32(&fail, 1)
					fmt.Fprintf(os.Stderr, "session %d: %v\n", i, err)
				} else {
					atomic.AddInt32(&ok, 1)
				}
			}
		}()
	}
	for i := 0; i < *n; i++ {
		tasks <- i
	}
	close(tasks)
	wg.Wait()

	elapsed := time.Since(start).Seconds()
	fmt.Printf("gateway-session-burst: ok=%d fail=%d in %.2fs (%.1f req/s)\n",
		ok, fail, elapsed, float64(*n)/elapsed)
	if fail > 0 {
		os.Exit(1)
	}
}
