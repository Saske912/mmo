data "terraform_remote_state" "staging" {
  backend = "kubernetes"

  config = {
    secret_suffix = var.remote_state_secret_suffix
    namespace     = var.remote_state_namespace
    config_path   = pathexpand(var.kube_config_path)
  }
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

  # Совпадает с metadata.name у kubernetes_namespace (var.namespace).
  cell_grpc_advertise = "${var.cell_service_name}.${var.namespace}.svc.cluster.local:${var.cell_grpc_port}"
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
        labels = {
          app = "grid-manager"
        }
      }

      spec {
        container {
          name              = "grid-manager"
          image             = local.image
          image_pull_policy = var.image_pull_policy

          command = ["/grid-manager"]
          args = [
            "-listen", "0.0.0.0:${var.grid_grpc_port}",
            "-backend", "auto",
          ]

          port {
            name           = "grpc"
            container_port = var.grid_grpc_port
          }

          env_from {
            secret_ref {
              name = kubernetes_secret.mmo_backend.metadata[0].name
            }
          }
        }
      }
    }
  }
}

resource "kubernetes_deployment" "cell_node" {
  metadata {
    name      = "cell-node"
    namespace = kubernetes_namespace.mmo.metadata[0].name
    labels = {
      app = "cell-node"
    }
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app = "cell-node"
      }
    }

    template {
      metadata {
        labels = {
          app = "cell-node"
        }
      }

      spec {
        container {
          name              = "cell-node"
          image             = local.image
          image_pull_policy = var.image_pull_policy

          command = ["/cell-node"]
          args = concat(
            ["-listen", "0.0.0.0:${var.cell_grpc_port}", "-id", var.cell_id],
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
            value = local.cell_grpc_advertise
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
  }
}

resource "kubernetes_service" "cell" {
  metadata {
    name      = var.cell_service_name
    namespace = kubernetes_namespace.mmo.metadata[0].name
  }

  spec {
    selector = {
      app = "cell-node"
    }

    port {
      name        = "grpc"
      port        = var.cell_grpc_port
      target_port = var.cell_grpc_port
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
        labels = {
          app = "gateway"
        }
      }

      spec {
        container {
          name  = "gateway"
          image = local.image
          # Тег образа по коммиту переиспользуется при -dirty; Always подтягивает свежий push из Harbor.
          image_pull_policy = "Always"

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

          env_from {
            secret_ref {
              name = kubernetes_secret.mmo_backend.metadata[0].name
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
