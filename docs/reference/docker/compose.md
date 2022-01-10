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

| Name             | Type                     | Description                              |
| -------------    |:-------------:           |:-------------:                           |
|*name*            | `*"source" \| string`    |App name (use as COMPOSE_PROJECT_NAME)    |
|*registries*      | `[...{\n                 username: string\n                         secret: {\n        $dagger: {\n            secret: {}\n        }\n    }\n}]`    |Image registries    |

### compose.#App Outputs

_No output._

## compose.#Client

A container image to run the docker-compose client

### compose.#Client Inputs

_No input._

### compose.#Client Outputs

_No output._
