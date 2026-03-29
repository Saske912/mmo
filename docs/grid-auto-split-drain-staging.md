# Авто `split_drain` на staging (load policy)

## Зачем

При **`MMO_GRID_AUTO_SPLIT_DRAIN=true`** grid-manager после устойчивого нарушения порогов (`MMO_GRID_THRESHOLD_*`, см. probe) вызывает **`Cell.Update(set_split_drain=true)`** на соту. Дальнейшие шаги — по [runbook cold-cell-split](../runbooks/cold-cell-split.md): экспорт NPC, handoff, вывод родителя и т.д.

## Включение (контролируемо через Terraform)

1. В каталоге `deploy/terraform/staging` задайте [`grid_manager_extra_env`](../deploy/terraform/staging/grid_manager.auto.tfvars.example) (например файл `grid_manager.auto.tfvars`):

   ```hcl
   grid_manager_extra_env = {
     MMO_GRID_AUTO_SPLIT_DRAIN = "true"
   }
   ```

2. **`tofu apply`** из каталога `deploy/terraform/staging` (см. [README туда же](../deploy/terraform/staging/README.md): единственный источник правды для env grid-manager; `kubectl set env` даёт расхождение до следующего apply).

3. Убедитесь в поде:  
   `kubectl -n mmo get deploy grid-manager -o jsonpath='{range .spec.template.spec.containers[0].env[*]}{.name}={.value}{"\n"}{end}' | grep MMO_GRID_AUTO_SPLIT_DRAIN`

## Репетиция с искусственным нарушением порога

Скрипт временно занижает порог по длительности тика, ждёт срабатывания policy, проверяет метрики и **Join** по in-cluster DNS (из пода grid-manager), затем снимает порог и выключает **split_drain** через registry:

```bash
cd backend && bash scripts/grid-auto-split-drain-rehearsal.sh
```

Требуется: `kubectl`, `curl`, доступ к кластеру, в образе grid-manager есть `/mmoctl` (стандартный Dockerfile).

## После репетиции

- Проверьте алерты/Grafana.
- Для типового B3 handoff используйте `scripts/grid-orchestrate-handoff.sh` (drain → dry-run → handoff → undrain).
- Полный §5 runbook (вывод родителя) держите как редкий инфраструктурный сценарий.

## Чеклист оператора после автоматического `split_drain`

Срабатывание видно по метрике **`mmo_grid_manager_load_policy_actions_total`** с **`action="split_drain_enable"`** и (если настроено) алерту вокруг порогов / политики нагрузки — см. [`deploy/observability/`](../deploy/observability/README.md).

1. **Подтвердить контекст:** Grafana (дашборд grid/cell load), при необходимости Loki по подам **grid-manager** и затронутой **cell** — нет ли шума, ожидаемо ли нарушение порога.
2. **Зафиксировать соту:** какой **`cell_id`** ушёл в drain (логи grid-manager / событие policy / `mmoctl -registry … list` / resolve).
3. **Дальше по cold-path** — [runbook `cold-cell-split.md` §6–7](../runbooks/cold-cell-split.md): окно, при необходимости **`migration-dry-run`**, **`forward-npc-handoff`** (или пошаговый export/import из §6), обновление **`cell_instances`** и **`tofu apply`**, если появлялись новые шарды; полная последовательность — [`docs/cells-migration-workflow.md`](cells-migration-workflow.md).
4. **Снять drain**, когда мир и каталог согласованы и дальнейшие Join на этой соте допустимы:  
   `mmoctl -registry <grid-manager:9100> forward-update <cell_id> split-drain false`  
   (из пода: см. `scripts/mmoctl-in-cluster.sh` / репетицию [`scripts/grid-auto-split-drain-rehearsal.sh`](../scripts/grid-auto-split-drain-rehearsal.sh)).
5. **Регрессия кластера** при крупных изменениях: [`scripts/staging-verify.sh`](../scripts/staging-verify.sh).
