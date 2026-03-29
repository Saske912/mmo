-- +goose Up
CREATE TABLE IF NOT EXISTS mmo_chain_cursor (
  chain_id BIGINT PRIMARY KEY,
  last_block_number BIGINT NOT NULL DEFAULT 0,
  last_block_hash TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mmo_chain_tx_event (
  id BIGSERIAL PRIMARY KEY,
  chain_id BIGINT NOT NULL,
  block_number BIGINT NOT NULL,
  block_hash TEXT NOT NULL DEFAULT '',
  tx_hash TEXT NOT NULL,
  log_index INTEGER NOT NULL,
  contract_address TEXT NOT NULL,
  event_name TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (chain_id, tx_hash, log_index)
);

CREATE INDEX IF NOT EXISTS idx_mmo_chain_tx_event_chain_block
  ON mmo_chain_tx_event (chain_id, block_number DESC);

CREATE INDEX IF NOT EXISTS idx_mmo_chain_tx_event_event_name
  ON mmo_chain_tx_event (event_name);

CREATE TABLE IF NOT EXISTS mmo_player_wallet_address (
  player_id TEXT PRIMARY KEY,
  wallet_address TEXT NOT NULL,
  chain_id BIGINT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mmo_player_wallet_address_unique
  ON mmo_player_wallet_address (chain_id, wallet_address);

-- +goose Down
DROP INDEX IF EXISTS idx_mmo_player_wallet_address_unique;
DROP TABLE IF EXISTS mmo_player_wallet_address;
DROP INDEX IF EXISTS idx_mmo_chain_tx_event_event_name;
DROP INDEX IF EXISTS idx_mmo_chain_tx_event_chain_block;
DROP TABLE IF EXISTS mmo_chain_tx_event;
DROP TABLE IF EXISTS mmo_chain_cursor;
