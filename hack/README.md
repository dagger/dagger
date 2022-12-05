# Overview

We dogfood dagger as much as possible when building, testing, linting, and just generally automating dagger related development tasks.

There are a few scripts for running automation in different ways

## `./hack/make`

_Example:_ `./hack/make engine:test`

`make` will first bootstrap an engine from local code _AND THEN_ run the specified mage step such that any Container Exec will point to that dev engine.

## `./hack/dev`

_Example:_ `./hack/dev bash`

`dev` will first boostrap an engine from local code and then execute whatever command you specify with environment variables set so that dagger SDKs will connect to the dev engine.

# Examples

## Build my local engine code and then run many commands against it, without always rebuilding

`./hack/dev bash`

This will bootstrap your local engine code and then open a shell with env vars pointing to that dev engine. You can thus run `go test`, `poetry run`, `yarn run` and have the tests execute against that dev engine.

Unlike `./hack/make`, this won't require always rebuilding the engine every time you run a command, which can sometimes be more convenient.
