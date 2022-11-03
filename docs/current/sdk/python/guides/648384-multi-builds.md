---
slug: /sdk/python/648384/multi-builds
displayed_sidebar: "current"
---

# Create a Multi-Build CI Pipeline

## Introduction

The Dagger Python SDK makes it easy to build an application for multiple OS and architecture combinations. This guide provides a working example of a Python CI tool that performs this task.

## Requirements

This guide assumes that:

- You have a Python development environment with Python 3.10 or later. If not, install [Python](https://www.python.org/downloads/).
- You are familiar with the basics of the Python SDK and have it installed. If not, read the [Python SDK guide](../628797-get-started.md) and the [Python SDK installation instructions](../866944-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have an application that you wish to build. This guide assumes a Go application, but you can use an application of your choice.

:::tip
Dagger pipelines are executed as standard OCI containers. This portability enables you to do very powerful things. For example, if you're a Python developer, you can use the Python SDK to create a pipeline (written in Python) that builds an application written in a different language (Go) without needing to learn that language.
:::

## Example

Assume that the Go application to be built is stored in the current directory on the host. The following code listing demonstrates how to build this Go application for multiple OS and architecture combinations using the Python SDK.

```python file=../snippets/multi-builds/build.py
```

The `build()` function does the following:

- It defines the build matrix, consisting of two OSs (`darwin` and `linux`) and two architectures (`amd64` and `arm64`).
- It creates a Dagger client with `dagger.Connection()`.
- It uses the client's `host().workdir().id()` method to obtain a reference to the current directory on the host. This reference is stored in the `src_id` variable.
- It uses the client's `container().from_()` method to initialize a new container from a base image. This base image contains all the tooling needed to build the application - in this case, the `golang:latest` image. This `from_()` method returns a new `Container` class with the results.
- It uses the `Container.with_mounted_directory()` method to mount the host directory into the container at the `/src` mount point.
- It uses the `Container.with_workdir()` method to set the working directory in the container.
- It iterates over the build matrix, creating a directory in the container for each OS/architecture combination and building the Go application for each such combination. The Go build process is instructed via the `GOOS` and `GOARCH` build variables, which are reset for each case via the `Container.with_env_variable()` method.
- It obtains a reference to the build output directory in the container with the `with_directory()` method, and then uses the `Directory.export()` method to write the build directory from the container to the host.
