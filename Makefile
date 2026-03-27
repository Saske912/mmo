.PHONY: proto build test consul-smoke infra-smoke docker-build kind-load harbor-login harbor-push tofu-init tofu-plan tofu-apply

STAGING_DIR := deploy/terraform/staging

PROTOC ?= protoc
DOCKER_IMAGE ?= mmo-backend:local

proto:
	$(PROTOC) -I proto proto/cell/v1/cell.proto \
		--go_out=. --go_opt=module=mmo \
		--go-grpc_out=. --go-grpc_opt=module=mmo

build:
	go build -o bin/grid-manager ./cmd/grid-manager
	go build -o bin/cell-node ./cmd/cell-node
	go build -o bin/mmoctl ./cmd/mmoctl

test:
	go test ./...

# Нужен живой Consul (см. scripts/consul-smoke.sh).
consul-smoke:
	bash scripts/consul-smoke.sh

# Consul + NATS (см. scripts/infra-smoke.sh).
infra-smoke:
	bash scripts/infra-smoke.sh

docker-build:
	docker build -t $(DOCKER_IMAGE) .

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

# Сборка, тег по container_image из staging (Harbor pass-k8s по умолчанию), push.
harbor-push: docker-build harbor-login
	@cd $(STAGING_DIR) && \
		REF=$$(tofu output -raw container_image) && \
		docker tag $(DOCKER_IMAGE) "$$REF" && \
		docker push "$$REF" && \
		echo "Pushed $$REF"

# OpenTofu: модуль staging (Harbor + K8s из remote state)
tofu-init:
	cd $(STAGING_DIR) && tofu init

tofu-plan:
	cd $(STAGING_DIR) && tofu plan

tofu-apply:
	cd $(STAGING_DIR) && tofu apply
