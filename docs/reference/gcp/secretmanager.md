---
sidebar_label: secretmanager
---

# alpha.dagger.io/gcp/secretmanager

Google Cloud Secret Manager

```cue
import "alpha.dagger.io/gcp/secretmanager"
```

## secretmanager.#Secrets

### secretmanager.#Secrets Inputs

| Name                                   | Type                 | Description        |
| -------------                          |:-------------:       |:-------------:     |
|*config.region*                         | `*null \| string`    |GCP region          |
|*config.zone*                           | `*null \| string`    |GCP zone            |
|*config.project*                        | `string`             |GCP project         |
|*config.serviceKey*                     | `dagger.#Secret`     |GCP service key     |
|*deployment.image.config.region*        | `*null \| string`    |GCP region          |
|*deployment.image.config.zone*          | `*null \| string`    |GCP zone            |
|*deployment.image.config.project*       | `string`             |GCP project         |
|*deployment.image.config.serviceKey*    | `dagger.#Secret`     |GCP service key     |

### secretmanager.#Secrets Outputs

_No output._
