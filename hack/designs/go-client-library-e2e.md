# Dagger-Managed Go Client-Library Tests

author: shykes
status: implemented
scope: Go client library first, shared bootstrap environment later

## Decision

Keep engine-dependent tests out of the published Go client-library module. They
live under `sdk/go/e2e`, a separate module. Both the outer harness and isolated
inner tests import the development client from the current source tree.

The information architecture reflects two different test contracts:

```text
sdk/go/e2e/
├── go.mod                              # isolated test dependencies
├── dagger.toml                         # outer orchestration modules
├── harness_test.go                     # shared project/engine setup
├── bootstrap_test.go                   # TestBootstrap
├── against_engines_test.go             # TestAgainstEngines
└── testdata/
    ├── bootstrap/
    │   └── bootstrap_inner_test.go
    └── against-engines/
        ├── client_inner_test.go
        └── dag/
            └── dag_inner_test.go
```

Directories below `testdata` are deliberately ignored by recursive Go package
discovery. A naive `go test ./...` in the outer module therefore runs only the
outer harness; inner tests run only after a harness has supplied the intended
development client, CLI, and engine.

## Test Contracts

### `TestBootstrap`

Client-library bootstrap means that the development library:

1. downloads a supplied development CLI archive;
2. verifies its checksum;
3. caches and executes it;
4. connects it to an already-running development engine; and
5. completes a real query, including concurrent cache reuse and stale-cache
   cleanup.

The outer harness builds and serves the development CLI, starts the development
engine, and launches
`testdata/bootstrap/bootstrap_inner_test.go` in a clean Go target. The target
has neither an inherited Dagger session nor `_EXPERIMENTAL_DAGGER_CLI_BIN`, so
the development library's real CLI download path is exercised.

The fixture is hermetic with respect to release infrastructure: it uses an
ephemeral HTTP asset service, not release uploads, the production CDN, or the
production engine registry.

Supplying an already-running engine is intentional. Pulling or starting an
engine through a container runner is CLI bootstrap and remains covered by
`core/integration/provision_test.go`.

### `TestAgainstEngines`

`TestAgainstEngines` is a matrix runner for ordinary engine-backed client
behavior. It currently has one `dev` matrix entry: a CLI and engine built from
the current source tree. It can later add released or compatibility engine
fixtures without reorganizing the inner suite.

The mapping is one outer test to many inner packages and files. Behavioral
granularity belongs inside `testdata/against-engines`; adding each inner test
does not require another outer harness. A separate outer test is warranted only
when setup or lifecycle semantics differ materially from the shared engine
matrix.

The engine-backed tests formerly in `sdk/go/client_test.go` and
`sdk/go/dag/client_test.go` run here as external-package tests. Engine-free tests
that need private access remain in the published module. `examples_test.go`
also remains there because Go uses its `ExampleXxx` functions as public package
documentation. Those functions intentionally omit Go's magic `// Output:`
directives: they remain documented and compile-checked, but a bare `go test`
does not execute their engine-dependent bodies.

## Separate Outer and Inner Modules

The module split prevents the orchestration dependency from leaking into the
published client library. The outer module imports the development client
without adding it to `sdk/go/go.mod`:

```text
require dagger.io/dagger v0.0.0
replace dagger.io/dagger => ..
```

Each target creates an ephemeral inner module with:

```text
require dagger.io/dagger v0.0.0
replace dagger.io/dagger => /sdk
```

For `TestBootstrap`, the development client discovers and downloads the CLI.
For `TestAgainstEngines`, the harness mounts the development CLI explicitly.
Both remove inherited session variables and point the CLI at the supplied
engine endpoint, so they behave the same when invoked from Dagger CI or a native
developer session.

Using the development client for outer orchestration couples the two test
layers. This is a temporary compatibility tradeoff: inherited clients cannot
yet select an API view independently from their outer session. Once
dagger/dagger#13701 is implemented, the outer module can return to a released
client.

The coupling has one visible cost. When no Dagger session is already
established, the client starts its own and downloads the CLI version named by
`sdk/go/engineconn/version.gen.go`; that version is unpublished between a
VERSION bump and its release, so export `_EXPERIMENTAL_DAGGER_CLI_BIN` to run in
that window. Runs that attach to an existing session are unaffected, since they
download nothing, and `TestBootstrap` always serves its own CLI archive.

## Project Integration

`sdk/go/e2e/dagger.toml` declares `toolchains/engine-dev`, which would normally
be enough for the engine to expose it in the session's API. It is not enough
here: the engine decides whether to include declared modules once, when it first
sets up a client's workspace, and caches that answer. `dagger api exec` sets the
client up with the built-in API only, and opting in afterwards does not re-run
the decision. The harness therefore loads the module itself, via
`CurrentWorkspace().ModuleSource()`, which reads the workspace files rather than
the session's API. It then calls the `engineDev` field with raw GraphQL and
converts returned core IDs into typed handles with `dagger.Ref`.
`go:test:include` directives make the project and development client sources
available to the generic Go test harness.

This plumbing should eventually move behind a project-specific Dagger module
with generated clients. Sharing setup should share artifact construction and
environment setup, not merge client-library bootstrap with CLI-bootstrap
coverage.

The entire `sdk/go/e2e` tree is excluded when the monorepo is filtered into the
standalone Go client-library repository. It is repository-owned integration
infrastructure, not part of the released library.

## Running

The generic Go test defaults include both `sdk/go` and `sdk/go/e2e`. Native
development commands, run from `sdk/go/e2e`, are:

```console
go test -timeout=30m -run '^TestBootstrap$' .
go test -timeout=30m -run '^TestAgainstEngines$' .
```

The explicit timeout matches the generic Go test runner and leaves enough time
to build the development CLI and engine from a cold cache before the inner test
timeout begins.

The outer package can be compiled and discovered without starting either inner
suite:

```console
go test -run '^$' ./...
```

## Published-Artifact Coverage

`.github/workflows/publish.yml` used to run `sdk/go`'s `TestProvision` on macOS
and Linux against the CLI tarball just uploaded for that commit or tag. Those
steps are removed here, along with the test they invoked.

Release integrity stays covered: the same jobs point Python and TypeScript at
the identical artifact, so a corrupt upload or a bad `checksums.txt` still fails.
What is no longer covered is Go's own download path against real inputs — the
published checksums file, CDN redirects, release tarball layout and permissions
— and macOS for Go at all. `TestBootstrap` exercises that logic more thoroughly
than the deleted test did, but only against a synthetic tarball it builds and
serves itself.

Restoring the workflow step is not an option: the test it invoked no longer
exists, so it would match nothing and pass vacuously. The runtime matrix below
closes the gap properly instead.

## Next Steps

1. Factor project-specific engine and CLI construction behind a generated
   project test-environment client.
2. Let inherited clients select their own API view (dagger/dagger#13701), then
   restore a released outer orchestration client.
3. Port Python and TypeScript client-library bootstrap tests to the same
   supplied-engine contract.
4. Evolve CLI bootstrap into a Dagger-managed runtime matrix on two independent
   axes: target platform (container, remote macOS over SSH) and CLI source
   (synthetic tarball, published artifact). The suite orchestrates its own
   targets, borrowing Dagger modules where they help — secret handling for SSH
   credentials, for example. Filling both axes closes the gap above and runs it
   per-PR rather than only on pushes to main.

## Acceptance Criteria

The Go migration is complete when:

- `dagger check golang:test-all` discovers the separate `sdk/go/e2e` module;
- native invocation works without an injected `DAGGER_SESSION_PORT`;
- `TestBootstrap` downloads, verifies, caches, and executes a JIT-built CLI;
- that CLI reaches a JIT-built, already-running engine and completes a query;
- concurrent connects and stale-cache cleanup remain covered;
- `TestAgainstEngines` runs development client tests through a development CLI
  against the development engine;
- ordinary outer `go test ./...` cannot accidentally run inner suites;
- ordinary `sdk/go` tests compile the documentation examples without requiring
  an engine;
- no test-only orchestration dependency enters `sdk/go/go.mod`;
- no `sdk/go/e2e` file enters the standalone client-library release; and
- `core/integration.TestProvision` remains unchanged as CLI-bootstrap coverage.

## Risks

| Risk | Mitigation |
|---|---|
| Client-library and CLI-bootstrap responsibilities become coupled again | Keep the supplied engine explicit and test runner provisioning only in core integration coverage. |
| An outer orchestration failure can come from the development client | Keep outer setup small and restore a released client after dagger/dagger#13701. |
| Recursive Go discovery runs inner tests without their fixtures | Keep every inner suite below `testdata`. |
| Engine compatibility needs duplicate harnesses | Grow the `TestAgainstEngines` matrix; keep behavior tests shared. |
| Public examples become an implicit engine dependency | Keep them in the library module without `// Output:` directives, so Go documents and compiles them without executing them. |
| Go's download path regresses against real release artifacts | Known gap, accepted until the runtime matrix grows a published-artifact axis. Python and TypeScript still validate the artifact itself on every affected job. |
