package main

import (
	"os"
	"strconv"
	"strings"
)

// loadThresholds — начальные пороги для grid-manager (без оркестрации, только наблюдаемость).
// Значение ≤ 0 отключает проверку по этому измерению.
type loadThresholds struct {
	maxPlayers  int
	maxEntities int
	maxTickSec  float64
}

func parseLoadThresholds() loadThresholds {
	t := loadThresholds{
		maxPlayers:  200,
		maxEntities: 8000,
		maxTickSec:  0.05, // 50ms, в одном ряду с алертом по mmo_cell_tick_step_duration_seconds
	}
	if s := strings.TrimSpace(os.Getenv("MMO_GRID_THRESHOLD_MAX_PLAYERS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			t.maxPlayers = n
		}
	}
	if s := strings.TrimSpace(os.Getenv("MMO_GRID_THRESHOLD_MAX_ENTITIES")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			t.maxEntities = n
		}
	}
	if s := strings.TrimSpace(os.Getenv("MMO_GRID_THRESHOLD_MAX_TICK_SECONDS")); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			t.maxTickSec = f
		}
	}
	return t
}

// evalLoadThresholds: withinLimits = 1.0 метрикой, если сота доступна и ни один активный порог не превышен.
// violation — по одному флагу 0/1 на kind (players, entities, tick, unreachable).
func evalLoadThresholds(reachable bool, players, entities, tickSec float64, th loadThresholds) (withinLimits float64, violation map[string]float64) {
	violation = map[string]float64{
		"players":     0,
		"entities":    0,
		"tick":        0,
		"unreachable": 0,
	}
	if !reachable {
		violation["unreachable"] = 1
		return 0, violation
	}
	if th.maxPlayers > 0 && players > float64(th.maxPlayers) {
		violation["players"] = 1
	}
	if th.maxEntities > 0 && entities > float64(th.maxEntities) {
		violation["entities"] = 1
	}
	if th.maxTickSec > 0 && tickSec > th.maxTickSec {
		violation["tick"] = 1
	}
	for k, val := range violation {
		if k == "unreachable" {
			continue
		}
		if val > 0 {
			return 0, violation
		}
	}
	return 1, violation
}
