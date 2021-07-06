---
sidebar_label: docker
---

# alpha.dagger.io/docker

Docker container operations

```cue
import "alpha.dagger.io/docker"
```

## docker.#Command

A container image that can run any docker command

### docker.#Command Inputs

| Name             | Type              | Description          |
| -------------    |:-------------:    |:-------------:       |
|*command*         | `string`          |Command to execute    |
|*registries*      | `[]`              |Image registries      |

### docker.#Command Outputs

_No output._

## docker.#Container

### docker.#Container Inputs

| Name              | Type                | Description                           |
| -------------     |:-------------:      |:-------------:                        |
|*ssh.host*         | `string`            |ssh host                               |
|*ssh.user*         | `string`            |ssh user                               |
|*ssh.port*         | `*22 \| int`        |ssh port                               |
|*ssh.key*          | `dagger.#Secret`    |private key                            |
|*ref*              | `string`            |Image reference (e.g: nginx:alpine)    |
|*command*          | `"""\n              # Run detach container\n                OPTS=""\n    \n    if [ ! -z "$CONTAINER_NAME" ]; then\n    \tOPTS="$OPTS --name $CONTAINER_NAME"\n    fi\n    \n    docker container run -d $OPTS "$IMAGE_REF"\n    """`    |Command to execute    |
|*env.IMAGE_REF*    | `string`            |-                                      |
|*registries*       | `[]`                |Image registries                       |

### docker.#Container Outputs

_No output._

## docker.#Image

Build a Docker image from source, using included Dockerfile

### docker.#Image Inputs

| Name             | Type                  | Description        |
| -------------    |:-------------:        |:-------------:     |
|*source*          | `dagger.#Artifact`    |-                   |

### docker.#Image Outputs

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

## docker.#RemoteContainer

Pull a docker container

### docker.#RemoteContainer Inputs

| Name             | Type              | Description                                             |
| -------------    |:-------------:    |:-------------:                                          |
|*from*            | `string`          |Remote ref (example: "index.docker.io/alpine:latest")    |

### docker.#RemoteContainer Outputs

_No output._

## docker.#RemoteImage

Push a docker image to a remote registry

### docker.#RemoteImage Inputs

| Name             | Type                  | Description                                                |
| -------------    |:-------------:        |:-------------:                                             |
|*target*          | `string`              |Remote target (example: "index.docker.io/alpine:latest")    |
|*source*          | `dagger.#Artifact`    |Image source                                                |

### docker.#RemoteImage Outputs

| Name             | Type              | Description        |
| -------------    |:-------------:    |:-------------:     |
|*ref*             | `string`          |Image ref           |
|*digest*          | `string`          |Image digest        |
