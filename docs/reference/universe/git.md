---
sidebar_label: git
---

# dagger.io/git

## #CurrentBranch

Get the name of the current checked out branch or tag

### #CurrentBranch Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*repository*      | `dagger.#Artifact`    |-                   |

### #CurrentBranch Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*name*            | `string`          |-                   |

## #Repository

A git repository

### #Repository Inputs

| Name             | Type                 | Description                                                |
| -------------    |:-------------:       |:-------------:                                             |
|*remote*          | `string`             |Git remote. Example: "https://github.com/dagger/dagger"     |
|*ref*             | `string`             |Git ref: can be a commit, tag or branch. Example: "main"    |
|*subdir*          | `*null \| string`    |(optional) Subdirectory                                     |

### #Repository Outputs

_No output._

## #Tags

List tags of a repository

### #Tags Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*repository*      | `dagger.#Artifact`    |-                   |

### #Tags Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*tags*            | `[]`              |-                   |
