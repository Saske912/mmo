package db

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// OpenPool создаёт пул подключений к Postgres (CNPG / локальный).
func OpenPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	if connString == "" {
		return nil, fmt.Errorf("empty database url")
	}
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// EnsureSchema создаёт таблицы приложения (idempotent).
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaSQL)
	return err
}

// RecordSessionIssue пишет факт выдачи сессии (best-effort в вызывающем коде).
func RecordSessionIssue(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `INSERT INTO mmo_session_issue (player_id) VALUES ($1)`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}
