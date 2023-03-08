---
slug: /757394/use-services
displayed_sidebar: "current"
category: "guides"
tags: ["go", "python", "nodejs"]
authors: ["Alex Suraci"]
date: "2023-03-07"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";
import Embed from '@site/src/components/atoms/embed.js'

# Use Services in Dagger

## Introduction

Dagger v0.4 introduced services, aka container-to-container networking. This feature enables users to spin up additional long-running services (as containers) and communicate with those services from their Dagger pipelines.

Some common use cases for services are:

- Run a test database
- Run end-to-end integration tests
- Run sidecar services

This tutorial teaches you the basics of using services in Dagger.

## Requirements

This tutorial assumes that:

- You have a Go, Python or Node.js development environment. If not, install [Go](https://go.dev/doc/install), [Python](https://www.python.org/downloads/) or [Node.js](https://nodejs.org/en/download/).
- You have a Dagger SDK installed for one of the above languages. If not, follow the installation instructions for the Dagger [Go](../sdk/go/371491-install.md), [Python](../sdk/python/866944-install.md) or [Node.js](../sdk/nodejs/835948-install.md) SDK.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Key concepts

Dagger services are just containers, which have the following characteristics:

- Each container has its own network namespace and IP address
- Each container has a unique, deterministic DNS address
- Containers can expose ports and endpoints
- Containers can bind other containers as services

Service containers come with the following built-in features:

- Service containers are started just-in-time, de-duplicated, and stopped when no longer needed
- Service containers are health checked prior to running clients
- Service containers are given an alias for the client container to use as its hostname

## Using hostnames

Containers run in a bridge network. Each container has its own IP address that other containers can reach. Here's a simple example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="blm2lRNoYzE" />

</TabItem>

<TabItem value="Python">

<Embed id="bhx9i4LCcD9" />

</TabItem>
<TabItem value="Node.js">

<Embed id="Ac9pAtECYRy" />

</TabItem>
</Tabs>

Containers never use IP addresses to reach each other directly. IP addresses are ephemeral, so doing so would nullify the cache. Instead, Dagger gives each container a unique but deterministic hostname, which doubles as a DNS address. Here's an example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="H5Eb0Hs7JMd" />

</TabItem>

<TabItem value="Python">

<Embed id="2qLVfgdsnI6" />

</TabItem>
<TabItem value="Node.js">

<Embed id="p4TAAfexI_a" />

</TabItem>
</Tabs>

This hash value is derived from the same value that determines whether an operation is a cache hit in Buildkit: the vertex digest.

To get a container's address, you wouldn't normally run the `hostname` command, because you'd just be getting the hostname of a container that runs `hostname`, which isn't very helpful. Instead, you would use the `Hostname()` (Go) or `hostname()` (Python and Node.js) SDK method, which returns a domain name reachable by other containers:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="cwmeT7388mg" />

</TabItem>

<TabItem value="Python">

<Embed id="U5SlCIjnDCN" />

</TabItem>
<TabItem value="Node.js">

<Embed id="WpUuvEWdtrG" />

</TabItem>
</Tabs>

In practice, you are more likely to use aliases with service bindings or endpoints, which are covered in the next section.

## Exposing ports

Dagger offers two methods to work with service ports:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

- Use the `WithExposedPort()` method to set ports that the container will listen on. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.
- Use the `Endpoint()` method to create a string address to a service's port. You can either specify a port or let Dagger pick the first exposed port.

Here's an example:

<Embed id="kDBfYoh2uau" />

</TabItem>

<TabItem value="Python">

- Use the `with_exposed_port()` method to set ports that the container will listen on. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.
- Use the `endpoint()` method to create a string address to a service's port. You can either specify a port or let Dagger pick the first exposed port.

Here's an example:

<Embed id="d43ukG64Vv-" />

</TabItem>
<TabItem value="Node.js">

- Use the `withExposedPort()` method to set ports that the container will listen on. Dagger checks the health of each exposed port prior to running any clients that use the service, so that clients don't have to implement their own polling logic.
- Use the `endpoint()` method to create a string address to a service's port. You can either specify a port or let Dagger pick the first exposed port.

Here's an example:

<Embed id="x72_mgQv8hS" />

</TabItem>
</Tabs>

## Binding services

Dagger enables users to bind a service container to a client container with an alias (such as `redis`) that the client container can use as a hostname.

Binding a service to a container expresses a dependency: the service container needs to be running when the client container runs. The bound service container is started automatically whenever its client container runs.

Here's an example of an HTTP service automatically starting in tandem with a client container. The service binding enables the client container to access the HTTP service using the alias `www`.

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="pQoE-5_0Ghg" />

</TabItem>

<TabItem value="Python">

<Embed id="T47F8bg5_Eo" />

</TabItem>
<TabItem value="Node.js">

<Embed id="67ATACE21zB" />

</TabItem>
</Tabs>

When a service is bound to a container, it also conveys to any outputs of that container, such as files or directories. The service will be started whenever the output is used, so you can also do things like this:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="Qoljk5SEPuu" />

</TabItem>

<TabItem value="Python">

<Embed id="KgpaTo2NmE-" />

</TabItem>
<TabItem value="Node.js">

<Embed id="3AWLZLpSavU" />

</TabItem>
</Tabs>

## Understanding the service lifecycle

If you're not interested in what's happening in the background, you can skip this section and just trust that services are running when they need to be. If you're interested in the theory, keep reading.

Consider this example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="JwuCvswjsEM" />

</TabItem>

<TabItem value="Python">

<Embed id="vtG-PyKz2E5" />

</TabItem>
<TabItem value="Node.js">

<Embed id="r9lHD3r4fcI" />

</TabItem>
</Tabs>

Here's what happens on the last line:

1. The client requests the `ping` container's stdout, which requires the container to run.
1. Dagger sees that the `ping` container has a service binding, `redisSrv`.
1. Dagger starts the `redisSrv` container, which recurses into this same process.
1. Dagger waits for health checks to pass against `redisSrv`.
1. Dagger runs the `ping` container with the `redis-srv` alias magically added to `/etc/hosts`.

:::note
Dagger cancels each service run after a 10 second grace period to avoid frequent restarts.
:::

It's worth noting that services are just containers, and all containers in Dagger have run-exactly-once semantics. Concurrent runs of the same container synchronize and attach to the same run with progress/logs multiplexed to each caller. Canceling a run only interrupts the container process when all runs are canceled, so new clients can come and go throughout a service run.

Run-exactly-once semantics are very convenient. You don't have to come up with names and maintain instances of services; they're content-addressed, so you just use them by value. You also don't have to manage the state of the service; you can just trust that it will be running when needed and stopped when not.

:::tip
If you need multiple instances of a service, just attach something unique to each one, such as an instance ID.
:::

Let's put all this together in a full client-server example of running commands against a Redis service:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="d4SIgAW2Feo" />

</TabItem>

<TabItem value="Python">

<Embed id="D80RyJ7f8h0" />

</TabItem>
<TabItem value="Node.js">

<Embed id="A-ypySBNtVq" />

</TabItem>
</Tabs>

Note that this example relies on the 10-second grace period, which you should try to avoid. It would be better to chain both commands together, which ensures that the service stays running for both:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="avO1QVAIwBZ" />

</TabItem>

<TabItem value="Python">

<Embed id="0EGmAzoPXPM" />

</TabItem>
<TabItem value="Node.js">

<Embed id="zajuPzL79cf" />

</TabItem>
</Tabs>

:::note
Depending on the 10-second grace period is risky because there are many factors which could cause a 10-second delay between calls to Dagger, such as excessive CPU load, high network latency between the client and Dagger, or Dagger operations that require a variable amount of time to process.
:::

## Persisting service state

Another way to avoid relying on the grace period is to use a cache volume to persist a service's data, as in the following example:

<Tabs groupId="language" className="embeds">
<TabItem value="Go">

<Embed id="23lXKbJiCz0" />

</TabItem>

<TabItem value="Python">

<Embed id="uMNx2j1-GTt" />

</TabItem>
<TabItem value="Node.js">

<Embed id="6FdvzPEfBgH" />

</TabItem>
</Tabs>

Note that this example uses Redis's `SAVE` command to ensure data is synced. By default, Redis flushes data to disk periodically.

## Conclusion

This tutorial walked you through the basics of using services with Dagger. It explained how container-to-container networking and the service lifecycle is implemented in Dagger. It also provided examples of exposing service ports, binding services and persisting service state using Dagger.

Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger.
