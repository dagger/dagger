---
sidebar_label: elb
---

# alpha.dagger.io/aws/elb

AWS Elastic Load Balancer (ELBv2)

```cue
import "alpha.dagger.io/aws/elb"
```

## elb.#RandomRulePriority

Returns an unused rule priority (randomized in available range)

### elb.#RandomRulePriority Inputs

| Name                 | Type                | Description        |
| -------------        |:-------------:      |:-------------:     |
|*config.region*       | `string`            |AWS region          |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key      |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key      |
|*listenerArn*         | `string`            |ListenerArn         |

### elb.#RandomRulePriority Outputs

| Name             | Type              | Description         |
| -------------    |:-------------:    |:-------------:      |
|*priority*        | `string`          |exported priority    |
