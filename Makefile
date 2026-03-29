.PHONY: proto unity-proto build test print-image-tag print-harbor-image-ref consul-smoke infra-smoke staging-verify load-smoke verify-readyz-goose staging-image-tfvars staging-tofu-validate docker-build kind-load harbor-login harbor-push tofu-init tofu-plan tofu-apply deploy-staging goose-migrate-job web3-indexer-ingest-smoke split-e2e-smoke merge-e2e-smoke

# harbor-login и др. рецепты используют bash (подстановки ${var//…}, [[ … ]]).
SHELL := /bin/bash

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
	go build -o bin/cell-controller ./cmd/cell-controller
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/mmoctl ./cmd/mmoctl
	go build -o bin/migrate ./cmd/migrate
	go build -o bin/web3-indexer ./cmd/web3-indexer

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

# Лёгкая нагрузка: параллельные POST /v1/session (нагрузка на gateway, не на cell). Переопределить: GATEWAY_PUBLIC_URL N J
load-smoke:
	go run ./scripts/gateway-session-burst -gateway "$${GATEWAY_PUBLIC_URL:-https://mmo.pass-k8s.ru}" -n $${LOAD_SMOKE_N:-80} -j $${LOAD_SMOKE_J:-16}

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

# Docker login в Harbor: удалённый state (tofu output) или переопределение окружения:
#   HARBOR_REGISTRY_HOSTNAME  HARBOR_DOCKER_USERNAME  HARBOR_DOCKER_PASSWORD
# Сырое значение output принимается только если похоже на хост/логин (без ANSI и мусора от «No outputs»).
# Docker login в Harbor: логин/пароль из outputs.mmo.harbor (через tofu output). Нужен рабочий KUBECONFIG и tofu init в staging.
harbor-login:
	@cd $(STAGING_DIR) && \
		_raw_host=$$(tofu output -raw harbor_registry_hostname 2>/dev/null || true); \
		_raw_host=$${_raw_host//$$'\r'/}; \
		_raw_host=$${_raw_host//$$'\n'/}; \
		_raw_user=$$(tofu output -raw harbor_docker_username 2>/dev/null || true); \
		_raw_user=$${_raw_user//$$'\r'/}; \
		_raw_user=$${_raw_user//$$'\n'/}; \
		_raw_pass=$$(tofu output -raw harbor_docker_password 2>/dev/null || true); \
		_raw_pass=$${_raw_pass//$$'\r'/}; \
		_raw_pass=$${_raw_pass//$$'\n'/}; \
		if [ -n "$$HARBOR_REGISTRY_HOSTNAME" ]; then HOST="$$HARBOR_REGISTRY_HOSTNAME"; \
		elif [ -n "$$_raw_host" ] && [[ "$$_raw_host" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]+$$ ]]; then HOST="$$_raw_host"; \
		else HOST=""; fi; \
		if [ -n "$$HARBOR_DOCKER_USERNAME" ]; then USER="$$HARBOR_DOCKER_USERNAME"; \
		elif [ -n "$$_raw_user" ] && [[ "$$_raw_user" =~ ^[[:graph:]]+$$ ]]; then USER="$$_raw_user"; \
		else USER=""; fi; \
		if [ -n "$$HARBOR_DOCKER_PASSWORD" ]; then PASS="$$HARBOR_DOCKER_PASSWORD"; \
		elif [ -n "$$_raw_pass" ]; then PASS="$$_raw_pass"; \
		else PASS=""; fi; \
		if [ -z "$$HOST" ] || [ -z "$$USER" ] || [ -z "$$PASS" ]; then \
			echo "Harbor: задайте HARBOR_REGISTRY_HOSTNAME, HARBOR_DOCKER_USERNAME, HARBOR_DOCKER_PASSWORD или проверьте remote state (tofu output harbor_*)" >&2; exit 1; \
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
		HOST="$${HARBOR_REGISTRY_HOSTNAME:-}"; \
		if [ -z "$$HOST" ]; then \
			_raw=$$(tofu output -raw harbor_registry_hostname 2>/dev/null || true); \
			_raw=$${_raw//$$'\r'/}; _raw=$${_raw//$$'\n'/}; \
			if [ -n "$$_raw" ] && [[ "$$_raw" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]+$$ ]]; then HOST="$$_raw"; fi; \
		fi; \
		if [ -z "$$HOST" ]; then echo "Harbor push: задайте HARBOR_REGISTRY_HOSTNAME или remote state с harbor_registry_hostname" >&2; exit 1; fi; \
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

# Смок ingest web3-indexer + проверка mmo_chain_tx_event (нужны -indexer-url и -database-url или env).
web3-indexer-ingest-smoke:
	go run ./scripts/web3-indexer-ingest-smoke $(WEB3_INDEXER_SMOKE_ARGS)

# Auto split workflow smoke: rehearsal + workflow metrics.
split-e2e-smoke:
	bash scripts/grid-auto-split-e2e.sh

merge-e2e-smoke:
	bash scripts/grid-merge-e2e.sh
