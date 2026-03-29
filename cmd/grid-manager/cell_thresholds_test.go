package main

import (
	"math"
	"testing"
)

func TestEvalLoadThresholds(t *testing.T) {
	th := loadThresholds{maxPlayers: 10, maxEntities: 100, maxTickSec: 0.05}

	t.Run("ok", func(t *testing.T) {
		w, v := evalLoadThresholds(true, 3, 50, 0.01, th)
		if w != 1 {
			t.Fatalf("within=%v", w)
		}
		for _, k := range []string{"players", "entities", "tick", "unreachable"} {
			if v[k] != 0 {
				t.Fatalf("%s=%v", k, v[k])
			}
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		w, v := evalLoadThresholds(false, 0, 0, 0, th)
		if w != 0 || v["unreachable"] != 1 {
			t.Fatalf("within=%v v=%v", w, v)
		}
	})

	t.Run("players", func(t *testing.T) {
		w, v := evalLoadThresholds(true, 11, 1, 0.01, th)
		if w != 0 || v["players"] != 1 {
			t.Fatalf("within=%v v=%v", w, v)
		}
	})

	t.Run("tick", func(t *testing.T) {
		w, v := evalLoadThresholds(true, 1, 1, 0.06, th)
		if w != 0 || v["tick"] != 1 {
			t.Fatalf("within=%v v=%v", w, v)
		}
	})

	t.Run("disabled_limits", func(t *testing.T) {
		open := loadThresholds{maxPlayers: -1, maxEntities: -1, maxTickSec: -1}
		w, v := evalLoadThresholds(true, 1e6, 1e6, 1.0, open)
		if w != 1 {
			t.Fatalf("within=%v", w)
		}
		if v["unreachable"] != 0 {
			t.Fatal(v)
		}
	})
}

func TestParseLoadThresholds_defaults(t *testing.T) {
	t.Setenv("MMO_GRID_THRESHOLD_MAX_PLAYERS", "")
	t.Setenv("MMO_GRID_THRESHOLD_MAX_ENTITIES", "")
	t.Setenv("MMO_GRID_THRESHOLD_MAX_TICK_SECONDS", "")
	th := parseLoadThresholds()
	if th.maxPlayers != 200 || th.maxEntities != 8000 || math.Abs(th.maxTickSec-0.05) > 1e-9 {
		t.Fatalf("%+v", th)
	}
}
