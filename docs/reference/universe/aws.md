---
sidebar_label: aws
---

# dagger.io/aws

## #CLI

Re-usable aws-cli component

### #CLI Inputs

| Name                 | Type                | Description        |
| -------------        |:-------------:      |:-------------:     |
|*config.region*       | `string`            |AWS region          |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key      |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key      |

### #CLI Outputs

_No output._

## #Config

Base AWS Config

### #Config Inputs

| Name             | Type                | Description        |
| -------------    |:-------------:      |:-------------:     |
|*region*          | `string`            |AWS region          |
|*accessKey*       | `dagger.#Secret`    |AWS access key      |
|*secretKey*       | `dagger.#Secret`    |AWS secret key      |

### #Config Outputs

_No output._
