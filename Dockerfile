# Сборка образа с бинарями grid-manager, cell-node и gateway (staging / kind / ручной push).
# Запуск: command задаётся в Kubernetes (см. deploy/terraform/staging).
# GIT_REVISION: make docker-build передаёт --build-arg (тег = коммит).
FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/grid-manager ./cmd/grid-manager \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cell-node ./cmd/cell-node \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cell-controller ./cmd/cell-controller \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mmoctl ./cmd/mmoctl \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/web3-indexer ./cmd/web3-indexer

FROM gcr.io/distroless/static-debian12:nonroot
ARG GIT_REVISION=
LABEL org.opencontainers.image.revision="${GIT_REVISION}"
COPY --from=build /out/grid-manager /grid-manager
COPY --from=build /out/cell-node /cell-node
COPY --from=build /out/cell-controller /cell-controller
COPY --from=build /out/gateway /gateway
COPY --from=build /out/mmoctl /mmoctl
COPY --from=build /out/migrate /migrate
COPY --from=build /out/web3-indexer /web3-indexer
USER nonroot:nonroot
