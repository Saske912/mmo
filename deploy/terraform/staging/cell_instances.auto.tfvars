# Базовая статическая топология staging: только primary-сота.
# Child-соты для split materialize через cell-controller runtime-path.
cell_instances = {
  primary = {
    id    = "cell_0_0_0"
    level = 0
    xmin  = -1000
    xmax  = 1000
    zmin  = -1000
    zmax  = 1000
  }
}
