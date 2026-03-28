#!/usr/bin/env bash
# Первичный импорт уже существующих объектов Kubernetes в OpenTofu state каталога
# deploy/terraform/staging. Запускать ОДИН раз на пустом state после:
#   - cp deploy/terraform/staging/backend.tf.example deploy/terraform/staging/backend.tf
#   - tofu init -reconfigure
#   - наличие certs/*.pem если gateway_ingress_enabled = true
#
# Повторный импорт тех же адресов завершится ошибкой — это нормально.
#
# Использование из корня backend:
#   bash scripts/import-staging-tofu-state.sh
#
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGING="$ROOT/deploy/terraform/staging"
NS="${K8S_IMPORT_NAMESPACE:-mmo}"

cd "$STAGING"
need() { command -v "$1" >/dev/null 2>&1 || { echo "нужна команда: $1" >&2; exit 1; }; }
need tofu
need kubectl

tofu import -input=false kubernetes_namespace.mmo "$NS"
tofu import -input=false kubernetes_secret.mmo_backend "$NS/mmo-backend"
tofu import -input=false 'kubernetes_secret.gateway_tls[0]' "$NS/mmo-gateway-tls"

tofu import -input=false kubernetes_deployment.grid_manager "$NS/grid-manager"
tofu import -input=false 'kubernetes_deployment.cell_node["primary"]' "$NS/cell-node"
tofu import -input=false 'kubernetes_deployment.cell_node["child-sw"]' "$NS/cell-node-child-sw"
tofu import -input=false kubernetes_deployment.gateway "$NS/gateway"

tofu import -input=false kubernetes_service.grid_manager "$NS/mmo-grid-manager"
tofu import -input=false 'kubernetes_service.cell["primary"]' "$NS/mmo-cell"
tofu import -input=false 'kubernetes_service.cell["child-sw"]' "$NS/mmo-cell-child-sw"
tofu import -input=false kubernetes_service.gateway "$NS/mmo-gateway"

tofu import -input=false 'kubernetes_ingress_v1.gateway[0]' "$NS/mmo-gateway-ingress"

tofu import -input=false 'kubernetes_manifest.servicemonitor_gateway[0]' \
  "apiVersion=monitoring.coreos.com/v1,kind=ServiceMonitor,namespace=$NS,name=mmo-gateway-metrics"
tofu import -input=false 'kubernetes_manifest.servicemonitor_cell[0]' \
  "apiVersion=monitoring.coreos.com/v1,kind=ServiceMonitor,namespace=$NS,name=mmo-cell-metrics"
tofu import -input=false 'kubernetes_manifest.servicemonitor_grid_manager[0]' \
  "apiVersion=monitoring.coreos.com/v1,kind=ServiceMonitor,namespace=$NS,name=mmo-grid-manager-metrics"

echo "Готово. Дальше: cd $STAGING && tofu plan"
