---
sidebar_label: kustomize
---

# alpha.dagger.io/kubernetes/kustomize

Kustomize config management

```cue
import "alpha.dagger.io/kubernetes/kustomize"
```

## kustomize.#Kustomization

### kustomize.#Kustomization Inputs

| Name             | Type              | Description                |
| -------------    |:-------------:    |:-------------:             |
|*version*         | `v3.8.7`          |Kustomize binary version    |

### kustomize.#Kustomization Outputs

_No output._

## kustomize.#Kustomize

Apply a Kubernetes Kustomize folder

### kustomize.#Kustomize Inputs

| Name              | Type                  | Description                   |
| -------------     |:-------------:        |:-------------:                |
|*source*           | `dagger.#Artifact`    |Kubernetes source              |
|*kustomization*    | `string`              |Optional Kustomization file    |
|*version*          | `v3.8.7`              |Kustomize binary version       |

### kustomize.#Kustomize Outputs

_No output._
