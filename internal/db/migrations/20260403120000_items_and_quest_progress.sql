-- +goose Up
ALTER TABLE mmo_player_quest ADD COLUMN IF NOT EXISTS progress INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS mmo_item_def (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT '',
  stack_max INTEGER NOT NULL DEFAULT 99
);

CREATE TABLE IF NOT EXISTS mmo_player_item (
  player_id TEXT NOT NULL,
  item_id TEXT NOT NULL REFERENCES mmo_item_def (id) ON DELETE CASCADE,
  quantity INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (player_id, item_id),
  CONSTRAINT mmo_player_item_qty_positive CHECK (quantity > 0)
);

CREATE INDEX IF NOT EXISTS idx_mmo_player_item_player ON mmo_player_item (player_id);

INSERT INTO mmo_item_def (id, display_name) VALUES
  ('coin_copper', 'Медная монета'),
  ('tutorial_shard', 'Осколок учебного квеста')
ON CONFLICT (id) DO NOTHING;

-- +goose Down
DROP INDEX IF EXISTS idx_mmo_player_item_player;
DROP TABLE IF EXISTS mmo_player_item;
DROP TABLE IF EXISTS mmo_item_def;
ALTER TABLE mmo_player_quest DROP COLUMN IF EXISTS progress;
