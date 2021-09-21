---
sidebar_label: netlify
---

# alpha.dagger.io/netlify

Netlify client operations

```cue
import "alpha.dagger.io/netlify"
```

## netlify.#Account

Netlify account credentials

### netlify.#Account Inputs

| Name             | Type                | Description                                                                      |
| -------------    |:-------------:      |:-------------:                                                                   |
|*name*            | `*"" \| string`     |Use this Netlify account name (also referred to as "team" in the Netlify docs)    |
|*token*           | `dagger.#Secret`    |Netlify authentication token                                                      |

### netlify.#Account Outputs

_No output._

## netlify.#Site

Netlify site

### netlify.#Site Inputs

| Name                                | Type                  | Description                                                                      |
| -------------                       |:-------------:        |:-------------:                                                                   |
|*account.name*                       | `*"" \| string`       |Use this Netlify account name (also referred to as "team" in the Netlify docs)    |
|*account.token*                      | `dagger.#Secret`      |Netlify authentication token                                                      |
|*contents*                           | `dagger.#Artifact`    |Contents of the application to deploy                                             |
|*name*                               | `string`              |Deploy to this Netlify site                                                       |
|*create*                             | `*true \| bool`       |Create the Netlify site if it doesn't exist?                                      |
|*ctr.env.NETLIFY_SITE_NAME*          | `string`              |-                                                                                 |
|*ctr.env.NETLIFY_ACCOUNT*            | `*"" \| string`       |-                                                                                 |
|*ctr.mount."/src".from*              | `dagger.#Artifact`    |-                                                                                 |
|*ctr.secret."/run/secrets/token"*    | `dagger.#Secret`      |-                                                                                 |

### netlify.#Site Outputs

| Name             | Type              | Description                    |
| -------------    |:-------------:    |:-------------:                 |
|*url*             | `string`          |Website url                     |
|*deployUrl*       | `string`          |Unique Deploy URL               |
|*logsUrl*         | `string`          |Logs URL for this deployment    |
