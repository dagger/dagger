---
sidebar_label: compose
---

# alpha.dagger.io/docker/compose

```cue
import "alpha.dagger.io/docker/compose"
```

## compose.#Client

A container image to run the docker-compose client

### compose.#Client Inputs

_No input._

### compose.#Client Outputs

_No output._

## compose.#Up

### compose.#Up Inputs

| Name                             | Type              | Description                           |
| -------------                    |:-------------:    |:-------------:                        |
|*registries*                      | `[]`              |Image registries                       |
|*run.command*                     | `"""\n            if [ -n "$DOCKER_HOSTNAME" ]; then\n    \tssh -i /key -fNT -o "StreamLocalBindUnlink=yes" -L "$(pwd)"/docker.sock:/var/run/docker.sock -p "$DOCKER_PORT" "$DOCKER_USERNAME"@"$DOCKER_HOSTNAME"\n    \texport DOCKER_HOST="unix://$(pwd)/docker.sock"\n    fi\n    \n    # Extend session duration\n    echo "Host *\\nServerAliveInterval 240" \>\> "$HOME"/.ssh/config\n    chmod 600 "$HOME"/.ssh/config\n    \n    # Move compose\n    if [ -d "$SOURCE_DIR" ]; then\n    \tif [ -f docker-compose.yaml ]; then\n    \t\tcp docker-compose.yaml "$SOURCE_DIR"/docker-compose.yaml\n    \tfi\n    \tcd "$SOURCE_DIR"\n    fi\n    \n    docker-compose build\n    docker-compose up -d\n    """`    |Command to execute    |
|*run.package."docker-compose"*    | `true`            |-                                      |
|*run.registries*                  | `[]`              |Image registries                       |

### compose.#Up Outputs

_No output._
