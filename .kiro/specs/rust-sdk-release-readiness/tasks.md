# Implementation Plan

## Overview

This plan changes only the approved Rust SDK documentation, seven maintained child
specifications, the release-readiness specification, and one new release-note fragment.
It reuses existing completeness, immutable-dependency, package-closure, and selected-
manifest authorities; it adds no source code, test, generated file, manifest, lockfile,
Go/Dagger module change, committed acceptance runner, or upstream long-query check.
After local documentation acceptance, the clean detached devbox clone builds and exports
exact non-publishing artifacts through the existing Build and Verify entry points.

## Tasks

- [ ] 1. Establish the closed local edit and acceptance scope
  - [ ] 1.1 Verify the visible local worktree boundary
    - Confirm branch `kiro/rust-sdk-release-readiness` is based on
      `0513782e713257a9285b101f45230af00e3558d8` and preserve both named pre-existing
      stashes without applying, dropping, or rewriting them.
    - Record the exact nine maintained documentation paths, seven maintained child-spec
      directories, readiness spec, and one new release-note fragment as the only allowed
      tracked edits.
    - Confirm Rust/Go/Dagger source, tests, generated files, Cargo manifests/lockfiles,
      workflow files, CLI code, historical changelog records, and the clean umbrella spec
      are excluded.
    - _Requirements: 1.1, 1.3, 1.4, 1.12, 1.13, 1.14, 4.1, 5.11, 5.12_
  - [ ] 1.2 Classify current sensitive occurrences before editing
    - Search the closed maintained scope for F8/F10, `SDK_Signoff`, the deleted workflow,
      Feature 8/9 release routing, publication, provenance, crates.io, evidence, verdict,
      and signoff terms.
    - Classify every match as obsolete delivery/external publication to remove, accepted
      capability to retain, Internal_Publication to retain, Runtime_Safety_Identity to
      retain, package classification to retain, or historical text outside the scan.
    - Reuse existing completeness authority/capability data, immutable Git validators,
      Build/Verify implementation, and documentation drift guards as ground truth; do
      not create another policy model.
    - _Requirements: 1.5, 1.6, 1.7, 1.8, 1.9, 1.10, 1.11, 4.21, 4.22, 5.1, 5.2, 5.3, 5.4_

- [ ] 2. Rewrite the nine maintained Rust documentation surfaces
  - [ ] 2.1 Make the root README the current capability overview
    - Add exact beta compatibility, an authority-bounded current capability table, one
      concise owned-client quickstart, one concise module-authoring invocation, and links
      to focused guides.
    - Add a short Development and release builds section that separates canonical direct
      Engine_Free_Checks from Ordinary_Build and Ordinary_Verification.
    - Remove `cargo add dagger-sdk`, stable/publication promises, and Go source,
      package-layout, ownership, or API-shape compatibility implications.
    - _Requirements: 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.23, 5.3, 5.5, 5.6, 5.7, 5.8_
  - [ ] 2.2 Document only artifact-supported package installation
    - Update `sdk/rust/crates/dagger-sdk/README.md` to obtain and unpack both authorized
      `.crate` artifacts, use a local path for `dagger-sdk`, and patch
      `dagger-sdk-macros` to its sibling vendor directory.
    - Preserve owned lifecycle, explicit close, macro companion, standalone-client reuse,
      quickstart, and feature matrix while removing unconditional crates.io and release-
      evidence language.
    - _Requirements: 4.7, 4.21, 4.22, 5.3, 5.4_
  - [ ] 2.3 Preserve architecture and contributor safety boundaries
    - Update `ARCHITECTURE.md` to say “two public package artifacts,” distinguish local
      checks from engine-backed assembly, and describe Runtime_Safety_Identity as real
      implementation safety rather than signoff.
    - Update `CONTRIBUTING.md` without changing canonical Cargo/direct Go commands; state
      explicitly that they construct no Dagger engine and keep complete-engine Verify
      separate.
    - Preserve ownership, acyclic package boundaries, operation manifests, clean runtime
      promotion, peer authorities, toolchain, and generated ownership.
    - _Requirements: 4.8, 4.9, 4.10, 4.11, 4.21, 5.4, 5.5, 5.7_
  - [ ] 2.4 Replace obsolete engine and maintenance delivery history
    - Update `ENGINE_INTEGRATION.md` to remove the deleted workflow, old release matrix,
      Feature closure, release-evidence flow, and canonical crates.io path; use current
      Build/Verify and retain focused engine cases only as regression tools.
    - Preserve immutable generated dependency policy through existing production rules:
      credential-free canonical HTTPS plus one reachable lowercase 40-character commit,
      rejecting path, branch, tag, default, credentials, query, and fragment forms.
    - Reduce `MAINTAINING.md` to direct checks; exactly two package checks;
      RustEngineContent/Complete_Engine; isolated Verify; package/OCI/checksum export;
      and a separately invoked manual GitHub Release attachment only after direct
      authorization.
    - _Requirements: 4.12, 4.13, 4.14, 4.15, 4.16, 4.17, 4.18, 4.19, 4.20, 4.21, 4.22, 4.27, 5.3, 5.4, 5.6, 5.7, 5.8, 5.9, 5.10_
  - [ ] 2.5 Clean module, client-generation, and completeness guides semantically
    - Remove stale Feature/signoff/external-publication language from
      `MODULE_AUTHORING.md`, `CLIENT_GENERATION.md`, and `completeness/README.md`.
    - Preserve macros, dispatch, Result_Sink terminal election, cancellation, engine-
      free fixtures, immutable dependency, caller/generated ownership, manifest-last
      Internal_Publication, deterministic derivation, isolated staging, completeness
      checks, and peer-authority scope.
    - _Requirements: 1.9, 1.10, 4.21, 4.22, 5.4, 5.6, 5.10_

- [ ] 3. Clean the seven maintained child specifications without changing capability
  - [ ] 3.1 Clean completeness-contract and client-lifecycle delivery routing
    - Preserve deterministic inventory/ledger/report, isolated staging, normalized
      secret-free outcomes, owned client/session, close election, cancellation, and typed
      configuration/errors.
    - Remove or durably reframe future Feature 8/9 conformance, migration, publication,
      release profiles, and final gates.
    - _Requirements: 1.3, 1.7, 1.8, 1.11, 5.1, 5.2_
  - [ ] 3.2 Clean transport-observability and core-codegen delivery routing
    - Preserve verified CLI download, redaction, tracing, artifact identity, atomic cache
      publication, pure checked generation, schema mapping, generated ownership, and
      scoped atomic publication.
    - Remove or reframe obsolete platform/security/release owners, beta.10 release gate,
      migration/release assets, and stable gate.
    - _Requirements: 1.7, 1.8, 1.9, 1.10, 1.11, 5.1, 5.2, 5.4_
  - [ ] 3.3 Clean engine-integration, module-authoring, and client-generation ownership
    - Remove `SDK_Signoff`, the deleted workflow, evidence admission, signoff manifests,
      deferred verdict planes, sole-package/release-owner claims, and Feature 8/9
      publication/self-hosting ownership.
    - Preserve engine-free checks, focused regressions, immutable dependency, runtime
      identity, two package artifacts, compiler/dispatcher, Result_Sink, cancellation,
      generated assets, project/runtime checks, typed API, caller ownership, and authored-
      byte preservation.
    - _Requirements: 1.5, 1.6, 1.7, 1.8, 1.9, 1.10, 1.11, 5.1, 5.2, 5.4, 5.10_
  - [ ] 3.4 Review every child-spec diff against the preserve/remove classification
    - Restore any accepted Features 1–7 capability, Internal_Publication,
      Runtime_Safety_Identity, package classification, or technical regression case
      removed by an over-broad edit.
    - Confirm the clean umbrella and release-readiness specs were not included in child-
      spec cleanup.
    - _Requirements: 1.3, 1.4, 1.7, 1.8, 1.9, 1.10, 1.11_

- [ ] 4. Add release-facing text and complete local static acceptance
  - [ ] 4.1 Add one concise Rust SDK unreleased fragment
    - Add one new file under `sdk/rust/.changes/unreleased/` describing the current beta
      capability/readiness boundary and exact compatibility without promising crates.io
      publication or a hosted release.
    - Leave every existing `CHANGELOG.md` and `.changes/**` record byte-for-byte
      unchanged and use only generic consumer language.
    - Include no Discord summary, signoff, workflow, TUF, downstream product/platform
      name, publication action, or upstream long-query validation claim.
    - _Requirements: 1.12, 1.14, 2.1, 2.2, 4.23, 4.24, 4.25, 4.26, 4.27, 5.11, 5.12_
  - [ ] 4.2 Run hard-zero and semantic-allowlist scans interactively
    - Over exactly the nine docs and seven child specs, require zero current process
      matches for F8, F10, `SDK_Signoff`, `rust-sdk-security.yml`,
      `cargo add dagger-sdk`, canonical crates.io release, future Feature 8/9 owners,
      signoff matrices/manifests, release evidence/verdicts, and public automation.
    - Review every publication/provenance match and retain only Internal_Publication,
      Runtime_Safety_Identity, `publish = false`, package classification, truthful
      historical text outside scope, and the direct-authorization manual boundary.
    - Save no committed scan result, policy file, verdict, evidence object, or registry.
    - _Requirements: 1.5, 1.6, 1.7, 1.8, 1.9, 1.10, 1.14, 5.1, 5.2, 5.3, 5.4, 5.11, 5.12_
  - [ ] 4.3 Verify positive documentation boundaries and authority grounding
    - Confirm maintained docs state engine-free direct checks, focused engine regression
      scope, exact Build outputs, isolated Verify, no crate publication, direct release
      authorization, and immutable Git requirements.
    - Trace every root README capability row to merged Rust code and the applicable
      schema, scoped harness, pinned Go, or Rust-policy authority; reject blanket Go
      compatibility claims.
    - Confirm no dedicated upstream long-query reproducer/result is part of Rust SDK
      acceptance and no engine source change is authorized by this work.
    - _Requirements: 2.1, 2.2, 2.3, 4.2, 4.3, 4.4, 4.5, 4.6, 4.23, 4.25, 4.26, 5.5, 5.6, 5.7, 5.8, 5.9, 5.10, 5.12_
  - [ ] 4.4 Checkpoint: local documentation boundary is clean
    - Run diagnostics over every changed Markdown/YAML file and check fences, links,
      trailing whitespace, version/package/command names, and immutable Git examples.
    - Run the existing package-closure and selected-manifest tests from
      `ordinary_build.rs` without renaming or modifying them.
    - Confirm the tracked diff is limited exactly to approved documentation, child specs,
      readiness specs, and one new release-note fragment; preserve named stashes and the
      protected archive branch.
    - _Requirements: 1.1, 1.3, 1.4, 1.12, 1.13, 1.14, 3.5, 3.7, 4.1, 4.24, 5.1, 5.2, 5.3, 5.4, 5.12_

- [ ] 5. Build and verify exact non-publishing artifacts in the devbox
  - [ ] 5.1 Re-establish exact remote preconditions
    - Reactivate `dag-rust-xl` without a second console session; require the clone to be
      detached at `0513782e713257a9285b101f45230af00e3558d8`, clean, and free of `._*`
      files.
    - Confirm the protected archive remains at
      `a9f55dd48c88b91b69e6e36c8289178362ad979e`; confirm or create only
      `/.namespace/tasks/kiro-rust-sdk-readiness-0513782e7`.
    - Record active `dagger` binary/version and Docker availability, determine the runner
      host, and set `_EXPERIMENTAL_DAGGER_RUNNER_HOST` on every Dagger invocation.
    - Do not patch local documentation/spec changes into the exact-build clone.
    - _Requirements: 1.1, 1.2, 1.13, 1.14, 3.1, 3.2, 3.3, 3.11, 6.3_
  - [ ] 5.2 Run canonical direct engine-free checkpoints
    - Run the documented pinned Cargo suite with `--locked`, including format, check,
      workspace tests, Clippy, rustdoc, no-default-features, deny, and focused codegen/
      engine/completeness tests.
    - Run preserved direct Go checkpoints for `engine-dev`, `rust-client-dev`
      enginefixture/enginefree, runtime metadata, and Linux compile-only core SDK,
      schema, and CLI boundaries; these checks construct no Dagger engine.
    - _Requirements: 3.1, 3.2, 3.3, 4.10, 4.15, 5.5_
  - [ ] 5.3 Invoke only existing Build and Verify entry points
    - Confirm active CLI help, then run Build version, Build packages export, Build
      complete-engine tarball export, and Build Verify with the runner host on every
      invocation.
    - Place outputs only below `/workspaces/artifacts/0513782e7/`; invoke no historical
      wrapper, publisher, tag, hosted-release path, or dedicated long-query reproducer.
    - _Requirements: 2.1, 3.1, 3.2, 3.3, 3.4, 3.8, 3.11, 3.12_
  - [ ] 5.4 Validate exact package, consumer, and engine closure
    - Require only
      `dagger-sdk-macros-1.0.0-beta.11.rust.1.crate` and
      `dagger-sdk-1.0.0-beta.11.rust.1.crate`, with exact metadata, roots, files,
      features, and macro dependency.
    - Require Verify to unpack only those artifacts, compile the isolated path-based
      consumer, run it against Complete_Engine, query the expected engine version, and
      close cleanly.
    - Require the OCI to contain standard engine/CLI binaries and selected
      RustEngineContent equal to the workspace-built manifest while tolerating unrelated
      standard SDK blobs.
    - _Requirements: 2.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9, 3.13, 4.16, 4.17, 4.18, 5.7, 5.8_

- [ ] 6. Finalize, retrieve, and independently verify the artifact set
  - [ ] 6.1 Generate and verify canonical checksums
    - Produce sorted relative-path lowercase SHA-256 entries for exactly the two crate
      packages and
      `dagger-engine-v1.0.0-beta.11.rust.1-linux-amd64.oci.tar` in `SHA256SUMS`.
    - Rehash all three remote files independently; fail on missing, duplicate, extra,
      renamed, or mismatched paths.
    - Include no readiness record, evidence registry, TUF metadata, signature,
      credential, conformance verdict, or signoff provenance.
    - _Requirements: 3.10, 3.11, 3.12, 3.13, 4.19, 6.5_
  - [ ] 6.2 Download every required output before shutdown
    - Use `devbox download ... --mkdir` to retrieve both package files, complete OCI, and
      `SHA256SUMS` from `/workspaces/artifacts/0513782e7/` to the agreed local artifact
      directory.
    - Compute local SHA-256 values and require a one-to-one match with the manifest;
      preserve the marker and leave the task incomplete on any mismatch.
    - _Requirements: 6.1, 6.2, 6.5_
  - [ ] 6.3 Remove only the owned marker and shut down
    - After local checksum success, remove only
      `/.namespace/tasks/kiro-rust-sdk-readiness-0513782e7` and verify no other marker
      changed.
    - Force shutdown only `dag-rust-xl`; do not create/push a tag, publish a crate,
      create a GitHub Release, or invoke a manual attachment path.
    - _Requirements: 1.13, 4.27, 6.3, 6.4, 6.5_

- [ ] 7. Final checkpoint: reconcile the original success criteria
  - [ ] 7.1 Re-run local static acceptance and inspect the final tracked diff
    - Recheck diagnostics, fences, links, whitespace, hard-zero terms, semantic
      allowlists, positive boundaries, capability grounding, and exact allowed paths.
    - Confirm no Rust SDK source/test/generated/manifests, Go/Dagger modules, workflows,
      CLI code, historical records, or clean umbrella specification changed.
    - _Requirements: 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 1.9, 1.10, 1.11, 1.12, 1.14, 4.1, 4.2, 4.22, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 5.8, 5.9, 5.10, 5.11, 5.12_
  - [ ] 7.2 Reconcile exact artifacts and hard non-goals
    - Confirm Target_Commit, version/label mapping, exactly two packages, complete OCI,
      isolated consumer, checksums, downloaded-byte verification, protected branch,
      owned-marker removal, and devbox shutdown.
    - State explicitly that no upstream long-query revalidation, engine fix, committed
      readiness machinery, TUF, GitHub Action, Dagger CLI change, crates.io publication,
      Git tag, GitHub Release, Discord summary, or downstream consumer name was created.
    - _Requirements: 1.1, 1.2, 1.13, 1.14, 2.1, 2.2, 2.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9, 3.10, 3.11, 3.12, 3.13, 4.24, 4.25, 4.26, 4.27, 6.1, 6.2, 6.3, 6.4, 6.5_

## Task Dependency Graph

```json
{
  "waves": [
    ["1"],
    ["2", "3"],
    ["4"],
    ["5"],
    ["6"],
    ["7"]
  ],
  "dependencies": {
    "1": [],
    "2": ["1"],
    "3": ["1"],
    "4": ["2", "3"],
    "5": ["4"],
    "6": ["5"],
    "7": ["6"]
  }
}
```

## Notes

- The user's request to begin tasks is the execution consent gate for this revised plan.
- The local branch is the visible documentation/spec editing authority. The devbox clone
  remains detached, clean exact-commit artifact authority and receives no local patch.
- Existing completeness, documentation-drift, immutable-dependency, package-closure, and
  selected-manifest models are reused unchanged. No new test or policy model is added.
- The already-addressed upstream Dagger long-query regression is not revalidated,
  recorded, or treated as Rust SDK acceptance evidence.
- Existing historical changelog/changie files are preserved; one new unreleased Rust SDK
  fragment supplies concise release-facing text without a publication promise.
- No task authorizes TUF/signing, GitHub Actions, crates.io publication, a Git tag,
  GitHub Release creation, Dagger CLI changes, Discord copy, or downstream consumer
  names. A later manual GitHub Release attachment remains outside this plan and requires
  direct authorization.
