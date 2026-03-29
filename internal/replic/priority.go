package replic

import (
	"sort"

	"mmo/internal/ecs"
)

// Уровни приоритета в потоке репликации (меньше — раньше в Snapshot / Changed).
const (
	replicationTierPlayer = 0
	replicationTierNPC    = 1
	replicationTierItem   = 2 // зарезервировано: сущности-предметы на земле в ECS (пока нет компонента)
)

func replicationTier(isPlayer func(ecs.Entity) bool, e ecs.Entity) int {
	if isPlayer != nil && isPlayer(e) {
		return replicationTierPlayer
	}
	// TODO: when world item entities exist, return replicationTierItem for them.
	return replicationTierNPC
}

// SortEntitiesReplicationPriority игроки → NPC (и прочие); внутри уровня — по Entity (детерминизм).
func SortEntitiesReplicationPriority(entities []ecs.Entity, isPlayer func(ecs.Entity) bool) {
	if len(entities) <= 1 {
		return
	}
	sort.SliceStable(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
		ti := replicationTier(isPlayer, a)
		tj := replicationTier(isPlayer, b)
		if ti != tj {
			return ti < tj
		}
		return a < b
	})
}
