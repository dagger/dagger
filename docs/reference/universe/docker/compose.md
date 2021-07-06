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

| Name                                    | Type                     | Description                              |
| -------------                           |:-------------:           |:-------------:                           |
|*name*                                   | `*"source" \| string`    |App name (use as COMPOSE_PROJECT_NAME)    |
|*registries*                             | `[]`                     |Image registries                          |
|*deployment.command*                     | `"""\n                   if [ -n "$DOCKER_HOSTNAME" ]; then\n       \tssh -i /key -fNT -o "StreamLocalBindUnlink=yes" -L "$(pwd)"/docker.sock:/var/run/docker.sock -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME"\n    \texport DOCKER_HOST="unix://$(pwd)/docker.sock"\n    fi\n    \n    # Extend session duration\n    echo "Host *\\nServerAliveInterval 240" \>\> "$HOME"/.ssh/config\n    chmod 600 "$HOME"/.ssh/config\n    \n    # Move compose\n    if [ -d "$SOURCE_DIR" ]; then\n    \tif [ -f docker-compose.yaml ]; then\n    \t\tcp docker-compose.yaml "$SOURCE_DIR"/docker-compose.yaml\n    \tfi\n    \tcd "$SOURCE_DIR"\n    fi\n    \n    docker-compose build\n    docker-compose up -d\n    """`    |Command to execute    |
|*deployment.env.COMPOSE_PROJECT_NAME*    | `*"source" \| string`    |-                                         |
|*deployment.package."docker-compose"*    | `true`                   |-                                         |
|*deployment.registries*                  | `[]`                     |Image registries                          |

### compose.#App Outputs

_No output._

## compose.#Client

A container image to run the docker-compose client

### compose.#Client Inputs

_No input._

### compose.#Client Outputs

_No output._
