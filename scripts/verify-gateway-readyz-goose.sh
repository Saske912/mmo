#!/usr/bin/env bash
# Проверка gateway после goose-migrate-job / деплоя: тело /readyz = ok и заголовок X-MMO-Goose-Version.
# Использование: GATEWAY_PUBLIC_URL=https://host bash scripts/verify-gateway-readyz-goose.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

STAGING_DIR="${ROOT}/deploy/terraform/staging"

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

curl_pub() {
  if [ "${VERIFY_READYZ_TLS_INSECURE:-0}" = 1 ] || [ "${STAGING_VERIFY_TLS_INSECURE:-0}" = 1 ]; then
    curl -fsSk "$@"
  else
    curl -fsS "$@"
  fi
}

echo "== GET ${GATEWAY_PUBLIC}/readyz (тело + заголовок Goose) =="
BODY="$(curl_pub "${GATEWAY_PUBLIC}/readyz")"
echo "$BODY"
if ! echo "$BODY" | grep -q ok; then
  echo "readyz: ожидалось ok в теле" >&2
  exit 1
fi

HDRS="$(curl_pub -sSI "${GATEWAY_PUBLIC}/readyz" | tr -d '\r')"
GOOSE_LINE="$(echo "$HDRS" | grep -i '^X-MMO-Goose-Version:' || true)"
if [ -z "$GOOSE_LINE" ]; then
  echo "нет заголовка X-MMO-Goose-Version (gateway без goose / только что поднят / пустая БД?)" >&2
  exit 1
fi
echo "$GOOSE_LINE"
echo "OK: readyz + X-MMO-Goose-Version"
