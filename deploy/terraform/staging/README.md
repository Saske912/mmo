# OpenTofu: staging (MMO)

## Синхронизация с репозиторием

Переменные из файлов `*.auto.tfvars` (в т.ч. [`grid_manager.auto.tfvars`](grid_manager.auto.tfvars) с **`grid_manager_extra_env`**) попадают в манифесты только после **`tofu apply`** из **этого каталога**.

- **Не полагайтесь** на разовый `kubectl set env deploy/grid-manager …`: при следующем apply Terraform перезапишет env пода из конфига в.git. Если в кластере и в tfvars расходятся — значит apply не делали после коммита или правили кластер вручную. То же для **`gateway`** и переменных вроде `GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH`; держите их через [`variables.tf`](variables.tf) и `tofu apply`.

### После изменения `grid_manager.auto.tfvars`

1. Из каталога `backend/deploy/terraform/staging`:  
   `tofu plan` → **`tofu apply`** (или ваш CI/CD на этот каталог).
2. Проверка в кластере:

   ```bash
   kubectl -n mmo get deploy grid-manager -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' | grep MMO_GRID_
   ```

3. Ожидаемые значения должны совпадать с теми, что заданы в **`grid_manager_extra_env`** (например `MMO_GRID_AUTO_SPLIT_DRAIN`).

Подробнее про авто `split_drain`: [`docs/grid-auto-split-drain-staging.md`](../../../docs/grid-auto-split-drain-staging.md). Операторский пайплайн после срабатывания: [`runbooks/cold-cell-split.md`](../../../runbooks/cold-cell-split.md) (§6–7).

Для auto state-machine split в grid-manager: задайте env через `grid_manager_extra_env`:
- `MMO_GRID_AUTO_SPLIT_DRAIN=true`
- `MMO_GRID_AUTO_SPLIT_WORKFLOW=true`
- `MMO_GRID_REGISTRY_ADDR` — в Kubernetes: `mmo-grid-manager.<namespace>.svc.cluster.local:9100` (см. [`grid_manager.auto.tfvars`](grid_manager.auto.tfvars)); локально с port-forward: `127.0.0.1:9100`
- опционально **`MMO_GRID_AUTO_POST_HANDOFF_ORCHESTRATION=false`** — отключить префлайт и запись **`automation_complete`** в Redis после `retire_ready` (по умолчанию оркестрация включена, если переменная не задана)
- опционально guardrails:
  - `MMO_GRID_SPLIT_MAX_LEVEL=<N>` — не запускать workflow для cell path с depth `>= N`;
  - `MMO_GRID_SPLIT_MAX_CONCURRENT_WORKFLOWS=<N>` — ограничить число одновременно активных workflow;
  - `MMO_GRID_SPLIT_WORKFLOW_BLOCKLIST=cell_a,cell_b` — CSV блоклист `cell_id`.

Для auto merge workflow (scale-in): **`MMO_GRID_AUTO_MERGE_WORKFLOW=true`** и при необходимости **`MMO_GRID_MERGE_*`** (длительность low-load, cooldown, пороги нагрузки группы — см. [`docs/cells-migration-workflow.md`](../../docs/cells-migration-workflow.md)). Текущие значения в [`grid_manager.auto.tfvars`](grid_manager.auto.tfvars) совпадают с дефолтами в `cell_load_policy` в коде.

Для live handoff игроков при merge (child → parent):
- **`MMO_GRID_MERGE_PLAYER_HANDOFF=true`** — разрешить merge при игроках на children и запускать handoff;
- **`MMO_GRID_MERGE_PLAYER_HANDOFF_MAX_PLAYERS=<N>`** — guardrail по максимуму игроков в merge-окне;
- для smoke-проверки: `make merge-live-players-e2e-smoke`.

Для gateway handoff guard:
- В Terraform: **`gateway_allow_cell_handoff_mismatch`** ([`variables.tf`](variables.tf), по умолчанию **`true`**) выставляет на поде **`GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH=true`**. Тогда при расхождении `mmo_player_last_cell` и результата **`ResolvePosition`** для координат сессии gateway **не** возвращает **409** `cell_handoff_required` на **`GET /v1/ws`**, а логирует предупреждение **`ws_cell_id_mismatch_allowed`** и подключает клиента к **resolved** соте (удобно после **merge** к `cell_root`, пока БД ещё содержит id дочерней соты). Для регрессии «строгого» клиентского потока (ожидание 409) задайте **`gateway_allow_cell_handoff_mismatch = false`** и выполните **`tofu apply`**.
- Проверка в кластере после apply:

  ```bash
  kubectl -n mmo get deploy gateway -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' | grep GATEWAY_ALLOW_CELL_HANDOFF_MISMATCH
  ```

- Поведение **gateway** при кратковременных обрывах gRPC к соте (топология/teardown): при ошибках транспорта к текущему downstream выполняется попытка **перерезолва** и переключения сессии на актуальный endpoint (см. `cmd/gateway`). В логах возможны единичные **`apply_input: ... Unavailable`** в момент смены шардов — это не обязательно сбой сессии.

## Breaking rollout: path-based cell IDs

Переход на `cell_root` / `cell_q...` ломающий для runtime state. Перед первым `tofu apply` нового формата сделайте очистку staging:

1. Удалите runtime child workloads (`cell-node-auto-*`, `mmo-cell-auto-*`), если остались после прошлых split.
2. Очистите Redis ключи, привязанные к старым `cell_id` (`mmo:cell:*`, `mmo:grid:split:*`, `mmo:grid:merge:*`) в тестовом окружении.
3. Очистите старые Consul регистрации с legacy id (`cell_*_*_*`), если они ещё присутствуют.
4. Примените `tofu apply` с baseline root id `cell_root` и перезапустите smoke (`split/merge/staging-verify`).

## Web3 indexer

По умолчанию **`web3_indexer_enabled = true`**: Deployment `web3-indexer` и Service **`mmo-web3-indexer`** (HTTP, ingest). Переменные см. в `variables.tf` (`web3_indexer_http_port`, `web3_indexer_chain_id`, `web3_indexer_extra_env`, **`web3_indexer_ingest_api_key`**, **`web3_indexer_ingest_hmac_secret`**). Пример секретов без коммита: [`web3_indexer.auto.tfvars.example`](web3_indexer.auto.tfvars.example). Документация: [`docs/web3-indexer.md`](../../docs/web3-indexer.md).

## Cell-controller (split control-plane)

По умолчанию **`cell_controller_enabled = true`**: Terraform поднимает `ServiceAccount`, `Role` (в т.ч. **delete** на `deployments`/`services` для teardown), `RoleBinding` и `Deployment` `cell-controller` в `mmo`. Pod/контейнер — baseline **PodSecurity restricted** (distroless `nonroot`, UID **65532**). Контроллер слушает:
- `grid.split.workflow` (в т.ч. **`retire_ready`**),
- `cell.control` (materialize child и опционально **`op=delete_runtime_child`**).

Дополнительные env — `cell_controller_extra_env`. На **grid-manager** для авто-удаления runtime child после успешного split: **`MMO_GRID_SPLIT_TEARDOWN_RUNTIME_CHILDREN=true`** (см. [`docs/cells-migration-workflow.md`](../../docs/cells-migration-workflow.md)).

CRD/controller reference-манифесты для ручного старта/отладки:
- [`../staging/cell-crd.example.yaml`](../../staging/cell-crd.example.yaml)
- [`../staging/cell-controller.example.yaml`](../../staging/cell-controller.example.yaml)
- [`../staging/cell-controller-rbac.example.yaml`](../../staging/cell-controller-rbac.example.yaml)
