---
sidebar_label: gcs
---

# alpha.dagger.io/gcp/gcs

Google Cloud Storage

```cue
import "alpha.dagger.io/gcp/gcs"
```

## gcs.#Object

GCS Bucket object(s) sync

### gcs.#Object Inputs

| Name                  | Type                  | Description                                                       |
| -------------         |:-------------:        |:-------------:                                                    |
|*config.region*        | `string`              |GCP region                                                         |
|*config.project*       | `string`              |GCP project                                                        |
|*config.serviceKey*    | `dagger.#Secret`      |GCP service key                                                    |
|*source*               | `dagger.#Artifact`    |Source Artifact to upload to GCS                                   |
|*target*               | `string`              |Target GCS URL (eg. gs://\<bucket-name\>/\<path\>/\<sub-path\>)    |
|*delete*               | `*false \| true`      |Delete files that already exist on remote destination              |
|*contentType*          | ``                    |Object content type                                                |
|*always*               | `*true \| false`      |Always write the object to GCS                                     |

### gcs.#Object Outputs

| Name             | Type              | Description                      |
| -------------    |:-------------:    |:-------------:                   |
|*url*             | `string`          |URL of the uploaded GCS object    |
