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
    azuredevops = {
      source = "microsoft/azuredevops"
      version = "0.1.7"
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

provider "azuredevops" {
  org_service_url = "https://dev.azure.com/flux-azure"
  personal_access_token = data.azurerm_key_vault_secret.shared_pat.value
}

data "azurerm_client_config" "current" {}

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
