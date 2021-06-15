---
sidebar_label: cloudformation
---

# dagger.io/aws/cloudformation

AWS Cloud Formation

## #Stack

AWS CloudFormation Stack

### #Stack Inputs

| Name                 | Type                                         | Description                                                     |
| -------------        |:-------------:                               |:-------------:                                                  |
|*config.region*       | `string`                                     |AWS region                                                       |
|*config.accessKey*    | `dagger.#Secret`                             |AWS access key                                                   |
|*config.secretKey*    | `dagger.#Secret`                             |AWS secret key                                                   |
|*source*              | `string`                                     |Source is the Cloudformation template (JSON/YAML string)         |
|*stackName*           | `string`                                     |Stackname is the cloudformation stack                            |
|*onFailure*           | `*"DO_NOTHING" \| "ROLLBACK" \| "DELETE"`    |Behavior when failure to create/update the Stack                 |
|*timeout*             | `*10 \| \>=0 & int`                          |Maximum waiting time until stack creation/update (in minutes)    |
|*neverUpdate*         | `*false \| bool`                             |Never update the stack if already exists                         |

### #Stack Outputs

_No output._
