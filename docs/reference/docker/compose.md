---
sidebar_label: compose
---

# alpha.dagger.io/docker/compose

Docker-compose operations

```cue
import "alpha.dagger.io/docker/compose"
```

## compose.#App

### compose.#App Inputs

| Name                             | Type                                     | Description                                                                                                                                             |
| -------------                    |:-------------:                           |:-------------:                                                                                                                                          |
|*name*                            | `source`                                 |App name (use as COMPOSE_PROJECT_NAME)                                                                                                                   |
|*registries*                      | `[]`                                     |Image registries                                                                                                                                         |
|*run.command*                     | `if [ -n "$DOCKER_HOSTNAME" ]; then\n    ssh -i /key -fNT -o "StreamLocalBindUnlink=yes" -L "$(pwd)"/docker.sock:/var/run/docker.sock -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME"\n    export DOCKER_HOST="unix://$(pwd)/docker.sock"\nfi\n\n# Extend session duration\necho "Host *\nServerAliveInterval 240" \>\> "$HOME"/.ssh/config\nchmod 600 "$HOME"/.ssh/config\n\n# Move compose\nif [ -d "$SOURCE_DIR" ]; then\n    if [ -f docker-compose.yaml ]; then\n        cp docker-compose.yaml "$SOURCE_DIR"/docker-compose.yaml\n    fi\n    cd "$SOURCE_DIR"\nfi\n\ndocker-compose build\ndocker-compose up -d`    |Command to execute    |
|*run.env.COMPOSE_PROJECT_NAME*    | `source`                                 |-                                                                                                                                                        |
|*run.package."docker-compose"*    | `true`                                   |-                                                                                                                                                        |
|*run.registries*                  | `[]`                                     |Image registries                                                                                                                                         |

### compose.#App Outputs

_No output._

## compose.#Client

A container image to run the docker-compose client

### compose.#Client Inputs

_No input._

### compose.#Client Outputs

_No output._
