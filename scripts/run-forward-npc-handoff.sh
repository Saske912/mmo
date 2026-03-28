#!/usr/bin/env bash
# Один вызов mmoctl forward-npc-handoff (runbooks/cold-cell-split.md §6–§7).
# Требует: KUBECONFIG, kubectl, grid-manager с /mmoctl ИЛИ локальный mmoctl + registry:9100 на localhost.
#
# Примеры:
#   PARENT=cell_0_0_0 CHILD=cell_-1_-1_1 TICKET=handoff-$(date +%s) bash scripts/run-forward-npc-handoff.sh
#   REGISTRY=127.0.0.1:9100 PARENT=... CHILD=... ...  — явный registry
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

REGISTRY="${REGISTRY:-127.0.0.1:9100}"
PARENT="${PARENT:-}"
CHILD="${CHILD:-}"
TICKET="${TICKET:-npc-handoff-$(date +%s)}"
MODE="${MODE:-local}" # local | incluster

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }

if [ -z "$PARENT" ] || [ -z "$CHILD" ]; then
  echo "Задайте PARENT и CHILD (cell_id). Опционально TICKET, REGISTRY, MODE=incluster." >&2
  exit 1
fi

if [ "$MODE" = "incluster" ] || [ "$MODE" = "k8s" ]; then
  need kubectl
  NS="${K8S_NAMESPACE:-mmo}"
  echo "== kubectl exec deploy/grid-manager: mmoctl forward-npc-handoff $PARENT $CHILD $TICKET =="
  kubectl exec -n "$NS" deploy/grid-manager -- /mmoctl -registry "127.0.0.1:9100" forward-npc-handoff "$PARENT" "$CHILD" "$TICKET"
  exit 0
fi

need go
echo "== go run mmoctl -registry $REGISTRY forward-npc-handoff $PARENT $CHILD $TICKET =="
go run ./cmd/mmoctl -registry "$REGISTRY" forward-npc-handoff "$PARENT" "$CHILD" "$TICKET"
