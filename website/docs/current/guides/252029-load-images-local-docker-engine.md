---
slug: /252029/load-images-local-docker-engine
displayed_sidebar: "current"
category: "guides"
authors: ["Vikram Vaswani"]
tags: ["go", "python", "nodejs"]
date: "2023-03-31"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Load Container Images into a Local Docker Engine

## Introduction

There are two possible approaches to loading container images built with Dagger into a local Docker engine. This tutorial describes them both in detail, although only the first one is recommended.

## Requirements

This tutorial assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Approach 1: Use an exported tarball

This approach involves and exporting the container image to the host filesystem as a TAR file with Dagger, and then loading it into Docker. Here's an example:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/load-images-local-docker-engine/export/main.go
```

This code listing performs the following operations:

- It imports the Dagger client library.
- It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
- It uses the client's `Container().From()` method to initialize a new container from a base image (`nginx:1.23-alpine`). The additional `Platform` argument to the `Container()` method instructs Dagger to build for a specific architecture (`linux/amd64`). This method returns a `Container` representing an OCI-compatible container image.
- It uses the previous `Container` object's `WithNewFile()` method to create a new file at the NGINX web server root and return the result as a new `Container`.
- It uses the `Export()` method to write the final container image to the host filesystem as a TAR file at `/tmp/my-nginx.tar`.

</TabItem>
<TabItem value="Node.js">

```javascript file=./snippets/load-images-local-docker-engine/export/index.mjs
```

This code listing performs the following operations:

- It imports the Dagger client library.
- It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
- It uses the client's `container().from()` method to initialize a new container from a base image (`nginx:1.23-alpine`). The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture (`linux/amd64`). This method returns a `Container` representing an OCI-compatible container image.
- It uses the previous `Container` object's `withNewFile()` method to create a new file at the NGINX web server root and return the result as a new `Container`.
- It uses the `export()` method to write the final container image to the host filesystem as a TAR file at `/tmp/my-nginx.tar`.

</TabItem>
<TabItem value="Python">

```python file=./snippets/load-images-local-docker-engine/export/main.py
```

This code listing performs the following operations:

- It imports the Dagger client library.
- It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
- It uses the client's `container().from_()` method to initialize a new container from a base image (`nginx:1.23-alpine`). The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture (`linux/amd64`). This method returns a `Container` representing an OCI-compatible container image.
- It uses the previous `Container` object's `with_new_file()` method to create a new file at the NGINX web server root and return the result as a new `Container`.
- It uses the `export()` method to write the final container image to the host filesystem as a TAR file at `/tmp/my-nginx.tar`.

</TabItem>
</Tabs>

Once exported, the image can be imported into Docker with `docker load`:

```shell
docker load -i /tmp/my-nginx.tar
...
Loaded image ID: sha256:2e340...
```

Once imported, the image can be used like any other Docker image. For example, to run the sample image above, use the following `docker run` command:

```shell
docker run --rm --net=host 2e340...
```

## Approach 2: Use a local registry server

:::danger
This approach is significantly more complex than the previous one and is therefore not recommended. It is included only for documentation completeness.
:::

:::danger
The commands in this section deploy a local registry server without authentication or TLS. This is highly insecure and should only be used for local development, debugging and testing. Refer to the Docker documentation for details on [how to deploy a more secure and production-ready registry](https://docs.docker.com/registry/deploying/).
:::

This approach involves publishing the container image to a local registry, and then pulling from it as usual with Docker. Follow the steps below:

1. Deploy a local container registry with Docker. This local registry must be configured to run in the same network as the Dagger Engine container, and must use a host volume for the registry data.

  :::tip
  At this point, the local registry must run in the same network as the Dagger Engine container so that Dagger is able to communicate with it using the `localhost` or `127.0.0.1` network address.
  :::

  ```shell
  DAGGER_CONTAINER_NETWORK_NAME=`docker ps --filter "name=^dagger-engine-*" --format '{{.Names}}'`

  docker run -d --rm --name registry --network container:$DAGGER_CONTAINER_NETWORK_NAME -v /opt/docker-registry/data:/var/lib/registry registry:2
  ```

1. Create a Dagger pipeline to build and push a container image to the local registry:

  <Tabs groupId="language">
  <TabItem value="Go">

  ```go file=./snippets/load-images-local-docker-engine/push/main.go
  ```

  This code listing performs the following operations:
    - It imports the Dagger client library.
    - It creates a Dagger client with `Connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `Container().From()` method to initialize a new container from a base image (`nginx:1.23-alpine`). The additional `Platform` argument to the `Container()` method instructs Dagger to build for a specific architecture (`linux/amd64`). This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `WithNewFile()` method to create a new file at the NGINX web server root and return the result as a new `Container`.
    - It uses the `Publish()` method to publish the final container image to the local registry.

  </TabItem>
  <TabItem value="Node.js">

  ```javascript file=./snippets/load-images-local-docker-engine/push/index.mjs
  ```

  This code listing performs the following operations:
    - It imports the Dagger client library.
    - It creates a Dagger client with `connect()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `container().from()` method to initialize a new container from a base image (`nginx:1.23-alpine`). The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture (`linux/amd64`). This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `withNewFile()` method to create a new file at the NGINX web server root and return the result as a new `Container`.
    - It uses the `publish()` method to publish the final container image to the local registry.

  </TabItem>
  <TabItem value="Python">

  ```python file=./snippets/load-images-local-docker-engine/push/main.py
  ```

  This code listing performs the following operations:
    - It imports the Dagger client library.
    - It creates a Dagger client with `dagger.Connection()`. This client provides an interface for executing commands against the Dagger engine.
    - It uses the client's `container().from_()` method to initialize a new container from a base image (`nginx:1.23-alpine`). The additional `platform` argument to the `container()` method instructs Dagger to build for a specific architecture (`linux/amd64`). This method returns a `Container` representing an OCI-compatible container image.
    - It uses the previous `Container` object's `with_new_file()` method to create a new file at the NGINX web server root and return the result as a new `Container`.
    - It uses the `publish()` method to publish the final container image to the local registry.

  </TabItem>
  </Tabs>

1. Run the Dagger pipeline:

  <Tabs groupId="language">
  <TabItem value="Go">

  ```shell
  dagger run go run ci/main.go
  ```

  </TabItem>
  <TabItem value="Node.js">

  ```shell
  dagger run node ci/index.mjs
  ```

  </TabItem>
  <TabItem value="Python">

  ```shell
  dagger run python ci/main.py
  ```

  </TabItem>
  </Tabs>

  The `dagger run` command executes the script in a Dagger session and displays live progress. At the end of the process, the built container is pushed to the local registry and a message similar to the one below appears in the console output:

  ```shell
  Published at: 127.0.0.1:5000/my-nginx:1.0@sha256:c59a...
  ```

1. Stop the local registry, then restart it after detaching it from the Dagger Engine container network and publishing the registry server port so that it can be used from the Docker host.

  :::tip
  At this point, the local registry must run in the same network as the host so that you can use it via the `localhost` or `127.0.0.1` network address.
  :::

  ```shell
  docker stop registry

  docker run -d --rm --name registry -p 5000:5000 -v /opt/docker-registry/data:/var/lib/registry  registry:2
  ```

The image can now be pulled from the registry and used like any other Docker image. For example, to run the sample image above, use the following `docker run` command:

```shell
docker run --net=host 127.0.0.1:5000/my-nginx:1.0@sha256:c59a...
```

## Conclusion

This tutorial walked you through two approaches to building container images with Dagger purely for local use: exporting the image as a tarball and loading it into Docker, or pushing the image to a local container registry.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
