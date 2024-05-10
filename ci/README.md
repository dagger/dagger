# CI

This dagger module is used to define the CI for the dagger project itself,
including building and releasing the CLI, engine and SDKs themselves.

Available functionality:

    $ dagger functions
    Name      Description
    cli       Develop the Dagger CLI
    dev       Creates a dev container that has a running CLI connected to a dagger engine
    docs      Develop the Dagger documentation
    engine    Develop the Dagger engine container
    helm      Develop the Dagger helm chart
    sdk       Develop Dagger SDKs
    test      Run all tests
    version   -

> [!NOTE]
>
> For best results, use the same version of dagger (both the CLI, and the
> engine) as defined in [`.github/workflows/_hack_make.yml`](../.github/workflows/_hack_make.yml).
> Without this, you may hit unexpected errors or other weird behavior.

## Developing after a fresh clone

> [!NOTE]
>
> This step is only required for make changes to the ci/ module itself, it's
> not required to run tests/linting/etc.

If you want to develop this module following a repository clone, remember to
run the following one-off command:

    cd ..
    dagger develop

This will re-create all the files required by your code editor - see
`.gitignore` for a list of what they are.

## Tests

Run all tests:

    dagger call --source=.:default test all

Run a specific test (e.g. `TestModuleNamespacing`):

    dagger call --source=.:default test custom --pkg="./core/integration" --run="^TestModuleNamespacing"

## Dev environment

Start a little dev shell with dagger-in-dagger:

    dagger call --source=.:default dev

## Engine & CLI

### Linting

Run the engine linter:

    dagger call --source=.:default engine lint

### Build the CLI

Build the CLI:

    dagger call --source=.:default cli file -o ./bin/dagger

### Run the engine service

Run the engine as a service:

    dagger call --source=.:default engine service --name=dagger-engine up --ports=1234:1234

Connect to it from a dagger cli:

    export _EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://0.0.0.0:1234
    dagger call -m github.com/shykes/daggerverse/hello@main hello
    # hello, world!

## Docs

Lint the docs:

    dagger call --source=.:default docs lint

Auto-generate docs components:

    dagger call --source=.:default docs generate export --path=.

## SDKs

Available SDKs:

    dagger functions sdk
    # Name         Description
    # elixir       -
    # go           -
    # java         -
    # php          -
    # python       -
    # rust         -
    # typescript   -

All SDKs have the same functions defined:

- `lint`: lints SDK-specific files
- `test`: tests SDK functionality against a dev engine
- `generate`: generates any auto-generated files against a dev engine
- `bump`: bumps the SDK version number
- `publish`: publishes the SDK to a registry
    - Note: options for this function are SDK-specific

### Linting

Run an SDK linter (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=.:default sdk <sdk> lint

### Tests

Run SDK tests (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=.:default sdk <sdk> test

### Generate

Generate SDK static files (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=.:default sdk <sdk> generate export --path=.

### Publish

Dry-run an SDK publishing step (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=.:default sdk <sdk> publish --dry-run

### Bump

Bump an SDK version for releasing (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=.:default sdk <sdk> bump --version=$VERSION export --path=.
