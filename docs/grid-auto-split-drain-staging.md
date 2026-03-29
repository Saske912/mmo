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

2. `tofu apply` (или ваш пайплайн).

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
- При реальном инциденте следуйте runbook (§6 export/import NPC, **ForwardNpcHandoff**, §5 вывод родителя и т.д.).
