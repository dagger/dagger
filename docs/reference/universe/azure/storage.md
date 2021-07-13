---
sidebar_label: storage
---

# alpha.dagger.io/azure/storage

```cue
import "alpha.dagger.io/azure/storage"
```

## storage.#StorageAccount

Create a storage account

### storage.#StorageAccount Inputs

| Name                      | Type                | Description                                     |
| -------------             |:-------------:      |:-------------:                                  |
|*config.tenantId*          | `dagger.#Secret`    |AZURE tenant id                                  |
|*config.subscriptionId*    | `dagger.#Secret`    |AZURE subscription id                            |
|*config.appId*             | `dagger.#Secret`    |AZURE app id for the service principal used      |
|*config.password*          | `dagger.#Secret`    |AZURE password for the service principal used    |
|*rgName*                   | `string`            |ResourceGroup name                               |
|*accountName*              | `string`            |StorageAccount name                              |
|*accountLocation*          | `string`            |StorageAccount location                          |

### storage.#StorageAccount Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*id*              | `string`          |-                   |
