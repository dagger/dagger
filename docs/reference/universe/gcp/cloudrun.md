---
sidebar_label: cloudrun
---

# dagger.io/gcp/cloudrun

```cue
import "dagger.io/gcp/cloudrun"
```

## cloudrun.#Deploy

Deploy deploys a Cloud Run service based on provided GCR image

### cloudrun.#Deploy Inputs

| Name                  | Type                      | Description          |
| -------------         |:-------------:            |:-------------:       |
|*config.region*        | `string`                  |GCP region            |
|*config.project*       | `string`                  |GCP project           |
|*config.serviceKey*    | `dagger.#Secret`          |GCP service key       |
|*serviceName*          | `string`                  |service name          |
|*image*                | `string`                  |GCR image ref         |
|*platform*             | `*"managed" \| string`    |Cloud Run platform    |

### cloudrun.#Deploy Outputs

_No output._
