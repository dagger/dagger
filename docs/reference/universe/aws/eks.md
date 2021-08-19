---
sidebar_label: eks
---

# alpha.dagger.io/aws/eks

AWS Elastic Kubernetes Service (EKS)

```cue
import "alpha.dagger.io/aws/eks"
```

## eks.#KubeConfig

KubeConfig config outputs a valid kube-auth-config for kubectl client

### eks.#KubeConfig Inputs

| Name                 | Type                      | Description           |
| -------------        |:-------------:            |:-------------:        |
|*config.region*       | `string`                  |AWS region             |
|*config.accessKey*    | `dagger.#Secret`          |AWS access key         |
|*config.secretKey*    | `dagger.#Secret`          |AWS secret key         |
|*config.localMode*    | `*null \| string`         |AWS localstack mode    |
|*clusterName*         | `string`                  |EKS cluster name       |
|*version*             | `*"v1.19.9" \| string`    |Kubectl version        |

### eks.#KubeConfig Outputs

| Name             | Type              | Description                                           |
| -------------    |:-------------:    |:-------------:                                        |
|*kubeconfig*      | `string`          |kubeconfig is the generated kube configuration file    |
