---
slug: /sdk/elixir/756758/get-started
---

# Get Started with the Dagger Elixir SDK

:::warning
The Dagger Elixir SDK is currently experimental and is subject to change.
:::

## Introduction

This tutorial teaches you the basics of using Dagger in Elixir. You will learn how to:

- Install the Elixir SDK
- Create an Elixir CI tool to test an application
- Improve the Elixir CI tool to test the application against multiple Elixir and OTP versions

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Elixir programming language. If not, [read the Elixir documentation](https://elixir-lang.org/learning.html).
- You have an Elixir development environment with Elixir 1.14 or later and Erlang/OTP 25 or later. If not, install [Elixir](https://elixir-lang.org/install.html) and [Erlang/OTP](https://www.erlang.org/downloads).
- You have the Dagger CLI installed on the host system. If not, [install the Dagger CLI](../../cli/465058-install.md).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Create an Elixir project

Create an Elixir project with `mix`:

```shell
mix new elixir_with_dagger
```

## Step 2: Install the Dagger Elixir SDK

In your project directory, open `mix.exs` and add `{:dagger, "~> 0.8"}` to the list in the `deps` function:

```elixir
def deps do
  [
    {:dagger, "~> 0.8", only: [:dev, :test]}
  ]
end
```

Run `mix deps.get` to fetch the Elixir SDK from Hex.pm:

```shell
mix deps.get
```

## Step 3: Create the Mix task

Create a new module and Mix task to test the project at `lib/mix/tasks/elixir_with_dagger.test.ex`:

```elixir file=snippets/get-started/step3/elixir_with_dagger.test.ex
```

This module performs the following operations:

- It starts all applications related to the Dagger Elixir SDK with `Application.ensure_all_started(:dagger)`.
- It creates a Dagger client with `Dagger.connect!/1`.
- It uses the client's `Dagger.Client.host/1` and `Dagger.Host.directory/3` to obtain a reference to the host directory. It additionally uses the `exclude` filter in `Dagger.Host.directory/3` to filter unwanted files and directories.
- It uses the client's `Dagger.Client.container/2` and `Dagger.Container.from/2` to initialize a new container from the `hexpm/elixir:1.15.4-erlang-25.3.2.5-ubuntu-bionic-20230126` base image.
- It uses `Dagger.Container.with_mounted_directory/3` to mount the project's source files into the container.
- It uses `Dagger.Container.with_exec/3` to define the commands to be executed in the container - in this case, commands such as `mix deps get`, which downloads dependencies and `mix test`, which runs unit tests. Each invocation of `with_exec` returns a revised `Container` with the results of command execution.
- It uses `Dagger.Container.stdout/1` to get the output of the last execution command.
- It uses `Dagger.close/1` to close the client connection.

Run the Mix task by executing the command below from the project directory:

```shell
dagger run mix elixir_with_dagger.test
```

The `dagger run` command executes the specified command in a Dagger session and displays live progress. Here is an example of the output:

```shell
Tests succeeded!
```

## Step 4: Test against multiple Elixir and Erlang/OTP versions

Now that the Elixir CI tool can test the application against a specified Elixir and Erlang/OTP version, the next step is to extend it for multiple Elixir and Erlang/OTP versions.

Replace the `lib/mix/tasks/a_project.test.ex` file from the previous step with the version below:

```elixir file=snippets/get-started/step4/elixir_with_dagger.test.ex
```

This version has additional support for testing and building against multiple Elixir and Erlang/OTP versions:

- It defines the test matrix, consisting of a list of Elixir and Erlang/OTP version pairs.
- It uses `Task.async_stream/3` to run tests against each version pair concurrently.
- It uses `Stream.run/1` to await all tasks.

Run the tool again by executing the command below:

```shell
dagger run mix elixir_with_dagger.test
```

The tool tests the application, logging its operations to the console as it works. If all tests pass, it displays the final output below:

```shell
Starting tests for Elixir 1.14.5 with Erlang OTP 25.3.2.5
Starting tests for Elixir 1.15.4 with Erlang OTP 25.3.2.5
Tests for Elixir 1.15.4 with Erlang OTP 25.3.2.5 succeeded
Tests for Elixir 1.14.5 with Erlang OTP 25.3.2.5 succeeded!
All tasks have finished
```

## Conclusion

This tutorial introduced you to the Dagger Elixir SDK. It explaned how to install the SDK and use it with an Elixir project. It also provided a working example of a CI tool powered by the SDK, demonstrating how to test a project against multiple Elixir and Erlang/OTP versions in parallel.

Use the [HexDocs SDK Reference](https://hexdocs.pm/dagger/Dagger.html) to learn more about the Dagger Elixir SDK.
