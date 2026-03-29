#!/usr/bin/env bash
# E2E smoke для auto split workflow:
#  1) включает env grid-manager (AUTO_SPLIT_DRAIN + AUTO_SPLIT_WORKFLOW) + тестовые пороги
#  2) ждёт срабатывания load policy и split workflow
#  3) проверяет runs_total{result="ok"} в /metrics
#  4) cleanup: снимает тестовые пороги и split_drain=false на всех сотах
set -euo pipefail

NS="${NAMESPACE:-mmo}"
GRID_DEPLOY="${GRID_DEPLOY:-grid-manager}"
METRICS_LOCAL_PORT="${METRICS_LOCAL_PORT:-19093}"
METRICS_REMOTE_PORT="${GRID_METRICS_CONTAINER_PORT:-9091}"
WAIT_SECONDS="${WAIT_SECONDS:-70}"
REGISTRY_PORT="${REGISTRY_PORT:-9100}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need curl
need grep

cleanup() {
  echo "== cleanup grid-manager thresholds + split-drain false =="
  kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
    MMO_GRID_THRESHOLD_MAX_TICK_SECONDS- \
    MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION- \
    MMO_GRID_LOAD_POLICY_COOLDOWN- \
    MMO_GRID_CELL_PROBE_INTERVAL- >/dev/null
  kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=120s >/dev/null || true
  LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list 2>/dev/null || true)"
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    cid="$(echo "$line" | awk '{print $1}')"
    [ -z "$cid" ] && continue
    kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" forward-update "$cid" split-drain false >/dev/null 2>&1 || true
  done <<< "$LIST"
}
trap cleanup EXIT

echo "== ensure grid-manager env + trigger thresholds =="
kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
  MMO_GRID_AUTO_SPLIT_DRAIN=true \
  MMO_GRID_AUTO_SPLIT_WORKFLOW=true \
  MMO_GRID_REGISTRY_ADDR=127.0.0.1:9100 \
  MMO_GRID_THRESHOLD_MAX_TICK_SECONDS=0.000001 \
  MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION=8s \
  MMO_GRID_LOAD_POLICY_COOLDOWN=60s \
  MMO_GRID_CELL_PROBE_INTERVAL=6s
kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=120s

echo "== port-forward metrics =="
kubectl -n "$NS" port-forward "deployment/$GRID_DEPLOY" "${METRICS_LOCAL_PORT}:${METRICS_REMOTE_PORT}" >/tmp/grid-auto-split-e2e.pf.log 2>&1 &
PF_PID=$!
cleanup() {
  if kill -0 "$PF_PID" 2>/dev/null; then
    kill "$PF_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT
sleep 2

echo "== wait ${WAIT_SECONDS}s for workflow =="
sleep "$WAIT_SECONDS"
M="$(curl -sf "http://127.0.0.1:${METRICS_LOCAL_PORT}/metrics")"

echo "$M" | grep -E 'mmo_grid_manager_split_workflow_runs_total|mmo_grid_manager_split_workflow_duration_seconds' || true

if ! echo "$M" | grep -q 'mmo_grid_manager_split_workflow_runs_total'; then
  echo "ERROR: no split workflow counter metric found" >&2
  exit 1
fi
if ! echo "$M" | grep -q 'mmo_grid_manager_split_workflow_runs_total{result="ok"}'; then
  echo "ERROR: no successful workflow run result=\"ok\"" >&2
  exit 1
fi

echo "OK: auto split e2e smoke finished"
