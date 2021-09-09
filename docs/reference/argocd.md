---
sidebar_label: argocd
---

# alpha.dagger.io/argocd

ArgoCD client operations

```cue
import "alpha.dagger.io/argocd"
```

## argocd.#App

Create an ArgoCD application

### argocd.#App Inputs

| Name               | Type                                             | Description                    |
| -------------      |:-------------:                                   |:-------------:                 |
|*config.version*    | `*"v2.0.5" \| string`                            |ArgoCD CLI binary version       |
|*config.server*     | `string`                                         |ArgoCD server                   |
|*config.project*    | `*"default" \| string`                           |ArgoCD project                  |
|*config.token*      | `dagger.#Secret`                                 |ArgoCD authentication token     |
|*name*              | `string`                                         |App name                        |
|*repo*              | `string`                                         |Repository url (git or helm)    |
|*path*              | `string`                                         |Folder to deploy                |
|*server*            | `*"https://kubernetes.default.svc" \| string`    |Destination server              |
|*namespace*         | `*"default" \| string`                           |Destination namespace           |

### argocd.#App Outputs

_No output._

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

## argocd.#Status

Get application's status

### argocd.#Status Inputs

| Name               | Type                      | Description                   |
| -------------      |:-------------:            |:-------------:                |
|*config.version*    | `*"v2.0.5" \| string`     |ArgoCD CLI binary version      |
|*config.server*     | `string`                  |ArgoCD server                  |
|*config.project*    | `*"default" \| string`    |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`          |ArgoCD authentication token    |
|*name*              | `string`                  |ArgoCD application             |

### argocd.#Status Outputs

| Name                  | Type              | Description                                |
| -------------         |:-------------:    |:-------------:                             |
|*outputs.health*       | `string`          |Application health                          |
|*outputs.sync*         | `string`          |Application sync state                      |
|*outputs.namespace*    | `string`          |Namespace                                   |
|*outputs.server*       | `string`          |Server                                      |
|*outputs.urls*         | `string`          |Comma separated list of application URLs    |
|*outputs.state*        | `string`          |Last operation state message                |

## argocd.#Sync

Sync an application to its targer state

### argocd.#Sync Inputs

| Name               | Type                      | Description                              |
| -------------      |:-------------:            |:-------------:                           |
|*config.version*    | `*"v2.0.5" \| string`     |ArgoCD CLI binary version                 |
|*config.server*     | `string`                  |ArgoCD server                             |
|*config.project*    | `*"default" \| string`    |ArgoCD project                            |
|*config.token*      | `dagger.#Secret`          |ArgoCD authentication token               |
|*application*       | `string`                  |ArgoCD application                        |
|*wait*              | `*false \| bool`          |Wait the application to sync correctly    |

### argocd.#Sync Outputs

_No output._
