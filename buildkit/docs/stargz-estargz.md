# Lazy pulling stargz/eStargz base images

This document describes the configuration that allows buildkit to lazily pull [stargz](https://github.com/google/crfs/blob/master/README.md#introducing-stargz)/[eStargz](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md)-formatted images from registries and how to obtain stargz/eStargz images.

By default, buildkit doesn't pull images until they are strictly needed.
For example, during a build, buildkit doesn't pull the base image until it runs commands on it (e.g. `RUN` Dockerfile instruction) or until it exports stages as tarballs, etc.

Additionally, if the image is formatted as stargz/eStargz, buildkit with the configuration described here can skip pull of that image even when it's needed (e.g. on `RUN` and `COPY` instructions).
Instead, it *mounts* that image from the registry to the node and *lazily* fetches necessary files (or chunks for big files) contained in that image on demand.
This can hopefully reduce the time to take for the build.
This document describes the configuration and usage of this feature.

For more details about stargz/eStargz image format, please see also [Stargz and eStargz image formats](#stargz-and-estargz-image-formats) section.

## Enabling lazy pulling of stargz/eStargz images

Buildkit supports two ways to enable lazy pulling of stargz/eStargz images.

### Using builtin support (recommended)

- Requirements
  - Rootless execution requires kernel >= 5.11 or Ubuntu kernel. BuildKit >= v0.11 is recommended.

OCI worker has builtin support for stargz/eStargz.
You can enable this feature by running `buildkitd` with an option `--oci-worker-snapshotter=stargz`.

```
buildkitd --oci-worker-snapshotter=stargz
```

This is the easiest way to use this lazy pulling feature on buildkit.

#### Builtin stargz snapshotter with rootless

To run it by non-root user, you can use [RootlessKit](https://github.com/rootless-containers/rootlesskit/).

```
rootlesskit buildkitd --oci-worker-snapshotter=stargz
```

```
buildctl --addr unix:///run/user/$UID/buildkit/buildkitd.sock build ...
```

> NOTE1: For details about rootless configuration, see [`/docs/rootless.md`](./rootless.md).

> NOTE2: If buildkitd can't create directory or socket under `/run`, check if `$XDG_RUNTIME_DIR` is set correctly (e.g. typically `/run/user/$UID`)

#### Example of building an image with lazy pulling

Once `buildkitd` starts with the above configuration, stargz/eStargz images can be lazily pulled.
For example, we build the following golang binary with buildkit.

```golang
package main

import "fmt"

func main() {
	fmt.Println("Hello, world!")
}
```

The example Dockerfile which leverages eStargz-formatted base image will be the following.

```Dockerfile
# Uses eStargz-formatted golang image as the base image. This isn't pulled here.
FROM ghcr.io/stargz-containers/golang:1.15.3-buster-esgz AS dev

# Copies the source code from the context. The base image is mounted and lazily pulled.
COPY ./hello.go /hello.go

# Runs go compiler on that base image. The base image is mounted and lazily pulled.
RUN go build -o /hello /hello.go

FROM scratch

# Harvesting the result binary.
COPY --from=dev /hello /
```

Then this can be built with skipping pull of `ghcr.io/stargz-containers/golang:1.15.3-buster-esgz`.
Instead, this image will be mounted from the registry to the node and necessary chunks of contents (e.g. `go` command binary, library, etc.) are partially fetched from that registry on demand.

```
$ buildctl build --frontend dockerfile.v0 \
                 --local context=/tmp/hello \
                 --local dockerfile=/tmp/hello \
                 --output type=local,dest=./
$ ./hello
Hello, world!
```

Note that when a stage is exported (e.g. to the registry), the base image (even stargz/eStargz) of that stage needs to be pulled to copy it to the destination.
However if the destination is a registry and the target repository already contains some blobs of that image or [cross repository blob mount](https://docs.docker.com/registry/spec/api/#cross-repository-blob-mount) can be used, buildkit keeps these blobs lazy.

### Using proxy (standalone) snapshotter

This is another way to enable stargz-based lazy pulling.
This configuration is for users of containerd worker.

- Requirements
  - [Stargz Snapshotter (`containerd-stargz-grpc`)](https://github.com/containerd/stargz-snapshotter) needs to be installed. stargz-snapshotter >= v0.13 is recommended.
  - Rootless execution requires kernel >= 5.11 or Ubuntu kernel. BuildKit >= v0.11 is recommended.

> NOTE: BuildKit's registry configuration isn't propagated to the proxy stargz snapshotter so it needs to be configured separately when you use private/mirror registries. If you use OCI worker + builtin stargz snapshotter, the separated configurations isn't needed.

#### Proxy snapshotter with containerd worker

Configure containerd's config.toml (default = `/etc/containerd/config.toml`) to make it recognize stargz snapshotter as a [proxy snapshotter](https://github.com/containerd/containerd/blob/master/PLUGINS.md#proxy-plugins).

```toml
[proxy_plugins]
  [proxy_plugins.stargz]
    type = "snapshot"
    address = "/run/containerd-stargz-grpc/containerd-stargz-grpc.sock"
```

Then spawn `containerd-stargz-grpc` and `containerd`, and run `buildkitd` with an option `--containerd-worker-snapshotter=stargz` which tells `containerd` to use stargz snapshotter.

```
containerd-stargz-grpc
containerd
buildkitd --containerd-worker-snapshotter=stargz --oci-worker=false --containerd-worker=true
```

#### Proxy snapshotter with rootless containerd worker

To run it by non-root user, you can use `containerd-rootless-setuptool.sh` included in [containerd/nerdctl](https://github.com/containerd/nerdctl).

`install` subcommand installs rootless containerd.

```
containerd-rootless-setuptool.sh install
```

`install-stargz` subcommand installs rootless stargz-snapshotter.

```
$ containerd-rootless-setuptool.sh install-stargz
$ cat <<'EOF' >> ~/.config/containerd/config.toml
[proxy_plugins]
  [proxy_plugins."stargz"]
      type = "snapshot"
      address = "/run/user/1000/containerd-stargz-grpc/containerd-stargz-grpc.sock"
EOF
$ systemctl --user restart containerd.service
```

> NOTE: replace "1000" with your actual UID

[`install-buildkit-containerd` subcommand](https://github.com/containerd/nerdctl/blob/v1.0.0/docs/build.md#setting-up-buildkit-with-containerd-worker) installs rootless buildkitd with containerd worker.
`CONTAINERD_SNAPSHOTTER=stargz` enables stargz-snapshotter.
`CONTAINERD_NAMESPACE` specifies containerd namespace used by BuildKit.

```
$ CONTAINERD_NAMESPACE=default CONTAINERD_SNAPSHOTTER=stargz containerd-rootless-setuptool.sh install-buildkit-containerd
```

> NOTE: `CONTAINERD_SNAPSHOTTER=stargz` doesn't work with nerdctl <= v1.0.0.

```
buildctl --addr unix:///run/user/1000/buildkit-default/buildkitd.sock build ...
```

#### Proxy snapshotter with OCI worker

Spawn `containerd-stargz-grpc` as a separated process.
Then specify stargz-snapshotter's socket path to `--oci-worker-proxy-snapshotter-path`.

```
containerd-stargz-grpc
buildkitd --oci-worker-snapshotter=stargz \
          --oci-worker-proxy-snapshotter-path=/run/containerd-stargz-grpc/containerd-stargz-grpc.sock
```

#### Proxy snapshotter with rootless OCI worker

Run stargz-snapshotter with rootlesskit.

```
rootlesskit --state-dir=/run/user/$UID/rootlesskit-buildkit \
            containerd-stargz-grpc --root=$HOME/.local/share/containerd-stargz-grpc \
                                   --address=/run/user/$UID/containerd-stargz-grpc/containerd-stargz-grpc.sock &
```

RootlessKit writes the PID to a file named `child_pid` under `--state-dir` directory.

Join buildkitd to the same rootlesskit namespace.
Specify stargz-snapshotter's socket path to `--oci-worker-proxy-snapshotter-path`.

```
nsenter -U --preserve-credentials -m -t $(cat /run/user/$UID/rootlesskit-buildkit/child_pid) \
        buildkitd --oci-worker-snapshotter=stargz \
                  --oci-worker-proxy-snapshotter-path=/run/user/$UID/containerd-stargz-grpc/containerd-stargz-grpc.sock &
```

> NOTE: If buildkitd can't create directory or socket under `/run`, check if `$XDG_RUNTIME_DIR` is set correctly (e.g. typically `/run/user/$UID`)

```
buildctl --addr unix:///run/user/$UID/buildkit/buildkitd.sock build ...
```

#### Registry-related configurations for proxy (standalone) stargz snapshotter

> NOTE: You don't need this configuration if you use OCI worker + builtin stargz snapshotter

When you use standalone stargz snapshotter, registry configuration needs to be done for the stargz snapshotter process, separately.
Create a configuration toml file which contains the registry configuration for stargz snapshotter (e.g. `/etc/containerd-stargz-grpc/config.toml`).
The configuration format [differs from buildkit](https://github.com/containerd/stargz-snapshotter/blob/master/cmd/containerd-stargz-grpc/config.go).
For more information about this format, please see also [docs in the repository](https://github.com/containerd/stargz-snapshotter/blob/master/docs/overview.md#registry-related-configuration).

```toml
[[resolver.host."exampleregistry.io".mirrors]]
host = "examplemirror.io"
```

Then pass this file to stargz snapshotter through `--config` option.

```
containerd-stargz-grpc --config=/etc/containerd-stargz-grpc/config.toml
```

## Stargz and eStargz image formats

[Stargz](https://github.com/google/crfs/blob/master/README.md#introducing-stargz) and [eStargz](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md) are OCI/Docker-compatible image formats that can be lazily pulled from standard registries (e.g. Docker Hub, GitHub Container Registry, etc).
Because they are backwards-compatible to OCI/Docker images, they can run on standard runtimes (e.g. Docker, containerd, etc.).
Stargz is proposed by [Google CRFS project](https://github.com/google/crfs).
eStargz is an extended format of stargz by [Stargz Snapshotter](https://github.com/containerd/stargz-snapshotter).
It comes with [additional features](https://github.com/containerd/stargz-snapshotter/blob/main/docs/estargz.md) including chunk verification and prefetch for avoiding the overhead of on-demand fetching.
For more details about lazy pulling with stargz/eStargz images, please refer to the docs on these repositories.

## Creating stargz/eStargz images

### Building eStargz image with BuildKit

BuildKit supports creating eStargz as one of the compression types.

:information_source: Creating eStargz image does NOT require [stargz-snapshotter setup](#enabling-lazy-pulling-of-stargzeStargz-images).

As shown in the following, `compression=estargz` creates an eStargz-formatted image.
Specifying `oci-mediatypes=true` option is highly recommended for enabling [layer verification](https://github.com/containerd/stargz-snapshotter/blob/v0.13.0/docs/estargz.md#content-verification-in-estargz) of eStargz.

```
buildctl build ... \
  --output type=image,name=docker.io/username/image,push=true,compression=estargz,oci-mediatypes=true
```

:information_source: Building eStargz is only supported by OCI worker.

:information_source: `compression` option isn't applied to layers that already exist in the cache (including the base images). Thus if you create eStargz image using non-eStargz base images, you need to specify `force-compression=true` option as well for applying the `compression` config to all existing layers.

:information_source: BuildKit doesn't support [prefetch-based optimization of eStargz](https://github.com/containerd/stargz-snapshotter/blob/v0.6.4/docs/stargz-estargz.md#example-use-case-of-prioritized-files-workload-based-image-optimization-in-stargz-snapshotter). To enable full feature of eStargz, you can also use other tools as described in the next section.

### Other methods to obtain stargz/eStargz images

Pre-converted stargz/eStargz images are available at [`ghcr.io/stargz-containers` repository](https://github.com/containerd/stargz-snapshotter/blob/main/docs/pre-converted-images.md) (mainly for testing purpose).

You can also create any stargz/eStargz image using the variety of tools including the following.

- [Docker Buildx](https://github.com/containerd/stargz-snapshotter/tree/v0.13.0#building-estargz-images-using-buildkit): Docker CLI plugin for BuildKit.
- [Kaniko](https://github.com/containerd/stargz-snapshotter/tree/v0.13.0#building-estargz-images-using-kaniko): An image builder runnable in containers and Kubernetes.
- [nerdctl](https://github.com/containerd/nerdctl/blob/v1.0.0/docs/stargz.md#building-stargz-images-using-nerdctl-build): Docker-compatible CLI for containerd and BuildKit. This supports `convert` subcommand to convert an OCI/Docker image into eStargz.
- [`ctr-remote`](https://github.com/containerd/stargz-snapshotter/blob/v0.13.0/docs/ctr-remote.md): containerd CLI developed in stargz snapshotter project. This supports converting an OCI/Docker image into eStargz and [optimizing](https://github.com/containerd/stargz-snapshotter/blob/v0.13.0/docs/estargz.md#example-use-case-of-prioritized-files-workload-based-image-optimization-in-stargz-snapshotter) it.
- [`stargzify`](https://github.com/google/crfs/tree/master/stargz/stargzify): CLI tool to convert an OCI/Docker image to stargz. This is developed in CRFS project. Creating eStargz is unsupported.

There are also other tools including Kaniko, ko, builpacks.io that support eStargz creation.
For more details, please refer to [`Creating eStargz images with tools in the community` section in the introductory post](https://medium.com/nttlabs/lazy-pulling-estargz-ef35812d73de).
