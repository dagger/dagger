---
sidebar_label: kubernetes
---

# alpha.dagger.io/kubernetes

Kubernetes client operations

```cue
import "alpha.dagger.io/kubernetes"
```

## kubernetes.#Kubectl

Kubectl client

### kubernetes.#Kubectl Inputs

| Name             | Type                      | Description        |
| -------------    |:-------------:            |:-------------:     |
|*version*         | `*"v1.19.9" \| string`    |Kubectl version     |

### kubernetes.#Kubectl Outputs

_No output._

## kubernetes.#Resources

Apply Kubernetes resources

### kubernetes.#Resources Inputs

| Name             | Type                      | Description                                              |
| -------------    |:-------------:            |:-------------:                                           |
|*source*          | `dagger.#Artifact`        |Kubernetes config to deploy                               |
|*manifest*        | `*null \| string`         |Kubernetes manifest to deploy inlined in a string         |
|*url*             | `*null \| string`         |Kubernetes manifest url to deploy remote configuration    |
|*namespace*       | `*"default" \| string`    |Kubernetes Namespace to deploy to                         |
|*version*         | `*"v1.19.9" \| string`    |Version of kubectl client                                 |
|*kubeconfig*      | `(string\|struct)`        |Kube config file                                          |

### kubernetes.#Resources Outputs

_No output._
