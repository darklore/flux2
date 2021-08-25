# Azure E2E

E2E tests for Azure are needed to mitigate introduction of new bugs in dependencies like libgit2. The goal is to verify that Flux integration with
Azure services are actually working now and in the future.

## Tests

[x] Flux can be successfully installed on AKS using the CLI e.g.:
[x] source-controller can clone Azure DevOps repositories (https+ssh)
[ ] source-controller can pull charts from Azure Container Registry Helm repositories
[ ] image-reflector-controller can list tags from Azure Container Registry image repositories
[ ] image-automation-controller can create branches and push to Azure DevOps repositories (https+ssh)
[ ] kustomize-controller can decrypt secrets using SOPS and Azure Key Vault
[ ] notification-controller can send commit status to Azure DevOps
[ ] notification-controller can forward events to Azure Event Hub
[ ] Network policies do not work with Azure CNI. (flux install --components-extra=image-reflector-controller,image-automation-controller --network-policy=false)

## Architecture

The tests are designed so that as little computation has to be done when running the tests. The majority of Git repositories and Helm Charts should
already be created.



## Running
