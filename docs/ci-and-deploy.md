# CI и деплой (backend)

## Локальная проверка перед выкатом

- **`make test`** или **`go test ./...`** из каталога [`backend`](../).
- Сборка образов и staging: [`scripts/deploy-staging.sh`](../scripts/deploy-staging.sh) (опции `--no-commit`, `--skip-test` — см. справку в скрипте).

### Предусловия полного `tofu apply`

- **Remote state** для каталога [`deploy/terraform/staging`](../deploy/terraform/staging) (как у рабочей станции оператора) или свой backend — иначе `tofu output` / план не совпадут с кластером.
- При **`gateway_ingress_enabled = true`** (дефолт) в каталоге модуля должны быть **`certs/fullchain.pem`** и **`certs/privkey.pem`** (см. `certs/README`). Скрипт деплоя проверяет их до `make tofu-apply`. Иначе отключите Ingress в `*.auto.tfvars` или используйте запасной путь ниже.
- **Harbor:** `make harbor-login` читает `tofu output harbor_*` или переменные **`HARBOR_REGISTRY_HOSTNAME`**, **`HARBOR_DOCKER_USERNAME`**, **`HARBOR_DOCKER_PASSWORD`**. Рецепты Makefile требуют **bash** (`SHELL := /bin/bash`).
- Если учётные данные Harbor **компрометированы** (утечка в логах, чат): в Harbor **отозвать/сменить** robot или пароль, обновить секреты в родительском IAC и при необходимости Secret **`mmo-backend`** / переменные окружения оператора; затем **`docker logout`** и повторный **`make harbor-login`**.

### Первичный импорт в OpenTofu (кластер уже развёрнут, state пустой)

Если **`tofu plan`** предлагает создать ресурсы, которые **уже есть** в **`mmo`**, сначала импортируйте их (иначе **AlreadyExists** или дубликаты):

1. Скопировать [`deploy/terraform/staging/backend.tf.example`](../deploy/terraform/staging/backend.tf.example) → **`backend.tf`**, **`tofu init -reconfigure`**.
2. Убедиться, что при включённом Ingress есть **`certs/fullchain.pem`** и **`certs/privkey.pem`** (или данные из существующего Secret **`mmo-gateway-tls`**).
3. Из корня **`backend`:** **`bash scripts/import-staging-tofu-state.sh`** (переменная **`K8S_IMPORT_NAMESPACE`** при отличии от **`mmo`**).
4. **`tofu plan`**, затем обычный цикл [**`deploy-staging.sh`**](../scripts/deploy-staging.sh).

Если ключи шардов в **`cell_instances`** не **`primary`** / **`child-sw`**, имена Deployment/импорта в скрипте нужно поправить вручную под ваш **`*.auto.tfvars`**.

### Запасной выкат только образа (`kubectl set image`)

Если OpenTofu с этой машины недоступен, но образ уже в registry (например после **`make harbor-push`** с переопределением Harbor через env):

- Namespace: **`mmo`** (см. Terraform). Образ: **`$REGISTRY/library/mmo-backend:$TAG`** (подставьте хост Harbor и тег коммита / `image.auto.tfvars`).
- Имя контейнера в pod **всегда `cell-node`** у каждого Deployment соты; отличаются только имена Deployment.

```bash
NS=mmo
IMG=harbor.example/library/mmo-backend:YOUR_TAG
kubectl -n "$NS" set image deploy/gateway gateway="$IMG"
kubectl -n "$NS" set image deploy/grid-manager grid-manager="$IMG"
kubectl -n "$NS" set image deploy/cell-node cell-node="$IMG"
# Дочерние шарды: имя Deployment = cell-node-<ключ_из_cell_instances>, контейнер = cell-node
kubectl -n "$NS" set image deploy/cell-node-child-sw cell-node="$IMG"
kubectl -n "$NS" rollout status deploy/gateway --timeout=120s
```

После выката: **`bash scripts/staging-verify.sh`** (и при смене схемы БД — убедиться, что Job **`/migrate`** отработал или gateway мигрирует сам).

## Цикл контента, баланса и нагрузки

Типовая итерация после правок данных или API:

1. **`bash scripts/deploy-staging.sh`** (или **`--no-commit`** / **`--skip-test`** по необходимости) из корня **`backend/`**.
2. **`bash scripts/staging-verify.sh`** с **`GATEWAY_PUBLIC_URL`** при нестандартном Ingress.
3. При риске для сессий и gateway: **`make load-smoke`** — переменные **`GATEWAY_PUBLIC_URL`**, **`LOAD_SMOKE_N`**, **`LOAD_SMOKE_J`** (см. раздел ниже).

## GitHub Actions

- Workflow в **суперпроекте** [Saske912/full_mmo](https://github.com/Saske912/full_mmo): [`.github/workflows/backend-ci.yml`](../../.github/workflows/backend-ci.yml) — **`go test ./...`** при изменениях в `backend/**`.
- Репозиторий клонируется **с сабмодулями**: в шаге **`actions/checkout`** задано **`submodules: recursive`**, иначе каталог **`backend/`** может остаться пустым (только gitlink).

## После деплоя gateway (staging / prod с БД)

- Убедиться, что миграции goose применены (Job **`/migrate`** или встроенный путь — по вашей конфигурации **`gateway_skip_db_migrations`**).
- Выполнить **`make verify-readyz-goose`** — [`scripts/verify-gateway-readyz-goose.sh`](../scripts/verify-gateway-readyz-goose.sh): **`GET /readyz`** и заголовок **`X-MMO-Goose-Version`**.
- В **`deploy-staging.sh`** можно включить проверку сразу после apply: **`STAGING_VERIFY_READYZ_GOOSE_AFTER_DEPLOY=1`**.

## Нагрузка и баланс (лёгкая проверка)

- После изменений контента/экономики — обычный цикл: **`scripts/deploy-staging.sh`** → **`scripts/staging-verify.sh`**.
- Для быстрой проверки устойчивости **gateway** к потоку сессий: **`make load-smoke`** — [`scripts/gateway-session-burst`](../scripts/gateway-session-burst/main.go) (параллельные **`POST /v1/session`**). Параметры: **`GATEWAY_PUBLIC_URL`**, **`LOAD_SMOKE_N`** (число запросов), **`LOAD_SMOKE_J`** (параллелизм), при самоподписанном Ingress — **`STAGING_VERIFY_TLS_INSECURE=1`**.
