terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "2.65.0"
    }
  }
}

provider "azurerm" {
  features {}
}

data "azurerm_client_config" "current" {}

# Shared resources between runs
data "azurerm_resource_group" "shared" {
  name = "azure-e2e-shared"
}

data "azurerm_key_vault" "shared" {
  resource_group_name = data.azurerm_resource_group.shared.name
  name                = "flux-e2e"
}

data "azurerm_key_vault_secret" "shared_pat" {
  key_vault_id = data.azurerm_key_vault.shared.id
  name         = "azure-e2e-flux"
}

data "azurerm_key_vault_secret" "shared_id_rsa" {
  key_vault_id = data.azurerm_key_vault.shared.id
  name         = "id-rsa"
}

data "azurerm_key_vault_secret" "shared_id_rsa_pub" {
  key_vault_id = data.azurerm_key_vault.shared.id
  name         = "id-rsa-pub"
}

# Temporary resource group
resource "random_pet" "prefix" {}

locals {
  name_prefix = "${random_pet.prefix.id}-e2e"
}

resource "azurerm_resource_group" "this" {
  name     = "${local.name_prefix}-rg"
  location = "West Europe"

  tags = {
    environment = "e2e"
  }
}
