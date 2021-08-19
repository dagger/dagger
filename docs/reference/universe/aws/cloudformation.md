---
sidebar_label: cloudformation
---

# alpha.dagger.io/aws/cloudformation

AWS CloudFormation

```cue
import "alpha.dagger.io/aws/cloudformation"
```

## cloudformation.#Stack

AWS CloudFormation Stack

### cloudformation.#Stack Inputs

| Name                 | Type                                         | Description                                                     |
| -------------        |:-------------:                               |:-------------:                                                  |
|*config.region*       | `string`                                     |AWS region                                                       |
|*config.accessKey*    | `dagger.#Secret`                             |AWS access key                                                   |
|*config.secretKey*    | `dagger.#Secret`                             |AWS secret key                                                   |
|*config.localMode*    | `*null \| string`                            |AWS localstack mode                                              |
|*source*              | `string`                                     |Source is the Cloudformation template (JSON/YAML string)         |
|*stackName*           | `string`                                     |Stackname is the cloudformation stack                            |
|*parameters*          | `struct`                                     |Stack parameters                                                 |
|*onFailure*           | `*"DO_NOTHING" \| "ROLLBACK" \| "DELETE"`    |Behavior when failure to create/update the Stack                 |
|*timeout*             | `*10 \| \>=0 & int`                          |Maximum waiting time until stack creation/update (in minutes)    |
|*neverUpdate*         | `*false \| true`                             |Never update the stack if already exists                         |

### cloudformation.#Stack Outputs

_No output._
