-- +goose Up
-- Ветвление после tutorial_followup + продолжение одной ветки (demonstration of prerequisite chains).
INSERT INTO mmo_item_def (id, display_name) VALUES
  ('forest_token', 'Жетон лесного пути')
ON CONFLICT (id) DO NOTHING;

INSERT INTO mmo_quest_def (quest_id, target_progress, reward_gold, reward_item_id, reward_item_qty, prerequisite_quest_id)
VALUES
  ('branch_path_mercenary', 2, 75, 'forest_token', 1, 'tutorial_followup'),
  ('branch_path_scholar', 2, 100, 'coin_copper', 10, 'tutorial_followup'),
  ('mercenary_elite_contract', 1, 150, NULL, 0, 'branch_path_mercenary')
ON CONFLICT (quest_id) DO UPDATE SET
  target_progress = EXCLUDED.target_progress,
  reward_gold = EXCLUDED.reward_gold,
  reward_item_id = EXCLUDED.reward_item_id,
  reward_item_qty = EXCLUDED.reward_item_qty,
  prerequisite_quest_id = EXCLUDED.prerequisite_quest_id;

-- +goose Down
DELETE FROM mmo_quest_def WHERE quest_id IN ('mercenary_elite_contract', 'branch_path_scholar', 'branch_path_mercenary');
DELETE FROM mmo_item_def WHERE id = 'forest_token';
