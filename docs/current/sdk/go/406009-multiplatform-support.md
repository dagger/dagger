---
slug: /sdk/go/406009/multiplatform-support
---

# Multi-Platform Support

Dagger supports pulling container images and executing commands on platforms that differ from the underlying host.

For example, you can use Dagger to compile binaries that target different CPU architectures, test those binaries and push them to a registry bundled as a multi-platform container image, _all on a single host_.

This document explores these features through the lens of the Go SDK.

It assumes familiarity with the basics of that SDK. See the [Getting Started Guide](./959738-get-started.md) for more background.

## Terminology

### Platform

- A combination of OS and CPU architecture that executable code may target.
- Registries compatible with the OCI Image Spec support pulling and pushing images with layers for different platforms all bundled together ([see the spec here](https://github.com/opencontainers/image-spec/blob/main/image-index.md#image-index-property-descriptions))

### Emulation

- A technique by which a binary built to target one CPU architecture can be executed on a different CPU architecture via automatic conversion of machine code.
- Typically quite slow relative to executing on a native CPU.
- For builds, cross-compilation will generally be much faster when it's an option.

### Cross-Compilation

- A technique by which you can build a binary that targets Platform A on a machine of Platform B. I.e. cross-compilation enables you to build a Windows x86_64 binary from a Linux aarch64 host.

## Pre-requisites

:::note
Dagger users running on MacOS can ignore these instructions. By default on MacOS, Dagger will run inside a Linux VM that should already be configured with binfmt_misc.
:::

To execute binaries in containers that use a different architecture than that of the host, `binfmt_misc` needs to be configured on the host kernel (that is, on the machine where Dagger will run containers).

If the host is running docker, this one-liner will setup everything (only needed once per boot cycle):

```sh
docker run --privileged --rm tonistiigi/binfmt --install all
```

[See the docs here for more info](https://github.com/tonistiigi/binfmt/).

## Usage Examples

### Pulling Images and Executing Commands For Multiple Architectures

Here we will pull an image for multiple different architectures and execute commands on each of them.

```go
package main

import (
  "context"
  "fmt"
  "os"

  "dagger.io/dagger"
)

// List of platforms we will execute on
var platforms = []dagger.Platform{
  "linux/amd64", // a.k.a. x86_64
  "linux/arm64", // a.k.a. aarch64
  "linux/s390x", // a.k.a. IBM S/390
}

func main() {
  ctx := context.Background()
  client, err := dagger.Connect(ctx)
  if err != nil {
    return err
  }
  defer client.Close()

  for _, platform := range platforms {
    // Initialize this container with the platform
    ctr := client.Container(dagger.ContainerOpts{Platform: platform})

    // This alpine image has published versions for each of the
    // platforms above. If it was missing a platform, we would
    // receive an error when executing a command below.
    ctr = ctr.From("alpine:3.16")

    // Execute `uname -m`, which prints the current CPU architecture
    // being executed as
    stdout, err := c.
      Exec(dagger.ContainerExecOpts{
        Args: []string{"uname", "-m"},
      }).
      Stdout().Contents(ctx)
    if err != nil {
      panic(err)
    }

    // This should print 3 times, once for each of the architectures
    // we are executing as
    fmt.Println(fmt.Sprintf("I'm executing on architecture: %s", stdout))
  }
}
```

As illustrated above, you can optionally initialize a `Container` with a specific platform. That platform will be used to pull images and execute any commands.

If the platform of the `Container` does not match that of the host, then emulation will be used for any commands specified in `Exec`.

If you don't specify a platform, the `Container` will be initialized with a platform matching that of the host.

### Creating New Multi-Platform Images

Now let's build on the previous example by:

1. Building binaries for each of the platforms. We'll use Go binaries for this example.
1. Combining those binaries into a multi-platform image that we push to a registry.

We'll start by running builds using emulation. The next example will show the changes needed to instead perform cross-compilation while still building a multi-platform image.

Note that this example will fail to push the final image unless you change the registry to one that you control and have write permissions for.

```go
package main

import (
  "context"
  "fmt"
  "os"

  "dagger.io/dagger"
)

// The platforms we will build for and push in a multi-platform image
var platforms = []dagger.Platform{
  "linux/amd64", // a.k.a. x86_64
  "linux/arm64", // a.k.a. aarch64
  "linux/s390x", // a.k.a. IBM S/390
}

// The container image repo we will push our multi-platform image to
const imageRepo = "localhost/testrepo:latest"

func main() {
  ctx := context.Background()
  client, err := dagger.Connect(ctx)
  if err != nil {
    return err
  }
  defer client.Close()

  // The git repository containing code for the binary we will build
  gitRepo := client.Git("https://github.com/dagger/dagger.git").
    Branch("027d0c0").
    Tree()

  var platformVariants []*dagger.Container
  for _, platform := range platforms {
    // pull the golang image for this platform
    ctr := client.Container(dagger.ContainerOpts{Platform: platform})
    ctr = ctr.From("golang:1.19-alpine")

    // mount in our source code
    ctr = ctr.WithMountedDirectory("/src", gitRepo)

    // mount in an empty dir where we'll put our built binary
    ctr = ctr.WithMountedDirectory("/output", client.Directory())

    // ensure our binary will be statically linked and thus executable
    // in our final image
    ctr = ctr.WithEnvVariable("CGO_ENABLED", "0")

    // build the binary and put the result at our mounted output
    // directory
    ctr = ctr.Exec(dagger.ContainerExecOpts{
      Args: []string{
        "go", "build",
        "-o", "/output/cloak",
        "/src/cmd/cloak",
      },
    })

    // select the output directory
    outputDir := ctr.Directory("/output")

    // wrap the output directory in a new empty container marked
    // with the same platform
    binaryCtr := client.
      Container(dagger.ContainerOpts{Platform: platform}).
      WithFS(outputDir)
    platformVariants = append(platformVariants, binaryCtr)
  }

  // Publishing the final image uses the same API as single-platform
  // images, but now we additionally specify the `PlatformVariants`
  // option with the containers we built before.
  imageDigest, err := client.
    Container().
    Publish(ctx, imageRepo, dagger.ContainerPublishOpts{
      PlatformVariants: platformVariants,
    })
  if err != nil {
    panic(err)
  }
  fmt.Println("Pushed multi-platform image w/ digest: ", imageDigest)
}
```

#### With Cross-Compilation

The previous example results in emulation being used to build the binary for different architectures.

Emulation is great to have because it requires no customization of build options; the exact same build can be run for different platforms.

However, emulation has the downside of being quite slow relative to executing native CPU instructions.

While cross-compilation is sometimes much easier said than done, it's a great option for speeding up multi-architecture builds when feasible.

Fortunately, Go has great built-in support for cross-compilation, so modifying the previous example to use that instead is straightforward (changes are highlighted):

```go
package main

import (
  "context"
  "fmt"
  "os"

  "dagger.io/dagger"
  "github.com/containerd/containerd/platforms"
)

var platforms = []dagger.Platform{
  "linux/amd64", // a.k.a. x86_64
  "linux/arm64", // a.k.a. aarch64
  "linux/s390x", // a.k.a. IBM S/390
}

// The container image repo we will push our multi-platform image to
const imageRepo = "localhost/testrepo:latest"

// highlight-start
// util that returns the architecture of the provided platform
func architectureOf(platform dagger.Platform) string {
  return platforms.MustParse(string(platform)).Architecture
}
// highlight-end

func main() {
  ctx := context.Background()
  client, err := dagger.Connect(ctx)
  if err != nil {
    return err
  }
  defer client.Close()

  gitRepo := client.Git("https://github.com/dagger/dagger.git").
    Branch("027d0c0").
    Tree()

  var platformVariants []*dagger.Container
  for _, platform := range platforms {
    // highlight-start
    // Pull the golang image for the *host platform*. This is
    // accomplished by just not specifying a platform; the default
    // is that of the host.
    ctr := client.Container()
    ctr = ctr.From("golang:1.19-alpine")
    // highlight-end

    // mount in our source code
    ctr = ctr.WithMountedDirectory("/src", gitRepo)

    // mount in an empty dir where we'll put our built binary
    ctr = ctr.WithMountedDirectory("/output", client.Directory())

    // ensure our binary will be statically linked and thus executable
    // in our final image
    ctr = ctr.WithEnvVariable("CGO_ENABLED", "0")

    // highlight-start
    // configure the go compiler to use cross-compilation targeting the
    // desired platform
    ctr = ctr.WithEnvVariable("GOOS", "linux")
    ctr = ctr.WithEnvVariable("GOARCH", architectureOf(platform))
    // highlight-end

    // build the binary and put the result at our mounted output
    // directory
    ctr = ctr.Exec(dagger.ContainerExecOpts{
      Args: []string{
        "go", "build",
        "-o", "/output/cloak",
        "/src/cmd/cloak",
      },
    })

    // select the output directory
    outputDir := ctr.Directory("/output")

    // wrap the output directory in a new empty container marked
    // with the platform
    binaryCtr := client.
      Container(dagger.ContainerOpts{Platform: platform}).
      WithFS(outputDir)
    platformVariants = append(platformVariants, binaryCtr)
  }

  // Publishing the final image uses the same API as single-platform
  // images, but now we additionally specify the `PlatformVariants`
  // option with the containers we built before.
  imageDigest, err := client.
    Container().
    Publish(ctx, imageRepo, dagger.ContainerPublishOpts{
      PlatformVariants: platformVariants,
    })
  if err != nil {
    panic(err)
  }
}
```

The only changes we made to enable faster cross-compilation are:

1. Pulling the base golang image for the host platform
1. Configuring the go compiler to target the specific platform

The final image is still multi-platform because we initialize each `Container` with specific platforms after the cross-compilation has occurred.

### Support for Non-Linux Platforms

The previous examples work with different architectures but the OS of the platform is always `linux`.

As explored in our [Getting Started Guide](./959738-get-started.md), Dagger can run cross-compilation builds that create binaries targeting other OSes such as Darwin (MacOS) and Windows.

Additionally, Dagger has _limited_ support for some operations involving non-Linux container images. Specifically, it is often possible to pull these images and perform basic file operations, but attempting to execute commands will result in an error:

```go
package main

import (
  "context"
  "fmt"
  "os"

  "dagger.io/dagger"
)

func main() {
  ctx := context.Background()
  client, err := dagger.Connect(ctx)
  if err != nil {
    return err
  }
  defer client.Close()

  // pull a Windows base image
  ctr := client.
    Container(dagger.ContainerOpts{Platform: "windows/amd64"}).
    From("mcr.microsoft.com/windows/nanoserver:ltsc2022")

  // Listing files works, no error should be returned
  entries, err := ctr.FS().Entries(ctx)
  if err != nil {
    panic(err) // shouldn't happen
  }
  for _, entry := range entries {
    fmt.Println(entry)
  }

  // However, executing a command will fail
  output, err := ctr.Exec(dagger.ContainerExecOpts{
    Args: []string{"cmd.exe"},
  }).Stdout().Contents(ctx)
  if err != nil {
    panic(err) // should happen
  }
}
```

We have an issue tracking support for executing commands on non-Linux OSes [here](https://github.com/dagger/dagger/issues/3158).

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
- Whether QEMU emulation is supported for the architecture and has been configured (as described in Pre-requisites above)
- Whether the OS is Linux (command execution on works on Linux for now)
