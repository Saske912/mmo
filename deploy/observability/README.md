# Observability (staging / примеры)

- **Grafana JSON:** [`grafana-dashboard-grid-rpc-p95-by-method.json`](grafana-dashboard-grid-rpc-p95-by-method.json) — дашборд **uid `mmo-grid-rpc-p95`** (p95 **`mmo_grid_registry_rpc_duration_seconds`** по `method`). Реимпорт при новом инстансе Grafana.

### Реимпорт дашборда в Grafana

1. В UI Grafana: **Dashboards → New → Import** (или **+ Import**).
2. Загрузить JSON-файл **`grafana-dashboard-grid-rpc-p95-by-method.json`** или вставить содержимое.
3. Указать **folder** (например общий для MMO) и при конфликте UID подтвердить **uid `mmo-grid-rpc-p95`** или заменить и обновить закладки/алерты.
4. Сохранить. Проверить выбор datasource Prometheus в панелях (переменная или дефолт вашего стенда).
- **Правила Prometheus (пример):** [`prometheus-rule-forward-npc-handoff.example.yaml`](prometheus-rule-forward-npc-handoff.example.yaml); пороги нагрузки сот с grid-manager — [`prometheus-rule-grid-cell-load.example.yaml`](prometheus-rule-grid-cell-load.example.yaml) (`mmo_grid_manager_cell_within_hard_limits`).
- **LogQL / trace_id:** [`loki-logql-traceid.example.txt`](loki-logql-traceid.example.txt).

## Per-тик span на cell-node (только под расследование)

Переменная окружения **`MMO_CELL_OTEL_TICK_SPAN=1`** на **cell-node** создаёт OTLP-span на каждый тик симуляции. Объём данных в Tempo растёт резко — включать **точечно** при отладке задержек тика, затем отключать.
