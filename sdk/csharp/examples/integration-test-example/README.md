# Integration Test Example

An xUnit test project that starts its own dependency, a PostgreSQL database, as a
Dagger service from a test fixture. There is no module and no wrapper script: the
tests use the SDK as a **client library**.

The same `dotnet test` works in three places:

- **On a developer machine or in an IDE test runner.** No session is provided, so the
  client starts one itself and ends it when the test process exits. It downloads a
  matching Dagger CLI on first use; only a container runtime has to be installed.
- **Under `dagger run dotnet test`.** The client uses the session of `dagger run`,
  and the service shows up in its progress output.
- **Inside a Dagger container**, for example in a function that runs
  `WithExec(["dotnet", "test"])`. The client uses the session of that exec, so the
  database appears in the same trace as the pipeline that runs the tests.

## Running This Example

```bash
dotnet test
```

## What This Shows

- A fixture ([PostgresFixture.cs](./PostgresFixture.cs)) that starts a container as a
  service once per test class and stops it afterwards
- `Dag.Host().Tunnel(service)` to reach the service on a free local port, so two
  test runs on one machine do not compete for a fixed port
- A test ([PostgresTest.cs](./PostgresTest.cs)) that talks to the database with a
  normal driver (Npgsql)

The project uses xUnit v3. Only the lifetime interface of the fixture (`IAsyncLifetime`)
is xUnit's; the same code works from the setup hooks of MSTest or NUnit.
