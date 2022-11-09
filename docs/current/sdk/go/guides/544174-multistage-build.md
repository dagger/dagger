---
slug: /544174/multistage-build
---

# Use Dagger with Multi-stage Container Builds

A common practice when building containers with Dockerfiles is something called a multistage build. This means within the `docker build`, your application is compiled in a context which has tools that are required for building your app, but not necessarily required for running the application. To reduce the number of dependencies in the image you actually run the application on, the compiled application is copied to a different base image which only has the required components to run the application. More information about multistage builds can be found in [this guide](https://docs.docker.com/build/building/multi-stage/) from Docker.

This document shows how multistage builds are created using the Go SDK.

## Introduction

## Requirements

This guide assumes that:

- You have a Go development environment with Go 1.15 or later. If not, [download and install Go](https://go.dev/doc/install).
- You are familiar with the basics of the Go SDK and have it installed. If not, read the [Go SDK guide](../959738-get-started.md) and the [Go SDK installation instructions](../371491-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Example

The following code snippet demonstrates a multistage build with the Go SDK. Below the snippet, we'll break down exactly what's happening

```go file=../snippets/multistage-build/main.go

```

The beginning of the file starts by creating a Dagger client and loading the project which is going to be built. Once we have a reference to the project, we build the application by using the `golang:latest` image to mount the source directory, set `CGO_ENABLED=` since the binary will be published on Alpine, and then execute `go build`.

Next, in the highlighted section, the multistage build is achieved by taking the build artifact from the build stage and putting it in an Alpine image.

- Create a new container which will be used as the runtime image. This is using `From("alpine")`
- Include the build artifact from the builder image in the new container by replacing the container's filesystem with the original filesystem plus the build artifact
- Set the entrypoint to our application so that the application is executed by default when the container is run

The final optimized image can now be pushed to a registry and deployed!
