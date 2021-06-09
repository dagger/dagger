---
sidebar_label: file
---

# dagger.io/file

DEPRECATED: see dagger.io/os

## #Append

### #Append Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*filename*        | `string`              |-                   |
|*permissions*     | `*0o644 \| int`       |-                   |
|*contents*        | `(string\|bytes)`     |-                   |
|*from*            | `dagger.#Artifact`    |-                   |

### #Append Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*orig*            | `string`          |-                   |

## #Create

### #Create Inputs

| Name             | Type                 | Description        |
| -------------    |:-------------:       |:-------------:     |
|*filename*        | `string`             |-                   |
|*permissions*     | `*0o644 \| int`      |-                   |
|*contents*        | `(string\|bytes)`    |-                   |

### #Create Outputs

_No output._

## #Glob

### #Glob Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*glob*            | `string`              |-                   |
|*from*            | `dagger.#Artifact`    |-                   |

### #Glob Outputs

| Name             | Type              | Description                                       |
| -------------    |:-------------:    |:-------------:                                    |
|*filenames*       | `_\|_`            |trim suffix because ls always ends with newline    |
|*files*           | `string`          |-                                                  |

## #Read

### #Read Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*filename*        | `string`              |-                   |
|*from*            | `dagger.#Artifact`    |-                   |

### #Read Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*contents*        | `string`          |-                   |

## #read

### #read Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*path*            | `string`              |-                   |
|*from*            | `dagger.#Artifact`    |-                   |

### #read Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*data*            | `string`          |-                   |
