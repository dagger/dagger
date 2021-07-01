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

| Name                  | Type                      | Description          |
| -------------         |:-------------:            |:-------------:       |
|*config.region*        | `string`                  |GCP region            |
|*config.project*       | `string`                  |GCP project           |
|*config.serviceKey*    | `dagger.#Secret`          |GCP service key       |
|*name*                 | `string`                  |service name          |
|*image*                | `string`                  |GCR image ref         |
|*platform*             | `*"managed" \| string`    |Cloud Run platform    |

### cloudrun.#Service Outputs

_No output._
