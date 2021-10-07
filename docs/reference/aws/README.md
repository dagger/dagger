---
sidebar_label: aws
---

# alpha.dagger.io/aws

AWS base package

```cue
import "alpha.dagger.io/aws"
```

## aws.#CLI

### aws.#CLI Inputs

| Name                 | Type                   | Description           |
| -------------        |:-------------:         |:-------------:        |
|*config.region*       | `string`               |AWS region             |
|*config.accessKey*    | `dagger.#Secret`       |AWS access key         |
|*config.secretKey*    | `dagger.#Secret`       |AWS secret key         |
|*config.localMode*    | `*false \| bool`       |AWS localstack mode    |
|*version*             | `*"1.18" \| string`    |-                      |

### aws.#CLI Outputs

_No output._

## aws.#Config

AWS Config shared by all AWS packages

### aws.#Config Inputs

| Name             | Type                | Description           |
| -------------    |:-------------:      |:-------------:        |
|*region*          | `string`            |AWS region             |
|*accessKey*       | `dagger.#Secret`    |AWS access key         |
|*secretKey*       | `dagger.#Secret`    |AWS secret key         |
|*localMode*       | `*false \| bool`    |AWS localstack mode    |

### aws.#Config Outputs

_No output._

## aws.#V1

Configuration specific to CLI v1

### aws.#V1 Inputs

| Name                 | Type                   | Description           |
| -------------        |:-------------:         |:-------------:        |
|*config.region*       | `string`               |AWS region             |
|*config.accessKey*    | `dagger.#Secret`       |AWS access key         |
|*config.secretKey*    | `dagger.#Secret`       |AWS secret key         |
|*config.localMode*    | `*false \| bool`       |AWS localstack mode    |
|*version*             | `*"1.18" \| string`    |-                      |

### aws.#V1 Outputs

_No output._

## aws.#V2

Configuration specific to CLI v2

### aws.#V2 Inputs

| Name                 | Type                     | Description           |
| -------------        |:-------------:           |:-------------:        |
|*config.region*       | `string`                 |AWS region             |
|*config.accessKey*    | `dagger.#Secret`         |AWS access key         |
|*config.secretKey*    | `dagger.#Secret`         |AWS secret key         |
|*config.localMode*    | `*false \| bool`         |AWS localstack mode    |
|*version*             | `*"2.1.27" \| string`    |-                      |

### aws.#V2 Outputs

_No output._
