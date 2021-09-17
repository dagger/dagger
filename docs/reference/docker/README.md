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

| Name             | Type              | Description          |
| -------------    |:-------------:    |:-------------:       |
|*command*         | `string`          |Command to execute    |
|*registries*      | `[]`              |Image registries      |

### docker.#Command Outputs

_No output._

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
|*run.registries*       | `[]`                |Image registries                       |

### docker.#Run Outputs

_No output._
