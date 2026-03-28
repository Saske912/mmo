-- +goose Up
CREATE TABLE IF NOT EXISTS mmo_player_quest (
  player_id TEXT NOT NULL,
  quest_id TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT 'active',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (player_id, quest_id)
);

CREATE INDEX IF NOT EXISTS idx_mmo_player_quest_player ON mmo_player_quest (player_id);

-- +goose Down
DROP INDEX IF EXISTS idx_mmo_player_quest_player;
DROP TABLE IF EXISTS mmo_player_quest;
