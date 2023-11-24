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

You can create a Dagger GraphQL API client in any programming language. This tutorial demonstrates the process in the following languages:

- Rust, using the [gql_client library](https://github.com/arthurkhlghatyan/gql-client-rs) (MIT License)
- PHP, using the [php-graphql-client library](https://github.com/mghoneimy/php-graphql-client) (MIT License)

This tutorial assumes that:

- You have a basic understanding of the Rust or PHP programming languages. If not, [read the Rust tutorial](https://www.rust-lang.org/learn) or [read the PHP tutorial](https://www.php.net/manual/en/getting-started.php).
- You have a development environment for Rust 1.65 (or later) or PHP 8.1 (or later). If not, [install Rust](https://www.rust-lang.org/tools/install) or [install PHP](https://www.php.net/manual/en/install.php).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed on the host system. If not, [install the Dagger CLI](../cli/465058-install.md).

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

{@include: ../partials/_run_api_client.md}

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
