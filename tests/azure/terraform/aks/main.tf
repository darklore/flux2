terraform {
  backend "azurerm" {
    resource_group_name  = "terraform-state"
    storage_account_name = "terraformstate0419"
    container_name       = "aks-tfstate"
    key                  = "prod.terraform.tfstate"
  }

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "2.73.0"
    }
    azuread = {
      source = "hashicorp/azuread"
      version = "1.6.0"
    }
  }
}

provider "azurerm" {
  features {}
}

provider "kubernetes" {
  host                   = azurerm_kubernetes_cluster.this.kube_config[0].host
  client_certificate     = base64decode(azurerm_kubernetes_cluster.this.kube_config[0].client_certificate)
  client_key             = base64decode(azurerm_kubernetes_cluster.this.kube_config[0].client_key)
  cluster_ca_certificate = base64decode(azurerm_kubernetes_cluster.this.kube_config[0].cluster_ca_certificate)
}

data "azurerm_client_config" "current" {}

# Shared resources between runs
locals {
  shared_suffix = "oarfish"
}

data "azurerm_resource_group" "shared" {
  name = "e2e-shared"
}

data "azurerm_container_registry" "this" {
  name                = "acrapps${local.shared_suffix}"
  resource_group_name = data.azurerm_resource_group.shared.name
}

data "azurerm_key_vault" "shared" {
  resource_group_name = data.azurerm_resource_group.shared.name
  name                = "kv-credentials-${local.shared_suffix}"
}

data "azurerm_key_vault_secret" "shared_pat" {
  key_vault_id = data.azurerm_key_vault.shared.id
  name         = "pat"
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
resource "random_pet" "suffix" {}

locals {
  name_suffix = "e2e-${random_pet.suffix.id}"
}

resource "azurerm_resource_group" "this" {
  name     = "rg-${local.name_suffix}"
  location = "West Europe"

  tags = {
    environment = "e2e"
  }
}
