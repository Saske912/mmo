output "namespace" {
  value = kubernetes_namespace.mmo.metadata[0].name
}

output "loki_logql_base" {
  description = "Базовый LogQL-селектор по namespace MMO; при MMO_LOG_FORMAT=json строки в stdout парсятся как JSON приложением."
  value       = "{namespace=\"${var.namespace}\"}"
}

output "loki_pod_label_selectors" {
  description = "Ключи labels на подах (при mmo_structured_logs=true) для relabel/log stream filters."
  value       = var.mmo_structured_logs ? var.mmo_loki_log_labels : {}
}

output "grid_manager_grpc" {
  value = "${var.grid_service_name}.${var.namespace}.svc.cluster.local:${var.grid_grpc_port}"
}

output "cell_grpc_advertise" {
  description = "gRPC DNS для primary-соты (обратная совместимость со скриптами)."
  value       = local.cell_grpc_advertise["primary"]
}

output "cell_grpc_advertise_by_shard" {
  description = "gRPC DNS по ключу cell_instances (primary, …)."
  value       = local.cell_grpc_advertise
}

output "gateway_http" {
  description = "Внутренний URL gateway (ClusterIP) для port-forward или сервис-mesh."
  value       = "http://${var.gateway_service_name}.${var.namespace}.svc.cluster.local:${var.gateway_http_port}"
}

output "gateway_ingress_host" {
  description = "Публичный hostname Ingress (если включён gateway_ingress_enabled)."
  value       = var.gateway_ingress_enabled ? var.gateway_ingress_host : null
}

output "gateway_public_url" {
  description = "HTTPS URL gateway через Ingress (сессия/WS снаружи кластера)."
  value       = var.gateway_ingress_enabled ? "https://${var.gateway_ingress_host}" : null
}

output "image_tag" {
  description = "var.image_tag (синхрон с локальным IMAGE_TAG при make tofu-apply / harbor-push)."
  value       = var.image_tag
}

output "container_image" {
  description = "Итоговый reference образа (Harbor или локальный)."
  value       = local.image
}

output "harbor_registry_hostname" {
  description = "Хост реестра для docker login / docker push (без схемы и пути)."
  value       = local.registry_host_normalized
}

output "harbor_docker_username" {
  description = "Учётная запись для docker push (make harbor-login) и env приложения."
  value       = local.harbor_user
  sensitive   = true
}

output "harbor_docker_password" {
  description = "Пароль для docker push; не используется для pull подов (реестр на уровне кластера)."
  value       = local.harbor_pass
  sensitive   = true
}
