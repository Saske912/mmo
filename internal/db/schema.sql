-- Выдача JWT-сессий gateway (логирование, без хранения секретов).
CREATE TABLE IF NOT EXISTS mmo_session_issue (
  id         bigserial PRIMARY KEY,
  player_id  text        NOT NULL,
  issued_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS mmo_session_issue_issued_at ON mmo_session_issue (issued_at);
