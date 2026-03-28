#!/usr/bin/env bash
# Одноразовый Job mmo-goose-migrate с образом того же тега, что только что запушили (image.auto.tfvars).
# Вызывать после make harbor-push (или make staging-image-tfvars) и до make tofu-apply.
#
#   STAGING_RUN_GOOSE_JOB=1 bash scripts/deploy-staging.sh
#   HARBOR_PROJECT=library IMAGE_REPOSITORY=mmo-backend bash scripts/goose-migrate-job.sh
#
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STAGING_DIR="$ROOT/deploy/terraform/staging"
NS="${K8S_NAMESPACE:-mmo}"
HARBOR_PROJECT="${HARBOR_PROJECT:-library}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-mmo-backend}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "need $1" >&2; exit 1; }; }
need kubectl
need tofu

TAG=""
if [ -f "$STAGING_DIR/image.auto.tfvars" ]; then
  TAG="$(grep -E '^[[:space:]]*image_tag[[:space:]]*=' "$STAGING_DIR/image.auto.tfvars" | head -1 | sed 's/.*"\([^"]*\)".*/\1/')"
fi
if [ -z "$TAG" ]; then
  echo "goose-migrate-job: нет image_tag в $STAGING_DIR/image.auto.tfvars (сделайте make harbor-push или staging-image-tfvars)" >&2
  exit 1
fi

cd "$STAGING_DIR"
HOST="$(tofu output -raw harbor_registry_hostname)"
IMAGE="${HOST}/${HARBOR_PROJECT}/${IMAGE_REPOSITORY}:${TAG}"

echo "== goose Job image: $IMAGE (tag from image.auto.tfvars) =="
kubectl delete job -n "$NS" mmo-goose-migrate --ignore-not-found=true
sleep 2

kubectl apply -n "$NS" -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: mmo-goose-migrate
  namespace: ${NS}
spec:
  ttlSecondsAfterFinished: 86400
  backoffLimit: 2
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: ${IMAGE}
          imagePullPolicy: Always
          command: ["/migrate"]
          envFrom:
            - secretRef:
                name: mmo-backend
EOF

echo "== kubectl wait job/mmo-goose-migrate (complete, 300s) =="
kubectl wait --for=condition=complete "job/mmo-goose-migrate" -n "$NS" --timeout=300s
echo "OK: goose migrate Job завершился"
