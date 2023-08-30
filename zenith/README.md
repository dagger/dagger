# Project Zenith

## Overview

*Project Zenith* is the codename of a future release of Dagger, currently in development.

The goal of Project Zenith is to make Dagger more accessible, by delivering it as a CLI tool rather than just a library.

Features of Project Zenith include:

* Major expansion of the `dagger` CLI, removing the need to create a custom CLI for each project
* Major expansion of the Dagger API, with a complete cross-language extension and composition system
* An open ecosystem of reusable content, to take advantage of the extension and composition system
* A major overhaul to our documentation and marketing, to explain Dagger as a tool for development and CI, rather than "just a CI engine"

## Status

As of August 24 2023, Project Zenith is in active development, with the goal of releasing before the end the year.

## How to participate

The Dagger Engine is developed in the open, and Project Zenith is no exception.

* Discussions take place [on our Discord server](https://discord.com/channels/707636530424053791/1120503349599543376)
* Contributors and testers meet every friday at 09:00 Pacific time [on our Discord audio room](https://discord.com/channels/707636530424053791/911305510882513037)


## How to test it

In order to run dagger with Zenith functionality, you will need to build a Dagger CLI off this branch and build a Dagger Engine off this branch.

Our existing command for doing this is `./hack/dev`. That script will:

1. Build the Dagger CLI and export it to `./bin/dagger`
1. Build the Dagger Engine and run it in your local docker installation in a container named `dagger-engine.dev`

In order to ensure you are using the dev CLI+Engine; it's suggested to set these environment variables after running `./hack/dev`:

- `_EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-engine.dev`
- `$PATH=$(pwd)/bin:$PATH`
  - This assumes `$(pwd)` points to the root of the dagger repo, replace it with the actual root if that's not the case

## Examples

For now, environments are easiest to setup as subdirectories in the dagger repo. This is just due to the requirements to use development versions of SDKs, not a permanent feature.

For these examples, we'll create new environments in the dagger repo.

### Example 1: Go Environment

After running `mkdir -p ./goexample && cd ./goexample`, you need to initialize your environment:

```console
dagger env init --name goexample --sdk go --root ..
```

- The `name` and `sdk` flags are self-explanatory
- The `root` flag is an ugly artifact of the need to use development SDKs and thus load the entire dagger repo, so it just needs to point to the root of the repo. This is just a temporary hack, not a permanent feature.

After that, you will see a `dagger.json` file and a `dagger.gen.go` file in your current dir. These hold your environment configuration and generated dagger client code, respectively.

To implement environment code, you just need to create a `main.go` file in this dir and register your checks/artifacts/commands/shells/functions.

- You can see some existing examples in `universe/_demo/main.go`, `universe/_demo/server/main.go`, `universe/go/main.go`, and `universe/apko/apko.go`

If at any point you want to add a dependency on another environment, you can run:

```console
dagger env extend <./path/to/other/env>
```

- Currently, dependencies must be relative paths to other environment directories on the local filesystem. Support for `git://` envs is easy to add once requested 🙂

That command, if successful, will update `dagger.gen.go` with the new bindings for the environment you just extended with.

Additionally, if you ever need to update codegen bindings for any extensions, you can just run

```console
dagger env sync
```

to bring it up to date.

### Example 2: Python Environment

After running `mkdir -p ./pythonexample && cd ./pythonexample`, you need to initialize your environment:

```console
dagger env init --name pythonexample --sdk python --root ..
```

- The `name` and `sdk` flags are self-explanatory
- The `root` flag is an ugly artifact of the need to use development SDKs and thus load the entire dagger repo, so it just needs to point to the root of the repo. This is just a temporary hack, not a permanent feature.

After that, you will see a `dagger.json` file in your current dir. That holds your environment configuration.

- Custom codegen for Python envs, similar to what's described for Go envs above, has not yet been implemented

To implement environment code, you need to create a `main.py` file and a `pyproject.toml` file in your current dir. See the `universe/_demo/client` dir for an example to help get started.
