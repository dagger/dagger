---
slug: /406009/multiplatform-support
displayed_sidebar: "current"
category: "guides"
tags: ["go"]
authors: ["Erik Sipsma"]
date: "2022-11-20"
---

# Understand Multi-Platform Support

## Introduction

Dagger supports pulling container images and executing commands on platforms that differ from the underlying host.

For example, you can use Dagger to compile binaries that target different CPU architectures, test those binaries and push them to a registry bundled as a multi-platform container image, _all on a single host_.

This document explores these features through the lens of the Go SDK.

## Requirements

This guide assumes that:

- You have a Go development environment with Go 1.20 or later. If not, [download and install Go](https://go.dev/doc/install).
- You are familiar with the basics of the Go SDK and have it installed. If not, read the [Go SDK guide](../sdk/go/959738-get-started.md) and the [Go SDK installation instructions](../sdk/go/371491-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have `binfmt_misc` configured on the host kernel (that is, on the machine where Dagger will run containers). This is necessary to execute binaries in containers that use a different architecture than that of the host.

  If the host is running Docker, this one-liner will setup everything (only needed once per boot cycle):

  ```sh
  docker run --privileged --rm tonistiigi/binfmt --install all
  ```

  Learn more in the [`binfmt` documentation](https://github.com/tonistiigi/binfmt/).

  :::note
  Dagger users running on MacOS can ignore these instructions. By default on MacOS, Dagger will run inside a Linux VM that should already be configured with `binfmt_misc`.
  :::

## Terminology

### Platform

- A combination of OS and CPU architecture that executable code may target.
- Registries compatible with the OCI Image Spec support pulling and pushing images with layers for different platforms all bundled together ([see the spec here](https://github.com/opencontainers/image-spec/blob/main/image-index.md#image-index-property-descriptions))

### Emulation

- A technique by which a binary built to target one CPU architecture can be executed on a different CPU architecture via automatic conversion of machine code.
- Typically quite slow relative to executing on a native CPU.
- For builds, cross-compilation will generally be much faster when it's an option.

### Cross-compilation

- A technique by which you can build a binary that targets Platform A on a machine of Platform B. I.e. cross-compilation enables you to build a Windows x86_64 binary from a Linux aarch64 host.

## Examples

### Pull images and execute commands for multiple architectures

This example demonstrates how to pull images for multiple different architectures and execute commands on each of them.

```go file=./snippets/multiplatform-support/pull-images/main.go

```

As illustrated above, you can optionally initialize a `Container` with a specific platform. That platform will be used to pull images and execute any commands.

If the platform of the `Container` does not match that of the host, then emulation will be used for any commands specified in `WithExec`.

If you don't specify a platform, the `Container` will be initialized with a platform matching that of the host.

### Create new multi-platform images

The next step builds on the previous example by:

1. Building binaries for each of the platforms. We'll use Go binaries for this example.
1. Combining those binaries into a multi-platform image that we push to a registry.

Start by running builds using emulation. The next example will show the changes needed to instead perform cross-compilation while still building a multi-platform image.

:::note
This example will fail to push the final image unless you change the registry to one that you control and have write permissions for.
:::

```go file=./snippets/multiplatform-support/build-images-emulation/main.go

```

### Use cross-compilation

The previous example results in emulation being used to build the binary for different architectures.

Emulation is great to have because it requires no customization of build options; the exact same build can be run for different platforms.

However, emulation has the downside of being quite slow relative to executing native CPU instructions.

While cross-compilation is sometimes much easier said than done, it's a great option for speeding up multi-platform builds when feasible.

Fortunately, Go has great built-in support for cross-compilation, so modifying the previous example to use this feature instead is straightforward (changes are highlighted):

```go file=./snippets/multiplatform-support/build-images-cross-compilation/main.go

```

The only changes we made to enable faster cross-compilation are:

1. Pulling the base `golang` image for the host platform
1. Configuring the Go compiler to target the specific platform

The final image is still multi-platform because each `Container` set as a `PlatformVariant` was initialized with a specific platform (after the cross-compilation has occurred, at the bottom of the `for` loop in the code above).

## Support for non-Linux platforms

The previous examples work with different architectures but the OS of the platform is always `linux`.

As explored in our [Get Started tutorial](../sdk/go/959738-get-started.md), Dagger can run cross-compilation builds that create binaries targeting other OSes such as Darwin (MacOS) and Windows.

Additionally, Dagger has _limited_ support for some operations involving non-Linux container images. Specifically, it is often possible to pull these images and perform basic file operations, but attempting to execute commands will result in an error:

```go file=./snippets/multiplatform-support/non-linux-support/main.go

```

:::note
Learn more about [support for executing commands on non-Linux OSes in this tracking issue](https://github.com/dagger/dagger/issues/3158).
:::

## FAQ

### What is the default value of platform if I don't specify it?

The platform will default to that of the machine running your containers.

If you are running Dagger from MacOS, by default your containers will run in a Linux virtual machine, so your platform will default to either `linux/amd64` (on Intel Macs) or `linux/arm64` (on ARM Macs).

### How do I know the valid values of platform?

The names of OSes and CPU architectures that we support are inherited from the [OCI image spec](https://github.com/opencontainers/image-spec/blob/main/image-index.md#image-index-property-descriptions), which in turn inherits names used by Go.

You can see the full list of valid platform strings by running the command `go tool dist list`. Some examples include:

- `linux/386`
- `linux/amd64`
- `linux/arm`
- `linux/arm64`
- `linux/mips`
- `linux/mips64`
- `linux/mips64le`
- `linux/mipsle`
- `linux/ppc64`
- `linux/ppc64le`
- `linux/riscv64`
- `linux/s390x`
- `windows/386`
- `windows/amd64`
- `windows/arm`
- `windows/arm64`

Whether a particular platform can be used successfully with Dagger depends on several factors:

- Whether an image you are pulling has a published version for that platform
- Whether QEMU emulation is supported for the architecture and has been configured (as described in Requirements above)
- Whether the OS is Linux (command execution only works on Linux for now)
