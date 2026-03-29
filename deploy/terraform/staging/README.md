# OpenTofu: staging (MMO)

## Синхронизация с репозиторием

Переменные из файлов `*.auto.tfvars` (в т.ч. [`grid_manager.auto.tfvars`](grid_manager.auto.tfvars) с **`grid_manager_extra_env`**) попадают в манифесты только после **`tofu apply`** из **этого каталога**.

- **Не полагайтесь** на разовый `kubectl set env deploy/grid-manager …`: при следующем apply Terraform перезапишет env пода из конфига в.git. Если в кластере и в tfvars расходятся — значит apply не делали после коммита или правили кластер вручную.

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
