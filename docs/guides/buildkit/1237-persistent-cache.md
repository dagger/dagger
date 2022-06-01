---
slug: /1237/persistent-cache-with-dagger
displayed_sidebar: 0.2
---

# Persistent cache

CI that takes an eternity is a real pain and can become a bottleneck when your
infrastructure and process grow. But Dagger, working with a Buildkit daemon,
has a powerful cache system that triggers actions only when it's necessary.

However, sometimes you can't keep a persistent Buildkit daemon along your CI/CD.
For instance, if you use a GitHub Actions runner, your daemon will be created on each
run and **cache will be lost**.

In this page, we will see how to use `--cache-from` and `--cache-to` flags to
keep a permanent cache, from a local environment to GitHub Actions.

## Ephemeral cache

As an example, we will use a Dagger plan to build a Go program.

You can use any Go project and the following snippet to build it in a
standard way

```cue file=../../tests/guides/persistent-cache/build.cue title="build.cue"
package ci

import (
  "dagger.io/dagger"
  "universe.dagger.io/go"
)

dagger.#Plan & {
  // Retrieve source from current directory
  // Input
  client: filesystem: ".": read: {
    contents: dagger.#FS
    // Include only contents we need to build our project
    // Any other files or patterns can be added
    include: ["**/*.go", "go.mod", "go.sum"]
  }

  // Output
  client: filesystem: "./output": write: {
    contents: actions.build.output
  }

  actions: {
    // Alias on source
    _source: client.filesystem.".".read.contents

    // Build go binary
    build: go.#Build & {
      source: _source
    }
  }
}
```

To build a Go binary, just run `dagger do build`. It should then take some time to
install dependencies, build the binary and output it.

Here's an example of a run

```shell
[✔] actions.build.container                                               338.0s
[✔] client.filesystem.".".read                                              0.1s
[✔] actions.build.container.export                                          0.1s
[✔] client.filesystem."./output".write                                      0.2s
```

Indeed, Dagger has an ephemeral cache so if you rerun it, that shouldn't take
that long.

```shell
[✔] actions.build.container                                                 2.9s
[✔] client.filesystem.".".read                                              0.0s
[✔] actions.build.container.export                                          0.0s
[✔] client.filesystem."./output".write                                      0.1s
```

But if you stop the Buildkit daemon and remove its storage volume, cache will be lost and
all actions will be executed again.

:::info
Now we have seen how ephemeral cache works, let's continue to understand how
store cache in your local filesystem, so you can clean your Buildkit daemon without
losing all your CI's cache.
:::

## Persistent cache in a remote registry

Buildkit can also import/export cache to a registry. This is a great way to
share cache between your team and avoid flooding your filesystem.

:::tip
Using a registry as cache storage is more efficient than local storage because Buildkit will only re-export
missing layers on multiple runs.
:::

To store cache in an external registry, you just need to add flags to dagger command :
`dagger do <action> --cache-to type=registry,mode=max,ref=<registry target>/<image> --cache-from type=registry,ref=<registry target>/<image>`

:::tip
Using `mode=max` argument will cache **all** layers from intermediate
steps, which is really useful in the context of Dagger where you will have
multiple steps to execute.
To only store the final layers of the exported result, use `mode=min`.
:::

Let's first deploy a simple registry in your localhost

```shell
docker run -d -p 5000:5000 --restart=always --name cache-registry registry:2
```

Then run `dagger do build` with export cache flags.

```shell
dagger do build --cache-to type=registry,mode=max,ref=localhost:5000/cache --cache-from type=registry,ref=localhost:5000/cache
[✔] actions.build.container                                                 1.3s
[✔] client.filesystem.".".read                                              0.0s
[✔] actions.build.container.export                                          0.0s
[✔] client.filesystem."./output".write                                      0.1s
```

:::info
See more options on registry export at [Buildkit cache documentation](https://github.com/moby/buildkit/blob/v0.10.1/README.md#registry-push-image-and-cache-separately)
:::

## Persistent cache in your local filesystem

To store cache in your local filesystem, you just need to change flags values to match `type=local`.

Here's an example that exports cache to a local directory at path `./storage`.

```shell
dagger do build --cache-to type=local,mode=max,dest=storage
# ...

tree storage -L 1
storage
├── blobs
├── index.json
└── ingest
```

As shown above, new directory has been created that contains cache artifacts from the run

:::caution
Local cache exports will overwrite anything already present in the directory,
including any previous cache exports. Cache for distinct actions can be
exported to different directories in order to not overwrite each other,
but this may currently result in duplicated data if the two actions share
any exported cache data in common.
:::

To import the cache previously stored, you can use `--cache-from type=local,src=<cache folder>`.

Here's an example, using a new buildkit daemon running on a Docker installation

```shell
# Down buildkit daemon
# It throws all buildkit data
docker container stop dagger-buildkitd && docker container rm dagger-buildkitd && docker volume rm dagger-buildkitd

# Import cache on rebuild
dagger do build --cache-to type=local,mode=max,dest=storage --cache-from type=local,src=storage
[✔] actions.build.container                                                 2.3s
[✔] client.filesystem.".".read                                              0.1s
[✔] actions.build.container.export                                          0.0s
[✔] client.filesystem."./output".write                                      0.4s
```

:::info
In this part, we have learned to export and import cache using a local filesystem, if you want
to see more options on local export, look at [Buildkit cache documentation](https://github.com/moby/buildkit/blob/v0.10.1/README.md#local-directory-1)
:::

## Persistent cache in GitHub Actions

Buildkit has a builtin support for storing cache with GitHub Action.

This cache backend is not that different from `local` or `registry` exports, Let's integrate cache in a
simple workflow using the one:

```yaml title=".github/workflows/build-example.cue"
name: "Dagger Export"

on:
  push:
    branches: [ main ]

jobs:
  build:
    name: "Build"
    runs-on: ubuntu-latest
    # Set cache export environment
    env:
      DAGGER_CACHE_FROM: type=gha,scope=dagger-cache-example
      DAGGER_CACHE_TO: type=gha,mode=max,scope=dagger-cache-example
    steps:
      - name: Checkout
        uses: actions/checkout@v2

      - name: "Run Dagger"
        uses: dagger/dagger-for-github@v3
        with:
          cmds: |
             do build
```

:::warning
To avoid invalidating cache between your PR, you can take inspiration from [Dagger ci](https://github.com/dagger/dagger/blob/v0.2.11/.github/workflows/dagger-ci.yml#L61)
:::

:::info
See more options on GitHub export at [Buildkit cache documentation](https://github.com/moby/buildkit/blob/v0.10.1/README.md#github-actions-cache-experimental)
:::
