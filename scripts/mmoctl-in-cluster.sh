#!/usr/bin/env bash
# Запуск mmoctl внутри пода grid-manager: registry на localhost:9100, cell — по DNS из каталога
# (так работает migration-dry-run: прямой gRPC к соте).
#
# Требуется: kubectl, выкатанный образ с бинарём /mmoctl (см. Dockerfile).
#
# Примеры:
#   ./scripts/mmoctl-in-cluster.sh -registry 127.0.0.1:9100 list
#   ./scripts/mmoctl-in-cluster.sh -registry 127.0.0.1:9100 migration-dry-run cell_0_0_0
#   ./scripts/mmoctl-in-cluster.sh -registry 127.0.0.1:9100 forward-update cell_0_0_0 export-npc-persist smoke
#
set -euo pipefail
NS="${K8S_NAMESPACE:-mmo}"
DEPLOY="${GRID_MANAGER_DEPLOY:-grid-manager}"
exec kubectl exec -n "$NS" "deploy/${DEPLOY}" -- /mmoctl "$@"
