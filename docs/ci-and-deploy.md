# CI и деплой (backend)

## Локальная проверка перед выкатом

- **`make test`** или **`go test ./...`** из каталога [`backend`](../).
- Сборка образов и staging: [`scripts/deploy-staging.sh`](../scripts/deploy-staging.sh) (опции `--no-commit`, `--skip-test` — см. справку в скрипте).

## GitHub Actions

- Workflow в **суперпроекте** [Saske912/full_mmo](https://github.com/Saske912/full_mmo): [`.github/workflows/backend-ci.yml`](../../.github/workflows/backend-ci.yml) — **`go test ./...`** при изменениях в `backend/**`.

## После деплоя gateway (staging / prod с БД)

- Убедиться, что миграции goose применены (Job **`/migrate`** или встроенный путь — по вашей конфигурации **`gateway_skip_db_migrations`**).
- Выполнить **`make verify-readyz-goose`** — [`scripts/verify-gateway-readyz-goose.sh`](../scripts/verify-gateway-readyz-goose.sh): **`GET /readyz`** и заголовок **`X-MMO-Goose-Version`**.
- В **`deploy-staging.sh`** можно включить проверку сразу после apply: **`STAGING_VERIFY_READYZ_GOOSE_AFTER_DEPLOY=1`**.

## Нагрузка и баланс (лёгкая проверка)

- После изменений контента/экономики — обычный цикл: **`scripts/deploy-staging.sh`** → **`scripts/staging-verify.sh`**.
- Для быстрой проверки устойчивости **gateway** к потоку сессий: **`make load-smoke`** — [`scripts/gateway-session-burst`](../scripts/gateway-session-burst/main.go) (параллельные **`POST /v1/session`**). Параметры: **`GATEWAY_PUBLIC_URL`**, **`LOAD_SMOKE_N`** (число запросов), **`LOAD_SMOKE_J`** (параллелизм), при самоподписанном Ingress — **`STAGING_VERIFY_TLS_INSECURE=1`**.
