# Maintaining the Dagger Rust generated client

This runbook is the review and recovery contract for the checked core-schema client.
Generation is deliberately based on repository inputs, not live introspection: the
target descriptor, schema snapshot, compatibility mappings, projection rules, and
formatter toolchain must all agree before output can be published.

## Ownership boundary

The generator owns only these paths:

- `crates/dagger-sdk/src/gen/`;
- `crates/dagger-sdk/tests/core_projection.rs`;
- `crates/dagger-sdk/tests/core_reachability.rs`; and
- `completeness/artifacts/core-codegen-bindings.json`.

Every generated Rust file contains a machine-readable provenance header. The binding
manifest records the exact owned path set, byte digest, semantic digest, checked Dagger
revision, and schema digest. Never edit an owned Rust file or the derived manifest by
hand. A correction belongs in schema validation, projection, naming, documentation,
rendering, or publication logic.

## Check and update

Run direct generation from `sdk/rust`. Check mode is read-only and silent on success:

```console
cargo run -p dagger-bootstrap --bin dagger-rust --locked -- \
  generate --workspace . --check
```

After an intentional generator, mapping, or checked-target change, publish the complete
candidate explicitly:

```console
cargo run -p dagger-bootstrap --bin dagger-rust --locked -- \
  generate --workspace . --update
```

The update command prints only added, removed, or changed owned paths. Immediately run
check mode again, then inspect both `git diff --stat` and `git diff`. The path set in the
diff must equal the change set explained by the binding manifest. An unrelated file,
missing generated file, formatter-only repair, or source change made after generation is
a failed update, even if the crate compiles.

Repository generation is the final integration fence. From the repository root:

```console
./hack/with-dev ./bin/dagger generate -y rust-sdk:apiclient
```

It must leave the checked generated client unchanged. Do not substitute the unscoped
`dagger generate -y`: that runs every generator in the workspace and tests unrelated
SDK, engine, docs, and release generation rather than the Rust publication boundary.
The focused graph-local gate is:

```console
./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev call generated-client-check
```

## Refreshing the target

A target refresh changes an immutable compatibility claim and should be reviewed
separately from ordinary renderer work.

1. Update `completeness/target.json` with the exact Dagger version, full revision,
   schema digest, CLI identities, Rust toolchain, and authority revisions.
2. Capture the schema through the repository's completeness workflow; do not substitute
   a schema from a nearby engine or reserialize it by hand.
3. Re-run source extraction and review inventory drift before changing classifications.
4. Review every failure in `core-codegen-mappings.json`. Mapping rules are closed sets:
   a new matching Go declaration is not adopted until its semantics, disposition,
   required evidence, and any idiomatic-equivalence decision are reviewed.
5. Run direct update, inspect the localized owned diff, and complete the verification
   matrix below.

Changed or removed schema coordinates must fail closed until their generated and
compatibility policies are explicit. Do not refresh digests merely to make a check pass.

## Manifest and evidence review

The binding manifest is an exhaustive join, not a completeness assertion. Each active
generated-client capability names its authority fingerprint, Rust representation,
implementation fingerprint, and required evidence domains. A manifest row with absent,
failed, stale, wrong-target, or wrong-scope evidence remains blocking.

Evidence admission checks the target and schema, source subject, command identity,
result digest, projection fingerprint, implementation fingerprint, and exact capability
scope. Shared compile or property evidence may cover the catalog entries it actually
enumerates. Exact-target evidence covers only the runtime strategies represented by its
record; it must not be widened because a neighbouring operation passed.

Run focused live conformance from the repository root:

```console
./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev call core-conformance
```

The result is normalized, credential-free candidate evidence. Its subject digest covers
the compilable Cargo workspace and excludes derived evidence/status artifacts, avoiding
a self-referential digest while the binding manifest continues to bind every generated
byte. Admit only a passing record produced from the reviewed source.

To publish evidence, capture that JSON without the progress stream, then run the sole
status transition command from `sdk/rust`:

```console
./hack/with-dev ./bin/dagger --silent -m toolchains/rust-sdk-dev \
  call core-conformance \
  > sdk/rust/completeness/evidence/core-codegen-exact-target.json

cargo run -p dagger-sdk-completeness \
  --bin dagger-core-evidence-registry --locked -- \
  --root . \
  --exact-target completeness/evidence/core-codegen-exact-target.json \
  --registry-output completeness/evidence/core-codegen-registry.json \
  --policy-output completeness/evidence/core-codegen-policy.json \
  --evidence-output completeness/evidence/registry.json
```

The command verifies freshness and domain closure before publishing the Feature 1
evidence links. Contract rendering applies those links through the capability-local
status transition engine. It is intentionally conservative: exact-target evidence
closes only the operations observed by the live matrix, while rows needing an
unobserved live operation remain blocking. Never edit files under
`completeness/artifacts/` or change a label to improve the headline count.

## Recovery and rollback

Generation publishes atomically, so a failed validation or formatting pass should
leave the previous output intact. If an update completes but review rejects it, restore
the whole owned path set from the reviewed pre-update commit or discard the entire
worktree and repeat in a clean worktree. Do not restore only selected modules: that can
create a client whose source, tests, and manifest describe different subjects.

After recovery, direct `--check` must pass before further work. If it does not, compare
the checked target, schema, mappings, toolchain, and generator revision in that order;
do not use compiler fix-ups or hand edits as recovery tools.

## Release verification

Run from `sdk/rust`:

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

Also run `generated-client-check`, `core-conformance`, the scoped
`rust-sdk:apiclient` repository generator, and the Rust SDK security workflow. Review
direct dependencies and feature activation with `cargo tree -p dagger-sdk -e features`;
inspect every cargo-deny advisory, license, ban, and source result rather than treating
configured warnings as invisible.

Release review must confirm:

- `unsafe_code = "deny"` still applies to every workspace crate;
- only `dagger-sdk` is publishable and its package contains the generated modules,
  README, and Apache-2.0 license;
- default, `gen`, no-default, all-features, MSRV, and warning-denied documentation
  builds have all passed;
- generated serde preserves optional omission and explicit zero-like values;
- generated docs need no module-wide rustdoc or missing-docs suppression;
- generation cannot escape owned paths, follow output symlinks, expose credentials in
  diagnostics, or invoke an unpinned formatter; and
- every capability lacking any declared evidence domain remains honestly blocking.
