output "shared_pat" {
  sensitive = true
  value = data.azurerm_key_vault_secret.shared_pat.value
}

output "shared_id_rsa" {
  sensitive = true
  value = data.azurerm_key_vault_secret.shared_id_rsa.value
}

output "shared_id_rsa_pub" {
  sensitive = true
  value = data.azurerm_key_vault_secret.shared_id_rsa_pub.value
}

output "shared_sops_id" {
  value = data.azurerm_key_vault_key.sops.id
}

output "acr_url" {
  value = data.azurerm_container_registry.shared.login_server
}

output "aks_kube_config" {
  sensitive = true
  value = azurerm_kubernetes_cluster.this.kube_config_raw
}

output "aks_host" {
  value = azurerm_kubernetes_cluster.this.kube_config[0].host
}

output "aks_client_certificate" {
  value = base64decode(azurerm_kubernetes_cluster.this.kube_config[0].client_certificate)
}

output "aks_client_key" {
  value = base64decode(azurerm_kubernetes_cluster.this.kube_config[0].client_key)
}

output "aks_cluster_ca_certificate" {
  value = base64decode(azurerm_kubernetes_cluster.this.kube_config[0].cluster_ca_certificate)
}

output "fleet_infra_repository_url" {
  value = azuredevops_git_repository.fleet_infra.remote_url
}

output "application_repository_url" {
  value = azuredevops_git_repository.application.remote_url
}
