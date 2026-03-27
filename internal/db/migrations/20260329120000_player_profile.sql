-- +goose Up
CREATE TABLE mmo_player_profile (
  player_id    text PRIMARY KEY,
  display_name text NOT NULL DEFAULT '',
  updated_at   timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS mmo_player_profile;
