---
sidebar_label: azure
---

# alpha.dagger.io/azure

Azure base package

```cue
import "alpha.dagger.io/azure"
```

## azure.#CLI

Azure Cli to be used by all Azure packages

### azure.#CLI Inputs

| Name                                     | Type                                                                                                            | Description                                             |
| -------------                            |:-------------:                                                                                                  |:-------------:                                          |
|*config.tenantId*                         | `dagger.#Secret`                                                                                                |AZURE tenant id                                          |
|*config.subscriptionId*                   | `dagger.#Secret`                                                                                                |AZURE subscription id                                    |
|*config.appId*                            | `dagger.#Secret`                                                                                                |AZURE app id for the service principal used              |
|*config.password*                         | `dagger.#Secret`                                                                                                |AZURE password for the service principal used            |
|*image.from*                              | `mcr.microsoft.com/azure-cli:2.27.1@sha256:1e117183100c9fce099ebdc189d73e506e7b02d2b73d767d3fc07caee72f9fb1`    |Remote ref (example: "index.docker.io/alpine:latest")    |
|*secret."/run/secrets/appId"*             | `dagger.#Secret`                                                                                                |-                                                        |
|*secret."/run/secrets/password"*          | `dagger.#Secret`                                                                                                |-                                                        |
|*secret."/run/secrets/tenantId"*          | `dagger.#Secret`                                                                                                |-                                                        |
|*secret."/run/secrets/subscriptionId"*    | `dagger.#Secret`                                                                                                |-                                                        |

### azure.#CLI Outputs

_No output._

## azure.#Config

Azure Config shared by all Azure packages

### azure.#Config Inputs

| Name               | Type                | Description                                     |
| -------------      |:-------------:      |:-------------:                                  |
|*tenantId*          | `dagger.#Secret`    |AZURE tenant id                                  |
|*subscriptionId*    | `dagger.#Secret`    |AZURE subscription id                            |
|*appId*             | `dagger.#Secret`    |AZURE app id for the service principal used      |
|*password*          | `dagger.#Secret`    |AZURE password for the service principal used    |

### azure.#Config Outputs

_No output._
