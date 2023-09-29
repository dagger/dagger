---
slug: /sdk/elixir/756758/get-started
---

# Get Started with the Dagger Elixir SDK

{@include: ../../partials/_experimental-sdk-elixir.md}

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

{@include: ../../partials/_install-sdk-elixir.md}

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
❯ dagger run mix elixir_with_dagger.test
┣─╮
│ ▽ init
│ █ [0.76s] connect
│ ┣ [0.52s] starting engine
│ ┣ [0.18s] starting session
│ ┻
█ [1.75s] mix elixir_with_dagger.test
┃ Tests succeeded!
┣─╮
│ ▽ host.directory .
│ █ [0.23s] upload .
│ ┣ [0.15s] transferring eyJvd25lcl9jbGllbnRfaWQiOiIwNWNmN2E4YTF4dDZ0dG52amUwbG1yeTYxIiwicGF0aCI6Ii4iLCJpbmNsdWRlX3BhdHRlcm5zIjpudWxsLCJleGNsdWRlX3BhdHRlcm5zIjpbImRlcHMiLCJfYnVpbGQiXSwiZm9sbG93X3BhdGhzIjpudWxsLCJyZWFkX3NpbmdsZV9maWxlX29ubHkiOmZhbHNlLCJtYXhfZmlsZV9zaXplIjowfQ==:
│ █ CACHED copy . (exclude deps, _build)
│ ┣─╮ copy . (exclude deps, _build)
│ ┻ │
┣─╮ │
│ ▽ │ from hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ █ │ [0.18s] resolve image config for docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ █ │ [0.04s] pull docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ ┣ │ [0.04s] resolve docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0@sha256:d77ef43aeb585ec172e290c7ebc171a16e21ebaf7c9ed09b596b9db55c848f00
│ ┣─┼─╮ pull docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ ┻ │ │
█◀──┴─╯ CACHED exec mix local.hex --force
█ CACHED exec mix local.rebar --force
█ CACHED exec mix deps.get
█ CACHED exec mix test
┻
• Engine: fd814943769d (version v0.8.7)
⧗ 2.51s ✔ 20 ∅ 5
```

## Step 4: Test against multiple Elixir and Erlang/OTP versions

Now that the Elixir CI tool can test the application against a specified Elixir and Erlang/OTP version, the next step is to extend it for multiple Elixir and Erlang/OTP versions.

Replace the `lib/mix/tasks/elixir_with_dagger.test.ex` file from the previous step with the version below:

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
❯ dagger run mix elixir_with_dagger.test
┣─╮
│ ▽ init
│ █ [0.59s] connect
│ ┣ [0.53s] starting engine
│ ┣ [0.01s] starting session
│ ┻
█ [2.09s] mix elixir_with_dagger.test
┃ Starting tests for Elixir 1.14.4 with Erlang OTP erlang-25.3.2
┃ Starting tests for Elixir 1.15.0-rc.2 with Erlang OTP erlang-26.0.1
┃ Tests for Elixir 1.14.4 with Erlang OTP erlang-25.3.2 succeeded!
┃ Tests for Elixir 1.15.0-rc.2 with Erlang OTP erlang-26.0.1 succeeded!
┃ All tasks have finished
┣─╮
│ ▽ host.directory .
│ █ [0.39s] upload .
│ ┣ [0.16s] transferring eyJvd25lcl9jbGllbnRfaWQiOiJsNHJlemx0cW10dHd3MHhrNzJ6N3l1eGg1IiwicGF0aCI6Ii4iLCJpbmNsdWRlX3BhdHRlcm5zIjpudWxsLCJleGNsdWRlX3BhdHRlcm5zIjpbImRlcHMiLCJfYnVpbGQiXSwiZm9sbG93X3BhdGhzIjpudWxsLCJyZWFkX3NpbmdsZV9maWxlX29ubHkiOmZhbHNlLCJtYXhfZmlsZV9zaXplIjowfQ==:
│ █ CACHED copy . (exclude deps, _build)
│ ┣─╮ copy . (exclude deps, _build)
│ ┻ │
┣─╮ │
│ ▽ │ from hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ █ │ [0.22s] resolve image config for docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
┣─┼─┼─╮
│ │ │ ▽ from hexpm/elixir:1.15.0-rc.2-erlang-26.0.1-alpine-3.18.2
│ │ │ █ [0.35s] resolve image config for docker.io/hexpm/elixir:1.15.0-rc.2-erlang-26.0.1-alpine-3.18.2
│ █ │ │ [0.04s] pull docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ ┣ │ │ [0.03s] resolve docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0@sha256:d77ef43aeb585ec172e290c7ebc171a16e21ebaf7c9ed09b596b9db55c848f00
│ ┣─┼─┼─╮ pull docker.io/hexpm/elixir:1.14.4-erlang-25.3.2-alpine-3.18.0
│ ┻ │ │ │
█◀──┤─┼─╯ CACHED exec mix local.hex --force
█   │ │ CACHED exec mix local.rebar --force
█   │ │ CACHED exec mix deps.get
█   │ │ CACHED exec mix test
│   │ █ [0.04s] pull docker.io/hexpm/elixir:1.15.0-rc.2-erlang-26.0.1-alpine-3.18.2
│   │ ┣ [0.03s] resolve docker.io/hexpm/elixir:1.15.0-rc.2-erlang-26.0.1-alpine-3.18.2@sha256:20eb9af6c46749c7d4a18de9aa36950f591ffa0e19e219ac6b21c58d01cfb07f
│ ╭─┼─┫ pull docker.io/hexpm/elixir:1.15.0-rc.2-erlang-26.0.1-alpine-3.18.2
│ │ │ ┻
█◀┴─╯ CACHED exec mix local.hex --force
█ CACHED exec mix local.rebar --force
█ CACHED exec mix deps.get
█ CACHED exec mix test
┻
• Engine: fd814943769d (version v0.8.7)
⧗ 2.69s ✔ 30 ∅ 9
```

## Conclusion

This tutorial introduced you to the Dagger Elixir SDK. It explaned how to install the SDK and use it with an Elixir project. It also provided a working example of a CI tool powered by the SDK, demonstrating how to test a project against multiple Elixir and Erlang/OTP versions in parallel.

Use the [HexDocs SDK Reference](https://hexdocs.pm/dagger/Dagger.html) to learn more about the Dagger Elixir SDK.
