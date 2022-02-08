---
sidebar_label: rds
---

# alpha.dagger.io/aws/rds

AWS Relational Database Service (RDS)

```cue
import "alpha.dagger.io/aws/rds"
```

## rds.#Database

Creates a new Database on an existing RDS Instance

### rds.#Database Inputs

| Name                 | Type                | Description                                                  |
| -------------        |:-------------:      |:-------------:                                               |
|*config.region*       | `string`            |AWS region                                                    |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                                                |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                                                |
|*config.localMode*    | `*false \| bool`    |AWS localstack mode                                           |
|*name*                | `string`            |DB name                                                       |
|*dbArn*               | `string`            |ARN of the database instance                                  |
|*secretArn*           | `string`            |ARN of the database secret (for connecting via rds api)       |
|*dbType*              | `string`            |Database type MySQL or PostgreSQL (Aurora Serverless only)    |

### rds.#Database Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*out*             | `string`          |Name of the DB created    |

## rds.#Instance

Fetches information on an existing RDS Instance

### rds.#Instance Inputs

| Name                 | Type                | Description                    |
| -------------        |:-------------:      |:-------------:                 |
|*config.region*       | `string`            |AWS region                      |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                  |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                  |
|*config.localMode*    | `*false \| bool`    |AWS localstack mode             |
|*dbArn*               | `string`            |ARN of the database instance    |

### rds.#Instance Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*hostname*        | `_\|_`            |DB hostname         |
|*port*            | `_\|_`            |DB port             |
|*info*            | `_\|_`            |-                   |

## rds.#User

Creates a new user credentials on an existing RDS Instance

### rds.#User Inputs

| Name                 | Type                | Description                                                  |
| -------------        |:-------------:      |:-------------:                                               |
|*config.region*       | `string`            |AWS region                                                    |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                                                |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                                                |
|*config.localMode*    | `*false \| bool`    |AWS localstack mode                                           |
|*username*            | `string`            |Username                                                      |
|*password*            | `string`            |Password                                                      |
|*dbArn*               | `string`            |ARN of the database instance                                  |
|*secretArn*           | `string`            |ARN of the database secret (for connecting via rds api)       |
|*grantDatabase*       | `*"" \| string`     |Name of the database to grants access to                      |
|*dbType*              | `string`            |Database type MySQL or PostgreSQL (Aurora Serverless only)    |

### rds.#User Outputs

| Name             | Type              | Description          |
| -------------    |:-------------:    |:-------------:       |
|*out*             | `string`          |Outputted username    |
