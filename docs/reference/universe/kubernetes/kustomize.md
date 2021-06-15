---
sidebar_label: kustomize
---

# dagger.io/kubernetes/kustomize

Kustomize config management

## #Kustomization

### #Kustomization Inputs

| Name             | Type                     | Description                |
| -------------    |:-------------:           |:-------------:             |
|*version*         | `*"v3.8.7" \| string`    |Kustomize binary version    |

### #Kustomization Outputs

_No output._

## #Kustomize

Apply a Kubernetes Kustomize folder

### #Kustomize Inputs

| Name              | Type                     | Description                   |
| -------------     |:-------------:           |:-------------:                |
|*source*           | `dagger.#Artifact`       |Kubernetes source              |
|*kustomization*    | `string`                 |Optional Kustomization file    |
|*version*          | `*"v3.8.7" \| string`    |Kustomize binary version       |

### #Kustomize Outputs

_No output._
