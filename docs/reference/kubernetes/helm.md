---
sidebar_label: helm
---

# alpha.dagger.io/kubernetes/helm

Helm package manager

```cue
import "alpha.dagger.io/kubernetes/helm"
```

## helm.#Chart

Install a Helm chart

### helm.#Chart Inputs

| Name               | Type                  | Description                                                                                                                                                                                                                   |
| -------------      |:-------------:        |:-------------:                                                                                                                                                                                                                |
|*name*              | `string`              |Helm deployment name                                                                                                                                                                                                           |
|*namespace*         | `string`              |Kubernetes Namespace to deploy to                                                                                                                                                                                              |
|*action*            | `installOrUpgrade`    |Helm action to apply                                                                                                                                                                                                           |
|*timeout*           | `5m`                  |time to wait for any individual Kubernetes operation (like Jobs for hooks)                                                                                                                                                     |
|*wait*              | `*true \| bool`       |if set, will wait until all Pods, PVCs, Services, and minimum number of Pods of a Deployment, StatefulSet, or ReplicaSet are in a ready state before marking the release as successful. It will wait for as long as timeout    |
|*atomic*            | `*true \| bool`       |if set, installation process purges chart on fail. The wait option will be set automatically if atomic is used                                                                                                                 |
|*kubeconfig*        | `string`              |Kube config file                                                                                                                                                                                                               |
|*version*           | `3.5.2`               |Helm version                                                                                                                                                                                                                   |
|*kubectlVersion*    | `v1.19.9`             |Kubectl version                                                                                                                                                                                                                |

### helm.#Chart Outputs

_No output._
