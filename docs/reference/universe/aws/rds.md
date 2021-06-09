---
sidebar_label: rds
---

# dagger.io/aws/rds

## #CreateDB

### #CreateDB Inputs

| Name                 | Type                | Description                                               |
| -------------        |:-------------:      |:-------------:                                            |
|*config.region*       | `string`            |AWS region                                                 |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                                             |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                                             |
|*name*                | `string`            |DB name                                                    |
|*dbArn*               | `string`            |ARN of the database instance                               |
|*secretArn*           | `string`            |ARN of the database secret (for connecting via rds api)    |
|*dbType*              | `string`            |-                                                          |

### #CreateDB Outputs

| Name             | Type              | Description              |
| -------------    |:-------------:    |:-------------:           |
|*out*             | `string`          |Name of the DB created    |

## #CreateUser

### #CreateUser Inputs

| Name                 | Type                | Description                                               |
| -------------        |:-------------:      |:-------------:                                            |
|*config.region*       | `string`            |AWS region                                                 |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key                                             |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key                                             |
|*username*            | `string`            |Username                                                   |
|*password*            | `string`            |Password                                                   |
|*dbArn*               | `string`            |ARN of the database instance                               |
|*secretArn*           | `string`            |ARN of the database secret (for connecting via rds api)    |
|*grantDatabase*       | `*"" \| string`     |-                                                          |
|*dbType*              | `string`            |-                                                          |

### #CreateUser Outputs

| Name             | Type              | Description         |
| -------------    |:-------------:    |:-------------:      |
|*out*             | `string`          |Outputed username    |

## #Instance

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
