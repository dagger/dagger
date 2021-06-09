---
sidebar_label: go
---

# dagger.io/go

## #Build

### #Build Inputs

| Name             | Type                    | Description                      |
| -------------    |:-------------:          |:-------------:                   |
|*version*         | `*"1.16" \| string`     |Go version to use                 |
|*source*          | `dagger.#Artifact`      |Source Directory to build         |
|*packages*        | `*"." \| string`        |Packages to build                 |
|*arch*            | `*"amd64" \| string`    |Target architecture               |
|*os*              | `*"linux" \| string`    |Target OS                         |
|*tags*            | `*"" \| string`         |Build tags to use for building    |
|*ldflags*         | `*"" \| string`         |LDFLAGS to use for linking        |

### #Build Outputs

| Name             | Type              | Description                        |
| -------------    |:-------------:    |:-------------:                     |
|*output*          | `string`          |Specify the targeted binary name    |

## #Container

A standalone go environment

### #Container Inputs

| Name             | Type                                | Description                                             |
| -------------    |:-------------:                      |:-------------:                                          |
|*version*         | `*"1.16" \| string`                 |Go version to use                                        |
|*source*          | `dagger.#Artifact`                  |-                                                        |
|*image.from*      | `"docker.io/golang:1.16-alpine"`    |Remote ref (example: "index.docker.io/alpine:latest")    |

### #Container Outputs

_No output._

## #Go

### #Go Inputs

| Name             | Type                   | Description                  |
| -------------    |:-------------:         |:-------------:               |
|*version*         | `*"1.16" \| string`    |Go version to use             |
|*args*            | `[]`                   |Arguments to the Go binary    |
|*source*          | `dagger.#Artifact`     |Source Directory to build     |

### #Go Outputs

_No output._

## #Test

### #Test Inputs

| Name             | Type                                | Description                  |
| -------------    |:-------------:                      |:-------------:               |
|*version*         | `*"1.16" \| string`                 |Go version to use             |
|*source*          | `dagger.#Artifact`                  |Source Directory to build     |
|*packages*        | `*"." \| string`                    |Packages to test              |
|*args*            | `["test", "-v", *"." \| string]`    |Arguments to the Go binary    |

### #Test Outputs

_No output._
