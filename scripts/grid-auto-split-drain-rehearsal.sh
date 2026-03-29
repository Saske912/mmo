#!/usr/bin/env bash
# Репетиция: load policy + MMO_GRID_AUTO_SPLIT_DRAIN → подтверждение split_drain на сотах.
#
# Предусловия:
#   - В deployment grid-manager уже выставлен MMO_GRID_AUTO_SPLIT_DRAIN=true (Terraform grid_manager_extra_env или ручной kubectl set env).
#   - kubectl, curl; namespace mmo; grid-manager слушает /metrics и gRPC :9100 в поде.
#
# Действия:
#   1) Проверяет флаг AUTO_SPLIT_DRAIN.
#   2) Временно занижает MMO_GRID_THRESHOLD_MAX_TICK_SECONDS и укорачивает breach/probe.
#   3) Ждёт counter mmo_grid_manager_load_policy_actions_total с action=split_drain_enable.
#   4) Для каждой соты в каталоге: Join через in-cluster endpoint → ожидает ok=false и split_drain в message.
#   5) Снимает пороги; на всех сотах forward-update split-drain false.
#
# Переменные:
#   NS, GRID_DEPLOY, METRICS_LOCAL_PORT, SLEEP_AFTER_ROLLOUT, REGISTRY_PORT (внутри пода, 9100)
#
set -euo pipefail

NS="${NAMESPACE:-mmo}"
GRID_DEPLOY="${GRID_DEPLOY:-grid-manager}"
METRICS_LOCAL="${METRICS_LOCAL_PORT:-19092}"
METRICS_REMOTE="${GRID_METRICS_CONTAINER_PORT:-9091}"
REGISTRY_PORT="${REGISTRY_PORT:-9100}"
WAIT_ROLLOUT="${WAIT_ROLLOUT:-120s}"
SLEEP_AFTER_ROLLOUT="${SLEEP_AFTER_ROLLOUT:-50}"
PF_PID=""

need() { command -v "$1" >/dev/null 2>&1 || { echo "нужна команда: $1" >&2; exit 1; }; }
need kubectl
need curl
need grep

deploy_env_lines() {
  kubectl -n "$NS" get deploy "$GRID_DEPLOY" -o jsonpath="{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{'\n'}{end}" 2>/dev/null || true
}

auto_split_drain_enabled() {
  deploy_env_lines | grep -iE '^MMO_GRID_AUTO_SPLIT_DRAIN=(1|true|yes)$' >/dev/null 2>&1
}

restore_thresholds() {
  echo "== restore thresholds на $GRID_DEPLOY =="
  kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
    MMO_GRID_THRESHOLD_MAX_TICK_SECONDS- \
    MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION- \
    MMO_GRID_LOAD_POLICY_COOLDOWN- \
    MMO_GRID_CELL_PROBE_INTERVAL- 2>/dev/null || true
  kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout="$WAIT_ROLLOUT" || true
}

disable_split_drain_all() {
  echo "== split-drain false на всех сотах из каталога =="
  local list out
  list="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list 2>/dev/null)" || {
    echo "ERROR: mmoctl list из пода grid-manager" >&2
    return 1
  }
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    local cid
    cid="$(echo "$line" | awk '{print $1}')"
    [ -z "$cid" ] && continue
    out="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" forward-update "$cid" split-drain false 2>&1)" || true
    echo "$out"
  done <<< "$list"
}

cleanup() {
  local st=$?
  if [[ -n "${PF_PID:-}" ]] && kill -0 "$PF_PID" 2>/dev/null; then
    kill "$PF_PID" 2>/dev/null || true
    wait "$PF_PID" 2>/dev/null || true
  fi
  restore_thresholds
  disable_split_drain_all || true
  exit "$st"
}

if ! auto_split_drain_enabled; then
  echo "ERROR: в deployment/grid-manager нет MMO_GRID_AUTO_SPLIT_DRAIN=true (или 1/yes)." >&2
  echo "Задайте grid_manager_extra_env в Terraform (см. deploy/terraform/staging/grid_manager.auto.tfvars.example) и tofu apply," >&2
  echo "либо временно: kubectl -n $NS set env deployment/$GRID_DEPLOY MMO_GRID_AUTO_SPLIT_DRAIN=true" >&2
  exit 1
fi

trap cleanup EXIT

echo "== MMO_GRID_AUTO_SPLIT_DRAIN включён в манифесте grid-manager — OK =="

echo "== patch: узкий порог tick + короткий breach/probe (временно) =="
kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
  MMO_GRID_THRESHOLD_MAX_TICK_SECONDS=0.000001 \
  MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION=8s \
  MMO_GRID_LOAD_POLICY_COOLDOWN=60s \
  MMO_GRID_CELL_PROBE_INTERVAL=6s
kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout="$WAIT_ROLLOUT"

echo "== port-forward metrics localhost:${METRICS_LOCAL}:${METRICS_REMOTE} =="
kubectl -n "$NS" port-forward "deployment/$GRID_DEPLOY" "${METRICS_LOCAL}:${METRICS_REMOTE}" >/dev/null 2>&1 &
PF_PID=$!
sleep 2
BASE="http://127.0.0.1:${METRICS_LOCAL}"
if ! curl -sf "$BASE/metrics" >/dev/null; then
  echo "ERROR: нет /metrics на $BASE" >&2
  exit 1
fi

echo "== ждём policy (до ${SLEEP_AFTER_ROLLOUT}s) =="
sleep "$SLEEP_AFTER_ROLLOUT"

M=$(curl -sf "$BASE/metrics")
echo "$M" | grep -E 'mmo_grid_manager_load_policy_actions_total|mmo_grid_manager_cell_within_hard_limits' || true

if ! echo "$M" | grep -q 'action="split_drain_enable"'; then
  echo "WARN: не нашли counter с action=split_drain_enable — увеличьте SLEEP_AFTER_ROLLOUT или проверьте reachability сот." >&2
else
  echo "OK: зафиксировано действие split_drain_enable в метриках."
fi

echo "== Join по каждой соте (ожидаем split_drain) =="
LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list)"
PLAYER="rehearsal-drain-$(date +%s)"
FAIL=0
while IFS= read -r line; do
  [ -z "$line" ] && continue
  cid="$(echo "$line" | awk '{print $1}')"
  ep="$(echo "$line" | sed -n 's/.*endpoint=\([^ ]*\).*/\1/p')"
  if [ -z "$cid" ] || [ -z "$ep" ]; then
    continue
  fi
  echo "--- cell_id=$cid endpoint=$ep ---"
  OUT="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl join "$ep" "${PLAYER}-${cid}" 2>&1)" || true
  echo "$OUT"
  if echo "$OUT" | grep -q 'ok=true'; then
    echo "ERROR: ожидали ok=false при split_drain для $cid" >&2
    FAIL=1
  fi
  if ! echo "$OUT" | grep -qi 'split_drain'; then
    echo "WARN: в ответе Join нет текста split_drain для $cid" >&2
  fi
done <<< "$LIST"

if [ "$FAIL" != 0 ]; then
  exit 1
fi

echo "OK: репетиция Join при включённом split_drain прошла."

echo "== cleanup выполнит trap (пороги + split-drain false) =="
