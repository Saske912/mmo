grid_manager_extra_env = {
  MMO_GRID_AUTO_SPLIT_DRAIN    = "true"
  MMO_GRID_AUTO_SPLIT_WORKFLOW = "true"
  # Self-call registry gRPC (ForwardNpcHandoff / workflow); в поде не 127.0.0.1
  MMO_GRID_REGISTRY_ADDR = "mmo-grid-manager.mmo.svc.cluster.local:9100"
  # Guardrails split workflow (см. docs/grid-auto-split-drain-staging.md)
  MMO_GRID_SPLIT_MAX_LEVEL                  = "8"
  MMO_GRID_SPLIT_MAX_CONCURRENT_WORKFLOWS  = "4"
}