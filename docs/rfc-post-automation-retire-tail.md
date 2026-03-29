# RFC: автоматизация хвоста после `automation_complete`

Дата: 2026-03-29  
Статус: Draft

## Контекст

Сейчас auto split workflow в `grid-manager` доводит parent до состояния Redis:
- `phase=automation_complete`,
- `next_action=operator_final_retire`.

После этого оператор вручную выполняет §5 runbook (`cold-cell-split.md`): вывод baseline parent из каталога и инфраструктурные правки (`cell_instances`, `tofu apply`).

Проблема: ручной хвост часто откладывается и создаёт дрейф между runtime-топологией и инфраструктурным baseline.

## Цели

1. Снизить ручные шаги после `automation_complete`.
2. Сделать путь идемпотентным и безопасным (без авто-удалений «по ошибке»).
3. Сохранить контроль оператора над Terraform-частью.

## Не-цели

- Автоматический `tofu apply` в production.
- Live-handoff игроков без реконнекта.
- Merge/unsplit (отдельный эпик).

## Варианты

### Вариант A: встроить post-retire в `grid-manager`

После `automation_complete` запускать дополнительную state-machine:
1) graceful retire parent,  
2) deregister из каталога,  
3) опциональный cleanup runtime ресурсов.

Плюсы: одна точка оркестрации.  
Минусы: высокий blast radius, сложнее отделить «операторское подтверждение» от автоматики.

### Вариант B: операторский job/скрипт по сигналу `automation_complete`

Вынести хвост в отдельный управляемый шаг:
1) скрипт читает `split-retire-state`, проверяет `phase=automation_complete`,  
2) выполняет retire/deregister parent,  
3) публикует `retire_tail_complete` (NATS/Redis marker),  
4) выводит оператору checklist по Terraform.

Плюсы: безопаснее, проще audit/retry, меньше риск автоповреждения topology.  
Минусы: остаётся полуавтоматический шаг.

## Предлагаемое решение

Принять **Вариант B** как следующий итерационный шаг.

## Контракт и флаги

- Новый флаг: `MMO_GRID_AUTO_RETIRE_TAIL=false` (по умолчанию выключен).
- При `false`: текущий процесс без изменений.
- При `true`: допускается только «guarded mode» с явным allowlist parent (например, `MMO_GRID_RETIRE_TAIL_ALLOWLIST`).
- Идемпотентность: повторный запуск не должен падать, если parent уже выведен.

## Минимальные критерии готовности

1. Повторный запуск шага не меняет итог (`idempotent`).
2. Ошибки фиксируются в читаемом state (`retire_tail_failed_reasons`).
3. `staging-verify` умеет проверять marker завершения хвоста (опционально).

## Изменения в runbook

- До внедрения автоматики: §5 остаётся операторским.
- После внедрения guarded-tail: §5 делится на «auto-candidate» и «manual fallback».
