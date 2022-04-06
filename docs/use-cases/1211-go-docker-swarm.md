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
Every commit to the main branch goes straight to [docs.particubes.com](https://docs.particubes.com) via a Dagger pipeline that runs in GitHub Actions. Let us see how the Particubes Dagger plan fits together.

## Actions API

This is a high level overview of all actions in the Particubes docs Dagger plan:

![particubes flat plan](/img/use-cases/particubes-actions.png)

We can see all available actions in a Plan by running the following command:

```console
$ dagger do
Execute a dagger action.

Available Actions:
 build  Create a container image
 clean  Remove a container image
 test   Locally test a container image
 deploy Deploy a container image
```

## Client API

Dagger actions usually need to interact with the host environment where the Dagger client runs. The Particubes' plan uses environment variables and the filesystem.

This is an overview of all client interactions for this plan:

![Client API](/img/use-cases/client-api.png)

This is what the above looks like in the Dagger plan config:

```cue file=../tests/use-cases/go-docker-swarm/client-api.cue.fragment

```

## The `build` Action

This is a more in-depth overview of the _build_ action and how it interacts with the client in the Particubes docs Dagger plan:

![build action](/img/use-cases/build-action.png)

This is what the above looks like in the Dagger plan config:

```cue file=../tests/use-cases/go-docker-swarm/build-action.cue.fragment

```

## GitHub Action integration

This is the GitHub Actions workflow config that invokes `dagger`, which in turn runs the full plan:

```yaml
name: Dagger/docs.particubes.com

on:
  push:
    branches: [master]

jobs:
  deploy:
    runs-on: ubuntu-latest
    env:
      GITHUB_SHA: ${{ github.sha }}
      SSH_PRIVATE_KEY_DOCKER_SWARM: ${{ secrets.SSH_PRIVATE_KEY_DOCKER_SWARM }}
    steps:
      - name: Checkout code
        uses: actions/checkout@v3

      - name: Install Dagger
        uses: dagger/dagger-action@v2
        with:
          install-only: true

      - name: Dagger project update
        run: dagger project update

      - name: Dagger do test
        run: dagger do test --log-format plain

      - name: Dagger do deploy
        run: dagger do deploy --log-format plain
```

Since this is a Dagger pipeline, anyone on the team can run it locally with a single command:

```console
dagger do
```

This is the first step that enabled the Particubes team to have the same CI/CD experience everywhere.

## Full Particubes docs Dagger plan

This is the entire plan running on Particubes' CI:

```cue file=../tests/use-cases/go-docker-swarm/full/particubes.docs.cue

```

## What comes next ?

Particubes' team suggested that we create a `dev` action with _hot reload_, that way Dagger would even asbtract away the ramp-up experience when developing the doc

:::tip
The latest version of this pipeline can be found at [github.com/voxowl/particubes/pull/144](https://github.com/voxowl/particubes/blob/2af173596729929cfb7a7a1f78f1ec0d8b685e5e/lua-docs/docs.cue)
:::
