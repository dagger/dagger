# Europa Universe

## About this directory

`europa-universe/` is a staging area for the upcoming `universe.dagger.io` package namespace,
which will be shipped as part of the [Europa release](https://github.com/dagger/dagger/issues/1088).

## What is Universe?

The Dagger Universe is a catalog of reusable Cue packages, curated by Dagger but possibly authored by third parties. Most packages in Universe contain reusable actions; some may also contain entire configuration templates.

The import domain for Universe will be `universe.dagger.io`. It will deprecate the current domain `alpha.dagger.io`.

## Where is the `dagger` package?

Europa will also introduce a new package for the Dagger Core API: `dagger.io/dagger`.
This is a core package, and is *not* part of Universe (note the import domain).

The development version of the Europa core API can be imported as [alpha.dagger.io/europa/dagger](../stdlib/europa/dagger).

## Where is the `dagger/engine` package?

Europa will also introduce a new package for the Low-Level Dagger Engine API : `dagger.io/dagger/engine`.
This is a core package, and is *not* part of Universe (note the import domain).

The development version of the Europa Low-Level Engine API can be imported as either:

* [alpha.dagger.io/europa/dagger/engine/spec/engine](../stdlib/europa/dagger/engine/spec/engine) for the full spec
* [alpha.dagger.io/dagger/europa/engine](../stdlib/europa/dagger/engine) for the implemented subset of the spec

## Universe vs other packages

This table compares Dagger core packages, Dagger Universe packages, and the overall CUE package ecosystem.

|   |  *Dagger core* | *Dagger Universe* | *CUE ecosystem* |
|---|----------------|-------------------|-----------------|
| Import path |  `dagger.io` | `universe.dagger.io` | Everything else |
| Purpose |  Access core Dagger features | Safely reuse code from the Dagger community | Reuse any CUE code from anyone |
| Author | Dagger team | Dagger community, curated by Dagger | Anyone |
| Release cycle |    Released with Dagger engine   |  Released continuously | No release cycle |
| Size |  Small  | Large | Very large |
| Growth rate | Grows slowly, with engine features | Grows fast, with Dagger community | Grows even faster, with CUE ecosystem |


## Notable packages

### Docker API

*Import path: [`universe.dagger.io/docker`](./universe/docker)*

The `docker` package is a native Cue API for Docker. You can use it to build, run, push and pull Docker containers directly from Cue.

The Dagger container API defines the following types:

* `#Image`: a container image
* `#Run`: run a command in a container
* `#Push`: upload an image to a repository
* `#Pull`: download an image from a repository
* `#Build`: build an image

### Examples

*Import path: [`universe.dagger.io/examples`](./examples)*

This package contains examples of complete Dagger configurations, including the result of following tutorials in the documentations.

For example, [the todoapp example](./examples/todoapp) corresponds to the [Getting Started tutorial](https://docs.dagger.io/1003/get-started/)


## TODO LIST

* Support native language dev in `docker.#Run` with good DX (Python, Go, Typescript etc.)
* Coding style. When to use verbs vs. nouns?
* Easy file injection API (`container.#Image.files` ?)
* Use file injection instead of inline for `#Command.script` (to avoid hitting arg character limits)
* Organize universe packages in sub-categories?
