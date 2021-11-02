---
sidebar_label: docker
---

# alpha.dagger.io/docker

Docker container operations

```cue
import "alpha.dagger.io/docker"
```

## docker.#Build

Build a Docker image from source

### docker.#Build Inputs

| Name             | Type                  | Description                     |
| -------------    |:-------------:        |:-------------:                  |
|*source*          | `dagger.#Artifact`    |Build context                    |
|*dockerfile*      | `*null \| string`     |Dockerfile passed as a string    |

### docker.#Build Outputs

_No output._

## docker.#Command

A container image that can run any docker command

### docker.#Command Inputs

_No input._

### docker.#Command Outputs

_No output._

## docker.#Load

Load a docker image into a docker engine

### docker.#Load Inputs

| Name             | Type                  | Description                                          |
| -------------    |:-------------:        |:-------------:                                       |
|*tag*             | `string`              |Name and optionally a tag in the 'name:tag' format    |
|*source*          | `dagger.#Artifact`    |Image source                                          |

### docker.#Load Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*id*              | `string`          |Image ID            |

## docker.#Pull

Pull a docker container

### docker.#Pull Inputs

| Name             | Type              | Description                                             |
| -------------    |:-------------:    |:-------------:                                          |
|*from*            | `string`          |Remote ref (example: "index.docker.io/alpine:latest")    |

### docker.#Pull Outputs

_No output._

## docker.#Push

Push a docker image to a remote registry

### docker.#Push Inputs

| Name             | Type                  | Description                                                |
| -------------    |:-------------:        |:-------------:                                             |
|*target*          | `string`              |Remote target (example: "index.docker.io/alpine:latest")    |
|*source*          | `dagger.#Artifact`    |Image source                                                |

### docker.#Push Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*ref*             | `string`          |Image ref           |
|*digest*          | `string`          |Image digest        |

## docker.#Run

### docker.#Run Inputs

| Name                  | Type               | Description                           |
| -------------         |:-------------:     |:-------------:                        |
|*ref*                  | `string`           |Image reference (e.g: nginx:alpine)    |
|*recreate*             | `*true \| bool`    |Recreate container?                    |
|*run.env.IMAGE_REF*    | `string`           |-                                      |

### docker.#Run Outputs

_No output._
