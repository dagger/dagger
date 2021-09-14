---
sidebar_label: gcr
---

# alpha.dagger.io/gcp/gcr

Google Container Registry

```cue
import "alpha.dagger.io/gcp/gcr"
```

## gcr.#Credentials

Credentials retriever for GCR

### gcr.#Credentials Inputs

| Name                  | Type                 | Description        |
| -------------         |:-------------:       |:-------------:     |
|*config.region*        | `*null \| string`    |GCP region          |
|*config.zone*          | `*null \| string`    |GCP zone            |
|*config.project*       | `string`             |GCP project         |
|*config.serviceKey*    | `dagger.#Secret`     |GCP service key     |

### gcr.#Credentials Outputs

| Name             | Type                     | Description             |
| -------------    |:-------------:           |:-------------:          |
|*username*        | `"oauth2accesstoken"`    |GCR registry username    |
|*secret*          | `string`                 |GCR registry secret      |
