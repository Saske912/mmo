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
- **`WEB3_INDEXER_INGEST_API_KEY`** — если задан: для `POST /v1/indexer/ingest` нужен заголовок **`X-MMO-Ingest-Key`** с тем же значением либо **`Authorization: Bearer <ключ>`**.
- **`WEB3_INDEXER_INGEST_HMAC_SECRET`** — если задан: нужен **`X-MMO-Ingest-Signature`**: нижний регистр **hex(HMAC-SHA256(raw body))**; допускается префикс **`sha256=`**.

Если оба заданы — проверяются оба. Если ни одного — ingest без авторизации (только для локальной отладки).

Максимальный размер тела ingest: **1 MiB**.

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

Переменные **`web3_indexer_ingest_api_key`** и **`web3_indexer_ingest_hmac_secret`** (sensitive) см. в `variables.tf` и пример `web3_indexer.auto.tfvars.example`.

## Смок ingest + БД

После port-forward на `mmo-web3-indexer:8091` и локального DSN (или из Secret):

```bash
cd backend
go run ./scripts/web3-indexer-ingest-smoke \
  -indexer-url http://127.0.0.1:8091 \
  -database-url "$DATABASE_URL_RW" \
  -api-key "$WEB3_INDEXER_INGEST_API_KEY" \
  -hmac-secret "$WEB3_INDEXER_INGEST_HMAC_SECRET"
```

Или только флаги (если **`DATABASE_URL_RW`** уже в окружении):

```bash
make web3-indexer-ingest-smoke WEB3_INDEXER_SMOKE_ARGS='-indexer-url http://127.0.0.1:8091 -api-key <ключ> -hmac-secret <секрет>'
```

**С ноутбука** DSN из Secret указывает на `*.svc.cluster.local` — БД с хоста не резолвится. Нужны два port-forward и подмена хоста в DSN (пароль не трогаем):

```bash
kubectl -n mmo port-forward svc/mmo-web3-indexer 8091:8091 &
kubectl -n postgresql port-forward svc/postgresql-rw 15432:5432 &
RAW="$(kubectl -n mmo get secret mmo-backend -o jsonpath='{.data.DATABASE_URL_RW}' | base64 -d)"
export RAW
export DATABASE_URL_RW="$(python3 -c "import os; r=os.environ['RAW']; l,rt=r.rsplit('@',1); _,p=rt.split('/',1); print(l+'@127.0.0.1:15432/'+p)")"
cd backend && make web3-indexer-ingest-smoke WEB3_INDEXER_SMOKE_ARGS='-indexer-url http://127.0.0.1:8091 -api-key … -hmac-secret …'
```

Готовые значения для Terraform ingest-секретов см. в [`deploy/terraform/staging/web3_indexer.auto.tfvars.example`](../deploy/terraform/staging/web3_indexer.auto.tfvars.example) (скопируйте в **`web3_indexer.auto.tfvars`**, файл в `.gitignore`, не коммитьте).
