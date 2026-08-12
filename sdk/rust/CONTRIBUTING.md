# Contributing to the Dagger Rust SDK

Thanks for helping improve the Rust SDK. Read the repository-wide
[contribution guide](../../CONTRIBUTING.md), the shared
[SDK guide](../CONTRIBUTING.md), and the Rust-specific
[agent guidance](AGENTS.md) before making a substantial change.

For substantial features or public API changes, open an issue or discussion before
implementation. This is especially important while module support and the path to a
stable Rust SDK are being designed.

## Design direction

The Rust SDK should provide the same capabilities and observable behaviour as the Go
SDK without mechanically copying its API shape. The engine schema defines the public
wire surface, target-compatible `sdk-sdk` checks define common lifecycle behaviour
within their bounded scope, and the pinned Go SDK defines behaviour outside that scope
while remaining reference evidence for overlap. Established Rust conventions determine
ownership, error handling, naming, safety, and ergonomics.

These are peer authorities, not a global precedence list. Genuine incompatibility must
remain visible and reviewed. See the executable
[completeness contract](completeness/README.md) for the pinned source selections,
classification model, staged refresh flow, and evidence requirements.

Historical Rust implementations and proposals, including pull request #12229, are
useful evidence. They do not override the current engine contract, Go SDK behaviour,
or a better idiomatic Rust design.

## Repository layout

- `crates/dagger-sdk` contains the public SDK, connection management, query builder,
  and generated client.
- `crates/dagger-codegen` converts the engine's GraphQL schema into Rust types and
  client methods.
- `crates/dagger-bootstrap` provides the code-generation entry point.
- `crates/dagger-sdk/src/gen/` and the `core_projection`/`core_reachability` integration
  tests are generated. Change the generator or templates, then regenerate them; do not
  edit them directly.
- `examples` contains executable examples and example applications.
- `../../toolchains/rust-sdk-dev` contains the Dagger-based development, generation,
  test, and release automation for this SDK.
- `completeness` contains authored target and ledger inputs plus reproducible derived
  artifacts for the Go-level completeness programme.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the existing component overview.
The focused built-in SDK build, case, and evidence procedure is in
[ENGINE_INTEGRATION.md](ENGINE_INTEGRATION.md).
The module authoring contract, direct harness, and deferred exact-target case inventory
are in [MODULE_AUTHORING.md](MODULE_AUTHORING.md).
The standalone-client compiler, project, generated API, ownership, checkpoint, and
deferred sign-off workflow is in [CLIENT_GENERATION.md](CLIENT_GENERATION.md).
The complete checked-target refresh and release procedure is in
[MAINTAINING.md](MAINTAINING.md).

## Rust toolchains

The repository pins the development toolchain in `rust-toolchain.toml`. Rustup uses it
automatically when commands run from this directory.

The current baseline is Rust 1.97.1 for development, CI, and the MSRV, with Rust
edition 2024. The Rust SDK deliberately targets a modern compiler rather than carrying
forward its historical Rust 1.77 constraint.

The workspace also declares a minimum supported Rust version (MSRV). These are separate
contracts:

- the pinned development toolchain keeps formatting, linting, and CI reproducible;
- the MSRV describes the oldest compiler supported by published crates.

Advancing the development toolchain does not by itself raise the MSRV. Raise the MSRV
only in a deliberate compatibility change with corresponding CI, documentation, and
release notes.

## Development workflow

From `sdk/rust`:

```console
cargo fmt --all --check
cargo check --workspace --all-features --locked
cargo test --workspace --all-features --locked
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
RUSTDOCFLAGS="-D warnings" cargo doc --workspace --all-features --no-deps --locked
cargo deny check
```

Use focused tests while iterating, but run the complete relevant set before submitting
a pull request. Module-authoring and standalone-client checkpoints are explicitly
engine-free: use the production compilers, generated fixtures, recording transport,
and typed checkpoint/evidence suites directly through Cargo. Reuse checked generated
assets unless an owning source, schema, target, or generator digest changed. The
standalone-client fixture materializes one SDK baseline and fans out isolated Cargo
projects from it; investigate fixture sequencing and its owning input before a broader
rerun.

An engine is not a local checkpoint fallback. If a contract cannot be represented by
the direct production harness, document the precise model gap and smallest proposed
sign-off case for maintainer approval. Exact-engine cases run only through the bounded
SDK sign-off workflow documented in [MODULE_AUTHORING.md](MODULE_AUTHORING.md).
Standalone-client exact-engine cases follow the same separation and remain the
deferred five-case inventory in [CLIENT_GENERATION.md](CLIENT_GENERATION.md).

For non-module work whose owning contract genuinely requires a running Dagger engine,
the relevant repository checks may be run through Dagger:

```console
dagger check '*sdk:*test*'
```

Run that command from the repository root.

## Generated code

During generator development, run the direct read-only check from `sdk/rust`:

```console
cargo run -p dagger-bootstrap --bin dagger-rust --locked -- \
  generate --workspace . --check
```

Use `--update` only when intentionally publishing the complete generator-owned output
set. Before submitting, run repository generation from the repository root:

```console
./hack/with-dev ./bin/dagger generate -y rust-sdk:apiclient
```

Generation must be deterministic. Include generated changes that result from an
intentional generator or schema change, and inspect them as part of review. Do not
hide unrelated generated churn in a feature commit. The unscoped `dagger generate -y`
runs every generator registered by the repository workspace and is not required for a
Rust-only change.

## Testing parity

When adding a capability represented in the Go SDK, identify and port its behavioural
and integration tests. Preserve the assertions and edge cases that define the
behaviour, while expressing setup and API usage idiomatically in Rust.

New public behaviour needs tests at the lowest useful level:

- code-generator tests for generated API shape;
- query-builder or unit tests for local semantics;
- engine-backed integration tests for end-to-end behaviour;
- examples or documentation for important user-facing workflows.

For module authoring, “integration” normally means the engine-free production harness:
real generated assets, registry, codecs, context, dispatcher, and result sink over a
recording Rust transport. Engine registration, runtime-container construction, and
real engine ID behaviour remain SDK-sign-off observations and cannot replace the
direct compiler/dispatch closure.

## Pull requests

- Keep each pull request to one coherent change.
- Explain what changed, why the design is idiomatic Rust, and how parity was verified.
- List the commands actually run and explain any omissions.
- Run `./hack/with-dev ./bin/dagger generate -y rust-sdk:apiclient` and include
  intentional generated output.
- Run the relevant format, check, test, and lint commands.
- Keep `cargo deny check` green for advisories, licenses, dependency bans, and sources.
- Add a Changie fragment for user-facing changes as required by the root contribution
  guide. From this directory, `changie new` uses the Rust SDK changelog configuration.
- Sign every commit under the Developer Certificate of Origin with `git commit -s`.
  The `Signed-off-by` identity is the human Git author.
- Credit agent authorship and assistance using the trailers defined in
  [AGENTS.md](AGENTS.md#commit-attribution--recognise-agent-work-required).
