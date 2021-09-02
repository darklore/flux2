output "azure_devops_client_id" {
  value = azuread_service_principal.azure_devops.application_id
}

output "azure_devops_client_secret" {
  value = azuread_service_principal_password.azure_devops.value
  sensitive = true
}

