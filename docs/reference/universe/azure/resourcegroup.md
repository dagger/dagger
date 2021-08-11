---
sidebar_label: resourcegroup
---

# alpha.dagger.io/azure/resourcegroup

```cue
import "alpha.dagger.io/azure/resourcegroup"
```

## resourcegroup.#ResourceGroup

Create a resource group

### resourcegroup.#ResourceGroup Inputs

| Name                                | Type                               | Description                                             |
| -------------                       |:-------------:                     |:-------------:                                          |
|*config.tenantId*                    | `dagger.#Secret`                   |AZURE tenant id                                          |
|*config.subscriptionId*              | `dagger.#Secret`                   |AZURE subscription id                                    |
|*config.appId*                       | `dagger.#Secret`                   |AZURE app id for the service principal used              |
|*config.password*                    | `dagger.#Secret`                   |AZURE password for the service principal used            |
|*rgName*                             | `string`                           |ResourceGroup name                                       |
|*rgLocation*                         | `string`                           |ResourceGroup location                                   |
|*ctr.image.config.tenantId*          | `dagger.#Secret`                   |AZURE tenant id                                          |
|*ctr.image.config.subscriptionId*    | `dagger.#Secret`                   |AZURE subscription id                                    |
|*ctr.image.config.appId*             | `dagger.#Secret`                   |AZURE app id for the service principal used              |
|*ctr.image.config.password*          | `dagger.#Secret`                   |AZURE password for the service principal used            |
|*ctr.image.image.from*               | `"mcr.microsoft.com/azure-cli"`    |Remote ref (example: "index.docker.io/alpine:latest")    |

### resourcegroup.#ResourceGroup Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*id*              | `string`          |Resource Id         |
