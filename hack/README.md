# Overview

We dogfood dagger as much as possible when building, testing, linting, and just generally automating dagger related development tasks.

To avoid chicken and egg problems we use a previous version of dagger to bootstrap dagger built from this local code.

- This works by maintaining a separate go.mod in `internal/mage/` which specifies the version of dagger used to run our automation.
- Typically, this will be an officially released version of dagger, but it should also be possible to use development versions of dagger that have only been merged to main if ever needed too.

There are a few scripts for running automation in different ways

## `./hack/make-prod`

_Example:_ `./hack/make-prod sdk:go:lint`

`make-prod` runs mage steps defined in `internal/mage` using the version of dagger specified in `internal/mage/go.mod`.

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

## Just lint my code, even if my local engine code is broken

`./hack/make-prod sdk:<go,python,nodejs>:lint`

Using `make-prod` means that you won't first bootstrap a dev engine from local engine code and lint using that. This makes sense for linting and similar tasks since they typically don't need bleeding edge engine features. You can thus also run this if you are making engine changes and currently have a broken one locally.
