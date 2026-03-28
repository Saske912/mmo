.PHONY: proto unity-proto build test print-image-tag print-harbor-image-ref consul-smoke infra-smoke staging-verify verify-readyz-goose staging-image-tfvars staging-tofu-validate docker-build kind-load harbor-login harbor-push tofu-init tofu-plan tofu-apply deploy-staging goose-migrate-job

STAGING_DIR := deploy/terraform/staging
# OpenTofu подхватывает *.auto.tfvars автоматически; приоритет выше, чем у TF_VAR_ — обновлять перед plan/apply.
STAGING_IMAGE_TFVARS := $(STAGING_DIR)/image.auto.tfvars

PROTOC ?= protoc

# Образ привязан к коммиту: short SHA, при изменённом дереве суффикс -dirty. Вне git — unknown.
GIT_SHORT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_DIRTY := $(shell git status --porcelain 2>/dev/null | grep -q . && echo -dirty || true)
IMAGE_TAG ?= $(GIT_SHORT_SHA)$(GIT_DIRTY)
DOCKER_IMAGE ?= mmo-backend:$(IMAGE_TAG)
# Согласовать деплой staging с тем же тегом, что и docker build / harbor-push.
export TF_VAR_image_tag := $(IMAGE_TAG)

PROTO_FILES := proto/game/v1/replication.proto proto/cell/v1/cell.proto

proto:
	$(PROTOC) -I proto $(PROTO_FILES) \
		--go_out=. --go_opt=module=mmo \
		--go-grpc_out=. --go-grpc_opt=module=mmo

# C# для Unity (Google.Protobuf в Unity/Assets/Plugins/GoogleProtobuf).
unity-proto:
	bash scripts/generate-unity-proto.sh

build:
	go build -o bin/grid-manager ./cmd/grid-manager
	go build -o bin/cell-node ./cmd/cell-node
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/mmoctl ./cmd/mmoctl
	go build -o bin/migrate ./cmd/migrate

test:
	go test ./...

# Вывести тег образа (коммит ± -dirty) для CI / ручного TF_VAR_image_tag.
print-image-tag:
	@echo $(IMAGE_TAG)

# Полный reference в Harbor (хост из tofu output). Передайте IMAGE_TAG=... если тег уже зафиксирован в шелле.
print-harbor-image-ref:
	@cd $(STAGING_DIR) && \
		HOST=$$(tofu output -raw harbor_registry_hostname) && \
		printf '%s\n' "$$HOST/$(HARBOR_PROJECT)/$(IMAGE_REPOSITORY):$(IMAGE_TAG)"

# Нужен живой Consul (см. scripts/consul-smoke.sh).
consul-smoke:
	bash scripts/consul-smoke.sh

# Consul + NATS (см. scripts/infra-smoke.sh).
infra-smoke:
	bash scripts/infra-smoke.sh

staging-verify:
	bash scripts/staging-verify.sh

# GET /readyz + заголовок X-MMO-Goose-Version (после Job /migrate или выката gateway).
verify-readyz-goose:
	bash scripts/verify-gateway-readyz-goose.sh

# Пример принудительной пересборки бинарей: DOCKER_BUILD_OPTS=--no-cache make harbor-push
DOCKER_BUILD_OPTS ?=

docker-build:
	docker build $(DOCKER_BUILD_OPTS) --build-arg GIT_REVISION=$(IMAGE_TAG) -t $(DOCKER_IMAGE) .

# kind load docker-image $(DOCKER_IMAGE)
kind-load:
	kind load docker-image $(DOCKER_IMAGE)

# Docker login в Harbor: логин/пароль из outputs.mmo.harbor (через tofu output). Нужен рабочий KUBECONFIG и tofu init в staging.
harbor-login:
	@cd $(STAGING_DIR) && \
		HOST=$$(tofu output -raw harbor_registry_hostname) && \
		USER=$$(tofu output -raw harbor_docker_username) && \
		PASS=$$(tofu output -raw harbor_docker_password) && \
		if [ -z "$$USER" ] || [ -z "$$PASS" ]; then \
			echo "Harbor: пустые учётные данные — проверьте outputs.mmo.harbor в remote state" >&2; exit 1; \
		fi && \
		printf '%s' "$$PASS" | docker login "$$HOST" -u "$$USER" --password-stdin

# Push по тегу коммита (IMAGE_TAG). Не используем tofu output container_image — в state может быть старый тег до apply.
HARBOR_PROJECT ?= library
IMAGE_REPOSITORY ?= mmo-backend

# Сохраняет image_tag для staging (чтобы tofu apply из каталога staging не тянул старый тег).
staging-image-tfvars:
	@{ \
		echo '# Сгенерировано Makefile (harbor-push / tofu-apply); не править вручную.'; \
		printf 'image_tag = "%s"\n' "$(IMAGE_TAG)"; \
	} > "$(STAGING_IMAGE_TFVARS)"
	@echo "== $(STAGING_IMAGE_TFVARS) image_tag=$(IMAGE_TAG) =="

staging-tofu-validate:
	cd $(STAGING_DIR) && tofu validate

harbor-push: docker-build staging-image-tfvars harbor-login
	@cd $(STAGING_DIR) && \
		HOST=$$(tofu output -raw harbor_registry_hostname) && \
		REF="$$HOST/$(HARBOR_PROJECT)/$(IMAGE_REPOSITORY):$(IMAGE_TAG)" && \
		docker tag $(DOCKER_IMAGE) "$$REF" && \
		docker push "$$REF" && \
		echo "Pushed $$REF"

# OpenTofu: модуль staging (Harbor + K8s из remote state)
tofu-init:
	cd $(STAGING_DIR) && tofu init

tofu-plan: staging-image-tfvars
	cd $(STAGING_DIR) && tofu plan

tofu-apply: staging-image-tfvars staging-tofu-validate
	cd $(STAGING_DIR) && tofu apply -input=false -auto-approve

# Миграции staging: gateway_migrations.auto.tfvars + goose-migrate-job в deploy-staging.sh; /readyz → X-MMO-Goose-Version.
#
# Локальный CI/CD: тест → (коммит при изменениях) → harbor-push → tofu-apply. См. scripts/deploy-staging.sh
deploy-staging:
	bash scripts/deploy-staging.sh $(DEPLOY_STAGING_ARGS)

# После harbor-push, до tofu-apply: Job /migrate (нужен gateway_skip_db_migrations=true в TF).
goose-migrate-job:
	bash scripts/goose-migrate-job.sh
