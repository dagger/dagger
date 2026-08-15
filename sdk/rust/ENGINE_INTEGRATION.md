# Rust SDK engine integration

This document is the maintainer contract for the private Rust compiler, engine adapter,
runtime image, and completed-engine verification workflow. Application lifecycle and
transport ownership are described in [ARCHITECTURE.md](ARCHITECTURE.md).

## Runtime build audit

The Rust runtime follows the observable build-safety contracts shared by established
Dagger SDKs while keeping implementation policy in Rust.

| Contract | Rust decision |
| --- | --- |
| Generation modes are explicit | Missing introspection selects checked committed generation; present introspection selects private legacy generation. Neither mode silently falls through. |
| Dependency resolution is locked | Runtime verification requires `Cargo.lock`, checks with `cargo metadata --locked`, and builds with `cargo build --locked`. |
| Mutable inputs arrive late | Schema, project, and request inputs are mounted after immutable tools and policy. |
| Build caches are not runtime state | Cargo registry, Git, and target caches remain on the builder. A fresh digest-pinned runtime receives only the stripped binary and runtime manifest. |
| Credentials cannot become artifacts | Control data, runtime identity, cache keys, and generated files contain no credentials. Unsafe Cargo output becomes a bounded typed diagnostic; no socket, secret, Cargo home, or cache enters the runtime. |
| Runtime shape is fixed | The image clears inherited arguments, installs one entrypoint, and uses the engine-owned work directory and platform target. |

`.dagger/modules/rust-client-dev/internal/enginefree` keeps this source-level audit
executable. If an owning build contract changes, update the test and review the Rust
decision rather than allowing silent drift.

## Verification boundaries

Use three deliberately separate boundaries:

1. While iterating, run the narrowest Cargo package, fixture, or direct Go ABI test.
2. Before artifact assembly, run the canonical Cargo and Go checks without a Dagger
   engine.
3. For release readiness, run the ordinary `Build` and `Verify` entry points. They
   package the two public crates, compose Rust content into a complete engine, and run
   one isolated external Rust consumer against that engine.

Focused engine cases remain diagnostic tools for engine-only behavior. They are not a
second release gate and do not replace the ordinary build.

## Engine-free development workflow

From `sdk/rust`, run the canonical Rust commands in `AGENTS.md`. Useful focused suites
include:

```console
cargo test -p dagger-codegen --test engine_operations --test visible_schema_properties --locked
cargo test -p dagger-sdk-engine --locked
```

The direct Go boundaries are also engine-free:

```console
cd ../../.dagger/modules/engine-dev
GOCACHE=/tmp/dagger-rust-go-cache go test . ./build -count=1

cd ../rust-client-dev
GOCACHE=/tmp/dagger-rust-go-cache go test . ./internal/enginefixture ./internal/enginefree -count=1

cd ../../sdk/rust/runtime
GOCACHE=/tmp/dagger-rust-go-cache go test ./internal/metadata -count=1
```

On macOS, compile the Linux-only root adapter packages for their engine platform:

```console
cd ../../..
GOOS=linux GOARCH=amd64 GOCACHE=/tmp/dagger-rust-go-cache \
  go test -c -o /tmp/dagger-rust-core-sdk.test ./core/sdk
GOOS=linux GOARCH=amd64 GOCACHE=/tmp/dagger-rust-go-cache \
  go test -c -o /tmp/dagger-rust-core-schema.test ./core/schema
GOOS=linux GOARCH=amd64 GOCACHE=/tmp/dagger-rust-go-cache \
  go test -c -o /tmp/dagger-rust-cli.test ./internal/cmd/dagger
```

Generation is not part of the continuous local loop. Refresh bindings only when their
owning module API or schema changes, inspect that scoped diff once, and return to direct
checks.

## Ordinary complete-engine build

`Build` performs no publication. It:

- packages exactly `dagger-sdk-macros` and `dagger-sdk` and validates package closure;
- builds the Rust SDK OCI content from the current workspace;
- composes that content into the standard complete `linux/amd64` engine;
- requires the normal engine and CLI binaries plus the selected Rust manifest; and
- retains the complete engine and package directory for explicit export.

`Verify` unpacks those packages into an isolated Cargo project, patches the macro
companion locally, starts this build's completed engine, executes one SDK query, and
closes the client cleanly. Export the two packages and complete engine only after
verification passes. Checksums cover the exported bytes.

The single authoritative Namespace procedure, including the fresh checkout,
AppleDouble guard, Docker CLI `PATH`, pinned runner, exact CLI build, exports,
checksums, retrieval, marker handling, and pause, is
[Namespace Rust SDK artifact build](NAMESPACE_BUILD.md). Do not reconstruct the
procedure from fragments in other documents.

## Immutable generated dependency

Generated Cargo projects accept either an exact registry version or a credential-free
HTTPS Git repository plus an exact lowercase 40-character revision. A path, branch,
tag-only coordinate, default branch, credential-bearing URL, query, fragment, malformed
revision, or unreachable commit fails before Cargo runs.

For Git-backed development, the selected revision must already be reachable from the
repository and contain byte-identical public SDK build inputs. Confirm the local and
remote revision, then pass the same repository and revision to every focused engine
content invocation. The current workspace identity and the generated project's
dependency identity are separate safety contracts; never substitute a mutable ref to
make resolution pass.

This immutable dependency mechanism protects generated projects. It does not publish a
crate, create a tag, or authorize a hosted release.

## Focused engine regressions

Run a focused case only when its boundary cannot be represented by the direct harness:

```console
./bin/dagger -m .dagger/modules/rust-client-dev call engine-unit
./bin/dagger -m .dagger/modules/rust-client-dev call \
  engine-content engine-integration --cases=operations
```

The closed case inventory covers built-in resolution; empty and existing project
initialization; no-generate behavior; library, module, client, and entrypoint
operations; checked and legacy runtime paths; lock/toolchain rejection; path ownership;
and credential-safe diagnostics. Multiple requested cases share one content object.

Before a focused run, describe the initial files, each mutation, schema source,
generation mode, runtime precondition, and expected owned paths. Assert those semantics
in `internal/enginefree` first. A multi-minute engine build is the final observation,
not the fixture-design loop.

The previously observed long-running module-query failure—
`Post "http://dagger/query": unexpected EOF` after roughly 30 seconds—is a separate,
unverified engine regression. Ordinary external-consumer verification does not exercise
that duration boundary, and Rust SDK release readiness does not claim it is fixed.

## Local triage and generated ownership

Start with the owning direct Cargo test and deterministic fixture. Repair generated
ownership through scoped generation; never delete caller-authored Cargo, source, VCS,
or workspace files. Missing or stale lockfiles are repaired by generation, never by an
unlocked runtime build.

Operation manifests, not filenames or Go symbols, own generated files. The runtime
manifest is finalized only after the stripped binary exists, so input validation cannot
fabricate its digest. Inspect the final image for the installed binary and manifest and
confirm that Cargo homes, caches, source, credentials, and builder state are absent.

When the development module's public API changes, preview and apply only that module's
generated Go bindings. Repository-wide generation is not part of an ordinary Rust
engine-integration checkpoint.
