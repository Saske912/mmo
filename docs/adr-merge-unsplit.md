# ADR: merge / unsplit для scale-in

Дата: 2026-03-29  
Статус: Accepted for MVP scope (phase 0-1)

## Контекст

Scale-out (split) автоматизирован: load policy -> split workflow -> handoff -> `automation_complete`.
Обратного пути (слияние соседних child обратно в parent или более крупный shard) в коде нет.

Без формального контракта merge есть риск:
- потери NPC/состояния при обратном переносе,
- конфликтов Resolve во время пересечения coverage,
- деградации UX (скачки `last_cell` / 409 handoff),
- гонок с runtime teardown.

## Решение

Не внедрять merge как ad-hoc логику.  
Вести как отдельный эпик с обязательным протоколом и тестовым контуром.

### MVP scope (первая итерация)

1. Инициатор merge — только операторский/manual trigger (через `mmoctl`/registry RPC).
2. Merge разрешён только для ровной группы из 4 sibling-child одного parent (зеркало `PlanSplit`).
3. Модель игроков в MVP — cold-path: merge blocked при активных player-сессиях на child.
4. В MVP автоматический low-load auto-merge не включается.
5. В MVP runtime merge покрывает handoff NPC parent <- children и восстановление `split_drain=false`; полный topology retire/teardown child остаётся операторским шагом.

## Предварительный контракт (черновик)

1. **Detect candidates**  
   Выделить sibling-группу (4 child одного `(qx,qz,parent_level)`), которая стабильно below-threshold.
2. **Prepare merge**  
   `split_drain=true` на участниках merge-группы, freeze новых join.
3. **Persist/export**  
   Экспорт NPC/состояния из child (аналог handoff persist), валидация пустых/допустимых player-сессий.
4. **Import/compose**  
   Импорт в target parent-cell (или reactivated baseline shard).
5. **Switch resolve**  
   Атомарно сделать parent самым специфичным winner для coverage merge-группы.
6. **Runtime teardown**  
   Грациозно остановить child workloads и удалить runtime ресурсы.
7. **Verify**  
   Проверка каталога, Resolve, Ping, Redis/NATS state markers.

## Инварианты

- Ни одна точка coverage не должна остаться без winner в каталоге.
- После merge для зоны группы `ResolveMostSpecific` должен возвращать target parent.
- Повторный шаг протокола должен быть безопасен (idempotent).
- Runtime teardown выполняется только после успешной верификации switch.

## Влияние на компоненты

- `grid-manager`: новая state-machine `merge_workflow`.
- `cell-controller`: команды materialize/retire для merge-path.
- `gateway`: правила handoff/409 во время merge-окна.
- `staging` scripts: отдельный e2e smoke для merge.

## Последствия

До полной реализации topology switch/teardown, scale-in остаётся частично операторским: автоматизируется только orchestrated handoff NPC и подготовка к merge.
