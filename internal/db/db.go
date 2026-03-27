package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

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

// RunMigrations применяет встроенные SQL-миграции goose (Up до актуальной версии).
func RunMigrations(ctx context.Context, connString string) error {
	db, err := sql.Open("pgx", connString)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return err
	}

	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetBaseFS(migrationFS)
	defer goose.SetBaseFS(nil)

	return goose.UpContext(ctx, db, "migrations")
}

// RecordSessionIssue пишет факт выдачи сессии (best-effort в вызывающем коде).
func RecordSessionIssue(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `INSERT INTO mmo_session_issue (player_id) VALUES ($1)`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}
