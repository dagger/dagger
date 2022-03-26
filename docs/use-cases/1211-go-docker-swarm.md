---
slug: /1211/go-docker-swarm
displayed_sidebar: europa
---

# Go on Docker Swarm

![particubes.com](/img/use-cases/particubes.com.png)

[Particubes](https://particubes.com) is a platform dedicated to voxel games, which are games made out of little cubes, like Minecraft.
The team consists of 10 developers that like to keep things simple.
They write primarily Go & Lua, push to GitHub and use GitHub Actions for automation.
The production setup is a multi-node Docker Swarm cluster running on AWS.

The Particubes team chose Dagger for continuous deployment because it was the easiest way of integrating GitHub with Docker Swarm.
Every commit to the main branch goes straight to [docs.particubes.com](https://docs.particubes.com) via a Dagger pipeline that runs in GitHub Actions.
`universe.dagger.io/docker` made building this pipeline trivial:

:::danger
TODO: this config is meta Europa, meaning that it was not tested. Next steps:

- implement it in GitHub Actions and ensure that it all works as expected
- update this meta config to the final version that we know works
:::

```cue
package particubes

import (
  "dagger.io/dagger"
  "dagger.io/dagger/core"
  "universe.dagger.io/docker"
)

dagger.#Plan & {
  inputs: {
    directories: src: path: "./lua-docs"
    secrets: docs: command: {
      name: "sops"
      args: ["-d", "../../lua-docs/sops_secrets.yaml"]
    }
    params: {
      image: ref: docker.#Ref | *"registry.particubes.com/lua-docs:latest"
    }
  }

  actions: {
    docs: {
      // TODO: write GITHUB_SHA into a static /github_sha.txt
      build: docker.#Dockerfile & {
        source: inputs.directories.src.contents
      }

      test: {
        // TODO:
        // - run container
        // - check http response code
        // - verify /github_sha.txt value matches GITHUB_SHA
        // - stop container
      }

      push: docker.#Push & {
        dest: inputs.params.image.ref
        image: build.output
      }

      docsSecrets: core.#DecodeSecret & {
        input: inputs.secrets.docs.contents
        format: "yaml"
      }
      deploy: {
        // TODO:
        // - run this command in the remote Docker Swarm
        // - secrests are ready in docsSecrets, e.g. docsSecrets.output.swarmKey.contents
      }

      verifyDeploy: {
        // TODO:
        // - check http response code
        // - verify /github_sha.txt value matches GITHUB_SHA
      }
    }
  }
}
```

This is the GitHub Actions workflow config that invokes `dagger`, which in turn runs the above pipeline:

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
      // TODO: install sops
      - name: Dagger
        uses: dagger/dagger-action@v1
        with:
          age-key: ${{ secrets.DAGGER_AGE_KEY }}
          args: up
```

Since this is a Dagger pipeline, anyone on the team can run it locally with a single command:

```console
dagger up
```

This is the first step that enabled the Particubes team to have the same CI/CD experience everywhere.

We don't know what comes next for particubes.com, but we would like find out. Some ideas:

- deploy particubes.com with Dagger
- manage the Docker Swarm cluster with Dagger
- contribute `universe.dagger.io/particubes` package

:::tip
The latest version of this pipeline can be found at [github.com/voxowl/particubes/lua-docs/docs.cue](https://github.com/voxowl/particubes/blob/b698777465c02462296de37087dd3c341c29df92/lua-docs/docs.cue)
:::
