#!/usr/bin/env bash
# E2E smoke для live merge handoff игроков:
#  1) включает MMO_GRID_MERGE_PLAYER_HANDOFF на grid-manager
#  2) создаёт тестовых игроков на child-соте (Join напрямую в child endpoint)
#  3) выполняет forward-merge-handoff children -> parent
#  4) проверяет, что игроки появились на parent и исчезли на child
set -euo pipefail

NS="${NAMESPACE:-mmo}"
GRID_DEPLOY="${GRID_DEPLOY:-grid-manager}"
REGISTRY_PORT="${REGISTRY_PORT:-9100}"
PARENT_CELL="${MERGE_PARENT_CELL_ID:-cell_0_0_0}"
PLAYER_PREFIX="${PLAYER_PREFIX:-merge-live}"
PLAYER_COUNT="${PLAYER_COUNT:-2}"
ENABLE_HANDOFF_MAX="${MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS:-16}"
AUTO_PREPARE_SPLIT="${AUTO_PREPARE_SPLIT:-1}"
AUTO_SELECT_PARENT="${AUTO_SELECT_PARENT:-1}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need awk
need grep

cleanup() {
  echo "== cleanup merge-player env =="
  kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
    "MMO_GRID_MERGE_PLAYER_HANDOFF=false" \
    MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS- >/dev/null 2>&1 || true
  kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=120s >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== enable merge player handoff env =="
kubectl -n "$NS" set env "deployment/$GRID_DEPLOY" \
  "MMO_GRID_MERGE_PLAYER_HANDOFF=true" \
  "MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS=${ENABLE_HANDOFF_MAX}" >/dev/null
kubectl -n "$NS" rollout status "deployment/$GRID_DEPLOY" --timeout=120s >/dev/null

echo "== read catalog =="
LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list)"
echo "$LIST"

PARENT_EP="$(printf '%s\n' "$LIST" | awk -v id="$PARENT_CELL" '$1==id{for(i=1;i<=NF;i++) if($i ~ /^endpoint=/){sub(/^endpoint=/,"",$i); print $i; exit}}')"
PARENT_LEVEL="$(printf '%s\n' "$LIST" | awk -v id="$PARENT_CELL" '$1==id{for(i=1;i<=NF;i++) if($i ~ /^level=/){sub(/^level=/,"",$i); print $i; exit}}')"
if [[ -z "$PARENT_EP" || -z "$PARENT_LEVEL" ]]; then
  echo "ERROR: parent spec not found for ${PARENT_CELL}" >&2
  exit 1
fi

ensure_parent_ready() {
  local pid="$1" pep="$2"
  local out players
  out="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl migration-candidates "$pep" "merge-live-parent-check" 2>/dev/null || true)"
  [[ -n "$out" ]] || return 1
  players="$(echo "$out" | grep -c 'is_player=true' || true)"
  [[ "$players" == "0" ]]
}

if ! ensure_parent_ready "$PARENT_CELL" "$PARENT_EP"; then
  if [[ "$AUTO_SELECT_PARENT" == "1" || "$AUTO_SELECT_PARENT" == "true" ]]; then
    echo "== parent has active players, auto-selecting alternative parent =="
    while IFS= read -r line; do
      [[ -z "$line" ]] && continue
      cid="$(echo "$line" | awk '{print $1}')"
      lvl="$(echo "$line" | awk '{for(i=1;i<=NF;i++) if($i ~ /^level=/){sub(/^level=/,"",$i); print $i; exit}}')"
      ep="$(echo "$line" | awk '{for(i=1;i<=NF;i++) if($i ~ /^endpoint=/){sub(/^endpoint=/,"",$i); print $i; exit}}')"
      [[ -z "$cid" || -z "$lvl" || -z "$ep" ]] && continue
      [[ "$lvl" -lt 1 ]] && continue
      if ensure_parent_ready "$cid" "$ep"; then
        PARENT_CELL="$cid"
        PARENT_LEVEL="$lvl"
        PARENT_EP="$ep"
        break
      fi
    done <<< "$LIST"
  fi
fi

if ! ensure_parent_ready "$PARENT_CELL" "$PARENT_EP"; then
  echo "ERROR: parent ${PARENT_CELL} has active players; choose MERGE_PARENT_CELL_ID with 0 players" >&2
  exit 1
fi

NEXT_LEVEL=$((PARENT_LEVEL + 1))
CHILD_IDS=("cell_-1_-1_${NEXT_LEVEL}" "cell_1_-1_${NEXT_LEVEL}" "cell_-1_1_${NEXT_LEVEL}" "cell_1_1_${NEXT_LEVEL}")
for cid in "${CHILD_IDS[@]}"; do
  if ! printf '%s\n' "$LIST" | grep -q "^${cid} "; then
    echo "ERROR: expected child ${cid} not found in catalog for parent level ${PARENT_LEVEL}" >&2
    if [[ "$AUTO_PREPARE_SPLIT" == "1" || "$AUTO_PREPARE_SPLIT" == "true" ]]; then
      echo "== child missing, running split smoke bootstrap =="
      bash scripts/grid-auto-split-e2e.sh
      LIST="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" list)"
      echo "$LIST"
      if ! printf '%s\n' "$LIST" | grep -q "^${cid} "; then
        echo "ERROR: child ${cid} still missing after split bootstrap" >&2
        exit 1
      fi
    else
      exit 1
    fi
  fi
done
CHILD_CSV="$(IFS=,; echo "${CHILD_IDS[*]}")"
SOURCE_CHILD="${CHILD_IDS[0]}"
SOURCE_EP="$(printf '%s\n' "$LIST" | awk -v id="$SOURCE_CHILD" '$1==id{for(i=1;i<=NF;i++) if($i ~ /^endpoint=/){sub(/^endpoint=/,"",$i); print $i; exit}}')"
if [[ -z "$SOURCE_EP" ]]; then
  echo "ERROR: source child endpoint not found for ${SOURCE_CHILD}" >&2
  exit 1
fi

echo "== join ${PLAYER_COUNT} players to ${SOURCE_CHILD} (${SOURCE_EP}) =="
for i in $(seq 1 "$PLAYER_COUNT"); do
  PID="${PLAYER_PREFIX}-${i}-$(date +%s)"
  OUT="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl join "$SOURCE_EP" "$PID")"
  echo "$OUT"
  echo "$OUT" | grep -q 'ok=true' || {
    echo "ERROR: join failed for $PID" >&2
    exit 1
  }
done

echo "== run forward-merge-handoff =="
OUT="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" forward-merge-handoff "$PARENT_CELL" "$CHILD_CSV" "grid-merge-live-players-e2e")"
echo "$OUT"
echo "$OUT" | grep -q 'ok=true' || {
  echo "ERROR: merge handoff failed" >&2
  exit 1
}

echo "== verify players moved to parent =="
PARENT_CANDS="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl migration-candidates "$PARENT_EP" "merge-live-verify-parent" || true)"
echo "$PARENT_CANDS"
PARENT_PLAYERS="$(echo "$PARENT_CANDS" | grep -c 'is_player=true' || true)"
if [[ "$PARENT_PLAYERS" -lt "$PLAYER_COUNT" ]]; then
  echo "ERROR: expected >=${PLAYER_COUNT} players on parent, got ${PARENT_PLAYERS}" >&2
  exit 1
fi

echo "== verify source child no longer has those players =="
CHILD_CANDS="$(kubectl -n "$NS" exec "deploy/$GRID_DEPLOY" -- /mmoctl migration-candidates "$SOURCE_EP" "merge-live-verify-child" || true)"
echo "$CHILD_CANDS"
if echo "$CHILD_CANDS" | grep -q 'is_player=true'; then
  echo "ERROR: source child still has players after handoff" >&2
  exit 1
fi

echo "OK: live merge players e2e smoke finished"
