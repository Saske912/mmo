# Скопируйте в backend.tf и выполните: tofu init -reconfigure
# (при переносе с локального state: tofu init -migrate-state).
#
# Это хранилище собственного стека MMO (gateway, cell-node, Secret mmo-backend, …).
# Оно НЕ заменяет data.terraform_remote_state.staging в main.tf — тот по-прежнему
# читает секрет родительского «staging» (см. mmo_remote_state.tf.example и var.remote_state_secret_suffix).

terraform {
  backend "kubernetes" {
    namespace     = "state"
    secret_suffix = "mmo-backend-staging"
    config_path   = "~/.kube/config"
  }
}
