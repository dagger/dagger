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
