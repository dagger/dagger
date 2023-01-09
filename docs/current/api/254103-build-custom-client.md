---
slug: /api/254103/build-custom-client
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Build a Custom API Client

## Introduction

This tutorial teaches you how to create a custom client for the Dagger GraphQL API in a programming language of your choice. You will learn how to:

- Create a custom client for the Dagger GraphQL API
- Connect to the Dagger GraphQL API and run your custom client with the Dagger CLI

## Requirements

This tutorial assumes that:

- You have a development environment for your chosen programming language.
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed on the host system. If not, [install the Dagger CLI](../cli/465058-install.md).

This tutorial demonstrates the process of creating an API client in two dfferent programming languages:

- Rust, using the [gql_client library](https://github.com/arthurkhlghatyan/gql-client-rs) (MIT License)
- PHP, using the [php-graphql-client library](https://github.com/mghoneimy/php-graphql-client) (MIT License)

## Step 1: Select and install your GraphQL client library

The first step is to identify available GraphQL clients for your chosen programming language and select one that fits your requirements. GraphQL has a [large and growing list of client implementations](https://graphql.org/code/#language-support) in over 20 languages.

Create a new directory for the project and install the client as follows:

<Tabs>
<TabItem value="Rust">

```shell
mkdir my-project
cd my-project
cargo init
cargo add gql_client@1.0.7
cargo add serde_json@1.0.89
cargo add tokio@1.22.0 -F full
cargo add base64@0.13.1
```

</TabItem>
<TabItem value="PHP">

```shell
mkdir my-project
cd my-project
composer require gmostafa/php-graphql-client
```

</TabItem>
</Tabs>

## Step 2: Create an API client

Once the client library is installed, create an API client as described below.

<Tabs>
<TabItem value="Rust">

Add the following code to `src/main.rs`:

```rust file=snippets/build-custom-client/step2/main.rs

```

</TabItem>
<TabItem value="PHP">

Create a new file named `client.php` and add the following code to it:

```php file=snippets/build-custom-client/step2/client.php

```

</TabItem>
</Tabs>

This code listing initializes the client library and defines the Dagger pipeline to be executed as a GraphQL query. This query performs the following operations:

- It requests the `from` field of Dagger's `Container` object type, passing it the address of a container image. To resolve this, Dagger will initialize a container using the specified image and return a `Container` object representing the `alpine:latest` container image.
- Next, it requests the `withExec` field of the `Container` object from the previous step, passing the `uname -a` command to the field as an array of arguments. To resolve this, Dagger will return a `Container` object containing the execution plan.
- Finally, it requests the `stdout` field of the `Container` object returned in the previous step. To resolve this, Dagger will execute the command and return a `String` containing the results.
- The result of the query is a JSON object, which is processed and printed to the output device.

:::info
The API endpoint and the HTTP authentication token for the GraphQL client are not statically defined, they must be retrieved at run-time from the special `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` environment variables. This is explained in more detail in the next section.
:::

## Step 3: Run the API client

To run the pipeline, the API client in the previous step needs to communicate with the Dagger Engine, which is responsible for accepting the query, executing it and returning the result. The `dagger run` command takes care of initializing a new local instance (or reusing a running instance) of the Dagger Engine on the host system and executing a specified command against it.

:::info
The Dagger Engine creates a unique local API endpoint for GraphQL queries for every Dagger session. This API endpoint is served over localhost at the port specified by the `DAGGER_SESSION_PORT` environment variable, and can be directly read from the environment in your client code.

For example, if `DAGGER_SESSION_PORT` is set to `12345`, the API endpoint can be reached at `http://127.0.0.1:$DAGGER_SESSION_PORT/query`

It additionally protects the exposed API with an HTTP Basic authentication token which can be retrieved from the `DAGGER_SESSION_TOKEN` variable.
:::

:::warning
Treat the `DAGGER_SESSION_TOKEN` value as you would any other sensitive credential. Store it securely and avoid passing it to, or over, insecure applications and networks.
:::

Run the API client using the Dagger CLI as follows:

<Tabs>
<TabItem value="Rust">

```shell
dagger run cargo run
```

This command:

- initializes a new Dagger Engine session
- sets the `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` environment variables.
- executes the `cargo run` command in that session

</TabItem>
<TabItem value="PHP">

```shell
dagger run php client.php
```

This command:

- initializes a new Dagger Engine session
- sets the `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN` environment variables.
- executes the `php client.php` command in that session

</TabItem>
</Tabs>

The specified command, in turn, invokes the custom API client, connects to the API endpoint specified in the `DAGGER_SESSION_PORT` environment variable, sets an HTTP Basic authentication token with `DAGGER_SESSION_TOKEN` and executes the GraphQL query. Here is an example of the output:

```shell
buildkitsandbox 5.15.0-53-generic unknown Linux
```

## Conclusion

This tutorial explained how to write a custom client for the Dagger GraphQL API. It provided working examples of how to program and run a Dagger pipeline using this client in two different programming languages. A similar approach can be followed in any other programming language with a GraphQL client implementation.

Use the [API Reference](https://docs.dagger.io/api/reference) and the [CLI Reference](../cli/979595-reference.md) to learn more about the Dagger GraphQL API and the Dagger CLI respectively.
