# Dockerfile conversion to LLB

If you want to understand how Buildkit translates Dockerfile instructions into
LLB, or you want to write your own frontend, then seeing how Dockerfile maps to
using the Buildkit LLB package will give you a jump start.

The `llb` package from Buildkit provides a chainable state object to help
construct a LLB. Then you can marshal the state object into a definition using
protocol buffers, and send it off in a solve request over gRPC.

In code, these transformations are performed by the [`Dockerfile2LLB()`](../../frontend/dockerfile/dockerfile2llb/convert.go)
function, which takes a raw `Dockerfile`'s contents and converts it to an LLB
state, and associated image config, which are then both assembled in the
[`Build()`](../../frontend/dockerfile/builder/build.go) function.

## Basic examples

Here are a few Dockerfile instructions you should be familiar with:

- Base image

  ```dockerfile
  FROM golang:1.12
  ```

  ```golang
  st := llb.Image("golang:1.12")
  ```

- Scratch image

  ```dockerfile
  FROM scratch
  ```

  ```golang
  st := llb.Scratch()
  ```

- Environment variables

  ```dockerfile
  ENV DEBIAN_FRONTEND=noninteractive
  ```

  ```golang
  st = st.AddEnv("DEBIAN_FRONTEND", "noninteractive")
  ```

- Running programs

  ```dockerfile
  RUN echo hello
  ```

  ```golang
  st = st.Run(
    llb.Shlex("echo hello"),
  ).Root()
  ```

- Working directory

  ```dockerfile
  WORKDIR /path
  ```

  ```golang
  st = st.Dir("/path")
  ```

## File operations

This is where LLB starts to deviate from Dockerfile in features. In
Dockerfiles, the run command is completely opaque to the builder and just
executes the command. But in LLB, there are file operations that have better
caching semantics and understanding of the command:

- Copying files

  ```dockerfile
  COPY --from=builder /files/* /files
  ```

  ```golang
  var CopyOptions = &llb.CopyInfo{
    FollowSymlinks:      true,
    CopyDirContentsOnly: true,
    AttemptUnpack:       false,
    CreateDestPath:      true,
    AllowWildcard:       true,
    AllowEmptyWildcard:  true,
  }
  st = st.File(
    llb.Copy(builder, "/files/*", "/files", CopyOptions),
  )
  ```

- Adding files

  ```dockerfile
  ADD --from=builder /files.tgz /files
  ```

  ```golang
  var AddOptions = &llb.CopyInfo{
    FollowSymlinks:      true,
    CopyDirContentsOnly: true,
    AttemptUnpack:       true,
    CreateDestPath:      true,
    AllowWildcard:       true,
    AllowEmptyWildcard:  true,
  }
  st = st.File(
    llb.Copy(builder, "/files.tgz", "files", AddOptions),
  )
  ```

- Chaining file commands

  ```dockerfile
  # not possible without RUN in Dockerfile
  RUN mkdir -p /some && echo hello > /some/file
  ```

  ```golang
  st = st.File(
    llb.Mkdir("/some", 0755),
  ).File(
    llb.Mkfile("/some/file", 0644, "hello"),
  )
  ```

## Bind mounts

Bind mounts allow unidirectional syncing of the host's local file system into
the build environment.

Bind mounts in Buildkit should not be confused with bind mounts in the linux
kernel - they do not sync bidirectionally. Bind mounts are only a snapshot of
your local state, which is specified through the `llb.Local` state object:

- Using bind mounts

  ```dockerfile
  WORKDIR /builder
  RUN --mount=type=bind,target=/builder \
  PIP_INDEX_URL=https://my-proxy.com/pypi \
      pip install .
  ```

  ```golang
  localState := llb.Local(
    "context",
    llb.SessionID(client.BuildOpts().SessionID),
    llb.WithCustomName("loading .")
    llb.FollowPaths([]string{"."}),
  )

  execState = st.Dir("/builder").Run(
    llb.Shlex("pip install ."),
    llb.AddEnv(
      "PIP_INDEX_URL",
      "https://my-proxy.com/pypi",
    ),
  )
  _ := execState.AddMount("/builder", localState)
  // the return value of AddMount captures the resulting state of the mount
  // after the exec operation has completed

  st := execState.Root()
  ```

## Cache mounts

Cache mounts allow for a shared file cache location between build invocations,
which allow manually caching expensive operations, such as package downloads.
Mounts have options to persist between builds with different sharing modes.

- Using cache mounts

  ```dockerfile
  RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
      --mount=type=cache,target=/var/lib/apt \
      apt-get update
  ```

  ```golang
  var VarCacheAptMount = llb.AddMount(
    "/var/cache/apt",
    llb.Scratch(),
    llb.AsPersistentCacheDir(
      "some-cache-id",
      llb.CacheMountLocked,
    ),
  )

  var VarLibAptMount = llb.AddMount(
    "/var/lib/apt",
    llb.Scratch(),
    llb.AsPersistentCacheDir(
      "another-cache-id",
      llb.CacheMountShared,
    ),
  )

  st := st.Run(
    llb.Shlex("apt-get update"),
    VarCacheAptMount,
    VarLibAptMount,
  ).Root()
  ```
