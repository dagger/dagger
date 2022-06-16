---
slug: /1241/docker
displayed_sidebar: "0.2"
---

# The docker package

The `universe.dagger.io` module is meant to provide higher level abstractions on top of [core actions](../../references/1222-core-actions-reference.md). Of these, the `universe.dagger.io/docker` package provides a general base for building and running docker images.

Let's explore what you can do with this package.

:::tip

There's multiple packages that use this general `docker` package, and build on top of it with even higher abstractions. See the [bash](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/bash), [python](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/python) and [alpine](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/alpine) packages for examples.

:::

## `docker.#Image`

While [core actions](../../references/1222-core-actions-reference.md) handle the file system tree and metadata separately, at the center of the `docker` package is the `#Image` structure which packs both in the same field:

```cue
// A container image
#Image: {
    // Root filesystem of the image
    rootfs: dagger.#FS

    // Image config
    config: core.#ImageConfig
}
```

All `docker` actions pass this structure around.

<!--FIXME: example when this is useful
### `docker.#Scratch`

If you need an empty image (no files, empty metadata), you can use `docker.#Scratch`. It's the equivalent of a Dockerfile with `FROM scratch`.
-->

## Base actions

Let's go through the common example of building an image just so we cover every action. More detailed explanations will follow, just refer back to the example for context.

```cue file=../../tests/guides/docker/base.cue
```

:::tip

For this example, ensure you have a registry on `localhost` listening on port `5042`:

```shell
➜ docker run -d -p 5042:5000 --restart=always --name localregistry registry:2
```

:::

:::tip

You can see more examples in the [Building container images](./1205-container-images.md) guide.

:::

### `docker.#Pull`

In most cases, you'll need to pull a docker image from a docker registry in order to work on top of it with dagger. Authentication is supported via a simple `username` and `secret` combination, although these credentials can be fetched through more complex means (see the [AWS package](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io/aws) for an example).

```cue file=../../tests/guides/docker/pull.cue
```

### `docker.#Set`

The image metadata (i.e., [image config](https://github.com/dagger/dagger/blob/main/pkg/dagger.io/dagger/core/image.cue)) can be changed with the `docker.#Set` action. It takes an `#Image` as input, configurations to change, and outputs a new image with the changed metadata. The files in the image (`dagger.#FS`) are untouched.

This is only additive. It either adds a field or replaces an existing one.

For example, let's say you want to change the default user, working directory, set an environment variable and expose a port:

```cue
_set: docker.#Set & {
    input: image.output
    config: {
        user: "nginx"
        workdir: "/app"
        env: APP_ROOT: "/app"
        expose: "8080/tcp": {}
    }
}
```

This is usually used with [`docker.#Build`](#dockerbuild), which conveniently hooks inputs and outputs from sequencial steps.

:::tip

If you need to reference a previous value, even in `docker.#Build`, just use `input.config`:

```cue
docker.#Set & {
    input: _
    config: env: PATH: "/app/bin:\(input.config.env.PATH)"
},
```

:::

### `docker.#Copy`

This action copies a file system tree ([`dagger.#FS`](../../references/1234-dagger-types-reference.md)) into an image. You can select source and destination paths and include/exclude patterns.

By default, the destination path is relative to the working directory if defined in the image metadata. If not, the default is root (i.e., `/`). The source path always defaults to the root of the file system tree to copy (from `contents`).

```cue
_copy: docker.#Copy & {
    input: _pull.output

    // files to copy into input image
    contents: app

    // optionally copy only a sub directory from "contents" (use absolute path)
    source: "/src"

    // absolute destination path, always used as is
    dest: "/app"

    // relative to the input image's "workdir" or to the default "/" if not set
    dest: "app"

    // with `workdir: "/app"`, "dest" can be omitted
}
```

You can also limit which files to copy via a pattern if the files you need aren't grouped in a sub directory.

```cue
_copy_: docker.#Copy & {
    input: _pull.output
    contents: app
    include: ["**/*.py", "*.toml", "Poetry*"]
    exclude: ["tests"]
}
```

### `docker.#Run`

This is the most complex and versatile action in the *docker* package. There's quite a bit of abstractions and useful conveniences. Let's look at a few of them.

#### Defaults

Some fields use the image's metadata as defaults if not defined. These are: `entrypoint`, `command`, `env`, `workdir` and `user`.

This means, for example, that the image's environment variables are automatically accessible, but also that you can run a command as `user: "root"` if the image's user is something different, without affecting the image's metadata for later actions.

#### Entrypoint

The `entrypoint` field exists only for compatibility reasons. Avoid it if possible. In the end, it just gets prepended to the command to run so you can use `command: name` instead.

:::tip

If you want to ignore the image's entrypoint when running your command, you can clear it by setting it to an empty list:

```cue
docker.#Run & {
    entrypoint: []
    command: ...
}
```

:::

#### Command

The `command` field has 3 components: a `name` string, an `args` list and a `flags` struct. They will be combined in the following manner: `<name> <flags> <args>`.

Flag values can either be `true` for just adding the field name, or a `string` to append to the flag.

For example:

```cue
command: {
    name: "bash"
    args: ["/run.sh", "-l", "debug"]
    flags: {
        "--norc": true
        "-e":     true
        "-u":     true
        "-o":     "pipefail"
    }
}

// will produce
cmd: ["bash", "--norc", "-e", "-u", "-o", "pipefail", "/run.sh", "-l", "debug"]
```

#### Secret environment variables

Unlike the image metadata, `docker.#Run` environment variables support `dagger.#Secret` values as well as strings. It's a very simple way to access a secret from a command.

You can read more on this in [Using secrets in a `docker.#Run`](../../core-concepts/1204-secrets.md#in-a-dockerrun).

#### Mounts

The following mount types are available:

- Secret ([example](../../core-concepts/1204-secrets.md#in-a-dockerrun))
- cache
- temporary directory
- directory
- network socket ([example](../../core-concepts/1203-client.md#using-a-local-socket))

Always specify `contents` and `dest` fields.

The type of the mount is inferred from the value of the `contents` field:

```cue file=../../tests/guides/docker/mounts.cue
```

<!--FIXME: create separate guide on using mounts and link here-->

#### Exports

It's very common to want to use `core.#ReadFile` to get a file's string contents that a command produced, a `core.#Subdir` to extract a sub directory from the resulting image as a `dagger.#FS` or even `core.#NewSecret` to get the contents of a file as a `dagger.#Secret`. `docker.#Run` allows you to *export* all of that in a very convenient way:

```cue
_run: docker.#Run & {
    // mounts, command, etc

    export: {
        // notice: you can have multiple paths
        // under each of these fields
        files: "/output.txt": _
        secrets: "/token.txt": _
        directories: "/app/dist": _
    }
}

// reference in other fields
output: _run.export.files."/output.txt"     // string
token:  _run.export.secrets."/token.txt"    // dagger.#Secret
dist:   _run.export.directories."/app/dist" // dagger.#FS
```

:::tip

As in [Use *top* to match anything](../../guidelines/1226-coding-style.md#use-top-to-match-anything), the *export* fields `files`, `secrets` and `directories` are already sufficient to declare the type, so we use *top* (`_`) as a simpler alternative to this:

```cue
    export: {
        // tip: use `_` instead
        files: "/output.txt": string
        secrets: "/token.txt": dagger.#Secret
        directories: "/app/dist": dagger.#FS
    }
```

:::

:::caution

You can't export from mounts.

:::

#### Skipping cache

If you need to skip the cache for a `docker.#Run`, set `always: true` (as in "always run").

See [How to always execute an action?](../actions/1231-always-execute.md) for more information.

### `docker.#Push`

This is the opposite of `docker.#Pull`. It's only needed when publishing a built image to a docker registry for use elsewhere. It supports the same `auth` field as `docker.#Pull` and it returns the complete reference in the `result` field, digest included.

If you target the push action directly, you'll get this value printed on the screen:

```shell
➜ dagger do push
[✔] ...
[✔] actions.push
Field   Value
result  "localhost:5042/example:latest@sha256:47a163eb7b572819d862b4a2c95a399829c8c79fab51f1d40c59708aa0e35331"
```

:::tip

Another useful pattern is to save it in a `json` file in order to be consumed by another automated process.

:::

:::tip

If you're interested in knowing more about controling the output, check out the [Handling action outputs](../actions/1228-handling-outputs.md#controlling-the-output) guide.

:::

## `docker.#Build`

The `docker.#Build` action is a convenience for building a docker image, so you'd **use it when you care about a `docker.#Image` in the end**. Additionally, these conditions need to be met:

- You have a list of sequencial actions to run;
- All actions have `output: docker.#Image` fields;
- All actions have `input: docker.#Image` fields (except the first one where it's optional);

It takes care of hooking the outputs to the inputs, and to make up names for their fields. See the difference from the previous section's example on the `#PythonBuild` action:

```cue file=../../tests/guides/docker/build.cue
```

Notice the difference of using a [list](https://cuelang.org/docs/tutorials/tour/types/lists/) here instead of a [struct](https://cuelang.org/docs/tutorials/tour/types/optional/), so don't forget your commas.

:::tip

There's a guide specifically for [Building container images](../concepts/1205-container-images.md), with more examples.

:::

:::tip

Your first step can be any `docker.#Image` field, even if built outside of `docker.#Build`. Here's two ways to hook it in:

```cue
_base: ... // something that produces a `image: docker.#Image`

// 1. Create a struct with an `output` field
_build: docker.#Build & {
    steps: [
        // Only the output field is mandatory so the
        // next action uses it as input
        { output: _base.image },
        docker.#Run & { ... },
    ]
}

// 2. Use the first action's `input` field
_build: docker.#Build & {
    steps: [
        // Simpler
        docker.#Run & {
            input: _base.image
            ...
        },
    ]
}

```

:::

:::caution

Don't attempt to reference actions from `steps` directly, as it won't work. This field is only a convenience for generating new actions that hook the inputs and outputs correctly.

For example, if you find the need to use the [export](#exports) field from a `docker.#Run` step you'll either need to use a `core.#ReadFile` or `core.#Subdir` directly on the resulting image, or go back to not using `docker.#Build` so you can access other fields freely.

```cue
_build: docker.#Build & {
    steps: [
        ...,
        // third action in the list
        docker.#Run & {
            command: ...
            export: directories: "/wheels": _
        },
        ...
    ]
}

// This won't work!
wheels: _build.steps[2].export.directories."/wheels"
```

To avoid defaulting to `docker.#Build` and finding you have to break away from this convenience later, think if a `docker.#Image` is what you care about in the end or not. If not, then it's perhaps better to avoid it for greater flexibility.

:::

:::caution

There's currently a limitation for nesting `docker.#Build` actions **more than** 3 levels deep. It looks like this:

```cue
docker.#Build & {
    steps: [
        docker.#Build & {
            steps: [
                docker.#Build & {
                    steps: [
                        ...,
                    ]
                },
            ]
        },
    ]
}
```

It may not be so obvious. You need to be aware if the actions you're using are evaluating to a `docker.#Build` underneath.

For more context on this, see [issue #1466](https://github.com/dagger/dagger/issues/1466).

:::

## `docker.#Dockerfile`

:::caution

Do not be confused with `core.#Dockerfile`. Remember that packages in *universe* are prefered over core actions whenever possible, since they represent higher-level abstractions.

:::

You're encouraged to build your images using CUE, but sometimes you need compatibility for using the `Dockerfile` files you already have.

In this example, let's assume you have a `Dockerfile` in your current directory:

```cue file=../../tests/guides/docker/dockerfile.cue
```

If it has a different name, it can be specified as well:

```cue
build: docker.#Dockerfile & {
    source: client.filesystem.".".read.contents
    dockerfile: path: "Dockerfile.production"
}
```

And you can also specify the `Dockerfile` instructions directly, without loading a file:

```cue
build: docker.#Dockerfile & {
    source: client.filesystem.".".read.contents
    dockerfile: contents: """
        FROM ubuntu
        // ...
        """
}
```

:::tip

Check the [Building container images](../concepts/1205-container-images.md#executing-a-dockerfile) guide for more on how to embed `Dockerfile` instructions directly in CUE.

:::

### Authentication

Like `docker.#Pull` and `docker.#Push` there's also support for authentication, but unlike those, multiple registries can be defined because a `Dockerfile` can use images from multiple places (e.g., `FROM`, `COPY --from`).

```cue file=../../tests/guides/docker/dockerfile_auth.cue
```

### Target

You can build a single named build stage in a multi-stage build. This is useful to have a single `Dockerfile` declare multiple base images to publish, like *build* and *run* images.

```Dockerfile title="Dockerfile" file=../../tests/guides/docker/Dockerfile
```

```cue title="dagger.cue" file=../../tests/guides/docker/dockerfile_target.cue
```

You can use these base images later in your app's own multi-stage build.

### Build arguments

For build arguments, add the `buildArgs` struct:

```Dockerfile title="Dockerfile"
ARG PYTHON_VERSION
FROM python:${PYTHON_VERSION}
```

```cue
build: docker.#Dockerfile & {
    source: client.filesystem.".".read.contents
    buildArgs: PYTHON_VERSION: "3.9"
}
```

## Connecting to a docker engine

There's a `universe.dagger.io/docker/cli` sub-package to interact directly with a `docker` binary (CLI), in connection with a local or remote docker engine.

Refer to these actions' specific guides for more information:

- `cli.#Load` - [Loading an image into a docker engine](../docker/1216-docker-cli-load.md)
- `cli.#Run` - [Running commands with the docker binary](../docker/1217-docker-cli-run.md)
