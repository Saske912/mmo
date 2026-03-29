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

## Web3 indexer

По умолчанию **`web3_indexer_enabled = true`**: Deployment `web3-indexer` и Service **`mmo-web3-indexer`** (HTTP, ingest). Переменные см. в `variables.tf` (`web3_indexer_http_port`, `web3_indexer_chain_id`, `web3_indexer_extra_env`, **`web3_indexer_ingest_api_key`**, **`web3_indexer_ingest_hmac_secret`**). Пример секретов без коммита: [`web3_indexer.auto.tfvars.example`](web3_indexer.auto.tfvars.example). Документация: [`docs/web3-indexer.md`](../../docs/web3-indexer.md).
