---
slug: /757394/use-services
displayed_sidebar: "current"
category: "guides"
tags: ["go", "python", "nodejs"]
authors: ["Alex Suraci"]
date: "2023-10-12"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import Embed from '@site/src/components/atoms/embed.js'

# Use Services in Dagger

:::warning
Dagger v0.9.0 includes a breaking change for binding service containers. The `Container.withServiceBinding` API now takes a `Service` instead of a `Container`, so you must call `Container.asService` on its argument. See the section on [binding service containers](#bind-service-containers) for examples.
:::

## Introduction

Dagger [v0.4.0](https://github.com/dagger/dagger/releases/tag/v0.4.0) introduced service containers, aka container-to-container networking. This feature enables users to spin up additional long-running services (as containers) and communicate with those services from their Dagger pipelines. Dagger v0.9.0 further improved this implementation, enabling support for container-to-host networking and host-to-container networking.

Some common use cases for services and service containers are:

- Run a test database
- Run end-to-end integration tests
- Run sidecar services

This guide teaches you the basics of using services and service containers in Dagger.

## Requirements

This guide assumes that:

- You have a Go, Python, or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/), or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md), or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Key concepts

Dagger's service containers have the following characteristics:

- Each service container has a canonical, content-addressed hostname and an optional set of exposed ports
- Service containers can bind to other containers as services

Service containers come with the following built-in features:

- Service containers are started just-in-time, de-duplicated, and stopped when no longer needed
- Service containers are health checked prior to running clients
- Service containers are given an alias for the client container to use as its hostname

## Working with service hostnames and ports

Each service container has a canonical, content-addressed hostname and an optional set of exposed ports.

<Tabs groupId="language">
<TabItem value="Go">

You can query a service container's canonical hostname by calling the `Service.Hostname()` SDK method.

```go file=./snippets/use-services/use-hostnames/main.go
```

</TabItem>
<TabItem value="Node.js">

You can query a service container's canonical hostname by calling the `Service.hostname()` SDK method.

```typescript file=./snippets/use-services/use-hostnames/index.ts
```

</TabItem>
<TabItem value="Python">

You can query a service container's canonical hostname by calling the `Service.hostname()` SDK method.

```python file=./snippets/use-services/use-hostnames/main.py
```

</TabItem>
</Tabs>

You can also define the ports on which the service container will listen. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.

<Tabs groupId="language">
<TabItem value="Go">

This example uses the `WithExposedPort()` method to set ports on which the service container will listen. Note also the `Endpoint()` helper method, which returns an address pointing to a particular port, optionally with a URL scheme. You can either specify a port or let Dagger pick the first exposed port.

```go file=./snippets/use-services/expose-ports/main.go
```

</TabItem>
<TabItem value="Node.js">

This example uses the `withExposedPort()` method to set ports on which the service container will listen. Note also the `endpoint()` helper method, which returns an address pointing to a particular port, optionally with a URL scheme. You can either specify a port or let Dagger pick the first exposed port.

```typescript file=./snippets/use-services/expose-ports/index.ts
```

</TabItem>
<TabItem value="Python">

This example uses the `with_exposed_port()` method to set ports on which the service container will listen. Note also the `endpoint()` helper method, which returns an address pointing to a particular port, optionally with a URL scheme. You can either specify a port or let Dagger pick the first exposed port.

```python file=./snippets/use-services/expose-ports/main.py
```

</TabItem>
</Tabs>

In practice, you are more likely to set your own hostname aliases with service bindings, which are covered in the next section.

## Working with services

You can use services in Dagger in three ways:

- [Bind service containers](#bind-service-containers)
- [Expose service containers to the host](#expose-service-containers-to-the-host)
- [Expose host services to client containers](#expose-host-services-to-containers)

### Bind service containers

:::warning
Dagger v0.9.0 includes a breaking change for binding service containers. The examples below have been updated.
:::

Dagger enables users to bind a service running in a container to another (client) container with an alias that the client container can use as a hostname to communicate with the service.

Binding a service to a container or the host creates a dependency in your Dagger pipeline. The service container needs to be running when the client container runs. The bound service container is started automatically whenever its client container runs.

Here's an example of an HTTP service automatically starting in tandem with a client container. The service binding enables the client container to access the HTTP service using the alias `www`.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/bind-service-containers-1/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/bind-service-containers-1/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/bind-service-containers-1/main.py
```

</TabItem>
</Tabs>

:::tip
Services in service containers should be configured to listen on the IP address 0.0.0.0 instead of 127.0.0.1. This is because 127.0.0.1 is only reachable within the container itself, so other services (including the Dagger health check) won't be able to connect to it. Using 0.0.0.0 allows connections to and from any IP address, including the container's private IP address in the Dagger network.
:::

When a service is bound to a container, it also conveys to any outputs of that container, such as files or directories. The service will be started whenever the output is used, so you can also do things like this:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/bind-service-containers-2/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/bind-service-containers-2/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/bind-service-containers-2/main.py
```

</TabItem>
</Tabs>

### Expose service containers to the host

Starting with Dagger v0.9.0, you can expose service container ports directly to the host. This enables clients on the host to communicate with services running in Dagger.

One use case is for testing, where you need to be able to spin up ephemeral databases to run tests against. You might also use this to access a web UI in a browser on your desktop.

Here's an example of how to use Dagger services on the host. In this example, the host makes HTTP requests to an HTTP service running in a container.

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/expose-service-containers-to-host/main.go
```

The Dagger pipeline calls `Host.Tunnel(service).Start()` to create a new `Service`. By default, Dagger lets the operating system randomly choose which port to use based on the available ports on the host's side. Finally, a call to `Service.Endpoint()` gets the final address with whichever port is bound.

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/expose-service-containers-to-host/index.ts
```

The Dagger pipeline calls `Host.Tunnel(service).Start()` to create a new `Service`. By default, Dagger lets the operating system randomly choose which port to use based on the available ports on the host's side. Finally, a call to `Service.Endpoint()` gets the final address with whichever port is bound.

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/expose-service-containers-to-host/main.py
```

The Dagger pipeline calls `host.tunnel(service).start()` to create a new `Service`. By default, Dagger lets the operating system randomly choose which port to use based on the available ports on the host's side. Finally, a call to `Service.endpoint()` gets the final address with whichever port is bound.

</TabItem>
</Tabs>

### Expose host services to containers

Starting with Dagger v0.9.0, you can bind containers to host services. This enables client containers in Dagger pipelines to communicate with services running on the host.

:::note
This implies that a service is already listening on a port on the host, out-of-band of Dagger.
:::

Here's an example of how a container running in a Dagger pipeline can access a service on the host. In this example, a container in a Dagger pipeline queries a MariaDB database service running on the host. Before running the pipeline, use the following command to start a MariaDB database service on the host:

```shell
docker run --rm --detach -p 3306:3306 --name my-mariadb --env MARIADB_ROOT_PASSWORD=secret  mariadb:10.11.2
```

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/expose-host-services-to-container/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/expose-host-services-to-container/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/expose-host-services-to-container/main.py
```

</TabItem>
</Tabs>

This Dagger pipeline creates a service that proxies traffic through the host to the configured port. It then sets the service binding on the client container to the host.

:::note
To connect client containers to Unix sockets on the host instead of TCP, see `Host.unixSocket`.
:::

## Persist service state

Another way to avoid relying on the grace period is to use a cache volume to persist a service's data, as in the following example:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/persist-service-state/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/persist-service-state/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/persist-service-state/main.py
```

</TabItem>
</Tabs>

:::info
This example uses Redis's `SAVE` command to ensure data is synced. By default, Redis flushes data to disk periodically.
:::

## Start and stop services

Services are designed to be expressed as a Directed Acyclic Graph (DAG) with explicit bindings allowing services to be started lazily, just like every other DAG node. But sometimes, you may need to explicitly manage the lifecycle. Starting with Dagger v0.9.0, you can explicitly start and stop services in your pipelines.

Here's an example which demonstrates explicitly starting a Docker daemon for use in a test suite:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/start-stop-services/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/start-stop-services/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/start-stop-services/main.py
```

</TabItem>
</Tabs>

## Example: MariaDB database service for application tests

The following example demonstrates service containers in action, by creating and binding a MariaDB database service container for use in application unit/integration testing.

The application used in this example is [Drupal](https://www.drupal.org/), a popular open-source PHP CMS. Drupal includes a large number of unit tests, including tests which require an active database connection. All Drupal 10.x tests are written and executed using the [PHPUnit](https://phpunit.de/) testing framework. Read more about [running PHPUnit tests in Drupal](https://www.drupal.org/docs/automated-testing/phpunit-in-drupal/running-phpunit-tests).

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/use-db-service/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/use-db-service/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/use-db-service/main.py
```

</TabItem>
</Tabs>

This example begins by creating a MariaDB service container and initializing a new MariaDB database. It then creates a Drupal container (client) and installs required dependencies into it. Next, it adds a binding for the MariaDB service (`db`) in the Drupal container and sets a container environment variable (`SIMPLETEST_DB`) with the database DSN. Finally, it runs Drupal's kernel tests (which [require a database connection](https://www.drupal.org/docs/automated-testing/phpunit-in-drupal/running-phpunit-tests#non-unit-tests)) using PHPUnit and prints the test summary to the console.

:::tip
Explicitly specifying the service container port with `WithExposedPort()` (Go), `withExposedPort()` (Node.js) or `with_exposed_port()` (Python) is particularly important here. Without it, Dagger will start the service container and immediately allow access to service clients. With it, Dagger will wait for the service to be listening first.
:::

## Reference: How service binding works for container services

If you're not interested in what's happening in the background, you can skip this section and just trust that services are running when they need to be. If you're interested in the theory, keep reading.

Consider this example:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/test-service-lifecycle-1/main.go
```

Here's what happens on the last line:

1. The client requests the `ping` container's stdout, which requires the container to run.
1. Dagger sees that the `ping` container has a service binding, `redisSrv`.
1. Dagger starts the `redisSrv` container, which recurses into this same process.
1. Dagger waits for health checks to pass against `redisSrv`.
1. Dagger runs the `ping` container with the `redis-srv` alias magically added to `/etc/hosts`.

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/test-service-lifecycle-1/index.ts
```

Here's what happens on the last line:

1. The client requests the `ping` container's stdout, which requires the container to run.
1. Dagger sees that the `ping` container has a service binding, `redisSrv`.
1. Dagger starts the `redisSrv` container, which recurses into this same process.
1. Dagger waits for health checks to pass against `redisSrv`.
1. Dagger runs the `ping` container with the `redis-srv` alias magically added to `/etc/hosts`.

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/test-service-lifecycle-1/main.py
```

Here's what happens on the last line:

1. The client requests the `ping` container's stdout, which requires the container to run.
1. Dagger sees that the `ping` container has a service binding, `redis_srv`.
1. Dagger starts the `redis_srv` container, which recurses into this same process.
1. Dagger waits for health checks to pass against `redis_srv`.
1. Dagger runs the `ping` container with the `redis-srv` alias magically added to `/etc/hosts`.

</TabItem>
</Tabs>

:::note
Dagger cancels each service run after a 10 second grace period to avoid frequent restarts.
:::

Services are based on containers, but they run a little differently. Whereas regular containers in Dagger are de-duplicated across the entire Dagger Engine, service containers are only de-duplicated within a Dagger client session. This means that if you run separate Dagger sessions that use the exact same services, they will  each get their own "instance" of the service. This process is carefully tuned to preserve caching at each client call-site, while prohibiting "cross-talk" from one Dagger session's client to another Dagger session's service.

Content-addressed services are very convenient. You don't have to come up with names and maintain instances of services; just use them by value. You also don't have to manage the state of the service; you can just trust that it will be running when needed and stopped when not.

:::tip
If you need multiple instances of a service, just attach something unique to each one, such as an instance ID.
:::

Here's a more detailed client-server example of running commands against a Redis service:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/test-service-lifecycle-2/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/test-service-lifecycle-2/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/test-service-lifecycle-2/main.py
```

</TabItem>
</Tabs>

Note that this example relies on the 10-second grace period, which you should try to avoid. It would be better to chain both commands together, which ensures that the service stays running for both:

<Tabs groupId="language">
<TabItem value="Go">

```go file=./snippets/use-services/test-service-lifecycle-3/main.go
```

</TabItem>
<TabItem value="Node.js">

```typescript file=./snippets/use-services/test-service-lifecycle-3/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/test-service-lifecycle-3/main.py
```

</TabItem>
</Tabs>

:::note
Depending on the 10-second grace period is risky because there are many factors which could cause a 10-second delay between calls to Dagger, such as excessive CPU load, high network latency between the client and Dagger, or Dagger operations that require a variable amount of time to process.
:::

## Conclusion

This tutorial walked you through the basics of using service containers with Dagger. It explained how container-to-container networking and the service lifecycle is implemented in Dagger. It also provided examples of exposing service containers to the host, exposiing host services to containers and persisting service state using Dagger.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
