resource "azurerm_container_registry" "this" {
  name                = "acrapps${random_pet.suffix.id}"
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  sku                 = "standard"
}

resource "azuread_application" "azure_devops" {
  name = "azure-devops-${random_pet.suffix.id}"

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

resource "azuread_service_principal" "azure_devops" {
  application_id = azuread_application.azure_devops.application_id
}

resource "azuread_service_principal_password" "azure_devops" {
  service_principal_id = azuread_service_principal.azure_devops.object_id
}

resource "azurerm_role_assignment" "acr" {
  scope                = azurerm_container_registry.this.id
  role_definition_name = "Contributor"
  principal_id         = azuread_service_principal.azure_devops.object_id
}
