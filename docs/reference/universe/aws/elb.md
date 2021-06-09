---
sidebar_label: elb
---

# dagger.io/aws/elb

## #RandomRulePriority

Returns a non-taken rule priority (randomized)

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
