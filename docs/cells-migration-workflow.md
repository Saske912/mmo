# Соты: migration-dry-run, handoff, регрессия (ссылка на runbook)

Операторский поток **cold-path** и проверки после изменений собраны в [runbooks/cold-cell-split.md](../runbooks/cold-cell-split.md). Ниже — сжатый указатель без дублирования длинных процедур.

## Preflight (перед регрессией п.3 / staging)

- **`kubectl`**: контекст с namespace **`mmo`**; **`grid-manager`**, **`gateway`**, родительский и дочерний **`cell-node`** в `Running` (см. вывод `staging-verify.sh`).
- **`PARENT` / `CHILD`**: сверить с фактическим каталогом и с [cell_instances в Terraform](../deploy/terraform/staging/cell_instances.auto.tfvars) (пример staging: `cell_root` → `cell_q0`).
- **`migration-dry-run`**: для вызова **ListMigrationCandidates** на соте нужен cluster DNS; с хоста вне кластера gRPC endpoint соты часто не резолвится — используйте **`STAGING_VERIFY_MIGRATION_DRY_RUN=incluster`** в [`staging-verify.sh`](../scripts/staging-verify.sh) или [`mmoctl-in-cluster.sh`](../scripts/mmoctl-in-cluster.sh).
- **Импорт на CHILD** (`forward-npc-handoff` / `import_npc_persist`): при живых сессиях на целевой соте импорт отклоняется — см. runbook §6.
- **Gateway / Ingress**: при нестандартном URL — **`GATEWAY_PUBLIC_URL`**; самоподписанный TLS — **`STAGING_VERIFY_TLS_INSECURE=1`** (см. комментарии в `staging-verify.sh`).

## §7 Операторский пайплайн (drain → handoff → инфра)

При **`MMO_GRID_AUTO_SPLIT_WORKFLOW=true`** перенос NPC и post-handoff (**`automation_complete`** в Redis) выполняет grid-manager; блок ниже — **ручной fallback** (cold-path без авто-workflow или донастройка после `operator_final_retire`).

Последовательность из runbook:

1. **`set-split-drain true`** на родителе — запрет новых Join.
2. Опционально **`migration-dry-run`** / **`ListMigrationCandidates`** — сверка сущностей (прямой gRPC к соте, нужен cluster DNS или [scripts/mmoctl-in-cluster.sh](../scripts/mmoctl-in-cluster.sh)).
3. **`mmoctl forward-npc-handoff`** — перенос NPC одним вызовом (§6 runbook).
4. OpenTofu: обновить **`cell_instances`**, **`tofu apply`**; **`set-split-drain false`** перед выводом родителя.
5. Клиенты: реконнект WS при смене покрытия.

Обёртка: [scripts/run-forward-npc-handoff.sh](../scripts/run-forward-npc-handoff.sh) (`PARENT`, `CHILD`, `MODE=incluster`).
Альтернатива для registry-first цикла (drain/dry-run/handoff/undrain): [scripts/grid-orchestrate-handoff.sh](../scripts/grid-orchestrate-handoff.sh) (`REGISTRY`, `PARENT`, `CHILD`).

### Репетиция §7 без вывода родителя (§5 не выполнять)

Чтобы отработать drain → (опционально) dry-run → **forward-npc-handoff**, но **оставить родителя** в каталоге и не идти в §5 runbook: выполните шаги §7 выше, затем обязательно снимите запрет join: `mmoctl -registry <host:9100> forward-update <PARENT> split-drain false`. Убедитесь, что на **CHILD** не было игроков на момент import. Клиентам с открытым WS может понадобиться реконнект (runbook §4).

## §8 После изменений БД, контента или handoff

1. Миграции: Job **`/migrate`** или путь gateway; [ci-and-deploy.md](ci-and-deploy.md) — **`verify-readyz-goose`**.
2. **[scripts/staging-verify.sh](../scripts/staging-verify.sh)** с при необходимости **`STAGING_VERIFY_MIGRATION_DRY_RUN=incluster`**, **`STAGING_VERIFY_EXPECT_CELL_IDS`**, **`STAGING_VERIFY_RESOLVE_CHECKS`**, **`STAGING_VERIFY_READYZ_GOOSE_HEADER=1`**.
3. Дым handoff: **`run-forward-npc-handoff.sh`**.
4. Нагрузка: **`make load-smoke`** (корень `backend/`).

## Хвосты cold-path после MVP (операционка и UX)

См. также таблицу в снимке [roadmap-checklist.md](../../docs/roadmap-checklist.md) («Эпик B3 — cold-path»).

| Область | Что ещё открыто |
|---------|------------------|
| **Игроки** | Полный автоперенос без реконнекта — вне scope; **`GET /v1/me/resolve-preview`**. Расхождение **`last_cell`** ↔ **`ResolvePosition`** для JWT: в **строгом** режиме **`GET /v1/ws`** даёт **409** (`cell_handoff_required`, `last_cell` / `resolved`) — клиент обновляет **`POST /v1/session`** с координатами и переподнимает WS ([runbook §4](../runbooks/cold-cell-split.md)). На **staging** по умолчанию включён **`GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH`** ([Terraform `gateway_allow_cell_handoff_mismatch`](../deploy/terraform/staging/variables.tf)): 409 не отдаётся, в логах **`ws_cell_id_mismatch_allowed`**. При обрыве gRPC к соте gateway может **перерезолвить** и переключить downstream (кратковременные **`apply_input: Unavailable`** в логах при merge/split — ожидаемо). |
| **Вывод родителя** | Полный вывод из каталога (в т.ч. §5 runbook): graceful shutdown → deregister; при необходимости убрать соту из [`cell_instances.auto.tfvars`](../deploy/terraform/staging/cell_instances.auto.tfvars) и `tofu apply`. |
| **NPC / миграции** | **`ForwardNpcHandoff`** / **`mmoctl forward-npc-handoff`** — [runbook §6–7](../runbooks/cold-cell-split.md); операторский цикл §7 — выше в этом файле. Репетиция §7 без §5 — см. подраздел выше. |

## Live-миграция игроков

Полный **live-handoff** игроков без реконнекта — вне текущего scope (см. конец runbook). Для планирования переноса сущностей на уровне соты реализованы **`ListMigrationCandidates`** (gRPC **cell**) и **`migration-dry-run`** в [cmd/mmoctl](../cmd/mmoctl/main.go).

## Приоритет исполнения B3 (операторский)

Для ежедневной эксплуатации приоритет: **grid-manager orchestration** (auto split-drain + scripted handoff), а полный §5 runbook (вывод родителя из каталога) выполнять только когда действительно меняется топология и требуется инфраструктурный вывод parent.

Для state-machine workflow в grid-manager: `MMO_GRID_AUTO_SPLIT_WORKFLOW=true` (дополнительно к `MMO_GRID_AUTO_SPLIT_DRAIN=true`), метрики `mmo_grid_manager_split_workflow_*`, события `grid.split.workflow`.

Guardrails для controlled scale-out:
- `MMO_GRID_SPLIT_MAX_LEVEL=<N>` — не запускать workflow для cell path с depth `>= N`;
- `MMO_GRID_SPLIT_MAX_CONCURRENT_WORKFLOWS=<N>` — ограничить параллельные workflow;
- `MMO_GRID_SPLIT_WORKFLOW_BLOCKLIST=cell_a,cell_b` — CSV блоклист `cell_id`.

## Split control-plane (staging)

Текущий путь авто-split в staging:

1. `grid-manager` на breach включает `split_drain`.
2. Делает `PlanSplit` и публикует `cell.control` запросы на создание child-cell.
3. `cell-controller` materialize child-cell как Kubernetes `Service` + `Deployment`.
4. Workflow ждёт, пока children появятся в каталоге и пройдут `Ping` reachability.
5. В начале `runOnce` workflow идемпотентно выставляет `split_drain=true` на parent; после **всех** успешных `ForwardNpcHandoff` по детям (multi-child без partial-success) снимает `split_drain` и публикует стадию **`retire_ready`** в `grid.split.workflow`.
6. **Post-handoff orchestration (по умолчанию вкл.):** после записи `retire_ready` в Redis grid-manager выполняет префлайт (parent/children в каталоге, `Ping`, `Resolve` по probe-точкам внутри bounds каждого child, с фильтром чужих более глубоких веток) и переводит `mmo:grid:split:retire_state:<parent>` в **`phase=automation_complete`** либо **`phase=preflight_blocked`** с массивом `preflight_blocked_reasons`. События в `grid.split.workflow`: **`automation_complete`** или **`post_handoff_preflight_failed`**. Отключить: **`MMO_GRID_AUTO_POST_HANDOFF_ORCHESTRATION=false`**.
7. **Формальное состояние после сплита:** в Redis (grid-manager / cell-controller):
   - `mmo:grid:split:retire_state:<parent_cell_id>` — JSON: `phase` (`retire_ready` → `automation_complete` или `preflight_blocked`), `handoff_children`, `next_action` (`operator_final_retire`), `next_step` (операторский §5 для вывода baseline parent);
   - `mmo:cell-controller:retire_ready:<parent_cell_id>` и `mmo:cell-controller:retire:<parent_cell_id>` — снимок на **`retire_ready`**;
   - `mmo:cell-controller:automation_complete:<parent_cell_id>` — снимок на **`automation_complete`**;
   baseline **primary** `cell-node` из Terraform **не** удаляется автоматически.
8. **Опционально (staging / уборка runtime child):** `MMO_GRID_SPLIT_TEARDOWN_RUNTIME_CHILDREN=true` — после успешного workflow (после orchestration) публикует `op=delete_runtime_child` на каждого ребёнка. По умолчанию **false**.

**Чтение состояния:** `kubectl exec deploy/grid-manager -n mmo -- /mmoctl split-retire-state <parent_cell_id>` (нужны `REDIS_*` в поде).

Проверка end-to-end: [`scripts/grid-auto-split-e2e.sh`](../scripts/grid-auto-split-e2e.sh)
- проверяет `mmo_grid_manager_split_workflow_runs_total{result="ok"}`,
- проверяет рост числа сот в каталоге,
- проверяет `retire_ready_set` и **`automation_complete_set`** в логах `cell-controller`,
- проверяет **`automation_complete`** в JSON `split-retire-state` для родителя (переменная **`GRID_SPLIT_PARENT_CELL_ID`**, по умолчанию `cell_root`).

В [`scripts/staging-verify.sh`](../scripts/staging-verify.sh) при наличии **`cell_q0`** в каталоге проверяется `automation_complete` в Redis; отключение: **`STAGING_VERIFY_POST_HANDOFF_STATE=0`**.

## Merge handoff (MVP, manual trigger)

Для scale-in MVP добавлен ручной orchestrated merge handoff NPC (`children -> parent`) через registry RPC:

```bash
kubectl -n mmo exec deploy/grid-manager -- /mmoctl -registry 127.0.0.1:9100 \
  forward-merge-handoff cell_root cell_q0,cell_q1,cell_q2,cell_q3 "ticket"
```

События публикуются в NATS `grid.merge.workflow`, метрики: `mmo_grid_manager_merge_workflow_*`.
Полный topology switch/teardown child после handoff остаётся операторским шагом (см. ADR merge).

## Auto merge workflow (feature-flag)

В `grid-manager` добавлен флаговый `auto merge` контур:
- `MMO_GRID_AUTO_MERGE_WORKFLOW=true` — включает запуск merge workflow из load policy.
- `MMO_GRID_MERGE_MIN_LOW_LOAD_DURATION` / `MMO_GRID_MERGE_COOLDOWN` — hysteresis/cooldown для scale-in.
- `MMO_GRID_MERGE_THRESHOLD_MAX_PLAYERS|MAX_ENTITIES|MAX_TICK_SECONDS` — low-load пороги для sibling-группы.

Состояние автоматизации merge пишется в Redis:
- `mmo:grid:merge:state:<parent_cell_id>`
- чтение: `mmoctl merge-state <parent_cell_id>`

Для смоука с проверкой Redis-маркера:
- `make merge-auto-e2e-smoke` (включает `MERGE_VERIFY_AUTOMATION_STATE=1`).

**Игроки при auto merge:** `MMO_GRID_MERGE_PLAYER_HANDOFF=true` (и при необходимости `MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS`) — merge не блокируется только из‑за игроков на children; после схлопывания в БД может остаться **stale** `last_cell` с id ребёнка — на staging см. **`GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH`** в [staging README](../deploy/terraform/staging/README.md).

### Логи и ожидаемый «шум» (staging)

- **grid-manager:** `split workflow: failed` / `ForwardNpcHandoff` с **`import_npc_persist blocked: players connected`** — split/handoff отклонён, пока на целевой соте есть игроки (см. runbook §6), не падение кластера.
- **gateway:** пачки **`apply_input: ... Unavailable`** / **`SubscribeDeltas recv: ... EOF`** во время teardown/смены endpoint — кратковременно; при стойком росте смотреть каталог и readiness cell-node.
- **consul:** сообщения вроде **Unknown service ID** при deregister после гонки с control-plane — в коде трактуются как идемпотентный успех (см. `internal/discovery/consul.go`).

Пример отбора в Loki (namespace `mmo`, datasource вашей инсталляции):

```logql
{namespace="mmo", app="gateway"} |= "apply_input: rpc error"
```

```logql
{namespace="mmo", app="grid-manager"} |= "split workflow: failed"
```

### Латентность ForwardMergeHandoff (staging)

Гистограмма Prometheus **`mmo_grid_registry_rpc_duration_seconds`**, лейбл **`method="ForwardMergeHandoff"`**. В Grafana: дашборд uid **`mmo-grid-rpc-p95`** ([`deploy/observability/grafana-dashboard-grid-rpc-p95-by-method.json`](../deploy/observability/grafana-dashboard-grid-rpc-p95-by-method.json)).

**Наблюдение (staging, март 2026):** во время волн **авто-merge** типичны **p50 порядка субсекунды–1 s**, при этом **p95/p99 могут доходить до нескольких секунд** на отдельных вызовах (глубина пути соты, цепочка gRPC к детям, перенос сущностей/игроков). **Частота вызовов низкая** — на графике в первую очередь виден **хвост** распределения, а не «каждый RPC 5 s». Сверка по факту одного вызова: в Loki у **grid-manager** пары JSON-сообщений **`registry_op_start`** / **`registry_op_done`** с полем **`method":"ForwardMergeHandoff"`** и **`parent_cell_id`**.

**Операционно:** высокий p95 уметь отличать от регрессии **`ForwardCellUpdate`** / **`ResolvePosition`** без контекста; при росте тренда — Loki, при необходимости — разметка стадий внутри handoff или отдельный алерт с порогом под merge. Пункт в чеклисте суперпроекта: [`roadmap-checklist.md`](../../docs/roadmap-checklist.md) → **Мониторинг и observability**.
