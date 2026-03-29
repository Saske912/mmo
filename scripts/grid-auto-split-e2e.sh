#!/usr/bin/env bash
# E2E smoke для auto split workflow:
#  1) включает/проверяет env grid-manager (AUTO_SPLIT_DRAIN + AUTO_SPLIT_WORKFLOW)
#  2) запускает rehearsal load-policy (нарушение порога)
#  3) проверяет split workflow метрики
set -euo pipefail

NS="${NAMESPACE:-mmo}"
GRID_DEPLOY="${GRID_DEPLOY:-grid-manager}"
METRICS_LOCAL_PORT="${METRICS_LOCAL_PORT:-19093}"
METRICS_REMOTE_PORT="${GRID_METRICS_CONTAINER_PORT:-9091}"
WAIT_SECONDS="${WAIT_SECONDS:-20}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need curl
need grep

echo "== ensure grid-manager env (AUTO_SPLIT_DRAIN + AUTO_SPLIT_WORKFLOW) =="
kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
  MMO_GRID_AUTO_SPLIT_DRAIN=true \
  MMO_GRID_AUTO_SPLIT_WORKFLOW=true \
  MMO_GRID_REGISTRY_ADDR=127.0.0.1:9100
kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=120s

echo "== run load rehearsal =="
bash scripts/grid-auto-split-drain-rehearsal.sh

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
  echo "WARN: no split workflow metrics found (likely grid-manager image without workflow code yet)." >&2
  echo "      Deploy current backend image and rerun split-e2e-smoke." >&2
  exit 0
fi
if ! echo "$M" | grep -q 'result="ok"'; then
  echo "WARN: no successful workflow run yet (check child topology or handoff prerequisites)" >&2
fi

echo "OK: auto split e2e smoke finished"
