---
sidebar_label: netlify
---

# dagger.io/netlify

Netlify client operations

## #Account

Netlify account credentials

### #Account Inputs

| Name             | Type                | Description                                                                      |
| -------------    |:-------------:      |:-------------:                                                                   |
|*name*            | `*"" \| string`     |Use this Netlify account name (also referred to as "team" in the Netlify docs)    |
|*token*           | `dagger.#Secret`    |Netlify authentication token                                                      |

### #Account Outputs

_No output._

## #Site

Netlify site

### #Site Inputs

| Name              | Type                  | Description                                                                      |
| -------------     |:-------------:        |:-------------:                                                                   |
|*account.name*     | `*"" \| string`       |Use this Netlify account name (also referred to as "team" in the Netlify docs)    |
|*account.token*    | `dagger.#Secret`      |Netlify authentication token                                                      |
|*contents*         | `dagger.#Artifact`    |Contents of the application to deploy                                             |
|*name*             | `string`              |Deploy to this Netlify site                                                       |
|*create*           | `*true \| bool`       |Create the Netlify site if it doesn't exist?                                      |

### #Site Outputs

| Name             | Type              | Description                    |
| -------------    |:-------------:    |:-------------:                 |
|*url*             | `string`          |Website url                     |
|*deployUrl*       | `string`          |Unique Deploy URL               |
|*logsUrl*         | `string`          |Logs URL for this deployment    |
