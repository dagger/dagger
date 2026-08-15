# Maintaining the Dagger Rust SDK

This runbook covers generated-client ownership, target refresh, recovery, local
acceptance, and non-publishing artifact assembly. It has six ordered steps.

## 1. Confirm ownership and target identity

The core-schema generator owns only:

- `crates/dagger-sdk/src/gen/`;
- `crates/dagger-sdk/tests/core_projection.rs`;
- `crates/dagger-sdk/tests/core_reachability.rs`; and
- `codegen/generated.json`.

Generated Rust files carry a machine-readable source header. The binding manifest binds
the exact path set, byte and semantic digests, checked Dagger revision, and schema
digest. Never edit an owned output or derived manifest by hand. Fix schema validation,
projection, naming, documentation, rendering, or atomic update policy instead.

Before maintenance, confirm `codegen/target.json`, the workspace version, lockfile,
and pinned Rust toolchain agree with the intended target.

## 2. Check and update generated output

Run read-only generation from `sdk/rust`:

```console
cargo run -p dagger-bootstrap --bin dagger-rust --locked -- \
  generate --workspace . --check
```

After an intentional generator, schema, or target change, update the complete
owned candidate once:

```console
cargo run -p dagger-bootstrap --bin dagger-rust --locked -- \
  generate --workspace . --update
```

The update is failure-atomic and may replace only manifest-authorized paths. Run check
mode again and inspect `git diff --stat` and `git diff`. The diff must match the
binding manifest; unknown or authored content must remain unchanged.

From the repository root, the scoped integration fence is:

```console
./hack/with-dev ./bin/dagger generate -y rust-sdk:apiclient
```

It must leave the checked generated client unchanged. Do not substitute unscoped
workspace generation.

## 3. Refresh the target deliberately

A target refresh changes an immutable compatibility claim and is separate from ordinary
renderer work:

1. Update `codegen/target.json` with the exact Dagger version, full revision, schema
   digest, Rust SDK version, and Rust toolchain.
2. Capture the exact target engine schema as `codegen/schema.json`; do not substitute a
   nearby engine or hand-reserialize it.
3. Run the direct update, inspect the generated source and compact ownership-manifest
   diff, and repeat local acceptance.

Changed or removed schema coordinates fail closed until their generated and
compatibility policies are explicit. Never refresh a digest merely to make a check pass.

## 4. Recover or roll back as one unit

A validation, formatting, or cancellation failure leaves the previous generated set
intact. If review rejects a completed update, restore the whole owned set from the
reviewed pre-update commit or repeat in a clean worktree. Restoring selected generated
files can combine different source and manifest identities.

After recovery, direct `--check` must pass. If it does not, compare target, schema,
toolchain, and generator revision in that order. Compiler fix-ups and hand edits are
not recovery tools.

## 5. Run engine-free acceptance

From `sdk/rust`:

```console
cargo fmt --all --check
cargo check --workspace --all-features --locked
cargo test --workspace --all-features --locked
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
RUSTDOCFLAGS="-D warnings" cargo doc --workspace --all-features --no-deps --locked
cargo test -p dagger-sdk --no-default-features --locked
cargo deny check
cargo run -p dagger-bootstrap --bin dagger-rust --locked -- \
  generate --workspace . --check
```

Also run the direct Go ABI and Dagger-module Go tests listed in
[ENGINE_INTEGRATION.md](ENGINE_INTEGRATION.md). Review dependency activation with
`cargo tree -p dagger-sdk -e features` and inspect every Cargo Deny advisory, license,
ban, and source result. These checks do not construct an engine.

Acceptance confirms `unsafe_code = "deny"`, both public package contents, default and
no-default feature paths, warning-denied documentation, generated serde semantics,
owned-path confinement, checksum verification, and credential-safe diagnostics.

## 6. Assemble and retrieve artifacts

Use the complete [Namespace Rust SDK artifact build](NAMESPACE_BUILD.md) runbook. It is
the single authoritative procedure for the exact checkout, builder preflight, ordinary
build and external verification, artifact export, checksum, download, and devbox pause.
Do not duplicate or abbreviate that sequence here.
