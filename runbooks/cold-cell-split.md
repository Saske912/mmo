# Cold-path: четверичный сплит соты (без live-handoff)

Операторская процедура для первого прохода сплита: дети в каталоге (Consul) вытесняют родителя по `ResolveMostSpecific` там, где у ребёнка больший `level` и те же границы покрытия (см. B2 в чеклисте). **Автоматической смены соты в gateway нет:** WebSocket резолвит цель **один раз** при подключении (`-resolve-x` / `-resolve-z`). Клиентам нужен краткий простой или **ручной реконнект** после перевода трафика.

## Предпосылки

- Родительский `cell-node` уже в кластере с корректными `bounds` и `level` в Consul (как в `cell_instances`).
- Redis: ключи `mmo:cell:<cell_id>:state` на дочерних сотах при первом старте будут пустыми — **чистый мир**. Копирование снапшота родителя в ключ ребёнка — только вручную и осознанно (игроки в снапшот не входят).

**Авто-workflow (`MMO_GRID_AUTO_SPLIT_WORKFLOW`):** после успешных handoff grid-manager публикует **`retire_ready`**, затем по умолчанию выполняет **post-handoff orchestration**: префлайт каталога и переход Redis к **`phase=automation_complete`** (см. [`cells-migration-workflow.md`](../docs/cells-migration-workflow.md)). Снятие флага: **`MMO_GRID_AUTO_POST_HANDOFF_ORCHESTRATION=false`**. Полный **§5** (вывод baseline parent из каталога / deregister / Terraform) остаётся **операторским** после `next_action=operator_final_retire`. Удаление только runtime child — опционально **`MMO_GRID_SPLIT_TEARDOWN_RUNTIME_CHILDREN`**. Про следующий шаг автоматизации tail после `automation_complete`: [`rfc-post-automation-retire-tail.md`](../docs/rfc-post-automation-retire-tail.md).

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
- Если **`mmo_player_last_cell`** не совпадает с сотой, которую даёт **`ResolvePosition`** для координат из JWT сессии, **`GET /v1/ws`** возвращает **409** с заголовками **`X-MMO-Last-Cell-Id`** / **`X-MMO-Resolved-Cell-Id`** и **JSON-телом** (`error: cell_handoff_required`, поля **`last_cell`**, **`resolved`**, **`session_resolve_x/z`**, **`hint`**) — клиент может вызвать **`POST /v1/session`** с новыми **`resolve_x`/`resolve_z`** и переподнять WebSocket без разбора текста ошибки.

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

**Один вызов registry (export → import):** RPC **`Registry.ForwardNpcHandoff`** выполняет оба шага на стороне grid-manager без временного файла у оператора:

```bash
mmoctl -registry <grid-manager:9100> forward-npc-handoff <parent_cell_id> <child_cell_id> "handoff-ticket"
```

Из пода: `./scripts/mmoctl-in-cluster.sh -registry 127.0.0.1:9100 forward-npc-handoff cell_0_0_0 cell_-1_-1_1 ticket`.

Операторский **dry-run** (каталог → прямой gRPC списка кандидатов на **cell** + экспорт через registry):

```bash
mmoctl -registry <grid-manager:9100> migration-dry-run <cell_id>
```

`ListMigrationCandidates` вызывается **напрямую** по `grpc_endpoint` из каталога (обычно `*.svc.cluster.local`). С машины за пределами кластера без split-DNS адрес соты не резолвится. Запуск **из пода с cluster DNS** (после выката образа с бинарём **`/mmoctl`**):

```bash
# из корня репозитория, kubectl в контекст staging:
./scripts/mmoctl-in-cluster.sh -registry 127.0.0.1:9100 migration-dry-run <cell_id>
./scripts/mmoctl-in-cluster.sh -registry 127.0.0.1:9100 list
```

Эквивалент вручную: `kubectl exec -n mmo deploy/grid-manager -- /mmoctl -registry 127.0.0.1:9100 migration-dry-run <cell_id>`. В смоуке staging: **`STAGING_VERIFY_MIGRATION_DRY_RUN=incluster`** (см. `scripts/staging-verify.sh`).

### Регрессия staging (смоук + handoff, без §5 вывода родителя)

Полный автоматический прогон каталога (в т.ч. export/split-drain, B2 resolve, gateway, ws-smoke) и in-cluster **`migration-dry-run`** — **`scripts/staging-verify.sh`**. После успешного смоука при желании проверить **`ForwardNpcHandoff`**:

```bash
cd backend
export GATEWAY_PUBLIC_URL=https://<ingress>   # если `tofu output gateway_public_url` не задан
STAGING_VERIFY_MIGRATION_DRY_RUN=incluster \
STAGING_VERIFY_EXPECT_CELL_IDS="cell_0_0_0,cell_-1_-1_1" \
STAGING_VERIFY_RESOLVE_CHECKS="-500,-500,cell_-1_-1_1" \
STAGING_VERIFY_READYZ_GOOSE_HEADER=1 \
./scripts/staging-verify.sh

PARENT=cell_0_0_0 CHILD=cell_-1_-1_1 TICKET="regression-$(date +%s)" MODE=incluster \
  ./scripts/run-forward-npc-handoff.sh
```

Подставьте реальные `cell_id`, если в **`cell_instances`** другие имена шардов.

## 7. Операторский пайплайн (drain → handoff → инфра)

Сжатый указатель команд и регрессии: [`docs/cells-migration-workflow.md`](../docs/cells-migration-workflow.md).

Краткая последовательность без «ручного копирования Redis», когда дочерние соты уже в каталоге:

1. **Окно и drain:** `forward-update <parent> set-split-drain true` (новые **Join** на родителе отклоняются); дождаться выхода игроков или предупредить о реконнекте (cold-path).
2. **Кандидаты (опционально):** `migration-dry-run` / `ListMigrationCandidates` на родителе — сверка сущностей.
3. **Перенос NPC одним вызовом:** `mmoctl … forward-npc-handoff <parent_cell_id> <child_cell_id> "<ticket>"` (см. §6).
4. **Инфра:** при появлении новых шардов — обновить `cell_instances` в OpenTofu, `tofu apply`; **drain off** на родителе перед выводом: `set-split-drain false`.
5. **Клиенты:** новые сессии с **resolve** в зоне ребёнка идут на дочернюю соту; уже открытый WebSocket нужно переподнять при смене покрытия (см. §4).

### Авто `split_drain` от grid-manager (load policy)

При **`MMO_GRID_AUTO_SPLIT_DRAIN=true`** на **grid-manager** устойчивое нарушение порогов probe (`MMO_GRID_THRESHOLD_*`) может автоматически вызвать **`Cell.Update(set_split_drain=true)`** на соте; метрика действия — **`mmo_grid_manager_load_policy_actions_total`** с `action="split_drain_enable"`. Включение контролируемо через Terraform: **`grid_manager_extra_env`** ([пример `grid_manager.auto.tfvars.example`](../deploy/terraform/staging/grid_manager.auto.tfvars.example)); после правок **`grid_manager.auto.tfvars`** обязателен **`tofu apply`** из `deploy/terraform/staging` ([README](../deploy/terraform/staging/README.md)), иначе кластер разойдётся с git. Репетиция на staging и проверка Join/runbook: [`docs/grid-auto-split-drain-staging.md`](../docs/grid-auto-split-drain-staging.md) (раздел **«Чеклист оператора после автоматического split_drain»**), скрипт [`scripts/grid-auto-split-drain-rehearsal.sh`](../scripts/grid-auto-split-drain-rehearsal.sh). После инцидента тот же пайплайн §7 (handoff), затем **`split-drain false`** на соте.

**Скрипт-обёртка:** из корня backend задайте `PARENT`, `CHILD` и при необходимости `MODE=incluster` — [`scripts/run-forward-npc-handoff.sh`](../scripts/run-forward-npc-handoff.sh) (локально: `go run mmoctl`; в кластере: `kubectl exec … /mmoctl`).

Подсказка для клиента после сессии с БД: **GET** `https://<gateway>/v1/me/last-cell` (JWT) — последние **`cell_id`** / **`resolve_x,z`** из `mmo_player_last_cell` для реконнекта без угадывания координат.

## 8. После изменений БД, контента или handoff

1. **Миграции:** при **`gateway_skip_db_migrations`** на gateway убедитесь, что Job **`/migrate`** (или эквивалент) применён до проверок; заголовок **`X-MMO-Goose-Version`** на **`GET /readyz`** — см. [`ci-and-deploy.md`](../docs/ci-and-deploy.md), **`make verify-readyz-goose`**.
2. **Регрессия кластера:** **`scripts/staging-verify.sh`** с при необходимости **`STAGING_VERIFY_MIGRATION_DRY_RUN=incluster`**, **`STAGING_VERIFY_EXPECT_CELL_IDS`**, **`STAGING_VERIFY_RESOLVE_CHECKS`**, **`STAGING_VERIFY_READYZ_GOOSE_HEADER=1`**.
3. **Только что выполнили handoff NPC:** при желании **`MODE=incluster`** — [`scripts/run-forward-npc-handoff.sh`](../scripts/run-forward-npc-handoff.sh) как дым после изменений образа или каталога.
4. **Нагрузка на gateway:** **`make load-smoke`** из корня `backend/` после затратных изменений в сессии/БД.

## Вне этой процедуры

- Live-migrate **игроков** и автоматический redirect в gateway при смене покрытия (сейчас — только реконнект клиента).
- Один Registry RPC `Unregister` для путей без Consul — не требуется, если сота сама снимает регистрацию при shutdown.
- Scale-in merge/unsplit: отдельный эпик, см. [`adr-merge-unsplit.md`](../docs/adr-merge-unsplit.md).

### Merge handoff smoke (MVP)

Для staging-проверки ручного merge handoff (children -> parent, без auto teardown child):

```bash
cd backend && make merge-e2e-smoke
```

Скрипт: [`scripts/grid-merge-e2e.sh`](../scripts/grid-merge-e2e.sh).
