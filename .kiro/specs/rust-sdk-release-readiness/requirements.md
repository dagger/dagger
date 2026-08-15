# Requirements Document

## Introduction

This specification defines the deliberately small, non-publishing boundary required to
assess and document the merged Rust SDK at Dagger commit
`0513782e713257a9285b101f45230af00e3558d8`. It adds no SDK capability. It aligns the
maintained Rust documentation and child specifications with merged code, builds exact-
commit artifacts through the normal Rust SDK module entry points, and prepares release-
facing text.

The exact merged commit is the implementation authority. The agreed source package
version is `1.0.0-beta.11.rust.1`; the corresponding release identity is
`v1.0.0-beta.11+rust.1`. This specification authorizes neither publication nor a release
action.

## Glossary

- **Target_Commit:** Dagger commit
  `0513782e713257a9285b101f45230af00e3558d8`.
- **Source_Version:** Cargo-compatible version `1.0.0-beta.11.rust.1`.
- **Release_Identity:** Git release label `v1.0.0-beta.11+rust.1`.
- **Engine_Free_Check:** A canonical direct Cargo or Go check that exercises Rust SDK
  implementation, fixture, generated ownership, or ABI behavior without constructing
  or invoking a Dagger engine.
- **Engine_Backed_Assembly:** The separate boundary that builds RustEngineContent and a
  Complete_Engine through Ordinary_Build and validates one isolated external consumer
  through Ordinary_Verification.
- **Ordinary_Build:** The non-publishing `Build` entry point in
  `.dagger/modules/rust-client-dev`.
- **Ordinary_Verification:** The `Verify` entry point on the result of Ordinary_Build.
- **Public_Package_Set:** Exactly the `dagger-sdk-macros` and `dagger-sdk` crate package
  artifacts.
- **RustEngineContent:** The Rust SDK OCI content produced from the Target_Commit
  workspace and selected into Complete_Engine.
- **Complete_Engine:** The standard beta.11-based engine container composed with
  RustEngineContent.
- **Artifact_Output:** The devbox runtime directory
  `/workspaces/artifacts/0513782e7/`.
- **Runtime_Safety_Identity:** Runtime, dependency, lockfile, binary, CLI artifact, generated
  header, operation-manifest, or generated-file identity used to prevent unsafe reuse,
  mutation, credential exposure, or ownership ambiguity. It is not release signoff.
- **Internal_Publication:** Failure-atomic publication of generated files, operation
  manifests, cache entries, or one terminal Result_Sink outcome. It is not external
  crate or hosted-release publication.
- **Obsolete_Publication_Machinery:** Removed automation or specification claims for
  crates.io publication, hosted-release publication, signoff provenance, or dedicated
  Rust SDK security/platform release gates.
- **Maintained_Documentation_Scope:** The nine current documentation surfaces listed in
  the Documentation Cleanup Policy.
- **Maintained_Child_Spec_Scope:** The seven accepted Rust SDK child specifications
  listed in the Child-Spec Cleanup Policy. The clean umbrella and this readiness spec
  are excluded.
- **Historical_Release_Record:** `sdk/rust/CHANGELOG.md` and `sdk/rust/.changes/**`, whose
  earlier crates.io links describe releases that actually existed and are excluded from
  present-tense publication-promise scans.
- **Readiness_Blocker:** A failed mandatory check that prevents artifact or release
  readiness from being claimed.

## Target State

Current maintained Rust documentation describes the implemented SDK, its idiomatic Rust
API, exact beta compatibility, two public package artifacts, and the separation between
Engine_Free_Checks and Engine_Backed_Assembly. It makes no present-tense crates.io
publication promise. The final release procedure ends at exported packages, complete
OCI archive, checksums, and a separately invoked manual GitHub Release path that requires
direct authorization.

The accepted Features 1–7 remain unchanged in capability and ownership. Their maintained
child specifications no longer route work through `SDK_Signoff`, the deleted
`rust-sdk-security.yml`, F8/F10-derived release machinery, future Feature 8/9 release
owners, or obsolete external publication flow. Internal_Publication and
Runtime_Safety_Identity remain intact because they enforce real generated ownership, atomicity,
credential safety, dependency immutability, and clean runtime construction.

The upstream Dagger long-query regression is historical context only. Dagger already
addressed and tested that issue; this Rust SDK readiness boundary neither revalidates it
nor treats it as SDK acceptance evidence. Engine source changes and upstream regression
ownership remain outside this feature.

For a passing exact-build path, Ordinary_Build and Ordinary_Verification produce and
validate the Public_Package_Set, RustEngineContent, a Complete_Engine OCI archive,
checksums, and one isolated external Rust consumer result.

## Evidence From Current Code

All repository evidence below is pinned to Target_Commit.

- **Source identity:** `sdk/rust/Cargo.toml` declares workspace version
  `1.0.0-beta.11.rust.1`, Rust 1.97.1, edition 2024, and exact internal workspace
  dependency versions.
- **Ordinary package and engine build:**
  `.dagger/modules/rust-client-dev/main.go:1285-1395` defines a non-publishing build,
  packages exactly `dagger-sdk-macros` and `dagger-sdk`, validates those packages,
  builds RustEngineContent, and composes and validates a complete engine.
- **Ordinary external verification:**
  `.dagger/modules/rust-client-dev/main.go:1419-1465` unpacks the two packages and runs
  the isolated external consumer against Complete_Engine.
- **Complete engine composition:** `.dagger/modules/engine-dev/main.go` and
  `.dagger/modules/engine-dev/build/sdk.go` overlay RustEngineContent into the standard
  engine while retaining the normal engine and CLI binaries.
- **Immutable generated dependency:**
  `sdk/rust/crates/dagger-sdk-engine/src/model.rs`, `scalar.rs`, and `descriptor.rs`
  admit an exact registry version or a credential-free HTTPS Git repository plus a full
  lowercase 40-character commit. They reject path, mutable branch, tag-only, default,
  credential-bearing, or malformed coordinates.
- **Real safety provenance:** `sdk/rust/ARCHITECTURE.md`, `ENGINE_INTEGRATION.md`,
  `CLIENT_GENERATION.md`, and `MAINTAINING.md` document operation manifests, generated
  ownership, immutable dependency identity, and clean runtime promotion. Those controls
  are implementation safety, not the removed signoff system.
- **Removed machinery:** Target_Commit contains neither the former signoff implementation
  nor `.github/workflows/rust-sdk-security.yml`; PR #69 replaced those surfaces with
  ordinary build and verification.
- **Stale maintained documentation:** the root and crate READMEs still promise
  `cargo add dagger-sdk`; engine and maintenance guides still contain deleted workflow,
  release-evidence, Feature closure, crates.io, and historical command language.
- **Stale child-spec terminology:** client-generation, engine-integration, and
  module-authoring specs still define `SDK_Signoff`; all seven child specs contain some
  obsolete future Feature 8/9 release ownership.
- **Protected historical branch:** remote branch
  `codex/rust-sdk-f8-signoff-archive` was observed at
  `a9f55dd48c88b91b69e6e36c8289178362ad979e` and is read-only for this work.
- **Upstream regression boundary:** the previously reported long-running Dagger query
  regression was addressed and tested by Dagger. It is historical context only and is
  not a Rust SDK readiness check, record, or ownership transfer.

## Artifact Contract Policy

| Surface | Required policy | Invalid state | Side-effect boundary |
|---|---|---|---|
| Source revision | Exact detached Target_Commit, clean status, no `._*` files | Any revision, worktree mutation, or AppleDouble file mismatch | Stop before validation or build |
| Source version | Exact Source_Version in Cargo source and package metadata | Missing or inconsistent package identity | Stop before artifact acceptance |
| Release label | Record Release_Identity in release-facing text only | Different tag/version mapping | Stop documentation acceptance; do not create a tag |
| Public packages | Exactly `dagger-sdk-macros` and `dagger-sdk` package artifacts | Missing, extra, unpackable, or inconsistent package | Stop artifact acceptance; never publish |
| Rust content | RustEngineContent produced from Target_Commit | Missing or mismatched selected Rust manifest | Stop engine acceptance |
| Engine artifact | Complete OCI engine containing selected RustEngineContent | Missing standard engine binary, CLI binary, or selected Rust manifest | Stop artifact acceptance |
| Checksums | Deterministic checksum manifest covering downloadable artifacts | Missing artifact, duplicate path, or checksum mismatch | Stop download/shutdown completion |
| External consumer | One isolated Rust consumer compiled from packaged crates and run against Complete_Engine | Workspace-path dependency, wrong engine, failed query, or unclean close | Stop Ordinary_Verification |

## Documentation Cleanup Policy

| Document | Required current policy | Content that must remain | Invalid current state |
|---|---|---|---|
| `sdk/rust/README.md` | Current capability overview, exact compatibility, client quickstart, and Development and release builds section | Owned client, transport, diagnostics, engine integration, module authoring, client generation, package/toolchain links | `cargo add dagger-sdk`, stable-publication claim, or missing engine-free/engine-backed split |
| `sdk/rust/crates/dagger-sdk/README.md` | Installation path supported by final artifacts only | Owned lifecycle, quickstart, macro companion, standalone-client reuse, feature matrix | Unqualified crates.io installation or release-evidence wording |
| `sdk/rust/ARCHITECTURE.md` | Exactly two public package artifacts and durable local/assembly boundary | Strong ownership, safety, operation manifests, runtime and generated-file provenance | “Two publishable crates” or signoff interpretation of Runtime_Safety_Identity |
| `sdk/rust/CONTRIBUTING.md` | Canonical direct Cargo/Go checks explicitly engine-free; complete-engine verification separate | Peer authorities, toolchain, generated ownership, focused commands | Release-automation implication or local checks described as engine construction |
| `sdk/rust/ENGINE_INTEGRATION.md` | Current Build/Verify boundary and immutable dependency policy | Runtime safety, direct checks, focused regressions, HTTPS plus full commit | Deleted workflow, old release matrix, Feature closure/evidence flow, or canonical crates.io path |
| `sdk/rust/MAINTAINING.md` | Six-step non-publishing release procedure | Generated ownership, check/update, rollback, target refresh, direct authorization | Deleted workflow, admitted release evidence, crate publication, or unauthorized hosted release |
| `sdk/rust/MODULE_AUTHORING.md` | Current engine-free module checkpoint and separate completed-engine boundary | Real macros, dispatch, Result_Sink, cancellation, generated ownership | Feature-end/signoff/publication-flow language |
| `sdk/rust/CLIENT_GENERATION.md` | Current engine-free client checkpoint, immutable dependency, and Ordinary_Verification boundary | Initialization, typed API, caller ownership, manifest-last generation | Published SDK assumption or Feature/signoff/evidence flow |
| `sdk/rust/completeness/README.md` | Current peer-authority and contract-maintenance guide | Deterministic derivation, isolated staging, source/evidence scope, safety provenance | Initial-F1 expected counts, future-Feature release routing, or release acceptance claim |

## Child-Spec Cleanup Policy

| Child specification | Preserve | Remove or durably reframe |
|---|---|---|
| `rust-sdk-completeness-contract` | Deterministic inventory/ledger/report, isolated staging, normalized secret-free outcomes | Future Feature 8/9 conformance, publication, migration, and release profiles |
| `rust-sdk-client-lifecycle` | Owned client, shared session, close election, cancellation, typed configuration and errors | Future Feature 8/9 live conformance, migration, publication, and final gate |
| `rust-sdk-transport-observability` | Verified CLI download, redaction, trace propagation, artifact provenance, atomic cache publication | Future Feature 8/9 platform/security/release owners and obsolete beta.10 release gate |
| `rust-sdk-core-codegen` | Pure checked generator, exact schema mapping, generated ownership, scoped atomic publication | Future Feature 8/9 conformance, migration, release assets, and stable gate |
| `rust-sdk-engine-integration` | Engine-free checks, focused regressions, immutable dependency, runtime provenance, confined publication | `SDK_Signoff`, deleted workflow, evidence admission, sole-public-crate and release owner claims |
| `rust-sdk-module-authoring` | Two package artifacts, compiler/dispatcher, Result_Sink, cancellation, manifest-last assets | `SDK_Signoff`, deleted workflow, signoff manifests, Feature 8/9 release self-hosting |
| `rust-sdk-client-generation` | Engine-free project/runtime checks, immutable dependency, authored-byte preservation | `SDK_Signoff`, deferred verdict plane, Feature 8/9 signoff/publication ownership |

## Requirements

### Requirement 1: Exact Ground Truth and Specification Hygiene

**User Story:** As a Rust SDK maintainer, I want release-readiness work bounded to the
merged implementation, so that obsolete delivery machinery is removed without erasing
accepted capability or safety contracts.

#### Acceptance Criteria

1. WHEN release-readiness work begins, THE working repository SHALL resolve exactly to
   Target_Commit.
2. WHEN repository cleanliness is checked, THE exact-build repository SHALL have no
   tracked or untracked changes and no `._*` files.
3. WHEN child-spec cleanup is evaluated, THE scope SHALL contain exactly the seven
   directories in the Child-Spec Cleanup Policy.
4. WHEN child-spec cleanup is evaluated, THE clean umbrella and this release-readiness
   specification SHALL remain outside the cleanup scope.
5. WHEN child specifications are aligned with Target_Commit, THE maintained child
   specifications SHALL contain no `SDK_Signoff` process.
6. WHEN child specifications are aligned with Target_Commit, THE maintained child
   specifications SHALL contain no reference to
   `.github/workflows/rust-sdk-security.yml`.
7. WHEN obsolete Feature 8, Feature 9, F8, or F10 release routing is encountered, THE
   cleanup SHALL remove or durably reframe that routing without recreating equivalent
   machinery under another name.
8. WHEN obsolete external crate or hosted-release publication ownership is encountered,
   THE cleanup SHALL replace it with the non-publishing Ordinary_Build boundary or
   remove it if no current owner exists.
9. WHEN Internal_Publication is encountered, THE cleanup SHALL preserve its accepted
   atomicity, ownership, and failure behavior.
10. WHEN Runtime_Safety_Identity is encountered, THE cleanup SHALL preserve its identity,
    immutability, credential-safety, and clean-runtime behavior.
11. WHEN child specifications are edited, THE cleanup SHALL preserve all implemented
    Features 1–7 capability requirements except text exclusively describing removed
    delivery, signoff, workflow, or external publication machinery.
12. WHEN Historical_Release_Records are scanned, THE cleanup SHALL preserve truthful
    historical crates.io links and release descriptions.
13. WHILE this work is active, THE protected historical branch SHALL remain unchanged at
    its observed commit.
14. WHILE this work is active, THE implementation SHALL change no Rust SDK source,
    test, generated file, Cargo manifest or lockfile, Go integration code, Dagger module,
    or CLI code; tracked edits SHALL be limited to the approved documentation, child
    specifications, release-readiness specification, and one release-note fragment.

### Requirement 2: Upstream Dagger Regression Exclusion

**User Story:** As a Rust SDK maintainer, I want an already-addressed upstream Dagger
regression kept outside this work, so that Rust SDK readiness does not claim ownership
of Dagger's fix or duplicate its validation.

#### Acceptance Criteria

1. WHEN this readiness work is executed, THE operator SHALL NOT run or record a dedicated
   long-query reproducer as Rust SDK acceptance evidence.
2. WHEN the previously reported Dagger long-query regression is mentioned, THE
   documentation SHALL identify it only as historical upstream context already addressed
   and tested by Dagger.
3. IF Ordinary_Build or Ordinary_Verification fails, THEN THE operator SHALL report the
   ordinary failure without attributing it to the historical long-query regression or
   changing engine source in this feature.

### Requirement 3: Exact Non-Publishing Artifacts

**User Story:** As a release operator, I want exact-commit packages and engine artifacts
validated by an isolated consumer, so that any later authorized release uses known bytes
rather than earlier build evidence.

#### Acceptance Criteria

1. WHEN preparing every Dagger build invocation, THE operator SHALL identify the
   `dagger` binary and record its reported version.
2. WHEN beginning every build session, THE operator SHALL recheck Docker availability
   and record the result.
3. WHILE a manually managed runner is used, THE operator SHALL set
   `_EXPERIMENTAL_DAGGER_RUNNER_HOST` on every Dagger invocation.
4. WHEN artifacts are built, THE operator SHALL use the normal
   `.dagger/modules/rust-client-dev` Ordinary_Build and Ordinary_Verification entry
   points without a historical wrapper.
5. WHEN package checks complete, THE Public_Package_Set SHALL contain exactly one
   `dagger-sdk-macros` package and one `dagger-sdk` package at Source_Version.
6. WHEN Ordinary_Build constructs Rust content, THE result SHALL be RustEngineContent
   produced from Target_Commit.
7. WHEN Ordinary_Build completes, THE Complete_Engine SHALL contain the standard engine
   binaries and the selected RustEngineContent.
8. WHEN the engine artifact is exported, THE exported artifact SHALL be a complete OCI
   archive rather than isolated RustEngineContent.
9. WHEN Ordinary_Verification runs, THE isolated consumer SHALL resolve only the
   unpacked Public_Package_Set and SHALL execute successfully against Complete_Engine.
10. WHEN downloadable artifacts are finalized, THE checksum manifest SHALL cover both
    package files and the complete OCI archive.
11. WHEN artifacts are written in the devbox, THE operator SHALL place them under
    Artifact_Output and not under `/tmp`.
12. WHEN exact artifacts are prepared, THE output SHALL contain no F8/F10 signoff
    provenance, conformance verdict, TUF metadata, or publication credential material.
13. WHEN Ordinary_Build or Ordinary_Verification fails, THE operator SHALL report a
    Readiness_Blocker and SHALL NOT substitute an artifact from another revision or an
    earlier build.

### Requirement 4: Current Maintained Documentation

**User Story:** As a Rust SDK consumer and maintainer, I want current capability,
development, safety, and release documentation, so that I can use and validate the beta
SDK without relying on obsolete publication or delivery history.

#### Acceptance Criteria

1. WHEN documentation cleanup is evaluated, THE scope SHALL contain exactly the nine
   files in the Documentation Cleanup Policy.
2. WHEN `sdk/rust/README.md` is prepared, THE README SHALL describe the currently
   implemented client, transport, observability, generated Core API, engine integration,
   module authoring/dispatch, standalone-client, package, and toolchain capabilities.
3. WHEN the README explains capability authority, THE README SHALL state the peer scope
   of the engine schema/protocol, target-compatible `sdk-sdk` checks, pinned definitive
   Go SDK/tests, and idiomatic Rust policy without assigning blanket precedence.
4. WHEN the README compares Rust and Go, THE README SHALL avoid claiming source,
   package-layout, ownership, or API-shape compatibility.
5. WHEN the root README is prepared, THE README SHALL contain one concise client
   quickstart and one concise module-authoring quickstart or direct minimal invocation.
6. WHEN the root README is prepared, THE README SHALL contain a short Development and
   release builds section that distinguishes Engine_Free_Checks from
   Engine_Backed_Assembly.
7. WHEN either README describes installation, THE documentation SHALL describe only a
   path supported by final artifacts and SHALL NOT promise current crates.io
   availability.
8. WHEN `ARCHITECTURE.md` describes package boundaries, THE document SHALL say exactly
   two public package artifacts rather than two publishable crates.
9. WHEN `ARCHITECTURE.md` describes runtime or generated-file provenance, THE document
   SHALL identify Runtime_Safety_Identity as a genuine safety mechanism rather than release
   signoff.
10. WHEN `CONTRIBUTING.md` lists direct Cargo and Go checkpoints, THE document SHALL
    preserve their commands and SHALL state that they do not construct a Dagger engine.
11. WHEN `CONTRIBUTING.md` describes complete-engine verification, THE document SHALL
    keep it separate from Engine_Free_Checks.
12. WHEN `ENGINE_INTEGRATION.md` is prepared, THE document SHALL contain no deleted
    workflow, old release matrix, Feature-number closure, release-evidence flow, or
    canonical crates.io dependency path.
13. WHEN `ENGINE_INTEGRATION.md` describes a generated Git dependency, THE document
    SHALL require a credential-free canonical HTTPS repository and a full reachable
    lowercase 40-character commit.
14. WHEN `ENGINE_INTEGRATION.md` describes ordinary readiness, THE document SHALL use
    the current Ordinary_Build and Ordinary_Verification entry points while preserving
    focused engine cases as regression tools.
15. WHEN `MAINTAINING.md` describes release preparation, THE procedure SHALL start with
    canonical Engine_Free_Checks and direct Go checks.
16. WHEN `MAINTAINING.md` describes package preparation, THE procedure SHALL validate
    exactly the Public_Package_Set without publishing it.
17. WHEN `MAINTAINING.md` describes engine preparation, THE procedure SHALL build
    RustEngineContent and Complete_Engine.
18. WHEN `MAINTAINING.md` describes consumer validation, THE procedure SHALL use
    Ordinary_Verification against Complete_Engine.
19. WHEN `MAINTAINING.md` describes output finalization, THE procedure SHALL export the
    packages, complete OCI archive, and checksums.
20. WHEN `MAINTAINING.md` describes a GitHub Release, THE procedure SHALL require a
    separately invoked manual path and direct authorization.
21. WHEN `MODULE_AUTHORING.md`, `CLIENT_GENERATION.md`, or
    `completeness/README.md` is prepared, THE cleanup SHALL preserve implemented module
    support, engine-free fixtures, generated ownership, completeness checks,
    Internal_Publication, and Runtime_Safety_Identity.
22. WHEN maintained documentation is prepared, THE cleanup SHALL remove stale Feature,
    signoff, release-evidence, and external publication language without replacing it
    with a new verdict system.
23. WHEN compatibility is stated, THE documentation SHALL identify Source_Version and
    the beta.11-based Complete_Engine validated from Target_Commit without claiming an
    untested engine range.
24. WHEN release identity is stated, THE documentation SHALL map Release_Identity to
    Source_Version exactly.
25. WHEN release-facing text names consumers, THE text SHALL use generic descriptions
    and SHALL NOT name a downstream consumer product or platform.
26. WHEN release-facing documentation is completed, THE documentation SHALL contain no
    instruction or automation that publishes to crates.io or creates a hosted release.
27. WHEN this specification completes, THE operator SHALL NOT create or push a Git tag,
    create a GitHub Release, or publish a crate without separate direct authorization.

### Requirement 5: Documentation and Child-Spec Acceptance

**User Story:** As a release reviewer, I want both prohibited-language scans and positive
boundary assertions, so that cleanup cannot pass by deleting real safety or capability
content.

#### Acceptance Criteria

1. WHEN the hard-zero scan runs over Maintained_Documentation_Scope and
   Maintained_Child_Spec_Scope, THE scan SHALL find no `F8`, `F10`, `SDK_Signoff`, or
   `rust-sdk-security.yml` process reference.
2. WHEN obsolete delivery-language review runs, THE maintained scope SHALL contain no
   forward-looking Feature 8/9 release owner, Feature-end gate, release-evidence flow,
   release-signed-off state, signoff gate/matrix/manifest, final SemVer gate, release
   asset owner, or public release automation.
3. WHEN present-tense package installation is scanned, THE maintained scope SHALL
   contain no `cargo add dagger-sdk`, exact-published-SDK assumption, canonical crates.io
   release path, or statement that this flow publishes crates.
4. WHEN publication or provenance language is reviewed, THE acceptance check SHALL
   preserve Internal_Publication, `publish = false`, public/private package
   classification, Runtime_Safety_Identity, and the directly authorized manual GitHub Release
   boundary.
5. WHEN positive local-check assertions are evaluated, THE maintained documentation
   SHALL state that canonical format, check, test, Clippy, rustdoc, deny, and focused
   Cargo/Go checks are engine-free.
6. WHEN focused engine cases are documented, THE maintained documentation SHALL identify
   them as regression tools rather than local checkpoints or release evidence.
7. WHEN positive build assertions are evaluated, THE maintained documentation SHALL
   state that Ordinary_Build produces exactly the Public_Package_Set,
   RustEngineContent, and Complete_Engine.
8. WHEN positive verification assertions are evaluated, THE maintained documentation
   SHALL state that Ordinary_Verification runs one isolated external consumer against
   Complete_Engine.
9. WHEN release publication assertions are evaluated, THE maintained documentation
   SHALL state that no crate publication occurs and manual GitHub Release attachment
   requires a separately invoked path plus direct authorization.
10. WHEN generated dependency assertions are evaluated, THE maintained documentation
    SHALL require credential-free canonical HTTPS plus a full lowercase 40-character
    commit and SHALL reject path, branch, tag, default, and credential-bearing sources.
11. WHEN Historical_Release_Records are scanned, THE acceptance check SHALL exclude them
    from present-tense crates.io and delivery-language failures.
12. WHEN cleanup acceptance completes, THE changed scope SHALL add no TUF, GitHub
    Actions, Dagger CLI surface, or downstream consumer name.

### Requirement 6: Controlled Artifact Retrieval and Shutdown

**User Story:** As a release operator, I want validated artifacts retrieved before the
remote builder is paused, so that volatile engine state cannot strand the exact outputs.

#### Acceptance Criteria

1. WHEN package files, the OCI archive, and checksums are complete, THE operator SHALL
   download all of them from Artifact_Output before shutting down the devbox.
2. WHEN downloaded artifacts are compared with the checksum manifest, THE local copies
   SHALL match every recorded checksum.
3. WHEN remote work is complete, THE operator SHALL remove only
   `/.namespace/tasks/kiro-rust-sdk-readiness-0513782e7` from the task namespace.
4. WHEN required outputs are downloaded and checksums match, THE operator SHALL force
   shutdown `dag-rust-xl`.
5. IF any required artifact is absent or a downloaded checksum differs, THEN THE
   operator SHALL leave the task incomplete and SHALL NOT claim retrieval success.

## Explicit Non-Goals

- TUF metadata or signing.
- New or replacement GitHub Actions workflows.
- crates.io publication or publication credentials.
- Git tag creation or push.
- GitHub Release creation without direct authorization.
- Dagger CLI behavior or source changes.
- F8/F10 signoff, provenance, conformance, security-matrix, or platform-matrix systems.
- Any committed readiness policy engine, acceptance runner, evidence registry, verdict
  schema, readiness database, or Rust SDK source/test/generated-code change.
- Broad SDK capability changes or refactors.
- Engine fixes or dedicated revalidation of already-addressed upstream Dagger regressions
  inside this feature specification.
- Named downstream consumer products or platforms.
