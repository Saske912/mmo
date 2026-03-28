variable "kube_config_path" {
  type        = string
  description = "Путь к kubeconfig для провайдера Kubernetes и remote state backend."
  default     = "~/.kube/config"
}

variable "remote_state_namespace" {
  type        = string
  description = "Namespace, где lego/terraform backend хранит state secret (как в основном IAC)."
  default     = "state"
}

variable "remote_state_secret_suffix" {
  type        = string
  default     = "terraform"
}

variable "namespace" {
  type        = string
  description = "Namespace для приложения MMO backend."
  default     = "mmo"
}

variable "secret_name" {
  type        = string
  description = "Имя Opaque Secret с env для подов (ключи как в internal/config)."
  default     = "mmo-backend"
}

variable "image_repository" {
  type        = string
  description = "Имя репозитория в Harbor без хоста/проекта (например mmo-backend). С Harbor: <registry_host>/<harbor_project>/<image_repository>:tag"
  default     = "mmo-backend"
}

variable "harbor_project" {
  type        = string
  description = "Проект Harbor в пути образа. Для robot вида harbor@library+library — проект library."
  default     = "library"
}

variable "image_registry_override" {
  type        = string
  description = "База реестра образов (URL или хост). По умолчанию Harbor staging; пустая строка — только outputs.mmo.harbor.registry_host из remote state."
  default     = "https://harbor.pass-k8s.ru/"
}

variable "image_tag" {
  type        = string
  description = "Тег образа в Harbor (лучше short SHA коммита; Makefile выставляет TF_VAR_image_tag)."
  default     = "local"
}

variable "image_override" {
  type        = string
  description = "Полный reference образа (например harbor.example.com/mmo/mmo-backend:v1). Если задан — игнорируются image_repository, harbor_project, image_tag и сборка пути из Harbor."
  default     = ""
}

variable "image_pull_policy" {
  type        = string
  description = "Для kind/local обычно IfNotPresent."
  default     = "IfNotPresent"
}

variable "grid_grpc_port" {
  type    = number
  default = 9100
}

variable "grid_metrics_port" {
  type        = number
  description = "HTTP /metrics для grid-manager; 0 — не слушать."
  default     = 9091
}

variable "cell_grpc_port" {
  type    = number
  default = 50051
}

variable "cell_metrics_port" {
  type        = number
  description = "HTTP /metrics для cell-node; 0 — не слушать метрики."
  default     = 9090
}

variable "cell_instances" {
  type = map(object({
    id    = string
    level = number
    xmin  = number
    xmax  = number
    zmin  = number
    zmax  = number
  }))
  description = <<EOT
Соты cell-node: ключ Terraform (например primary, child-sw) — RFC 1123, без подчёркиваний; label cell_shard и имя Service для не-primary.
Ключ primary сохраняет имя Deployment cell-node и Service равным var.cell_service_name (обратная совместимость).
EOT
  default = {
    primary = {
      id    = "cell_0_0_0"
      level = 0
      xmin  = -1000
      xmax  = 1000
      zmin  = -1000
      zmax  = 1000
    }
  }
}

variable "cell_service_name" {
  type        = string
  description = "Имя Service для cell-node (используется в MMO_CELL_GRPC_ADVERTISE)."
  default     = "mmo-cell"
}

variable "grid_service_name" {
  type    = string
  default = "mmo-grid-manager"
}

variable "gateway_http_port" {
  type        = number
  description = "HTTP/WebSocket порт gateway в поде и Service."
  default     = 8080
}

variable "gateway_service_name" {
  type        = string
  description = "Имя Service для game gateway."
  default     = "mmo-gateway"
}

variable "gateway_ingress_enabled" {
  type        = bool
  description = "Создавать Ingress с TLS для публичного hostname gateway (сертификаты в certs/ модуля)."
  default     = true
}

variable "gateway_ingress_host" {
  type        = string
  description = "FQDN для Ingress gateway (Host + TLS SNI)."
  default     = "mmo.pass-k8s.ru"
}

variable "gateway_ingress_class_name" {
  type        = string
  description = "IngressClass (например nginx, traefik). Пустая строка — без поля (дефолт кластера)."
  default     = "nginx"
}

variable "gateway_tls_fullchain_file" {
  type        = string
  description = "Путь к fullchain.pem относительно каталога модуля staging (TLS Secret)."
  default     = "certs/fullchain.pem"
}

variable "gateway_tls_privkey_file" {
  type        = string
  description = "Путь к privkey.pem относительно каталога модуля staging."
  default     = "certs/privkey.pem"
}

variable "gateway_jwt_secret" {
  type        = string
  description = "HMAC для JWT сессий; в K8s также в Opaque Secret (ключ GATEWAY_JWT_SECRET)."
  default     = "staging-gateway-jwt-change-me"
  sensitive   = true
}

variable "gateway_skip_db_migrations" {
  type        = bool
  description = "Если true — env GATEWAY_SKIP_DB_MIGRATIONS на gateway: DDL только через Job /migrate (см. scripts/goose-migrate-job.sh, deploy/staging/goose-job.example.yaml)."
  default     = false
}

variable "harbor_docker_username" {
  type        = string
  description = "Логин Harbor для env секрета приложения и make harbor-login (если непусто — приоритет над outputs.mmo.harbor.docker_login_username)."
  default     = ""
  sensitive   = false
}

variable "harbor_docker_password" {
  type        = string
  description = "Пароль Harbor для env секрета приложения и make harbor-push (если непусто — приоритет над outputs.mmo.harbor.docker_login_password)."
  default     = ""
  sensitive   = true
}

variable "mmo_structured_logs" {
  type        = bool
  description = "MMO_LOG_FORMAT=json у подов gateway / grid-manager / cell-node (structured logs в stdout для Loki/Alloy)."
  default     = true
}

variable "mmo_loki_log_labels" {
  type = map(string)
  description = <<EOT
Дополнительные Pod labels при mmo_structured_logs=true: задайте ключи, по которым ваш кластерный сборщик логов (Grafana Alloy, Promtail, агент k8s-monitoring) отбирает потоки из namespace приложения.
EOT
  default = {
    "mmo.log/format"     = "json"
    "mmo.log/stdout"     = "true"
    "mmo.log/deployment" = "mmo-backend"
  }
}

variable "service_monitor_enabled" {
  type        = bool
  description = "Создавать ServiceMonitor (prometheus-operator) для scrape /metrics."
  default     = true
}

variable "prometheus_service_monitor_labels" {
  type        = map(string)
  description = "metadata.labels на ServiceMonitor (должны совпадать с serviceMonitorSelector у Prometheus CR; пустая map — без доп. labels)."
  default     = {}
}
