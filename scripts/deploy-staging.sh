#!/usr/bin/env bash
# Локальный «одна команда»: при необходимости коммит → тесты → образ в Harbor → tofu apply в staging.
# Требуются: docker, git (опционально), kubectl/kubeconfig, make, cd в корень репозитория.
#
#   ./scripts/deploy-staging.sh
#   ./scripts/deploy-staging.sh "fix: gateway flags"
#   ./scripts/deploy-staging.sh --no-commit
#   ./scripts/deploy-staging.sh --no-cache -- "rebuild binaries"
#
# Job-only DDL (staging): deploy/terraform/staging/gateway_migrations.auto.tfvars → gateway_skip_db_migrations = true.
# Между harbor-push и tofu-apply автоматически вызывается scripts/goose-migrate-job.sh (отключить: STAGING_RUN_GOOSE_JOB=0).
#
# Тег образа пишется в deploy/terraform/staging/image.auto.tfvars (см. Makefile staging-image-tfvars),
# чтобы tofu plan/apply без ручного TF_VAR_image_tag совпадали с последним деплоем.
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

NO_COMMIT=0
NO_CACHE=0
SKIP_TEST=0
COMMIT_MSG=""

usage() {
  cat >&2 <<'EOF'
Использование: scripts/deploy-staging.sh [опции] [сообщение коммита]

Опции:
  --no-commit     не делать git add/commit
  --no-cache      docker build --no-cache
  --skip-test     не запускать go test ./... и tofu validate
  -h, --help      справка

Сообщение коммита — последние аргументы или всё после « -- ».
EOF
  exit "${1:-0}"
}

ARGS=()
while [ $# -gt 0 ]; do
  case "$1" in
    -h | --help) usage 0 ;;
    --no-commit) NO_COMMIT=1; shift ;;
    --no-cache) NO_CACHE=1; shift ;;
    --skip-test) SKIP_TEST=1; shift ;;
    --)
      shift
      COMMIT_MSG=$(printf '%s ' "$@" | sed 's/[[:space:]]*$//')
      break
      ;;
    *)
      ARGS+=("$1")
      shift
      ;;
  esac
done

if [ ${#ARGS[@]} -gt 0 ]; then
  COMMIT_MSG="${ARGS[*]}"
fi
if [ -z "$COMMIT_MSG" ]; then
  COMMIT_MSG="staging deploy $(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "нужна команда: $1" >&2; exit 1; }; }
need make
need docker

if [ "$NO_CACHE" = 1 ]; then
  export DOCKER_BUILD_OPTS=--no-cache
fi

if [ "$SKIP_TEST" = 0 ]; then
  echo "== go test ./... =="
  go test ./...
  echo "== make staging-tofu-validate =="
  make staging-tofu-validate
fi

if [ "$NO_COMMIT" = 0 ] && git rev-parse --git-dir >/dev/null 2>&1; then
  if git diff --quiet && git diff --cached --quiet; then
    echo "== git: рабочее дерево чистое, commit пропущен =="
  else
    echo "== git: commit =="
    git add -A
    git commit -m "$COMMIT_MSG"
  fi
elif [ "$NO_COMMIT" = 0 ]; then
  echo "== git: не репозиторий — commit пропущен ==" >&2
fi

echo "== make print-image-tag =="
TAG="$(make -s print-image-tag)"
echo "IMAGE_TAG=$TAG"

echo "== make harbor-push (= docker build + image.auto.tfvars + harbor login/push) =="
make harbor-push

# При gateway_skip_db_migrations в *.auto.tfvars по умолчанию гоняем Job до apply (иначе gateway без goose не поднимет схему).
STAGING_DIR="$ROOT/deploy/terraform/staging"
if [ "${STAGING_RUN_GOOSE_JOB:-}" = "" ]; then
  if grep -rqE '^[[:space:]]*gateway_skip_db_migrations[[:space:]]*=[[:space:]]*true[[:space:]]*$' "$STAGING_DIR"/*.auto.tfvars 2>/dev/null; then
    STAGING_RUN_GOOSE_JOB=1
  else
    STAGING_RUN_GOOSE_JOB=0
  fi
fi

if [ "$STAGING_RUN_GOOSE_JOB" = 1 ] || [ "$STAGING_RUN_GOOSE_JOB" = true ]; then
  echo "== goose-migrate-job (STAGING_RUN_GOOSE_JOB=${STAGING_RUN_GOOSE_JOB}; до выката gateway) =="
  bash scripts/goose-migrate-job.sh
fi

echo "== make tofu-apply (= staging-tofu-validate + apply) =="
make tofu-apply

if [ "${STAGING_VERIFY_READYZ_GOOSE_AFTER_DEPLOY:-0}" = 1 ] || [ "${STAGING_VERIFY_READYZ_GOOSE_AFTER_DEPLOY:-}" = "yes" ]; then
  echo "== verify-gateway-readyz-goose (STAGING_VERIFY_READYZ_GOOSE_AFTER_DEPLOY) =="
  bash scripts/verify-gateway-readyz-goose.sh
fi

echo ""
echo "Готово. Образ: harbor (тег $TAG, зафиксирован в deploy/terraform/staging/image.auto.tfvars)."
echo "Проверка: bash scripts/staging-verify.sh"
echo "Миграции (staging): Job /migrate перед apply при gateway_migrations.auto.tfvars; gateway — GATEWAY_SKIP_DB_MIGRATIONS."
echo "  Проверка: GET …/readyz → X-MMO-Goose-Version (make verify-readyz-goose или STAGING_VERIFY_READYZ_GOOSE_AFTER_DEPLOY=1)."
echo "  Отключить Job: STAGING_RUN_GOOSE_JOB=0"
