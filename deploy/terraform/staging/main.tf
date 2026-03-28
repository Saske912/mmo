data "terraform_remote_state" "staging" {
  backend = "kubernetes"

  config = {
    secret_suffix = var.remote_state_secret_suffix
    namespace     = var.remote_state_namespace
    config_path   = pathexpand(var.kube_config_path)
  }
}

# Миграция: одиночные ресурсы → for_each с ключом primary (без пересоздания при первом apply после апгрейда).
moved {
  from = kubernetes_deployment.cell_node
  to   = kubernetes_deployment.cell_node["primary"]
}

moved {
  from = kubernetes_service.cell
  to   = kubernetes_service.cell["primary"]
}

locals {
  mmo    = data.terraform_remote_state.staging.outputs.mmo
  harbor = try(local.mmo.harbor, null)

  pg     = try(local.mmo.pgsql, null)
  redis  = try(local.mmo.redis, null)
  nats   = try(local.mmo.nats, null)
  consul = try(local.mmo.consul, null)
  mon    = try(local.mmo.monitoring, null)

  registry_host_raw = trimspace(
    var.image_registry_override != "" ? var.image_registry_override : try(local.harbor.registry_host, ""),
  )
  registry_host_normalized = trim(
    replace(replace(local.registry_host_raw, "https://", ""), "http://", ""),
    "/",
  )

  use_harbor_image = local.registry_host_normalized != ""

  # На Harbor один тег может указывать на новый digest; IfNotPresent держит старый образ на ноде → несовпадение флагов/бинарника.
  pull_policy = local.use_harbor_image ? "Always" : var.image_pull_policy

  image = trimspace(var.image_override) != "" ? trimspace(var.image_override) : (
    local.use_harbor_image ? "${local.registry_host_normalized}/${var.harbor_project}/${var.image_repository}:${var.image_tag}" : "${var.image_repository}:${var.image_tag}"
  )

  harbor_user = trimspace(var.harbor_docker_username) != "" ? trimspace(var.harbor_docker_username) : try(local.harbor.docker_login_username, "")
  harbor_pass = trimspace(var.harbor_docker_password) != "" ? trimspace(var.harbor_docker_password) : try(local.harbor.docker_login_password, "")

  # Строки для Secret (совпадают с mmo_remote_state.tf.example и internal/config).
  secret_strings = {
    CONSUL_HTTP_ADDR = try(local.consul.http_api_base_url, "")
    CONSUL_DNS_ADDR  = try(local.consul.dns_addr, "")
    NATS_URL         = try(local.nats.urls, "")
    DATABASE_URL_RW  = try(local.pg.url_rw, "")
    REDIS_ADDR       = try(local.redis.redis_addr, "")
    REDIS_PASSWORD   = try(local.redis.password, "")
    LOKI_PUSH_URL    = try(local.mon.loki.push_url, "")
    TEMPO_OTLP_GRPC  = try(local.mon.tempo.otlp_grpc, "")
    HARBOR_REGISTRY  = local.registry_host_raw
    HARBOR_USER      = local.harbor_user
    HARBOR_PASSWORD  = local.harbor_pass
    GATEWAY_JWT_SECRET = var.gateway_jwt_secret
  }

  # DNS gRPC для регистрации в Consul: primary → legacy имя Service (mmo-cell), остальные — mmo-cell-<key>.
  cell_grpc_advertise = {
    for key, _ in var.cell_instances : key => (
      key == "primary" ?
      "${var.cell_service_name}.${var.namespace}.svc.cluster.local:${var.cell_grpc_port}" :
      "${var.cell_service_name}-${key}.${var.namespace}.svc.cluster.local:${var.cell_grpc_port}"
    )
  }

  # Стабильные селекторы для пайплайна Loki/Alloy (stdout + JSON при mmo_structured_logs).
  mmo_log_pod_labels = var.mmo_structured_logs ? var.mmo_loki_log_labels : {}
}

resource "kubernetes_namespace" "mmo" {
  metadata {
    name = var.namespace
  }
}

resource "kubernetes_secret" "mmo_backend" {
  metadata {
    name      = var.secret_name
    namespace = kubernetes_namespace.mmo.metadata[0].name
  }
  type = "Opaque"
  data = local.secret_strings

  wait_for_service_account_token = false
}

resource "kubernetes_deployment" "grid_manager" {
  metadata {
    name      = "grid-manager"
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app = "grid-manager"
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app = "grid-manager"
      }
    }

    template {
      metadata {
        labels = merge(
          {
            app = "grid-manager"
          },
          local.mmo_log_pod_labels,
        )
      }

      spec {
        container {
          name              = "grid-manager"
          image             = local.image
          image_pull_policy = local.pull_policy

          command = ["/grid-manager"]
          args = concat(
            ["-listen", "0.0.0.0:${var.grid_grpc_port}", "-backend", "auto"],
            var.grid_metrics_port > 0 ? ["-metrics-listen", "0.0.0.0:${var.grid_metrics_port}"] : [],
          )

          port {
            name           = "grpc"
            container_port = var.grid_grpc_port
          }

          dynamic "port" {
            for_each = var.grid_metrics_port > 0 ? [var.grid_metrics_port] : []
            iterator = metrics
            content {
              name           = "metrics"
              container_port = metrics.value
            }
          }

          env_from {
            secret_ref {
              name = kubernetes_secret.mmo_backend.metadata[0].name
            }
          }

          dynamic "env" {
            for_each = var.mmo_structured_logs ? [1] : []
            content {
              name  = "MMO_LOG_FORMAT"
              value = "json"
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_deployment" "cell_node" {
  for_each = var.cell_instances

  metadata {
    name      = each.key == "primary" ? "cell-node" : "cell-node-${each.key}"
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app        = "cell-node"
      cell_shard = each.key
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app        = "cell-node"
        cell_shard = each.key
      }
    }

    template {
      metadata {
        labels = merge(
          {
            app        = "cell-node"
            cell_shard = each.key
          },
          local.mmo_log_pod_labels,
        )
      }

      spec {
        container {
          name              = "cell-node"
          image             = local.image
          image_pull_policy = local.pull_policy

          command = ["/cell-node"]
          args = concat(
            [
              "-listen", "0.0.0.0:${var.cell_grpc_port}",
              "-id", each.value.id,
              "-level", tostring(each.value.level),
              "-xmin", tostring(each.value.xmin),
              "-xmax", tostring(each.value.xmax),
              "-zmin", tostring(each.value.zmin),
              "-zmax", tostring(each.value.zmax),
            ],
            var.cell_metrics_port > 0 ? ["-metrics-listen", "0.0.0.0:${var.cell_metrics_port}"] : [],
          )

          port {
            name           = "grpc"
            container_port = var.cell_grpc_port
          }

          dynamic "port" {
            for_each = var.cell_metrics_port > 0 ? [var.cell_metrics_port] : []
            iterator = metrics
            content {
              name           = "metrics"
              container_port = metrics.value
            }
          }

          env_from {
            secret_ref {
              name = kubernetes_secret.mmo_backend.metadata[0].name
            }
          }

          env {
            name  = "MMO_CELL_GRPC_ADVERTISE"
            value = local.cell_grpc_advertise[each.key]
          }

          dynamic "env" {
            for_each = var.mmo_structured_logs ? [1] : []
            content {
              name  = "MMO_LOG_FORMAT"
              value = "json"
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_service" "grid_manager" {
  metadata {
    name      = var.grid_service_name
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app = "grid-manager"
    }
  }

  spec {
    selector = {
      app = "grid-manager"
    }

    port {
      name        = "grpc"
      port        = var.grid_grpc_port
      target_port = var.grid_grpc_port
    }

    dynamic "port" {
      for_each = var.grid_metrics_port > 0 ? [var.grid_metrics_port] : []
      iterator = metrics
      content {
        name        = "metrics"
        port        = metrics.value
        target_port = metrics.value
      }
    }
  }
}

resource "kubernetes_service" "cell" {
  for_each = var.cell_instances

  metadata {
    name      = each.key == "primary" ? var.cell_service_name : "${var.cell_service_name}-${each.key}"
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app        = "cell-node"
      cell_shard = each.key
    }
  }

  spec {
    selector = {
      app        = "cell-node"
      cell_shard = each.key
    }

    port {
      name        = "grpc"
      port        = var.cell_grpc_port
      target_port = var.cell_grpc_port
    }

    dynamic "port" {
      for_each = var.cell_metrics_port > 0 ? [var.cell_metrics_port] : []
      iterator = metrics
      content {
        name        = "metrics"
        port        = metrics.value
        target_port = metrics.value
      }
    }
  }
}

resource "kubernetes_deployment" "gateway" {
  metadata {
    name      = "gateway"
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app = "gateway"
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app = "gateway"
      }
    }

    template {
      metadata {
        labels = merge(
          {
            app = "gateway"
          },
          local.mmo_log_pod_labels,
        )
      }

      spec {
        container {
          name              = "gateway"
          image             = local.image
          image_pull_policy = local.pull_policy

          command = ["/gateway"]
          args = [
            "-listen", "0.0.0.0:${var.gateway_http_port}",
            "-registry", "${var.grid_service_name}.${var.namespace}.svc.cluster.local:${var.grid_grpc_port}",
            "-resolve-x", "0",
            "-resolve-z", "0",
          ]

          port {
            name           = "http"
            container_port = var.gateway_http_port
          }

          liveness_probe {
            http_get {
              path = "/healthz"
              port = "http"
            }
            initial_delay_seconds = 5
            period_seconds        = 10
          }

          readiness_probe {
            http_get {
              path = "/readyz"
              port = "http"
            }
            initial_delay_seconds = 5
            period_seconds        = 10
            failure_threshold     = 3
          }

          env_from {
            secret_ref {
              name = kubernetes_secret.mmo_backend.metadata[0].name
            }
          }

          dynamic "env" {
            for_each = var.mmo_structured_logs ? [1] : []
            content {
              name  = "MMO_LOG_FORMAT"
              value = "json"
            }
          }

          dynamic "env" {
            for_each = var.gateway_skip_db_migrations ? [1] : []
            content {
              name  = "GATEWAY_SKIP_DB_MIGRATIONS"
              value = "true"
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_service" "gateway" {
  metadata {
    name      = var.gateway_service_name
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app = "gateway"
    }
  }

  spec {
    selector = {
      app = "gateway"
    }

    port {
      name        = "http"
      port        = var.gateway_http_port
      target_port = var.gateway_http_port
    }
  }
}

# TLS из PEM в каталоге certs/ (Let's Encrypt / certbot; см. certs/README).
resource "kubernetes_secret" "gateway_tls" {
  count = var.gateway_ingress_enabled ? 1 : 0

  metadata {
    name      = "${var.gateway_service_name}-tls"
    namespace = kubernetes_namespace.mmo.metadata[0].name
  }

  type = "kubernetes.io/tls"

  data = {
    "tls.crt" = file("${path.module}/${var.gateway_tls_fullchain_file}")
    "tls.key" = file("${path.module}/${var.gateway_tls_privkey_file}")
  }

  wait_for_service_account_token = false
}

resource "kubernetes_ingress_v1" "gateway" {
  count = var.gateway_ingress_enabled ? 1 : 0

  metadata {
    name      = "${var.gateway_service_name}-ingress"
    namespace = kubernetes_namespace.mmo.metadata[0].name
    annotations = {
      # Долгоживущий WebSocket через NGINX Ingress Controller.
      "nginx.ingress.kubernetes.io/proxy-read-timeout"  = "3600"
      "nginx.ingress.kubernetes.io/proxy-send-timeout"    = "3600"
      "nginx.ingress.kubernetes.io/proxy-connect-timeout" = "60"
    }
  }

  spec {
    ingress_class_name = trimspace(var.gateway_ingress_class_name) != "" ? var.gateway_ingress_class_name : null

    tls {
      hosts       = [var.gateway_ingress_host]
      secret_name = kubernetes_secret.gateway_tls[0].metadata[0].name
    }

    rule {
      host = var.gateway_ingress_host
      http {
        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = kubernetes_service.gateway.metadata[0].name
              port {
                number = var.gateway_http_port
              }
            }
          }
        }
      }
    }
  }
}
