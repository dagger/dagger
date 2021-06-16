---
sidebar_label: gcp
---

# dagger.io/gcp

Google Cloud Platform

```cue
import "dagger.io/gcp"
```

## gcp.#Config

Base Google Cloud Config

### gcp.#Config Inputs

| Name             | Type                | Description        |
| -------------    |:-------------:      |:-------------:     |
|*region*          | `string`            |GCP region          |
|*project*         | `string`            |GCP project         |
|*serviceKey*      | `dagger.#Secret`    |GCP service key     |

### gcp.#Config Outputs

_No output._

## gcp.#GCloud

Re-usable gcloud component

### gcp.#GCloud Inputs

| Name                  | Type                | Description        |
| -------------         |:-------------:      |:-------------:     |
|*config.region*        | `string`            |GCP region          |
|*config.project*       | `string`            |GCP project         |
|*config.serviceKey*    | `dagger.#Secret`    |GCP service key     |

### gcp.#GCloud Outputs

_No output._
