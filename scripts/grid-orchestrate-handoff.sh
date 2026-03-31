#!/usr/bin/env bash
# Оркестрация B3 handoff через registry:
#   1) split-drain true на parent
#   2) (опционально) migration-dry-run
#   3) forward-npc-handoff parent -> child
#   4) split-drain false на parent (опционально)
#
# Скрипт намеренно не меняет Terraform tfvars: infra-шаг остаётся явным и контролируемым.
#
# Примеры:
#   REGISTRY=127.0.0.1:9100 PARENT=cell_root CHILD=cell_q0 bash scripts/grid-orchestrate-handoff.sh
#   EXECUTE=1 REGISTRY=127.0.0.1:9100 PARENT=... CHILD=... bash scripts/grid-orchestrate-handoff.sh
#   EXECUTE=1 MODE=incluster PARENT=cell_root CHILD=cell_q0 bash scripts/grid-orchestrate-handoff.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

REGISTRY="${REGISTRY:-127.0.0.1:9100}"
PARENT="${PARENT:-}"
CHILD="${CHILD:-}"
REASON="${REASON:-grid-orchestrate-handoff}"
RUN_DRY_RUN="${RUN_DRY_RUN:-1}"
UNSET_DRAIN="${UNSET_DRAIN:-1}"
EXECUTE="${EXECUTE:-0}"
MODE="${MODE:-local}" # local | incluster | k8s
K8S_NAMESPACE="${K8S_NAMESPACE:-mmo}"
REGISTRY_PORT="${REGISTRY_PORT:-9100}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }; }
need /bin/bash

if [[ -z "$PARENT" || -z "$CHILD" ]]; then
  cat >&2 <<'EOF'
usage:
  REGISTRY=<host:port> PARENT=<cell_id> CHILD=<cell_id> [EXECUTE=1] [MODE=local|incluster] bash scripts/grid-orchestrate-handoff.sh

env:
  MODE=incluster   kubectl exec deploy/grid-manager -- /mmoctl -registry 127.0.0.1:9100 …
  RUN_DRY_RUN=1|0  (default: 1)
  UNSET_DRAIN=1|0  (default: 1)
  REASON=<text>    (default: grid-orchestrate-handoff)
  EXECUTE=1        actually execute commands (default: 0 = preview only)
EOF
  exit 2
fi

incluster() {
  [[ "$MODE" == "incluster" || "$MODE" == "k8s" ]]
}

if incluster; then
  need kubectl
else
  REGISTRY="${REGISTRY:-127.0.0.1:9100}"
  need go
fi

run_mmoctl() {
  local -a cmd
  if incluster; then
    cmd=(kubectl -n "$K8S_NAMESPACE" exec deploy/grid-manager -- /mmoctl -registry "127.0.0.1:${REGISTRY_PORT}" "$@")
  else
    cmd=(go run ./cmd/mmoctl -registry "$REGISTRY" "$@")
  fi
  echo "+ ${cmd[*]}"
  if [[ "$EXECUTE" == "1" ]]; then
    "${cmd[@]}"
  fi
}

echo "mode=$MODE registry=$REGISTRY parent=$PARENT child=$CHILD execute=$EXECUTE"

run_mmoctl forward-update "$PARENT" split-drain true

if [[ "$RUN_DRY_RUN" == "1" ]]; then
  run_mmoctl migration-dry-run "$PARENT"
fi

run_mmoctl forward-npc-handoff "$PARENT" "$CHILD" "$REASON"

if [[ "$UNSET_DRAIN" == "1" ]]; then
  run_mmoctl forward-update "$PARENT" split-drain false
fi

cat <<'EOF'
next steps:
  1) check split workflow / cell-controller logs (children created + ready)
  2) run scripts/staging-verify.sh
  3) keep Terraform cell_instances focused on baseline primary topology
EOF
