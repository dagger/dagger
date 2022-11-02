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
This tutorial creates a Go CI tool using the Dagger Go SDK. It uses this tool to build an [example Go application from GitHub](https://github.com/kpenfound/greetings-api.git). If you already have a Go application on GitHub, you can use your own application and repository instead.
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
If you would prefer to use the final `main.go` file right away, it can be found in [Step 6](#step-6-run-builds-in-parallel)
:::

Create a new file named `main.go` and add the following code to it.

```go file=snippets/step2/main.go
```

This Go CI tool stub imports the Dagger SDK and defines two functions: `main()`, which provides an interface for the user to pass in an argument to the tool and `build()`, which defines the pipeline operations.

- The `main()` function accepts a Git repository URL as a command line parameter. This repository should contain a Go application for the tool to build.
- The `build()` function creates a Dagger client with [`dagger.Connect()`](https://pkg.go.dev/dagger.io/dagger#Connect). This client provides an interface for executing commands against the Dagger engine. This function is sparse to begin with; it will be improved in subsequent steps.

## Step 3: Add the Dagger Go SDK to the module

{@include: ../../partials/_install-sdk-go.md}

Try the Go CI tool by executing the commands below:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

The tool outputs the string below, although it isn't actually building anything yet.

```shell
Building https://github.com/kpenfound/greetings-api.git
```

## Step 4: Create a single-build pipeline

Now that the basic structure of the Go CI tool is defined and functional, the next step is to flesh out its `build()` function to actually build the Go application from the source repository.

Replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/step4/main.go
```

The revised `build()` function is the main workhorse here, so let's step through it in detail.

- It begins by creating a Dagger client with [`dagger.Connect()`](https://pkg.go.dev/dagger.io/dagger#Connect), as before.
- It uses the client's [`Git()`](https://pkg.go.dev/dagger.io/dagger#Query.Git) function to obtain a reference to the target repository. This function returns a `GitRepository` struct. The struct's [`Branch()`](https://pkg.go.dev/dagger.io/dagger#GitRepository.Branch) function provides details on a specific branch (here, the `main` branch), the [`Tree()`](https://pkg.go.dev/dagger.io/dagger#GitRef.Tree) function returns the directory of the branch, and the [`ID()`](https://pkg.go.dev/dagger.io/dagger#Directory.ID) function returns a reference for the directory. This reference is stored in the `src` variable.

  ```go
  ...
  repo := client.Git(repoUrl)
  src, err := repo.Branch("main").Tree().ID(ctx)
  ```

- It initializes a new container from a base image with the [`Container().From()`](https://pkg.go.dev/dagger.io/dagger#Container.From) function and returns a new `Container` struct. In this case, the base image is the `golang:latest` image.

  ```go
  ...
  golang := client.Container().From("golang:latest")
  ```

- It mounts the filesystem of the repository branch in the container using the [`WithMountedDirectory()`](https://pkg.go.dev/dagger.io/dagger#Container.WithMountedDirectory) function of the `Container`.
  - The first argument to this function is the target path in the container (here, `/src`).
  - The second argument is the directory to be mounted (here, the reference previously created in the `src` variable).
  It also changes the current working directory to the `/src` path of the container using the [`WithWorkdir()`](https://pkg.go.dev/dagger.io/dagger#Container.WithWorkdir) function and returns a revised `Container` with the results of these operations.

  ```go
  ...
  golang := client.Container().From("golang:latest")
  golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")
  ```

- It uses the [`Exec()`](https://pkg.go.dev/dagger.io/dagger#Container.Exec) function to define the command to be executed in the container - in this case, the command `go build -o PATH`, where `PATH` refers to the `build/` directory in the container. The `Exec()` function returns a revised `Container` containing the results of command execution.

  ```go
  ...
  golang = golang.Exec(dagger.ContainerExecOpts{
    Args: []string{"go", "build", "-o", path},
  })
  ```

- It obtains a reference to the `build/` directory in the container with the [`Directory().ID()`](https://pkg.go.dev/dagger.io/dagger#Directory.ID) function.
- It copies the build result from the container to the host as follows:
  - Using the Go standard library, it creates a directory on the host to store the final build output.
  - It uses the client's [`Host().Workdir()`](https://pkg.go.dev/dagger.io/dagger#Host.Workdir) function to obtain a reference to the current working directory on the host. This reference is stored as a `HostDirectory` struct in the `workdir` variable.
  - It writes the `build/` directory from the container to the host using the [`Write()`](https://pkg.go.dev/dagger.io/dagger#HostDirectory.Write) function.

  ```go
  ...
  workdir := client.Host().Workdir()
  _, err = workdir.Write(ctx, output, dagger.HostDirectoryWriteOpts{Path: path})
  ```

Try the tool by executing the commands below:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

The Go CI tool clones the Git repository, builds the application and writes the build result to `build/` on the host.

Use the `tree` command to see the build artifact on the host, as shown below:

```shell
tree build
build
└── greetings-api
```

## Step 5: Create a multi-build pipeline

Now that the Go CI tool can build a Go application and output the build result, the next step is to extend it for multiple OS and architecture combinations.

Replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/step5a/main.go
```

This revision of the Go CI tool does much the same as before, except that it now supports building the application for multiple OSs and architectures.

- It defines the build matrix, consisting of two OSs (`darwin` and `linux`) and two architectures (`amd64` and `arm64`).
- It iterates over this matrix, building the Go application for each combination. The Go build process is instructed via the `GOOS` and `GOARCH` build variables, which are reset for each case via the [`WithEnvVariable()`](https://pkg.go.dev/dagger.io/dagger#Container.WithEnvVariable) function of the `Container`.
- It creates an output directory on the host named for each OS/architecture combination so that the build outputs can be differentiated.

Try the Go CI tool by executing the commands below:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

The Go CI tool clones the Git repository, builds the application for each OS/architecture combination and writes the build results to the host. You will see the build process run four times, once for each combination.

Use the `tree` command to see the build artifacts on the host, as shown below:

```shell
tree build
build/
├── darwin
│   ├── amd64
│   │   └── greetings-api
│   └── arm64
│       └── greetings-api
└── linux
    ├── amd64
    │   └── greetings-api
    └── arm64
        └── greetings-api
```

Another common operation in a CI environment involves creating builds targeting multiple Go versions. To do this, extend the Go CI tool further and replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/step5b/main.go
```

This revision of the Go CI tool adds another layer to the build matrix, this time for Go language versions. Here, the `build()` function uses the Go version number to download the appropriate Go base image for each build. It also adds the Go version number to each build output directory on the host to differentiate the build outputs.

Try the Go CI tool by executing the commands below:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

The Go CI tool clones the Git repository, builds the application for each OS/architecture/version combination and writes the results to the host. You will see the build process run eight times, once for each combination.

Use the `tree` command to see the build artifacts on the host, as shown below:

```shell
tree build
build/
├── 1.18
│   ├── darwin
│   │   ├── amd64
│   │   │   └── greetings-api
│   │   └── arm64
│   │       └── greetings-api
│   └── linux
│       ├── amd64
│       │   └── greetings-api
│       └── arm64
│           └── greetings-api
└── 1.19
    ├── darwin
    │   ├── amd64
    │   │   └── greetings-api
    │   └── arm64
    │       └── greetings-api
    └── linux
        ├── amd64
        │   └── greetings-api
        └── arm64
            └── greetings-api

```

## Step 6: Run builds in parallel

The pipeline shown in the previous step is very useful, but not very scalable: every additional OS, architecture or language version adds to the total time the pipeline requires. Since the individual builds do not rely on each other, they can be run in parallel to save time.

To see how this works, replace the `main.go` file from the previous step with the version below (highlighted lines indicate changes):

```go file=snippets/step6/main.go
```

This revision of the Go CI tool performs the same build process as before, except that the steps are executed with an [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) to parallelize the process in separate goroutines.

Try the Go CI tool by executing the commands below:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

The output shows all of the builds happening at the same time, and the total time will be reduced. The process will produce the same output artifacts as those seen at the end of [Step 5](#step-5-create-a-multi-build-pipeline).

:::tip
As the previous steps illustrate, the Dagger Go SDK allows you to author your pipeline entirely in Go. This means that you don't need to spend time learning a new language, and you immediately benefit from all the powerful programming capabilities and packages available Go. For instance, this tutorial used native Go variables, conditionals and error handling throughout, together with the errgroup package for sub-task parallelization.
:::

## Conclusion

This tutorial introduced you to the Dagger Go SDK. It explained how to install the SDK and use it with a Go module. It also provided a working example of a Go CI tool powered by the SDK, which is able to build an application for multiple OSs, architectures and Go versions in parallel.

Use the [SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about the Dagger Go SDK.
