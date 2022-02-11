---
slug: /1211/go-docker-swarm
displayed_sidebar: europaSidebar
---

# Go on Docker Swarm

> TODO: particubes.com screenshot

[Particubes](https://particubes.com) is a platform dedicated to voxel games, which are games made out of little cubes (like Minecraft).
The team consists of 10 developers that like to keep things simple.

Particubes chose Dagger because it was the easiest way to integrate with their Docker Swarm production setup.
Every commit to the main branch gets deployed straight to [particubes.com](https://particubes.com) with Dagger running in GitHub Actions.
The Dagger Universe made it very easy for to build this pipeline.
`docker.#Build`, `docker.#Push` and `docker.#Command` was all that it took.

:::danger
TODO: this config is pre-Europa, the next step is to convert it to Europa.
:::

This is the entire Particubes Dagger plan:

```cue
package particubes

import (
  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
)

repo: dagger.#Input & {dagger.#Artifact}

swarmSSH: {
  user: dagger.#Input & {*"ubuntu" | string}
  host: dagger.#Input & {string}
  key: dagger.#Input & {dagger.#Secret}
}

docs: {
  containerImageName: dagger.#Input & {string}
  containerImageTag: dagger.#Input & {*"latest" | string}

  // TODO: write the GIT_SHA into a static /git_sha.txt
  buildContainerImage: docker.#Build & {
    source: repo
  }

  // TODO:
  // - [ ] bootContainer
  // - [ ] checkHttpResponse
  // - [ ] stopContainer

  publishContainerImage: docker.#Push & {
    "target": "\(containerImageName):\(containerImageTag)"
    source: buildContainerImage
  }

  deployContainerImage: docker.#Command & {
    ssh: swarmSSH
    command: "docker service update --image registry.particubes.com/lua-docs:$IMAGE_REF lua-docs"
    env: {
      "IMAGE_REF": publishContainerImage.ref
    }
  }

  // TODO: // check that the expected GIT_SHA is running in production
  // checkHttpResponse /git_sha.txt
}
```

You can find [the original pipeline](https://github.com/voxowl/particubes/blob/b698777465c02462296de37087dd3c341c29df92/lua-docs/docs.cue) in the Particubes GitHub repository.

Anyone on the Particubes team can run this pipeline locally using `dagger up`.
It also runs in GitHub Actions on every commit using the following config:

```yaml
name: Dagger/docs.particubes.com
  
on:
  push:
    branches: [ master ]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v2
        with:
          lfs: true
      - name: Dagger
        uses: dagger/dagger-action@v1
        with:
          age-key: ${{ secrets.DAGGER_AGE_KEY }}
          args: up -e docs
```

### What comes next for particubes.com?

We don't know but we would like find out 😀
