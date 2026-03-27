-- +goose Up
CREATE TABLE mmo_session_issue (
  id         bigserial PRIMARY KEY,
  player_id  text        NOT NULL,
  issued_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX mmo_session_issue_issued_at ON mmo_session_issue (issued_at);

-- +goose Down
DROP TABLE IF EXISTS mmo_session_issue;
