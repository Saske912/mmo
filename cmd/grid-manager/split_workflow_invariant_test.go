package main

import (
	"testing"

	cellv1 "mmo/gen/cellv1"
)

func TestValidateParentPlayerHandoffReadiness(t *testing.T) {
	t.Run("ok when parent has no players", func(t *testing.T) {
		if err := validateParentPlayerHandoffReadiness(0, 0); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("ok when candidates cover parent players", func(t *testing.T) {
		if err := validateParentPlayerHandoffReadiness(1, 1); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("error when parent has players and no candidates", func(t *testing.T) {
		err := validateParentPlayerHandoffReadiness(1, 0)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("error when candidates less than parent players", func(t *testing.T) {
		err := validateParentPlayerHandoffReadiness(2, 1)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestUniquePlayerCandidatesCount(t *testing.T) {
	candidates := []*cellv1.MigrationCandidate{
		{PlayerId: "p1"},
		{PlayerId: "p1"},
		{PlayerId: "p2"},
		{PlayerId: "  "},
		nil,
	}
	if got := uniquePlayerCandidatesCount(candidates); got != 2 {
		t.Fatalf("expected 2 unique players, got %d", got)
	}
}
