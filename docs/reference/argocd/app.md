---
sidebar_label: app
---

# alpha.dagger.io/argocd/app

ArgoCD applications

```cue
import "alpha.dagger.io/argocd/app"
```

## app.#Application

Get an application

### app.#Application Inputs

| Name               | Type                      | Description                   |
| -------------      |:-------------:            |:-------------:                |
|*config.version*    | `*"v2.0.5" \| string`     |ArgoCD CLI binary version      |
|*config.server*     | `string`                  |ArgoCD server                  |
|*config.project*    | `*"default" \| string`    |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`          |ArgoCD authentication token    |
|*name*              | `string`                  |ArgoCD application             |

### app.#Application Outputs

| Name                  | Type              | Description                                |
| -------------         |:-------------:    |:-------------:                             |
|*outputs.health*       | `string`          |Application health                          |
|*outputs.sync*         | `string`          |Application sync state                      |
|*outputs.namespace*    | `string`          |Namespace                                   |
|*outputs.server*       | `string`          |Server                                      |
|*outputs.urls*         | `string`          |Comma separated list of application URLs    |
|*outputs.state*        | `string`          |Last operation state message                |

## app.#Synchronization

Sync an application to its target state

### app.#Synchronization Inputs

| Name               | Type                      | Description                   |
| -------------      |:-------------:            |:-------------:                |
|*config.version*    | `*"v2.0.5" \| string`     |ArgoCD CLI binary version      |
|*config.server*     | `string`                  |ArgoCD server                  |
|*config.project*    | `*"default" \| string`    |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`          |ArgoCD authentication token    |
|*application*       | `string`                  |ArgoCD application             |

### app.#Synchronization Outputs

_No output._

## app.#SynchronizedApplication

Wait for an application to reach a synced and healthy state

### app.#SynchronizedApplication Inputs

| Name               | Type                      | Description                   |
| -------------      |:-------------:            |:-------------:                |
|*config.version*    | `*"v2.0.5" \| string`     |ArgoCD CLI binary version      |
|*config.server*     | `string`                  |ArgoCD server                  |
|*config.project*    | `*"default" \| string`    |ArgoCD project                 |
|*config.token*      | `dagger.#Secret`          |ArgoCD authentication token    |
|*application*       | `string`                  |ArgoCD application             |

### app.#SynchronizedApplication Outputs

_No output._
