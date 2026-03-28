# Cold-path: четверичный сплит соты (без live-handoff)

Операторская процедура для первого прохода сплита: дети в каталоге (Consul) вытесняют родителя по `ResolveMostSpecific` там, где у ребёнка больший `level` и те же границы покрытия (см. B2 в чеклисте). **Автоматической смены соты в gateway нет:** WebSocket резолвит цель **один раз** при подключении (`-resolve-x` / `-resolve-z`). Клиентам нужен краткий простой или **ручной реконнект** после перевода трафика.

## Предпосылки

- Родительский `cell-node` уже в кластере с корректными `bounds` и `level` в Consul (как в `cell_instances`).
- Redis: ключи `mmo:cell:<cell_id>:state` на дочерних сотах при первом старте будут пустыми — **чистый мир**. Копирование снапшота родителя в ключ ребёнка — только вручную и осознанно (игроки в снапшот не входят).

## 1. План четырёх детей

Живой родитель (как у cell gRPC):

```bash
mmoctl plansplit <родитель_host:port>
```

Офлайн-сверка по тем же правилам, что и `PlanSplit`, без вызова соты:

```bash
mmoctl partition-plan -id cell_0_0_0 -level 0 \
  -xmin -1000 -xmax 1000 -zmin -1000 -zmax 1000
mmoctl partition-plan ... -format json
mmoctl partition-plan ... -tfvars-skeleton   # каркас блоков под cell_instances
```

Сравните `id` / `level` / `bounds` с будущими записями в `cell_instances` (см. [deploy/terraform/staging/cell_instances.auto.tfvars.example](../deploy/terraform/staging/cell_instances.auto.tfvars.example)).

## 2. Инфраструктура: дочерние соты

- Добавьте детей в map `cell_instances` (OpenTofu staging): отдельный Deployment/Service на шард, свой `MMO_CELL_GRPC_ADVERTISE`, те же `id`/`bounds`/`level`, что в плане. **Ключ шарда в map** должен быть в форме RFC 1123 (например `child-sw`), без `_` — иначе Kubernetes отклонит имя Deployment/Service.
- `tofu apply` / выкат образов.

## 3. Пока в каталоге есть родитель и дети

`Resolve` в квадрантах детей должен выбирать **ребёнка** (более глубокий `level`). Проверка локально/через port-forward:

```bash
mmoctl -registry <grid-manager:9100> list
mmoctl -registry <grid-manager:9100> resolve -500 -500
```

В staging: [scripts/staging-verify.sh](../scripts/staging-verify.sh) и опционально `STAGING_VERIFY_EXPECT_CELL_IDS`, `STAGING_VERIFY_RESOLVE_CHECKS` (см. комментарии в скрипте).

## 4. Игроки и gateway

- Запланируйте окно: отключить клиентов или предупредить о реконнекте.
- После того как дети в Consul и `resolve` корректен для целевых координат, новые WebSocket-сессии пойдут на правильный endpoint (при согласованных флагах gateway с позицией игрока).

## 5. Вывод родителя (cold)

1. Убедитесь, что нагрузка не должна оставаться на родителе (все нужные точки покрыты детьми в каталоге).
2. Остановите родительский `cell-node` **gracefully** (SIGTERM): при `CONSUL_HTTP_ADDR` выполнится `ServiceDeregister` по составному id pod (см. [cmd/cell-node/main.go](../cmd/cell-node/main.go)).
3. При необходимости удалите родителя из `cell_instances` и примените Terraform, чтобы не поднимать разорванный шард снова.

## 6. Экспорт NPC для ручного переноса (MVP)

После **`split_drain`** и осознанного окна можно снять снапшот **только NPC** (как при persist в Redis, без игроков) в JSON **`game.v1.CellPersist`**:

```bash
mmoctl -registry <grid-manager:9100> forward-update <parent_cell_id> export-npc-persist "handoff-ticket"
```

Ответ **`ForwardCellUpdate`** содержит **`npc_export_json`** (на стороне grid-manager то же поле проксируется). Оператор может положить полезную нагрузку в ключ Redis дочерней соты `mmo:cell:<child_id>:state` или использовать для отладки. Полный live-handoff без ручных шагов — вне cold-path.

**Импорт на дочерней соте** (без ручного `redis-cli`): при **отсутствии подключённых игроков** на целевой соте тот же JSON применяется через **`Cell.Update` → `import_npc_persist`** (восстановление как при `snapshot.Decode`). Через registry:

```bash
mmoctl -registry <grid-manager:9100> forward-update <child_cell_id> import-npc-persist handoff.json "handoff-ticket"
# или stdin:
cat parent_export.json | mmoctl -registry ... forward-update <child_cell_id> import-npc-persist -
```

Если на соте есть живые сессии (**`PlayerCount` > 0**), импорт отклоняется — сначала освободите мир (cold-path).

Операторский **dry-run** (каталог → прямой gRPC списка кандидатов + экспорт через registry):

```bash
mmoctl -registry <grid-manager:9100> migration-dry-run <cell_id>
```

## Вне этой процедуры

- Автоматический live-migrate NPC между сотами без операторских шагов.
- Автоматический redirect игрока в gateway при смене покрытия.
- Один Registry RPC `Unregister` для путей без Consul — не требуется, если сота сама снимает регистрацию при shutdown.
