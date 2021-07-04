---
sidebar_label: docker
---

# alpha.dagger.io/docker

Docker container operations

```cue
import "alpha.dagger.io/docker"
```

## docker.#Build

Build a Docker image from source, using included Dockerfile

### docker.#Build Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*source*          | `dagger.#Artifact`    |-                   |

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

## docker.#ImageFromDockerfile

Build a Docker image from the provided Dockerfile contents

### docker.#ImageFromDockerfile Inputs

| Name             | Type                  | Description                     |
| -------------    |:-------------:        |:-------------:                  |
|*dockerfile*      | `string`              |Dockerfile passed as a string    |
|*context*         | `dagger.#Artifact`    |Build context                    |

### docker.#ImageFromDockerfile Outputs

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

| Name                   | Type                | Description                           |
| -------------          |:-------------:      |:-------------:                        |
|*sshConfig.host*        | `string`            |ssh host                               |
|*sshConfig.user*        | `string`            |ssh user                               |
|*sshConfig.port*        | `*22 \| int`        |ssh port                               |
|*sshConfig.key*         | `dagger.#Secret`    |private key                            |
|*ref*                   | `string`            |Image reference (e.g: nginx:alpine)    |
|*run.sshConfig.host*    | `string`            |ssh host                               |
|*run.sshConfig.user*    | `string`            |ssh user                               |
|*run.sshConfig.port*    | `*22 \| int`        |ssh port                               |
|*run.sshConfig.key*     | `dagger.#Secret`    |private key                            |
|*run.command*           | `"""\n              # Run detach container\n                OPTS=""\n    \n    if [ ! -z "$CONTAINER_NAME" ]; then\n    \tOPTS="$OPTS --name $CONTAINER_NAME"\n    fi\n    \n    docker container run -d $OPTS "$IMAGE_REF"\n    """`    |Command to execute    |
|*run.env.IMAGE_REF*     | `string`            |-                                      |
|*run.registries*        | `[]`                |Image registries                       |

### docker.#Run Outputs

_No output._
