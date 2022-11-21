---
slug: /sdk/go/959738/get-started
---

# Get Started with the Dagger Go SDK

## Introduction

This tutorial teaches you the basics of using Dagger in Go. You will learn how to:

- Install the Go SDK
- Create a Go CI tool that builds a Go application for multiple architectures and Go versions using the Go SDK

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Go programming language. If not, [read the Go tutorial](https://go.dev/doc/tutorial/getting-started).
- You have a Go development environment with Go 1.15 or later. If not, [download and install Go](https://go.dev/doc/install).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

:::note
This tutorial creates a Go CI tool using the Dagger Go SDK. It uses this tool to build the Go application in the current directory. The binary from this guide can be used to build any Go project.
:::

## Step 1: Create a Go module for the tool

The first step is to create a new Go module for the tool.

```shell
mkdir multibuild
cd multibuild
go mod init multibuild
```

## Step 2: Create a Dagger client in Go

:::note
If you would prefer to use the final `main.go` file right away, it can be found in [Step 5](#step-5-create-a-multi-build-pipeline)
:::

Create a new file named `main.go` and add the following code to it.

```go file=snippets/get-started/step2/main.go
```

This Go CI tool stub imports the Dagger SDK and defines two functions: `main()`, which provides an interface for the user to pass in an argument to the tool and `build()`, which defines the pipeline operations.

The `build()` function creates a Dagger client with [`dagger.Connect()`](https://pkg.go.dev/dagger.io/dagger#Connect). This client provides an interface for executing commands against the Dagger engine. This function is sparse to begin with; it will be improved in subsequent steps.

## Step 3: Add the Dagger Go SDK to the module

{@include: ../../partials/_install-sdk-go.md}

Try the Go CI tool by executing the commands below:

```shell
go run main.go
```

The tool outputs the string below, although it isn't actually building anything yet.

```shell
Building with Dagger
```

## Step 4: Create a single-build pipeline

Now that the basic structure of the Go CI tool is defined and functional, the next step is to flesh out its `build()` function to actually build the Go application.

Replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/get-started/step4/main.go
```

The revised `build()` function is the main workhorse here, so let's step through it in detail.

- It begins by creating a Dagger client with [`dagger.Connect()`](https://pkg.go.dev/dagger.io/dagger#Connect), as before.
- It uses the client's [`Host().Directory()`](https://pkg.go.dev/dagger.io/dagger#Host.Directory) method to obtain a reference to the current directory on the host. This reference is stored in the `src` variable.
- It initializes a new container from a base image with the [`Container().From()`](https://pkg.go.dev/dagger.io/dagger#Container.From) method and returns a new `Container` struct. In this case, the base image is the `golang:latest` image.
- It mounts the filesystem of the repository branch in the container using the [`WithMountedDirectory()`](https://pkg.go.dev/dagger.io/dagger#Container.WithMountedDirectory) method of the `Container`.
  - The first argument is the target path in the container (here, `/src`).
  - The second argument is the directory to be mounted (here, the reference previously created in the `src` variable).
  It also changes the current working directory to the `/src` path of the container using the [`WithWorkdir()`](https://pkg.go.dev/dagger.io/dagger#Container.WithWorkdir) method and returns a revised `Container` with the results of these operations.
- It uses the [`WithExec()`](https://pkg.go.dev/dagger.io/dagger#Container.WithExec) method to define the command to be executed in the container - in this case, the command `go build -o PATH`, where `PATH` refers to the `build/` directory in the container. The `WithExec()` method returns a revised `Container` containing the results of command execution.
- It obtains a reference to the `build/` directory in the container with the [`Directory()`](https://pkg.go.dev/dagger.io/dagger#Directory) method.
- It writes the `build/` directory from the container to the host using the `Directory.Export()` method.

Try the tool by executing the commands below:

```shell
go run main.go
```

The Go CI tool builds the current Go project and writes the build result to `build/` on the host.

Use the `tree` command to see the build artifact on the host, as shown below:

```shell
tree build
build
└── multibuild
```

## Step 5: Create a multi-build pipeline

Now that the Go CI tool can build a Go application and output the build result, the next step is to extend it for multiple OS and architecture combinations.

Replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/get-started/step5a/main.go
```

This revision of the Go CI tool does much the same as before, except that it now supports building the application for multiple OSs and architectures.

- It defines the build matrix, consisting of two OSs (`darwin` and `linux`) and two architectures (`amd64` and `arm64`).
- It iterates over this matrix, building the Go application for each combination. The Go build process is instructed via the `GOOS` and `GOARCH` build variables, which are reset for each case via the [`Container.WithEnvVariable()`](https://pkg.go.dev/dagger.io/dagger#Container.WithEnvVariable) method.
- It creates an output directory on the host named for each OS/architecture combination so that the build outputs can be differentiated.

Try the Go CI tool by executing the commands below:

```shell
go run main.go
```

The Go CI tool builds the application for each OS/architecture combination and writes the build results to the host. You will see the build process run four times, once for each combination. Note that the each build is happening concurrently, because each build in the DAG do not depend on eachother.

Use the `tree` command to see the build artifacts on the host, as shown below:

```shell
tree build
build/
├── darwin
│   ├── amd64
│   │   └── multibuild
│   └── arm64
│       └── multibuild
└── linux
    ├── amd64
    │   └── multibuild
    └── arm64
        └── multibuild
```

Another common operation in a CI environment involves creating builds targeting multiple Go versions. To do this, extend the Go CI tool further and replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/get-started/step5b/main.go
```

This revision of the Go CI tool adds another layer to the build matrix, this time for Go language versions. Here, the `build()` function uses the Go version number to download the appropriate Go base image for each build. It also adds the Go version number to each build output directory on the host to differentiate the build outputs.

Try the Go CI tool by executing the commands below:

```shell
go run main.go
```

The Go CI tool builds the application for each OS/architecture/version combination and writes the results to the host. You will see the build process run eight times, once for each combination. Note that the builds are happening concurrently, because each build in the DAG does not depend on any other build.

Use the `tree` command to see the build artifacts on the host, as shown below:

```shell
tree build
build/
├── 1.18
│   ├── darwin
│   │   ├── amd64
│   │   │   └── multibuild
│   │   └── arm64
│   │       └── multibuild
│   └── linux
│       ├── amd64
│       │   └── multibuild
│       └── arm64
│           └── multibuild
└── 1.19
    ├── darwin
    │   ├── amd64
    │   │   └── multibuild
    │   └── arm64
    │       └── multibuild
    └── linux
        ├── amd64
        │   └── multibuild
        └── arm64
            └── multibuild

```

:::tip
As the previous steps illustrate, the Dagger Go SDK allows you to author your pipeline entirely in Go. This means that you don't need to spend time learning a new language, and you immediately benefit from all the powerful programming capabilities and packages available in Go. For instance, this tutorial used native Go variables, conditionals and error handling throughout, together with the errgroup package for sub-task parallelization.
:::

## Conclusion

This tutorial introduced you to the Dagger Go SDK. It explained how to install the SDK and use it with a Go module. It also provided a working example of a Go CI tool powered by the SDK, which is able to build an application for multiple OSs, architectures and Go versions in parallel.

Use the [SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about the Dagger Go SDK.
