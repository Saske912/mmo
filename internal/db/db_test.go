package db

import (
	"context"
	"os"
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
}
