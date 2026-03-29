# Observability (staging / примеры)

- **Grafana JSON:** [`grafana-dashboard-grid-rpc-p95-by-method.json`](grafana-dashboard-grid-rpc-p95-by-method.json) — дашборд **uid `mmo-grid-rpc-p95`** (p95 **`mmo_grid_registry_rpc_duration_seconds`** по `method`). Реимпорт при новом инстансе Grafana.
- **Grafana JSON (нагрузка сот):** [`grafana-dashboard-grid-cell-load.json`](grafana-dashboard-grid-cell-load.json) — дашборд **uid `mmo-grid-cell-load`** (players/entities, `cell_within_hard_limits`, `cell_threshold_violation`).

### Реимпорт дашборда в Grafana

1. В UI Grafana: **Dashboards → New → Import** (или **+ Import**).
2. Загрузить JSON-файл **`grafana-dashboard-grid-rpc-p95-by-method.json`** или вставить содержимое.
3. Указать **folder** (например общий для MMO) и при конфликте UID подтвердить **uid `mmo-grid-rpc-p95`** или заменить и обновить закладки/алерты.
4. Сохранить. Проверить выбор datasource Prometheus в панелях (переменная или дефолт вашего стенда).
- **Правила Prometheus (пример):** [`prometheus-rule-forward-npc-handoff.example.yaml`](prometheus-rule-forward-npc-handoff.example.yaml); пороги нагрузки сот с grid-manager — [`prometheus-rule-grid-cell-load.example.yaml`](prometheus-rule-grid-cell-load.example.yaml) (`mmo_grid_manager_cell_within_hard_limits`).
- **Политика нагрузки grid-manager:** action-метрика **`mmo_grid_manager_load_policy_actions_total`** (`action`, `cell_id`, `result`), env: `MMO_GRID_LOAD_POLICY_MIN_BREACH_DURATION`, `MMO_GRID_LOAD_POLICY_COOLDOWN`, `MMO_GRID_AUTO_SPLIT_DRAIN`. Ручной прогон на staging (временный patch env + откат): [`../../scripts/grid-load-policy-smoke.sh`](../../scripts/grid-load-policy-smoke.sh). Репетиция **auto split_drain** (Terraform `grid_manager_extra_env` + Join): [`../../docs/grid-auto-split-drain-staging.md`](../../docs/grid-auto-split-drain-staging.md), [`../../scripts/grid-auto-split-drain-rehearsal.sh`](../../scripts/grid-auto-split-drain-rehearsal.sh).
- **LogQL / trace_id:** [`loki-logql-traceid.example.txt`](loki-logql-traceid.example.txt).
- **Grafana alerting:** правило **«cell outside hard limits»** (UID провижининга **`mmostgcelloutsidehardlimits`**, группа **`mmo-staging`**) можно завести вручную в UI или через MCP; PromQL: `sum(mmo_grid_manager_cell_within_hard_limits{namespace="mmo"} == bool 0) > 0`, `for: 2m`. Дублирование: уже есть пример **PrometheusRule** — [`prometheus-rule-grid-cell-load.example.yaml`](prometheus-rule-grid-cell-load.example.yaml) (держите одно из двух, чтобы не было двойных уведомлений).
- **Tempo из API:** поиск «медленных запросов» через плагин Grafana может быть недоступен (`Plugin not found`); трассы смотрите в **Explore → Tempo**, корреляция с Loki — по `trace_id` в JSON-логах (см. LogQL выше).

## Per-тик span на cell-node (только под расследование)

Переменная окружения **`MMO_CELL_OTEL_TICK_SPAN=1`** на **cell-node** создаёт OTLP-span на каждый тик симуляции. Объём данных в Tempo растёт резко — включать **точечно** при отладке задержек тика, затем отключать.
