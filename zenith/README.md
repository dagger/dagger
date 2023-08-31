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

To do that, just run this from the dagger repo root:

```console
./hack/dev
export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-engine.dev
export PATH=$(pwd)/bin:$PATH
```

Then browse our [examples](EXAMPLES.md) for inspiration.

For now, environments are easiest to setup as subdirectories in the dagger repo. This is just due to the requirements to use development versions of SDKs, not a permanent feature.

For these examples, we'll create new environments in the dagger repo.
