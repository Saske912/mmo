-- +goose Up
ALTER TABLE mmo_quest_def
  ADD COLUMN IF NOT EXISTS prerequisite_quest_id TEXT REFERENCES mmo_quest_def (quest_id) ON DELETE SET NULL;

INSERT INTO mmo_quest_def (quest_id, target_progress, reward_gold, reward_item_id, reward_item_qty, prerequisite_quest_id)
VALUES ('tutorial_followup', 1, 10, NULL, 0, 'tutorial_intro')
ON CONFLICT (quest_id) DO UPDATE SET
  target_progress = EXCLUDED.target_progress,
  reward_gold = EXCLUDED.reward_gold,
  reward_item_id = EXCLUDED.reward_item_id,
  reward_item_qty = EXCLUDED.reward_item_qty,
  prerequisite_quest_id = EXCLUDED.prerequisite_quest_id;

-- +goose Down
DELETE FROM mmo_quest_def WHERE quest_id = 'tutorial_followup';
ALTER TABLE mmo_quest_def DROP COLUMN IF EXISTS prerequisite_quest_id;
