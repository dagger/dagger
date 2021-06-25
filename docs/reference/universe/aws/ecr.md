---
sidebar_label: ecr
---

# alpha.dagger.io/aws/ecr

Amazon Elastic Container Registry (ECR)

```cue
import "alpha.dagger.io/aws/ecr"
```

## ecr.#Credentials

Convert ECR credentials to Docker Login format

### ecr.#Credentials Inputs

| Name                           | Type                | Description        |
| -------------                  |:-------------:      |:-------------:     |
|*config.region*                 | `string`            |AWS region          |
|*config.accessKey*              | `dagger.#Secret`    |AWS access key      |
|*config.secretKey*              | `dagger.#Secret`    |AWS secret key      |
|*ctr.image.config.region*       | `string`            |AWS region          |
|*ctr.image.config.accessKey*    | `dagger.#Secret`    |AWS access key      |
|*ctr.image.config.secretKey*    | `dagger.#Secret`    |AWS secret key      |

### ecr.#Credentials Outputs

| Name             | Type              | Description           |
| -------------    |:-------------:    |:-------------:        |
|*username*        | `"AWS"`           |ECR registry           |
|*secret*          | `string`          |ECR registry secret    |
