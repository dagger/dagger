---
slug: /757394/use-service-containers
displayed_sidebar: "current"
category: "guides"
tags: ["go", "python", "nodejs"]
authors: ["Alex Suraci"]
date: "2023-03-09"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import Embed from '@site/src/components/atoms/embed.js'

# Use Service Containers in Dagger

## Introduction

Dagger [v0.4.0](https://github.com/dagger/dagger/releases/tag/v0.4.0) introduced service containers, aka container-to-container networking. This feature enables users to spin up additional long-running services (as containers) and communicate with those services from their Dagger pipelines.

Some common use cases for service containers are:

- Run a test database
- Run end-to-end integration tests
- Run sidecar services

This tutorial teaches you the basics of using service containers in Dagger.

## Requirements

This tutorial assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Key concepts

Dagger's service containers have the following characteristics:

- Each service container has its own network namespace and IP address
- Each service container has a unique, deterministic DNS address
- Service containers can expose ports and endpoints
- Service containers can bind other containers as services

Service containers come with the following built-in features:

- Service containers are started just-in-time, de-duplicated, and stopped when no longer needed
- Service containers are health checked prior to running clients
- Service containers are given an alias for the client container to use as its hostname

## Use hostnames

Service containers run in a bridge network. Each container has its own IP address that other containers can reach. Here's a simple example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="blm2lRNoYzE" />

</TabItem>
<TabItem value="Node.js">

<Embed id="fR2OAVbNUVH" />

</TabItem>
<TabItem value="Python">

<Embed id="bhx9i4LCcD9" />

</TabItem>
</Tabs>

Service containers never use IP addresses to reach each other directly. IP addresses are ephemeral, so doing so would nullify the cache. Instead, Dagger gives each container a unique but deterministic hostname, which doubles as a DNS address. Here's an example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="H5Eb0Hs7JMd" />

</TabItem>
<TabItem value="Node.js">

<Embed id="Bpu7I8URtpg" />

</TabItem>
<TabItem value="Python">

<Embed id="2qLVfgdsnI6" />

</TabItem>
</Tabs>

This hash value is derived from the same value that determines whether an operation is a cache hit in Buildkit: the vertex digest.

To get a container's address, you wouldn't normally run the `hostname` command, because you'd just be getting the hostname of a container that runs `hostname`, which isn't very helpful. Instead, you would use the `Hostname()` (Go) or `hostname()` (Python and Node.js) SDK method, which returns a domain name reachable by other containers:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="cwmeT7388mg" />

</TabItem>
<TabItem value="Node.js">

<Embed id="uCP-rb3rLeK" />

</TabItem>
<TabItem value="Python">

<Embed id="YGkCbitVTYY" />

</TabItem>
</Tabs>

In practice, you are more likely to use aliases with service bindings or endpoints, which are covered in the next section.

## Expose ports

Dagger offers two methods to work with service ports:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

- Use the `WithExposedPort()` method to set ports that the service container will listen on. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.
- Use the `Endpoint()` method to create a string address to a service container's port. You can either specify a port or let Dagger pick the first exposed port.

Here's an example:

<Embed id="kDBfYoh2uau" />

</TabItem>
<TabItem value="Node.js">

- Use the `withExposedPort()` method to set ports that the service container will listen on. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.
- Use the `endpoint()` method to create a string address to a service container's port. You can either specify a port or let Dagger pick the first exposed port.

Here's an example:

<Embed id="cx-3lzMDn5i" />

</TabItem>
<TabItem value="Python">

- Use the `with_exposed_port()` method to set ports that the service container will listen on. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.
- Use the `endpoint()` method to create a string address to a service container's port. You can either specify a port or let Dagger pick the first exposed port.

Here's an example:

<Embed id="OPUGXdIujRC" />

</TabItem>
</Tabs>

## Bind services

Dagger enables users to bind a service container to a client container with an alias (such as `redis`) that the client container can use as a hostname.

Binding a service to a container expresses a dependency: the service container needs to be running when the client container runs. The bound service container is started automatically whenever its client container runs.

Here's an example of an HTTP service automatically starting in tandem with a client container. The service binding enables the client container to access the HTTP service using the alias `www`.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="pQoE-5_0Ghg" />

</TabItem>
<TabItem value="Node.js">

<Embed id="VIFYGYc8YRN" />

</TabItem>
<TabItem value="Python">

<Embed id="FkPqDoW3Oo-" />

</TabItem>
</Tabs>

:::tip
Services in service containers should be configured to listen on the IP address 0.0.0.0 instead of 127.0.0.1. This is because 127.0.0.1 is only reachable within the container itself, so other services (including the Dagger health check) won't be able to connect to it. Using 0.0.0.0 allows connections to and from any IP address, including the container's private IP address in the Dagger network.
:::

When a service is bound to a container, it also conveys to any outputs of that container, such as files or directories. The service will be started whenever the output is used, so you can also do things like this:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="Qoljk5SEPuu" />

</TabItem>
<TabItem value="Node.js">

<Embed id="RhA4m6ji1js" />

</TabItem>
<TabItem value="Python">

<Embed id="WG0Pqr49pKK" />

</TabItem>
</Tabs>

## Understand the service lifecycle

If you're not interested in what's happening in the background, you can skip this section and just trust that services are running when they need to be. If you're interested in the theory, keep reading.

Consider this example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="JwuCvswjsEM" />

Here's what happens on the last line:

1. The client requests the `ping` container's stdout, which requires the container to run.
1. Dagger sees that the `ping` container has a service binding, `redisSrv`.
1. Dagger starts the `redisSrv` container, which recurses into this same process.
1. Dagger waits for health checks to pass against `redisSrv`.
1. Dagger runs the `ping` container with the `redis-srv` alias magically added to `/etc/hosts`.

</TabItem>
<TabItem value="Node.js">

<Embed id="WRo9QMK9GKZ" />

Here's what happens on the last line:

1. The client requests the `ping` container's stdout, which requires the container to run.
1. Dagger sees that the `ping` container has a service binding, `redisSrv`.
1. Dagger starts the `redisSrv` container, which recurses into this same process.
1. Dagger waits for health checks to pass against `redisSrv`.
1. Dagger runs the `ping` container with the `redis-srv` alias magically added to `/etc/hosts`.

</TabItem>
<TabItem value="Python">

<Embed id="vtG-PyKz2E5" />

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

It's worth noting that services are just containers, and all containers in Dagger have run-exactly-once semantics. Concurrent runs of the same container synchronize and attach to the same run with progress/logs multiplexed to each caller. Canceling a run only interrupts the container process when all runs are canceled, so new clients can come and go throughout a service run.

Run-exactly-once semantics are very convenient. You don't have to come up with names and maintain instances of services; they're content-addressed, so you just use them by value. You also don't have to manage the state of the service; you can just trust that it will be running when needed and stopped when not.

:::tip
If you need multiple instances of a service, just attach something unique to each one, such as an instance ID.
:::

Here's a more detailed client-server example of running commands against a Redis service:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="d4SIgAW2Feo" />

</TabItem>
<TabItem value="Node.js">

<Embed id="zowrSKqU_0u" />

</TabItem>
<TabItem value="Python">

<Embed id="D80RyJ7f8h0" />

</TabItem>
</Tabs>

Note that this example relies on the 10-second grace period, which you should try to avoid. It would be better to chain both commands together, which ensures that the service stays running for both:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="avO1QVAIwBZ" />

</TabItem>
<TabItem value="Node.js">

<Embed id="ptz9Tj6pDLY" />

</TabItem>
<TabItem value="Python">

<Embed id="0EGmAzoPXPM" />

</TabItem>
</Tabs>

:::note
Depending on the 10-second grace period is risky because there are many factors which could cause a 10-second delay between calls to Dagger, such as excessive CPU load, high network latency between the client and Dagger, or Dagger operations that require a variable amount of time to process.
:::

## Persist service state

Another way to avoid relying on the grace period is to use a cache volume to persist a service's data, as in the following example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="23lXKbJiCz0" />

</TabItem>
<TabItem value="Node.js">

<Embed id="zhQX8VN750_A" />

</TabItem>
<TabItem value="Python">

<Embed id="uMNx2j1-GTt" />

</TabItem>
</Tabs>

:::info
This example uses Redis's `SAVE` command to ensure data is synced. By default, Redis flushes data to disk periodically.
:::

## Example: MariaDB database service for application tests

The following example demonstrates service containers in action, by creating a MariaDB database service container for use in application unit/integration testing.

The application used in this example is [Drupal](https://www.drupal.org/), a popular open-source PHP CMS. Drupal includes a large number of unit tests, including tests which require an active database connection. All Drupal 10.x tests are written and executed using the [PHPUnit](https://phpunit.de/) testing framework. Read more about [running PHPUnit tests in Drupal](https://www.drupal.org/docs/automated-testing/phpunit-in-drupal/running-phpunit-tests).

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

```go file=./snippets/use-services/use-db-service/main.go
```

</TabItem>
<TabItem value="Node.js">

```javascript file=./snippets/use-services/use-db-service/index.ts
```

</TabItem>
<TabItem value="Python">

```python file=./snippets/use-services/use-db-service/main.py
```

</TabItem>
</Tabs>

This example begins by creating a MariaDB service container and initializing a new MariaDB database. It then creates a Drupal container and installs required dependencies into it. Next, it adds a binding for the MariaDB service (`db`) in the Drupal container and sets a container environment variable (`SIMPLETEST_DB`) with the database DSN. Finally, it runs Drupal's kernel tests (which [require a database connection](https://www.drupal.org/docs/automated-testing/phpunit-in-drupal/running-phpunit-tests#non-unit-tests)) using PHPUnit and prints the test summary to the console.

:::tip
Explicitly specifying the service container port with `WithExposedPort()` (Go), `withExposedPort()` (Node.js) or `with_exposed_port()` (Python) is particularly important here. Without it, Dagger will start the service container and immediately allow access to service clients. With it, Dagger will wait for the service to be listening first.
:::

## Conclusion

This tutorial walked you through the basics of using service containers with Dagger. It explained how container-to-container networking and the service lifecycle is implemented in Dagger. It also provided examples of exposing service ports, binding services and persisting service state using Dagger.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
