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
	QuestID  string
	State    string
	Progress int
}

// ListPlayerQuests перечисляет квесты игрока (может быть пусто).
func ListPlayerQuests(ctx context.Context, pool *pgxpool.Pool, playerID string) ([]PlayerQuestRow, error) {
	const q = `SELECT quest_id, state, progress FROM mmo_player_quest WHERE player_id = $1 ORDER BY quest_id`
	rows, err := pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerQuestRow
	for rows.Next() {
		var r PlayerQuestRow
		if err := rows.Scan(&r.QuestID, &r.State, &r.Progress); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QuestDef описание квеста (цель и награды) из mmo_quest_def.
type QuestDef struct {
	QuestID        string
	TargetProgress int
	RewardGold     int64
	RewardItemID   string // пусто, если награды предметом нет
	RewardItemQty  int
}

// QuestProgressApplyResult итог применения прогресса (в т.ч. автозавершение).
type QuestProgressApplyResult struct {
	Completed         bool
	Progress          int
	TargetProgress    int
	GoldReward        int64
	ItemsRewarded     []PlayerItemRow
	AlreadyComplete   bool
	NewlyUnlockedQuests []string // квесты, выданные после завершения (цепочка по prerequisite)
}

// LatestAppliedGooseVersion максимальный применённый version_id из goose_db_version (0, если таблицы нет или пусто).
func LatestAppliedGooseVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var v sql.NullInt64
	err := pool.QueryRow(ctx, `
SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = true`).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// inventorySlotJSON элемент JSONB-инвентаря (согласован с заготовкой Phase 0).
type inventorySlotJSON struct {
	ItemID string `json:"item_id"`
	Qty    int    `json:"qty"`
}

// SyncPlayerInventoryJSONB пересобирает mmo_player_inventory.items из mmo_player_item + каталог.
func SyncPlayerInventoryJSONB(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	rows, err := ListPlayerItemsNormalized(ctx, pool, playerID)
	if err != nil {
		return err
	}
	slots := make([]inventorySlotJSON, 0, len(rows))
	for _, r := range rows {
		slots = append(slots, inventorySlotJSON{ItemID: r.ItemID, Qty: r.Quantity})
	}
	raw, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	if err := EnsurePlayerInventory(ctx, pool, playerID); err != nil {
		return err
	}
	const q = `UPDATE mmo_player_inventory SET items = $2::jsonb, updated_at = now() WHERE player_id = $1`
	tag, err := pool.Exec(ctx, q, playerID, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("inventory row missing after ensure")
	}
	return nil
}

func loadQuestDefTx(ctx context.Context, tx pgx.Tx, questID string) (QuestDef, bool, error) {
	var d QuestDef
	var rewardItem *string
	const q = `
SELECT quest_id, target_progress, reward_gold, reward_item_id, reward_item_qty
FROM mmo_quest_def WHERE quest_id = $1`
	err := tx.QueryRow(ctx, q, questID).Scan(
		&d.QuestID, &d.TargetProgress, &d.RewardGold, &rewardItem, &d.RewardItemQty,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QuestDef{}, false, nil
		}
		return QuestDef{}, false, err
	}
	if rewardItem != nil {
		d.RewardItemID = *rewardItem
	}
	return d, true, nil
}

func grantWalletGoldTx(ctx context.Context, tx pgx.Tx, playerID string, delta int64) error {
	if delta == 0 {
		return nil
	}
	if delta < 0 {
		return fmt.Errorf("grant gold: negative delta")
	}
	const q = `UPDATE mmo_player_wallet SET gold = gold + $2, updated_at = now() WHERE player_id = $1`
	tag, err := tx.Exec(ctx, q, playerID, delta)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("wallet row missing")
	}
	return nil
}

func addPlayerItemQuantityTx(ctx context.Context, tx pgx.Tx, playerID, itemID string, qty int) error {
	if qty <= 0 {
		return nil
	}
	const q = `
INSERT INTO mmo_player_item (player_id, item_id, quantity) VALUES ($1, $2, $3)
ON CONFLICT (player_id, item_id) DO UPDATE SET
  quantity = LEAST(
    (SELECT stack_max FROM mmo_item_def WHERE id = EXCLUDED.item_id),
    mmo_player_item.quantity + EXCLUDED.quantity
  )`
	_, err := tx.Exec(ctx, q, playerID, itemID, qty)
	return err
}

func removePlayerItemQuantityTx(ctx context.Context, tx pgx.Tx, playerID, itemID string, qty int) error {
	if qty <= 0 {
		return fmt.Errorf("quantity must be > 0")
	}
	var cur int
	err := tx.QueryRow(ctx,
		`SELECT quantity FROM mmo_player_item WHERE player_id = $1 AND item_id = $2 FOR UPDATE`,
		playerID, itemID,
	).Scan(&cur)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("item not found")
		}
		return err
	}
	if cur < qty {
		return fmt.Errorf("insufficient quantity")
	}
	if cur == qty {
		_, err = tx.Exec(ctx, `DELETE FROM mmo_player_item WHERE player_id = $1 AND item_id = $2`, playerID, itemID)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE mmo_player_item SET quantity = quantity - $3 WHERE player_id = $1 AND item_id = $2`,
			playerID, itemID, qty,
		)
	}
	return err
}

// ApplyPlayerQuestProgress выставляет progress; при достижении target из mmo_quest_def завершает квест и выдаёт награды, синхронизируя JSONB-инвентарь.
func ApplyPlayerQuestProgress(ctx context.Context, pool *pgxpool.Pool, playerID, questID string, progress int) (*QuestProgressApplyResult, error) {
	if progress < 0 {
		return nil, fmt.Errorf("progress must be >= 0")
	}
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return nil, fmt.Errorf("empty quest_id")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var state string
	var cur int
	err = tx.QueryRow(ctx,
		`SELECT state, progress FROM mmo_player_quest WHERE player_id = $1 AND quest_id = $2 FOR UPDATE`,
		playerID, questID,
	).Scan(&state, &cur)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("quest row not found")
		}
		return nil, err
	}
	if state == "completed" {
		return &QuestProgressApplyResult{Progress: cur, TargetProgress: 0, AlreadyComplete: true}, nil
	}

	def, hasDef, err := loadQuestDefTx(ctx, tx, questID)
	if err != nil {
		return nil, err
	}
	if !hasDef {
		const qu = `UPDATE mmo_player_quest SET progress = $3, updated_at = now() WHERE player_id = $1 AND quest_id = $2`
		if _, err := tx.Exec(ctx, qu, playerID, questID, progress); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &QuestProgressApplyResult{Progress: progress, Completed: false}, nil
	}

	res := &QuestProgressApplyResult{TargetProgress: def.TargetProgress}
	if progress < def.TargetProgress {
		const qu = `UPDATE mmo_player_quest SET progress = $3, updated_at = now() WHERE player_id = $1 AND quest_id = $2`
		if _, err := tx.Exec(ctx, qu, playerID, questID, progress); err != nil {
			return nil, err
		}
		res.Progress = progress
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return res, nil
	}

	_, err = tx.Exec(ctx, `INSERT INTO mmo_player_wallet (player_id) VALUES ($1) ON CONFLICT (player_id) DO NOTHING`, playerID)
	if err != nil {
		return nil, err
	}
	if err := grantWalletGoldTx(ctx, tx, playerID, def.RewardGold); err != nil {
		return nil, err
	}
	if def.RewardItemID != "" && def.RewardItemQty > 0 {
		if err := addPlayerItemQuantityTx(ctx, tx, playerID, def.RewardItemID, def.RewardItemQty); err != nil {
			return nil, err
		}
	}
	const quc = `UPDATE mmo_player_quest SET progress = $3, state = 'completed', updated_at = now() WHERE player_id = $1 AND quest_id = $2`
	if _, err := tx.Exec(ctx, quc, playerID, questID, def.TargetProgress); err != nil {
		return nil, err
	}
	if err := syncPlayerInventoryJSONBTx(ctx, tx, playerID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	res.Completed = true
	res.Progress = def.TargetProgress
	res.GoldReward = def.RewardGold
	if def.RewardItemID != "" && def.RewardItemQty > 0 {
		rows, err := ListPlayerItemsNormalized(ctx, pool, playerID)
		if err != nil {
			return res, nil
		}
		for _, row := range rows {
			if row.ItemID == def.RewardItemID {
				res.ItemsRewarded = append(res.ItemsRewarded, row)
				break
			}
		}
		if len(res.ItemsRewarded) == 0 {
			res.ItemsRewarded = []PlayerItemRow{{ItemID: def.RewardItemID, Quantity: def.RewardItemQty}}
		}
	}
	unlocked, uerr := EnsureUnlockedQuestsForPlayer(ctx, pool, playerID)
	if uerr != nil {
		return nil, uerr
	}
	res.NewlyUnlockedQuests = unlocked
	return res, nil
}

func listPlayerItemsNormalizedTx(ctx context.Context, tx pgx.Tx, playerID string) ([]PlayerItemRow, error) {
	const q = `
SELECT pi.item_id, pi.quantity, d.display_name
FROM mmo_player_item pi
JOIN mmo_item_def d ON d.id = pi.item_id
WHERE pi.player_id = $1
ORDER BY pi.item_id`
	rows, err := tx.Query(ctx, q, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerItemRow
	for rows.Next() {
		var r PlayerItemRow
		if err := rows.Scan(&r.ItemID, &r.Quantity, &r.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func syncPlayerInventoryJSONBTx(ctx context.Context, tx pgx.Tx, playerID string) error {
	rows, err := listPlayerItemsNormalizedTx(ctx, tx, playerID)
	if err != nil {
		return err
	}
	slots := make([]inventorySlotJSON, 0, len(rows))
	for _, r := range rows {
		slots = append(slots, inventorySlotJSON{ItemID: r.ItemID, Qty: r.Quantity})
	}
	raw, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO mmo_player_inventory (player_id) VALUES ($1) ON CONFLICT (player_id) DO NOTHING`, playerID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE mmo_player_inventory SET items = $2::jsonb, updated_at = now() WHERE player_id = $1`, playerID, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("inventory row missing after ensure")
	}
	return nil
}

// EnsureUnlockedQuestsForPlayer вставляет активные строки квестов, у которых выполнен prerequisite в mmo_quest_def.
func EnsureUnlockedQuestsForPlayer(ctx context.Context, pool *pgxpool.Pool, playerID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
INSERT INTO mmo_player_quest (player_id, quest_id, state)
SELECT $1, d.quest_id, 'active'
FROM mmo_quest_def d
WHERE d.prerequisite_quest_id IS NOT NULL
  AND EXISTS (
    SELECT 1 FROM mmo_player_quest q
    WHERE q.player_id = $1 AND q.quest_id = d.prerequisite_quest_id AND q.state = 'completed'
  )
ON CONFLICT (player_id, quest_id) DO NOTHING
RETURNING quest_id`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var qid string
		if err := rows.Scan(&qid); err != nil {
			return nil, err
		}
		out = append(out, qid)
	}
	return out, rows.Err()
}

// PlayerQuestAPIRow квест с целевым прогрессом для API.
type PlayerQuestAPIRow struct {
	QuestID             string `json:"quest_id"`
	State               string `json:"state"`
	Progress            int    `json:"progress"`
	TargetProgress      int    `json:"target_progress"`
	PrerequisiteQuestID string `json:"prerequisite_quest_id,omitempty"`
}

// ListPlayerQuestsForAPI объединяет прогресс игрока с mmo_quest_def.
func ListPlayerQuestsForAPI(ctx context.Context, pool *pgxpool.Pool, playerID string) ([]PlayerQuestAPIRow, error) {
	const q = `
SELECT q.quest_id, q.state, q.progress, COALESCE(d.target_progress, 0), COALESCE(d.prerequisite_quest_id, '')
FROM mmo_player_quest q
LEFT JOIN mmo_quest_def d ON d.quest_id = q.quest_id
WHERE q.player_id = $1
ORDER BY q.quest_id`
	rows, err := pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerQuestAPIRow
	for rows.Next() {
		var r PlayerQuestAPIRow
		var prereq string
		if err := rows.Scan(&r.QuestID, &r.State, &r.Progress, &r.TargetProgress, &prereq); err != nil {
			return nil, err
		}
		r.PrerequisiteQuestID = prereq
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddPlayerItemQuantity добавляет предмет (каталог должен содержать item_id) и синхронизирует JSONB-инвентарь.
func AddPlayerItemQuantity(ctx context.Context, pool *pgxpool.Pool, playerID, itemID string, qty int) error {
	if qty <= 0 {
		return fmt.Errorf("quantity must be > 0")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("empty item_id")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM mmo_item_def WHERE id = $1`, itemID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("unknown item_id")
		}
		return err
	}
	if err := addPlayerItemQuantityTx(ctx, tx, playerID, itemID, qty); err != nil {
		return err
	}
	if err := syncPlayerInventoryJSONBTx(ctx, tx, playerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemovePlayerItemQuantity снимает количество предмета и синхронизирует JSONB-инвентарь.
func RemovePlayerItemQuantity(ctx context.Context, pool *pgxpool.Pool, playerID, itemID string, qty int) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("empty item_id")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := removePlayerItemQuantityTx(ctx, tx, playerID, itemID, qty); err != nil {
		return err
	}
	if err := syncPlayerInventoryJSONBTx(ctx, tx, playerID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TransferPlayerItems переносит предметы между игроками (MVP «торговля» без обмена на стороне клиента).
func TransferPlayerItems(ctx context.Context, pool *pgxpool.Pool, fromPlayer, toPlayer, itemID string, qty int) error {
	if qty <= 0 {
		return fmt.Errorf("quantity must be > 0")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Errorf("empty item_id")
	}
	fromPlayer = strings.TrimSpace(fromPlayer)
	toPlayer = strings.TrimSpace(toPlayer)
	if fromPlayer == "" || toPlayer == "" {
		return fmt.Errorf("empty player_id")
	}
	if fromPlayer == toPlayer {
		return fmt.Errorf("cannot transfer to self")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM mmo_item_def WHERE id = $1`, itemID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("unknown item_id")
		}
		return err
	}
	if err := removePlayerItemQuantityTx(ctx, tx, fromPlayer, itemID, qty); err != nil {
		return err
	}
	_, _ = tx.Exec(ctx, `INSERT INTO mmo_player_wallet (player_id) VALUES ($1) ON CONFLICT (player_id) DO NOTHING`, toPlayer)
	_, _ = tx.Exec(ctx, `INSERT INTO mmo_player_inventory (player_id) VALUES ($1) ON CONFLICT (player_id) DO NOTHING`, toPlayer)
	if err := addPlayerItemQuantityTx(ctx, tx, toPlayer, itemID, qty); err != nil {
		return err
	}
	if err := syncPlayerInventoryJSONBTx(ctx, tx, fromPlayer); err != nil {
		return err
	}
	if err := syncPlayerInventoryJSONBTx(ctx, tx, toPlayer); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PlayerLastCellRecord строка mmo_player_last_cell для реконнекта клиента.
type PlayerLastCellRecord struct {
	CellID   string
	ResolveX float64
	ResolveZ float64
}

// GetPlayerLastCellRecord возвращает последнюю известную соту игрока.
func GetPlayerLastCellRecord(ctx context.Context, pool *pgxpool.Pool, playerID string) (*PlayerLastCellRecord, error) {
	const q = `SELECT cell_id, resolve_x, resolve_z FROM mmo_player_last_cell WHERE player_id = $1`
	var r PlayerLastCellRecord
	err := pool.QueryRow(ctx, q, playerID).Scan(&r.CellID, &r.ResolveX, &r.ResolveZ)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// PlayerItemRow предмет игрока с отображаемым именем из каталога.
type PlayerItemRow struct {
	ItemID      string
	Quantity    int
	DisplayName string
}

// ListPlayerItemsNormalized возвращает предметы из mmo_player_item + mmo_item_def.
func ListPlayerItemsNormalized(ctx context.Context, pool *pgxpool.Pool, playerID string) ([]PlayerItemRow, error) {
	const q = `
SELECT pi.item_id, pi.quantity, d.display_name
FROM mmo_player_item pi
JOIN mmo_item_def d ON d.id = pi.item_id
WHERE pi.player_id = $1
ORDER BY pi.item_id`
	rows, err := pool.Query(ctx, q, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayerItemRow
	for rows.Next() {
		var r PlayerItemRow
		if err := rows.Scan(&r.ItemID, &r.Quantity, &r.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnsureStarterPlayerItems выдаёт минимальный стартовый предмет для обучения (идемпотентно).
func EnsureStarterPlayerItems(ctx context.Context, pool *pgxpool.Pool, playerID string) error {
	const q = `
INSERT INTO mmo_player_item (player_id, item_id, quantity)
VALUES ($1, 'tutorial_shard', 1)
ON CONFLICT (player_id, item_id) DO NOTHING`
	_, err := pool.Exec(ctx, q, playerID)
	return err
}
