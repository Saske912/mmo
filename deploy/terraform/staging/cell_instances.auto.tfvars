# Две соты на staging: родитель + SW-ребёнок после PlanSplit (cold-path проверка B3).
# См. runbooks/cold-cell-split.md и cell_instances.auto.tfvars.example
cell_instances = {
  primary = {
    id    = "cell_0_0_0"
    level = 0
    xmin  = -1000
    xmax  = 1000
    zmin  = -1000
    zmax  = 1000
  }
  "child-sw" = {
    id    = "cell_-1_-1_1"
    level = 1
    xmin  = -1000
    xmax  = 0
    zmin  = -1000
    zmax  = 0
  }
}
