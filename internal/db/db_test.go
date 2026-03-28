package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenPool_emptyURLErrors(t *testing.T) {
	_, err := OpenPool(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRunMigrationsAndRecord_integration требует рабочий Postgres: MMO_TEST_DATABASE_URL (например postgres://user:pass@localhost:5432/db?sslmode=disable).
func TestRunMigrationsAndRecord_integration(t *testing.T) {
	url := os.Getenv("MMO_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set MMO_TEST_DATABASE_URL to run integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := RunMigrations(ctx, url); err != nil {
		t.Fatal(err)
	}

	pool, err := OpenPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if err := RecordSessionIssue(ctx, pool, "test-player-integration"); err != nil {
		t.Fatal(err)
	}

	if err := UpsertPlayerProfile(ctx, pool, "profile-user", "Display One"); err != nil {
		t.Fatal(err)
	}
	var dn string
	err = pool.QueryRow(ctx, `SELECT display_name FROM mmo_player_profile WHERE player_id = $1`, "profile-user").Scan(&dn)
	if err != nil || dn != "Display One" {
		t.Fatalf("profile: %q err=%v", dn, err)
	}
	if err := UpsertPlayerProfile(ctx, pool, "profile-user", "Renamed"); err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, `SELECT display_name FROM mmo_player_profile WHERE player_id = $1`, "profile-user").Scan(&dn)
	if err != nil || dn != "Renamed" {
		t.Fatalf("profile after upsert: %q err=%v", dn, err)
	}

	if err := EnsurePlayerStats(ctx, pool, "stats-user"); err != nil {
		t.Fatal(err)
	}
	var lvl int
	var xp int64
	err = pool.QueryRow(ctx, `SELECT level, xp FROM mmo_player_stats WHERE player_id = $1`, "stats-user").Scan(&lvl, &xp)
	if err != nil || lvl != 1 || xp != 0 {
		t.Fatalf("stats row: level=%d xp=%d err=%v", lvl, xp, err)
	}
	lvl2, xp2, ok2, err := GetPlayerStats(ctx, pool, "stats-user")
	if err != nil || !ok2 || lvl2 != 1 || xp2 != 0 {
		t.Fatalf("GetPlayerStats: %v ok=%v %d %d", err, ok2, lvl2, xp2)
	}
	_, _, okMiss, err := GetPlayerStats(ctx, pool, "no-stats-row-xyz")
	if err != nil || okMiss {
		t.Fatalf("GetPlayerStats missing: err=%v ok=%v", err, okMiss)
	}

	if err := EnsurePlayerWallet(ctx, pool, "wallet-user"); err != nil {
		t.Fatal(err)
	}
	var gld int64
	err = pool.QueryRow(ctx, `SELECT gold FROM mmo_player_wallet WHERE player_id = $1`, "wallet-user").Scan(&gld)
	if err != nil || gld != 0 {
		t.Fatalf("wallet row: gold=%d err=%v", gld, err)
	}
	g2, okW, err := GetPlayerWallet(ctx, pool, "wallet-user")
	if err != nil || !okW || g2 != 0 {
		t.Fatalf("GetPlayerWallet: %v ok=%v %d", err, okW, g2)
	}
	_, okWMiss, err := GetPlayerWallet(ctx, pool, "no-wallet-row-xyz")
	if err != nil || okWMiss {
		t.Fatalf("GetPlayerWallet missing: err=%v ok=%v", err, okWMiss)
	}

	if err := EnsurePlayerInventory(ctx, pool, "inv-user"); err != nil {
		t.Fatal(err)
	}
	raw, okI, err := GetPlayerInventoryItems(ctx, pool, "inv-user")
	if err != nil || !okI || string(raw) != "[]" {
		t.Fatalf("inventory default: %q ok=%v err=%v", string(raw), okI, err)
	}
	_, okIMiss, err := GetPlayerInventoryItems(ctx, pool, "no-inv-row-xyz")
	if err != nil || okIMiss {
		t.Fatalf("GetPlayerInventoryItems missing: err=%v ok=%v", err, okIMiss)
	}

	if err := UpsertPlayerLastCell(ctx, pool, "test-last-cell", "cell_0_0_0", 10.5, -20.25); err != nil {
		t.Fatal(err)
	}
	lx, lz, lok, lerr := GetPlayerLastCellCoords(ctx, pool, "test-last-cell")
	if lerr != nil || !lok || lx != 10.5 || lz != -20.25 {
		t.Fatalf("GetPlayerLastCellCoords: %v ok=%v (%v,%v)", lerr, lok, lx, lz)
	}
	_, _, hasRow, lerr := GetPlayerLastCellCoords(ctx, pool, "no-such-player-coords")
	if lerr != nil || hasRow {
		t.Fatalf("GetPlayerLastCellCoords missing: err=%v ok=%v", lerr, hasRow)
	}

	var gotCell string
	var rx, rz float64
	err = pool.QueryRow(ctx,
		`SELECT cell_id, resolve_x, resolve_z FROM mmo_player_last_cell WHERE player_id = $1`,
		"test-last-cell",
	).Scan(&gotCell, &rx, &rz)
	if err != nil {
		t.Fatal(err)
	}
	if gotCell != "cell_0_0_0" || rx != 10.5 || rz != -20.25 {
		t.Fatalf("unexpected row: %q %v %v", gotCell, rx, rz)
	}

	if err := UpsertPlayerLastCell(ctx, pool, "test-last-cell", "cell_-1_-1_1", -500, -500); err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx,
		`SELECT cell_id, resolve_x, resolve_z FROM mmo_player_last_cell WHERE player_id = $1`,
		"test-last-cell",
	).Scan(&gotCell, &rx, &rz)
	if err != nil {
		t.Fatal(err)
	}
	if gotCell != "cell_-1_-1_1" || rx != -500 || rz != -500 {
		t.Fatalf("after upsert: %q %v %v", gotCell, rx, rz)
	}

	if err := EnsurePlayerQuestSeed(ctx, pool, "quest-user"); err != nil {
		t.Fatal(err)
	}
	qrows, err := ListPlayerQuests(ctx, pool, "quest-user")
	if err != nil || len(qrows) != 1 || qrows[0].QuestID != "tutorial_intro" || qrows[0].State != "active" || qrows[0].Progress != 0 {
		t.Fatalf("quest seed: %+v err=%v", qrows, err)
	}
	if err := EnsurePlayerWallet(ctx, pool, "quest-user"); err != nil {
		t.Fatal(err)
	}
	prog2, err := ApplyPlayerQuestProgress(ctx, pool, "quest-user", "tutorial_intro", 2)
	if err != nil || prog2.Completed || prog2.Progress != 2 {
		t.Fatalf("quest step 2: %+v err=%v", prog2, err)
	}
	qMid, err := ListPlayerQuests(ctx, pool, "quest-user")
	if err != nil || len(qMid) != 1 || qMid[0].State != "active" || qMid[0].Progress != 2 {
		t.Fatalf("quest mid: %+v err=%v", qMid, err)
	}
	prog3, err := ApplyPlayerQuestProgress(ctx, pool, "quest-user", "tutorial_intro", 3)
	if err != nil || !prog3.Completed || prog3.GoldReward != 50 {
		t.Fatalf("quest complete: %+v err=%v", prog3, err)
	}
	if len(prog3.NewlyUnlockedQuests) != 1 || prog3.NewlyUnlockedQuests[0] != "tutorial_followup" {
		t.Fatalf("expected unlock tutorial_followup: %+v", prog3.NewlyUnlockedQuests)
	}
	qDone, err := ListPlayerQuests(ctx, pool, "quest-user")
	if err != nil || len(qDone) != 2 {
		t.Fatalf("quest done + chain: %+v err=%v", qDone, err)
	}
	var introDone, followActive bool
	for _, q := range qDone {
		if q.QuestID == "tutorial_intro" && q.State == "completed" && q.Progress == 3 {
			introDone = true
		}
		if q.QuestID == "tutorial_followup" && q.State == "active" && q.Progress == 0 {
			followActive = true
		}
	}
	if !introDone || !followActive {
		t.Fatalf("unexpected quest rows: %+v", qDone)
	}
	var gold int64
	err = pool.QueryRow(ctx, `SELECT gold FROM mmo_player_wallet WHERE player_id = $1`, "quest-user").Scan(&gold)
	if err != nil || gold != 50 {
		t.Fatalf("quest reward gold: %d err=%v", gold, err)
	}
	coins, err := ListPlayerItemsNormalized(ctx, pool, "quest-user")
	if err != nil {
		t.Fatal(err)
	}
	var coinQty int
	for _, it := range coins {
		if it.ItemID == "coin_copper" {
			coinQty = it.Quantity
			break
		}
	}
	if coinQty != 5 {
		t.Fatalf("expected 5 coin_copper, got items %+v", coins)
	}
	rawInv, okInv, err := GetPlayerInventoryItems(ctx, pool, "quest-user")
	if err != nil || !okInv || !strings.Contains(string(rawInv), "coin_copper") {
		t.Fatalf("jsonb inventory sync: %q ok=%v err=%v", string(rawInv), okInv, err)
	}
	if err := EnsurePlayerQuestSeed(ctx, pool, "quest-user"); err != nil {
		t.Fatal(err)
	}
	qrows2, err := ListPlayerQuests(ctx, pool, "quest-user")
	if err != nil || len(qrows2) != 2 {
		t.Fatalf("quest idempotent: %+v err=%v", qrows2, err)
	}
	emptyQuests, err := ListPlayerQuests(ctx, pool, "no-quest-rows-xyz")
	if err != nil || len(emptyQuests) != 0 {
		t.Fatalf("quest empty list: %+v err=%v", emptyQuests, err)
	}

	if err := AddPlayerItemQuantity(ctx, pool, "pickup-user", "coin_copper", 3); err != nil {
		t.Fatal(err)
	}
	if err := RemovePlayerItemQuantity(ctx, pool, "pickup-user", "coin_copper", 2); err != nil {
		t.Fatal(err)
	}
	pickupItems, err := ListPlayerItemsNormalized(ctx, pool, "pickup-user")
	if err != nil || len(pickupItems) != 1 || pickupItems[0].ItemID != "coin_copper" || pickupItems[0].Quantity != 1 {
		t.Fatalf("pickup remove: %+v err=%v", pickupItems, err)
	}

	if err := EnsureStarterPlayerItems(ctx, pool, "xfer-from"); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePlayerWallet(ctx, pool, "xfer-to"); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePlayerInventory(ctx, pool, "xfer-to"); err != nil {
		t.Fatal(err)
	}
	if err := TransferPlayerItems(ctx, pool, "xfer-from", "xfer-to", "tutorial_shard", 1); err != nil {
		t.Fatal(err)
	}
	fromItems, err := ListPlayerItemsNormalized(ctx, pool, "xfer-from")
	if err != nil || len(fromItems) != 0 {
		t.Fatalf("xfer from empty: %+v err=%v", fromItems, err)
	}
	toItems, err := ListPlayerItemsNormalized(ctx, pool, "xfer-to")
	if err != nil || len(toItems) != 1 || toItems[0].ItemID != "tutorial_shard" || toItems[0].Quantity != 1 {
		t.Fatalf("xfer to: %+v err=%v", toItems, err)
	}

	if err := EnsureStarterPlayerItems(ctx, pool, "item-user"); err != nil {
		t.Fatal(err)
	}
	items, err := ListPlayerItemsNormalized(ctx, pool, "item-user")
	if err != nil || len(items) != 1 || items[0].ItemID != "tutorial_shard" || items[0].Quantity != 1 || items[0].DisplayName == "" {
		t.Fatalf("starter items: %+v err=%v", items, err)
	}
	if err := EnsureStarterPlayerItems(ctx, pool, "item-user"); err != nil {
		t.Fatal(err)
	}
	items2, err := ListPlayerItemsNormalized(ctx, pool, "item-user")
	if err != nil || len(items2) != 1 || items2[0].Quantity != 1 {
		t.Fatalf("starter items idempotent: %+v err=%v", items2, err)
	}
}
