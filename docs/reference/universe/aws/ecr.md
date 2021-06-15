---
sidebar_label: ecr
---

# dagger.io/aws/ecr

Amazon Elastic Container Registry (ECR)

## #Credentials

Convert AWS credentials to Docker Registry credentials for ECR

### #Credentials Inputs

| Name                           | Type                | Description        |
| -------------                  |:-------------:      |:-------------:     |
|*config.region*                 | `string`            |AWS region          |
|*config.accessKey*              | `dagger.#Secret`    |AWS access key      |
|*config.secretKey*              | `dagger.#Secret`    |AWS secret key      |
|*ctr.image.config.region*       | `string`            |AWS region          |
|*ctr.image.config.accessKey*    | `dagger.#Secret`    |AWS access key      |
|*ctr.image.config.secretKey*    | `dagger.#Secret`    |AWS secret key      |

### #Credentials Outputs

_No output._
