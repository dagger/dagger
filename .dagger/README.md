# Developing Dagger with Dagger

This Dagger module defines pipelines to develop the Dagger itself, including
building and releasing the CLI, engine and SDKs.

Also see [../hack/](../hack) for additional higher-level helpers to create an
interactive local dev environment.

## Dagger 101: functions, pipelines, modules

In Dagger, a pipeline is a sequence of containerized functions, each passing
its output to the next. Unlike typical CI and build tools, functions are
composed _dynamically_. This can be done from the command-line with `dagger
call`.

For example `dagger call foo bar baz` will call 3 functions (`foo`, `bar` and
`baz`) and connect them into a pipeline. The output of the last function (in
this case, `baz`) will be printed to the terminal.

A module is a collection of functions (and types) which can be used by `dagger
call`. By default, the current module will be inferred from your working
directory, but you can override this with `-m`.

To discover available functions in a given module: `dagger functions`.

With that in mind: this document includes examples of _typical pipelines_ that
are useful while developing Dagger. But remember, you are free to compose your
own pipelines, either by modifying the examples, of starting from scratch. This
flexibility is one of the killer features of Dagger.

## Tests

Run all tests:

    dagger call test all

Run a specific test (e.g. `TestNamespacing` in the `TestModule` suite):

    dagger call test specific --pkg="./core/integration" --run="^TestModule/TestNamespacing$"

## Dev shell

Start a dev shell with dagger-in-dagger:

    dagger call dev terminal

## Engine & CLI

### Linting

Run the engine linter:

    dagger call engine lint

### Build the CLI

Build the CLI:

    dagger call cli binary -o ./bin/dagger

### Run the engine service

Run the engine as a service:

    dagger call engine service --name=dagger-engine up --ports=1234:1234

Connect to it from a dagger cli:

    export _EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://0.0.0.0:1234
    dagger call -m github.com/shykes/daggerverse/hello@main hello
    # hello, world!

## Code Generation

In core/schema, changes utilizing the dagql package modify the engine's GraphQL API. API documentation and SDK bindings must be generated and committed when modifying the schema. See "Docs" and "SDKs" below for more granular generation functionality. This command also runs go generate for engine code.

    dagger call generate export --path=.

> [!NOTE]
>
> For `PHP` and `Elixir` SDKs it's important to manually delete the generated folder before running
> this command


## Docs

Lint the docs:

    dagger call docs lint

Auto-generate docs components:

    dagger call docs generate -o .

    

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

    dagger call sdk <sdk> lint

### Tests

Run SDK tests (replace `<sdk>` with one of the supported SDKs):

    dagger call sdk <sdk> test

### Generate

Generate SDK static files (replace `<sdk>` with one of the supported SDKs, or "all" for all of them):

    dagger call sdk <sdk> generate export --path=.

If you've made changes to the GraphQL schema, you will need to generate all sdks in one go prior to committing:

    dagger call sdk all generate export --path=.

> [!NOTE]
>
> For `PHP` and `Elixir` SDKs it's important to manually delete the generated folder before running
> this command

### Publish

Dry-run an SDK publishing step (replace `<sdk>` with one of the supported SDKs):

    dagger call sdk <sdk> publish --dry-run

### Bump

Bump an SDK version for releasing (replace `<sdk>` with one of the supported SDKs):

    dagger call sdk <sdk> bump --version=$VERSION export --path=.

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
