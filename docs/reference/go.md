---
sidebar_label: go
---

# alpha.dagger.io/go

Go build operations

```cue
import "alpha.dagger.io/go"
```

## go.#Build

Go application builder

### go.#Build Inputs

| Name             | Type                    | Description                      |
| -------------    |:-------------:          |:-------------:                   |
|*version*         | `*"1.16" \| string`     |Go version to use                 |
|*source*          | `dagger.#Artifact`      |Source Directory to build         |
|*packages*        | `*"." \| string`        |Packages to build                 |
|*arch*            | `*"amd64" \| string`    |Target architecture               |
|*os*              | `*"linux" \| string`    |Target OS                         |
|*tags*            | `*"" \| string`         |Build tags to use for building    |
|*ldflags*         | `*"" \| string`         |LDFLAGS to use for linking        |

### go.#Build Outputs

| Name             | Type              | Description                        |
| -------------    |:-------------:    |:-------------:                     |
|*output*          | `string`          |Specify the targeted binary name    |

## go.#Container

A standalone go environment

### go.#Container Inputs

| Name                  | Type                                | Description                                             |
| -------------         |:-------------:                      |:-------------:                                          |
|*version*              | `*"1.16" \| string`                 |Go version to use                                        |
|*source*               | `dagger.#Artifact`                  |Source code                                              |
|*image.from*           | `"docker.io/golang:1.16-alpine"`    |Remote ref (example: "index.docker.io/alpine:latest")    |
|*mount."/src".from*    | `dagger.#Artifact`                  |-                                                        |

### go.#Container Outputs

_No output._

## go.#Go

Re-usable component for the Go compiler

### go.#Go Inputs

| Name             | Type                   | Description                  |
| -------------    |:-------------:         |:-------------:               |
|*version*         | `*"1.16" \| string`    |Go version to use             |
|*args*            | `[]`                   |Arguments to the Go binary    |
|*source*          | `dagger.#Artifact`     |Source Directory to build     |

### go.#Go Outputs

_No output._

## go.#Test

### go.#Test Inputs

| Name             | Type                                | Description                  |
| -------------    |:-------------:                      |:-------------:               |
|*version*         | `*"1.16" \| string`                 |Go version to use             |
|*source*          | `dagger.#Artifact`                  |Source Directory to build     |
|*packages*        | `*"." \| string`                    |Packages to test              |
|*args*            | `["test", "-v", *"." \| string]`    |Arguments to the Go binary    |
|*args[2]*         | `*"." \| string`                    |-                             |

### go.#Test Outputs

_No output._
