---
sidebar_label: staticwebapp
---

# alpha.dagger.io/azure/staticwebapp

```cue
import "alpha.dagger.io/azure/staticwebapp"
```

## staticwebapp.#StaticWebApp

Create a static web app

### staticwebapp.#StaticWebApp Inputs

| Name                                               | Type                                                                                                            | Description                                             |
| -------------                                      |:-------------:                                                                                                  |:-------------:                                          |
|*config.tenantId*                                   | `dagger.#Secret`                                                                                                |AZURE tenant id                                          |
|*config.subscriptionId*                             | `dagger.#Secret`                                                                                                |AZURE subscription id                                    |
|*config.appId*                                      | `dagger.#Secret`                                                                                                |AZURE app id for the service principal used              |
|*config.password*                                   | `dagger.#Secret`                                                                                                |AZURE password for the service principal used            |
|*rgName*                                            | `string`                                                                                                        |ResourceGroup name in which to create static webapp      |
|*stappLocation*                                     | `string`                                                                                                        |StaticWebApp location                                    |
|*stappName*                                         | `string`                                                                                                        |StaticWebApp name                                        |
|*remote*                                            | `string`                                                                                                        |GitHubRepository URL                                     |
|*ref*                                               | `main`                                                                                                          |GitHub Branch                                            |
|*appLocation*                                       | `/`                                                                                                             |Location of your application code                        |
|*buildLocation*                                     | `build`                                                                                                         |Location of your build artifacts                         |
|*authToken*                                         | `dagger.#Secret`                                                                                                |GitHub Personal Access Token                             |
|*ctr.image.config.tenantId*                         | `dagger.#Secret`                                                                                                |AZURE tenant id                                          |
|*ctr.image.config.subscriptionId*                   | `dagger.#Secret`                                                                                                |AZURE subscription id                                    |
|*ctr.image.config.appId*                            | `dagger.#Secret`                                                                                                |AZURE app id for the service principal used              |
|*ctr.image.config.password*                         | `dagger.#Secret`                                                                                                |AZURE password for the service principal used            |
|*ctr.image.image.from*                              | `mcr.microsoft.com/azure-cli:2.27.1@sha256:1e117183100c9fce099ebdc189d73e506e7b02d2b73d767d3fc07caee72f9fb1`    |Remote ref (example: "index.docker.io/alpine:latest")    |
|*ctr.image.secret."/run/secrets/appId"*             | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.image.secret."/run/secrets/password"*          | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.image.secret."/run/secrets/tenantId"*          | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.image.secret."/run/secrets/subscriptionId"*    | `dagger.#Secret`                                                                                                |-                                                        |
|*ctr.env.AZURE_DEFAULTS_GROUP*                      | `string`                                                                                                        |-                                                        |
|*ctr.env.AZURE_DEFAULTS_LOCATION*                   | `string`                                                                                                        |-                                                        |
|*ctr.env.AZURE_STATICWEBAPP_NAME*                   | `string`                                                                                                        |-                                                        |
|*ctr.env.GIT_URL*                                   | `string`                                                                                                        |-                                                        |
|*ctr.env.GIT_BRANCH*                                | `main`                                                                                                          |-                                                        |
|*ctr.env.APP_LOCATION*                              | `/`                                                                                                             |-                                                        |
|*ctr.env.BUILD_LOCATION*                            | `build`                                                                                                         |-                                                        |
|*ctr.secret."/run/secrets/git_pat"*                 | `dagger.#Secret`                                                                                                |-                                                        |

### staticwebapp.#StaticWebApp Outputs

| Name                | Type              | Description                          |
| -------------       |:-------------:    |:-------------:                       |
|*defaultHostName*    | `string`          |DefaultHostName generated by Azure    |
