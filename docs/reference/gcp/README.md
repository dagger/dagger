---
sidebar_label: gcp
---

# alpha.dagger.io/gcp

Google Cloud Platform

```cue
import "alpha.dagger.io/gcp"
```

## gcp.#Config

Base Google Cloud Config

### gcp.#Config Inputs

| Name             | Type                 | Description        |
| -------------    |:-------------:       |:-------------:     |
|*region*          | `*null \| string`    |GCP region          |
|*zone*            | `*null \| string`    |GCP zone            |
|*project*         | `string`             |GCP project         |
|*serviceKey*      | `dagger.#Secret`     |GCP service key     |

### gcp.#Config Outputs

_No output._

## gcp.#GCloud

Re-usable gcloud component

### gcp.#GCloud Inputs

| Name                  | Type                 | Description        |
| -------------         |:-------------:       |:-------------:     |
|*config.region*        | `*null \| string`    |GCP region          |
|*config.zone*          | `*null \| string`    |GCP zone            |
|*config.project*       | `string`             |GCP project         |
|*config.serviceKey*    | `dagger.#Secret`     |GCP service key     |

### gcp.#GCloud Outputs

_No output._
