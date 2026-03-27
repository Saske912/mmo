#!/usr/bin/env bash
# Проверка staging: поды в mmo, grid-manager list, ping cell через port-forward.
# Требуется kubectl и контекст на кластер. Namespace по умолчанию: mmo.
set -euo pipefail

NS="${K8S_NAMESPACE:-mmo}"
GM_SVC="${GRID_MANAGER_SVC:-mmo-grid-manager}"
CELL_SVC="${CELL_SVC:-mmo-cell}"
GM_PORT="${GRID_MANAGER_PORT:-9100}"
CELL_PORT="${CELL_GRPC_PORT:-50051}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need go

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
go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" list

echo "== mmoctl ping (cell localhost:${CELL_PORT}) =="
go run ./cmd/mmoctl ping "127.0.0.1:${CELL_PORT}"

echo "OK: registry видит соту, cell Ping отвечает."
