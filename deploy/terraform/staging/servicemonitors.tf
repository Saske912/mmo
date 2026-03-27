# ServiceMonitor: prometheus-operator (monitoring.coreos.com/v1).
# Подберите prometheus_service_monitor_labels под ваш Prometheus CR (serviceMonitorSelector / matchLabels).

locals {
  servicemonitor_ns = kubernetes_namespace.mmo.metadata[0].name
}

resource "kubernetes_manifest" "servicemonitor_gateway" {
  count = var.service_monitor_enabled ? 1 : 0

  manifest = {
    apiVersion = "monitoring.coreos.com/v1"
    kind       = "ServiceMonitor"
    metadata = {
      name      = "${var.gateway_service_name}-metrics"
      namespace = local.servicemonitor_ns
      labels    = var.prometheus_service_monitor_labels
    }
    spec = {
      selector = {
        matchLabels = {
          app = "gateway"
        }
      }
      endpoints = [
        {
          port     = "http"
          path     = "/metrics"
          scheme   = "http"
          interval = "15s"
        }
      ]
    }
  }
}

resource "kubernetes_manifest" "servicemonitor_cell" {
  count = var.service_monitor_enabled && var.cell_metrics_port > 0 ? 1 : 0

  manifest = {
    apiVersion = "monitoring.coreos.com/v1"
    kind       = "ServiceMonitor"
    metadata = {
      name      = "${var.cell_service_name}-metrics"
      namespace = local.servicemonitor_ns
      labels    = var.prometheus_service_monitor_labels
    }
    spec = {
      selector = {
        matchLabels = {
          app = "cell-node"
        }
      }
      endpoints = [
        {
          port     = "metrics"
          path     = "/metrics"
          scheme   = "http"
          interval = "15s"
        }
      ]
    }
  }
}

resource "kubernetes_manifest" "servicemonitor_grid_manager" {
  count = var.service_monitor_enabled && var.grid_metrics_port > 0 ? 1 : 0

  manifest = {
    apiVersion = "monitoring.coreos.com/v1"
    kind       = "ServiceMonitor"
    metadata = {
      name      = "${var.grid_service_name}-metrics"
      namespace = local.servicemonitor_ns
      labels    = var.prometheus_service_monitor_labels
    }
    spec = {
      selector = {
        matchLabels = {
          app = "grid-manager"
        }
      }
      endpoints = [
        {
          port     = "metrics"
          path     = "/metrics"
          scheme   = "http"
          interval = "15s"
        }
      ]
    }
  }
}
