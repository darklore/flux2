resource "kubernetes_namespace" "flux_system" {
  metadata {
    name = "flux-system"
  }

  lifecycle {
    ignore_changes = [
      metadata[0].labels,
      metadata[0].annotations,
    ]
  }
}

# SAS Secret for eventhub
resource "kubernetes_secret" "flux_azure_event_hub_sas" {
  metadata {
    name = "azure-event-hub-sas"
    namespace = "flux-system"
  }

  type = "Opaque"

  data = {
    address = azurerm_eventhub_authorization_rule.this.primary_connection_string
  }
}

# Service Principal for SOPS and ACR
resource "azuread_application" "flux" {
  display_name = "flux-${local.name_suffix}"

  required_resource_access {
    resource_app_id = "00000003-0000-0000-c000-000000000000"

    resource_access {
      id   = "df021288-bdef-4463-88db-98f22de89214"
      type = "Role"
    }
  }

  required_resource_access {
    resource_app_id = "00000002-0000-0000-c000-000000000000"

    resource_access {
      id   = "1cda74f2-2616-4834-b122-5cb1b07f8a59"
      type = "Role"
    }
    resource_access {
      id   = "78c8a3c8-a07e-4b9e-af1b-b5ccab50a175"
      type = "Role"
    }
  }
}

resource "azuread_service_principal" "flux" {
  application_id = azuread_application.flux.application_id
}

resource "azuread_service_principal_password" "flux" {
  service_principal_id = azuread_service_principal.flux.object_id
}

resource "azurerm_role_assignment" "acr" {
  scope                = data.azurerm_container_registry.this.id
  role_definition_name = "AcrPull"
  principal_id         = azuread_service_principal.flux.object_id
}

# Service Principal secret in different required formats
resource "kubernetes_secret" "flux_azure_sp" {
  metadata {
    name = "azure-sp"
    namespace = "flux-system"
  }

  type = "Opaque"

  data = {
    AZURE_TENANT_ID = data.azurerm_client_config.current.tenant_id
    AZURE_CLIENT_ID = azuread_service_principal.flux.application_id
    AZURE_CLIENT_SECRET = azuread_service_principal_password.flux.value
  }
}

resource "kubernetes_secret" "flux_acr_helm" {
  metadata {
    name = "acr-helm"
    namespace = "flux-system"
  }

  type = "Opaque"

  data = {
    username = azuread_service_principal.flux.application_id
    password = azuread_service_principal_password.flux.value
  }
}

resource "kubernetes_secret" "flux_acr_docker" {
  metadata {
    name = "acr-docker"
    namespace = "flux-system"
  }

  data = {
    ".dockerconfigjson" = <<DOCKER
{
  "auths": {
    "${data.azurerm_container_registry.this.login_server}": {
      "auth": "${base64encode("${azuread_service_principal.flux.application_id}:${azuread_service_principal_password.flux.value}")}"
    }
  }
}
DOCKER
  }

  type = "kubernetes.io/dockerconfigjson"
}
