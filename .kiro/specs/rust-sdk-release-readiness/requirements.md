# Requirements Document

## Introduction

This specification finishes the Rust SDK release-readiness work with an ordinary,
non-publishing build. It corrects an over-broad preservation decision in the first
readiness draft: the historical completeness/evidence system is build apparatus, not a
maintained SDK capability. It is removed while the SDK, module support, runtime safety,
generator atomicity, package validation, complete-engine assembly, and external-consumer
test remain.

The source package version is `1.0.0-beta.11.rust.1`; the corresponding manual release
identity is `v1.0.0-beta.11+rust.1`. This specification authorizes neither publication nor
a release action.

## Ground Truth

- PR #69 removed the Feature 8 signoff implementation and introduced the ordinary
  `Build`/`Verify` path.
- The older `dagger-sdk-completeness` crate and `sdk/rust/completeness` registry remained
  coupled to code generation and build validation.
- After the upstream merge, a harmless documentation/specification change caused
  authority, policy, mapping, observation, closure, baseline, and registry digests to
  cascade. That behavior is the machinery this cleanup removes.
- `dagger-bootstrap` already owns a compact generated-artifact manifest, checked
  formatting, exhaustive owned-file comparison, and failure-atomic publication. The
  completeness layer only extends that manifest with capability/evidence joins.
- The package/engine checker and its two property tests are useful ordinary build checks
  and can be retained without the completeness crate.

## Glossary

- **Maintained_SDK:** The public Rust crates, internal generator/engine crates, module
  runtime, Dagger development module, genuine tests, and current documentation.
- **Codegen_Inputs:** The exact target descriptor and schema used by Rust code generation.
- **Ownership_Manifest:** The compact generator-owned list of generated paths, byte and
  semantic hashes, kinds, and exact target identity.
- **Registry_Machinery:** Completeness inventories, ledgers, mappings, authorities,
  evidence registries, closure reports, pinned harness snapshots, feature-scope policy,
  and executables that derive or admit them.
- **Ordinary_Build:** `.dagger/modules/rust-client-dev` `Build`, which packages exactly
  `dagger-sdk-macros` and `dagger-sdk`, builds Rust engine content, and assembles a
  complete engine.
- **Ordinary_Verification:** `Verify` on the Ordinary_Build result, which compiles one
  external Rust consumer from the packaged crates and runs it against that engine.
- **Runtime_Safety:** Engine version/revision compatibility, credential-safe diagnostics,
  downloaded CLI checksum verification, immutable generated dependencies, owned-file
  publication, and clean runtime promotion.
- **Artifact_Platform:** `linux/amd64`.
- **Artifact_Output:** `/workspaces/artifacts/<exact-commit>/` on the documented
  `dagger-rust-builder-xl` Namespace devbox.

## Deletion Allowlist

The cleanup may delete or replace only the following build-apparatus surfaces:

| Surface | Required result |
|---|---|
| `sdk/rust/crates/dagger-sdk-completeness/**` | Delete the crate after relocating the ordinary checker and its two useful property tests |
| `sdk/rust/completeness/**` | Delete registry machinery; move only the target and schema into `sdk/rust/codegen/` |
| `.dagger/modules/rust-client-dev/completeness.go` | Delete completeness integrity/render/harness operations |
| Rust-client-dev evidence wrappers | Delete registry-producing core/engine evidence entry points and mapping-only fields; retain direct resolution and engine-integration tests |
| Generated Dagger bindings | Regenerate after the public development-module surface shrinks |
| `dagger-bootstrap` generation boundary | Remove ledger/mapping inputs; emit the existing compact ownership projection |
| Workspace manifests/lockfile | Remove the completeness member/dependency and add only dependencies required by the relocated checker |
| Completed Features 1–7 child specs | Delete after preserving enduring capability requirements in the umbrella; Git history retains delivery detail |
| Current docs and umbrella | Remove completeness/evidence-registry maintenance claims while preserving capability and safety requirements |

Git history and `codex/rust-sdk-f8-signoff-archive` remain the historical record. No
historical branch is rewritten.

## Requirements

### Requirement 1: Remove Registry Machinery

**User Story:** As a maintainer, I want ordinary Rust changes to avoid cascading evidence
digest rebuilds, so that SDK development uses normal source, package, and engine checks.

#### Acceptance Criteria

1. WHEN cleanup completes, THE maintained workspace SHALL contain no
   `dagger-sdk-completeness` package or dependency.
2. WHEN cleanup completes, THE maintained source tree SHALL contain no completeness
   authority, inventory, ledger, mapping, evidence registry, closure observation,
   feature-scope admission, pinned baseline harness, or registry generator.
3. WHEN code generation runs, THE generator SHALL consume only Codegen_Inputs and its
   prior Ownership_Manifest.
4. WHEN code generation checks unchanged source, THE generator SHALL report no drift.
5. WHEN code generation updates changed source, THE generator SHALL preserve checked
   formatting, complete owned-file comparison, obsolete-file removal, and failure-atomic
   publication.
6. WHEN the development Dagger module is regenerated, ITS public surface SHALL contain
   no completeness integrity, artifact, harness, core-evidence, or engine-evidence
   operation.
7. WHEN repository terminology is scanned, legitimate runtime compatibility evidence
   and operation-manifest safety terms MAY remain; Registry_Machinery SHALL NOT be
   recreated under a new name.

### Requirement 2: Preserve SDK Capability and Safety

**User Story:** As a Rust SDK user, I want cleanup to remove build bureaucracy rather
than functionality, so that the client and module SDK remain complete and safe.

#### Acceptance Criteria

1. WHEN the workspace test suite runs, THE public client, macros, generated Core API,
   transport, observability, lifecycle, engine adapter, module initialization,
   compilation, dispatch, cancellation, and standalone-client tests SHALL remain.
2. WHEN implicit connection compatibility is evaluated, THE SDK SHALL continue to check
   engine version and clean revision identity.
3. WHEN the SDK downloads a CLI, IT SHALL continue to verify the declared checksum before
   reuse.
4. WHEN diagnostics contain credentials or sensitive paths, THEY SHALL continue to be
   redacted or bounded by the existing credential-safe diagnostic policy.
5. WHEN generated dependencies are selected, THEY SHALL remain immutable and
   credential-free according to the current registry-or-exact-HTTPS-Git policy.
6. WHEN focused engine integration is used during development, ITS real resolution,
   initialization, operation, runtime, ownership, lock/toolchain, and redaction cases
   SHALL remain available as tests rather than release evidence.
7. WHEN cleanup is reviewed, IT SHALL introduce no Dagger CLI surface change and no
   downstream consumer product or platform name.

### Requirement 3: Retain Ordinary Build Validation

**User Story:** As a release operator, I want small Rust-native build checks, so that the
two packages and complete engine are validated without an evidence framework.

#### Acceptance Criteria

1. WHEN the ordinary checker is built, IT SHALL be owned by `dagger-bootstrap` or an
   equivalently narrow internal Rust package and SHALL NOT depend on Registry_Machinery.
2. WHEN package validation runs, IT SHALL accept exactly one `dagger-sdk-macros` archive
   and one `dagger-sdk` archive at the workspace version with the required dependency and
   file closure.
3. WHEN complete-engine validation runs, IT SHALL require the selected Rust manifest to
   equal the Rust engine-content manifest and require its blob to exist while tolerating
   unrelated standard engine content.
4. WHEN package archives are unpacked for Ordinary_Verification, traversal, non-file
   entries, duplicates, invalid roots, and non-empty destinations SHALL remain rejected.
5. FOR ANY generated package/engine validation input, THE existing property tests SHALL
   continue to exercise exact public-package closure and selected-manifest correctness.

### Requirement 4: Produce Exact Non-Publishing Artifacts

**User Story:** As a release operator, I want exact-commit artifacts and one external
consumer result, so that a later authorized GitHub Release can attach known bytes.

#### Acceptance Criteria

1. WHEN final validation begins, THE repository SHALL be at one exact clean commit with
   no `._*` files.
2. WHEN ordinary checks run, THE operator SHALL use normal Cargo format, check, test,
   Clippy, rustdoc, package, and focused Go checks without an evidence registry.
3. WHEN Ordinary_Build runs, IT SHALL target Artifact_Platform explicitly and produce
   exactly the two public crate packages, Rust engine content, and the complete Dagger
   engine containing that content.
4. WHEN Ordinary_Verification runs, ONE external Rust consumer SHALL resolve the unpacked
   packages and query the completed engine successfully.
5. WHEN artifacts are exported, THE output SHALL contain the two `.crate` files, one
   complete engine OCI archive, and `SHA256SUMS` under Artifact_Output.
6. WHEN checksums are verified, THE manifest SHALL cover exactly those three downloadable
   artifacts with lowercase SHA-256 values.
7. IF any required check fails, THEN no earlier artifact or different revision SHALL be
   substituted.

### Requirement 5: Documentation and Release Boundary

**User Story:** As a maintainer, I want concise current documentation, so that consumers
understand the Rust-first, engine-free development path and the separate engine assembly.

#### Acceptance Criteria

1. WHEN current Rust SDK documentation is scanned, IT SHALL describe normal Rust checks
   as engine-free and Ordinary_Build/Ordinary_Verification as the engine-backed boundary.
2. WHEN code generation is documented, IT SHALL describe Codegen_Inputs and the compact
   Ownership_Manifest rather than completeness/evidence registries.
3. WHEN release preparation is documented, IT SHALL describe artifact creation only and
   SHALL NOT promise crates.io publication.
4. WHEN Release_Identity is used, IT SHALL map exactly to source version
   `1.0.0-beta.11.rust.1`.
5. WHILE this specification is executed, THE operator SHALL NOT add TUF, GitHub Actions,
   automated publication, release credentials, or a new readiness policy engine.
6. UNTIL the user directly authorizes publication, THE operator SHALL NOT create or push
   a tag, publish a crate, or create a GitHub Release.

### Requirement 6: Devbox Operation and Retrieval

**User Story:** As an operator, I want the fast Linux builder used safely, so that exact
artifacts survive devbox reactivation and shutdown.

#### Acceptance Criteria

1. WHEN `dagger-rust-builder-xl` is used, THE checkout SHALL be fresh or revalidated,
   detached at the intended commit, clean, and treated as a builder rather than source
   of truth.
2. WHEN a build session begins, THE operator SHALL recheck Docker state and the Dagger
   CLI version.
3. WHILE a manually managed runner is used, THE operator SHALL set
   `_EXPERIMENTAL_DAGGER_RUNNER_HOST` on every Dagger command.
4. BEFORE long work, THE operator SHALL create one specifically named activity marker
   and SHALL remove only that marker after retrieval succeeds.
5. WHEN artifacts complete, THE operator SHALL download them from Artifact_Output and
   independently verify their checksums before the devbox is paused or stopped.

## Explicit Non-Goals

- TUF, signing, attestations, provenance bundles, or evidence registries.
- GitHub Actions additions.
- crates.io publication.
- Dagger CLI behavior changes.
- New SDK capability work.
- A dedicated long-query engine-regression fix or acceptance result.
- Any tag, GitHub Release, or external publication without direct user authorization.
