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
}
