# CI

This dagger module is used to define the CI for the dagger project itself,
including building and releasing the CLI, engine and SDKs themselves.

Available functionality:

    $ dagger functions
    Name     Description
    cli      -
    dev      Creates a dev container that has a running CLI connected to a dagger engine
    docs     -
    engine   -
    sdk      -
    test     -

> [!NOTE]
>
> For best results, use the same version of dagger (both the CLI, and the
> engine) as defined in [`.github/workflows/_hack_make.yml`](../.github/workflows/_hack_make.yml).
> Without this, you may hit unexpected errors or other weird behavior.

## Developing after a fresh clone

If you want to develop this module following a repository clone, remember to
run the following one-off command:

    cd ..
    dagger develop --sdk go --source ci

This will re-create all the files required by your code editor - see
`.gitignore` for a list of what they are.

## Tests

Run all tests:

    dagger call --source=. test all

Run a specific test (e.g. `TestModuleNamespacing`):

    dagger call --source=. test custom --run="^TestModuleNamespacing" --pkg="./core/integration"

## Dev environment

Start a little dev shell with dagger-in-dagger:

    dagger call --source=. dev

## Engine & CLI

### Linting

Run the engine linter:

    dagger call --source=. engine lint

### Build the CLI

Build the CLI:

    dagger call --source=. cli file export --path=./dagger

### Run the engine service

Run the engine as a service:

    dagger call --source=. engine service --name=dagger-engine up --ports=1234:1234

Connect to it from a dagger cli:

    export _EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://0.0.0.0:1234
    dagger call -m github.com/shykes/daggerverse/hello@main hello
    # hello, world!

## Docs

Lint the docs:

    dagger call --source=. docs lint

Auto-generate docs components:

    dagger call --source=. docs generate export --path=.

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

    dagger call --source=. sdk <sdk> lint

### Tests

Run SDK tests (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=. sdk <sdk> test

### Generate

Generate SDK static files (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=. sdk <sdk> generate export --path=.

### Publish

Dry-run an SDK publishing step (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=. sdk <sdk> publish --dry-run

### Bump

Bump an SDK version for releasing (replace `<sdk>` with one of the supported SDKs):

    dagger call --source=. sdk <sdk> bump --version=$VERSION export --path=.
