---
slug: /205271/replace-dockerfile
displayed_sidebar: 'current'
---

# Replace a Dockerfile with Go

## Introduction

This guide explains how to use the Dagger Go SDK to perform all the same operations that you would typically perform with a Dockerfile, except using Go. You will learn how to:

- Create a Dagger client in Go
- Write a Dagger pipeline in Go to:
  - Configure a container with all required dependencies and environment variables
  - Download and build the application source code in the container
  - Set the container entrypoint
  - Publish the built container image to Docker Hub
- Test the Dagger pipeline locally

## Requirements

This guide assumes that:

- You have a Go development environment with Go 1.15 or later. If not, [download and install Go](https://go.dev/doc/install).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have a Go module with the Dagger Go SDK installed. If not, [install the Dagger Go SDK](../371491-install.md).
- You have a Docker Hub account. If not, [register for a Docker Hub account](https://hub.docker.com/signup).

## Step 1: Understand the source Dockerfile

To illustrate the process, this guide replicates the build process for the popular open source [Memcached caching system](https://www.memcached.org/) using Dagger. It uses the Dockerfile and entrypoint script for the [official Docker Hub Memcached image](https://github.com/docker-library/memcached).

Begin by reviewing the [source Dockerfile](https://github.com/docker-library/memcached/blob/1e3f84629bb2ab9975235401c716c1e00563fa82/alpine/Dockerfile) and corresponding [entrypoint script](https://github.com/docker-library/memcached/blob/1e3f84629bb2ab9975235401c716c1e00563fa82/alpine/docker-entrypoint.sh) to understand how it works. This Dockerfile is current at the time of writing and is available under the BSD 3-Clause License.

Broadly, this Dockerfile performs the following steps:

- It starts from a base `alpine` container image.
- It adds a `memcache` user and group with defined IDs.
- It sets environment variables for the Memcached version (`MEMCACHED_VERSION`) and commit hash (`MEMCACHED_SHA1`).
- It installs dependencies in the container.
- It downloads the source code archive for the specified version of Memcached, checks the commit hash and extracts the source code into a directory.
- It configures, builds, tests and installs Memcached from source using `make`.
- It copies and sets the container entrypoint script.
- It configures the image to run as the `memcache` user.

## Step 2: Replicate the Dockerfile using a Dagger pipeline

The Dagger Go SDK enables you to develop a CI/CD pipeline in Go to achieve the same result as using a Dockerfile.

To see how this works, add the following code to your Go module as `main.go`. Replace the DOCKER-HUB-USERNAME placeholder with your Docker Hub username.

```go file=../snippets/replace-dockerfile/main.go
```

:::warning
Like the source Dockerfile, this pipeline assumes that the entrypoint script exists in the current  working directory on the host as `docker-entrypoint.sh`. You can either create a custom entrypoint script, or use the [entrypoint script from the Docker Hub Memcached image repository](https://github.com/docker-library/memcached/blob/1e3f84629bb2ab9975235401c716c1e00563fa82/alpine/docker-entrypoint.sh).
:::

There's a lot going on here, so let's step through it in detail:

- The Go CI pipeline imports the Dagger SDK and defines a `main()` function. The `main()` function creates a Dagger client with `dagger.Connect()`. This client provides an interface for executing commands against the Dagger engine.
- It initializes a new container from a base image with the client's `Container().From()` method and returns a new `Container` struct. In this case, the base image is the `alpine:3.17` image.
- It calls the `withExec()` method to define the `adduser`, `addgroup` and `apk add` commands for execution, and the `WithEnvVariable()` method to set the `MEMCACHED_VERSION` and `MEMCACHED_SHA1` container environment variables.
- It calls a custom `setDependencies()` function, which internally uses `withExec()` to define the `apk add` command that installs all the required dependencies to build and test Memcached in the container.
- It calls a custom `downloadMemcached()` function, which internally uses `withExec()` to define the `wget`, `tar` and related commands required to download, verify and extract the Memcached source code archive in the container at the `/usr/src/memcached` container path.
- It calls a custom `buildMemcached()` function, which internally uses `withExec()` to define the `configure` and `make` commands required to build, test and install Memcached in the container. The `buildMemcached()` function also takes care of deleting the source code directory at `/usr/src/memcached` in the container and executing `memcached -V` to output the version string to the console.
- It updates the container filesystem to include the entrypoint script from the host using `withFile()` and specifies it as the command to be executed when the container runs using `WithEntrypoint()`.
- Finally, it calls the `Container.publish()` method, which executes the entire pipeline descried above and publishes the resulting container image to Docker Hub.

## Step 3: Test the Dagger pipeline

Test the Dagger pipeline as follows:

1. Log in to Docker on the host:

  ```shell
  docker login
  ```

  :::info
  This step is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
  :::

1. Run the pipeline:

  ```shell
  go run main.go
  ```

  :::warning
  Verify that you have an entrypoint script on the host at `./docker-entrypoint.sh` before running the Dagger pipeline.
  :::

Dagger performs the operations defined in the pipeline script, logging each operation to the console. This process will take some time. At the end of the process, the built container image is published on Docker Hub and a message similar to the one below appears in the console output:

```shell
Published to docker.io/.../my-memcached@sha256:692....
```

Browse to your Docker Hub registry to see the published Memcached container image.

## Conclusion

This tutorial introduced you to the Dagger Go SDK. By replacing a Dockerfile with native Go code, it demonstrated how the SDK contains everything you need to develop CI/CD pipelines in Go and run them on any OCI-compatible container runtime.

The advantage of this approach is that it allows you to use all the poweful native language features of Go, such as static typing, concurrency, programming structures such as loops and conditionals, and built-in testing, to create powerful CI/CD tooling for your project or organization.

Use the [SDK Reference](https://pkg.go.dev/dagger.io/dagger) to learn more about the Dagger Go SDK.
