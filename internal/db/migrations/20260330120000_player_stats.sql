-- +goose Up
CREATE TABLE mmo_player_stats (
  player_id text PRIMARY KEY,
  level     integer NOT NULL DEFAULT 1 CHECK (level >= 1),
  xp        bigint NOT NULL DEFAULT 0 CHECK (xp >= 0),
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS mmo_player_stats;
