---
slug: /sdk/cue/564914/go-docker-swarm
displayed_sidebar: 'current'
---

# Go on Docker Swarm

![particubes.com](/img/use-cases/particubes.com.png)

[Particubes](https://particubes.com) is a platform dedicated to voxel games, which are games made out of little cubes, like Minecraft.
The team consists of 10 developers that like to keep things simple.
They write primarily Go & Lua, push to GitHub and use GitHub Actions for automation.
The production setup is a multi-node Docker Swarm cluster running on AWS.

The Particubes team chose Dagger for continuous deployment because it was the easiest way of integrating GitHub with Docker Swarm.
Every commit to the main branch goes straight to [docs.particubes.com](https://docs.particubes.com) via a Dagger pipeline that runs in GitHub Actions. Let us see how the Particubes fits together.

## Actions API

This is a high level overview of all actions in the Particubes plan:

![particubes flat plan](/img/use-cases/particubes-actions.png)

We can see all available actions in a Plan by running the following command:

```console
$ dagger-cue do
Execute a dagger action.

Available Actions:
 build  Create a container image
 clean  Remove a container image
 test   Locally test a container image
 deploy Deploy a container image
```

## Client API

Dagger Engine actions usually need to interact with the host environment where the client runs. The Particubes' plan uses environment variables and the filesystem.

This is an overview of all client interactions for this plan:

![Client API](/img/use-cases/client-api.png)

This is what the above looks like in the plan config:

```cue file=../tests/use-cases/go-docker-swarm/client-api.cue.fragment

```

## The `build` Action

This is a more in-depth overview of the _build_ action and how it interacts with the client in the Particubes plan:

![build action](/img/use-cases/build-action.png)

This is what the above looks like in the plan config:

```cue file=../tests/use-cases/go-docker-swarm/build-action.cue.fragment

```

## GitHub Action integration

This is the GitHub Actions workflow config that invokes `dagger-cue`, which in turn runs the full plan:

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
        uses: dagger/dagger-for-github@v3
        with:
          install-only: true

      - name: dagger-cue project update
        run: dagger-cue project update

      - name: dagger-cue do test
        run: dagger-cue do test --log-format plain

      - name: dagger-cue do deploy
        run: dagger-cue do deploy --log-format plain
```

Since this is a Dagger pipeline, anyone on the team can run it locally with a single command:

```console
dagger-cue do
```

This is the first step that enabled the Particubes team to have the same CI/CD experience everywhere.

## Full Particubes plan

This is the entire plan running on Particubes' CI:

```cue file=../tests/use-cases/go-docker-swarm/full/particubes.docs.cue

```

:::tip
The latest version of this pipeline can be found at [github.com/voxowl/particubes/pull/144](https://github.com/voxowl/particubes/blob/2af173596729929cfb7a7a1f78f1ec0d8b685e5e/lua-docs/docs.cue)
:::
