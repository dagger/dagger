# Europa Core packages

## About this directory

`stdlib/europa/` holds the development version of the Core packages for the upcoming [Europa release](https://github.com/dagger/dagger/issues/1088).

Once Europa is released, `stdlib/europa` will become the new `stdlib/`

## What are Dagger core packages?

Dagger core packages are CUE packages released alongside the Dagger engine, to allow developers to access its features.

### Dagger Core API: `dagger.io/dagger`

*Development import path: `alpha.dagger.io/europa/dagger`*

The Dagger Core API defines core types and utilities for programming Dagger:

* `#Plan`: a complete configuration executable by `dagger up`
* `#FS` to reference filesystem state
* `#Secret` to (securely) reference external secrets
* `#Service` to reference network service endpoints
* `#Stream` to reference byte streams

### Low-level Engine API: `dagger.io/dagger/engine`

* *Development import path (implemented subset): `alpha.dagger.io/europa/dagger/engine`*
* *Development importa pth (full spec): `alpha.dagger.io/dagger/europa/dagger/engine/spec/engine`*

`engine` is a low-level API for accessing the raw capabilities of the Dagger Engine. Most developers should use the Dagger Core API instead (`dagger.io/dagger`), but experts and framework developers can target the engine API directly for maximum control.

This API prioritizes robustness, consistency, and feature completeness. It does NOT prioritize developer convenience or leveraging Cue for composition.

In Europa, `engine` will deprecate the following implicit API:
* Low-level operations defined in `alpha.dagger.io/dagger/op`
* Imperative DSL to assemble Dockerfile-like arrays as Cue arrays
* Convention to embed pipelines in the Cue lattice with the special nested definition `#up`
* Convention to reference filesystem state from the Cue lattice with `@dagger(artifact)`
* Convention to reference external secrets from the Cue lattice with `@dagger(secret)`
* Convention to reference external network endpoints from the Cue lattive with `@dagger(stream)`
* Convention that some operations (specifically `op.#Local`) are meant to be generated at runtime rather than authored manually.

## TODO LIST

* #Scratch: replace with null #FS?
* Resolve registry auth special case (buildkit does not support scoping registry auth)
* Are there runtime limitations in….
     * using hidden fields `_foo` as part of the DAG?
     * using `if` statements as part of the DAG?
     * using inlined Cue expressions as part of the DAG?
* Readability of error messages
  * At a minimum don’t make it worse!
  * Small improvements are good (eg. 
* Make sure we don’t make error messages LESS readable
* [Outstanding questions on proxy features](https://github.com/dagger/dagger/pull/1117#discussion_r765211280)
* [Outstanding questions on #Stream and emulating unix pipes with them](https://github.com/dagger/dagger/pull/1117#discussion_r766145864)
* [Outstanding questions on engine.#Pull and information loss](https://github.com/dagger/dagger/pull/1117#discussion_r765219049)
* [Outstanding questions on global registry auth scope in buildkit](https://github.com/dagger/dagger/pull/1117#discussion_r765963051)
* [Outstanding questions on platform key](https://github.com/dagger/dagger/pull/1117#discussion_r766085610)
