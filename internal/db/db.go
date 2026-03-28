package db

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
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

// GetPlayerLastCellCoords возвращает сохранённые координаты resolve для игрока; ok=false если строки нет.
func GetPlayerLastCellCoords(ctx context.Context, pool *pgxpool.Pool, playerID string) (rx, rz float64, ok bool, err error) {
	const q = `SELECT resolve_x, resolve_z FROM mmo_player_last_cell WHERE player_id = $1`
	err = pool.QueryRow(ctx, q, playerID).Scan(&rx, &rz)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	return rx, rz, true, nil
}

// UpsertPlayerLastCell сохраняет последнюю соту и точку resolve gateway при отключении клиента (on conflict по player_id).
func UpsertPlayerLastCell(ctx context.Context, pool *pgxpool.Pool, playerID, cellID string, resolveX, resolveZ float64) error {
	const q = `
INSERT INTO mmo_player_last_cell (player_id, cell_id, resolve_x, resolve_z)
VALUES ($1, $2, $3, $4)
ON CONFLICT (player_id) DO UPDATE SET
	cell_id = EXCLUDED.cell_id,
	resolve_x = EXCLUDED.resolve_x,
	resolve_z = EXCLUDED.resolve_z,
	updated_at = now()`
	_, err := pool.Exec(ctx, q, playerID, cellID, resolveX, resolveZ)
	return err
}

const maxDisplayNameLen = 128

// UpsertPlayerProfile сохраняет отображаемое имя игрока (пустое имя — no-op).
func UpsertPlayerProfile(ctx context.Context, pool *pgxpool.Pool, playerID, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return nil
	}
	if len(displayName) > maxDisplayNameLen {
		displayName = displayName[:maxDisplayNameLen]
	}
	const q = `
INSERT INTO mmo_player_profile (player_id, display_name)
VALUES ($1, $2)
ON CONFLICT (player_id) DO UPDATE SET
	display_name = EXCLUDED.display_name,
	updated_at = now()`
	_, err := pool.Exec(ctx, q, playerID, displayName)
	return err
}

// EnsurePlayerStats создаёт строку прогрессии для игрока при первой сессии (идемпотентно).
func EnsurePlayerStats(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `
INSERT INTO mmo_player_stats (player_id) VALUES ($1)
ON CONFLICT (player_id) DO NOTHING`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}

// GetPlayerStats возвращает level и xp; ok=false если строки ещё нет (после Ensure — всегда ok).
func GetPlayerStats(ctx context.Context, pool *pgxpool.Pool, playerID string) (level int, xp int64, ok bool, err error) {
	const q = `SELECT level, xp FROM mmo_player_stats WHERE player_id = $1`
	err = pool.QueryRow(ctx, q, playerID).Scan(&level, &xp)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	return level, xp, true, nil
}

// EnsurePlayerWallet создаёт строку кошелька игрока при первой сессии (идемпотентно).
func EnsurePlayerWallet(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `
INSERT INTO mmo_player_wallet (player_id) VALUES ($1)
ON CONFLICT (player_id) DO NOTHING`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}

// GetPlayerWallet возвращает gold; ok=false если строки ещё нет (после Ensure — всегда ok).
func GetPlayerWallet(ctx context.Context, pool *pgxpool.Pool, playerID string) (gold int64, ok bool, err error) {
	const q = `SELECT gold FROM mmo_player_wallet WHERE player_id = $1`
	err = pool.QueryRow(ctx, q, playerID).Scan(&gold)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return gold, true, nil
}

// EnsurePlayerInventory создаёт пустой инвентарь при первой сессии (идемпотентно).
func EnsurePlayerInventory(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `
INSERT INTO mmo_player_inventory (player_id) VALUES ($1)
ON CONFLICT (player_id) DO NOTHING`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}

// GetPlayerInventoryItems возвращает нормализованный JSON массива предметов для ответа API.
func GetPlayerInventoryItems(ctx context.Context, pool *pgxpool.Pool, playerID string) (items json.RawMessage, ok bool, err error) {
	const q = `SELECT items FROM mmo_player_inventory WHERE player_id = $1`
	var raw []byte
	err = pool.QueryRow(ctx, q, playerID).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return json.RawMessage(raw), true, nil
}

// EnsurePlayerQuestSeed вставляет заготовку квеста tutorial_intro при первой сессии (идемпотентно).
func EnsurePlayerQuestSeed(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `
INSERT INTO mmo_player_quest (player_id, quest_id, state)
VALUES ($1, 'tutorial_intro', 'active')
ON CONFLICT (player_id, quest_id) DO NOTHING`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}

// PlayerQuestRow строка прогресса квеста для API.
type PlayerQuestRow struct {
	QuestID string
	State   string
}

// ListPlayerQuests перечисляет квесты игрока (может быть пусто).
func ListPlayerQuests(ctx context.Context, pool *pgxpool.Pool, playerID string) ([]PlayerQuestRow, error) {
	const q = `SELECT quest_id, state FROM mmo_player_quest WHERE player_id = $1 ORDER BY quest_id`
	rows, err := pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerQuestRow
	for rows.Next() {
		var r PlayerQuestRow
		if err := rows.Scan(&r.QuestID, &r.State); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
