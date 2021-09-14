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

| Name                                               | Type                                                                                                            | Description                                             |
| -------------                                      |:-------------:                                                                                                  |:-------------:                                          |
|*config.tenantId*                                   | `dagger.#Secret`                                                                                                |AZURE tenant id                                          |
|*config.subscriptionId*                             | `dagger.#Secret`                                                                                                |AZURE subscription id                                    |
|*config.appId*                                      | `dagger.#Secret`                                                                                                |AZURE app id for the service principal used              |
|*config.password*                                   | `dagger.#Secret`                                                                                                |AZURE password for the service principal used            |
|*rgName*                                            | `string`                                                                                                        |ResourceGroup name                                       |
|*stLocation*                                        | `string`                                                                                                        |StorageAccount location                                  |
|*stName*                                            | `string`                                                                                                        |StorageAccount name                                      |
|*ctr.image.config.tenantId*                         | `dagger.#Secret`                                                                                                |AZURE tenant id                                          |
|*ctr.image.config.subscriptionId*                   | `dagger.#Secret`                                                                                                |AZURE subscription id                                    |
|*ctr.image.config.appId*                            | `dagger.#Secret`                                                                                                |AZURE app id for the service principal used              |
|*ctr.image.config.password*                         | `dagger.#Secret`                                                                                                |AZURE password for the service principal used            |
|*ctr.image.image.from*                              | `mcr.microsoft.com/azure-cli:2.27.1@sha256:1e117183100c9fce099ebdc189d73e506e7b02d2b73d767d3fc07caee72f9fb1`    |Remote ref (example: "index.docker.io/alpine:latest")    |
|*ctr.image.secret."/run/secrets/appId"*             | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.image.secret."/run/secrets/password"*          | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.image.secret."/run/secrets/tenantId"*          | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.image.secret."/run/secrets/subscriptionId"*    | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.env.AZURE_DEFAULTS_GROUP*                      | `string`                                                                                                        |-                                                        |
|*ctr.env.AZURE_DEFAULTS_LOCATION*                   | `string`                                                                                                        |-                                                        |
|*ctr.env.AZURE_STORAGE_ACCOUNT*                     | `string`                                                                                                        |-                                                        |

### storage.#StorageAccount Outputs

| Name             | Type              | Description         |
| -------------    |:-------------:    |:-------------:      |
|*id*              | `string`          |StorageAccount Id    |
