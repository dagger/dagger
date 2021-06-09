---
sidebar_label: ecr
---

# dagger.io/aws/ecr

## #Credentials

Credentials retriever for ECR

### #Credentials Inputs

| Name                 | Type                | Description        |
| -------------        |:-------------:      |:-------------:     |
|*config.region*       | `string`            |AWS region          |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key      |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key      |

### #Credentials Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*secret*          | `string`          |-                   |
