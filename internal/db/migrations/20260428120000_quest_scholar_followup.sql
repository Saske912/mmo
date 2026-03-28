-- +goose Up
-- Продолжение ветки учёного (симметрия mercenary_elite_contract).
INSERT INTO mmo_item_def (id, display_name) VALUES
  ('scholar_sigil', 'Сигиль знатока')
ON CONFLICT (id) DO NOTHING;

INSERT INTO mmo_quest_def (quest_id, target_progress, reward_gold, reward_item_id, reward_item_qty, prerequisite_quest_id)
VALUES
  ('scholar_deep_archive', 1, 120, 'scholar_sigil', 1, 'branch_path_scholar')
ON CONFLICT (quest_id) DO UPDATE SET
  target_progress = EXCLUDED.target_progress,
  reward_gold = EXCLUDED.reward_gold,
  reward_item_id = EXCLUDED.reward_item_id,
  reward_item_qty = EXCLUDED.reward_item_qty,
  prerequisite_quest_id = EXCLUDED.prerequisite_quest_id;

-- +goose Down
DELETE FROM mmo_quest_def WHERE quest_id = 'scholar_deep_archive';
DELETE FROM mmo_item_def WHERE id = 'scholar_sigil';
