-- +goose Up
-- Заготовка инвентаря: JSONB-массив слотов {item_id, qty} (MVP без нормализации предметов).
CREATE TABLE IF NOT EXISTS mmo_player_inventory (
    player_id   TEXT PRIMARY KEY,
    items       JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT mmo_player_inventory_items_array
        CHECK (jsonb_typeof(items) = 'array')
);

-- +goose Down
DROP TABLE IF EXISTS mmo_player_inventory;
