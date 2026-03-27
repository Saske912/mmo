#!/usr/bin/env bash
# Smoke-тест чеклиста 0.1: Consul + grid-manager (consul) + cell-node + mmoctl.
# Требуется запущенный Consul на CONSUL_HTTP_ADDR (по умолчанию 127.0.0.1:8500).
# Пример: docker run --rm -d --name consul-dev -p 8500:8500 hashicorp/consul:1.20 agent -dev -client=0.0.0.0

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

RAW_ADDR="${CONSUL_HTTP_ADDR:-127.0.0.1:8500}"
# curl needs URL
if [[ "$RAW_ADDR" != http://* && "$RAW_ADDR" != https://* ]]; then
  CURL_BASE="http://${RAW_ADDR}"
else
  CURL_BASE="$RAW_ADDR"
fi

echo "waiting for Consul at $CURL_BASE ..."
for _ in $(seq 1 40); do
  if curl -sf "$CURL_BASE/v1/status/leader" | grep -qE .; then
    break
  fi
  sleep 0.25
done
if ! curl -sf "$CURL_BASE/v1/status/leader" | grep -qE .; then
  echo "Consul unavailable; start it or set CONSUL_HTTP_ADDR" >&2
  exit 1
fi

make -s build

export CONSUL_HTTP_ADDR="$RAW_ADDR"

./bin/grid-manager -listen 127.0.0.1:9100 -backend consul &
GM_PID=$!
sleep 0.4

./bin/cell-node -id smoke_cell -listen 127.0.0.1:0 -consul-addr "$RAW_ADDR" &
CN_PID=$!
sleep 2

echo "== mmoctl list =="
./bin/mmoctl -registry 127.0.0.1:9100 list

echo "== catalog service mmo-cell (HTTP) =="
curl -sf "$CURL_BASE/v1/catalog/service/mmo-cell" | head -c 400 || true
echo

kill -TERM "$CN_PID" 2>/dev/null || true
wait "$CN_PID" 2>/dev/null || true
kill -TERM "$GM_PID" 2>/dev/null || true
wait "$GM_PID" 2>/dev/null || true

echo "consul-smoke: ok"
