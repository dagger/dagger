---
sidebar_label: git
---

# dagger.io/git

Git operations

```cue
import "dagger.io/git"
```

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

## git.#Repository

A git repository

### git.#Repository Inputs

| Name             | Type                 | Description                                                 |
| -------------    |:-------------:       |:-------------:                                              |
|*remote*          | `string`             |Git remote. Example: `"https://github.com/dagger/dagger"`    |
|*ref*             | `string`             |Git ref: can be a commit, tag or branch. Example: "main"     |
|*subdir*          | `*null \| string`    |(optional) Subdirectory                                      |

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
