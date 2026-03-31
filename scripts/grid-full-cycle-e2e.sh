#!/usr/bin/env bash
# E2E smoke полного auto-cycle:
#  1) включает auto split + auto merge policy/workflow
#  2) форсирует split через жёсткий tick-threshold
#  3) переключает пороги на low-load и ждёт auto merge
#  4) проверяет split/merge метрики и Redis state markers
set -euo pipefail

NS="${NAMESPACE:-mmo}"
GRID_DEPLOY="${GRID_DEPLOY:-grid-manager}"
REGISTRY_PORT="${REGISTRY_PORT:-9100}"
METRICS_LOCAL_PORT="${METRICS_LOCAL_PORT:-19097}"
METRICS_REMOTE_PORT="${GRID_METRICS_CONTAINER_PORT:-9091}"
RESTORE_REGISTRY_ADDR="${MMO_GRID_REGISTRY_ADDR_RESTORE:-mmo-grid-manager.${NS}.svc.cluster.local:9100}"
SPLIT_WAIT_SECONDS="${SPLIT_WAIT_SECONDS:-90}"
MERGE_WAIT_SECONDS="${MERGE_WAIT_SECONDS:-120}"
PARENT_CELL="${GRID_FULL_CYCLE_PARENT_CELL_ID:-cell_root}"
CELL_CONTROLLER_DEPLOY="${CELL_CONTROLLER_DEPLOY:-cell-controller}"
PF_PID=""
ORIG_SPLIT_MAX_LEVEL=""

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need curl
need awk
need grep

cleanup() {
  if [[ -n "${PF_PID:-}" ]] && kill -0 "$PF_PID" 2>/dev/null; then
    kill "$PF_PID" 2>/dev/null || true
  fi
  echo "== cleanup: unset temporary grid-manager env =="
  kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
    MMO_GRID_THRESHOLD_MAX_TICK_SECONDS- \
    MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION- \
    MMO_GRID_LOAD_POLICY_COOLDOWN- \
    MMO_GRID_CELL_PROBE_INTERVAL- \
    MMO_GRID_MERGE_MIN_LOW_LOAD_DURATION- \
    MMO_GRID_MERGE_COOLDOWN- \
    MMO_GRID_MERGE_THRESHOLD_MAX_PLAYERS- \
    MMO_GRID_MERGE_THRESHOLD_MAX_ENTITIES- \
    MMO_GRID_MERGE_THRESHOLD_MAX_TICK_SECONDS- >/dev/null || true
  echo "== cleanup: restore registry addr + keep workflows enabled =="
  RESTORE_ARGS=(
    "MMO_GRID_REGISTRY_ADDR=${RESTORE_REGISTRY_ADDR}" \
    MMO_GRID_AUTO_SPLIT_DRAIN=true \
    MMO_GRID_AUTO_SPLIT_WORKFLOW=true \
    MMO_GRID_AUTO_MERGE_WORKFLOW=true
  )
  if [[ -n "${ORIG_SPLIT_MAX_LEVEL:-}" ]]; then
    RESTORE_ARGS+=("MMO_GRID_SPLIT_MAX_LEVEL=${ORIG_SPLIT_MAX_LEVEL}")
  fi
  kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" "${RESTORE_ARGS[@]}" >/dev/null || true
  kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=180s >/dev/null || true
}
trap cleanup EXIT

metric_value() {
  local metric_line="$1"
  local body="$2"
  printf '%s\n' "$body" | awk -v m="$metric_line" '
    index($0, m) == 1 {
      v=$NF
      sub(/\r$/, "", v)
      print v
      found=1
      exit
    }
    END { if (!found) print "0" }
  '
}

wait_metric_growth() {
  local metric_line="$1"
  local baseline="$2"
  local timeout_seconds="$3"
  local step="${4:-6}"
  local elapsed=0
  while (( elapsed < timeout_seconds )); do
    local m
    m="$(curl -sf "http://127.0.0.1:${METRICS_LOCAL_PORT}/metrics")"
    local cur
    cur="$(metric_value "$metric_line" "$m")"
    if awk -v c="$cur" -v b="$baseline" 'BEGIN{exit !(c>b)}'; then
      echo "$cur"
      return 0
    fi
    sleep "$step"
    elapsed=$((elapsed + step))
  done
  return 1
}

echo "== reset auto child workloads =="
ORIG_SPLIT_MAX_LEVEL="$(kubectl -n "$NS" get deploy "$GRID_DEPLOY" -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={"="}{.value}{"\n"}{end}' | awk -F= '$1=="MMO_GRID_SPLIT_MAX_LEVEL"{print $2; exit}')"
auto_deploys="$(kubectl -n "$NS" get deploy -o name | rg '^deployment.apps/cell-node-auto-' || true)"
auto_svcs="$(kubectl -n "$NS" get svc -o name | rg '^service/mmo-cell-auto-' || true)"
if [[ -n "${auto_deploys:-}" ]]; then
  printf '%s\n' "$auto_deploys" | xargs -r kubectl -n "$NS" delete --ignore-not-found >/dev/null || true
fi
if [[ -n "${auto_svcs:-}" ]]; then
  printf '%s\n' "$auto_svcs" | xargs -r kubectl -n "$NS" delete --ignore-not-found >/dev/null || true
fi
kubectl -n "$NS" wait --for=delete pod -l app=cell-node,cell_shard!=primary --timeout=120s >/dev/null 2>&1 || true

echo "== set env for phase-1 (force split) =="
kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
  MMO_GRID_AUTO_SPLIT_DRAIN=true \
  MMO_GRID_AUTO_SPLIT_WORKFLOW=true \
  MMO_GRID_AUTO_MERGE_WORKFLOW=true \
  MMO_GRID_REGISTRY_ADDR=127.0.0.1:9100 \
  MMO_GRID_SPLIT_MAX_LEVEL=1 \
  MMO_GRID_THRESHOLD_MAX_TICK_SECONDS=0.000001 \
  MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION=8s \
  MMO_GRID_LOAD_POLICY_COOLDOWN=40s \
  MMO_GRID_CELL_PROBE_INTERVAL=6s \
  MMO_GRID_MERGE_MIN_LOW_LOAD_DURATION=20s \
  MMO_GRID_MERGE_COOLDOWN=40s \
  MMO_GRID_MERGE_THRESHOLD_MAX_PLAYERS=0 \
  MMO_GRID_MERGE_THRESHOLD_MAX_ENTITIES=100000 \
  MMO_GRID_MERGE_THRESHOLD_MAX_TICK_SECONDS=1 >/dev/null
kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=180s

echo "== port-forward metrics =="
kubectl -n "$NS" port-forward "deployment/$GRID_DEPLOY" "${METRICS_LOCAL_PORT}:${METRICS_REMOTE_PORT}" >/tmp/grid-full-cycle-e2e.pf.log 2>&1 &
PF_PID=$!
sleep 2

metrics0="$(curl -sf "http://127.0.0.1:${METRICS_LOCAL_PORT}/metrics")"
split_base="$(metric_value 'mmo_grid_manager_split_workflow_runs_total{result="ok"}' "$metrics0")"
merge_base="$(metric_value 'mmo_grid_manager_merge_workflow_runs_total{result="ok"}' "$metrics0")"
echo "baseline split_ok=${split_base} merge_ok=${merge_base}"

echo "== wait split workflow success growth (timeout=${SPLIT_WAIT_SECONDS}s) =="
split_now="$(wait_metric_growth 'mmo_grid_manager_split_workflow_runs_total{result="ok"}' "$split_base" "$SPLIT_WAIT_SECONDS" 6)" || {
  echo "ERROR: split ok metric did not grow" >&2
  exit 1
}
echo "split ok metric grew: ${split_base} -> ${split_now}"

echo "== verify split automation marker in Redis =="
RS="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl split-retire-state "$PARENT_CELL" 2>/dev/null || echo "{}")"
echo "$RS"
echo "$RS" | grep -qE '"phase"[[:space:]]*:[[:space:]]*"automation_complete"' || {
  echo "ERROR: split retire_state phase!=automation_complete for ${PARENT_CELL}" >&2
  exit 1
}
if ! kubectl -n "$NS" logs "deploy/${CELL_CONTROLLER_DEPLOY}" --since=20m 2>/dev/null | rg 'automation_complete_set' >/dev/null; then
  echo "ERROR: no automation_complete_set in cell-controller logs" >&2
  exit 1
fi

echo "== wait merge workflow success growth (timeout=${MERGE_WAIT_SECONDS}s) =="
merge_now="$(wait_metric_growth 'mmo_grid_manager_merge_workflow_runs_total{result="ok"}' "$merge_base" "$MERGE_WAIT_SECONDS" 6)" || {
  echo "ERROR: merge ok metric did not grow" >&2
  exit 1
}
echo "merge ok metric grew: ${merge_base} -> ${merge_now}"

echo "== verify merge automation marker in Redis =="
MS="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl merge-state "$PARENT_CELL" 2>/dev/null || echo "{}")"
echo "$MS"
if [[ "$MS" == "{}" ]]; then
  echo "ERROR: merge-state is empty" >&2
  exit 1
fi
echo "$MS" | grep -qE '"topology_switched"[[:space:]]*:[[:space:]]*true' || {
  echo "ERROR: merge-state missing topology_switched=true" >&2
  exit 1
}

echo "OK: full auto split->merge cycle smoke finished"
