# Developing Dagger with Dagger

This Dagger module defines pipelines to develop the Dagger itself, including building and releasing the CLI, engine and SDKs.

## Dagger 101: functions, pipelines, modules

In Dagger, a pipeline is a sequence of containerized functions, each passing its output to the next. Unlike typical CI and build tools, functions are composed *dynamically*. This can be done from the command-line with `dagger call`.

For example `dagger call foo bar baz` will call 3 functions (`foo`, `bar` and `baz`) and connect them into a pipeline. The output of the last function (in this case, `baz`) will be printed to the terminal.

A module is a collection of functions (and types) which can be used by `dagger call`. By default, the current module will be infered from your working directory, but you can override this with `-m`.

To discover available functions in a given module: `dagger functions`.

With that in mind: this document includes examples of *typical pipelines* that are useful while developing Dagger. But remember, you are free to compose your own pipelines, either by modifying the examples, of starting from scratch. This flexibility is one of the killer features of Dagger.

## Running pipelines in a local checkout

All the pipelines in this document can be run against a local or remote source directory. We will assume a local checkout, unless explicitly stated otherwise.

To keep the example commands shorter, let's use a convenience environment variable to designate the current git repository as the source.

  # Run anywhere inside a local checkout of github.com/dagger/dagger
  export SRC="$(git rev-parse --show-toplevel):default"

That variable has no meaning outside of this document, it is for convenience only.

## Tests

Run all tests:

    dagger call --source="$SRC" test all

Run a specific test (e.g. `TestModuleNamespacing`):

    dagger call --source="$SRC" test custom --pkg="./core/integration" --run="^TestModule/TestNamespacing"

## Dev shell

Start a dev shell with dagger-in-dagger:

    dagger call --source="$SRC" dev terminal

## Engine & CLI

### Linting

Run the engine linter:

    dagger call --source="$SRC" engine lint

### Build the CLI

Build the CLI:

    dagger call --source="$SRC" cli file -o ./bin/dagger

### Run the engine service

Run the engine as a service:

    dagger call --source="$SRC" engine service --name=dagger-engine up --ports=1234:1234

Connect to it from a dagger cli:

    export _EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://0.0.0.0:1234
    dagger call -m github.com/shykes/daggerverse/hello@main hello
    # hello, world!

## Docs

Lint the docs:

    dagger call --source="$SRC" docs lint

Auto-generate docs components:

    dagger call --source="$SRC" docs generate -o .

## SDKs

### List available SDKs

    dagger functions sdk


All SDKs have the same functions defined:

- `lint`: lints SDK-specific files
- `test`: tests SDK functionality against a dev engine
- `generate`: generates any auto-generated files against a dev engine
- `bump`: bumps the SDK version number
- `publish`: publishes the SDK to a registry
    - Note: options for this function are SDK-specific

### Linting

Run an SDK linter (replace `<sdk>` with one of the supported SDKs):

    dagger call --source="$SRC" sdk <sdk> lint

### Tests

Run SDK tests (replace `<sdk>` with one of the supported SDKs):

    dagger call --source="$SRC" sdk <sdk> test

### Generate

Generate SDK static files (replace `<sdk>` with one of the supported SDKs):

    dagger call --source="$SRC" sdk <sdk> generate export --path=.

### Publish

Dry-run an SDK publishing step (replace `<sdk>` with one of the supported SDKs):

    dagger call --source="$SRC" sdk <sdk> publish --dry-run

### Bump

Bump an SDK version for releasing (replace `<sdk>` with one of the supported SDKs):

    dagger call --source="$SRC" sdk <sdk> bump --version=$VERSION export --path=.


## Contributing to this module

> [!NOTE]
>
> This step is only required for make changes to the module itself, it's
> not required to run tests/linting/etc.

If you want to develop this module, remember to run the following one-off command after a repository clone:

```
dagger develop
```

This will re-create all the files required by your code editor - see `.gitignore` for a list of what they are.
