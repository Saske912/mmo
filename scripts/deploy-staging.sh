#!/usr/bin/env bash
# Локальный «одна команда»: при необходимости коммит → тесты → образ в Harbor → tofu apply в staging.
# Требуются: docker, git (опционально), kubectl/kubeconfig, make, cd в корень репозитория.
#
#   ./scripts/deploy-staging.sh
#   ./scripts/deploy-staging.sh "fix: gateway flags"
#   ./scripts/deploy-staging.sh --no-commit
#   ./scripts/deploy-staging.sh --no-cache -- "rebuild binaries"
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
  --skip-test     не запускать go test ./...
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
# Один и тот же тег для push и tofu (без IMAGE_TAG= каждый make пересчитывает git и теоретически может разъехаться).
echo "== make harbor-push =="
make harbor-push IMAGE_TAG="$TAG"

echo "== check manifest in Harbor (после login из harbor-push) =="
HARBOR_REF="$(make -s print-harbor-image-ref IMAGE_TAG="$TAG")"
if ! docker manifest inspect "$HARBOR_REF" >/dev/null 2>&1; then
  echo "образ не читается из Harbor: $HARBOR_REF (проверь push, логин и право robot на pull/manifest)" >&2
  exit 1
fi

echo "== make tofu-apply =="
make tofu-apply IMAGE_TAG="$TAG"

echo ""
echo "Готово. Образ: harbor (тег $TAG). Проверка: bash scripts/staging-verify.sh"
