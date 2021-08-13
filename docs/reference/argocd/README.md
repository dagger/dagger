---
sidebar_label: argocd
---

# alpha.dagger.io/argocd

ArgoCD client operations

```cue
import "alpha.dagger.io/argocd"
```

## argocd.#CLI

Re-usable CLI component

### argocd.#CLI Inputs

| Name               | Type                      | Description                   |
| -------------      |:-------------:            |:-------------:                |
|*config.version*    | `*"v2.0.5" \| string`     |ArgoCD CLI binary version      |
|*config.server*     | `string`                  |ArgoCD server                  |
|*config.project*    | `*"default" \| string`    |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`          |ArgoCD authentication token    |

### argocd.#CLI Outputs

_No output._

## argocd.#Config

ArgoCD configuration

### argocd.#Config Inputs

| Name             | Type                      | Description                   |
| -------------    |:-------------:            |:-------------:                |
|*version*         | `*"v2.0.5" \| string`     |ArgoCD CLI binary version      |
|*server*          | `string`                  |ArgoCD server                  |
|*project*         | `*"default" \| string`    |ArgoCD project                 |
|*token*           | `dagger.#Secret`          |ArgoCD authentication token    |

### argocd.#Config Outputs

_No output._
