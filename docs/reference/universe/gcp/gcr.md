---
sidebar_label: gcr
---

# dagger.io/gcp/gcr

Google Container Registry

## #Credentials

Credentials retriever for GCR

### #Credentials Inputs

| Name                  | Type                | Description        |
| -------------         |:-------------:      |:-------------:     |
|*config.region*        | `string`            |GCP region          |
|*config.project*       | `string`            |GCP project         |
|*config.serviceKey*    | `dagger.#Secret`    |GCP service key     |

### #Credentials Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*secret*          | `string`          |-                   |
