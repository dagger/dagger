---
sidebar_label: kubernetes
---

# dagger.io/kubernetes

Kubernetes client operations

```cue
import "dagger.io/kubernetes"
```

## kubernetes.#Kubectl

Kubectl client

### kubernetes.#Kubectl Inputs

_No input._

### kubernetes.#Kubectl Outputs

_No output._

## kubernetes.#Resources

Apply Kubernetes resources

### kubernetes.#Resources Inputs

| Name             | Type                      | Description                         |
| -------------    |:-------------:            |:-------------:                      |
|*namespace*       | `*"default" \| string`    |Kubernetes Namespace to deploy to    |
|*version*         | `*"v1.19.9" \| string`    |Version of kubectl client            |
|*kubeconfig*      | `string`                  |Kube config file                     |

### kubernetes.#Resources Outputs

_No output._
