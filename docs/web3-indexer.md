# Web3 Indexer (Phase C bootstrap)

`cmd/web3-indexer` — минимальный ingest-сервис для старта фазы C:

- принимает батч событий (`POST /v1/indexer/ingest`);
- пишет события в PostgreSQL (`mmo_chain_tx_event`);
- обновляет cursor по сети (`mmo_chain_cursor`);
- опционально кеширует cursor в Redis (`mmo:web3:cursor:{chain_id}`);
- сохраняет привязку `player_id -> wallet_address` (`mmo_player_wallet_address`).

## Переменные окружения

- `DATABASE_URL_RW` или `DATABASE_URL` — обязательно.
- `WEB3_INDEXER_LISTEN` — адрес HTTP (по умолчанию `:8091`).
- `WEB3_INDEXER_CHAIN_ID` — дефолтный chain id для ingest без `chain_id`.
- `REDIS_ADDR`, `REDIS_PASSWORD` — опционально.

## Endpoints

- `GET /healthz`
- `POST /v1/indexer/ingest`

Пример запроса:

```json
{
  "chain_id": 11155111,
  "events": [
    {
      "block_number": 1234567,
      "block_hash": "0xabc",
      "tx_hash": "0xdef",
      "log_index": 0,
      "contract_address": "0xbet",
      "event_name": "Transfer",
      "player_id": "player-42",
      "wallet_address": "0xplayerwallet",
      "payload": {
        "from": "0x0",
        "to": "0xplayerwallet",
        "value": "1000000000000000000"
      }
    }
  ]
}
```

## Локальный запуск

```bash
cd backend
go run ./cmd/web3-indexer
```

Перед запуском примените миграции (`make goose-migrate-job` в staging или `go run ./cmd/migrate` локально).

## Staging (Terraform)

В `deploy/terraform/staging` при `web3_indexer_enabled = true` создаются Deployment `web3-indexer` и Service `mmo-web3-indexer` (порт по умолчанию 8091). Образ тот же, что у gateway. Внутренний URL: `tofu output -raw web3_indexer_http`. `DATABASE_URL_RW`, `REDIS_*`, `NATS_URL` — из Secret `mmo-backend` (remote state).
