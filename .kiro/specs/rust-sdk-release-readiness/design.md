# Design Document

## Overview

Release readiness becomes a small source-to-artifact path:

```text
target.json + schema.json
          |
          v
dagger-codegen -> checked Rust source -> compact ownership manifest
          |
          +-> normal Cargo checks and two public .crate packages
          |
          +-> Rust engine content -> complete linux/amd64 engine
                                      |
                                      v
                              one external consumer
```

The former completeness path joined this flow to inventories, ledgers, authority
digests, mappings, evidence registries, closure observations, reports, and a pinned
baseline harness. None is required to generate source, validate the public packages,
assemble engine content, or run the external consumer. The design deletes that parallel
graph instead of refreshing it.

## Architecture

### 1. Neutral codegen inputs

Move the two genuine checked inputs:

```text
sdk/rust/completeness/target.json          -> sdk/rust/codegen/target.json
sdk/rust/completeness/snapshots/schema.json -> sdk/rust/codegen/schema.json
```

All compile-time includes, tests, source-policy checks, engine adapter reads, and Dagger
workspace filters use the new paths. The target continues to bind the exact engine
version, revision, schema digest, and Rust toolchain. This is compatibility identity,
not release evidence.

### 2. Compact generated ownership

`dagger-bootstrap::generate::publish::ArtifactManifest` already models exactly what the
publisher needs:

```rust
struct ArtifactManifest {
    format_version: u32,
    target_revision: String,
    schema_digest: String,
    artifacts: BTreeMap<ArtifactPath, ArtifactRecord>,
}
```

Generation changes from:

```text
target + schema + ledger + mappings + previous extended manifest
  -> generated source + capability/evidence binding manifest
```

to:

```text
target + schema + previous compact ownership manifest
  -> generated source + compact ownership manifest
```

The manifest moves to `sdk/rust/codegen/generated.json`. The existing formatter,
provenance header validation, complete owned-path comparison, symlink defenses,
publication journal, rollback, obsolete-file retirement, and failure-injection tests
remain. Only the capability/evidence join and its diagnostics disappear.

### 3. Ordinary Rust build checker

Move `dagger-rust-sdk-check` from the deleted completeness crate into
`dagger-bootstrap/src/bin/`. Move `ordinary_build.rs` beside the bootstrap tests and
update its relative module path. The checker retains three commands:

- `packages`: validate Cargo metadata and exactly two package archives;
- `engine`: validate selected Rust manifest identity and blob presence;
- `unpack`: safely unpack the two packages for the external consumer.

The move is intentionally mechanical. It adds no policy registry and keeps the two
existing property-based tests.

### 4. Dagger development module

Delete `completeness.go`, then regenerate Dagger bindings. Remove these obsolete public
operations:

- completeness integrity;
- completeness artifact rendering;
- pinned completeness harness;
- core conformance evidence production; and
- complete engine evidence production.

Keep `GeneratedClientCheck`, but remove its completeness binding test. Keep `Resolution`,
`EngineIntegration`, `Build`, and `Verify`. `EngineContent` no longer reads
`engine-integration-mappings.json` or carries mapping/target fields used only by
`EngineEvidence`. Direct engine-integration results remain useful development tests,
but are not admitted to a registry.

Build compiles the relocated checker from `dagger-bootstrap` and otherwise retains its
current package, Rust engine-content, complete-engine, and consumer graph.

### 5. Workspace and documentation cleanup

Remove the completeness workspace dependency and lockfile package. Add `flate2`, `tar`,
and `toml_edit` to `dagger-bootstrap` because the relocated checker uses them.

Delete the completed Features 1–7 child specifications after verifying that their
enduring capability requirements remain in the umbrella specification. Git history
retains their delivery designs, tasks, evidence, and checkpoints. Remove the obsolete
Feature 1 completeness contract and registry/release-gate language from the umbrella,
then update current Rust docs for the checked inputs and compact ownership manifest.
Preserve:

- public client and module capabilities;
- exact target compatibility;
- verified CLI downloads;
- credential-safe diagnostics;
- immutable dependency selection;
- operation manifests and generated-file atomicity;
- focused engine tests; and
- the manual, directly authorized GitHub Release boundary.

## Data Boundaries

| Boundary | Inputs | Outputs | Side effects |
|---|---|---|---|
| Codegen check | target, schema, committed source/manifest | drift result | none |
| Codegen update | target, schema, committed source/manifest | generated source, compact manifest | failure-atomic local replacement |
| Cargo checks | Rust workspace | normal test/check result | local build cache only |
| Ordinary_Build | exact source commit, `linux/amd64` | two packages, Rust content, complete engine | Dagger graph/cache only |
| Ordinary_Verification | packages, complete engine, consumer fixture | terminal success/failure | isolated container/service only |
| Manual release | downloaded artifacts | GitHub Release attachments | forbidden without direct authorization |

## Correctness Properties

### Property 1: Public package closure is exact

*For any* package metadata and archive set, validation succeeds if and only if the public
set is exactly `dagger-sdk-macros` and `dagger-sdk` at one version with canonical roots,
required files/features, and the exact macro dependency.

The existing 256-case Proptest remains with the relocated checker.

**Validates:** Requirements 3.2, 3.4, 3.5

### Property 2: Complete-engine Rust manifest selection is exact

*For any* expected digest, selected digest, and blob set, validation succeeds if and only
if both digests are equal canonical SHA-256 values and the selected blob exists,
regardless of unrelated standard SDK blobs.

The existing 256-case Proptest remains with the relocated checker.

**Validates:** Requirements 3.3, 3.5

### Property 3: Generated publication is failure-atomic

*For any* publication interruption checkpoint, update either commits the complete new
generated set and manifest or restores the complete prior set.

Existing `dagger-bootstrap` generation property tests remain. Ledger/mapping mutation
strategies are deleted because those inputs no longer exist; ownership, path confinement,
drift, obsolete-file, formatter, and failure-schedule strategies remain.

**Validates:** Requirements 1.3, 1.4, 1.5

## Error Handling

All failures remain ordinary typed diagnostics or command failures. There is no admitted
evidence state, verdict, closure report, or digest refresh path. A failing check stops the
build; an artifact from another commit cannot satisfy it.

## Verification Strategy

### Engine-free checks

- `cargo fmt --all -- --check`
- `cargo check --workspace --all-features --locked`
- `cargo test --workspace --all-features --locked`
- no-default-feature SDK test
- Clippy, rustdoc, and deny checks
- read-only `dagger-rust generate --check`
- focused direct Go tests for the Rust adapter and engine integration helpers

### Engine-backed acceptance

On the documented `dagger-rust-builder-xl` Namespace devbox, from one exact detached
clean commit:

1. build exact CLI/help context and revalidate Docker/runner state;
2. invoke `Build` with explicit `linux/amd64`;
3. export exactly two `.crate` packages and the complete engine OCI archive;
4. invoke `Verify` for one external Rust consumer;
5. create and verify `SHA256SUMS`;
6. download all outputs and independently rehash them; and
7. remove only the owned activity marker, then pause or stop the devbox as requested.

No TUF, Action, crates.io operation, tag, or GitHub Release is part of verification.
