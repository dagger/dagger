---
sidebar_label: eks
---

# dagger.io/aws/eks

## #KubeConfig

KubeConfig config outputs a valid kube-auth-config for kubectl client

### #KubeConfig Inputs

| Name                 | Type                      | Description        |
| -------------        |:-------------:            |:-------------:     |
|*config.region*       | `string`                  |AWS region          |
|*config.accessKey*    | `dagger.#Secret`          |AWS access key      |
|*config.secretKey*    | `dagger.#Secret`          |AWS secret key      |
|*clusterName*         | `string`                  |EKS cluster name    |
|*version*             | `*"v1.19.9" \| string`    |Kubectl version     |

### #KubeConfig Outputs

| Name             | Type              | Description                                           |
| -------------    |:-------------:    |:-------------:                                        |
|*kubeconfig*      | `string`          |kubeconfig is the generated kube configuration file    |
