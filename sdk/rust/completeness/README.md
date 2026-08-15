# Rust SDK completeness contract

This directory holds the executable contract used to compare the Rust SDK with its
pinned Dagger authorities. It keeps two questions separate:

- **Integrity:** do pinned inputs, reviewed decisions, observations, and derived files
  agree?
- **Completeness:** does every active capability have sufficient implementation and
  verification?

A valid contract may report incomplete capability coverage. Neither integrity nor
completeness is a release or publication decision.

## Authority model

The authorities are peers with bounded scopes:

- engine introspection defines the public wire surface;
- target-compatible `sdk-sdk` checks define shared lifecycle behavior within their
  declared check and platform scope;
- the pinned Go client, engine SDK, generator, and tests define behavior outside that
  harness scope and provide reference observations where scopes overlap; and
- Rust policy defines language-specific safety, ownership, documentation, dependency,
  and ergonomic requirements.

No source has blanket precedence. Incompatible claims stay visible for review rather
than being silently reordered. Historical Rust implementations are reference material,
not authority over the current target.

## Authored and derived files

Review changes to these authored inputs as contract decisions:

- `target.json` pins repository, version, toolchain, schema, and CLI identities;
- `authorities.json` selects source paths, exclusions, extractors, and source digests;
- `capabilities.json` defines non-schema capabilities and source anchors;
- `classifications.json` records current status and residual gaps;
- `evidence/registry.json`, `harness-mappings.json`, and `compatibility.json` bind
  observations to immutable sources, commands, targets, artifacts, and platforms; and
- `snapshots/` and `sources/` contain the raw schema, normalized Go helper protocol,
  vendored harness subset, and checksum-verified CLI/runtime identities.

Everything under `artifacts/` is derived. Do not hand-edit it. Source inventory,
ledger, reports, binding manifests, and compatibility metadata must reproduce
byte-for-byte from the authored inputs.

Capability observations are internal verification records, not release authorization. Scope
is exact: a passing record covers only its declared target, command, platform, subject,
and capability coordinates. Expected failures, skipped tests, removed tests,
documentation, and harness self-checks cannot establish implementation support.

## Verify the checked contract locally

From `sdk/rust`, run the read-only integrity gate:

```console
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  verify --root ../.. --gate integrity --format human
```

Run the completeness gate separately when reviewing capability coverage:

```console
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  verify --root ../.. --gate completeness --format human
```

Exit `0` means the selected gate passed, exit `1` means a valid contract whose selected
gate is false, and exit `2` means invalid input or a tooling failure. Automation must
not convert an expected incompleteness result into either a pass or a generic error.

The complete engine-free repository checks are:

```console
cargo fmt --all --check
cargo check --workspace --all-features --locked
cargo test --workspace --all-features --locked
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
RUSTDOCFLAGS="-D warnings" cargo doc --workspace --all-features --no-deps --locked
cargo deny check
```

Run the dependency-free Go extractor from `completeness/extractors/go` with
`GO111MODULE=off go test ./...`. Run `go test ./...` from
`.dagger/modules/rust-client-dev` for its direct contract tests.

## Recompute source boundaries

Dagger recapture is separate from the direct local gate. From the repository root:

```console
./hack/with-dev ./bin/dagger -m .dagger/modules/rust-client-dev \
  check completeness-integrity
./hack/with-dev ./bin/dagger -m .dagger/modules/rust-client-dev api call \
  completeness-artifacts is-empty
```

The first command compares fresh normalized source extraction with checked inputs. The
second captures engine introspection, reconstructs derived artifacts in graph-local
staging, and reports whether the resulting changeset is empty. An unexpected change is
drift to inspect with `diff-stats` or `as-patch`; it is not authority to overwrite the
checkout.

The pinned conformance profile records exact CLI and engine identities, checksums,
platform, commands, and per-check outcomes. Acquisition success and individual SDK
outcomes are distinct. A harness self-check proves the harness only.

## Render and review safely

Reproduction asks whether the existing claim remains true. A refresh changes the claim
and requires review of authored decisions and derived output.

Render checked inputs only to a new empty staging directory:

```console
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  render --root ../.. --output /absolute/path/to/empty-review-directory
```

Compare staged `artifacts/` byte-for-byte with the checked directory. Normal rendering
and Dagger evaluation never authorize copying output into the active contract.

For a successor target:

1. describe the exact target movement and expected capability effects;
2. prepare an isolated candidate with full commit identities, source selections, raw
   schema, vendored harness subset, CLI/runtime checksums, and source digests;
3. re-extract every selected source and explicitly review changed capability scope;
4. update target-scoped implementation and verification records without widening them;
5. render and review inventory, ledger, reports, ownership, exceptions, and digests; and
6. validate the transition into a separate empty staging directory.

```console
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  transition --root ../.. \
  --candidate /absolute/path/to/candidate/target.json \
  --output /absolute/path/to/empty-transition-directory
```

To stage a normalized harness result for review, use `import-evidence` with the exact
profile and another empty output directory. Import validates target, revision, CLI
artifact, check kind, capability IDs, platform, command, outcome, and reverse scope. It
does not mutate the active contract.

## Ground-truth and safety rules

- Capture real engine introspection. Do not seed the contract from a synthetic codegen
  fixture.
- Use the checked CLI/runtime identities and checksums, not a guessed download URL.
- Keep schema, source, toolchain, CLI, engine manifest, and platform identities
  explicit. A nearby version or mutable ref is not equivalent.
- Keep credentials out of source descriptors, observations, diagnostics, cache keys,
  generated files, and rendered reports.
- Preserve authored files and unknown bytes. Derived output moves through isolated
  staging and manifest-authorized atomic replacement only.
- Regenerate module bindings only when the Dagger module API changes; ordinary contract
  reproduction does not require repository-wide generation.
- Treat linked-worktree warnings as diagnostics. If repository loading actually fails,
  reproduce from a normal clean checkout rather than weakening the contract.

Normal verification neither downloads authority sources nor mutates this directory.
Standalone-client generation and its owned dependency boundary are described in
[CLIENT_GENERATION.md](../CLIENT_GENERATION.md).
Artifact assembly and one external consumer against the completed engine are separate
release-readiness checks described in [MAINTAINING.md](../MAINTAINING.md); they do not
change this contract or authorize publication.
