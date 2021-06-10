---
sidebar_label: kubernetes
---

# dagger.io/kubernetes

## #Kubectl

### #Kubectl Inputs

_No input._

### #Kubectl Outputs

_No output._

## #Resources

Apply Kubernetes resources

### #Resources Inputs

| Name             | Type                      | Description                         |
| -------------    |:-------------:            |:-------------:                      |
|*namespace*       | `*"default" \| string`    |Kubernetes Namespace to deploy to    |
|*version*         | `*"v1.19.9" \| string`    |Version of kubectl client            |
|*kubeconfig*      | `dagger.#Secret`          |Kube config file                     |

### #Resources Outputs

_No output._
