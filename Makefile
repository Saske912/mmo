.PHONY: proto build test consul-smoke infra-smoke

PROTOC ?= protoc

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
