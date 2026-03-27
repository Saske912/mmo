#!/usr/bin/env bash
# Инфра-smoke: Consul + NATS + grid-manager (consul) + cell-node + mmoctl list + NATS round-trip.
# Требуется запущенный Consul (CONSUL_HTTP_ADDR) и NATS (NATS_URL).
# Пример:
#   docker run --rm -d --name consul-dev -p 8500:8500 hashicorp/consul:1.20 agent -dev -client=0.0.0.0
#   docker run --rm -d --name nats-dev  -p 4222:4222 nats:2.12-alpine

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

RAW_CONSUL="${CONSUL_HTTP_ADDR:-127.0.0.1:8500}"
export NATS_URL="${NATS_URL:-nats://127.0.0.1:4222}"
export CONSUL_HTTP_ADDR="$RAW_CONSUL"

if [[ "$RAW_CONSUL" != http://* && "$RAW_CONSUL" != https://* ]]; then
  CURL_BASE="http://${RAW_CONSUL}"
else
  CURL_BASE="$RAW_CONSUL"
fi

echo "waiting for Consul at $CURL_BASE ..."
for _ in $(seq 1 40); do
  if curl -sf "$CURL_BASE/v1/status/leader" | grep -qE .; then
    break
  fi
  sleep 0.25
done
if ! curl -sf "$CURL_BASE/v1/status/leader" | grep -qE .; then
  echo "Consul unavailable" >&2
  exit 1
fi

tcp_open() {
  local host="$1" port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z "$host" "$port"
    return
  fi
  timeout 1 bash -c "echo >/dev/tcp/$host/$port" 2>/dev/null
}

echo "waiting for NATS (4222) ..."
make -s build
for _ in $(seq 1 40); do
  if tcp_open 127.0.0.1 4222; then
    break
  fi
  sleep 0.25
done
if ! tcp_open 127.0.0.1 4222; then
  echo "NATS not reachable on 127.0.0.1:4222 (set NATS_URL and open port)" >&2
  exit 1
fi

./bin/grid-manager -listen 127.0.0.1:9100 -backend consul &
GM_PID=$!
sleep 0.4

./bin/cell-node -id infra_smoke_cell -listen 127.0.0.1:0 -consul-addr "$RAW_CONSUL" &
CN_PID=$!
sleep 2

echo "== mmoctl list =="
./bin/mmoctl -registry 127.0.0.1:9100 list

echo "== mmoctl infra-print (sample) =="
./bin/mmoctl infra-print | head -n 4

echo "== NATS pub/sub (cell.events) =="
MSG="infra-smoke-$(date +%s)"
OUT="$(mktemp)"
./bin/mmoctl nats sub -wait 1 -timeout 15s cell.events >"$OUT" &
SUB_PID=$!
sleep 0.4
./bin/mmoctl nats pub cell.events "$MSG"
wait "$SUB_PID"
if ! grep -qF "$MSG" "$OUT"; then
  echo "NATS round-trip failed" >&2
  rm -f "$OUT"
  kill -TERM "$CN_PID" 2>/dev/null || true
  wait "$CN_PID" 2>/dev/null || true
  kill -TERM "$GM_PID" 2>/dev/null || true
  wait "$GM_PID" 2>/dev/null || true
  exit 1
fi
rm -f "$OUT"

kill -TERM "$CN_PID" 2>/dev/null || true
wait "$CN_PID" 2>/dev/null || true
kill -TERM "$GM_PID" 2>/dev/null || true
wait "$GM_PID" 2>/dev/null || true

echo "infra-smoke: ok"
