---
slug: /sdk/cue/667572/dagger-fs
displayed_sidebar: 'current'
---

# Filesystems: `#FS`

Along with container images, filesystems are one of the building blocks of the Dagger CUE SDK. They are represented by the `dagger.#FS` type. An `#FS` is a reference to a filesystem tree: a directory storing files in a hierarchical/tree structure.

## Filesystems are everywhere

Filesystems are key to any CI pipeline. Pipeline are essentially a series of transformations applied on filesystems until deployment. You may, for example:

- load code
- compile binaries
- run unit/integration tests
- deploy code/artifacts
- do anything that can possibly be done with a container

Each of these use cases requires a change or transfer of data, in the form of files in directories, from one action/step to another. The `dagger.#FS` makes that transfer possible.

### `docker.#Image` vs `dagger.#FS`

You need an understanding of how filesystems relate to core actions and container images to fully benefit from the power of the CUE SDK.

#### The core API

The Dagger CUE SDK leverages, at its core, a low level API to interact with filesystem trees [(see reference)](../references/565505-core-actions-reference.md#core-actions-related-to-filesystem-trees). Every other Universe package is just an abstraction on top of these low-level `core` primitives.

Let's dissect one:

```cue
// Create one or multiple directory in a container
#Mkdir: {
    $dagger: task: _name: "Mkdir"

    // Container filesystem
    input: dagger.#FS

    // Path of the directory to create
    // It can be nested (e.g : "/foo" or "/foo/bar")
    path: string

    // Permissions of the directory
    permissions: *0o755 | int

    // If set, it creates parents' directory if they do not exist
    parents: *true | false

    // Modified filesystem
    output: dagger.#FS @dagger(generated)
}
```

`core.#Mkdir` is the dagger equivalent of the `mkdir` command: it takes as `input` a `dagger.#FS` and retrieves a `dagger.#FS` containing the newly created folders.

As Dagger CUE is statically typed, you can look at an action definition to see the types that an action requires or outputs. In most cases, an action will either take as input a `dagger.#FS` or a `docker.#Image`. Let's look inside a `docker.#Image` to see the `#FS` inside.

#### Dissecting `docker.#Image`

Inspecting the `docker.#Image` package is a good way to grasp the relation between filesystems and images:

```cue
// A container image
#Image: {
    // Root filesystem of the image.
    rootfs: dagger.#FS

    // Image config
    config: core.#ImageConfig
}
```

Many [Universe packages](https://github.com/dagger/dagger/tree/main/pkg/universe.dagger.io) don't accept filesystems (`dagger.#FS`) directly as `input`, but rely on images (`docker.#Image`) instead, to let users specify the environment in which an action's logic will be executed.

As you can see, `docker.#Image` and `dagger.#FS` are closely tied, since an image is just a filesystem (`rootfs`) together with some configuration (`config`).

Since every `docker.#Image` contains a `rootfs` field, it is possible to access the `dagger.#FS` of any `docker.#Image`. This is very useful when copying filesystems between container images, passing an `#FS` to an action that requires one, or exporting the final filesystem/files/artifacts after a build to use with other actions (or to save on the client filesystem).

The corollary is also true: from a given filesystem, building up a `docker.#Image` is possible (with the help of the `dagger.#Scratch` type and some `core` actions).

:::note
Sometimes you need an empty filesystem. For example, to start a minimal image build. The docker package implements a `docker.#Scratch` image by relying on the `dagger.#Scratch` type. `dagger.#Scratch` is a core type representing a minimal rootfs.

```cue
#Scratch: #Image & {
    rootfs: dagger.#Scratch
    config: {}
}
```

:::

[Learn more about the docker package](../guides/concepts/966156-docker.md)

## Moving filesystems between actions

Let's explore, in-depth, how to transfer filesystems between actions.

### Topology of a Universe action (from an `#FS` perspective)

As stated above, most of actions require a `docker.#Image` as `input`. As an image contains a `rootfs`, the first `#FS` of an action is its `rootfs`. As some actions create artifacts, these computed outputs are a second type of filesystem living inside the `rootfs`.

![Universe action topology](/img/core-concepts/fs/universe-action-topology.png)

Lastly, some filesystems need to be shared between actions, and only need to live during the lifetime of its execution logic.
This is the last type of filesystem that you might encounter: for the sake of the guide, let's call them `additional filesystems`.

### Transfer of filesystems via `docker.#Image`

As most of actions run within a container image, an easy way to transfer an `fs` is to make the `output` image of an action the `input` of the next one. In other words, to make the state of the `rootfs` after execution, the initial `rootfs` of my second action.

![Universe action topology](/img/core-concepts/fs/fs-share-via-rootfs.png)

A common use-case is to create an action whose sole purpose is to install all the required packages/dependencies, and let the next action execute the core logic:

```cue file=../tests/core-concepts/fs/client/input_output.cue title="dagger-cue do test --log-format plain"

```

A simplified visual representation of above plan would be:

![diagram-representing-above-bash-example](/img/core-concepts/fs/explanation-bash-example.png)

### Transfer of `#FS` via mounts

A `mount` is way to add new filesystem layers to a container image. With mounts, an image will not only have a `rootfs`, but also as many filesystem layers as the amount of `fs` mounted.

![diagram-representing-action-with-mounts](/img/core-concepts/fs/mounts-action-mounts.png)

#### Difference between a Docker bind mount and Dagger CUE SDK mounts

Dagger CUE SDK mounts are very similar to the docker ones you may be familiar with. You can mount filesytems from your underlying dev/CI machine (host) or from another action. The main difference is that Dagger CUE SDK mounts are transient (more on that below) and not bi-directional like "bind" mounts. So, even if you're mounting a `#FS` that is read from the host system (client API), any script interacting with the mounted folder (inside the container) writes to the container's filesystem layer only, and the writes do not impact the underlying client system at all.

![diagram-representing-dagger-mount](/img/core-concepts/fs/fs-mount.png)

Whether you mount a directory from your dev/CI host (using the client API), or between actions, the `mount` will only be modified in the context of an action execution.

However, if your script creates artifacts outside of the mounted filesystem, then it will be created inside the `rootfs` layer. That's a great way to make generated artifacts, files, directories sharable between actions.

![diagram-representing-dagger-mount-script-not-interacting-with-mounted-fs](/img/core-concepts/fs/fs-mount-not-interacting.png)

#### Mounts are not shared between actions (transient)

In the Dagger CUE SDK, as an image is only composed of a `rootfs` + a `config`, when passing the image to the next action, it loses all the mounted filesystems:

![diagram-representing-mount-loss](/img/core-concepts/fs/mount-loss-action.png)

#### Mounted FS cannot be exported (transient)

Exports only export from a `rootfs`. As mounts do not reside inside the `rootfs` layer, but on a layer above, the information residing inside a mounted filesystem gets lost, unless you mount it again inside the next action.

#### Mounts can overshadow filesystems

When mounting filesystems on top of a preexisting directory, they are temporarily overwritten/shadowed.

![diagram-representing-mount-loss](/img/core-concepts/fs/mount-overwrite.png)

In above example, the script only has access to the `/foo` and `/bar` directories that were mounted, as the artifacts present in the `rootfs` layer have been overshadowed in the superposition of all layers.

#### Example

Below is a plan showing how to mount an FS prior executing a command on top of it:

```cue file=../tests/core-concepts/fs/mount/mount_fs.cue title="dagger-cue do verify --log-format plain"

```

Visually, these are the underlying steps of above plan:

![diagram-representing-mount](/img/core-concepts/fs/explanation-mount-steps.png)

As mounts only live during the execution of the `verify` action, chaining outputs will not work.

:::note
Mounts are very useful to retrieve filesystems from several previous actions and use them together (to execute or produce something), as you can mount as many filesystems as you need.
:::

### Transfer of `#FS` via `docker.#Copy`

The aim of this action is to copy the content of an `#FS` to a `rootfs`. It relies on the `core.#Copy` action to perform this operation:

```cue
// Copy files from one FS tree to another
#Copy: {
    $dagger: task: _name: "Copy"
    // Input of the operation
    input: dagger.#FS
    // Contents to copy
    contents: dagger.#FS
    // Source path (optional)
    source: string | *"/"
    // Destination path (optional)
    dest: string | *"/"
    // Optionally include certain files
    include: [...string]
    // Optionally exclude certain files
    exclude: [...string]
    // Output of the operation
    output: dagger.#FS @dagger(generated)
}
```

Let's take the same plan as the one used to previously show how to `mount` a `dagger.#FS`. In this example, we will rely on the `docker.#Copy` instead of the mount, so the `#FS` is made part of the `rootfs` and can be shared with other actions:

```cue file=../tests/core-concepts/fs/copy/copy_fs.cue title="dagger-cue do verify --log-format plain"

```

Visually, these are the underlying steps of above plan:

![diagram-representing-copy](/img/core-concepts/fs/explanation-copy-steps.png)

The `verify` action does not have any mount, and instead has access to the `/target` artifact from the `exec` action due to the `docker.#Copy` (`_copy` action).

## Mounting host `#FS` to container (`#FS` perspective)

Filesystems are not just shared between actions, they can also be shared between the host (e.g. dev/CI machine) and the Dagger Engine:

![dagger client api](/img/core-concepts/fs/dagger-client-api-fs.png)

### Example plan to read an `#FS` from the host

Below is a plan showing how to list the content of the current directory from which the dagger plan is being run (relative to the dagger CLI).

:::note
This example uses the client API, but if you only need access to files within your project, `core.#Source` [may be a better choice](../guides/actions/246250-core-source.md).
:::

```cue file=../tests/core-concepts/fs/client/read_fs.cue title="dagger-cue do list --log-format plain"

```

A simplified visual representation of above plan would be:

![dagger action](/img/core-concepts/fs/read_fs.png)

### Example plan to write an `#FS` to the host

Let's now write to the host filesystem:

```cue file=../tests/core-concepts/fs/client/write_fs.cue title="dagger-cue do create --log-format plain"

```

A simplified visual representation of above plan would be:

![dagger action](/img/core-concepts/fs/write_fs.png)

[More information regarding the client API](./006395-client.md)
