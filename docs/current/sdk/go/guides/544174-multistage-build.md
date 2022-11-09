---
slug: /544174/multistage-build
---

# Use Dagger with Multi-stage Container Builds

Multi-stage builds are a common practice when building containers with Docker. 

- First, your application is compiled in a context which has tools that are required for building the application, but not necessarily required for running it. 
- Next, to reduce the number of dependencies and hence the size of the image, the compiled application is copied to a different base image which only has the required components to run the application. 

[Learn more about multi-stage builds in the Docker documentation](https://docs.docker.com/build/building/multi-stage/).

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

Next, in the highlighted section, the multi-stage build is achieved by transferring the build artifact from the builder image to a runtime image based on `alpine`. The steps are:

- Create a new container image which will be used as the runtime image, using `From("alpine")`.
- Include the build artifact from the builder image in the new container by replacing the container's filesystem with the original filesystem plus the build artifact
- Set the container entrypoint to the application so that it is executed by default when the container runs.

The final optimized image can now be pushed to a registry and deployed!
