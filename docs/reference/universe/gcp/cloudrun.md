---
sidebar_label: cloudrun
---

# dagger.io/gcp/cloudrun

## #Deploy

Deploy deploys a Cloud Run service based on provided GCR image

### #Deploy Inputs

| Name                  | Type                       | Description                          |
| -------------         |:-------------:             |:-------------:                       |
|*config.region*        | `string`                   |GCP region                            |
|*config.project*       | `string`                   |GCP project                           |
|*config.serviceKey*    | `dagger.#Secret`           |GCP service key                       |
|*serviceName*          | `string`                   |service name                          |
|*region*               | `*"us-west2" \| string`    |region to which deploy the service    |
|*image*                | `string`                   |GCR image ref                         |
|*platform*             | `*"managed" \| string`     |Cloud Run platform                    |

### #Deploy Outputs

_No output._
