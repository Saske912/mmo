# Сборка образа с бинарями grid-manager и cell-node (staging / kind / ручной push).
# Запуск: command задаётся в Kubernetes (см. deploy/terraform/staging).
FROM golang:1.25-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/grid-manager ./cmd/grid-manager \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cell-node ./cmd/cell-node

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/grid-manager /grid-manager
COPY --from=build /out/cell-node /cell-node
USER nonroot:nonroot
