---
sidebar_label: docker
---

# dagger.io/docker

## #Build

Build a Docker image from source, using included Dockerfile

### #Build Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*source*          | `dagger.#Artifact`    |-                   |

### #Build Outputs

_No output._

## #Client

A container image to run the Docker client

### #Client Inputs

_No input._

### #Client Outputs

_No output._

## #ImageFromDockerfile

Build a Docker image from the provided Dockerfile contents

### #ImageFromDockerfile Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*dockerfile*      | `string`              |-                   |
|*context*         | `dagger.#Artifact`    |-                   |

### #ImageFromDockerfile Outputs

_No output._

## #Pull

Pull a docker container

### #Pull Inputs

| Name             | Type              | Description                                             |
| -------------    |:-------------:    |:-------------:                                          |
|*from*            | `string`          |Remote ref (example: "index.docker.io/alpine:latest")    |

### #Pull Outputs

_No output._

## #Push

Push a docker image

### #Push Inputs

| Name             | Type                  | Description                                             |
| -------------    |:-------------:        |:-------------:                                          |
|*ref*             | `string`              |Remote ref (example: "index.docker.io/alpine:latest")    |
|*source*          | `dagger.#Artifact`    |Image                                                    |

### #Push Outputs

_No output._

## #Run

### #Run Inputs

| Name             | Type                | Description                           |
| -------------    |:-------------:      |:-------------:                        |
|*ssh.host*        | `string`            |ssh host                               |
|*ssh.user*        | `string`            |ssh user                               |
|*ssh.port*        | `*22 \| int`        |ssh port                               |
|*ssh.key*         | `dagger.#Secret`    |private key                            |
|*ref*             | `string`            |Image reference (e.g: nginx:alpine)    |

### #Run Outputs

_No output._
