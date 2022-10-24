---
slug: /sdk/go/959738/get-started
---

# Get Started with the Dagger Go SDK

## Introduction

This tutorial teaches you the basics of using Dagger in Go. You will learn how to:

- Install the Go SDK
- Create a Go CI tool that builds a Go application with multiple architectures and Go versions using the Dagger Go SDK

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Go programming language. If not, [read the Go tutorial](https://go.dev/doc/tutorial/getting-started).
- You have a Go development environment with Go 1.15 or later. If not, [download and install Go](https://go.dev/doc/install).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Create a Go CI/CD tool

Begin creating a Go CI tool that uses the Dagger SDK to build your application. In this step, create the main function that the tool will run.

1. Create a new Go module where the tool will be developed.

```shell
mkdir multibuild
cd multibuild
go mod init multibuild
```

1. Create a new file named `main.go` and add the following code to it. Save the file once done.

:::note
If you would rather copy the complete `main.go` right away, it can be found in the [Appendix](#appendix-completed-code-sample)
:::

```go
package main

import (
  "context"
  "fmt"
  "os"
  "path/filepath"

  "go.dagger.io/dagger/sdk/go/dagger"
  "go.dagger.io/dagger/sdk/go/dagger/api"
  "golang.org/x/sync/errgroup"
)

func main() {
  if len(os.Args) < 2 {
    fmt.Println("must pass in a git repo to build")
    os.Exit(1)
  }
  repo := os.Args[1]
  if err := build(repo); err != nil {
    fmt.Println(err)
  }
}

func build(repoUrl string) error {
  fmt.Printf("Building %s\n", repoUrl)
  return nil
}
```

  This tool imports the Dagger SDK and defines two functions: `main()`, which provides an interface for the user to pass in an argument, and `build()`, which is where the pipeline will be defined in the next steps.

  The `main()` function accepts a git repo url as an argument. This is a Go repo that the tool will build in the following steps.

1. Install the Dagger Go SDK

{@include: ../../partials/_install-sdk-go.md}

1. Try the tool by executing the command below:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

  The tool will output `Building https://github.com/kpenfound/greetings-api.git`, although it isn't actually building anything yet.

## Step 2: Build a git repo with the Dagger Go SDK

Now that the basic structure of `main.go` is setup, it is time to actually build the provided git repo.

1. Update the file `main.go` and fill in `build()` function to it as shown below. Save the file once done.

```go
func build(repoUrl string) error {
  fmt.Printf("Building %s\n", repoUrl)

  // 1. Get a context
  ctx := context.Background()
  // 2. Initialize dagger client
  client, err := dagger.Connect(ctx)
  if err != nil {
    return err
  }
  defer client.Close()
  // 3. Clone the repo using Dagger
  repo := client.Core().Git(repoUrl)
  src, err := repo.Branch("main").Tree().ID(ctx)
  if err != nil {
    return err
  }
  // 4. Load the golang image
  golang := client.Core().Container().From("golang:latest")
  // 5. Mount the cloned repo to the golang image
  golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")
  // 6. Do the go build
  golang = golang.Exec(api.ContainerExecOpts{
    Args: []string{"go", "build", "-o", "build/"},
  })
  return nil
}
```

This new code will connect to a dagger engine, clone the given git repo, load a golang container image, and build the repo.

- initialize a `context.Background` for the client to use.
- get a Dagger client with `dagger.Connect()`. This will provide the interface to execute commands against the Dagger engine.
- clone the git repo. `client.Core().Git()` gives a `GitRepository`, then `.Branch("main").Tree().ID()` will clone the main branch.
- load the latest golang image with `client.Core().Container().From("golang:latest")`.
- mount the cloned repo with `.WithMountedDirectory("/src", src)` and set the container's working directory using `.WithWorkdir("/src")`.
- execute the build command, `go build -o build/` by calling `Container.Exec()`.

1. Try the `test` step of the pipeline by executing the command below from the application directory:

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
```

In the output of this command, you will see Dagger cloning the git repo and running go build on it.

## Step 3: Copy the build output to the host machine

Once the tool has completed the build, it should put that build artifact somewhere to be used. In this step, Dagger will copy the build artifact to the host machine after the build is complete.

1. Update the file `main.go` and some new steps to the `build()` function as shown below. Save the file once done.

```go
func build(repoUrl string) error {
  ...
  // 1. reference to the current working directory on the host
  workdir := client.Core().Host().Workdir() // <-- New

  golang := client.Core().Container().From("golang:latest")
  golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

  // 2. Create the output path on the host for the build
  // -->
  path := "build/"
  outpath := filepath.Join(".", path)
  err = os.MkdirAll(outpath, os.ModePerm)
  if err != nil {
    return err
  }
  // <-- New

  golang = golang.Exec(api.ContainerExecOpts{
    Args: []string{"go", "build", "-o", path},
  })

  // 3. Get build output from builder
  // -->
  output, err := golang.Directory(path).ID(ctx)
  if err != nil {
    return err
  }
  // <-- New

  // 4. Write the build output to the host
  // -->
  _, err = workdir.Write(ctx, output, api.HostDirectoryWriteOpts{Path: path})
  if err != nil {
    return err
  }
  // <-- New

  return nil
}
```

With this new code, the tool will now write the build artifact to the host after the build is complete.

- using the Dagger Go SDK, a reference to the host workdir is created with `.Core().Host().Workdir()`
- in native Go, create a directory where the build artifact will be output
- create a reference to the build output in the Dagger engine with `Container.Directory().ID()`
- write the directory to the host with `HostDirectory.Write()`

1. Now try out the updated build function, running the tool exactly as before

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
tree build
```

In the output of the multibuild, you'll see the build happening the same as it did before, but then Dagger will write the build artifact to `build/`.

The output of the `tree` command will show you the built artifact on your machine at `build/greetings-api`.

## Step 4: Build for multiple OS and architectures

Now that the tool can build a Go application and output the build result, it should target multiple OS and architecture combinations. Many applications need to be distributed to users on a variety of systems.

1. Update the file `main.go` and some new steps to the `build()` function as shown below. Save the file once done.

```go
func build(repoUrl string) error {
  ...

  // 1. Define our build matrix
  // -->
  oses := []string{"linux", "darwin"}
  arches := []string{"amd64", "arm64"}
  // <-- New

  ...

  // 2. Loop through the os and arch matrices
  for _, goos := range oses {
    for _, goarch := range arches {
      // 3. Create a directory for each os and arch
      path := fmt.Sprintf("build/%s/%s/", goos, goarch) // <-- Changed
      outpath := filepath.Join(".", path)

      ...

      // 4. Set GOARCH and GOOS in the build environment
      // -->
      build := golang.WithEnvVariable("GOOS", goos) // <-- Uses new reference for the container, "build". Updated references below
      build = build.WithEnvVariable("GOARCH", goarch)
      // <--
      build = build.Exec(api.ContainerExecOpts{
        Args: []string{"go", "build", "-o", path},
      })

      ...
    }
  }
  return nil
}
```

Now the tool is doing the build just as before, except for multiple OS and architectures

- define the build matrix. In this case darwin and linux on amd64 and arm64.
- iterate through each OS and architecture combination
- create an output directory that includes the OS and architecture so the build outputs can be differentiated
- set GOOS and GOARCH in the go build environment

1. Now try out the updated build function, running the tool exactly as before

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
tree build
```

In the output of the multibuild, you'll see the build happening the same as it did before, but 4 times, building each OS and archictecture combination.

The output of the `tree` command will show you the all of the built artifacts on your machine at `build/<darwin|linux>/<amd64|arm64>/greetings-api`.

## Step 5. Build for multiple Go versions

Another common operation that might happen in a CI environment is targeting multiple Go versions. In this step the tool will be updated to build the git repo with multiple Go versions.

1. Update the file `main.go` and some new steps to the `build()` function as shown below. Save the file once done.

```go
func build(repoUrl string) error {
  ...
  // 1. Define multiple Go versions
  goVersions := []string{"1.18", "1.19"}

  // 2. Iterate through the Go versions
  for _, version := range goVersions {
    // 3. Determine the golang image to use
    imageTag := fmt.Sprintf("golang:%s", version)
    golang := client.Core().Container().From(imageTag) // <-- Updated with the formatted image tag

    for _, goos := range oses {
      for _, goarch := range arches {
        // 4. Update the output artifact path
        path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch) // <-- Updated with version
        outpath := filepath.Join(".", path)

        ...
      }
    }
  }
  return nil
}
```

Similar to the previous step, another layer to the build matrix is added, this time with Go versions

- define the Go versions to use: 1.18 and 1.19
- iterate though these versions at the top level
- using string templating, determine the golang image tag to use for the Go version
- use the Go version in the build artifact output path to differentiate build outputs

1. Now try out the updated build function, running the tool exactly as before

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
tree build
```

In the output of the multibuild, you'll see the build happening the same as it did before, but 8 times, building each Go version, OS, and archictecture combination.

The output of the `tree` command will show you the all of the built artifacts on your machine at `build/<go version>/<darwin|linux>/<amd64|arm64>/greetings-api`.

## Step 6: Run builds in parallel

Running all of these matrix combinations is very useful, but adds to the total amount of time our pipeline takes to complete. Because these individual builds do not rely on eachother, they can be run in parallel and save time overall.

1. Update the file `main.go` and some new steps to the `build()` function as shown below. Save the file once done.

```go
func build(repoUrl string) error {
  ...
  // 1. Create an errgroup
  g, ctx := errgroup.WithContext(ctx)

  ...

  for _, version := range goVersions {
    ...

    for _, goos := range oses {
      for _, goarch := range arches {
        // 2. Run version/os/arch build in errgroup
        goos, goarch, version := goos, goarch, version
        g.Go(func() error {
          path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)
          outpath := filepath.Join(".", path)
          err = os.MkdirAll(outpath, os.ModePerm)
          if err != nil {
            return err
          }

          build := golang.WithEnvVariable("GOOS", goos)
          build = build.WithEnvVariable("GOARCH", goarch)
          build = build.Exec(api.ContainerExecOpts{
            Args: []string{"go", "build", "-o", path},
          })

          output, err := build.Directory(path).ID(ctx)
          if err != nil {
            return err
          }

          _, err = workdir.Write(ctx, output, api.HostDirectoryWriteOpts{Path: path})
          if err != nil {
            return err
          }
          return nil
        })
      }
    }
  }
  // 3. Wait for all builds to complete
  if err := g.Wait(); err != nil {
    return err
  }
  return nil
}
```

Now the build steps are the same, except they're executed with an [errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup)

- create an errgroup to manage the build processes
- run the same build steps as before, except within a an errgroup anonymous function to parallelize the process
- wait for all of the build processes to complete before returning

1. Now try out the updated build function, running the tool exactly as before

```shell
go build
./multibuild https://github.com/kpenfound/greetings-api.git
tree build
```

The output of multibuild will show all of the builds happening at the same time, and the total time will be reduced. The output of `tree` will show the same output artifacts as before

:::tip
As the previous three steps illustrate, the Dagger Go SDK allows you to author your pipeline entirely in Go. This means that you don't need to spend time learning a new language, and you immediately benefit from all the powerful programming capabilities and packages available Go. For instance, this tutorial used native Go variables, conditionals and error handling throughout together with Go's testing package and built-in test framework.
:::

## Conclusion

This tutorial introduced you to the Dagger Go SDK. It explained how to install the SDK and use it to create a Go CI tool. It also provided code samples and explanations of how to build an application with the Go SDK.

Use the [SDK Reference](https://pkg.go.dev/go.dagger.io/dagger@v0.3.0-alpha.1) to learn more about the Dagger Go SDK.

## Appendix: Completed code sample

`main.go`:

```go
package main

import (
  "context"
  "fmt"
  "os"
  "path/filepath"

  "go.dagger.io/dagger/sdk/go/dagger"
  "go.dagger.io/dagger/sdk/go/dagger/api"
  "golang.org/x/sync/errgroup"
)

func main() {
  if len(os.Args) < 2 {
    fmt.Println("must pass in a git repo to build")
    os.Exit(1)
  }
  repo := os.Args[1]
  if err := build(repo); err != nil {
    fmt.Println(err)
  }
}

func build(repoUrl string) error {
  fmt.Printf("Building %s\n", repoUrl)

  ctx := context.Background()
  g, ctx := errgroup.WithContext(ctx)

  oses := []string{"linux", "darwin"}
  arches := []string{"amd64", "arm64"}
  goVersions := []string{"1.18", "1.19"}

  client, err := dagger.Connect(ctx)
  if err != nil {
    return err
  }
  defer client.Close()

  repo := client.Core().Git(repoUrl)
  src, err := repo.Branch("main").Tree().ID(ctx)
  if err != nil {
    return err
  }

  workdir := client.Core().Host().Workdir()

  for _, version := range goVersions {
    imageTag := fmt.Sprintf("golang:%s", version)
    golang := client.Core().Container().From(imageTag)
    golang = golang.WithMountedDirectory("/src", src).WithWorkdir("/src")

    for _, goos := range oses {
      for _, goarch := range arches {
        goos, goarch, version := goos, goarch, version
        g.Go(func() error {
          path := fmt.Sprintf("build/%s/%s/%s/", version, goos, goarch)
          outpath := filepath.Join(".", path)
          err = os.MkdirAll(outpath, os.ModePerm)
          if err != nil {
            return err
          }

          build := golang.WithEnvVariable("GOOS", goos)
          build = build.WithEnvVariable("GOARCH", goarch)
          build = build.Exec(api.ContainerExecOpts{
            Args: []string{"go", "build", "-o", path},
          })

          output, err := build.Directory(path).ID(ctx)
          if err != nil {
            return err
          }

          _, err = workdir.Write(ctx, output, api.HostDirectoryWriteOpts{Path: path})
          if err != nil {
            return err
          }
          return nil
        })
      }
    }
  }
  if err := g.Wait(); err != nil {
    return err
  }
  return nil
}
```
