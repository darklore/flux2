terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "2.73.0"
    }
  }
}

provider "azurerm" {
  features {}
}

resource "random_pet" "suffix" {
  length = 1
}

data "azurerm_client_config" "current" {}

resource "azurerm_resource_group" "this" {
  name     = "azure-e2e-shared"
  location = "West Europe"
}

resource "azurerm_key_vault" "this" {
  name                = "kv-credentials-${random_pet.suffix.id}"
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  tenant_id           = data.azurerm_client_config.current.tenant_id
  sku_name            = "standard"

  access_policy {
    tenant_id = data.azurerm_client_config.current.tenant_id
    object_id = data.azurerm_client_config.current.object_id

    key_permissions = [
      "Backup",
      "Create",
      "Decrypt",
      "Delete",
      "Encrypt",
      "Get",
      "Import",
      "List",
      "Purge",
      "Recover",
      "Restore",
      "Sign",
      "UnwrapKey",
      "Update",
      "Verify",
      "WrapKey",
    ]

    secret_permissions = [
      "Backup",
      "Delete",
      "Get",
      "List",
      "Purge",
      "Recover",
      "Restore",
      "Set",
    ]
  }
}

resource "azurerm_key_vault_key" "sops" {
  name         = "sops"
  key_vault_id = azurerm_key_vault.this.id
  key_type     = "RSA"
  key_size     = 2048

  key_opts = [
    "decrypt",
    "encrypt",
  ]
}

resource "azurerm_container_registry" "this" {
  name                = "acrapps${random_pet.suffix.id}"
  resource_group_name = azurerm_resource_group.this.name
  location            = azurerm_resource_group.this.location
  sku = "standard"
}
