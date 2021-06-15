---
sidebar_label: rds
---

# dagger.io/aws/rds

AWS Relational Database Service (RDS)

## #CreateDB

Creates a new Database on an existing RDS Instance

### #CreateDB Inputs

| Name                 | Type                | Description                                                  |
| -------------        |:-------------:      |:-------------:                                               |
|*config.region*       | `string`            |AWS region                                                    |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                                                |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                                                |
|*name*                | `string`            |DB name                                                       |
|*dbArn*               | `string`            |ARN of the database instance                                  |
|*secretArn*           | `string`            |ARN of the database secret (for connecting via rds api)       |
|*dbType*              | `string`            |Database type MySQL or PostgreSQL (Aurora Serverless only)    |

### #CreateDB Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*out*             | `string`          |Name of the DB created    |

## #CreateUser

Creates a new user credentials on an existing RDS Instance

### #CreateUser Inputs

| Name                 | Type                | Description                                                  |
| -------------        |:-------------:      |:-------------:                                               |
|*config.region*       | `string`            |AWS region                                                    |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                                                |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                                                |
|*username*            | `string`            |Username                                                      |
|*password*            | `string`            |Password                                                      |
|*dbArn*               | `string`            |ARN of the database instance                                  |
|*secretArn*           | `string`            |ARN of the database secret (for connecting via rds api)       |
|*grantDatabase*       | `*"" \| string`     |Name of the database to grants access to                      |
|*dbType*              | `string`            |Database type MySQL or PostgreSQL (Aurora Serverless only)    |

### #CreateUser Outputs

| Name             | Type              | Description         |
| -------------    |:-------------:    |:-------------:      |
|*out*             | `string`          |Outputed username    |

## #Instance

Fetches information on an existing RDS Instance

### #Instance Inputs

| Name                 | Type                | Description                    |
| -------------        |:-------------:      |:-------------:                 |
|*config.region*       | `string`            |AWS region                      |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                  |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                  |
|*dbArn*               | `string`            |ARN of the database instance    |

### #Instance Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*hostname*        | `_\|_`            |DB hostname         |
|*port*            | `_\|_`            |DB port             |
|*info*            | `_\|_`            |-                   |
