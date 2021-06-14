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

## #Command

A container image that can run any docker command

### #Command Inputs

| Name             | Type              | Description          |
| -------------    |:-------------:    |:-------------:       |
|*command*         | `string`          |Command to execute    |

### #Command Outputs

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

| Name                  | Type                | Description                           |
| -------------         |:-------------:      |:-------------:                        |
|*ssh.host*             | `string`            |ssh host                               |
|*ssh.user*             | `string`            |ssh user                               |
|*ssh.port*             | `*22 \| int`        |ssh port                               |
|*ssh.key*              | `dagger.#Secret`    |private key                            |
|*ref*                  | `string`            |Image reference (e.g: nginx:alpine)    |
|*run.ssh.host*         | `string`            |ssh host                               |
|*run.ssh.user*         | `string`            |ssh user                               |
|*run.ssh.port*         | `*22 \| int`        |ssh port                               |
|*run.ssh.key*          | `dagger.#Secret`    |private key                            |
|*run.command*          | `"""\n              # Run detach container\n                OPTS=""\n    \n    if [ ! -z "$CONTAINER_NAME" ]; then\n    \tOPTS="$OPTS --name $CONTAINER_NAME"\n    fi\n    \n    docker container run -d $OPTS "$IMAGE_REF"\n    """`    |Command to execute    |
|*run.env.IMAGE_REF*    | `string`            |-                                      |

### #Run Outputs

_No output._
