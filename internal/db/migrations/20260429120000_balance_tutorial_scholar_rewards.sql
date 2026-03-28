-- +goose Up
-- Итерация баланса staging: вступительный квест и ветка учёного (п.2 roadmap — контент/баланс в БД).
UPDATE mmo_quest_def SET reward_gold = 55 WHERE quest_id = 'tutorial_intro';
UPDATE mmo_quest_def
SET reward_item_qty = 6
WHERE quest_id = 'tutorial_intro' AND reward_item_id = 'coin_copper';
UPDATE mmo_quest_def SET reward_gold = 130 WHERE quest_id = 'scholar_deep_archive';

-- +goose Down
UPDATE mmo_quest_def SET reward_gold = 50 WHERE quest_id = 'tutorial_intro';
UPDATE mmo_quest_def
SET reward_item_qty = 5
WHERE quest_id = 'tutorial_intro' AND reward_item_id = 'coin_copper';
UPDATE mmo_quest_def SET reward_gold = 120 WHERE quest_id = 'scholar_deep_archive';
