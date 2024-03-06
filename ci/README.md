# CI

Available functionality:

    $ dagger functions
    Name             Description
    cli              -
    dev              -
    engine           -
    sdk              -
    source           -
    test             Test runs Engine tests
    test-important   TestImportant runs Engine Container+Module tests, which give good basic coverage
    test-race        TestRace runs Engine tests with go race detector enabled

## Tests

Run tests:

    $ dagger call --source=. test

## Dev environment

Start a little dev shell with dagger-in-dagger:

    $ dagger call --source=. dev

## Engine

### Linting

Run the engine linter:

    $ dagger call --source=. engine lint

### Build the CLI

Build the CLI:

    $ dagger call --source=. cli file export --path=./dagger

### Run the engine service

Run the engine as a service:

    $ dagger call --source=. engine service --name=dagger-engine up --ports=1234:1234
    
Connect to it from a dagger cli:

    $ export _EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://0.0.0.0:1234
    $ dagger query
    Error: make request: returned error 422 Unprocessable Entity: {"errors":[{"message":"no operation provided","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}
    
### Publish the engine image

WIP

## SDKs

### Language-specific

### Linting

Run the Go SDK linter:

    $ dagger call --source=. sdk go lint

### Tests

Run the Go SDK tests:

    $ dagger call --source=. sdk go test

### Generate

Run the Go SDK static files:

    $ dagger call --source=. sdk go generate export --path=.

### Publish

Run the Go SDK publishing step (dry run):

    $ dagger call --source=. sdk go publish --dry-run

### Bump

Run the Go SDK bump step for releasing: 

    $ dagger call --source=. sdk go bump --version=$VERSION export --path=.
