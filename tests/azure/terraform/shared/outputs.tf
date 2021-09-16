output "azure_devops_sp" {
  value = {
    client_id = azuread_service_principal.azure_devops.application_id
    client_secret = azuread_service_principal_password.azure_devops.value
  }
  sensitive = true
}

output "github_sp" {
  value = {
    client_id = azuread_service_principal.github.application_id
    client_secret = azuread_service_principal_password.github.value
  }
  sensitive = true
}
