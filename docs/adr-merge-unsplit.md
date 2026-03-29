# ADR: merge / unsplit для scale-in

Дата: 2026-03-29  
Статус: Proposed

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

Пока ADR не принят и протокол не реализован, scale-in остаётся операторским и вне auto-path.
