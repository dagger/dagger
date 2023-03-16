---
slug: /205271/replace-dockerfile
displayed_sidebar: 'current'
category: "guides"
tags: ["go", "python", "nodejs"]
authors: ["Kyle Penfound", "Vikram Vaswani"]
date: "2023-01-07"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Replace a Dockerfile with Go (or Python, or Node.js)

## Introduction

This guide explains how to use a Dagger SDK to perform all the same operations that you would typically perform with a Dockerfile, except using Go, Python or Node.js. You will learn how to:

- Create a Dagger client
- Write a Dagger pipeline to:
  - Configure a container with all required dependencies and environment variables
  - Download and build the application source code in the container
  - Set the container entrypoint
  - Publish the built container image to Docker Hub
- Test the Dagger pipeline locally

## Requirements

This guide assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
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

The Dagger SDK enables you to develop a CI/CD pipeline in one of the supported languages (Go, Python or Node.js) to achieve the same result as using a Dockerfile.

<Tabs groupId="language">
<TabItem value="Go">

To see how this works, add the following code to your Go module as `main.go`. Replace the DOCKER-HUB-USERNAME placeholder with your Docker Hub username.

```go file=./snippets/replace-dockerfile/main.go
```

There's a lot going on here, so let's step through it in detail:

- The Go CI pipeline imports the Dagger SDK and defines a `main()` function. The `main()` function creates a Dagger client with `dagger.Connect()`. This client provides an interface for executing commands against the Dagger engine.
- It initializes a new container from a base image with the client's `Container().From()` method and returns a new `Container` struct. In this case, the base image is the `alpine:3.17` image.
- It calls the `WithExec()` method to define the `adduser`, `addgroup` and `apk add` commands for execution, and the `WithEnvVariable()` method to set the `MEMCACHED_VERSION` and `MEMCACHED_SHA1` container environment variables.
- It calls a custom `setDependencies()` function, which internally uses `WithExec()` to define the `apk add` command that installs all the required dependencies to build and test Memcached in the container.
- It calls a custom `downloadMemcached()` function, which internally uses `WithExec()` to define the `wget`, `tar` and related commands required to download, verify and extract the Memcached source code archive in the container at the `/usr/src/memcached` container path.
- It calls a custom `buildMemcached()` function, which internally uses `WithExec()` to define the `configure` and `make` commands required to build, test and install Memcached in the container. The `buildMemcached()` function also takes care of deleting the source code directory at `/usr/src/memcached` in the container and executing `memcached -V` to output the version string to the console.
- It updates the container filesystem to include the entrypoint script from the host using `WithFile()` and specifies it as the command to be executed when the container runs using `WithEntrypoint()`.
- Finally, it calls the `Container.Publish()` method, which executes the entire pipeline described above and publishes the resulting container image to Docker Hub.

</TabItem>
<TabItem value="Python">

To see how this works, create a file named `main.py` and add the following code to it. Replace the DOCKER-HUB-USERNAME placeholder with your Docker Hub username.

```python file=./snippets/replace-dockerfile/main.py
```

There's a lot going on here, so let's step through it in detail:

- The Python CI pipeline imports the Dagger SDK and defines a `main()` function. The `main()` function creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
- It initializes a new container from a base image with the client's `container().from_()` method and returns a new `Container`. In this case, the base image is the `alpine:3.17` image.
- It calls the `with_exec()` method to define the `adduser`, `addgroup` and `apk add` commands for execution, and the `with_env_variable()` method to set the `MEMCACHED_VERSION` and `MEMCACHED_SHA1` container environment variables.
- It calls a custom `set_dependencies()` function, which internally uses `with_exec()` to define the `apk add` command that installs all the required dependencies to build and test Memcached in the container.
- It calls a custom `download_memcached()` function, which internally uses `with_exec()` to define the `wget`, `tar` and related commands required to download, verify and extract the Memcached source code archive in the container at the `/usr/src/memcached` container path.
- It calls a custom `build_memcached()` function, which internally uses `with_exec()` to define the `configure` and `make` commands required to build, test and install Memcached in the container. The `build_memcached()` function also takes care of deleting the source code directory at `/usr/src/memcached` in the container and executing `memcached -V` to output the version string to the console.
- It updates the container filesystem to include the entrypoint script from the host using `with_file()` and specifies it as the command to be executed when the container runs using `with_entrypoint()`. The `with_default_args()` methods specifies the entrypoint arguments.
- Finally, it calls the `Container.publish()` method, which executes the entire pipeline described above and publishes the resulting container image to Docker Hub.

</TabItem>
<TabItem value="Node.js">

To see how this works, create a file named `index.mjs` and add the following code to it. Replace the DOCKER-HUB-USERNAME placeholder with your Docker Hub username.

```javascript file=./snippets/replace-dockerfile/index.mjs
```

There's a lot going on here, so let's step through it in detail:

- The Node.js CI pipeline imports the Dagger SDK and creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
- It initializes a new container from a base image with the client's `container().from()` method and returns a new `Container` object. In this case, the base image is the `alpine:3.17` image.
- It calls the `withExec()` method to define the `adduser`, `addgroup` and `apk add` commands for execution, and the `withEnvVariable()` method to set the `MEMCACHED_VERSION` and `MEMCACHED_SHA1` container environment variables.
- It calls a custom `setDependencies()` function, which internally uses `withExec()` to define the `apk add` command that installs all the required dependencies to build and test Memcached in the container.
- It calls a custom `downloadMemcached()` function, which internally uses `withExec()` to define the `wget`, `tar` and related commands required to download, verify and extract the Memcached source code archive in the container at the `/usr/src/memcached` container path.
- It calls a custom `buildMemcached()` function, which internally uses `withExec()` to define the `configure` and `make` commands required to build, test and install Memcached in the container. The `buildMemcached()` function also takes care of deleting the source code directory at `/usr/src/memcached` in the container and executing `memcached -V` to output the version string to the console.
- It updates the container filesystem to include the entrypoint script from the host using `withFile()` and specifies it as the command to be executed when the container runs using `withEntrypoint()`. The `withDefaultArgs()` methods specifies the entrypoint arguments.
- Finally, it calls the `Container.publish()` method, which executes the entire pipeline described above and publishes the resulting container image to Docker Hub.

</TabItem>
</Tabs>

:::warning
Like the source Dockerfile, this pipeline assumes that the entrypoint script exists in the current  working directory on the host as `docker-entrypoint.sh`. You can either create a custom entrypoint script, or use the [entrypoint script from the Docker Hub Memcached image repository](https://github.com/docker-library/memcached/blob/1e3f84629bb2ab9975235401c716c1e00563fa82/alpine/docker-entrypoint.sh).
:::

## Step 3: Test the Dagger pipeline

Test the Dagger pipeline as follows:

<Tabs groupId="language">
<TabItem value="Go">

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

</TabItem>
<TabItem value="Python">

1. Log in to Docker on the host:

  ```shell
  docker login
  ```

  :::info
  This step is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
  :::

1. Run the pipeline:

  ```shell
  python main.py
  ```

</TabItem>
<TabItem value="Node.js">

1. Log in to Docker on the host:

  ```shell
  docker login
  ```

  :::info
  This step is necessary because Dagger relies on the host's Docker credentials and authorizations when publishing to remote registries.
  :::

1. Run the pipeline:

  ```shell
  node index.mjs
  ```

</TabItem>
</Tabs>

:::warning
Verify that you have an entrypoint script on the host at `./docker-entrypoint.sh` before running the Dagger pipeline.
:::

Dagger performs the operations defined in the pipeline script, logging each operation to the console. This process will take some time. At the end of the process, the built container image is published on Docker Hub and a message similar to the one below appears in the console output:

```shell
Published to docker.io/.../my-memcached@sha256:692....
```

Browse to your Docker Hub registry to see the published Memcached container image.

## Conclusion

This tutorial introduced you to the Dagger SDKs. By replacing a Dockerfile with native code, it demonstrated how Dagger SDKs contain everything you need to develop CI/CD pipelines in your favorite language and run them on any OCI-compatible container runtime.

The advantage of this approach is that it allows you to use powerful native language features, such as (where applicable) static typing, concurrency, programming structures such as loops and conditionals, and built-in testing, to create powerful CI/CD tooling for your project or organization.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
