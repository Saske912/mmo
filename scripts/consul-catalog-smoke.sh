#!/usr/bin/env bash
# Матрица HTTP-запросов к Consul API (план диагностики пустого каталога mmo-cell).
# Запуск из кластера (namespace mmo): передать базовый URL без завершающего слэша.
#
#   CONSUL_HTTP_ADDR=http://mmo-consul-server.consul:8500 bash scripts/consul-catalog-smoke.sh
#   kubectl run ... -- curl ... (см. вывод подсказки)
#
set -euo pipefail

BASE="${CONSUL_HTTP_ADDR:-}"
if [ -z "$BASE" ]; then
  echo "Задайте CONSUL_HTTP_ADDR, например: http://host:8500" >&2
  exit 1
fi
BASE="${BASE%/}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "нужен $1" >&2; exit 1; }; }
need curl

json_len() {
  if command -v jq >/dev/null 2>&1; then
    jq 'length'
  else
    python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'
  fi
}

# GET /v1/catalog/services возвращает объект имя -> теги, не массив.
json_dict_keycount() {
  if command -v jq >/dev/null 2>&1; then
    jq 'keys | length'
  else
    python3 -c 'import json,sys; print(len(json.load(sys.stdin).keys()))'
  fi
}

echo "== GET $BASE/v1/catalog/service/mmo-cell (count) =="
curl -fsS "$BASE/v1/catalog/service/mmo-cell" | json_len

echo "== GET $BASE/v1/health/service/mmo-cell?passing=true (count) =="
curl -fsS "$BASE/v1/health/service/mmo-cell?passing=true" | json_len

echo "== GET $BASE/v1/health/service/mmo-cell (все статусы, count) =="
curl -fsS "$BASE/v1/health/service/mmo-cell" | json_len

echo "== GET $BASE/v1/catalog/services (число имён сервисов) =="
curl -fsS "$BASE/v1/catalog/services" | json_dict_keycount

echo "== GET $BASE/v1/agent/services (локальный агент; только если URL — agent API) =="
if out=$(curl -fsS "$BASE/v1/agent/services" 2>/dev/null); then
  if command -v jq >/dev/null 2>&1; then
    echo "$out" | jq 'keys | length'
  else
    echo "$out" | head -c 2000
    echo
  fi
else
  echo "(недоступно или не agent endpoint — нормально для server-only URL)"
fi

echo "OK"
