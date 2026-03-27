-- +goose Up
CREATE TABLE IF NOT EXISTS mmo_player_wallet (
    player_id   TEXT PRIMARY KEY,
    gold        BIGINT NOT NULL DEFAULT 0 CHECK (gold >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS mmo_player_wallet;
