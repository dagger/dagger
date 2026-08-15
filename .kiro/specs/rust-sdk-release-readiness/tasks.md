# Implementation Plan

## Completed groundwork

- [x] 1. Merge the Rust SDK implementation and upstream beta.11 changes
  - [x] 1.1 Preserve the Feature 8 archive branch unchanged.
  - [x] 1.2 Remove the Feature 8 signoff implementation and release workflows.
  - [x] 1.3 Establish ordinary `Build` and `Verify` entry points.
  - [x] 1.4 Align the source version `1.0.0-beta.11.rust.1` with manual release identity
    `v1.0.0-beta.11+rust.1`.
  - _Requirements: 4.3, 4.4, 5.4, 5.6_

- [x] 2. Prepare current capability and release documentation
  - [x] 2.1 Document the complete client and module SDK capabilities.
  - [x] 2.2 Separate engine-free Rust-first checks from engine-backed assembly.
  - [x] 2.3 Remove current crates.io promises and require direct authorization for a
    manual GitHub Release.
  - [x] 2.4 Clean obsolete Feature 8/F10 routing from the umbrella and child specs.
  - _Requirements: 2.1, 5.1, 5.3, 5.5, 5.6_

## Cleanup implementation

- [x] 3. Remove the completeness/evidence registry graph
  - [x] 3.1 Relocate the ordinary Rust build checker
    - Move `dagger-rust-sdk-check` into `dagger-bootstrap` without changing its three
      commands.
    - Move the two ordinary-build property tests and update only their module path and
      internal-package fixture name.
    - Add only the checker dependencies required by `dagger-bootstrap`.
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5_
  - [x] 3.2 Simplify checked code generation
    - Move target/schema into `sdk/rust/codegen/` and update all consumers.
    - Replace the extended capability-binding registry with
      `codegen/generated.json` encoded from `ArtifactManifest::from_artifacts`.
    - Remove ledger/mapping overrides, decoders, adapters, diagnostics, and their
      registry-specific tests.
    - Preserve exact-target validation, checked formatting, complete ownership,
      obsolete-file removal, symlink defenses, journaling, and rollback tests.
    - _Requirements: 1.3, 1.4, 1.5, 2.2_
  - [x] 3.3 Delete registry machinery
    - Delete `sdk/rust/crates/dagger-sdk-completeness` after the relocations.
    - Delete `sdk/rust/completeness` after moving only target/schema.
    - Remove the workspace dependency and update `Cargo.lock` normally.
    - Require zero maintained references to the deleted crate, ledgers, mappings,
      registries, closures, reports, authorities, or pinned harness.
    - _Requirements: 1.1, 1.2, 1.7_

- [x] 4. Remove Dagger evidence entry points while retaining real tests
  - [x] 4.1 Delete completeness module operations
    - Delete `.dagger/modules/rust-client-dev/completeness.go`.
    - Remove completeness paths from workspace construction.
    - Regenerate Dagger bindings after the public development-module surface shrinks.
    - _Requirements: 1.6, 2.7_
  - [x] 4.2 Decouple normal engine checks
    - Remove the completeness crate test from `EngineUnit` and generated-client checks.
    - Remove `CoreConformance` registry production and `EngineEvidence`.
    - Remove engine mapping/target fields used only by evidence admission.
    - Keep `Resolution`, focused `EngineIntegration`, RustEngineContent, `Build`, and
      `Verify`; describe returned JSON as results rather than admitted evidence.
    - Build the relocated checker from `dagger-bootstrap`.
    - _Requirements: 1.6, 2.1, 2.6, 3.1, 4.3, 4.4_

- [x] 5. Align maintained docs and specs with the smaller build
  - [x] 5.1 Verify the umbrella retains enduring Features 2–7 capability requirements,
    then delete the completed Features 1–7 child specs; rely on Git history for their
    delivery designs, tasks, checkpoints, and evidence records.
  - [x] 5.2 Remove Feature 1 completeness-ledger and registry/release-gate language from
    the umbrella while preserving compatibility, capability, safety, and ordinary-build
    requirements.
  - [x] 5.3 Update current docs for target, schema, and the compact ownership manifest
    without deleting atomicity, compatibility, checksum, redaction, immutable-dependency,
    module, or focused-test requirements.
  - [x] 5.4 Confirm no F8/F10 process, TUF, Action, crates.io publication, Dagger CLI
    change, downstream consumer name, or replacement readiness system was introduced.
  - _Requirements: 1.2, 1.7, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 5.1, 5.2, 5.3, 5.5_

## Green checks and artifacts

- [x] 6. Drive the simplified source tree to green
  - [x] 6.1 Run formatting and the read-only generator check; run update once only if the
    compact ownership manifest or generated target paths require it.
  - [x] 6.2 Run normal workspace check/test, no-default-feature test, Clippy, rustdoc,
    Cargo Deny, and focused direct Go tests. Package validation remains part of the
    ordinary Dagger build in task 8 because the companion crate is not on crates.io.
  - [x] 6.3 Run the retained package-closure, selected-manifest, and generator atomicity
    property tests.
  - [x] 6.4 Inspect the final diff for accidental capability/test deletion and require a
    large net deletion with no evidence registry refresh.
  - _Requirements: 1.4, 1.5, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 3.2, 3.3, 3.4, 3.5_

- [x] 7. Commit the exact artifact target
  - [x] 7.1 Unwind all uncommitted digest-refresh experiments; retain only the deliberate
    registry deletion and simplified build changes.
  - [x] 7.2 Commit concise spec cleanup and code cleanup changes, then require a clean
    exact commit and no AppleDouble files.
  - [x] 7.3 Push only after local checks are green; do not create a PR, tag, release, or
    publish crates without separate direction.
  - _Requirements: 4.1, 4.7, 5.5, 5.6_

- [x] 8. Build and retrieve exact `linux/amd64` artifacts on the documented
  `dagger-rust-builder-xl` Namespace devbox
  - [x] 8.1 Revalidate the detached checkout, Dagger CLI, Docker state, disk, runner host,
    activity marker, and artifact directory.
  - [x] 8.2 Run Ordinary_Build and Ordinary_Verification with the runner host present on
    every Dagger invocation.
  - [x] 8.3 Export exactly two `.crate` packages, one complete engine OCI archive, and
    `SHA256SUMS` below Artifact_Output.
  - [x] 8.4 Download all four files, independently verify checksums, remove only the owned
    marker, and pause or stop the devbox as requested.
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 6.1, 6.2, 6.3, 6.4, 6.5_

## Task Dependency Graph

```json
{
  "waves": [["3"], ["4", "5"], ["6"], ["7"], ["8"]],
  "dependencies": {
    "3": [],
    "4": ["3"],
    "5": ["3"],
    "6": ["4", "5"],
    "7": ["6"],
    "8": ["7"]
  }
}
```

No task authorizes TUF, GitHub Actions, crates.io publication, Dagger CLI changes, a tag,
or a GitHub Release. Git history and the archive branch preserve removed machinery.
