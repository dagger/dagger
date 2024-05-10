# Overview

This directory contains various helpers for developing dagger, providing a
utility layer on top of our [ci module](../ci).

## `./hack/dev`

_Usage:_ `./hack/dev`

`dev` builds the engine and cli from local code, and additionally starts the
engine in a docker container.

_Usage:_ `./hack/dev ...`

As above, `dev` builds and starts the engine, but runs the specified command
with the dagger context environment variables that allow dagger commands and
SDKs to connect directly to it.

## `./hack/with-dev`

_Usage:_ `./hack/with-dev ...`

`with-dev` runs the specified command with the dagger context environment
variables set (similar to `dev` above, but does not rebuild the engine).

# Examples

## Build my local engine code and then run many commands against it, without always rebuilding

`./hack/dev bash`

This will bootstrap your local engine code and then open a shell with env vars
pointing to that dev engine. You can thus run `go test`, `hatch run`, `yarn run`
and have the tests execute against that dev engine.
