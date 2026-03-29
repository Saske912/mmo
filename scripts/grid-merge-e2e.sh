#!/usr/bin/env bash
# E2E smoke для merge handoff (MVP):
#  1) берёт 4 child уровня 1 для parent из каталога
#  2) вызывает mmoctl forward-merge-handoff (children -> parent)
#  3) проверяет merge workflow метрику result="ok"
set -euo pipefail

NS="${NAMESPACE:-mmo}"
GRID_DEPLOY="${GRID_DEPLOY:-grid-manager}"
REGISTRY_PORT="${REGISTRY_PORT:-9100}"
PARENT_CELL="${MERGE_PARENT_CELL_ID:-cell_0_0_0}"
METRICS_LOCAL_PORT="${METRICS_LOCAL_PORT:-19095}"
METRICS_REMOTE_PORT="${GRID_METRICS_CONTAINER_PORT:-9091}"
PF_PID=""

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need curl
need awk
need grep

cleanup() {
  if [[ -n "${PF_PID:-}" ]] && kill -0 "$PF_PID" 2>/dev/null; then
    kill "$PF_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

echo "== list catalog =="
LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list)"
echo "$LIST"

mapfile -t CHILD_IDS < <(printf '%s\n' "$LIST" | awk '
  / level=1 / && $1 != "cell_0_0_0" { print $1 }
')
if [[ "${#CHILD_IDS[@]}" -lt 4 ]]; then
  echo "ERROR: need at least 4 level=1 child cells in catalog; got ${#CHILD_IDS[@]}" >&2
  echo "hint: run make split-e2e-smoke first" >&2
  exit 1
fi
CHILD_IDS=("${CHILD_IDS[@]:0:4}")
CHILD_CSV="$(IFS=,; echo "${CHILD_IDS[*]}")"
echo "parent=${PARENT_CELL}"
echo "children=${CHILD_CSV}"

echo "== run forward-merge-handoff =="
OUT="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" forward-merge-handoff "$PARENT_CELL" "$CHILD_CSV" "grid-merge-e2e")"
echo "$OUT"
if ! echo "$OUT" | grep -q 'ok=true'; then
  echo "ERROR: merge handoff failed" >&2
  exit 1
fi

echo "== verify merge workflow metric =="
kubectl -n "$NS" port-forward "deployment/$GRID_DEPLOY" "${METRICS_LOCAL_PORT}:${METRICS_REMOTE_PORT}" >/tmp/grid-merge-e2e.pf.log 2>&1 &
PF_PID=$!
sleep 2
M="$(curl -sf "http://127.0.0.1:${METRICS_LOCAL_PORT}/metrics")"
echo "$M" | grep -E 'mmo_grid_manager_merge_workflow_runs_total|mmo_grid_manager_merge_workflow_duration_seconds' || true
if ! echo "$M" | grep -q 'mmo_grid_manager_merge_workflow_runs_total{result="ok"}'; then
  echo "ERROR: no merge workflow result=\"ok\" metric" >&2
  exit 1
fi

echo "OK: merge e2e smoke finished"
