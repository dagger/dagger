# Requirements Document: Rust SDK Completeness Contract

## Introduction

This specification defines Feature 1 of the approved
`rust-sdk-complete-implementation` umbrella: the executable contract that makes
“Go-level Rust SDK completeness” exhaustive, reviewable, and resistant to drift. It
produces a pinned authority registry, a canonical capability inventory, a resolved
completeness ledger, deterministic validation, and separate integrity and completeness
verdicts. It does not implement the client, transport, code generator, module runtime,
or release capabilities owned by Features 2–9.

The initial target is Dagger repository commit
`25300124ca110612edc09c43f89cb5fad6028170`. Three peer authorities define different
parts of that target: the engine schema is definitive for wire shape;
[`dagger/sdk-sdk@8c16442`](https://github.com/dagger/sdk-sdk/commit/8c164424b7a8a37b33a77367ef7547490d5b87b5)
is the definitive executable authority for the common SDK-module conformance contract
it declares; and the Definitive_Go_SDK is the reference authority for client,
transport, code-generation, module, and other parity behaviours that the common
contract does not cover. The Go implementation informs conformance-harness coverage
but does not overrule a target-compatible Harness_Check within its declared scope.

The pinned SDK_Contract_Harness exposes a public `check` interface, individual check
identities, reusable `SdkTarget`, and `mod-test` assertions for the Rust conformance
profile. A green harness result proves only its mapped lifecycle assertions: the
pinned harness does not exercise `initClient`, typed-client/session/transport
behaviour, deep cross-SDK module semantics, or platforms beyond its `linux/amd64`
runner. Those present boundaries constrain today's evidence without diminishing the
harness's authority or preventing its contract from expanding through explicit target
transitions.

The engine's Go SDK implementation at the Target_Revision pins
`github.com/dagger/dagger-go-sdk` commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5`. That immutable commit is the definitive
Go client-library authority for this target. Although `core/sdk/go_sdk.go` comments the
pin as `v0.21.7`, the `v0.21.7` Git tag resolved on 2026-08-05 to
`5a8ad2cd8d2470c06517eddbf1a243c7c1798dc4` and has diverged from the pinned commit.
The contract therefore never treats a mutable or mismatched version label as source
identity.

The local `sdk/go/**` tree supplies an offline-readable mirror of the pinned client
source at the Target_Revision; representative client, session, telemetry, and test
blobs were verified byte-identical to the external pin. Engine-integrated Go module
behaviour remains defined by the Target_Revision's engine SDK contracts, Go SDK
implementation, generator, and active tests. The engine schema itself remains the
wire-shape authority.

This feature is foundational and has no implementation dependency on the later
features. Every later child specification consumes stable Capability_IDs from this
contract, updates their status and evidence, and leaves the validator able to detect
unclassified source changes. Feature 8 expands the conformance execution behind the
evidence records. Feature 9 consumes the Completeness_Verdict as a release gate.

## Glossary

- **Active_Capability:** A capability present in the selected Target_Descriptor and
  therefore required in the Resolved_Ledger.
- **Authority_Registry:** The checked-in declaration of authoritative repositories,
  immutable revisions, included source sets, explicit exclusions, and extraction
  methods.
- **Authority_Source:** One registered source from which capabilities or policy
  obligations are derived.
- **Behavioural_Capability:** An externally observable SDK behaviour not fully
  described by a single schema coordinate, such as connection precedence, module
  dispatch, or generated-client layout.
- **Blocking_Status:** `Missing` or `Partial`; either status makes the
  Completeness_Verdict fail while remaining a valid classification.
- **Canonical_Inventory:** The deterministic union of Schema_Capabilities,
  Behavioural_Capabilities, and Policy_Capabilities derived from the
  Authority_Registry.
- **Capability_Fingerprint:** A deterministic digest of the authoritative semantic
  shape represented by one Capability_ID.
- **Capability_ID:** A stable, globally unique identity for one atomic capability.
- **Classification_Rule:** A concise checked-in rule that expands to an expected set
  of capability records without allowing newly matched capabilities to be silently
  classified.
- **Complete_Status:** `Implemented`, `Idiomatic_Equivalent`, or `Inapplicable` with
  all status-specific evidence present.
- **Completeness_Ledger:** The checked-in classifications, evidence, and ownership
  data used to build the Resolved_Ledger.
- **Completeness_Verdict:** The verdict that is true only when the Integrity_Verdict is
  true and no Active_Capability has a Blocking_Status.
- **Definitive_Go_SDK:** The pinned Go client library plus the Target_Revision's Go
  engine SDK implementation, generator, and authoritative behaviour tests, used as
  the parity reference outside the engine schema and SDK_Contract_Harness scopes.
- **Drift:** An added, removed, or semantically changed authority item that is not
  reconciled by an explicit target transition or ledger update.
- **Evidence_Reference:** A pinned, machine-validatable reference supporting an
  authority claim, implementation claim, verification result, or reviewed decision.
- **Harness_Check:** One stable, independently executable `@check` identity exported
  by the pinned SDK_Contract_Harness, whether it tests the subject SDK or the harness
  itself.
- **Harness_Check_Kind:** `subject-conformance` when a check exercises the SDK under
  test, or `harness-self` when it exercises `sdk-sdk`'s own implementation.
- **Harness_Check_Mapping:** The exact relationship between one Harness_Check, its
  execution target and platform, and the Capability_IDs its assertions can prove.
- **Idiomatic_Equivalent:** Rust support with equivalent externally observable
  behaviour but a deliberately Rust-native public shape that materially differs from
  the Go mechanism.
- **Inapplicable:** A Go-specific capability with no meaningful Rust counterpart, as
  established by a reviewed decision; it never means merely unimplemented.
- **Integrity_Verdict:** The verdict that the target, authority sources, inventory,
  classifications, evidence shapes, and drift reconciliation are internally valid.
- **Policy_Capability:** A Rust quality or release obligation derived from the approved
  umbrella and `sdk/rust/AGENTS.md`, rather than from the engine schema or Go API.
- **Resolved_Ledger:** The deterministic, explicit record containing exactly one
  classification for every Active_Capability after Classification_Rules expand.
- **Schema_Capability:** One atomic public schema element or semantic property derived
  from canonical engine introspection.
- **Schema_Coordinate:** A stable identity locating a schema root, type, field,
  argument, input field, enum value, directive definition, or directive argument.
- **Schema_Snapshot:** Canonically ordered introspection of the public core engine
  schema for a Target_Revision, including deprecated elements.
- **SDK_Contract_Harness:** The independently pinned official `dagger/sdk-sdk`
  black-box harness that is the definitive executable conformance authority for the
  common SDK-module contract it declares.
- **Semantic_Signature:** The normalized shape of a capability, excluding incidental
  ordering and source line numbers while retaining behaviourally relevant metadata.
- **Source_Set:** The included paths and symbols, minus explicit exclusions, belonging
  to an Authority_Source.
- **Target_Descriptor:** The checked-in identity and compatibility metadata for the
  Dagger, engine schema, Go SDK, SDK_Contract_Harness, and Rust baseline being
  assessed.
- **Target_Transition:** An explicit, reviewable move from one Target_Descriptor to
  another with a semantic capability diff.
- **Verification_Evidence:** An executable check whose assertion proves the behaviour
  claimed by one or more ledger rows.

## Target State

The repository contains a hermetic, deterministic completeness contract that answers
five questions for any selected target:

1. What exact engine schema, SDK contract, and Go reference sources define the target?
2. What complete set of schema, behavioural, and Rust policy capabilities follows from
   those sources?
3. What is the Rust support status of every capability?
4. What pinned implementation, verification, or decision evidence justifies each
   complete claim?
5. Does the contract have integrity, and separately, is the Rust SDK complete?

Normal verification uses checked-in canonical inputs and repository sources without
consulting moving branches, tags, generated language bindings as schema authority, or
an unpinned network resource. An explicit target refresh may retrieve immutable remote
commits, but it produces a semantic transition diff for review.

The pinned SDK_Contract_Harness contributes an exhaustive inventory of its public
Harness_Check identities. Each check is classified by Harness_Check_Kind; every
`subject-conformance` check is mapped to the exact Capability_IDs and target scope that
its assertions cover before any result can become Verification_Evidence. Harness
self-checks protect the integrity of the imported harness but never count as Rust
capability evidence. Feature 8 expands this execution profile with Rust-native client,
transport, observability, semantic, security, and platform checks, reusing `SdkTarget`
and `mod-test` where their public contracts fit.

The initial ledger is expected to have a true Integrity_Verdict and a false
Completeness_Verdict. Existing Rust support is classified conservatively: code
presence can establish `Partial`, but only suitable executable evidence can establish
`Implemented` or `Idiomatic_Equivalent`. Every `Missing` and `Partial` capability is
assigned to Features 2–9. The contract becomes the stable spine through which later
features advance the same rows rather than maintaining separate informal checklists.

## Evidence From Current Code

Unless another revision is stated, repository citations refer to Dagger commit
`25300124ca110612edc09c43f89cb5fad6028170`.

- **Engine schema shape (authoritative):**
  `cmd/codegen/introspection/introspection.graphql:1-83` requests schema version,
  roots, all types, deprecated fields and inputs, interfaces, possible types, enum
  values, defaults, deprecations, and Dagger directive applications.
  `cmd/codegen/introspection/introspection.go:15-349` defines the consumed response
  model and every supported GraphQL type kind.
- **Public codegen traversal:** `cmd/codegen/introspection/visitor.go:12-72` defines the
  current generated-language traversal order and exclusions. It omits underscore-
  prefixed types and several built-in scalars, so the ledger must distinguish an
  explicit public-generation policy from raw introspection rather than silently
  inheriting exclusions.
- **Go client behaviour (authoritative):** `sdk/go/client.go:15-164` defines client
  ownership, options, raw GraphQL execution, query-builder access, and shutdown;
  `sdk/go/engineconn/engineconn.go:15-95` defines the connection abstraction,
  configuration, and connection-source precedence; `sdk/go/engineconn/session.go` and
  `sdk/go/engineconn/otel.go` define process lifecycle and trace propagation.
- **Pinned external Go client:** `core/sdk/go_sdk.go:27,562` passes external commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5` as the Go SDK library version. The
  immutable source is
  [`github.com/dagger/dagger-go-sdk@1309520`](https://github.com/dagger/dagger-go-sdk/commit/1309520660f6a5b35ef97b4fbe151e32a06a8dc5).
  On 2026-08-05, its `v0.21.7` tag resolved to
  `5a8ad2cd8d2470c06517eddbf1a243c7c1798dc4`, demonstrating why the
  Target_Descriptor must verify labels rather than trust comments.
- **Go engine SDK behaviour (authoritative):** `core/sdk.go:14-428` defines engine SDK
  capability interfaces; `core/sdk/go_sdk.go:62-371` supplies Go runtime, codegen,
  client generation, type-discovery strategy, and self-call behaviour.
- **Go generator behaviour (authoritative):**
  `cmd/codegen/generator/generator.go:17-36` defines module, client, library, and
  entrypoint operations; `cmd/codegen/generator/go/**` and its tests define the Go
  implementation and edge cases.
- **Go behaviour tests (authoritative where active):** `sdk/go/**/*_test.go`, relevant
  `cmd/codegen/generator/go/**/*_test.go`, relevant `core/integration/*module*test.go`,
  and relevant `internal/cmd/dagger/*{module,sdk}*test.go` exercise client, generator,
  runtime, module, and SDK selection behaviour. `future/sdk-tests.md` records removed
  tests and their recovery commit; those rows are inventory evidence, not passing
  verification at the Target_Revision.
- **Official SDK contract harness (definitive within its declared boundary):**
  [`dagger/sdk-sdk@8c16442`](https://github.com/dagger/sdk-sdk/commit/8c164424b7a8a37b33a77367ef7547490d5b87b5)
  introduces the black-box SDK test harness. Its
  [`README.md`](https://github.com/dagger/sdk-sdk/blob/8c164424b7a8a37b33a77367ef7547490d5b87b5/README.md)
  defines the public `dagger -m github.com/dagger/sdk-sdk -W <sdk-repo> check`
  contract, lifecycle scope, reusable target, and optional `initClient` exclusion.
  Its
  [`sdk-sdk.dang`](https://github.com/dagger/sdk-sdk/blob/8c164424b7a8a37b33a77367ef7547490d5b87b5/sdk-sdk.dang)
  defines the individual install, initialization, generation, API-loading,
  module-option, dependency, working-directory, changeset, and `@generate` checks.
  Its `initModuleRendersRootType` check exercises `sdk-sdk`'s own scaffolder rather
  than the subject SDK and therefore supplies harness-integrity evidence only.
- **Harness execution boundary:** At the pinned revision, `sdk-sdk.dang` defaults to
  Dagger CLI `1.0.0-beta.9`, downloads a release CLI, runs on `linux/amd64`, and does
  not exercise optional `initClient`. These are recorded scope constraints, not
  evidence for client/session/transport, deeper module semantics, another engine
  target, or another platform.
- **Reusable black-box assertions:** The pinned repository's `SdkTarget` supports a
  language SDK target plus additional checks, while `mod-test/mod-test.dang` provides
  `dagger api call -j` success, failure, output, JSON, and list assertions. Feature 8
  can extend the generic profile without copying its runner.
- **Official Go SDK adoption (comparative evidence):**
  [`dagger/go-sdk@7dcaf4f`](https://github.com/dagger/go-sdk/commit/7dcaf4f65b97f54923d2ec0064f64a9dc4bcd14c)
  registers `github.com/dagger/sdk-sdk` in the Go SDK checks. This confirms intended
  harness use, but the Go repository's later revision is not substituted for the
  Target_Revision's Definitive_Go_SDK authority.
- **Current Rust evidence:** `sdk/rust/crates/dagger-sdk/src/client.rs:16-40` exposes
  closure-scoped connection; `sdk/rust/crates/dagger-sdk/src/core/config.rs:8-46`
  defines its configuration; `sdk/rust/crates/dagger-sdk/src/gen.rs` contains broad
  generated bindings; `sdk/rust/crates/dagger-sdk/tests/mod.rs` contains a limited
  integration set. None currently forms an exhaustive parity ledger.
- **Approved scope (this specification branch):**
  `.kiro/specs/rust-sdk-complete-implementation/requirements.md:219-260` requires an
  exhaustive versioned ledger, evidence for complete claims, drift detection, and an
  explicit compatibility policy. At the Target_Revision, `sdk/rust/AGENTS.md` defines
  the same source-of-truth order and Rust quality obligations.
- **Historical proposals:** PR #12229 is useful implementation evidence for later
  features, but an open proposal or PR discussion is not authoritative behaviour or
  Verification_Evidence for this contract.

## Contract Policy

### Target Descriptor

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `contract_format_version` | Required supported schema version for all contract artifacts | `FORMAT_UNSUPPORTED` | Controls parsing only |
| `dagger_repository` | Required canonical repository identity | `TARGET_REPOSITORY_INVALID` | Read-only authority selection |
| `dagger_revision` | Required full immutable commit SHA | `TARGET_REVISION_INVALID` | Selects all in-repository sources |
| `engine_version` | Required engine release identifier embedded for the target | `TARGET_VERSION_MISMATCH` | Compatibility metadata only |
| `schema_version` | Required `__schemaVersion` value from canonical capture | `SCHEMA_VERSION_MISMATCH` | Selects the engine schema view |
| `schema_digest` | Required digest of canonical Schema_Snapshot bytes | `SCHEMA_DIGEST_MISMATCH` | Detects wire-shape drift |
| `go_sdk_repository` | Required canonical external Go SDK repository identity | `GO_AUTHORITY_INVALID` | Read-only authority selection |
| `go_sdk_revision` | Required full immutable commit SHA actually selected by the engine | `GO_REVISION_MISMATCH` | Selects external client source |
| `go_sdk_version_label` | Optional human label accepted only when it resolves to `go_sdk_revision` | `GO_VERSION_LABEL_MISMATCH` | Display metadata only |
| `sdk_contract_repository` | Required canonical `dagger/sdk-sdk` repository identity | `SDK_CONTRACT_AUTHORITY_INVALID` | Read-only harness selection |
| `sdk_contract_revision` | Required full immutable commit SHA of the selected SDK_Contract_Harness | `SDK_CONTRACT_REVISION_MISMATCH` | Selects check identities and runner behaviour |
| `sdk_contract_cli_version` | Required exact Dagger CLI and engine version used for harness execution | `SDK_CONTRACT_TARGET_MISMATCH` | Binds results to the assessed target |
| `rust_sdk_version` | Required Rust workspace package version | `RUST_TARGET_MISMATCH` | Baseline reporting only |
| `rust_edition` | Required Rust workspace edition | `RUST_TARGET_MISMATCH` | Baseline compatibility metadata |
| `rust_version` | Required declared Rust MSRV/toolchain contract | `RUST_TARGET_MISMATCH` | Baseline compatibility metadata |
| `previous_target` | Optional immutable Target_Descriptor identity for a Target_Transition | `TRANSITION_BASE_INVALID` | Enables semantic transition reporting |

### Authority Source

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `authority_id` | Required globally unique stable identifier | `AUTHORITY_DUPLICATE` | Keys capability provenance |
| `authority_class` | Required value from the Authority Class Policy | `AUTHORITY_CLASS_INVALID` | Selects extraction rules |
| `repository` | Required registered repository identity | `AUTHORITY_REPOSITORY_INVALID` | Resolves source anchors |
| `revision` | Required immutable commit matching the corresponding source identity in the Target_Descriptor | `AUTHORITY_REVISION_MISMATCH` | Pins behaviour and shape |
| `include` | Required non-empty set of paths or symbols | `AUTHORITY_SOURCE_EMPTY` | Defines the Source_Set |
| `exclude` | Explicit set of excluded paths or symbols with a rationale per entry | `AUTHORITY_EXCLUSION_INVALID` | Removes only reviewed non-contract material |
| `extractor` | Required deterministic extraction method and version | `AUTHORITY_EXTRACTOR_INVALID` | Produces inventory records |
| `source_digest` | Required digest of normalized included source identity | `AUTHORITY_DRIFT` | Detects unreviewed source movement |

### Authority Class Policy

| Authority class | Included contract | Explicit boundary |
|---|---|---|
| `engine-schema` | Canonical public Core_Schema introspection and schema version | Raw GraphQL introspection meta-types are excluded only by recorded policy |
| `go-client` | Handwritten exported client, query, connection, provisioning, error, and telemetry behaviour plus active tests | Generated bindings are represented by `engine-schema`, not double-counted as behavioural authority |
| `go-engine-sdk` | Engine SDK interfaces and the Go runtime/codegen/client-generation implementation | Other language SDK implementations are comparative evidence only |
| `go-codegen` | Common generator operations plus Go generation, type mapping, dispatch, and fixtures | Formatting-only implementation details are not separate behaviours |
| `go-integration-tests` | Active Go-relevant engine and CLI behaviour tests plus the explicit removed-test handoff inventory | Skipped or removed tests cannot serve as passing Verification_Evidence |
| `sdk-contract-harness` | Definitive executable common SDK-module conformance contract: pinned lifecycle Harness_Check identities plus the public `check`, `SdkTarget`, and `mod-test` contracts | Does not currently exercise `initClient`, typed-client/session/transport behaviour, deep cross-SDK module semantics, or non-`linux/amd64` platforms; green results prove only mapped assertions |
| `rust-policy` | Approved umbrella requirements and repository Rust contributor obligations | Policy cannot override engine wire shape or verified Go behaviour |

### Harness Check Mapping

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `check_id` | Required globally unique stable Harness_Check identity | `HARNESS_CHECK_DUPLICATE` | Keys check history and drift |
| `check_kind` | Required `subject-conformance` or `harness-self` classification derived from what the check executes | `HARNESS_CHECK_KIND_INVALID` | Selects Rust evidence eligibility |
| `harness_revision` | Required immutable revision equal to `sdk_contract_revision` | `HARNESS_REVISION_MISMATCH` | Pins check semantics |
| `source_locator` | Required exact check symbol at the pinned revision | `HARNESS_CHECK_MISSING` | Makes the imported check reviewable |
| `capability_ids` | Required non-empty exact ordered set for `subject-conformance`; required empty set for `harness-self` | `HARNESS_CAPABILITY_MISSING` | Bounds what a green result can prove |
| `execution_target` | Required Target_Descriptor and CLI identity used by the invocation | `HARNESS_TARGET_MISMATCH` | Prevents evidence crossing targets |
| `platform_scope` | Required exact runner operating system and architecture set | `HARNESS_PLATFORM_INVALID` | Bounds platform evidence |
| `invocation` | Required reproducible public harness command | `HARNESS_INVOCATION_INVALID` | Executes without mutating ledger state |
| `expected_outcome` | Required assertion or pass condition | `HARNESS_OUTCOME_MISSING` | Defines check success |
| `verification_evidence` | Optional until executed; when present, required valid result for the declared target and platform | `HARNESS_EVIDENCE_INVALID` | Supports only mapped complete claims |
| `limitations` | Required explicit behaviours and platforms not proved by the check | `HARNESS_SCOPE_INVALID` | Prevents scope inflation |

### Schema Capability Kinds

| Kind | Semantic signature must account for | Invalid state |
|---|---|---|
| `schema-root` | Query, mutation, or subscription role and referenced type | Missing or unresolved root type |
| `schema-type` | Kind, name, description, applied directives, interfaces, and possible types | Unknown kind or unresolved relationship |
| `schema-field` | Parent, name, description, return TypeRef, deprecation, and applied directives | Missing parent or unresolved TypeRef |
| `schema-argument` | Parent field, name, description, TypeRef, default, deprecation, and applied directives | Missing field or unresolved TypeRef |
| `schema-input-field` | Parent input, name, description, TypeRef, default, deprecation, and applied directives | Missing input type or unresolved TypeRef |
| `schema-enum-value` | Parent enum, name, description, deprecation, and applied directives | Missing enum parent |
| `schema-directive` | Name, description, valid locations, and repeatability when exposed | Unknown location or duplicate definition |
| `schema-directive-argument` | Parent directive, name, description, TypeRef, default, and deprecation | Missing directive or unresolved TypeRef |

Every TypeRef signature includes the complete nested `NON_NULL` and `LIST` structure.
Descriptions participate in documentation drift; defaults, deprecations, directives,
type relationships, and TypeRefs participate in semantic compatibility drift.

### Capability Record

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `capability_id` | Required globally unique stable identity | `CAPABILITY_DUPLICATE` | Primary traceability key |
| `authority_id` | Required reference to one Authority_Source | `CAPABILITY_AUTHORITY_MISSING` | Establishes provenance |
| `capability_kind` | Required registered schema, behavioural, or policy kind | `CAPABILITY_KIND_INVALID` | Selects validation policy |
| `source_anchors` | Required non-empty pinned authority evidence | `CAPABILITY_SOURCE_MISSING` | Grounds the target behaviour |
| `summary` | Required concise human-readable capability statement | `CAPABILITY_SUMMARY_MISSING` | Reporting only |
| `semantic_signature` | Required normalized authoritative shape or behaviour | `CAPABILITY_SIGNATURE_INVALID` | Feeds Capability_Fingerprint |
| `capability_fingerprint` | Required digest of the Semantic_Signature | `CAPABILITY_FINGERPRINT_MISMATCH` | Detects semantic drift |
| `status` | Required value from the Status Policy | `CAPABILITY_STATUS_INVALID` | Determines Completeness_Verdict |
| `stability` | Required `stable`, `experimental`, `internal`, or `not-applicable` classification for Rust public API impact | `CAPABILITY_STABILITY_INVALID` | Feeds transition SemVer policy |
| `gap` | Required exact missing behaviour for `Missing` or `Partial`; absent otherwise | `CAPABILITY_GAP_INVALID` | Guides owning child spec |
| `owner_feature` | Required Feature 2–9 owner for `Missing` or `Partial`; optional otherwise | `CAPABILITY_OWNER_MISSING` | Routes implementation work |
| `implementation_evidence` | Required for `Partial`, `Implemented`, and `Idiomatic_Equivalent` | `IMPLEMENTATION_EVIDENCE_MISSING` | Links Rust source only |
| `verification_evidence` | Required for `Implemented` and `Idiomatic_Equivalent` | `VERIFICATION_EVIDENCE_MISSING` | Supports complete claims |
| `decision_evidence` | Required for `Idiomatic_Equivalent`, `Inapplicable`, and experimental public APIs; optional for other reviewed compatibility decisions | `DECISION_EVIDENCE_INVALID` | Records reviewed exceptions and stability conditions |

### Status Policy

| Status | Entry condition | Required evidence | Completeness effect |
|---|---|---|---|
| `Missing` | No Rust implementation of the atomic capability | Authority anchors, exact gap, and owner feature | Blocks |
| `Partial` | Some Rust implementation exists but one or more observable behaviours are absent or unverified | Authority anchors, implementation anchors, exact gap, and owner feature | Blocks |
| `Implemented` | Rust provides behaviourally equivalent support | Authority, implementation, and executable verification evidence | Complete |
| `Idiomatic_Equivalent` | Rust provides equivalent behaviour through a materially different Rust-native shape | `Implemented` evidence plus a reviewed equivalence decision | Complete |
| `Inapplicable` | The Go-specific capability has no meaningful Rust counterpart | Authority anchors plus a reviewed inapplicability decision | Complete exception |

`Deferred`, `Unknown`, `Planned`, and `Unsupported` are not valid statuses. Planned
work remains truthfully `Missing` or `Partial` until its evidence changes.

### Classification Rule

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `rule_id` | Required globally unique stable identifier | `CLASSIFICATION_RULE_DUPLICATE` | Keys rule review history |
| `authority_id` | Required registered authority boundary | `CAPABILITY_AUTHORITY_MISSING` | Limits selector scope |
| `selector` | Required deterministic selector over canonical capability attributes | `CLASSIFICATION_SELECTOR_INVALID` | Selects existing inventory only |
| `expected_capability_ids` | Required exact ordered expansion or its deterministic digest | `LEDGER_DRIFT` | Prevents automatic classification of new matches |
| `classification` | Required Capability Record values shared by the expected expansion | `CAPABILITY_STATUS_INVALID` | Supplies resolved row data |
| `overrides` | Explicit per-Capability_ID differences from the shared classification | `CLASSIFICATION_OVERRIDE_INVALID` | Preserves atomic row truth |

### Target Transition

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `from_target` | Required validated prior Target_Descriptor | `TRANSITION_BASE_INVALID` | Establishes comparison baseline |
| `to_target` | Required validated successor Target_Descriptor | `TARGET_REVISION_INVALID` | Establishes proposed authority set |
| `added_capabilities` | Required complete ordered added Capability_ID set | `TRANSITION_DIFF_INVALID` | Requires new classifications |
| `removed_capabilities` | Required complete ordered removed Capability_ID set with prior records | `TRANSITION_DIFF_INVALID` | Preserves historical audit data |
| `changed_capabilities` | Required complete ordered set of prior and successor fingerprints | `TRANSITION_DIFF_INVALID` | Requires evidence revalidation |
| `authority_changes` | Required complete ordered authority-source diff | `TRANSITION_DIFF_INVALID` | Exposes source-boundary movement |
| `semver_effect` | Required `none`, `additive`, `deprecation`, or `breaking` classification | `TRANSITION_SEMVER_INVALID` | Feeds release planning |
| `migration_requirements` | Required references for every user-facing breaking change; empty otherwise | `TRANSITION_MIGRATION_MISSING` | Routes Feature 9 work |

### Compatibility Claim

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `rust_sdk_version` | Required released or candidate Rust SDK version | `RUST_TARGET_MISMATCH` | Identifies the compatibility subject |
| `supported_targets` | Required exact Target_Descriptors or one bounded range | `COMPATIBILITY_TARGET_INVALID` | Defines the public compatibility claim |
| `range_boundaries` | Required lower and upper Target_Descriptors when a range is used | `COMPATIBILITY_RANGE_INVALID` | Defines tested boundary semantics |
| `conformance_evidence` | Required passing boundary evidence for every claimed target or range boundary | `COMPATIBILITY_EVIDENCE_MISSING` | Supports the published claim |
| `outside_range_capability` | Required Capability_ID for the typed unsupported-target response | `COMPATIBILITY_RESPONSE_MISSING` | Routes Features 2 or 3 implementation |
| `claim_digest` | Required digest of normalized compatibility inputs | `COMPATIBILITY_DRIFT` | Prevents separately edited release metadata |

### Evidence Reference

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `evidence_kind` | Required `authority`, `implementation`, `verification`, or `decision` | `EVIDENCE_KIND_INVALID` | Selects conditional fields |
| `repository` | Required registered repository identity | `EVIDENCE_REPOSITORY_INVALID` | Resolves the anchor |
| `revision` | Required immutable revision matching its Authority_Source | `EVIDENCE_REVISION_MISMATCH` | Prevents moving evidence |
| `path` | Required repository-relative path | `EVIDENCE_PATH_INVALID` | Prevents machine-local citations |
| `locator` | Required stable symbol, test ID, schema coordinate, or line anchor | `EVIDENCE_LOCATOR_INVALID` | Makes review and validation precise |
| `claim` | Required statement of exactly what the reference proves | `EVIDENCE_CLAIM_MISSING` | Prevents evidence-by-proximity |
| `command` | Required reproducible invocation for verification evidence only | `EVIDENCE_COMMAND_INVALID` | Executes without mutating ledger state |
| `expected_outcome` | Required assertion or pass condition for verification evidence only | `EVIDENCE_OUTCOME_MISSING` | Defines executable success |
| `execution_target` | Required exact Target_Descriptor for verification evidence that exercises an engine or CLI | `EVIDENCE_TARGET_MISMATCH` | Prevents cross-target reuse |
| `platform_scope` | Required exact operating system and architecture set for platform-sensitive verification evidence | `EVIDENCE_PLATFORM_INVALID` | Prevents cross-platform inference |

### Completeness Report

| Field | Target policy | Error if invalid | Persistence / side-effect impact |
|---|---|---|---|
| `contract_format_version` | Echo the validated contract format | `REPORT_TARGET_MISMATCH` | Machine consumer compatibility |
| `target_descriptor` | Echo the complete validated target identity | `REPORT_TARGET_MISMATCH` | Audit metadata |
| `inventory_digest` | Digest the ordered Canonical_Inventory | `REPORT_DIGEST_MISMATCH` | Reproducibility check |
| `ledger_digest` | Digest the ordered Resolved_Ledger | `REPORT_DIGEST_MISMATCH` | Reproducibility check |
| `integrity_verdict` | True only when all structural and drift checks pass | `REPORT_VERDICT_INVALID` | Drives integrity-gate exit status |
| `completeness_verdict` | True only when integrity passes and no Blocking_Status exists | `REPORT_VERDICT_INVALID` | Drives release-gate exit status |
| `counts_by_authority` | Exact totals for every Authority_Source | `REPORT_COUNT_MISMATCH` | Human and machine summary |
| `counts_by_kind` | Exact totals for every capability kind | `REPORT_COUNT_MISMATCH` | Human and machine summary |
| `counts_by_status` | Exact totals for all five statuses including zeroes | `REPORT_COUNT_MISMATCH` | Human and machine summary |
| `counts_by_owner` | Exact Blocking_Status totals for Features 2–9 | `REPORT_COUNT_MISMATCH` | Workstream planning summary |
| `integrity_errors` | Deterministically ordered complete list of validation failures | `REPORT_ERROR_SET_MISMATCH` | Actionable integrity diagnosis |
| `blocking_capabilities` | Deterministically ordered complete list of `Missing` and `Partial` Capability_IDs | `REPORT_BLOCKER_SET_MISMATCH` | Actionable completeness diagnosis |
| `complete_exceptions` | Deterministically ordered complete list of `Idiomatic_Equivalent` and `Inapplicable` decisions | `REPORT_EXCEPTION_SET_MISMATCH` | Prevents hidden exception inflation |

## Requirements

### Requirement 1: Immutable Target Identity

**User Story:** As a Rust SDK maintainer, I want every completeness assessment pinned
to exact source identities, so that later source movement cannot change what a green
report meant.

#### Acceptance Criteria

1. WHEN a Target_Descriptor is created, THE contract tooling SHALL populate every
   required Target Descriptor field from authoritative source.
2. IF a repository revision is not a full immutable commit SHA, THEN THE contract
   tooling SHALL return `TARGET_REVISION_INVALID`.
3. IF `go_sdk_revision` differs from the commit selected by the target engine, THEN THE
   contract tooling SHALL return `GO_REVISION_MISMATCH`.
4. WHERE `go_sdk_version_label` is present, THE contract tooling SHALL verify that the
   label resolves to `go_sdk_revision`.
5. IF a Go SDK version label resolves to another commit, THEN THE contract tooling
   SHALL return `GO_VERSION_LABEL_MISMATCH`.
6. WHEN the Schema_Snapshot is captured, THE contract tooling SHALL record its
   `__schemaVersion` value.
7. WHEN the Schema_Snapshot is canonicalized, THE contract tooling SHALL record its
   content digest.
8. WHEN normal verification runs, THE contract tooling SHALL use checked-in inputs and
   immutable local source identities without requiring a moving remote reference.
9. WHEN a Target_Descriptor is created, THE contract tooling SHALL record the exact
   SDK_Contract_Harness repository and immutable revision.
10. WHEN harness Verification_Evidence is produced, THE evidence record SHALL identify
    the exact Dagger CLI and engine target used by the harness.
11. IF harness execution uses a CLI or engine other than `sdk_contract_cli_version`,
    THEN THE contract tooling SHALL return `SDK_CONTRACT_TARGET_MISMATCH`.

### Requirement 2: Exhaustive Authority Registry

**User Story:** As a reviewer, I want the contract to declare every source it treats as
authoritative, so that parity cannot be expanded or narrowed through an invisible file
selection.

#### Acceptance Criteria

1. THE Authority_Registry SHALL contain one valid source for every Authority Class
   Policy row.
2. WHEN an Authority_Source is registered, THE Authority_Registry SHALL record every
   Authority Source field.
3. WHEN an included path contains generated bindings already represented by the engine
   schema, THE Authority_Registry SHALL exclude it from behavioural extraction with a
   recorded rationale.
4. WHEN an authority exclusion is declared, THE Authority_Registry SHALL identify the
   exact path or symbol excluded.
5. IF an included source path matches no file at the pinned revision, THEN THE contract
   tooling SHALL return `AUTHORITY_SOURCE_EMPTY`.
6. IF an included authority file changes without a Target_Transition, THEN THE contract
   tooling SHALL return `AUTHORITY_DRIFT`.
7. WHEN active Go behaviour tests are registered, THE Authority_Registry SHALL preserve
   their stable test and subtest identities.
8. WHEN `future/sdk-tests.md` references removed Go tests, THE Authority_Registry SHALL
   preserve each handoff row and its recovery commit as non-passing inventory evidence.
9. IF a skipped or removed test is offered as Verification_Evidence, THEN THE contract
   tooling SHALL return `EVIDENCE_OUTCOME_MISSING`.
10. WHEN the `sdk-contract-harness` authority is registered, THE Authority_Registry
    SHALL preserve every public Harness_Check identity and source locator selected at
    `sdk_contract_revision`.
11. IF the registered SDK_Contract_Harness revision differs from
    `sdk_contract_revision`, THEN THE contract tooling SHALL return
    `SDK_CONTRACT_REVISION_MISMATCH`.

### Requirement 3: Complete Schema Inventory

**User Story:** As a Rust code-generator maintainer, I want every public schema element
and semantic property inventoried, so that broad generated coverage cannot conceal one
missing field, argument, default, or type relationship.

#### Acceptance Criteria

1. WHEN canonical introspection runs, THE schema extractor SHALL request deprecated
   fields, arguments, input fields, and enum values.
2. THE Canonical_Inventory SHALL contain every public schema root.
3. THE Canonical_Inventory SHALL contain every public scalar, object, interface, union,
   enum, and input-object type.
4. THE Canonical_Inventory SHALL contain every public field on every object and
   interface.
5. THE Canonical_Inventory SHALL contain every argument on every public field.
6. THE Canonical_Inventory SHALL contain every field on every public input object.
7. THE Canonical_Inventory SHALL contain every value on every public enum.
8. THE Canonical_Inventory SHALL contain every public directive definition and
   directive argument.
9. WHEN a schema capability is normalized, THE Semantic_Signature SHALL preserve every
   property listed for its Schema Capability Kind.
10. WHEN a TypeRef is normalized, THE Semantic_Signature SHALL preserve its complete
    nested list and nullability shape.
11. WHEN raw introspection contains a non-public meta-type, THE schema extractor SHALL
    exclude it only through an explicit Authority_Registry policy.
12. IF a schema relationship points to an uninventoried type, THEN THE contract tooling
    SHALL return `CAPABILITY_SIGNATURE_INVALID`.
13. WHEN two equivalent schema responses differ only in input ordering, THE schema
    extractor SHALL produce identical Canonical_Inventory bytes.

### Requirement 4: Complete Behavioural and Policy Inventory

**User Story:** As a Rust SDK maintainer, I want non-schema Go behaviour and Rust
quality obligations inventoried alongside the schema, so that generated API parity is
not mistaken for SDK completeness.

#### Acceptance Criteria

1. THE Canonical_Inventory SHALL contain every handwritten exported Go client and
   connection capability selected by the `go-client` Authority_Source.
2. THE Canonical_Inventory SHALL contain every engine SDK capability selected by the
   `go-engine-sdk` Authority_Source.
3. THE Canonical_Inventory SHALL contain module, client, library, and entrypoint
   generation capabilities selected by the `go-codegen` Authority_Source.
4. THE Canonical_Inventory SHALL contain every active test identity selected by the
   `go-client`, `go-codegen`, and `go-integration-tests` Authority_Sources.
5. WHEN one Go test verifies multiple atomic behaviours, THE Completeness_Ledger SHALL
   map that test to every corresponding Capability_ID.
6. WHEN multiple Go tests verify one behaviour, THE Completeness_Ledger SHALL preserve
   every non-redundant authority anchor for that Capability_ID.
7. WHEN an exported Go declaration is deprecated, THE Canonical_Inventory SHALL retain
   its deprecation metadata for classification.
8. WHEN a Go mechanism is language-specific, THE Canonical_Inventory SHALL represent
   its observable behaviour before any inapplicability decision is considered.
9. THE Canonical_Inventory SHALL contain every Policy_Capability selected from the
   approved umbrella and `sdk/rust/AGENTS.md`.
10. IF two extracted behaviours have the same Capability_ID but different semantic
    signatures, THEN THE contract tooling SHALL return `CAPABILITY_DUPLICATE`.
11. WHEN a `subject-conformance` Harness_Check is imported, THE Canonical_Inventory
    SHALL map every atomic behaviour asserted by that check to its corresponding
    Capability_ID.
12. IF a harness omission has a capability in another authority source, THEN THE
    Canonical_Inventory SHALL retain that capability for explicit classification.

### Requirement 5: Stable Capability Identity and Rule Expansion

**User Story:** As a child-spec author, I want stable atomic Capability_IDs, so that
requirements, implementation, tests, and release evidence keep traceability across
target updates.

#### Acceptance Criteria

1. WHEN a capability is first inventoried, THE extractor SHALL assign a deterministic
   Capability_ID from its authority class and semantic coordinate.
2. WHEN incidental source line numbers change, THE extractor SHALL preserve the
   Capability_ID.
3. WHEN a capability's semantic shape changes without changing identity, THE extractor
   SHALL change its Capability_Fingerprint.
4. WHEN a Classification_Rule is used, THE Resolved_Ledger SHALL expose every expanded
   Capability_ID as an individual record.
5. WHEN a Classification_Rule is committed, THE rule SHALL record the expected digest
   of its expanded Capability_ID set.
6. IF a Classification_Rule begins matching an additional capability, THEN THE
   contract tooling SHALL return `LEDGER_DRIFT`.
7. IF a Classification_Rule ceases matching an expected capability, THEN THE contract
   tooling SHALL return `LEDGER_DRIFT`.
8. IF two rules classify the same Capability_ID, THEN THE contract tooling SHALL return
   `CAPABILITY_DUPLICATE`.
9. IF an Active_Capability is matched by no ledger row or rule, THEN THE contract
   tooling SHALL return `CAPABILITY_STATUS_INVALID`.

### Requirement 6: Truthful Status and Evidence

**User Story:** As a release reviewer, I want strict status-entry rules, so that source
presence, compilation, or an optimistic design cannot be reported as completed
behaviour.

#### Acceptance Criteria

1. THE Resolved_Ledger SHALL contain exactly one Status Policy value for every
   Active_Capability.
2. WHEN a capability has no Rust implementation, THE Resolved_Ledger SHALL classify it
   as `Missing`.
3. WHEN a capability has incomplete or insufficiently verified Rust behaviour, THE
   Resolved_Ledger SHALL classify it as `Partial`.
4. WHEN a capability is classified as `Missing`, THE ledger row SHALL identify its
   exact gap and owning feature.
5. WHEN a capability is classified as `Partial`, THE ledger row SHALL identify its
   exact residual gap and owning feature.
6. WHEN a capability is classified as `Implemented`, THE ledger row SHALL contain valid
   implementation and Verification_Evidence.
7. WHEN a capability is classified as `Idiomatic_Equivalent`, THE ledger row SHALL
   contain a reviewed decision describing the Rust mapping.
8. WHEN a capability is classified as `Inapplicable`, THE ledger row SHALL contain a
   reviewed decision proving that no meaningful Rust counterpart exists.
9. IF an `Inapplicable` rationale states only that Rust has not implemented the
   capability, THEN THE contract tooling SHALL return `DECISION_EVIDENCE_INVALID`.
10. IF a complete status relies only on documentation, source comments, an issue, or a
    pull request, THEN THE contract tooling SHALL return
    `VERIFICATION_EVIDENCE_MISSING`.
11. WHEN a verification reference is evaluated, THE referenced assertion SHALL test
    the behaviour stated by the capability's Semantic_Signature.
12. WHEN one verification property proves a class of schema capabilities, THE evidence
    record SHALL identify the exact resolved Capability_ID set covered by the property.
13. WHEN a Harness_Check passes, THE evidence audit SHALL limit its claim to the
    Capability_IDs and assertions in its Harness_Check_Mapping.
14. IF a harness result is offered as evidence for an omitted behaviour or platform,
    THEN THE evidence audit SHALL return `HARNESS_SCOPE_INVALID`.

### Requirement 7: Pinned and Reproducible Evidence

**User Story:** As a contributor, I want evidence references to be precise and
executable, so that I can reproduce a completeness claim without relying on the
original author's workstation or memory.

#### Acceptance Criteria

1. WHEN an Evidence_Reference is recorded, THE Completeness_Ledger SHALL populate every
   field required for its evidence kind.
2. IF an evidence path is absolute or machine-local, THEN THE contract tooling SHALL
   return `EVIDENCE_PATH_INVALID`.
3. IF an evidence revision is a moving branch or unresolved tag, THEN THE contract
   tooling SHALL return `EVIDENCE_REVISION_MISMATCH`.
4. IF an evidence path or locator does not exist at its pinned revision, THEN THE
   contract tooling SHALL return `EVIDENCE_LOCATOR_INVALID`.
5. WHEN Verification_Evidence is executed from a clean checkout, THE evidence command
   SHALL produce its recorded expected outcome.
6. WHEN a verification command requires an engine, THE evidence record SHALL identify
   the compatible Target_Descriptor.
7. IF a verification command passes without exercising its claimed behaviour, THEN THE
   evidence audit SHALL return `EVIDENCE_OUTCOME_MISSING`.
8. WHEN multiple ledger rows share Verification_Evidence, THE evidence audit SHALL
   preserve row-level traceability in the Resolved_Ledger.
9. WHEN SDK_Contract_Harness evidence is executed, THE evidence command SHALL select
   `sdk_contract_cli_version` explicitly rather than rely on the harness default.
10. WHEN platform-sensitive evidence is recorded, THE evidence record SHALL preserve
    the exact operating system and architecture scope exercised.

### Requirement 8: Drift Detection and Target Transitions

**User Story:** As a maintainer upgrading Dagger, I want semantic drift to fail loudly
and target changes to produce an explicit migration record, so that capabilities
cannot appear, disappear, or change behind a refreshed generated file.

#### Acceptance Criteria

1. WHEN integrity verification runs, THE contract tooling SHALL derive a fresh
   Canonical_Inventory from the registered pinned sources.
2. WHEN integrity verification runs, THE contract tooling SHALL compare the fresh
   inventory with the checked-in capability identities and fingerprints.
3. IF an authority adds a capability outside a Target_Transition, THEN THE contract
   tooling SHALL return `LEDGER_DRIFT` with the added Capability_ID.
4. IF an authority removes a capability outside a Target_Transition, THEN THE contract
   tooling SHALL return `LEDGER_DRIFT` with the removed Capability_ID.
5. IF an authority changes a Semantic_Signature outside a Target_Transition, THEN THE
   contract tooling SHALL return `LEDGER_DRIFT` with the changed Capability_ID.
6. WHEN a Target_Transition is requested, THE transition tooling SHALL report every
   added, removed, and changed Capability_ID.
7. WHEN a Target_Transition removes a capability, THE transition record SHALL preserve
   its prior status and evidence as historical audit data.
8. WHEN a Target_Transition changes a capability fingerprint, THE Completeness_Ledger
   SHALL require that capability's status evidence to be revalidated.
9. WHEN a Target_Transition adds a capability, THE Completeness_Ledger SHALL classify it
   explicitly before integrity can pass.
10. WHEN identical pinned inputs are processed repeatedly, THE contract tooling SHALL
    produce byte-identical inventory, ledger, and report output.
11. WHEN a Target_Transition is reviewed, THE transition record SHALL classify its
    public Rust SemVer impact.
12. WHERE a Target_Transition has user-facing incompatibility, THE transition record
    SHALL reference a migration requirement owned by Feature 9.
13. IF the pinned harness adds, removes, or semantically changes a Harness_Check outside
    a Target_Transition, THEN THE contract tooling SHALL return `LEDGER_DRIFT` with the
    affected `check_id`.

### Requirement 9: Independent Integrity and Completeness Verdicts

**User Story:** As a maintainer, I want structural correctness separated from feature
completion, so that the ledger can govern an incomplete SDK without either blocking
all development or producing a false green release signal.

#### Acceptance Criteria

1. WHEN validation completes, THE contract tooling SHALL emit every Completeness Report
   field in machine-readable form.
2. WHEN validation completes, THE contract tooling SHALL emit a human-readable report
   with the same verdicts and counts.
3. WHEN all target, authority, inventory, ledger, evidence-shape, and drift checks pass,
   THE report SHALL set `integrity_verdict` to true.
4. IF any integrity check fails, THEN THE report SHALL set `integrity_verdict` to false.
5. IF `integrity_verdict` is false, THEN THE report SHALL set
   `completeness_verdict` to false.
6. IF any Active_Capability is `Missing` or `Partial`, THEN THE report SHALL set
   `completeness_verdict` to false.
7. WHEN integrity is true and every Active_Capability has a Complete_Status, THE report
   SHALL set `completeness_verdict` to true.
8. WHEN the integrity gate is selected, THE contract command SHALL return success only
   for a true Integrity_Verdict.
9. WHEN the completeness gate is selected, THE contract command SHALL return success
   only for a true Completeness_Verdict.
10. WHEN the initial F1 baseline enters normal CI, THE CI check SHALL enforce the
    Integrity_Verdict without requiring the Completeness_Verdict.
11. WHEN Feature 9 evaluates stable release readiness, THE release gate SHALL enforce
    the Completeness_Verdict.

### Requirement 10: Initial Baseline and Downstream Traceability

**User Story:** As the Rust SDK programme owner, I want the initial ledger populated
and routed into the remaining feature specs, so that F1 immediately becomes the work
plan rather than empty infrastructure.

#### Acceptance Criteria

1. WHEN F1 is complete, THE Resolved_Ledger SHALL classify every capability in the
   initial Canonical_Inventory.
2. WHEN the initial ledger classifies existing code without sufficient behavioural
   evidence, THE Resolved_Ledger SHALL use `Partial` rather than `Implemented`.
3. WHEN the initial ledger identifies absent behaviour, THE Resolved_Ledger SHALL use
   `Missing` rather than a planning-only status.
4. WHEN a `Missing` or `Partial` row belongs to client lifecycle, THE ledger row SHALL
   assign Feature 2.
5. WHEN a `Missing` or `Partial` row belongs to transport, observability, provisioning,
   or reliability, THE ledger row SHALL assign Feature 3.
6. WHEN a `Missing` or `Partial` row belongs to core schema generation, THE ledger row
   SHALL assign Feature 4.
7. WHEN a `Missing` or `Partial` row belongs to engine SDK resolution, runtime, or
   generator integration, THE ledger row SHALL assign Feature 5.
8. WHEN a `Missing` or `Partial` row belongs to module authoring, type discovery, or
   dispatch, THE ledger row SHALL assign Feature 6.
9. WHEN a `Missing` or `Partial` row belongs to standalone or dependency client
   generation, THE ledger row SHALL assign Feature 7.
10. WHEN a `Missing` or `Partial` row belongs to conformance, platform, or security
    gates, THE ledger row SHALL assign Feature 8.
11. WHEN a `Missing` or `Partial` row belongs to packaging, release, compatibility
    publication, or documentation, THE ledger row SHALL assign Feature 9.
12. WHEN a later child specification is authored, THE child requirements SHALL cite
    every Capability_ID whose status they intend to change.
13. WHEN a later implementation changes a capability status, THE same change SHALL
    update its implementation and verification evidence.
14. WHEN the initial baseline report is produced, THE report SHALL expose a true
    Integrity_Verdict before F1 is considered complete.
15. WHEN the pinned harness omits `initClient`, THE ledger SHALL retain standalone and
    dependency client-generation gaps under Feature 7.
16. WHEN the pinned harness supplies only `linux/amd64` execution evidence, THE ledger
    SHALL retain unverified platform obligations under Feature 8.

### Requirement 11: Compatibility and Stability Contract

**User Story:** As a Rust SDK consumer, I want compatibility claims tied to tested
targets and explicit stability decisions, so that an SDK release never implies a wider
engine range than its evidence supports.

#### Acceptance Criteria

1. WHEN a Rust SDK release declares engine compatibility, THE compatibility policy
   SHALL identify every supported Target_Descriptor or bounded target range.
2. WHEN a bounded target range is declared, THE compatibility policy SHALL link passing
   conformance evidence for each claimed compatibility boundary.
3. IF a target falls outside the declared compatibility policy, THEN THE ledger SHALL
   classify the required typed client response as an owned capability for Features 2
   or 3.
4. WHEN a public Rust API capability is added, THE Completeness_Ledger SHALL classify
   its stability as stable, experimental, or internal.
5. WHEN a stable public Rust API changes incompatibly, THE Target_Transition SHALL
   classify the change as breaking.
6. WHERE a public Rust API remains experimental, THE decision evidence SHALL state its
   graduation or removal condition.
7. IF compatibility is inferred only from matching version labels, THEN THE contract
   tooling SHALL return `TARGET_VERSION_MISMATCH`.
8. WHEN release metadata is generated, THE release process SHALL derive its
   compatibility claim from validated contract data rather than a separately authored
   range.

### Requirement 12: Official SDK Contract Harness Integration

**User Story:** As a Rust SDK maintainer, I want to reuse Dagger's official black-box
SDK contract harness within explicit evidential boundaries, so that Rust gains shared
lifecycle conformance without mistaking the common harness for complete Go parity.

#### Acceptance Criteria

1. THE initial Target_Descriptor SHALL pin `dagger/sdk-sdk` revision
   `8c164424b7a8a37b33a77367ef7547490d5b87b5`.
2. WHEN the SDK_Contract_Harness is imported, THE contract tooling SHALL create one
   Harness_Check_Mapping for every public Harness_Check at the pinned revision.
3. WHEN a Harness_Check_Mapping is created, THE contract tooling SHALL populate every
   Harness Check Mapping field.
4. WHEN the generic Rust SDK lifecycle profile executes, THE conformance runner SHALL
   invoke the harness's public `check` interface against the target Rust SDK workspace.
5. WHEN the generic profile invokes the harness, THE conformance runner SHALL use the
   Target_Descriptor's exact Dagger CLI and engine identity.
6. WHEN a mapped `subject-conformance` Harness_Check passes, THE Completeness_Ledger
   SHALL accept the result only for its declared Capability_ID, target, platform, and
   assertion scope.
7. WHEN expected Rust SDK incompleteness causes a mapped Harness_Check to fail, THE
   Resolved_Ledger SHALL retain `Missing` or `Partial` for the affected capabilities
   without treating expected incompleteness alone as an Integrity_Verdict failure.
8. BECAUSE the pinned harness does not exercise `initClient`, THE contract tooling
   SHALL reject any inference that a green generic profile proves typed-client
   generation completeness.
9. BECAUSE the pinned harness runner is `linux/amd64`, THE contract tooling SHALL
   reject any inference that a green generic profile proves another platform.
10. WHERE Feature 8 adds Rust-specific black-box scenarios, THE Rust conformance
    profile SHALL reuse the harness's public `SdkTarget` or `mod-test` contracts as the
    integration boundary.
11. WHEN a cross-SDK scenario is ported from the active integration corpus or
    `future/sdk-tests.md`, THE Rust conformance profile SHALL preserve the observable
    behaviour and Capability_ID mapping without requiring Go-specific CLI syntax.
12. WHEN a Target_Transition changes `sdk_contract_revision`, THE transition tooling
    SHALL report every added, removed, and semantically changed Harness_Check.
13. WHEN a target-compatible Harness_Check and Go source cover the same common
    SDK-module lifecycle behaviour, THE Completeness_Ledger SHALL treat the
    Harness_Check assertion as conformance authority and the Go source as reference
    evidence.
14. IF the pinned harness contract is incompatible with the Target_Descriptor's engine
    behaviour, THEN THE contract tooling SHALL return
    `SDK_CONTRACT_TARGET_MISMATCH` rather than select one source silently.
15. WHEN a Harness_Check exercises `sdk-sdk` itself rather than the subject SDK, THE
    Harness_Check_Mapping SHALL classify it as `harness-self` with no Capability_IDs.
16. IF a `harness-self` result is offered as Rust capability Verification_Evidence,
    THEN THE evidence audit SHALL return `HARNESS_SCOPE_INVALID`.

## Iteration and Feedback Notes

- Requirements approval is the consent gate before `design.md` is authored.
- The design must choose repository locations and serialization formats without
  weakening the conceptual field policies above.
- The design must derive property-based tests for canonicalization, stable identity,
  rule expansion, status-entry validation, evidence validation, harness inventory and
  scope mapping, semantic drift, report aggregation, and target transitions.
- The initial target refresh must preserve the exact external Go SDK commit even while
  the misleading `v0.21.7` source comment remains; correcting that comment belongs in a
  separate coherent change unless the F1 implementation needs to make the pin
  machine-readable.
