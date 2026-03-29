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
CELL_CONTROLLER_DEPLOY="${CELL_CONTROLLER_DEPLOY:-cell-controller}"
RESET_AUTO_CHILDREN_BEFORE_TEST="${RESET_AUTO_CHILDREN_BEFORE_TEST:-1}"
PF_PID=""

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need curl
need grep

cleanup() {
  if [[ -n "${PF_PID:-}" ]] && kill -0 "$PF_PID" 2>/dev/null; then
    kill "$PF_PID" 2>/dev/null || true
  fi
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

cleanup_auto_children() {
  echo "== cleanup auto child-cell workloads before test =="
  local auto_deploys auto_svcs
  auto_deploys="$(kubectl -n "$NS" get deploy -o name | rg '^deployment.apps/cell-node-auto-' || true)"
  auto_svcs="$(kubectl -n "$NS" get svc -o name | rg '^service/mmo-cell-auto-' || true)"
  if [[ -n "$auto_deploys" ]]; then
    while IFS= read -r d; do
      [[ -z "$d" ]] && continue
      kubectl -n "$NS" delete "$d" --ignore-not-found >/dev/null || true
    done <<< "$auto_deploys"
  fi
  if [[ -n "$auto_svcs" ]]; then
    while IFS= read -r s; do
      [[ -z "$s" ]] && continue
      kubectl -n "$NS" delete "$s" --ignore-not-found >/dev/null || true
    done <<< "$auto_svcs"
  fi
  # ждём, пока останется только primary pod cell-node (или текущий минимум)
  kubectl -n "$NS" wait --for=delete pod -l app=cell-node,cell_shard!=primary --timeout=90s >/dev/null 2>&1 || true
}

if [[ "$RESET_AUTO_CHILDREN_BEFORE_TEST" == "1" || "$RESET_AUTO_CHILDREN_BEFORE_TEST" == "true" ]]; then
  cleanup_auto_children
fi

echo "== baseline cells =="
BASE_LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list)"
BASE_COUNT="$(printf '%s\n' "$BASE_LIST" | awk 'NF>0{n++} END{print n+0}')"
echo "baseline count=${BASE_COUNT}"

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

echo "== verify child cells appeared in catalog =="
NOW_LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list)"
NOW_COUNT="$(printf '%s\n' "$NOW_LIST" | awk 'NF>0{n++} END{print n+0}')"
echo "current count=${NOW_COUNT}"
if [ "$NOW_COUNT" -le "$BASE_COUNT" ]; then
  echo "ERROR: child cells were not materialized (count did not grow)" >&2
  echo "$NOW_LIST"
  exit 1
fi

echo "== verify retire_ready signal in controller logs =="
# В режиме set -o pipefail + grep -q kubectl logs может завершаться SIGPIPE (ложный fail).
# Читаем весь поток и проверяем обычным grep без раннего выхода.
if ! kubectl -n "$NS" logs "deploy/${CELL_CONTROLLER_DEPLOY}" --since=15m 2>/dev/null | grep 'retire_ready_set' >/dev/null; then
  echo "ERROR: no retire_ready_set signal in cell-controller logs" >&2
  exit 1
fi

echo "OK: auto split e2e smoke finished"
