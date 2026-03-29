grid_manager_extra_env = {
  MMO_GRID_AUTO_SPLIT_DRAIN    = "true"
  MMO_GRID_AUTO_SPLIT_WORKFLOW = "true"
  # Self-call registry gRPC (ForwardNpcHandoff / workflow); в поде не 127.0.0.1
  MMO_GRID_REGISTRY_ADDR = "mmo-grid-manager.mmo.svc.cluster.local:9100"
}