---
sidebar_label: git
---

# alpha.dagger.io/git

Git operations

```cue
import "alpha.dagger.io/git"
```

## git.#Commit

Commit & push to github repository

### git.#Commit Inputs

| Name                       | Type                | Description             |
| -------------              |:-------------:      |:-------------:          |
|*repository.remote*         | `string`            |Repository remote URL    |
|*repository.PAT*            | `dagger.#Secret`    |Github PAT               |
|*repository.branch*         | `string`            |Git branch               |
|*name*                      | `string`            |Username                 |
|*email*                     | `string`            |Email                    |
|*message*                   | `string`            |Commit message           |
|*force*                     | `*false \| bool`    |Force push options       |
|*ctr.env.USER_NAME*         | `string`            |-                        |
|*ctr.env.USER_EMAIL*        | `string`            |-                        |
|*ctr.env.COMMIT_MESSAGE*    | `string`            |-                        |
|*ctr.env.GIT_BRANCH*        | `string`            |-                        |
|*ctr.env.GIT_REMOTE*        | `string`            |-                        |

### git.#Commit Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*hash*            | `string`          |Commit hash         |

## git.#CurrentBranch

Get the name of the current checked out branch or tag

### git.#CurrentBranch Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*repository*      | `dagger.#Artifact`    |Git repository      |

### git.#CurrentBranch Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*name*            | `string`          |Git branch name     |

## git.#Image

### git.#Image Inputs

_No input._

### git.#Image Outputs

_No output._

## git.#Repository

A git repository

### git.#Repository Inputs

| Name             | Type                 | Description                                                |
| -------------    |:-------------:       |:-------------:                                             |
|*remote*          | `string`             |Git remote link                                             |
|*ref*             | `string`             |Git ref: can be a commit, tag or branch. Example: "main"    |
|*subdir*          | `*null \| string`    |(optional) Subdirectory                                     |
|*authToken*       | `dagger.#Secret`     |(optional) Add Personal Access Token                        |
|*authHeader*      | `dagger.#Secret`     |(optional) Add OAuth Token                                  |

### git.#Repository Outputs

_No output._

## git.#Tags

List tags of a repository

### git.#Tags Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*repository*      | `dagger.#Artifact`    |Git repository      |

### git.#Tags Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*tags*            | `[]`              |Repository tags     |
