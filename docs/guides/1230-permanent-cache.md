---
slug: /1230/permanent-cache-with-dagger
displayed_sidebar: 0.2
---

# Permanent cache your CI

CI that takes eternity is a real pain and can become a bottleneck when your 
infrastructure and process grow. But Dagger, working with a Buildkit daemon, 
have a powerful cache system that triggers actions only when it's necessary.

However, sometime you can't keep the same Buildkit daemon along your CI/CD.
For instance, if you use GitHub runner, your daemon will be created on each
run and **cache will be lost**. 

In this page, we will see how to use `--cache-from` and `--cache-to` flags 
keep a permanent cache, from a local environment to GitHub action.

## Ephemeral cache

As an example, we will use a Dagger plan to build a go program.

You can use any go project and the following snippet to build it in a
standard way

```cue
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

To build go binary, just tip `dagger do build`, it should take some time to
install dependencies, build binary and output it.

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

But if you stop Buildkit daemon and remove volume, cache will be lost and
all actions will be executed again.

:::info
Now we have seen how ephemeral cache works, let's continue to understand how
store cache in your local filesystem, so you can clean your docker engine without
losing all your CI's cache.
:::

## Keep cache in your local filesystem

To store cache in your local filesystem, you don't need much effort : just
add `--cache-from type=local,mode=max,dest=<output folder>` to `dagger do build`.

:::tip
Using `mode=max` argument will cache **all** layers from intermediate
steps, with is really useful in the context of Dagger where you will have
multiple steps to execute.
:::

Here's an example that exports cache to `storage`.

```shell
dagger do build --cache-to type=local,mode=max,dest=storage 
# ...

tree storage -L 1   
storage
├── blobs
├── index.json
└── ingest
```

A new directory has been created that contains cache the run

:::caution
If you run different action in the plan, don't forget to store cache in different
destination, so it will be not overwritten.
:::

To import the cache previously stored, you can use `--cache-from type=local,src=<cache folder>`.

Here's an example, using a new buildkit daemon

```shell
# Down buildkit daemon
docker container stop dagger-buildkitd && docker container rm dagger-buildkitd && docker volume rm dagger-buildkitd

# Import cache on rebuild
dagger do build --cache-to type=local,mode=max,dest=storage --cache-from type=local,src=storage
[✔] actions.build.container                                                 2.3s
[✔] client.filesystem.".".read                                              0.1s
[✔] actions.build.container.export                                          0.0s
[✔] client.filesystem."./output".write                                      0.4s
```

:::info
In this part, we have how to keep cache in a local filesystem, if you want
to see more options on local export, look at [Buildkit cache documentation](https://github.com/moby/buildkit#local-directory-1)
:::

## Keep cache in a remote registry

Buildkit can also import/export cache to a registry, it's a great way to share cache between your team and avoid
flooding your filesystem.

:::tip
Using a registry as cache storage is more efficient than local storage because Buildkit will only re-export
missing layers on multiple runs.
:::

Let's first deploy a simple registry in your localhost

```shell
docker run -d -p 6000:5000 --restart=always --name cache-registry registry:2
```

Then it's not much different from local export

```shell
dagger do build --cache-to type=registry,mode=max,ref=localhost:5000/cache --cache-from type=registry,ref=localhost:5000/cache
[✔] actions.build.container                                                 1.3s
[✔] client.filesystem.".".read                                              0.0s
[✔] actions.build.container.export                                          0.0s
[✔] client.filesystem."./output".write                                      0.1s
```

:::info
See more options on registry export at [Buildkit cache documentation](https://github.com/moby/buildkit#registry-push-image-and-cache-separately)
:::

## Keep cache in GitHub Actions

Buildkit has a great integration to store cache from GitHub Action.  
That features is really powerful with Dagger because you can cache everything
that has not changes in your PR.

// Require integrating cache in Dagger action
Coming soon...
