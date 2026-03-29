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
#   REGISTRY=127.0.0.1:9100 PARENT=cell_0_0_0 CHILD=cell_-1_-1_1 bash scripts/grid-orchestrate-handoff.sh
#   EXECUTE=1 REGISTRY=127.0.0.1:9100 PARENT=cell_0_0_0 CHILD=cell_-1_-1_1 bash scripts/grid-orchestrate-handoff.sh
#
set -euo pipefail

REGISTRY="${REGISTRY:-127.0.0.1:9100}"
PARENT="${PARENT:-}"
CHILD="${CHILD:-}"
REASON="${REASON:-grid-orchestrate-handoff}"
RUN_DRY_RUN="${RUN_DRY_RUN:-1}"
UNSET_DRAIN="${UNSET_DRAIN:-1}"
EXECUTE="${EXECUTE:-0}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing command: $1" >&2; exit 1; }; }
need /bin/bash
need go

if [[ -z "$PARENT" || -z "$CHILD" ]]; then
  cat >&2 <<'EOF'
usage:
  REGISTRY=<host:port> PARENT=<cell_id> CHILD=<cell_id> [EXECUTE=1] bash scripts/grid-orchestrate-handoff.sh

env:
  RUN_DRY_RUN=1|0  (default: 1)
  UNSET_DRAIN=1|0  (default: 1)
  REASON=<text>    (default: grid-orchestrate-handoff)
  EXECUTE=1        actually execute commands (default: 0 = preview only)
EOF
  exit 2
fi

run() {
  echo "+ $*"
  if [[ "$EXECUTE" == "1" ]]; then
    "$@"
  fi
}

echo "registry=$REGISTRY parent=$PARENT child=$CHILD execute=$EXECUTE"

run go run ./cmd/mmoctl -registry "$REGISTRY" forward-update "$PARENT" split-drain true

if [[ "$RUN_DRY_RUN" == "1" ]]; then
  run go run ./cmd/mmoctl -registry "$REGISTRY" migration-dry-run "$PARENT"
fi

run go run ./cmd/mmoctl -registry "$REGISTRY" forward-npc-handoff "$PARENT" "$CHILD" "$REASON"

if [[ "$UNSET_DRAIN" == "1" ]]; then
  run go run ./cmd/mmoctl -registry "$REGISTRY" forward-update "$PARENT" split-drain false
fi

cat <<'EOF'
next steps (manual, out of this script):
  1) update deploy/terraform/staging/cell_instances.auto.tfvars if topology changed
  2) tofu apply in deploy/terraform/staging
  3) run scripts/staging-verify.sh
EOF
