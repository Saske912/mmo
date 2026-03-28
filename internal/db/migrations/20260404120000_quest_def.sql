-- +goose Up
CREATE TABLE IF NOT EXISTS mmo_quest_def (
  quest_id TEXT PRIMARY KEY,
  target_progress INTEGER NOT NULL DEFAULT 1 CHECK (target_progress > 0),
  reward_gold BIGINT NOT NULL DEFAULT 0 CHECK (reward_gold >= 0),
  reward_item_id TEXT REFERENCES mmo_item_def (id) ON DELETE SET NULL,
  reward_item_qty INTEGER NOT NULL DEFAULT 0 CHECK (reward_item_qty >= 0),
  CONSTRAINT mmo_quest_def_reward_item_consistent CHECK (
    (reward_item_id IS NULL AND reward_item_qty = 0)
    OR (reward_item_id IS NOT NULL AND reward_item_qty > 0)
  )
);

INSERT INTO mmo_quest_def (quest_id, target_progress, reward_gold, reward_item_id, reward_item_qty)
VALUES ('tutorial_intro', 3, 50, 'coin_copper', 5)
ON CONFLICT (quest_id) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS mmo_quest_def;
