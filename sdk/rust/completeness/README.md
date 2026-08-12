# Rust SDK completeness contract

This directory is the executable F1 baseline for bringing the Rust SDK to Go-level
completeness without turning Go API shape into Rust API design. It assesses one immutable
Dagger, Go SDK, and `sdk-sdk` target and keeps two independent statements visible:

- **Integrity** says the pinned inputs, reviewed decisions, evidence, and derived files agree.
- **Completeness** says every active capability has sufficient implementation and verification.

The initial baseline must pass Integrity and is expected to fail Completeness. A failed
subject-conformance check is therefore a truthful blocker, not a corrupt contract.

## Authority model

The authorities are peers with deliberately bounded scopes:

- the engine introspection schema defines the public wire surface;
- the target-compatible `sdk-sdk` checks define common lifecycle behaviour within their
  explicit check and platform scope;
- the pinned Go client, engine SDK, generator, and tests define behaviour outside that
  harness scope and provide reference evidence where scopes overlap;
- Rust policy defines language-specific safety, documentation, dependency, and ergonomic
  requirements.

No source has blanket precedence. The inventory retains overlap and fails on incompatible
claims instead of silently selecting the most convenient answer. Historical Rust proposals are
evidence only.

## Authored and derived files

Review changes to these authored inputs as contract decisions:

- `target.json` pins all repository, version, toolchain, schema, and CLI identities;
- `authorities.json` selects exact source paths, exclusions, extractors, and source digests;
- `capabilities.json` defines non-schema capabilities and source anchors;
- `classifications.json` records status, residual gap, and Feature 2–9 ownership;
- `evidence/registry.json`, `harness-mappings.json`, and `compatibility.json` bound claims to
  immutable sources, commands, targets, artifacts, and platforms;
- `snapshots/` and `sources/` contain the raw schema, normalized Go helper protocol, exact
  vendored harness subset, and published CLI provenance.

Everything under `artifacts/` is derived. Do not hand-edit it. `source-items.json`,
`inventory.json`, `ledger.json`, both reports, and release compatibility metadata must reproduce
byte-for-byte from the authored inputs.

Standalone-client closure adds one deliberately separate evidence chain:
`evidence/client-generation-closure-observation.json` records the engine-free typed
checkpoint, current/reused gate inputs, timings, and Cargo counts;
`evidence/client-generation-closure.json` is its admitted canonical closure; and
`artifacts/client-generation-report.json` is the derived honest report. Reproduce or
check those files with `dagger-client-generation-evidence`. The report must leave the
five exact-engine cases unexecuted and retain both sign-off blockers until Feature 8;
local closure never fabricates an engine result.
The governing workflow and deferred case semantics are documented in
[`CLIENT_GENERATION.md`](../CLIENT_GENERATION.md).

## Reproducing the checked F1 baseline

This runbook proves the checked baseline from two independent directions. Local verification
reconstructs the contract from pinned files without retrieval or writes. Dagger verification then
recaptures the live engine schema, reruns Go extraction in its pinned toolchain, and executes the
published conformance profile. Neither path silently updates the active contract.

### Prerequisites and safety boundary

- Start from a checkout containing the reviewed contract and no unreviewed changes to its selected
  source paths. The immutable identities are in [`target.json`](target.json), not inferred from a
  branch name or nearby version comment.
- Use the repository development prerequisites for local Rust commands. Contract verification
  itself performs no network access; a first Cargo invocation can still need an already-installed
  toolchain and cached dependencies.
- The Dagger paths need a working container runtime and network access on a cold cache. Images,
  the published CLI, the engine manifest, and their checksums are pinned by the toolchain and
  [`provenance.json`](sources/dagger-cli/v1.0.0-beta.9/provenance.json).
- Run Dagger commands from the repository root and Cargo commands from `sdk/rust`.
- Treat every output directory as disposable review staging. Never select
  `sdk/rust/completeness` itself as `--output`, and never apply an unreviewed Changeset to the
  active checkout.

### 1. Verify the checked contract locally

From `sdk/rust`, run the read-only Integrity gate:

```console
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  verify --root ../.. --gate integrity --format human
```

The command must exit `0`, report `Integrity: PASS`, and still report
`Completeness: FAIL`. The latter is not an Integrity failure: F1 records the real work remaining
rather than pretending the current SDK is complete.

Run the Completeness gate separately:

```console
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  verify --root ../.. --gate completeness --format human
```

For the initial baseline this command must exit `1`. Exit `1` means a valid contract whose
selected gate is false; exit `2` means malformed arguments, invalid artifacts, or a tooling
failure. Scripts must not turn the expected `1` into either a passing Completeness claim or a
generic tool error.

### 2. Recompute source boundaries through Dagger

From the repository root, rerun Go extraction and the offline Integrity gate inside the pinned
containers:

```console
./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev \
  check completeness-integrity
```

This command must exit `0`. It first compares fresh normalized Go helper output with the checked
`sources/go/*.json`, then overlays that output into a graph-local candidate and reconstructs the
contract. Raw Go source never crosses the Go-to-Rust extractor boundary.

Next, capture real engine introspection, rerun the Go helper, render every derived artifact, and
ask whether the resulting Changeset is empty:

```console
./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev api call \
  completeness-artifacts is-empty
```

The checked F1 baseline must print `true`. An unexpected `false` is drift to investigate, not a
request to overwrite the checked artifacts. Inspect it without mutation using either of these
commands; each recomputes the graph and can take several minutes on a cold cache:

```console
./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev api call \
  completeness-artifacts diff-stats

./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev api call \
  completeness-artifacts as-patch contents
```

`CompletenessArtifacts` returns only a Dagger Changeset. It cannot edit the host checkout by
itself.

### 3. Run the exact published conformance profile

Run the profile through the pinned `linux/amd64` beta.9 CLI and engine:

```console
./hack/with-dev ./bin/dagger -m toolchains/rust-sdk-dev api call \
  completeness-harness contents
```

The outer command must exit `0` and return a normalized JSON profile. Dagger's progress output is
expected to show **17 failed checks and 1 passed check**:

- the 17 `subject-conformance` failures are truthful Rust SDK blockers;
- `init-module-renders-root-type` is the passing `harness-self` check and has no Capability IDs;
- neither a harness-self pass nor an expected subject failure is accepted as passing Rust
  implementation evidence.

The current Rust workspace is not yet an SDK module workspace, so the subject checks report that
no SDK module was detected. When later features make those checks pass, their normalized profile
must be imported and reviewed as new target-scoped evidence rather than silently changing this
baseline.

### 4. Run the complete repository gates

From `sdk/rust`:

```console
cargo fmt --all --check
cargo check --workspace --all-features --locked
cargo test --workspace --all-features --locked
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
RUSTDOCFLAGS="-D warnings" cargo doc --workspace --all-features --no-deps --locked
cargo deny check
```

The workspace test command executes all 23 tagged correctness-property families with at least
256 cases, plus fixed, integration, exact-target, cross-root, and documentation tests. All four
`cargo deny` classes—advisories, bans, licenses, and sources—must pass. Configured warnings
remain visible and must be reviewed rather than hidden.

From `sdk/rust/completeness/extractors/go`, verify the dependency-free helper:

```console
GO111MODULE=off go test ./...
```

From `toolchains/rust-sdk-dev`, verify the Dagger module binding:

```console
go test ./...
```

### Current checked fingerprint

The exact result is locked by
[`initial_baseline.rs`](../crates/dagger-sdk-completeness/tests/initial_baseline.rs) and reproduced
in [`artifacts/report.json`](artifacts/report.json). Feature 2 completes the stable owned-client
contract while retaining explicit Feature 3 and Feature 8 blockers for live CLI, resource, and
workspace behaviour:

| Observation | Expected value |
| --- | ---: |
| Capabilities | 4,556 |
| `Implemented` | 15 |
| `Idiomatic_Equivalent` | 10 |
| `Partial` | 3,428 |
| `Missing` | 1,103 |
| Blocking capabilities | 4,531 |
| Inventory digest | `sha256:c0f27c650ab5847a861c599094ecca2ffac00aee35a9a995623dd018a7b38e66` |
| Ledger digest | `sha256:17003989b1e531913cad8adb4c86ba31dec4b7cd687c4aa5b8552d6cb65f8b24` |
| Harness partition | 17 subject failures, 1 harness-self pass |

Any difference requires an explained authored-input or extractor change. Do not update the
fingerprint merely to make a check green.

## Refreshing the baseline safely

Reproduction answers whether the existing claim is still true. A refresh changes the claim and
therefore requires review of both authored decisions and derived output.

### Same-target artifact review

To render the checked inputs without touching active files, create an empty staging directory and
run from `sdk/rust`:

```console
review_dir="$(mktemp -d)"
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  render --root ../.. --output "$review_dir"
```

Compare `$review_dir/artifacts` byte-for-byte with `completeness/artifacts`. For live schema and
Go-source recapture, use the Dagger Changeset commands above; review `diff-stats` and `as-patch`
before deciding whether the difference represents intended target movement or unexplained drift.
Normal rendering and Dagger evaluation never authorize copying output into the active contract.

### Successor-target review

Advancing the Dagger, Go, harness, schema, toolchain, or CLI target is deliberate feature work:

1. Describe the target movement and expected capability effects in a reviewed feature spec.
2. Prepare an isolated candidate contract with updated full commit identities, source selections,
   raw schema, vendored harness subset, published artifact provenance, and source digests.
3. Re-extract every selected source. Explicitly classify every new or changed capability; removed
   or skipped sources remain audit history and cannot become passing evidence.
4. Update target-scoped implementation, verification, and decision evidence. Do not infer passing
   Rust support from documentation, removed tests, expected failures, or harness-self checks.
5. Render and review the candidate's inventory, ledger, reports, compatibility metadata, counts,
   ownership, exceptions, and digests before staging a transition.

With the complete candidate contract beside its candidate `target.json`, run from `sdk/rust`:

```console
transition_dir="$(mktemp -d)"
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  transition --root ../.. \
  --candidate /absolute/path/to/candidate/target.json \
  --output "$transition_dir"
```

The candidate target's parent directory must contain its matching `authorities.json`,
`harness-mappings.json`, `evidence/registry.json`, `artifacts/inventory.json`, and
`artifacts/ledger.json`. The command validates the transition and writes only to the empty
staging directory. A rejected transition leaves every active byte unchanged.

To stage a normalized harness result for evidence review:

```console
evidence_dir="$(mktemp -d)"
cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- \
  import-evidence --root ../.. \
  --run /absolute/path/to/normalized-profile.json \
  --output "$evidence_dir"
```

Import validates target, revision, CLI artifact, check kind, Capability IDs, platform, command,
outcome, and reverse evidence scope. It does not make expected failures or self-checks into Rust
completion evidence. Review staged files explicitly, then rerun the complete reproduction matrix
before accepting any change.

### Ground-truth traps and diagnostics

- **Use real engine introspection.** Never seed the contract from
  `cmd/codegen/introspection/testdata/schema.json`; that is a synthetic codegen fixture containing
  types such as `Sub1`, `Sub2`, and `Test`. `CompletenessArtifacts` uses
  `DaggerEngine.IntrospectionJSON()`. For independent diagnosis, export the same engine-owned
  surface to a disposable path:

  ```console
  ./hack/with-dev ./bin/dagger -m toolchains/engine-dev api call \
    introspection-json export --path /tmp/dagger-engine-schema.json
  ```

- **Use published CLI provenance, not a guessed release URL.** The beta.9 archive is served by
  `dl.dagger.io`; the exact URL, archive digest, executable digest, engine multi-architecture
  index, and `linux/amd64` manifest are recorded in `provenance.json` and verified by the harness.
- **Preserve the edition-2024 execution path.** Completeness operations intentionally use the
  development container without the legacy cargo-chef installation route. Do not replace that
  boundary without first proving the pinned cargo-chef version can parse the workspace edition.
- **Distinguish graph progress from the operation verdict.** Nested subject failures are expected
  in the F1 harness. Judge acquisition and normalization by the outer exit status, then judge SDK
  completeness from the normalized per-check outcomes.
- **Regenerate bindings only when the Dagger module API changes.** Adding or renaming a public
  function in `toolchains/rust-sdk-dev` requires the repository's Go module-binding generator
  and review of `dagger.gen.go`. That exceptional binding refresh is distinct from the normal
  Rust API-client workflow, `dagger generate -y rust-sdk:apiclient`; ordinary contract
  reproduction requires neither.
- **Treat linked-worktree warnings as diagnostics, not verdicts.** Some local Dagger invocations
  warn that libgit does not understand the Git `worktreeConfig` extension. A successful trace and
  correct result remain valid; if repository loading actually fails, reproduce from a normal clone
  rather than weakening contract checks.

Normal verification never downloads authority sources or mutates this directory. Target
transitions and evidence imports also write only to isolated staging and preserve active bytes on
rejection.

## Closing later features

Feature specs must name the exact Capability IDs they intend to change. A status may advance only
with target-scoped implementation and behavioural evidence satisfying the destination state;
documentation, skipped tests, removed tests, harness-self checks, and expected failures are not
passing Rust evidence. Regenerate artifacts and update the Markdown report only through the same
candidate contract so source coverage, reverse evidence scope, ownership, and counts remain
auditable together.
