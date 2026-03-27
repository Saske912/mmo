#!/usr/bin/env bash
# Проверка staging: поды, grid-manager + cell через port-forward, gateway через Ingress (HTTPS).
# Требуется kubectl. Переопределить URL: GATEWAY_PUBLIC_URL=https://другой.host
# Если TLS ещё не доверен: STAGING_VERIFY_TLS_INSECURE=1
set -euo pipefail

NS="${K8S_NAMESPACE:-mmo}"
GM_SVC="${GRID_MANAGER_SVC:-mmo-grid-manager}"
CELL_SVC="${CELL_SVC:-mmo-cell}"
GM_PORT="${GRID_MANAGER_PORT:-9100}"
CELL_PORT="${CELL_GRPC_PORT:-50051}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGING_DIR="${ROOT}/deploy/terraform/staging"
cd "$ROOT"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need go

resolve_gateway_public() {
  if [ -n "${GATEWAY_PUBLIC_URL:-}" ]; then
    printf '%s' "${GATEWAY_PUBLIC_URL%/}"
    return
  fi
  if [ -d "$STAGING_DIR" ] && command -v tofu >/dev/null 2>&1; then
    local raw
    raw="$(cd "$STAGING_DIR" && tofu output -raw gateway_public_url 2>/dev/null || true)"
    if [ -n "$raw" ] && [ "$raw" != "null" ]; then
      printf '%s' "${raw%/}"
      return
    fi
  fi
  printf '%s' "https://mmo.pass-k8s.ru"
}

GATEWAY_PUBLIC="$(resolve_gateway_public)"

curl_public() {
  if [ "${STAGING_VERIFY_TLS_INSECURE:-0}" = 1 ]; then
    curl -fsSk "$@"
  else
    curl -fsS "$@"
  fi
}

echo "== kubectl -n $NS pods =="
kubectl get pods -n "$NS" -o wide

P1=""; P2=""
cleanup() { kill ${P1:-} ${P2:-} 2>/dev/null || true; }
trap cleanup EXIT

kubectl port-forward -n "$NS" "svc/$GM_SVC" "${GM_PORT}:${GM_PORT}" >/dev/null 2>&1 &
P1=$!
kubectl port-forward -n "$NS" "svc/$CELL_SVC" "${CELL_PORT}:${CELL_PORT}" >/dev/null 2>&1 &
P2=$!
sleep 2

echo "== mmoctl list (registry localhost:${GM_PORT}) =="
FIRST_CELL="$(go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" list | head -1 | awk '{print $1}')"
if [ -z "${FIRST_CELL:-}" ]; then
  echo "no cells in registry" >&2
  exit 1
fi
go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" list

echo "== mmoctl forward-update noop (registry -> cell, id=${FIRST_CELL}) =="
go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" forward-update "$FIRST_CELL" noop

echo "== mmoctl resolve (-500,-500) — при дочерней соте в каталоге выигрывает больший level =="
go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" resolve -500 -500

echo "== mmoctl ping (cell localhost:${CELL_PORT}) =="
go run ./cmd/mmoctl ping "127.0.0.1:${CELL_PORT}"

echo "== gateway /healthz (Ingress ${GATEWAY_PUBLIC}) =="
if ! curl_public "${GATEWAY_PUBLIC}/healthz" | grep -q ok; then
  echo "gateway healthz failed (${GATEWAY_PUBLIC})" >&2
  exit 1
fi

echo "== ws-smoke (Ingress ${GATEWAY_PUBLIC}, первые кадры) =="
go run ./scripts/ws-smoke -gateway "${GATEWAY_PUBLIC}" -n 3

echo "OK: registry, cell, gateway через Ingress (healthz + ws-smoke) прошли."
