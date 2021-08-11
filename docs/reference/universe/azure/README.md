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

| Name                      | Type                               | Description                                             |
| -------------             |:-------------:                     |:-------------:                                          |
|*config.tenantId*          | `dagger.#Secret`                   |AZURE tenant id                                          |
|*config.subscriptionId*    | `dagger.#Secret`                   |AZURE subscription id                                    |
|*config.appId*             | `dagger.#Secret`                   |AZURE app id for the service principal used              |
|*config.password*          | `dagger.#Secret`                   |AZURE password for the service principal used            |
|*image.from*               | `"mcr.microsoft.com/azure-cli"`    |Remote ref (example: "index.docker.io/alpine:latest")    |

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
