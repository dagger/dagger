# Overview

We dogfood dagger as much as possible when building, testing, linting, and just generally automating dagger related development tasks.

There are a few scripts for running automation in different ways

## `./hack/make-prod`

_Example:_ `./hack/make-prod sdk:go:lint`

`make-prod` runs mage steps defined in `internal/mage` using a _STABLE_ version of dagger (specified in `go.mod`).

It will thus _NOT_ run using the dagger engine defined in local code. For that, use `./hack/make`.

## `./hack/make`

_Example:_ `./hack/make engine:test`

`make` will first bootstrap an engine from local code _AND THEN_ run the specified mage step against that dev engine.

## `./hack/dev`

_Example:_ `./hack/dev bash`

`dev` will first boostrap an engine from local code and then execute whatever command you specify with environment variables set so that dagger SDKs will connect to the dev engine.

# Examples

## Build my local engine code and then run many commands against it, without always rebuilding

`./hack/dev bash`

This will bootstrap your local engine code and then open a shell with env vars pointing to that dev engine. You can thus run `go test`, `poetry run`, `yarn run` and have the tests execute against that dev engine.

Unlike `./hack/make`, this won't require always rebuilding the engine every time you run a command, which can sometimes be more convenient.

# Misc Hacks

## Bootstrapping

To avoid chicken and egg problems we use a previous version of dagger to bootstrap dagger built from local code in this repo.

- This works by specifying the stable version of the dagger Go SDK in go.mod and overriding it to the latest local version via go.work
  - Typically, the stable version will be an officially released version of dagger, but it should also be possible to use development versions of dagger that have only been merged to main if ever needed too.
- Go workspaces have a very useful property: they can be easily disabled by just setting `GOWORK=off`.
- So, when you want to use the stable go sdk, you thus want to run with `GOWORK=off`. When you want to run using the latest go sdk in this repo, you should just leave the env as is.
