#!/usr/bin/env bash
# Проверка staging: поды, grid-manager + cell через port-forward, gateway через Ingress (HTTPS).
# Требуется kubectl. Переопределить URL: GATEWAY_PUBLIC_URL=https://другой.host
# Если TLS ещё не доверен: STAGING_VERIFY_TLS_INSECURE=1
#
# Опционально (после cold-split / нескольких шардов):
#   STAGING_VERIFY_EXPECT_CELL_IDS="cell_0_0_0,cell_-1_-1_1" — все эти id должны быть в list
#   STAGING_VERIFY_RESOLVE_CHECKS="-500,-500,cell_-1_-1_1;500,-500,cell_1_-1_1" — точка x,z → ожидаемый id
set -euo pipefail

NS="${K8S_NAMESPACE:-mmo}"
GM_SVC="${GRID_MANAGER_SVC:-mmo-grid-manager}"
CELL_SVC="${CELL_SVC:-mmo-cell}"
GM_PORT="${GRID_MANAGER_PORT:-9100}"
CELL_PORT="${CELL_GRPC_PORT:-50051}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGING_DIR="${ROOT}/deploy/terraform/staging"
cd "$ROOT"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need go

resolve_gateway_public() {
  if [ -n "${GATEWAY_PUBLIC_URL:-}" ]; then
    printf '%s' "${GATEWAY_PUBLIC_URL%/}"
    return
  fi
  if [ -d "$STAGING_DIR" ] && command -v tofu >/dev/null 2>&1; then
    local raw
    raw="$(cd "$STAGING_DIR" && tofu output -raw gateway_public_url 2>/dev/null || true)"
    if [ -n "$raw" ] && [ "$raw" != "null" ]; then
      printf '%s' "${raw%/}"
      return
    fi
  fi
  printf '%s' "https://mmo.pass-k8s.ru"
}

GATEWAY_PUBLIC="$(resolve_gateway_public)"

curl_public() {
  if [ "${STAGING_VERIFY_TLS_INSECURE:-0}" = 1 ]; then
    curl -fsSk "$@"
  else
    curl -fsS "$@"
  fi
}

echo "== kubectl -n $NS pods =="
kubectl get pods -n "$NS" -o wide

P1=""; P2=""
cleanup() { kill ${P1:-} ${P2:-} 2>/dev/null || true; }
trap cleanup EXIT

kubectl port-forward -n "$NS" "svc/$GM_SVC" "${GM_PORT}:${GM_PORT}" >/dev/null 2>&1 &
P1=$!
kubectl port-forward -n "$NS" "svc/$CELL_SVC" "${CELL_PORT}:${CELL_PORT}" >/dev/null 2>&1 &
P2=$!
sleep 2

echo "== mmoctl list (registry localhost:${GM_PORT}) =="
FIRST_CELL="$(go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" list | head -1 | awk '{print $1}')"
if [ -z "${FIRST_CELL:-}" ]; then
  echo "no cells in registry" >&2
  exit 1
fi
go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" list

echo "== mmoctl forward-update noop (registry -> cell, id=${FIRST_CELL}) =="
go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" forward-update "$FIRST_CELL" noop

echo "== B2: unit-тест каталога (родитель + SW-ребёнок, Resolve) =="
go test ./internal/discovery -run TestResolveMostSpecific_childWinsInSWQuadrant -count=1

echo "== mmoctl resolve (-500,-500) =="
LIST_OUT="$(go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" list)"
RESOLVE_OUT="$(go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" resolve -500 -500)"
echo "$RESOLVE_OUT"
# Если в кластере зарегистрирована дочерняя сота из PlanSplit (пример cell_instances.auto.tfvars.example), точка (-500,-500) должна резолвиться в неё, а не в родителя level=0.
if echo "$LIST_OUT" | grep -qE '^cell_-1_-1_1[[:space:]]'; then
  if ! echo "$RESOLVE_OUT" | grep -qE '^cell_-1_-1_1[[:space:]]'; then
    echo "B2 staging: в каталоге есть cell_-1_-1_1, но resolve (-500,-500) не вернул её" >&2
    echo "list:" >&2
    echo "$LIST_OUT" >&2
    exit 1
  fi
  echo "B2 staging: resolve SW-квадранта в дочернюю соту — OK"
else
  echo "B2 staging: одна сота в каталоге; полный кластерный тест добавьте child_sw в cell_instances (см. deploy/terraform/staging/cell_instances.auto.tfvars.example)"
fi

if [ -n "${STAGING_VERIFY_EXPECT_CELL_IDS:-}" ]; then
  echo "== STAGING_VERIFY_EXPECT_CELL_IDS (все id в list) =="
  while IFS= read -r raw_id; do
    [ -z "$raw_id" ] && continue
    exp_id="$(echo "$raw_id" | tr -d '[:space:]')"
    [ -z "$exp_id" ] && continue
    if ! echo "$LIST_OUT" | grep -qE "^${exp_id}[[:space:]]"; then
      echo "ожидался cell id в каталоге: ${exp_id}" >&2
      echo "$LIST_OUT" >&2
      exit 1
    fi
  done <<< "$(echo "$STAGING_VERIFY_EXPECT_CELL_IDS" | tr ',' '\n')"
  echo "OK: все ожидаемые cell id из STAGING_VERIFY_EXPECT_CELL_IDS присутствуют"
fi

if [ -n "${STAGING_VERIFY_RESOLVE_CHECKS:-}" ]; then
  echo "== STAGING_VERIFY_RESOLVE_CHECKS =="
  TMP_RV="${STAGING_VERIFY_RESOLVE_CHECKS//;/$'\n'}"
  while IFS= read -r triple; do
    [ -z "$triple" ] && continue
    triple="$(echo "$triple" | tr -d '[:space:]')"
    [ -z "$triple" ] && continue
    rx="${triple%%,*}"
    rest="${triple#*,}"
    rz="${rest%%,*}"
    want_id="${rest#*,}"
    if [ "$rx" = "$triple" ] || [ "$rest" = "$rz" ] || [ -z "$want_id" ]; then
      echo "неверная запись (нужно x,z,expected_id): $triple" >&2
      exit 1
    fi
    rline="$(go run ./cmd/mmoctl -registry "127.0.0.1:${GM_PORT}" resolve "$rx" "$rz")"
    echo "resolve ($rx,$rz) -> $rline"
    if ! echo "$rline" | grep -qE "^${want_id}[[:space:]]"; then
      echo "resolve ($rx,$rz): ожидался id ${want_id}" >&2
      exit 1
    fi
  done <<< "$TMP_RV"
  echo "OK: все STAGING_VERIFY_RESOLVE_CHECKS прошли"
fi

echo "== mmoctl ping (cell localhost:${CELL_PORT}) =="
go run ./cmd/mmoctl ping "127.0.0.1:${CELL_PORT}"

echo "== gateway /healthz (Ingress ${GATEWAY_PUBLIC}) =="
if ! curl_public "${GATEWAY_PUBLIC}/healthz" | grep -q ok; then
  echo "gateway healthz failed (${GATEWAY_PUBLIC})" >&2
  exit 1
fi

echo "== ws-smoke (Ingress ${GATEWAY_PUBLIC}, первые кадры) =="
go run ./scripts/ws-smoke -gateway "${GATEWAY_PUBLIC}" -n 3

echo "OK: registry, cell, gateway через Ingress (healthz + ws-smoke) прошли."
