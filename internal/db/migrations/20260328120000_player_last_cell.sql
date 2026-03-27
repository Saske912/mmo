-- +goose Up
CREATE TABLE mmo_player_last_cell (
  player_id  text PRIMARY KEY,
  cell_id    text NOT NULL,
  resolve_x  double precision NOT NULL,
  resolve_z  double precision NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS mmo_player_last_cell;
