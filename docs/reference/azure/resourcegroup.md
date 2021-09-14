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

| Name                                               | Type                                                                                                            | Description                                             |
| -------------                                      |:-------------:                                                                                                  |:-------------:                                          |
|*config.tenantId*                                   | `dagger.#Secret`                                                                                                |AZURE tenant id                                          |
|*config.subscriptionId*                             | `dagger.#Secret`                                                                                                |AZURE subscription id                                    |
|*config.appId*                                      | `dagger.#Secret`                                                                                                |AZURE app id for the service principal used              |
|*config.password*                                   | `dagger.#Secret`                                                                                                |AZURE password for the service principal used            |
|*rgName*                                            | `string`                                                                                                        |ResourceGroup name                                       |
|*rgLocation*                                        | `string`                                                                                                        |ResourceGroup location                                   |
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

### resourcegroup.#ResourceGroup Outputs

| Name             | Type              | Description                    |
| -------------    |:-------------:    |:-------------:                 |
|*id*              | `string`          |ResourceGroup Id Resource Id    |
