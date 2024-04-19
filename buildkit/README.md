[![asciicinema example](https://asciinema.org/a/gPEIEo1NzmDTUu2bEPsUboqmU.png)](https://asciinema.org/a/gPEIEo1NzmDTUu2bEPsUboqmU)

# BuildKit <!-- omit in toc -->

[![GitHub Release](https://img.shields.io/github/release/moby/buildkit.svg?style=flat-square)](https://github.com/moby/buildkit/releases/latest)
[![PkgGoDev](https://img.shields.io/badge/go.dev-docs-007d9c?style=flat-square&logo=go&logoColor=white)](https://pkg.go.dev/github.com/moby/buildkit/client/llb)
[![CI BuildKit Status](https://img.shields.io/github/actions/workflow/status/moby/buildkit/buildkit.yml?label=buildkit&logo=github&style=flat-square)](https://github.com/moby/buildkit/actions?query=workflow%3Abuildkit)
[![CI Frontend Status](https://img.shields.io/github/actions/workflow/status/moby/buildkit/frontend.yml?label=frontend&logo=github&style=flat-square)](https://github.com/moby/buildkit/actions?query=workflow%3Afrontend)
[![Go Report Card](https://goreportcard.com/badge/github.com/moby/buildkit?style=flat-square)](https://goreportcard.com/report/github.com/moby/buildkit)
[![Codecov](https://img.shields.io/codecov/c/github/moby/buildkit?logo=codecov&style=flat-square)](https://codecov.io/gh/moby/buildkit)

BuildKit is a toolkit for converting source code to build artifacts in an efficient, expressive and repeatable manner.

Key features:

-   Automatic garbage collection
-   Extendable frontend formats
-   Concurrent dependency resolution
-   Efficient instruction caching
-   Build cache import/export
-   Nested build job invocations
-   Distributable workers
-   Multiple output formats
-   Pluggable architecture
-   Execution without root privileges

Read the proposal from https://github.com/moby/moby/issues/32925

Introductory blog post https://blog.mobyproject.org/introducing-buildkit-17e056cc5317

Join `#buildkit` channel on [Docker Community Slack](https://dockr.ly/comm-slack)

> **Note**
>
> If you are visiting this repo for the usage of BuildKit-only Dockerfile features
> like `RUN --mount=type=(bind|cache|tmpfs|secret|ssh)`, please refer to the
> [Dockerfile reference](https://docs.docker.com/engine/reference/builder/).

> **Note**
>
> `docker build` [uses Buildx and BuildKit by default](https://docs.docker.com/build/architecture/) since Docker Engine 23.0.
> You don't need to read this document unless you want to use the full-featured
> standalone version of BuildKit.

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Used by](#used-by)
- [Quick start](#quick-start)
  - [Linux Setup](#linux-setup)
  - [Windows Setup](#windows-setup)
  - [macOS Setup](#macos-setup)
  - [Build from source](#build-from-source)
  - [Exploring LLB](#exploring-llb)
  - [Exploring Dockerfiles](#exploring-dockerfiles)
    - [Building a Dockerfile with `buildctl`](#building-a-dockerfile-with-buildctl)
    - [Building a Dockerfile using external frontend](#building-a-dockerfile-using-external-frontend)
  - [Output](#output)
    - [Image/Registry](#imageregistry)
    - [Local directory](#local-directory)
    - [Docker tarball](#docker-tarball)
    - [OCI tarball](#oci-tarball)
    - [containerd image store](#containerd-image-store)
- [Cache](#cache)
  - [Garbage collection](#garbage-collection)
  - [Export cache](#export-cache)
    - [Inline (push image and cache together)](#inline-push-image-and-cache-together)
    - [Registry (push image and cache separately)](#registry-push-image-and-cache-separately)
    - [Local directory](#local-directory-1)
    - [GitHub Actions cache (experimental)](#github-actions-cache-experimental)
    - [S3 cache (experimental)](#s3-cache-experimental)
    - [Azure Blob Storage cache (experimental)](#azure-blob-storage-cache-experimental)
  - [Consistent hashing](#consistent-hashing)
- [Metadata](#metadata)
- [Systemd socket activation](#systemd-socket-activation)
- [Expose BuildKit as a TCP service](#expose-buildkit-as-a-tcp-service)
  - [Load balancing](#load-balancing)
- [Containerizing BuildKit](#containerizing-buildkit)
  - [Podman](#podman)
  - [Nerdctl](#nerdctl)
  - [Kubernetes](#kubernetes)
  - [Daemonless](#daemonless)
- [OpenTelemetry support](#opentelemetry-support)
- [Running BuildKit without root privileges](#running-buildkit-without-root-privileges)
- [Building multi-platform images](#building-multi-platform-images)
  - [Configuring `buildctl`](#configuring-buildctl)
    - [Color Output Controls](#color-output-controls)
    - [Number of log lines (for active steps in tty mode)](#number-of-log-lines-for-active-steps-in-tty-mode)
- [Contributing](#contributing)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

## Used by

BuildKit is used by the following projects:

-   [Moby & Docker](https://github.com/moby/moby/pull/37151) (`DOCKER_BUILDKIT=1 docker build`)
-   [img](https://github.com/genuinetools/img)
-   [OpenFaaS Cloud](https://github.com/openfaas/openfaas-cloud)
-   [container build interface](https://github.com/containerbuilding/cbi)
-   [Tekton Pipelines](https://github.com/tektoncd/catalog) (formerly [Knative Build Templates](https://github.com/knative/build-templates))
-   [the Sanic build tool](https://github.com/distributed-containers-inc/sanic)
-   [vab](https://github.com/stellarproject/vab)
-   [Rio](https://github.com/rancher/rio)
-   [kim](https://github.com/rancher/kim)
-   [PouchContainer](https://github.com/alibaba/pouch)
-   [Docker buildx](https://github.com/docker/buildx)
-   [Okteto Cloud](https://okteto.com/)
-   [Earthly earthfiles](https://github.com/vladaionescu/earthly)
-   [Gitpod](https://github.com/gitpod-io/gitpod)
-   [Dagger](https://dagger.io)
-   [envd](https://github.com/tensorchord/envd/)
-   [Depot](https://depot.dev)
-   [Namespace](https://namespace.so)
-   [Unikraft](https://unikraft.org)

## Quick start

:information_source: For Kubernetes deployments, see [`examples/kubernetes`](./examples/kubernetes).

BuildKit is composed of the `buildkitd` daemon and the `buildctl` client.
While the `buildctl` client is available for Linux, macOS, and Windows, the `buildkitd` daemon is only available for Linux and *Windows currently.

The latest binaries of BuildKit are available [here](https://github.com/moby/buildkit/releases) for Linux, macOS, and Windows.


### Linux Setup

The `buildkitd` daemon requires the following components to be installed:
-   [runc](https://github.com/opencontainers/runc) or [crun](https://github.com/containers/crun)
-   [containerd](https://github.com/containerd/containerd) (if you want to use containerd worker)

**Starting the `buildkitd` daemon:**
You need to run `buildkitd` as the root user on the host.

```bash
$ sudo buildkitd
```

To run `buildkitd` as a non-root user, see [`docs/rootless.md`](docs/rootless.md).

The buildkitd daemon supports two worker backends: OCI (runc) and containerd.

By default, the OCI (runc) worker is used. You can set `--oci-worker=false --containerd-worker=true` to use the containerd worker.

We are open to adding more backends.

To start the buildkitd daemon using systemd socket activation, you can install the buildkit systemd unit files.
See [Systemd socket activation](#systemd-socket-activation)

The buildkitd daemon listens gRPC API on `/run/buildkit/buildkitd.sock` by default, but you can also use TCP sockets.
See [Expose BuildKit as a TCP service](#expose-buildkit-as-a-tcp-service).

### Windows Setup

See instructions and notes at [`docs/windows.md`](./docs/windows.md).

### macOS Setup

[Homebrew formula](https://formulae.brew.sh/formula/buildkit) (unofficial) is available for macOS.
```console
$ brew install buildkit
```

The Homebrew formula does not contain the daemon (`buildkitd`).

For example, [Lima](https://lima-vm.io) can be used for launching the daemon inside a Linux VM.
```console
brew install lima
limactl start template://buildkit
export BUILDKIT_HOST="unix://$HOME/.lima/buildkit/sock/buildkitd.sock"
```

### Build from source

To build BuildKit from source, see [`.github/CONTRIBUTING.md`](./.github/CONTRIBUTING.md).

For a `buildctl` reference, see [this document](./docs/reference/buildctl.md).

### Exploring LLB

BuildKit builds are based on a binary intermediate format called LLB that is used for defining the dependency graph for processes running part of your build. tl;dr: LLB is to Dockerfile what LLVM IR is to C.

-   Marshaled as Protobuf messages
-   Concurrently executable
-   Efficiently cacheable
-   Vendor-neutral (i.e. non-Dockerfile languages can be easily implemented)

See [`solver/pb/ops.proto`](./solver/pb/ops.proto) for the format definition, and see [`./examples/README.md`](./examples/README.md) for example LLB applications.

Currently, the following high-level languages have been implemented for LLB:

-   Dockerfile (See [Exploring Dockerfiles](#exploring-dockerfiles))
-   [Buildpacks](https://github.com/tonistiigi/buildkit-pack)
-   [Mockerfile](https://matt-rickard.com/building-a-new-dockerfile-frontend/)
-   [Gockerfile](https://github.com/po3rin/gockerfile)
-   [bldr (Pkgfile)](https://github.com/talos-systems/bldr/)
-   [HLB](https://github.com/openllb/hlb)
-   [Earthfile (Earthly)](https://github.com/earthly/earthly)
-   [Cargo Wharf (Rust)](https://github.com/denzp/cargo-wharf)
-   [Nix](https://github.com/reproducible-containers/buildkit-nix)
-   [mopy (Python)](https://github.com/cmdjulian/mopy)
-   [envd (starlark)](https://github.com/tensorchord/envd/)
-   [Blubber](https://gitlab.wikimedia.org/repos/releng/blubber)
-   [Bass](https://github.com/vito/bass)
-   [kraft.yaml (Unikraft)](https://github.com/unikraft/kraftkit/tree/staging/tools/dockerfile-llb-frontend)
-   (open a PR to add your own language)

### Exploring Dockerfiles

Frontends are components that run inside BuildKit and convert any build definition to LLB. There is a special frontend called gateway (`gateway.v0`) that allows using any image as a frontend.

During development, Dockerfile frontend (`dockerfile.v0`) is also part of the BuildKit repo. In the future, this will be moved out, and Dockerfiles can be built using an external image.

#### Building a Dockerfile with `buildctl`

```bash
buildctl build \
    --frontend=dockerfile.v0 \
    --local context=. \
    --local dockerfile=.
# or
buildctl build \
    --frontend=dockerfile.v0 \
    --local context=. \
    --local dockerfile=. \
    --opt target=foo \
    --opt build-arg:foo=bar
```

`--local` exposes local source files from client to the builder. `context` and `dockerfile` are the names Dockerfile frontend looks for build context and Dockerfile location.

If the Dockerfile has a different filename it can be specified with `--opt filename=./Dockerfile-alternative`.

#### Building a Dockerfile using external frontend

External versions of the Dockerfile frontend are pushed to https://hub.docker.com/r/docker/dockerfile-upstream and https://hub.docker.com/r/docker/dockerfile and can be used with the gateway frontend. The source for the external frontend is currently located in `./frontend/dockerfile/cmd/dockerfile-frontend` but will move out of this repository in the future ([#163](https://github.com/moby/buildkit/issues/163)). For automatic build from master branch of this repository `docker/dockerfile-upstream:master` or `docker/dockerfile-upstream:master-labs` image can be used.

```bash
buildctl build \
    --frontend gateway.v0 \
    --opt source=docker/dockerfile \
    --local context=. \
    --local dockerfile=.
buildctl build \
    --frontend gateway.v0 \
    --opt source=docker/dockerfile \
    --opt context=https://github.com/moby/moby.git \
    --opt build-arg:APT_MIRROR=cdn-fastly.deb.debian.org
```

### Output

By default, the build result and intermediate cache will only remain internally in BuildKit. An output needs to be specified to retrieve the result.

#### Image/Registry

```bash
buildctl build ... --output type=image,name=docker.io/username/image,push=true
```

To export the image to multiple registries:

```bash
buildctl build ... --output type=image,\"name=docker.io/username/image,docker.io/username2/image2\",push=true
```

To export the cache embed with the image and pushing them to registry together, type `registry` is required to import the cache, you should specify `--export-cache type=inline` and `--import-cache type=registry,ref=...`. To export the cache to a local directly, you should specify `--export-cache type=local`.
Details in [Export cache](#export-cache).

```bash
buildctl build ...\
  --output type=image,name=docker.io/username/image,push=true \
  --export-cache type=inline \
  --import-cache type=registry,ref=docker.io/username/image
```

Keys supported by image output:
* `name=<value>`: specify image name(s)
* `push=true`: push after creating the image
* `push-by-digest=true`: push unnamed image
* `registry.insecure=true`: push to insecure HTTP registry
* `oci-mediatypes=true`: use OCI mediatypes in configuration JSON instead of Docker's
* `unpack=true`: unpack image after creation (for use with containerd)
* `dangling-name-prefix=<value>`: name image with `prefix@<digest>`, used for anonymous images
* `name-canonical=true`: add additional canonical name `name@<digest>`
* `compression=<uncompressed|gzip|estargz|zstd>`: choose compression type for layers newly created and cached, gzip is default value. estargz should be used with `oci-mediatypes=true`.
* `compression-level=<value>`: compression level for gzip, estargz (0-9) and zstd (0-22)
* `rewrite-timestamp=true`: rewrite the file timestamps to the `SOURCE_DATE_EPOCH` value.
   See [`docs/build-repro.md`](docs/build-repro.md) for how to specify the `SOURCE_DATE_EPOCH` value.
* `force-compression=true`: forcefully apply `compression` option to all layers (including already existing layers)
* `store=true`: store the result images to the worker's (e.g. containerd) image store as well as ensures that the image has all blobs in the content store (default `true`). Ignored if the worker doesn't have image store (e.g. OCI worker).
* `annotation.<key>=<value>`: attach an annotation with the respective `key` and `value` to the built image
  * Using the extended syntaxes, `annotation-<type>.<key>=<value>`, `annotation[<platform>].<key>=<value>` and both combined with `annotation-<type>[<platform>].<key>=<value>`, allows configuring exactly where to attach the annotation.
  * `<type>` specifies what object to attach to, and can be any of `manifest` (the default), `manifest-descriptor`, `index` and `index-descriptor`
  * `<platform>` specifies which objects to attach to (by default, all), and is the same key passed into the `platform` opt, see [`docs/multi-platform.md`](docs/multi-platform.md).
  * See [`docs/annotations.md`](docs/annotations.md) for more details.

If credentials are required, `buildctl` will attempt to read Docker configuration file `$DOCKER_CONFIG/config.json`.
`$DOCKER_CONFIG` defaults to `~/.docker`.

#### Local directory

The local client will copy the files directly to the client. This is useful if BuildKit is being used for building something else than container images.

```bash
buildctl build ... --output type=local,dest=path/to/output-dir
```

To export specific files use multi-stage builds with a scratch stage and copy the needed files into that stage with `COPY --from`.

```dockerfile
...
FROM scratch as testresult

COPY --from=builder /usr/src/app/testresult.xml .
...
```

```bash
buildctl build ... --opt target=testresult --output type=local,dest=path/to/output-dir
```

With a [multi-platform build](docs/multi-platform.md), a subfolder matching
each target platform will be created in the destination directory:

```dockerfile
FROM busybox AS build
ARG TARGETOS
ARG TARGETARCH
RUN mkdir /out && echo foo > /out/hello-$TARGETOS-$TARGETARCH

FROM scratch
COPY --from=build /out /
```

```bash
$ buildctl build \
  --frontend dockerfile.v0 \
  --opt platform=linux/amd64,linux/arm64 \
  --output type=local,dest=./bin/release

$ tree ./bin
./bin/
└── release
    ├── linux_amd64
    │   └── hello-linux-amd64
    └── linux_arm64
        └── hello-linux-arm64
```

You can set `platform-split=false` to merge files from all platforms together
into same directory:

```bash
$ buildctl build \
  --frontend dockerfile.v0 \
  --opt platform=linux/amd64,linux/arm64 \
  --output type=local,dest=./bin/release,platform-split=false

$ tree ./bin
./bin/
└── release
    ├── hello-linux-amd64
    └── hello-linux-arm64
```

Tar exporter is similar to local exporter but transfers the files through a tarball.

```bash
buildctl build ... --output type=tar,dest=out.tar
buildctl build ... --output type=tar > out.tar
```

#### Docker tarball

```bash
# exported tarball is also compatible with OCI spec
buildctl build ... --output type=docker,name=myimage | docker load
```

#### OCI tarball

```bash
buildctl build ... --output type=oci,dest=path/to/output.tar
buildctl build ... --output type=oci > output.tar
```

#### containerd image store

The containerd worker needs to be used

```bash
buildctl build ... --output type=image,name=docker.io/username/image
ctr --namespace=buildkit images ls
```

To change the containerd namespace, you need to change `worker.containerd.namespace` in [`/etc/buildkit/buildkitd.toml`](./docs/buildkitd.toml.md).

## Cache

To show local build cache (`/var/lib/buildkit`):

```bash
buildctl du -v
```

To prune local build cache:
```bash
buildctl prune
```

### Garbage collection

See [`./docs/buildkitd.toml.md`](./docs/buildkitd.toml.md).

### Export cache

BuildKit supports the following cache exporters:
* `inline`: embed the cache into the image, and push them to the registry together
* `registry`: push the image and the cache separately
* `local`: export to a local directory
* `gha`: export to GitHub Actions cache

In most case you want to use the `inline` cache exporter.
However, note that the `inline` cache exporter only supports `min` cache mode. 
To enable `max` cache mode, push the image and the cache separately by using `registry` cache exporter.

`inline` and `registry` exporters both store the cache in the registry. For importing the cache, `type=registry` is sufficient for both, as specifying the cache format is not necessary.

#### Inline (push image and cache together)

```bash
buildctl build ... \
  --output type=image,name=docker.io/username/image,push=true \
  --export-cache type=inline \
  --import-cache type=registry,ref=docker.io/username/image
```

Note that the inline cache is not imported unless [`--import-cache type=registry,ref=...`](#registry-push-image-and-cache-separately) is provided.

Inline cache embeds cache metadata into the image config. The layers in the image will be left untouched compared to the image with no cache information.

:information_source: Docker-integrated BuildKit (`DOCKER_BUILDKIT=1 docker build`) and `docker buildx`requires 
`--build-arg BUILDKIT_INLINE_CACHE=1` to be specified to enable the `inline` cache exporter.
However, the standalone `buildctl` does NOT require `--opt build-arg:BUILDKIT_INLINE_CACHE=1` and the build-arg is simply ignored.

#### Registry (push image and cache separately)

```bash
buildctl build ... \
  --output type=image,name=localhost:5000/myrepo:image,push=true \
  --export-cache type=registry,ref=localhost:5000/myrepo:buildcache \
  --import-cache type=registry,ref=localhost:5000/myrepo:buildcache
```

`--export-cache` options:
* `type=registry`
* `mode=<min|max>`: specify cache layers to export (default: `min`)
  * `min`: only export layers for the resulting image
  * `max`: export all the layers of all intermediate steps
* `ref=<ref>`: specify repository reference to store cache, e.g. `docker.io/user/image:tag`
* `image-manifest=<true|false>`: whether to export cache manifest as an OCI-compatible image manifest rather than a manifest list/index (default: `false`, must be used with `oci-mediatypes=true`)
* `oci-mediatypes=<true|false>`: whether to use OCI mediatypes in exported manifests (default: `true`, since BuildKit `v0.8`)
* `compression=<uncompressed|gzip|estargz|zstd>`: choose compression type for layers newly created and cached, gzip is default value. estargz and zstd should be used with `oci-mediatypes=true`
* `compression-level=<value>`: choose compression level for gzip, estargz (0-9) and zstd (0-22)
* `force-compression=true`: forcibly apply `compression` option to all layers
* `ignore-error=<false|true>`: specify if error is ignored in case cache export fails (default: `false`)

`--import-cache` options:
* `type=registry`
* `ref=<ref>`: specify repository reference to retrieve cache from, e.g. `docker.io/user/image:tag`

#### Local directory

```bash
buildctl build ... --export-cache type=local,dest=path/to/output-dir
buildctl build ... --import-cache type=local,src=path/to/input-dir
```

The directory layout conforms to OCI Image Spec v1.0.

`--export-cache` options:
* `type=local`
* `mode=<min|max>`: specify cache layers to export (default: `min`)
  * `min`: only export layers for the resulting image
  * `max`: export all the layers of all intermediate steps
* `dest=<path>`: destination directory for cache exporter
* `tag=<tag>`: specify custom tag of image to write to local index (default: `latest`)
* `image-manifest=<true|false>`: whether to export cache manifest as an OCI-compatible image manifest rather than a manifest list/index (default: `false`, must be used with `oci-mediatypes=true`)
* `oci-mediatypes=<true|false>`: whether to use OCI mediatypes in exported manifests (default `true`, since BuildKit `v0.8`)
* `compression=<uncompressed|gzip|estargz|zstd>`: choose compression type for layers newly created and cached, gzip is default value. estargz and zstd should be used with `oci-mediatypes=true`.
* `compression-level=<value>`: compression level for gzip, estargz (0-9) and zstd (0-22)
* `force-compression=true`: forcibly apply `compression` option to all layers
* `ignore-error=<false|true>`: specify if error is ignored in case cache export fails (default: `false`)

`--import-cache` options:
* `type=local`
* `src=<path>`: source directory for cache importer
* `tag=<tag>`: specify custom tag of image to read from local index (default: `latest`)
* `digest=sha256:<sha256digest>`: specify explicit digest of the manifest list to import

#### GitHub Actions cache (experimental)

```bash
buildctl build ... \
  --output type=image,name=docker.io/username/image,push=true \
  --export-cache type=gha \
  --import-cache type=gha
```

GitHub Actions cache saves both cache metadata and layers to GitHub's Cache service. This cache currently has a [size limit of 10GB](https://docs.github.com/en/actions/advanced-guides/caching-dependencies-to-speed-up-workflows#usage-limits-and-eviction-policy) that is shared across different caches in the repo. If you exceed this limit, GitHub will save your cache but will begin evicting caches until the total size is less than 10 GB. Recycling caches too often can result in slower runtimes overall.

Similarly to using [actions/cache](https://github.com/actions/cache), caches are [scoped by branch](https://docs.github.com/en/actions/advanced-guides/caching-dependencies-to-speed-up-workflows#restrictions-for-accessing-a-cache), with the default and target branches being available to every branch.

Following attributes are required to authenticate against the [GitHub Actions Cache service API](https://github.com/tonistiigi/go-actions-cache/blob/master/api.md#authentication):
* `url`: Cache server URL (default `$ACTIONS_CACHE_URL`)
* `token`: Access token (default `$ACTIONS_RUNTIME_TOKEN`)

:information_source: This type of cache can be used with [Docker Build Push Action](https://github.com/docker/build-push-action)
where `url` and `token` will be automatically set. To use this backend in an inline `run` step, you have to include [crazy-max/ghaction-github-runtime](https://github.com/crazy-max/ghaction-github-runtime)
in your workflow to expose the runtime.

`--export-cache` options:
* `type=gha`
* `mode=<min|max>`: specify cache layers to export (default: `min`)
  * `min`: only export layers for the resulting image
  * `max`: export all the layers of all intermediate steps
* `scope=<scope>`: which scope cache object belongs to (default `buildkit`)
* `ignore-error=<false|true>`: specify if error is ignored in case cache export fails (default: `false`)
* `timeout=<duration>`: sets the timeout duration for cache export (default: `10m`)

`--import-cache` options:
* `type=gha`
* `scope=<scope>`: which scope cache object belongs to (default `buildkit`)
* `timeout=<duration>`: sets the timeout duration for cache import (default: `10m`)

#### S3 cache (experimental)

```bash
buildctl build ... \
  --output type=image,name=docker.io/username/image,push=true \
  --export-cache type=s3,region=eu-west-1,bucket=my_bucket,name=my_image \
  --import-cache type=s3,region=eu-west-1,bucket=my_bucket,name=my_image
```

The following attributes are required:
* `bucket`: AWS S3 bucket (default: `$AWS_BUCKET`)
* `region`: AWS region (default: `$AWS_REGION`)

Storage locations:
* blobs: `s3://<bucket>/<prefix><blobs_prefix>/<sha256>`, default: `s3://<bucket>/blobs/<sha256>`
* manifests: `s3://<bucket>/<prefix><manifests_prefix>/<name>`, default: `s3://<bucket>/manifests/<name>`

S3 configuration:
* `blobs_prefix`: global prefix to store / read blobs on s3 (default: `blobs/`)
* `manifests_prefix`: global prefix to store / read manifests on s3 (default: `manifests/`)
* `endpoint_url`: specify a specific S3 endpoint (default: empty)
* `use_path_style`: if set to `true`, put the bucket name in the URL instead of in the hostname (default: `false`)

AWS Authentication:

The simplest way is to use an IAM Instance profile.
Other options are:

* Any system using environment variables / config files supported by the [AWS Go SDK](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html). The configuration must be available for the buildkit daemon, not for the client.
* Using the following attributes:
  * `access_key_id`: Access Key ID
  * `secret_access_key`: Secret Access Key
  * `session_token`: Session Token

`--export-cache` options:
* `type=s3`
* `mode=<min|max>`: specify cache layers to export (default: `min`)
  * `min`: only export layers for the resulting image
  * `max`: export all the layers of all intermediate steps
* `prefix=<prefix>`: set global prefix to store / read files on s3 (default: empty)
* `name=<manifest>`: specify name of the manifest to use (default `buildkit`)
  * Multiple manifest names can be specified at the same time, separated by `;`. The standard use case is to use the git sha1 as name, and the branch name as duplicate, and load both with 2 `import-cache` commands.
* `ignore-error=<false|true>`: specify if error is ignored in case cache export fails (default: `false`)

`--import-cache` options:
* `type=s3`
* `prefix=<prefix>`: set global prefix to store / read files on s3 (default: empty)
* `blobs_prefix=<prefix>`: set global prefix to store / read blobs on s3 (default: `blobs/`)
* `manifests_prefix=<prefix>`: set global prefix to store / read manifests on s3 (default: `manifests/`)
* `name=<manifest>`: name of the manifest to use (default `buildkit`)

#### Azure Blob Storage cache (experimental)

```bash
buildctl build ... \
  --output type=image,name=docker.io/username/image,push=true \
  --export-cache type=azblob,account_url=https://myaccount.blob.core.windows.net,name=my_image \
  --import-cache type=azblob,account_url=https://myaccount.blob.core.windows.net,name=my_image
```

The following attributes are required:
* `account_url`: The Azure Blob Storage account URL (default: `$BUILDKIT_AZURE_STORAGE_ACCOUNT_URL`)

Storage locations:
* blobs: `<account_url>/<container>/<prefix><blobs_prefix>/<sha256>`, default: `<account_url>/<container>/blobs/<sha256>`
* manifests: `<account_url>/<container>/<prefix><manifests_prefix>/<name>`, default: `<account_url>/<container>/manifests/<name>`

Azure Blob Storage configuration:
* `container`: The Azure Blob Storage container name (default: `buildkit-cache` or `$BUILDKIT_AZURE_STORAGE_CONTAINER` if set)
* `blobs_prefix`: Global prefix to store / read blobs on the Azure Blob Storage container (`<container>`) (default: `blobs/`)
* `manifests_prefix`: Global prefix to store / read blobs on the Azure Blob Storage container (`<container>`) (default: `manifests/`)

Azure Blob Storage authentication:

There are 2 options supported for Azure Blob Storage authentication:

* Any system using environment variables supported by the [Azure SDK for Go](https://docs.microsoft.com/en-us/azure/developer/go/azure-sdk-authentication). The configuration must be available for the buildkit daemon, not for the client.
* Secret Access Key, using the `secret_access_key` attribute to specify the primary or secondary account key for your Azure Blob Storage account. [Azure Blob Storage account keys](https://docs.microsoft.com/en-us/azure/storage/common/storage-account-keys-manage)

> **Note**
>
> Account name can also be specified with `account_name` attribute (or `$BUILDKIT_AZURE_STORAGE_ACCOUNT_NAME`)
> if it is not part of the account URL host.

`--export-cache` options:
* `type=azblob`
* `mode=<min|max>`: specify cache layers to export (default: `min`)
  * `min`: only export layers for the resulting image
  * `max`: export all the layers of all intermediate steps
* `prefix=<prefix>`: set global prefix to store / read files on the Azure Blob Storage container (`<container>`) (default: empty)
* `name=<manifest>`: specify name of the manifest to use (default: `buildkit`)
  * Multiple manifest names can be specified at the same time, separated by `;`. The standard use case is to use the git sha1 as name, and the branch name as duplicate, and load both with 2 `import-cache` commands.
* `ignore-error=<false|true>`: specify if error is ignored in case cache export fails (default: `false`)

`--import-cache` options:
* `type=azblob`
* `prefix=<prefix>`: set global prefix to store / read files on the Azure Blob Storage container (`<container>`) (default: empty)
* `blobs_prefix=<prefix>`: set global prefix to store / read blobs on the Azure Blob Storage container (`<container>`) (default: `blobs/`)
* `manifests_prefix=<prefix>`: set global prefix to store / read manifests on the Azure Blob Storage container (`<container>`) (default: `manifests/`)
* `name=<manifest>`: name of the manifest to use (default: `buildkit`)

### Consistent hashing

If you have multiple BuildKit daemon instances, but you don't want to use registry for sharing cache across the cluster,
consider client-side load balancing using consistent hashing.

See [`./examples/kubernetes/consistenthash`](./examples/kubernetes/consistenthash).

## Metadata

To output build metadata such as the image digest, pass the `--metadata-file` flag.
The metadata will be written as a JSON object to the specified file.
The directory of the specified file must already exist and be writable.

```bash
buildctl build ... --metadata-file metadata.json
```

```shell
jq '.' metadata.json
```
```json
{
  "containerimage.config.digest": "sha256:2937f66a9722f7f4a2df583de2f8cb97fc9196059a410e7f00072fc918930e66",
  "containerimage.descriptor": {
    "annotations": {
      "config.digest": "sha256:2937f66a9722f7f4a2df583de2f8cb97fc9196059a410e7f00072fc918930e66",
      "org.opencontainers.image.created": "2022-02-08T21:28:03Z"
    },
    "digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3",
    "mediaType": "application/vnd.oci.image.manifest.v1+json",
    "size": 506
  },
  "containerimage.digest": "sha256:19ffeab6f8bc9293ac2c3fdf94ebe28396254c993aea0b5a542cfb02e0883fa3"
}
```

## Systemd socket activation

On Systemd based systems, you can communicate with the daemon via [Systemd socket activation](http://0pointer.de/blog/projects/socket-activation.html), use `buildkitd --addr fd://`.
You can find examples of using Systemd socket activation with BuildKit and Systemd in [`./examples/systemd`](./examples/systemd).
## Expose BuildKit as a TCP service

The `buildkitd` daemon can listen the gRPC API on a TCP socket.

It is highly recommended to create TLS certificates for both the daemon and the client (mTLS).
Enabling TCP without mTLS is dangerous because the executor containers (aka Dockerfile `RUN` containers) can call BuildKit API as well.

```bash
buildkitd \
  --addr tcp://0.0.0.0:1234 \
  --tlscacert /path/to/ca.pem \
  --tlscert /path/to/cert.pem \
  --tlskey /path/to/key.pem
```

```bash
buildctl \
  --addr tcp://example.com:1234 \
  --tlscacert /path/to/ca.pem \
  --tlscert /path/to/clientcert.pem \
  --tlskey /path/to/clientkey.pem \
  build ...
```

### Load balancing

`buildctl build` can be called against randomly load balanced the `buildkitd` daemon.

See also [Consistent hashing](#consistent-hashing) for client-side load balancing.

## Containerizing BuildKit

BuildKit can also be used by running the `buildkitd` daemon inside a Docker container and accessing it remotely.

We provide the container images as [`moby/buildkit`](https://hub.docker.com/r/moby/buildkit/tags/):

-   `moby/buildkit:latest`: built from the latest regular [release](https://github.com/moby/buildkit/releases)
-   `moby/buildkit:rootless`: same as `latest` but runs as an unprivileged user, see [`docs/rootless.md`](docs/rootless.md)
-   `moby/buildkit:master`: built from the master branch
-   `moby/buildkit:master-rootless`: same as master but runs as an unprivileged user, see [`docs/rootless.md`](docs/rootless.md)

To run daemon in a container:

```bash
docker run -d --name buildkitd --privileged moby/buildkit:latest
export BUILDKIT_HOST=docker-container://buildkitd
buildctl build --help
```

### Podman
To connect to a BuildKit daemon running in a Podman container, use `podman-container://` instead of `docker-container://` .

```bash
podman run -d --name buildkitd --privileged moby/buildkit:latest
buildctl --addr=podman-container://buildkitd build --frontend dockerfile.v0 --local context=. --local dockerfile=. --output type=oci | podman load foo
```

`sudo` is not required.

### Nerdctl
To connect to a BuildKit daemon running in a Nerdctl container, use `nerdctl-container://` instead of `docker-container://`.

```bash
nerdctl run -d --name buildkitd --privileged moby/buildkit:latest
buildctl --addr=nerdctl-container://buildkitd build --frontend dockerfile.v0 --local context=. --local dockerfile=. --output type=oci | nerdctl load
```

`sudo` is not required.

### Kubernetes

For Kubernetes deployments, see [`examples/kubernetes`](./examples/kubernetes).

### Daemonless

To run the client and an ephemeral daemon in a single container ("daemonless mode"):

```bash
docker run \
    -it \
    --rm \
    --privileged \
    -v /path/to/dir:/tmp/work \
    --entrypoint buildctl-daemonless.sh \
    moby/buildkit:master \
        build \
        --frontend dockerfile.v0 \
        --local context=/tmp/work \
        --local dockerfile=/tmp/work
```

or

```bash
docker run \
    -it \
    --rm \
    --security-opt seccomp=unconfined \
    --security-opt apparmor=unconfined \
    -e BUILDKITD_FLAGS=--oci-worker-no-process-sandbox \
    -v /path/to/dir:/tmp/work \
    --entrypoint buildctl-daemonless.sh \
    moby/buildkit:master-rootless \
        build \
        --frontend \
        dockerfile.v0 \
        --local context=/tmp/work \
        --local dockerfile=/tmp/work
```

## OpenTelemetry support

BuildKit supports [OpenTelemetry](https://opentelemetry.io/) for buildkitd gRPC
API and buildctl commands. To capture the trace to
[Jaeger](https://github.com/jaegertracing/jaeger), set `JAEGER_TRACE`
environment variable to the collection address.

```bash
docker run -d -p6831:6831/udp -p16686:16686 jaegertracing/all-in-one:latest
export JAEGER_TRACE=0.0.0.0:6831
# restart buildkitd and buildctl so they know JAEGER_TRACE
# any buildctl command should be traced to http://127.0.0.1:16686/
```

## Running BuildKit without root privileges

Please refer to [`docs/rootless.md`](docs/rootless.md).

## Building multi-platform images

Please refer to [`docs/multi-platform.md`](docs/multi-platform.md).

### Configuring `buildctl`

#### Color Output Controls

`buildctl` has support for modifying the colors that are used to output information to the terminal. You can set the environment variable `BUILDKIT_COLORS` to something like `run=green:warning=yellow:error=red:cancel=255,165,0` to set the colors that you would like to use. Setting `NO_COLOR` to anything will disable any colorized output as recommended by [no-color.org](https://no-color.org/).

Parsing errors will be reported but ignored. This will result in default color values being used where needed.

- [The list of pre-defined colors](https://github.com/moby/buildkit/blob/master/util/progress/progressui/colors.go).

#### Number of log lines (for active steps in tty mode)
You can change how many log lines are visible for active steps in tty mode by setting `BUILDKIT_TTY_LOG_LINES` to a number (default: 6).

## Contributing

Want to contribute to BuildKit? Awesome! You can find information about contributing to this project in the [CONTRIBUTING.md](/.github/CONTRIBUTING.md)
