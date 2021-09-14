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

| Name                     | Type                                | Description                    |
| -------------            |:-------------:                      |:-------------:                 |
|*config.version*          | `v2.0.5`                            |ArgoCD CLI binary version       |
|*config.server*           | `string`                            |ArgoCD server                   |
|*config.project*          | `default`                           |ArgoCD project                  |
|*config.token*            | `dagger.#Secret`                    |ArgoCD authentication token     |
|*name*                    | `string`                            |App name                        |
|*repo*                    | `string`                            |Repository url (git or helm)    |
|*path*                    | `string`                            |Folder to deploy                |
|*server*                  | `https://kubernetes.default.svc`    |Destination server              |
|*image.config.version*    | `v2.0.5`                            |ArgoCD CLI binary version       |
|*image.config.server*     | `string`                            |ArgoCD server                   |
|*image.config.project*    | `default`                           |ArgoCD project                  |
|*image.config.token*      | `dagger.#Secret`                    |ArgoCD authentication token     |
|*namespace*               | `default`                           |Destination namespace           |
|*env.APP_NAME*            | `string`                            |-                               |
|*env.APP_REPO*            | `string`                            |-                               |
|*env.APP_PATH*            | `string`                            |-                               |
|*env.APP_SERVER*          | `https://kubernetes.default.svc`    |-                               |
|*env.APP_NAMESPACE*       | `default`                           |-                               |

### argocd.#App Outputs

_No output._

## argocd.#CLI

Re-usable CLI component

### argocd.#CLI Inputs

| Name               | Type                | Description                   |
| -------------      |:-------------:      |:-------------:                |
|*config.version*    | `v2.0.5`            |ArgoCD CLI binary version      |
|*config.server*     | `string`            |ArgoCD server                  |
|*config.project*    | `default`           |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`    |ArgoCD authentication token    |

### argocd.#CLI Outputs

_No output._

## argocd.#Config

ArgoCD configuration

### argocd.#Config Inputs

| Name             | Type                | Description                   |
| -------------    |:-------------:      |:-------------:                |
|*version*         | `v2.0.5`            |ArgoCD CLI binary version      |
|*server*          | `string`            |ArgoCD server                  |
|*project*         | `default`           |ArgoCD project                 |
|*token*           | `dagger.#Secret`    |ArgoCD authentication token    |

### argocd.#Config Outputs

_No output._

## argocd.#Status

Get application's status

### argocd.#Status Inputs

| Name               | Type                | Description                   |
| -------------      |:-------------:      |:-------------:                |
|*config.version*    | `v2.0.5`            |ArgoCD CLI binary version      |
|*config.server*     | `string`            |ArgoCD server                  |
|*config.project*    | `default`           |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`    |ArgoCD authentication token    |
|*name*              | `string`            |ArgoCD application             |

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

| Name                         | Type                | Description                              |
| -------------                |:-------------:      |:-------------:                           |
|*config.version*              | `v2.0.5`            |ArgoCD CLI binary version                 |
|*config.server*               | `string`            |ArgoCD server                             |
|*config.project*              | `default`           |ArgoCD project                            |
|*config.token*                | `dagger.#Secret`    |ArgoCD authentication token               |
|*application*                 | `string`            |ArgoCD application                        |
|*wait*                        | `*false \| bool`    |Wait the application to sync correctly    |
|*ctr.image.config.version*    | `v2.0.5`            |ArgoCD CLI binary version                 |
|*ctr.image.config.server*     | `string`            |ArgoCD server                             |
|*ctr.image.config.project*    | `default`           |ArgoCD project                            |
|*ctr.image.config.token*      | `dagger.#Secret`    |ArgoCD authentication token               |
|*ctr.env.APPLICATION*         | `string`            |-                                         |
|*status.config.version*       | `v2.0.5`            |ArgoCD CLI binary version                 |
|*status.config.server*        | `string`            |ArgoCD server                             |
|*status.config.project*       | `default`           |ArgoCD project                            |
|*status.config.token*         | `dagger.#Secret`    |ArgoCD authentication token               |
|*status.name*                 | `string`            |ArgoCD application                        |

### argocd.#Sync Outputs

| Name                         | Type              | Description                                |
| -------------                |:-------------:    |:-------------:                             |
|*status.outputs.health*       | `string`          |Application health                          |
|*status.outputs.sync*         | `string`          |Application sync state                      |
|*status.outputs.namespace*    | `string`          |Namespace                                   |
|*status.outputs.server*       | `string`          |Server                                      |
|*status.outputs.urls*         | `string`          |Comma separated list of application URLs    |
|*status.outputs.state*        | `string`          |Last operation state message                |
