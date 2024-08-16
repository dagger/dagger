---
cwd: ..
terminalRows: 16
---

# Developing Dagger with Dagger

This Dagger module defines pipelines to develop the Dagger itself, including building and releasing the CLI, engine and SDKs.

## Dagger 101: functions, pipelines, modules

In Dagger, a pipeline is a sequence of containerized functions, each passing its output to the next. Unlike typical CI and build tools, functions are composed *dynamically*. This can be done from the command-line with `dagger call`.

For example `dagger call foo bar baz` will call 3 functions (`foo`, `bar` and `baz`) and connect them into a pipeline. The output of the last function (in this case, `baz`) will be printed to the terminal.

A module is a collection of functions (and types) which can be used by `dagger call`. By default, the current module will be infered from your working directory, but you can override this with `-m`.

To discover available functions in a given module: `dagger functions`.

```sh {"id":"01J5Y82842KAER30RGCN96HH6M"}
dagger functions
```

With that in mind: this document includes examples of _typical pipelines_ that are useful while developing Dagger. But remember, you are free to compose your own pipelines, either by modifying the examples, of starting from scratch. This flexibility is one of the killer features of Dagger.

## Running pipelines in a local checkout

All the pipelines in this document can be run against a local or remote source directory. We will assume a local checkout, unless explicitly stated otherwise.

## Literally run this README

Be sure to install Runme first via homebrew on macOS.

```sh {"id":"01J5Y84809NWNP74YV0Y1MVFN4"}
brew install runme
```

Or install the VS Code extension that comes bundled with the runme binary.

```sh {"id":"01J5Y85Y7DVWMW8NB80BVNEFH7"}
code --install-extension stateful.runme
```

## Tests

Run all tests:

```sh {"id":"01J5EG1EXCHRB5J8ZEGZZXWE5H","name":"dev-dagger-test"}
dagger call --source=".:default" test all
```

Run a specific test (e.g. `TestModuleNamespacing`):

```sh {"id":"01J5EG1JNRB987MSB7SPM0EXGA"}
dagger call --source=".:default" test custom --pkg="./core/integration" --run="^TestModule/TestNamespacing"
```

## Dev shell

Start a dev shell with dagger-in-dagger:

```sh {"id":"01J5EG1QD02ZN97W42BMG57Q8A","name":"dev-dagger-terminal"}
dagger call --source=".:default" dev terminal
```

## Engine & CLI

### Linting

Run the engine linter:

```sh {"id":"01J5EG1VKBMG5X165Z7NMTX1ZZ","name":"dev-dagger-lint"}
dagger call --source=".:default" engine lint
```

### Build the CLI

Build the CLI:

```sh {"id":"01J5EFASD4HADA1XKEHPK27WJ4","name":"dev-dagger-cli"}
dagger call --source=".:default" cli binary -o ./bin/dagger
```

### Run the engine service

Run the engine as a service:

```sh {"background":"true","id":"01J5EFDBTC31PM0RKA1X3ZZM0F","name":"dev-engine-service"}
dagger call --source=".:default" engine service --name=dagger-engine up --ports=1234:1234
```

Connect to it from a dagger cli:

```sh {"id":"01J5EG220PWZVQREPZ7RWT8QRH","name":"dev-engine-hello"}
export _EXPERIMENTAL_DAGGER_RUNNER_HOST="tcp://0.0.0.0:1234"
dagger call -m github.com/shykes/daggerverse/hello@main hello
# hello, world!
```

## Docs

Lint the docs:

```sh {"id":"01J5EG2748AAH2FYQ8WXAWV9XY","name":"dev-docs-lint"}
dagger call --source=".:default" docs lint
```

Auto-generate docs components:

```sh {"id":"01J5EG2A6R51NYZ5RJX63P5NEF","name":"dev-docs-generate"}
dagger call --source=".:default" docs generate -o .
```

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

    dagger call --source=".:default" sdk <sdk> lint

### Tests

Run SDK tests (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=".:default" sdk <sdk> test

### Generate

Generate SDK static files (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=".:default" sdk <sdk> generate export --path=.

### Publish

Dry-run an SDK publishing step (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=".:default" sdk <sdk> publish --dry-run

### Bump

Bump an SDK version for releasing (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=".:default" sdk <sdk> bump --version=$VERSION export --path=.

## Contributing to this module

> [!NOTE]
>
> This step is only required for make changes to the module itself, it's
> not required to run tests/linting/etc.

If you want to develop this module, remember to run the following one-off command after a repository clone:

```sh {"id":"01J5EF8SWVX7KGC4YF6DXW5A7T"}
dagger develop
```

This will re-create all the files required by your code editor - see `.gitignore` for a list of what they are.
