---
sidebar_label: aws
---

# alpha.dagger.io/aws

AWS base package

```cue
import "alpha.dagger.io/aws"
```

## aws.#CLI

Re-usable aws-cli component

### aws.#CLI Inputs

| Name                 | Type                 | Description           |
| -------------        |:-------------:       |:-------------:        |
|*config.region*       | `string`             |AWS region             |
|*config.accessKey*    | `dagger.#Secret`     |AWS access key         |
|*config.secretKey*    | `dagger.#Secret`     |AWS secret key         |
|*config.localMode*    | `*null \| string`    |AWS localstack mode    |

### aws.#CLI Outputs

_No output._

## aws.#Config

AWS Config shared by all AWS packages

### aws.#Config Inputs

| Name             | Type                 | Description           |
| -------------    |:-------------:       |:-------------:        |
|*region*          | `string`             |AWS region             |
|*accessKey*       | `dagger.#Secret`     |AWS access key         |
|*secretKey*       | `dagger.#Secret`     |AWS secret key         |
|*localMode*       | `*null \| string`    |AWS localstack mode    |

### aws.#Config Outputs

_No output._
