#!/usr/bin/env bash
# Прогон политики нагрузки grid-manager на staging: искусственное нарушение порога по tick,
# ожидание срабатывания mmo_grid_manager_load_policy_actions_total, откат env.
#
# Требуется: kubectl, curl, grep, доступ к кластеру, namespace mmo, у grid-manager включён -metrics-listen.
#
# Переопределение:
#   NAMESPACE=mmo METRICS_LOCAL_PORT=19091 bash scripts/grid-load-policy-smoke.sh
#
set -euo pipefail

NS="${NAMESPACE:-mmo}"
DEPLOY="${GRID_DEPLOY:-grid-manager}"
METRICS_LOCAL="${METRICS_LOCAL_PORT:-19091}"
METRICS_CONTAINER_PORT="${GRID_METRICS_CONTAINER_PORT:-9091}"
WAIT_ROLLOUT="${WAIT_ROLLOUT:-120s}"
SLEEP_AFTER_ROLLOUT="${SLEEP_AFTER_ROLLOUT:-45}"
SKIP_RESTORE="${SKIP_RESTORE:-0}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "нужна команда: $1" >&2; exit 1; }; }
need kubectl
need curl
need grep

PF_PID=""

restore_env() {
  if [[ "$SKIP_RESTORE" == "1" ]]; then
    return 0
  fi
  echo "== restore: снимаем временные env с $DEPLOY =="
  kubectl -n "$NS" set env "deployment/$DEPLOY" \
    MMO_GRID_THRESHOLD_MAX_TICK_SECONDS- \
    MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION- \
    MMO_GRID_LOAD_POLICY_COOLDOWN- \
    MMO_GRID_CELL_PROBE_INTERVAL- \
    MMO_GRID_AUTO_SPLIT_DRAIN- 2>/dev/null || true
  kubectl -n "$NS" rollout status "deployment/$DEPLOY" --timeout="$WAIT_ROLLOUT" || true
}

cleanup() {
  local st=$?
  if [[ -n "$PF_PID" ]] && kill -0 "$PF_PID" 2>/dev/null; then
    kill "$PF_PID" 2>/dev/null || true
    wait "$PF_PID" 2>/dev/null || true
  fi
  restore_env
  exit "$st"
}
trap cleanup EXIT

echo "== patch env: узкое окно tick + короткий breach (только для smoke) =="
kubectl -n "$NS" set env "deployment/$DEPLOY" \
  MMO_GRID_THRESHOLD_MAX_TICK_SECONDS=0.000001 \
  MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION=8s \
  MMO_GRID_LOAD_POLICY_COOLDOWN=60s \
  MMO_GRID_CELL_PROBE_INTERVAL=6s \
  MMO_GRID_AUTO_SPLIT_DRAIN=false

kubectl -n "$NS" rollout status "deployment/$DEPLOY" --timeout="$WAIT_ROLLOUT"

echo "== port-forward metrics :$METRICS_LOCAL -> pod:$METRICS_CONTAINER_PORT =="
kubectl -n "$NS" port-forward "deployment/$DEPLOY" "${METRICS_LOCAL}:${METRICS_CONTAINER_PORT}" &
PF_PID=$!
sleep 2

BASE="http://127.0.0.1:${METRICS_LOCAL}"
if ! curl -sf "$BASE/metrics" >/dev/null; then
  echo "ERROR: нет ответа от $BASE/metrics (проверьте grid_metrics_port в Terraform и -metrics-listen)" >&2
  exit 1
fi

echo "== ждём накопление breach + действие policy (${SLEEP_AFTER_ROLLOUT}s) =="
sleep "$SLEEP_AFTER_ROLLOUT"

M=$(curl -sf "$BASE/metrics")

echo "== выборка метрик (фрагмент) =="
echo "$M" | grep -E 'mmo_grid_manager_(cell_within_hard_limits|cell_threshold_violation|load_policy_actions_total)' || true

if echo "$M" | grep -q 'mmo_grid_manager_load_policy_actions_total'; then
  echo "OK: load_policy_actions_total присутствует в scrape."
else
  echo "WARN: mmo_grid_manager_load_policy_actions_total пока не видна — увеличьте SLEEP_AFTER_ROLLOUT или проверьте reachable/probe." >&2
fi

if echo "$M" | grep -q 'cell_within_hard_limits{cell_id='; then
  echo "OK: cell_within_hard_limits экспортируется."
else
  echo "WARN: нет series cell_within_hard_limits — возможно каталог пуст или нет grpc_endpoint у сот." >&2
fi

echo "== smoke завершён (restore через trap) =="
