---
sidebar_label: resourcegroup
---

# alpha.dagger.io/azure/resourcegroup

```cue
import "alpha.dagger.io/azure/resourcegroup"
```

## resourcegroup.#ResourceGroup

Create a new resource group.

### resourcegroup.#ResourceGroup Inputs

| Name                      | Type                | Description                                     |
| -------------             |:-------------:      |:-------------:                                  |
|*config.tenantId*          | `dagger.#Secret`    |AZURE tenant id                                  |
|*config.subscriptionId*    | `dagger.#Secret`    |AZURE subscription id                            |
|*config.appId*             | `dagger.#Secret`    |AZURE app id for the service principal used      |
|*config.password*          | `dagger.#Secret`    |AZURE password for the service principal used    |
|*rgName*                   | `string`            |ResourceGroup name                               |
|*rgLocation*               | `string`            |ResourceGroup location                           |

### resourcegroup.#ResourceGroup Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*id*              | `string`          |-                   |
