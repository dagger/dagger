# Europa release staging

## About the europa/ directory

This directory is a staging area for the upcoming Europa release.
It is intended for experimentation and review without requiring long-lived development branches.
Its contents MUST NOT BE USED by `dagger` or its build and release tooling.

As part of the Europa release, this directory will be removed.

## About the Europa release

Europa is the codename of the final major release of Dagger before launch.
For more details on the Europa release, see the [Europa epic](https://github.com/dagger/dagger/issues/1088).

## New CUE packages

Europa introduces a new set of CUE packages for developers to use. These new packages are a complete, incompatible replacement for the pre-Europa packages.
* The bad news is that pre-Europa configurations will need to be manually adapted
* The good news is that Europa APIs are much better. So once ported to Europa, configurations will be shorter, easier to maintain, faster and more reliable (at least that's the goal!)

We intend for Europa to be the last breaking update. Going forward, we will aim for 100% compatibility
whenever possible, and when that is not possible, a migration path that is as automated and painless
as possible.

Starting with Europa, Dagger separates its Cue packages in two distinct namespaces: *stdlib* and *universe*.

* The Dagger Universe is a catalog of reusable Cue packages, curated by Dagger but possibly authored by third parties. Most packages in Universe contain reusable actions; some may also contain entire configuration templates.
* The Dagger Stdlib are core packages shipped with the Dagger engine.

|   |  *Stdlib* | *Universe* |
|----------|--------------|------|
| Import path |  `dagger.io` | `universe.dagger.io` |
| Purpose |  Access core Dagger features | Safely reuse code from the community |
| Author | Dagger team | Dagger community, curated by Dagger |
| Release cycle |    Released with Dagger engine   |  Released continuously |
| Size |  Small  | Large  |
| Growth rate | Grows slowly, with engine features | Grows fast, with community |

### Dagger Core API

*Import path: [`dagger.io/dagger`](./stdlib/dagger)*

The Dagger Core API defines core types and utilities for programming Dagger:

* `#Plan`: a complete configuration executable by `dagger up`
* `#FS` to reference filesystem state
* `#Secret` to (securely) reference external secrets
* `#Service` to reference network service endpoints
* `#Stream` to reference byte streams

### Low-level engine API

*Import path: [`dagger.io/dagger/engine`](./stdlib/dagger/engine)*

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

### Docker API

*Import path: [`universe.dagger.io/docker`](./universe/docker)*

The `docker` package is a native Cue API for Docker. You can use it to build, run, push and pull Docker containers directly from Cue.

The Dagger container API defines the following types:

* `#Image`: a container image
* `#Run`: run a comand in a container
* `#Push`: upload an image to a repository
* `#Pull`: download an image from a repository
* `#Build`: build an image

### Examples

*Import path: [`universe.dagger.io/examples`](https://github.com/shykes/dagger/tree/llb2/europa/universe/examples)*

This package contains examples of complete Dagger configurations, including the result of following tutorials in the documentations.

For example, [the todoapp example](https://github.com/shykes/dagger/tree/llb2/europa/universe/examples/todoapp/deploy) corresponds to the [Getting Started tutorial](https://docs.dagger.io/1003/get-started/)

### More packages

More packages are being developed under [universe.dagger.io](./universe)


## TODO LIST

* Support native language dev in `docker.#Run` with good DX (Python, Go, Typescript etc.)
* #Scratch: replace with null #FS?
* Coding style. When to use verbs vs. nouns?
* Resolve registry auth special case (buildkit does not support scoping registry auth)
* Easy file injection API (`container.#Image.files` ?)
* Use file injection instead of inline for `#Command.script` (to avoid hitting arg character limits)
* Organize universe packages in sub-categories?
* Are there runtime limitations in….
     * using hidden fields `_foo` as part of the DAG?
     * using `if` statements as part of the DAG?
     * using inlined Cue expressions as part of the DAG?
* Do we really need CUE definitions? cue/cmd doesn’t need them… This one is 💣🔥, pure speculation. We must pursue the best DX wherever that may lead us!
* Readability of error messages
  * At a minimum don’t make it worse!
  * Small improvements are good (eg. 
* Make sure we don’t make error messages LESS readable
* Add input.params as proposed by Richard
* Combining all container operations under an opinionated universe.dagger.io/docker package: [good or bad idea](https://github.com/dagger/dagger/pull/1117#discussion_r765178454)?
* [Outstanding questions on proxy features](https://github.com/dagger/dagger/pull/1117#discussion_r765211280)
* [Outstanding questions on #Stream and emulating unix pipes with them](https://github.com/dagger/dagger/pull/1117#discussion_r766145864)
* [Outstanding questions on engine.#Pull and information loss](https://github.com/dagger/dagger/pull/1117#discussion_r765219049)
* [Outstanding questions on global registry auth scope in buildkit](https://github.com/dagger/dagger/pull/1117#discussion_r765963051)
* [Outstanding questions on platform key](https://github.com/dagger/dagger/pull/1117#discussion_r766085610)
