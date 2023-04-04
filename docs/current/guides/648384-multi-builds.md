---
slug: /648384/multi-builds
displayed_sidebar: "current"
category: "guides"
tags: ["python", "go", "nodejs"]
authors: ["Helder Correia"]
date: "2022-11-22"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Create a Multi-Build CI Pipeline

## Introduction

The Dagger SDKs makes it easy to build an application for multiple OS and architecture combinations. This guide provides a working example of a CI tool that performs this task.

## Requirements

This guide assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have an application that you wish to build. This guide assumes a Go application, but you can use an application of your choice.

:::tip
Dagger pipelines are executed as standard OCI containers. This portability enables you to do very powerful things. For example, if you're a Python developer, you can use the Python SDK to create a pipeline (written in Python) that builds an application written in a different language (Go) without needing to learn that language.
:::

## Example

Assume that the Go application to be built is stored in the current directory on the host. The following code listing demonstrates how to build this Go application for multiple OS and architecture combinations using the Dagger SDKs.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./snippets/multi-builds/main.go
```

This code listing does the following:

- It defines the build matrix, consisting of two OSs (`darwin` and `linux`) and two architectures (`amd64` and `arm64`).
- It creates a Dagger client with `Connect()`.
- It uses the client's `Host().Directory(".")` method to obtain a reference to the current directory on the host. This reference is stored in the `src` variable.
- It uses the client's `Container().From()` method to initialize a new container from a base image. This base image contains all the tooling needed to build the application - in this case, the `golang:latest` image. This `From()` method returns a new `Container` class with the results.
- It uses the `Container.WithMountedDirectory()` method to mount the host directory into the container at the `/src` mount point.
- It uses the `Container.WithWorkdir()` method to set the working directory in the container.
- It iterates over the build matrix, creating a directory in the container for each OS/architecture combination and building the Go application for each such combination. The Go build process is instructed via the `GOOS` and `GOARCH` build variables, which are reset for each case via the `Container.WithEnvVariable()` method.
- It obtains a reference to the build output directory in the container with the `WithDirectory()` method, and then uses the `Directory.Export()` method to write the build directory from the container to the host.

</TabItem>
<TabItem value="Node.js">

```javascript file=./snippets/multi-builds/index.mjs
```

This code listing does the following:

- It defines the build matrix, consisting of two OSs (`darwin` and `linux`) and two architectures (`amd64` and `arm64`).
- It creates a Dagger client with `connect()`.
- It uses the client's `host().directory(".")` method to obtain a reference to the current directory on the host. This reference is stored in the `src` variable.
- It uses the client's `container().from()` method to initialize a new container from a base image. This base image contains all the tooling needed to build the application - in this case, the `golang:latest` image. This `from()` method returns a new `Container` class with the results.
- It uses the `Container.withMountedDirectory()` method to mount the host directory into the container at the `/src` mount point.
- It uses the `Container.withWorkdir()` method to set the working directory in the container.
- It iterates over the build matrix, creating a directory in the container for each OS/architecture combination and building the Go application for each such combination. The Go build process is instructed via the `GOOS` and `GOARCH` build variables, which are reset for each case via the `Container.withEnvVariable()` method.
- It obtains a reference to the build output directory in the container with the `withDirectory()` method, and then uses the `Directory.export()` method to write the build directory from the container to the host.

</TabItem>
<TabItem value="Python">

```python file=./snippets/multi-builds/main.py
```

This code listing does the following:

- It defines the build matrix, consisting of two OSs (`darwin` and `linux`) and two architectures (`amd64` and `arm64`).
- It creates a Dagger client with `dagger.Connection()`.
- It uses the client's `host().directory(".")` method to obtain a reference to the current directory on the host. This reference is stored in the `src` variable.
- It uses the client's `container().from_()` method to initialize a new container from a base image. This base image contains all the tooling needed to build the application - in this case, the `golang:latest` image. This `from_()` method returns a new `Container` class with the results.
- It uses the `Container.with_mounted_directory()` method to mount the host directory into the container at the `/src` mount point.
- It uses the `Container.with_workdir()` method to set the working directory in the container.
- It iterates over the build matrix, creating a directory in the container for each OS/architecture combination and building the Go application for each such combination. The Go build process is instructed via the `GOOS` and `GOARCH` build variables, which are reset for each case via the `Container.with_env_variable()` method.
- It obtains a reference to the build output directory in the container with the `with_directory()` method, and then uses the `Directory.export()` method to write the build directory from the container to the host.

</TabItem>
</Tabs>

## Conclusion

This guide showed you how to build an application for multiple OS and architecture combinations with Dagger.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
