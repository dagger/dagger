---
slug: /sdk/elixir/756758/get-started
---

# Get Started with the Dagger Elixir SDK

## Introduction

This tutorial teaches you the basic of using Dagger in Elixir. You will learn how to:

- Install Elixir SDK
- Create an Elixir CI task to test an application
- Improve the Python CI tool to test the application against multiple Elixir and OTP versions

## Requirements

This tutorial assumes that:

- You have a basic understanding of the Elixir programming language. If not, [read the Elixir learning](https://elixir-lang.org/learning.html).
- You have a Elixir development environment with Elixir 1.14 or later and Erlang/OTP 25 or later. If not, install [Elixir](https://elixir-lang.org/install.html) and [Erlang/OTP](https://www.erlang.org/downloads).
- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).

## Step 1: Create an Elixir application

Create an Elixir project with `mix`:

```shell
mix new a_project
```

## Step 2: Install the Dagger Elixir SDK

Open `mix.exs` and add `{:dagger, "~> 0.8"}` to the list in `deps` function:

```elixir
def deps do
  [
    {:dagger, "~> 0.8", only: [:dev, :test]}
  ]
end
```

Then run `mix deps.get` to fetch the Elixir SDK from Hex.pm.

## Step 3: Create the Mix task

Create Elixir module at `lib/mix/tasks/a_project.test.ex`:

```elixir file=snippets/get-started/step3/a_project.test.ex
```

This module performs the following operations:

- It starts all applications that related to Dagger Elixir SDK with `Application.ensure_all_started(:dagger)`.
- It creates a Dagger client with `Dagger.connect!/1`.
- It uses the client's `Dagger.Client.host/1` and `Dagger.Host.directory/3` to address the host directory. Uses `exclude` in `Dagger.Host.directory/3` to filter unwanted files and directories.
- It uses the client's `Dagger.Client.container/2` and `Dagger.Container.from/2` to initialize a new container and uses `hexpm/elixir:1.15.4-erlang-25.3.2.5-ubuntu-bionic-20230126` as a base image.
- It uses `Dagger.Container.with_mounted_directory/3` to mount source files into container.
- It uses `Dagger.Container.with_exec/3` to execute a command.
- It uses `Dagger.Sync.sync/1` to force executing commands.
- It uses `Dagger.close/1` to teardown the client.

Run the Mix task by executing the command below from the project directory:

```shell
mix a_project.test
```

## Step 4: Test against multiple Elixir and Erlang/OTP versions

Now that the Elixir CI tool can test the application against a Elixir and Erlang/OTP version, the next step is to extend it for multiple Elixir and Erlang/OTP versions.

Replace the `lib/mix/tasks/a_project.test.ex` file from the previous step with the version below:

```elixir file=snippets/get-started/step4/a_project.test.ex
```

In this version has additional support for test and building against multiple Elixir and Erlang/OTP versions.

- It defines the test matrix, by defining a list of Elixir and Erlang/OTP version pair.
- It pass through the `Task.async_strem/3` to run test in each version concurrently.

## Conclusion

This turorial introduced you to the Dagger Elixir SDK. It explaned how to install the SDK and use it with an Elixir application.  It also provided a working example of the a tool powered by the SDK, demonstrating how to test an application against multiple Elixir and Erlang/OTP versions in parallel.

Use the [HexDocs SDK Reference](https://hexdocs.pm/dagger/Dagger.html) to learn more abount the Dagger Elixir SDK.
