---
sidebar_label: elb
---

# dagger.io/aws/elb

AWS Elastic Load Balancer (ELBv2)

## #RandomRulePriority

Returns an unused rule priority (randomized in available range)

### #RandomRulePriority Inputs

| Name                 | Type                | Description        |
| -------------        |:-------------:      |:-------------:     |
|*config.region*       | `string`            |AWS region          |
|*config.accessKey*    | `dagger.#Secret`    |AWS access key      |
|*config.secretKey*    | `dagger.#Secret`    |AWS secret key      |
|*listenerArn*         | `string`            |ListenerArn         |

### #RandomRulePriority Outputs

| Name             | Type              | Description         |
| -------------    |:-------------:    |:-------------:      |
|*priority*        | `string`          |exported priority    |
