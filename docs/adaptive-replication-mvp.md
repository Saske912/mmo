# Adaptive Replication MVP (Cell.SubscribeDeltas)

Цель MVP — уменьшить лишний трафик и сохранить отзывчивость рядом с игроком, не меняя protobuf-контракт и Unity-клиент.

## Алгоритм v1 (server-only)

Используется только в AOI-режиме (`viewer_entity_id != 0`) в [`internal/grpc/cellsvc/server.go`](../internal/grpc/cellsvc/server.go):

- `100ms` (`replicationIntervalNear`) — если в AOI есть сущность ближе `15m` к viewer.
- `200ms` (`replicationIntervalDefault`) — если AOI не пустой, но ближних сущностей нет.
- `300ms` (`replicationIntervalFar`) — если кроме viewer никого нет.

Для non-AOI подписок остаётся фикс `200ms`.

Расчёт выполняется функцией [`pickAdaptiveReplicationInterval`](../internal/grpc/cellsvc/aoi_replication.go) на каждом цикле отправки.

## Почему без изменений proto

MVP не добавляет новые поля в `SubscribeDeltasRequest` и не требует обновления Unity submodule. Это безопасный первый шаг перед более гибкой моделью (например policy-параметры в запросе).

## Проверка

- Unit-тест: [`adaptive_interval_test.go`](../internal/grpc/cellsvc/adaptive_interval_test.go).
- Регресс: `go test ./...`.
- E2E smoke: `scripts/staging-verify.sh` (snapshot/delta поток без изменения клиентского контракта).
