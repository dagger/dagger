---
sidebar_label: cloudrun
---

# alpha.dagger.io/gcp/cloudrun

```cue
import "alpha.dagger.io/gcp/cloudrun"
```

## cloudrun.#Service

Service deploys a Cloud Run service based on provided GCR image

### cloudrun.#Service Inputs

| Name                  | Type                      | Description                      |
| -------------         |:-------------:            |:-------------:                   |
|*config.region*        | `*null \| string`         |GCP region                        |
|*config.zone*          | `*null \| string`         |GCP zone                          |
|*config.project*       | `string`                  |GCP project                       |
|*config.serviceKey*    | `dagger.#Secret`          |GCP service key                   |
|*name*                 | `string`                  |Cloud Run service name            |
|*image*                | `string`                  |GCR image ref                     |
|*platform*             | `*"managed" \| string`    |Cloud Run platform                |
|*port*                 | `*"80" \| string`         |Cloud Run service exposed port    |

### cloudrun.#Service Outputs

_No output._
