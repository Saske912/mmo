# Observability (staging / примеры)

- **Grafana JSON:** [`grafana-dashboard-grid-rpc-p95-by-method.json`](grafana-dashboard-grid-rpc-p95-by-method.json) — дашборд **uid `mmo-grid-rpc-p95`** (p95 **`mmo_grid_registry_rpc_duration_seconds`** по `method`). Реимпорт при новом инстансе Grafana.
- **Правила Prometheus (пример):** [`prometheus-rule-forward-npc-handoff.example.yaml`](prometheus-rule-forward-npc-handoff.example.yaml).
- **LogQL / trace_id:** [`loki-logql-traceid.example.txt`](loki-logql-traceid.example.txt).

## Per-тик span на cell-node (только под расследование)

Переменная окружения **`MMO_CELL_OTEL_TICK_SPAN=1`** на **cell-node** создаёт OTLP-span на каждый тик симуляции. Объём данных в Tempo растёт резко — включать **точечно** при отладке задержек тика, затем отключать.
