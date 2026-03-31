grid_manager_extra_env = {
  MMO_GRID_AUTO_SPLIT_DRAIN    = "true"
  MMO_GRID_AUTO_SPLIT_WORKFLOW = "true"
  # Self-call registry gRPC (ForwardNpcHandoff / workflow); в поде не 127.0.0.1
  MMO_GRID_REGISTRY_ADDR = "mmo-grid-manager.mmo.svc.cluster.local:9100"
  # Guardrails split workflow (см. docs/grid-auto-split-drain-staging.md)
  MMO_GRID_SPLIT_MAX_LEVEL                  = "8"
  MMO_GRID_SPLIT_MAX_CONCURRENT_WORKFLOWS  = "4"

  # Auto merge (scale-in) из load policy — см. docs/cells-migration-workflow.md
  MMO_GRID_AUTO_MERGE_WORKFLOW              = "true"
  MMO_GRID_MERGE_MIN_LOW_LOAD_DURATION      = "1m"
  MMO_GRID_MERGE_COOLDOWN                   = "2m"
  MMO_GRID_MERGE_THRESHOLD_MAX_PLAYERS      = "0"
  MMO_GRID_MERGE_THRESHOLD_MAX_ENTITIES     = "300"
  MMO_GRID_MERGE_THRESHOLD_MAX_TICK_SECONDS = "0.01"
}