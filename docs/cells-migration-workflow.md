# Соты: migration-dry-run, handoff, регрессия (ссылка на runbook)

Операторский поток **cold-path** и проверки после изменений собраны в [runbooks/cold-cell-split.md](../runbooks/cold-cell-split.md). Ниже — сжатый указатель без дублирования длинных процедур.

## Preflight (перед регрессией п.3 / staging)

- **`kubectl`**: контекст с namespace **`mmo`**; **`grid-manager`**, **`gateway`**, родительский и дочерний **`cell-node`** в `Running` (см. вывод `staging-verify.sh`).
- **`PARENT` / `CHILD`**: сверить с фактическим каталогом и с [cell_instances в Terraform](../deploy/terraform/staging/cell_instances.auto.tfvars) (пример staging: `cell_0_0_0` → `cell_-1_-1_1`).
- **`migration-dry-run`**: для вызова **ListMigrationCandidates** на соте нужен cluster DNS; с хоста вне кластера gRPC endpoint соты часто не резолвится — используйте **`STAGING_VERIFY_MIGRATION_DRY_RUN=incluster`** в [`staging-verify.sh`](../scripts/staging-verify.sh) или [`mmoctl-in-cluster.sh`](../scripts/mmoctl-in-cluster.sh).
- **Импорт на CHILD** (`forward-npc-handoff` / `import_npc_persist`): при живых сессиях на целевой соте импорт отклоняется — см. runbook §6.
- **Gateway / Ingress**: при нестандартном URL — **`GATEWAY_PUBLIC_URL`**; самоподписанный TLS — **`STAGING_VERIFY_TLS_INSECURE=1`** (см. комментарии в `staging-verify.sh`).

## §7 Операторский пайплайн (drain → handoff → инфра)

Последовательность из runbook:

1. **`set-split-drain true`** на родителе — запрет новых Join.
2. Опционально **`migration-dry-run`** / **`ListMigrationCandidates`** — сверка сущностей (прямой gRPC к соте, нужен cluster DNS или [scripts/mmoctl-in-cluster.sh](../scripts/mmoctl-in-cluster.sh)).
3. **`mmoctl forward-npc-handoff`** — перенос NPC одним вызовом (§6 runbook).
4. OpenTofu: обновить **`cell_instances`**, **`tofu apply`**; **`set-split-drain false`** перед выводом родителя.
5. Клиенты: реконнект WS при смене покрытия.

Обёртка: [scripts/run-forward-npc-handoff.sh](../scripts/run-forward-npc-handoff.sh) (`PARENT`, `CHILD`, `MODE=incluster`).

### Репетиция §7 без вывода родителя (§5 не выполнять)

Чтобы отработать drain → (опционально) dry-run → **forward-npc-handoff**, но **оставить родителя** в каталоге и не идти в §5 runbook: выполните шаги §7 выше, затем обязательно снимите запрет join: `mmoctl -registry <host:9100> forward-update <PARENT> split-drain false`. Убедитесь, что на **CHILD** не было игроков на момент import. Клиентам с открытым WS может понадобиться реконнект (runbook §4).

## §8 После изменений БД, контента или handoff

1. Миграции: Job **`/migrate`** или путь gateway; [ci-and-deploy.md](ci-and-deploy.md) — **`verify-readyz-goose`**.
2. **[scripts/staging-verify.sh](../scripts/staging-verify.sh)** с при необходимости **`STAGING_VERIFY_MIGRATION_DRY_RUN=incluster`**, **`STAGING_VERIFY_EXPECT_CELL_IDS`**, **`STAGING_VERIFY_RESOLVE_CHECKS`**, **`STAGING_VERIFY_READYZ_GOOSE_HEADER=1`**.
3. Дым handoff: **`run-forward-npc-handoff.sh`**.
4. Нагрузка: **`make load-smoke`** (корень `backend/`).

## Live-миграция игроков

Полный **live-handoff** игроков без реконнекта — вне текущего scope (см. конец runbook). Для планирования переноса сущностей на уровне соты реализованы **`ListMigrationCandidates`** (gRPC **cell**) и **`migration-dry-run`** в [cmd/mmoctl](../cmd/mmoctl/main.go).
