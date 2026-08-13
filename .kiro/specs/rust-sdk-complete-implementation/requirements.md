# Requirements Document: Rust SDK Complete Implementation

## Introduction

This umbrella specification defines the work required to elevate the Dagger Rust SDK
from an experimental generated client to a stable, engine-integrated SDK with the
observable capabilities of the definitive Go SDK. The target is **behavioural
completeness with an idiomatic Rust API**, not a transliteration of Go types, naming,
or ownership patterns.

The baseline audit is pinned to Dagger repository commit
`25300124ca110612edc09c43f89cb5fad6028170` (`v1.0.0-beta.9-52-g25300124c`). At that
revision, the Rust workspace declares `1.0.0-beta.10`, while the embedded engine
version remains `1.0.0-beta.7`. The generated Rust GraphQL surface already includes
module-related engine objects such as `Module::serve`, `Query::current_function_call`,
and `Query::current_module`; the missing module capability is therefore principally
SDK registration, runtime, code generation, type discovery, dispatch, and user-facing
authoring support rather than absence of the underlying GraphQL schema.

The sources of truth are, in order:

1. the Dagger engine schema and protocol for the wire contract and available
   capabilities;
2. the Go SDK and its tests at the same repository revision for expected feature
   completeness and observable behaviour;
3. idiomatic Rust for ownership, lifetimes, naming, errors, concurrency, and public API
   shape; and
4. current Rust code and historical proposals as implementation evidence only.

Pull request [#12229](https://github.com/dagger/dagger/pull/12229) demonstrates one
possible module implementation using an engine SDK wrapper, procedural macros, and a
Rust dispatch runtime. It is open and unmerged at the baseline, so it informs the
child designs without defining their requirements.

The implementation is organized into nine features. Each feature will receive a child
specification with its own `requirements.md`, `design.md`, and `tasks.md` before code is
changed.

Two deliberately separate execution contracts govern that work:

**Checkpoint build/test — engine-free and Rust-first**

- scoped Cargo checks, properties, compile fixtures, and the direct Rust-owned Go ABI
  tests run without a Dagger engine or module invocation;
- checked generated assets are reused unless an owning input digest changed;
- no network-backed engine graph, unrelated SDK build, or unscoped generation enters
  the checkpoint; and
- commands, elapsed time, generated-asset decisions, and any proposed deferred engine
  exception are recorded as implementation-closure evidence.

**SDK sign-off — bounded and exact-target**

- one reusable exact-target artifact;
- engine, CLI, Go runtime, and Rust content built at most once;
- one engine service and one installed Rust baseline;
- no unrelated SDK builders, tests, generation, or distribution builds;
- reuse of engine-free closure evidence;
- isolated case fan-out;
- atomic digest-bound verdict and phase timings; and
- rejection of duplicate builds or engine starts.

The checkpoint contract proves the implementation without spending engine resources.
The sign-off contract later proves only the real-engine boundaries that the direct
Rust models deliberately cannot claim.

**Release handoff — signed-off bytes, Git-pinned SDK, and immutable assets**

- Feature 8 hands Feature 9 the exact artifact bytes, manifest, security report, and
  passing verdict that were admitted during sign-off; release automation does not
  rebuild or silently widen them;
- the fork distributes the Rust SDK from one immutable, namespaced Git tag whose
  commit is the exact Cargo `rev` recorded by downstream consumers;
- GitHub Release assets contain the separately signed-off platform payloads and their
  checksums, SBOMs, attestations, verification record, and humble capability report;
- the fork publishes neither Rust crates to crates.io nor engine images to GHCR; and
- a draft release becomes immutable only after an independent verifier has admitted
  every required asset and no release gate was skipped.

**Dependency graph:**

- Feature 1 (Completeness Contract) — no dependencies
- Feature 2 (Client Lifecycle and Configuration) — depends on Feature 1
- Feature 3 (Transport, Observability, and Reliability) — depends on Feature 1 and
  supports Feature 2
- Feature 4 (Core Schema Code Generation) — depends on Feature 1
- Feature 5 (Engine SDK Integration) — depends on Features 1 and 4
- Feature 6 (Rust Module Authoring and Dispatch) — depends on Feature 5
- Feature 7 (Standalone Client and Dependency Generation) — depends on Features 4 and
  5
- Feature 8 (Conformance, Platform, and Security Gates) — begins with Feature 1 and
  closes Features 2–7
- Feature 9 (Distribution, Documentation, and Stable Release) — depends on Features
  2–8

The child specifications are:

- `rust-sdk-completeness-contract` (Feature 1)
- `rust-sdk-client-lifecycle` (Feature 2)
- `rust-sdk-transport-observability` (Feature 3)
- `rust-sdk-core-codegen` (Feature 4)
- `rust-sdk-engine-integration` (Feature 5)
- `rust-sdk-module-authoring` (Feature 6)
- `rust-sdk-client-generation` (Feature 7)
- `rust-sdk-conformance-security` (Feature 8)
- `rust-sdk-release-readiness` (Feature 9)

## Glossary

- **Behavioural_Parity:** Equivalent externally observable capability and semantics,
  allowing a Rust-native public API.
- **Atomic_Signoff_Verdict:** The canonical Feature 8 pass or fail record binding one
  complete exact-target run, every admitted input and outcome, and all duplicate-work
  and security checks.
- **Completeness_Ledger:** An exhaustive, versioned mapping from engine and Go SDK
  capabilities to Rust implementation and verification evidence.
- **Core_Schema:** The GraphQL schema exposed by the Dagger engine independently of a
  user's modules.
- **Definitive_Go_SDK:** `sdk/go/**` and its tests at the target repository revision,
  used as the feature-completeness and behavioural authority.
- **Engine_Contract:** The engine SDK interfaces in `core/sdk.go`, including code
  generation, runtime construction, type discovery, initialization, and client
  generation.
- **Engine_Integrated_SDK:** An SDK that the Dagger engine can resolve by language
  name and use for development, code generation, execution, and client generation.
- **Existing_Rust_SDK_Baseline:** The Rust SDK present at the pinned baseline audit
  revision, used only for a factual and appreciative before/after capability account.
- **Exact_Target_Signoff_Artifact:** An immutable, content-addressed engine artifact
  for one Target_Revision, platform, engine/CLI input identity, engine-packaged Go SDK
  runtime identity, Rust SDK manifest and descriptor identity, and toolchain identity.
- **Existing_Session:** A Dagger engine session supplied through the standard session
  environment rather than started by the Rust process.
- **Generated_Bindings:** Rust source emitted from engine introspection for GraphQL
  objects, interfaces, enums, scalars, inputs, field arguments, and selections.
- **Go_Level:** Complete according to the Completeness_Ledger, not source-compatible
  with Go.
- **Idiomatic_Equivalence:** Rust API shape that preserves behaviour while following
  accepted Rust ownership, error, naming, async, and type-system conventions.
- **Implementation_Closure_Evidence:** Immutable evidence that the canonical
  engine-free Rust implementation, fixture, hygiene, and security gates passed for an
  exact source and generated-asset identity.
- **Installed_Rust_Baseline:** The immutable sign-off runner and workspace state
  produced by one canonical Rust SDK installation, from which isolated cases branch;
  a case that explicitly verifies installation semantics may perform further
  case-local installation operations without reconstructing this baseline.
- **Module_Dispatch:** Decoding an engine function call, invoking the matching Rust
  function against module state, and returning either its value or structured error.
- **Module_SDK:** The code generator and runtime implementation used by the engine to
  develop and execute Dagger modules written in Rust.
- **Owned_Client:** A client handle whose session lifetime can outlive a single
  closure and whose shutdown semantics are explicit and deterministic.
- **Release_Gate:** A check that must pass before the Rust SDK can be presented as
  stable and Go-level complete.
- **Release_Handoff_Record:** The Feature 8 record binding a passing verdict to the
  exact artifact manifest, payload bytes, security evidence, subject revision, and one
  platform identity which Feature 9 may distribute without rebuilding.
- **Release_Manifest:** The canonical Feature 9 inventory binding the Rust SDK Git tag
  and commit, every platform asset and checksum, its Feature 8 handoff, SBOM,
  attestation, workflow identity, and verification result.
- **Release_Capability_Report:** The versioned, evidence-linked Feature 9 document
  describing the released Rust SDK against the Definitive_Go_SDK, the complete
  Completeness_Ledger, and the Existing_Rust_SDK_Baseline.
- **Rust_SDK_Release_Tag:** An immutable, namespaced Git tag whose commit is the exact
  Cargo `rev` supported for downstream Rust SDK consumption.
- **Rust_SDK_Signoff:** The bounded engine-backed evaluation that consumes matching
  Implementation_Closure_Evidence, runs the complete Rust case inventory against one
  Exact_Target_Signoff_Artifact, and emits one atomic verdict.
- **Standalone_Client:** Generated Rust bindings and project metadata for consuming
  the Core_Schema, a module, or its dependencies outside a Dagger module runtime.
- **Target_Revision:** The Dagger engine revision against which generated code and SDK
  behaviour are claimed to be compatible.
- **Wire_Parity:** Faithful representation of engine schema types, nullability,
  defaults, argument semantics, identifiers, and errors.

## Target State

At completion, a Rust user can connect to Dagger from an ordinary Rust application,
author and execute a Dagger module selected with `--sdk rust`, consume other modules,
generate standalone clients, diagnose failures and traces, and rely on published
Git-tagged SDK source, verified GitHub Release assets, and documentation under a
coherent stability and compatibility policy.

Every applicable engine and Definitive_Go_SDK capability is represented in the
Completeness_Ledger as one of:

- implemented by behaviourally equivalent Rust functionality;
- implemented through a documented Idiomatic_Equivalence; or
- explicitly inapplicable to Rust, with reviewed evidence and an automated guard
  against accidental expansion of the exception.

Untracked omissions, permanently aspirational checklist items, and parity claims based
only on API-name comparison are not acceptable. Go-specific syntax, Go memory models,
and Go package layout are outside scope. Changes to the engine protocol solely to
imitate a Go API are outside scope unless the engine contract itself is incomplete for
all SDKs.

Rust_SDK_Signoff builds or imports one Exact_Target_Signoff_Artifact for each immutable
target and platform identity, reuses one engine service and installed Rust baseline
across the complete case inventory, and consumes matching Implementation_Closure_Evidence
without replaying engine-free development checks. The sign-off graph contains only the
engine, CLI, engine-packaged Go SDK runtime content required by the Rust adapter, Rust
SDK content, and Rust-owned cases; behavioural authority from the Definitive_Go_SDK
does not require its complete build or test suite to run inside Rust sign-off.

Feature 9 consumes each platform's Release_Handoff_Record without rebuilding its
signed-off payload. The initial release contains Linux/amd64 and Apple Silicon consumer
coordinates only: the latter combines a separately signed-off Linux/arm64 engine OCI
payload with a compatible Darwin/arm64 CLI. Windows remains outside the initial release
matrix until it receives an explicit artifact and verdict. Downstream Cargo consumers
pin the Rust SDK by the exact commit behind Rust_SDK_Release_Tag; no fork release
depends on crates.io or GHCR publication.

## Evidence From Current Code

All repository citations in this document refer to Target_Revision
`25300124ca110612edc09c43f89cb5fad6028170` unless stated otherwise.

- **Engine wire contract (authoritative):** engine introspection consumed by
  `cmd/codegen/introspection/**` and the generated API surfaces under `sdk/go/**` and
  `sdk/rust/crates/dagger-sdk/src/gen.rs`.
- **Expected client behaviour (authoritative):** `sdk/go/client.go:15-164` defines the
  owned client, connection options, shutdown, and raw request execution;
  `sdk/go/engineconn/engineconn.go:15-56` defines connection abstraction and
  selection.
- **Expected engine SDK surfaces (authoritative):** `core/sdk.go:14-428` defines client
  generation, initialization, runtime, code generation, type discovery, and SDK
  capability interfaces.
- **Expected generator surfaces (authoritative):**
  `cmd/codegen/generator/generator.go:17-36` defines Go and TypeScript backends and the
  module, client, library, and entrypoint generation operations.
- **Current builtin gap:** `core/sdk/sdkmeta/sdkmeta.go:9-20` lists Go, Dang, Python,
  TypeScript, PHP, Elixir, and Java, but not Rust; `internal-docs/dagger-codegen.md:102`
  records Rust as SDK-related code rather than a built-in SDK.
- **Current Rust client:** `sdk/rust/crates/dagger-sdk/src/client.rs:16-40` exposes
  closure-scoped connection helpers; `sdk/rust/crates/dagger-sdk/src/core/config.rs:8-46`
  exposes a smaller configuration surface than the Definitive_Go_SDK.
- **Current generated coverage:**
  `sdk/rust/crates/dagger-sdk/src/gen.rs:10611-10629,11655-11943` includes module serve
  and current-call/current-module GraphQL bindings, but no engine module runtime or
  dispatch layer is registered.
- **Current version inconsistency:** `sdk/rust/Cargo.toml:6-8` declares SDK
  `1.0.0-beta.10`, edition 2024, and Rust 1.97.1, while
  `sdk/rust/crates/dagger-sdk/src/core/version.rs:1` embeds engine
  `1.0.0-beta.7`.
- **Current release path:** `toolchains/rust-sdk-dev/main.go:244-355` dry-runs and
  publishes `dagger-sdk`; `sdk/rust/crates/dagger-codegen/Cargo.toml:4` separately
  declares `dagger-codegen` publishable. The fork release policy must fence this
  historical registry path and prove that Git-sourced package resolution is complete.
- **Current security baseline:** the Rust workspace denies unsafe code in
  `sdk/rust/Cargo.toml`, has Cargo Deny policy in `sdk/rust/deny.toml`, and is covered
  by repository dependency and vulnerability automation. These controls are a
  foundation, not evidence of feature completeness.
- **Current focused sign-off graph:** `toolchains/rust-sdk-dev/main.go:169-201`
  excludes unrelated SDK sources; `toolchains/rust-sdk-dev/main.go:397-469` constructs
  one digest-identified Rust SDK content object; and
  `toolchains/rust-sdk-dev/main.go:568-672` reuses one focused service and installed
  runner across a closed, bounded-concurrency case set.
- **Current focused engine composition:** `toolchains/engine-dev/main.go:331-375`
  retains one Rust OCI content identity, while
  `toolchains/engine-dev/build/builder.go:234-273` overlays only the changing engine,
  exact-target Go SDK content, and Rust SDK content on a digest-pinned baseline. The
  current graph does not yet define the umbrella-wide import/retry contract,
  Implementation_Closure_Evidence consumption, phase timings, or duplicate-build
  rejection required by Rust_SDK_Signoff.
- **Historical module evidence:** upstream pull request #12229, open and unmerged as
  of 2026-08-05, proposes Rust SDK registration, a Go-based module runtime,
  procedural-macro authoring, call dispatch, and nullable/Void fixes.
- **Informative supply-chain precedent:** `MChorfa/dagger-zig` commit
  `1ae0304f173fc2f617960cd67a7daad1729357bb` demonstrates per-target archives,
  checksums, SBOMs, GitHub attestations, and keyless signing. Its release run
  `30175226466` also demonstrates why the fork must fail closed: provenance and final
  verification were skipped while other release jobs passed. Its tag-only container
  references, permissive security steps, and self-declared policy are evidence to
  improve upon rather than a release implementation to copy.
- **GitHub release authority:** GitHub's
  [artifact-attestation](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations)
  and
  [immutable-release](https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases)
  documentation defines OIDC/Sigstore-backed attestations, immutable tags/assets, and
  the draft-assemble-verify-publish sequence used by Feature 9.
- **Current fork release controls:** repository-settings observations on 2026-08-13
  confirmed that legacy branch protection already guards `main` with strict Rust
  security status, linear history, resolved conversations, admin enforcement, and no
  force-push or deletion. Immutable releases were then enabled, and active repository
  ruleset `20817649` was added without bypass actors to prevent update or deletion of
  `refs/tags/rust-sdk/v*` after creation. Feature 9 must reverify these controls before
  public publication and fail closed if they have weakened.
- **Apple distribution authority:** Apple
  [Developer ID](https://developer.apple.com/developer-id/) and
  [notarization](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)
  documentation defines the Gatekeeper-ready contract for a fork-built Darwin CLI.
  Apple signing does not apply to the Linux/arm64 engine OCI payload, and
  GitHub/Sigstore attestation does not substitute for Developer ID signing.

## Audit Gap Traceability

| Audited surface | Baseline state | Owning feature | Completion evidence |
|---|---|---|---|
| Capability inventory | No exhaustive Go-to-Rust ledger | Feature 1 | Versioned Completeness_Ledger with automated drift detection |
| Core GraphQL bindings | Broad generated surface exists; parity is not measured | Feature 4 | Schema fixtures, generated snapshots, and engine tests |
| Client ownership | Connection is closure-scoped | Feature 2 | Owned_Client lifecycle tests |
| Client options | Workdir, config path, module loading, timeouts, and logger only | Feature 2 | Configuration conformance table and tests |
| Connection injection | No public Go-equivalent connection abstraction | Feature 2 | Injected transport/session tests |
| Session source precedence | Partial existing-session, local CLI, and download support | Feature 3 | Deterministic precedence and failure tests |
| Existing-session invariants | Invalid overrides are not comprehensively rejected | Feature 3 | Typed invariant-error tests |
| Trace propagation | No Go-equivalent OpenTelemetry propagation | Feature 3 | HTTP and CLI propagation integration tests |
| CLI provisioning | Windows archive path is unfinished; lifecycle hardening differs | Feature 3 | Linux, macOS, and Windows provisioning tests |
| Error model | Public paths retain broad errors and panic-prone branches | Feature 3 | Typed error and panic-free-path tests |
| Generator registration | Main generator supports only Go and TypeScript | Feature 5 | Rust generator selected by engine integration tests |
| Builtin SDK registration | Rust is absent from engine SDK metadata | Feature 5 | `--sdk rust` initialization and development tests |
| Module runtime | No merged runtime implementation | Feature 5 | Engine runtime construction and execution tests |
| Module type discovery | No merged Rust source-to-TypeDef path | Feature 6 | Complete type-surface conformance fixtures |
| Module dispatch | No merged Rust invocation/return path | Feature 6 | Sync, async, stateful, dependency, and error tests |
| Standalone clients | Rust generator is not wired to engine client generation | Feature 7 | Core, module, and dependency client fixtures |
| Platform matrix | Rust-specific end-to-end platform coverage is incomplete | Feature 8 | Required CI matrix passes |
| Reusable SDK sign-off | Focused content and one-service reuse exist, but no umbrella-wide artifact/retry/closure/timing contract | Feature 8 | One digest-bound artifact and atomic Rust-only sign-off verdict per target/platform identity |
| Security gates | Strong baseline exists but must cover every new component | Feature 8 | Locked, denied, audited, and secret-safe checks pass |
| Fork distribution graph | Historical registry settings conflict with Git-only distribution | Feature 9 | Exact-revision Cargo rehearsal and immutable asset verification |
| Version synchronization | Workspace and embedded engine versions differ | Feature 9 | Single release update and consistency checks |
| User documentation | Current material still describes an experimental SDK | Feature 9 | Stable client and module guides with tested examples |

## Client Configuration Policy

The table maps every non-deprecated `ClientOpt` exposed by
`sdk/go/client.go:34-109` plus the existing Rust timeout controls. Child design may
choose Rust-native names and builders while preserving these semantics.

| Behavioural input | Target policy | Invalid-input behaviour | Side-effect boundary |
|---|---|---|---|
| Working directory | Accept a local path used as the host workdir | Return a typed configuration error | Passed only to a newly started CLI session |
| Workspace reference | Accept the local or remote workspace reference understood by the engine | Return a typed configuration error | Passed only to a newly started CLI session |
| Log/progress destination | Accept a caller-controlled diagnostic sink | Return a typed configuration error before session start | Receives CLI progress without secrets |
| Load workspace modules | Opt into loading workspace modules | Reject override for Existing_Session | Passed only to a newly started CLI session |
| Explicit connection | Use a caller-supplied engine connection | Reject mutually exclusive session-start options | Must not start or download a CLI |
| Engine version override | Select an explicitly requested engine/CLI version | Return a typed unsupported-version error | Affects CLI discovery/download only |
| Verbosity | Forward the requested diagnostic level | Return a typed range error | Affects CLI progress only |
| Runner host | Forward the alternate engine runner endpoint | Return a typed endpoint error | Affects newly started CLI session only |
| Additional environment variable | Forward each explicit key/value pair | Return a typed key error | Affects newly started CLI process only |
| Session startup timeout | Bound session establishment | Return a typed timeout error | Cancels and reaps any started child process |
| GraphQL execution timeout | Bound individual query execution | Return a typed timeout error | Cancels the request without corrupting the client |

## Requirements

---

## Feature 1: Completeness Contract

### Requirement 1.1: Exhaustive, Versioned Parity Ledger

**User Story:** As a Rust SDK maintainer, I want a reproducible completeness contract,
so that “Go-level” is an evidence-backed release claim rather than an impression.

#### Acceptance Criteria

1. WHEN a Target_Revision is selected, THE Completeness_Ledger SHALL identify its
   engine schema revision and Definitive_Go_SDK revision.
2. THE Completeness_Ledger SHALL record the corresponding Rust support status for every
   public engine schema type, field, argument, input, enum, scalar, and identifier.
3. THE Completeness_Ledger SHALL record the corresponding Rust support status for every
   observable Definitive_Go_SDK capability outside generated schema bindings.
4. WHEN Rust uses an Idiomatic_Equivalence, THE Completeness_Ledger SHALL record the
   behavioural mapping and rationale.
5. IF a capability is classified as inapplicable, THEN THE Completeness_Ledger SHALL
   cite reviewed evidence for the exception.
6. WHEN a ledger item is marked complete, THE Completeness_Ledger SHALL link automated
   verification evidence.
7. WHEN the Target_Revision changes, THE parity tooling SHALL fail on unclassified
   engine or Go SDK capability drift.

### Requirement 1.2: Compatibility and Stability Policy

**User Story:** As a Rust SDK consumer, I want explicit compatibility guarantees, so
that I can upgrade Dagger and the SDK without guessing which combinations work.

#### Acceptance Criteria

1. WHEN a Rust SDK release is prepared, THE release metadata SHALL identify its
   supported engine compatibility range.
2. WHEN an engine schema change is incompatible with generated Rust code, THE client
   SHALL return a typed compatibility error.
3. WHEN a public Rust API is proposed, THE child specification SHALL classify its
   stability and SemVer impact.
4. IF a temporary experimental API is necessary, THEN its documentation SHALL state
   the graduation or removal condition.

---

## Feature 2: Client Lifecycle and Configuration

### Requirement 2.1: Owned Client Lifecycle

**User Story:** As a Rust application author, I want an owned Dagger client with clear
lifecycle semantics, so that I can compose it naturally across async application code.

#### Acceptance Criteria

1. WHEN a connection succeeds, THE Rust SDK SHALL return an Owned_Client.
2. WHEN an Owned_Client is explicitly closed, THE Rust SDK SHALL release its owned
   session resources deterministically.
3. WHEN close is requested more than once, THE Owned_Client SHALL return an idempotent
   result.
4. WHEN the last client owner is dropped without explicit close, THE Rust SDK SHALL
   initiate safe best-effort cleanup without blocking a destructor indefinitely.
5. WHEN a caller cancels connection establishment, THE Rust SDK SHALL terminate and
   reap any child process it started.
6. WHEN closure-scoped convenience is retained, THE convenience API SHALL delegate to
   the Owned_Client lifecycle.
7. WHEN client state is shared across tasks, THE public API SHALL preserve Rust's
   thread-safety guarantees without exposing transport internals.

### Requirement 2.2: Complete Connection Configuration

**User Story:** As a Dagger user, I want the Rust client to support every material Go
connection behaviour, so that environment and deployment choices do not force me to
switch SDKs.

#### Acceptance Criteria

1. THE Rust SDK SHALL expose an Idiomatic_Equivalence for every row in the Client
   Configuration Policy.
2. WHEN configuration values conflict, THE Rust SDK SHALL return a typed error before
   starting external work.
3. WHEN an Explicit_Connection is supplied, THE Rust SDK SHALL avoid CLI discovery,
   startup, and download.
4. WHEN a caller supplies custom environment variables, THE Rust SDK SHALL preserve
   all explicitly supplied non-conflicting values.
5. IF a custom environment variable attempts to replace an SDK-managed secret or
   session invariant, THEN THE Rust SDK SHALL return a typed configuration error.
6. WHEN raw GraphQL execution is required, THE Owned_Client SHALL provide a supported
   request path equivalent to the Definitive_Go_SDK's `Do` capability.
7. WHEN advanced query composition is required, THE Owned_Client SHALL provide stable
   access to the supported query-construction surface.

---

## Feature 3: Transport, Observability, and Reliability

### Requirement 3.1: Deterministic Session Selection

**User Story:** As a Rust SDK user, I want predictable connection selection, so that
the same configuration connects to the same engine source across environments.

#### Acceptance Criteria

1. WHEN an Explicit_Connection is configured, THE connection selector SHALL choose it
   before every implicit source.
2. WHEN valid Existing_Session variables are present, THE connection selector SHALL
   choose that session before local or downloaded CLI sources.
3. WHEN an explicit local CLI path is configured, THE connection selector SHALL use it
   before a downloaded CLI.
4. WHEN no earlier source applies, THE connection selector SHALL use a verified
   downloaded CLI for the requested engine version.
5. IF Existing_Session variables are malformed, THEN THE connection selector SHALL
   return a typed session-environment error.
6. IF Existing_Session configuration attempts to override session-owned workspace
   state, THEN THE connection selector SHALL return a typed invariant error.
7. IF an explicitly configured local CLI cannot start, THEN THE connection selector
   SHALL return its typed startup error without silently changing source.

### Requirement 3.2: Trace and Diagnostic Propagation

**User Story:** As an operator, I want Rust SDK work to appear in the same trace and
diagnostic stream as other Dagger SDKs, so that cross-process failures are observable.

#### Acceptance Criteria

1. WHEN an application context carries W3C trace state, THE Rust HTTP transport SHALL
   propagate that state to engine requests.
2. WHEN the SDK starts a CLI session, THE child environment SHALL receive the active
   trace context through the engine-defined propagation variables.
3. WHEN the SDK starts a CLI session, THE session labels SHALL identify the Rust SDK
   name and version.
4. WHEN the CLI emits progress output, THE configured diagnostic sink SHALL receive it
   without corrupting GraphQL protocol data.
5. WHEN a diagnostic event contains credentials or session tokens, THE Rust SDK SHALL
   redact the sensitive value.

### Requirement 3.3: Typed Failures and Cross-Platform Provisioning

**User Story:** As a Rust SDK consumer, I want reliable failures and CLI provisioning
on supported platforms, so that infrastructure problems are actionable rather than
panics or hangs.

#### Acceptance Criteria

1. WHEN connection, transport, GraphQL, engine-domain, timeout, or shutdown fails, THE
   Rust SDK SHALL return a distinguishable typed public error.
2. WHEN the engine returns structured execution failure extensions, THE Rust SDK SHALL
   preserve their typed fields.
3. THE Rust SDK SHALL avoid `panic!`, `unwrap`, and invariant-free `expect` termination
   for every reachable library input or external failure.
4. WHEN downloading a CLI release, THE provisioner SHALL verify its published checksum
   before installation.
5. WHEN provisioning on Linux, macOS, or Windows, THE provisioner SHALL extract the
   platform's published archive format.
6. WHEN a newer CLI version is installed, THE provisioner SHALL apply a bounded and
   race-safe retention policy to obsolete managed versions.
7. WHEN session establishment exceeds its configured timeout, THE Rust SDK SHALL return
   a typed timeout after reaping owned processes.
8. WHEN background protocol or logging work fails, THE Owned_Client SHALL surface that
   failure through a supported observation path.

---

## Feature 4: Core Schema Code Generation

### Requirement 4.1: Complete and Idiomatic Generated Bindings

**User Story:** As a Rust SDK user, I want generated bindings for the complete engine
schema, so that every core Dagger capability is available with Rust-native types.

#### Acceptance Criteria

1. THE Rust generator SHALL emit the corresponding binding required by the
   Completeness_Ledger for every Core_Schema object, interface, enum, scalar, input,
   field, and argument.
2. WHEN the schema marks a value nullable, THE generated Rust signature SHALL preserve
   its nullability without runtime unwrapping.
3. WHEN the schema marks a value required, THE generated Rust signature SHALL prevent
   omission where Rust's type system can express the constraint.
4. WHEN an argument has an engine default, THE generated API SHALL preserve the
   distinction between omission and an explicit value.
5. WHEN a schema type has an identifier, THE generated API SHALL support typed load and
   reference round-trips.
6. WHEN a GraphQL name conflicts with Rust syntax or naming conventions, THE generator
   SHALL emit a deterministic documented Rust-safe mapping.
7. WHEN equivalent introspection input is generated twice, THE generator SHALL produce
   byte-stable source after formatting.
8. WHEN generated code is compiled at the declared MSRV, THE generated crate SHALL
   require no undocumented feature flags.

### Requirement 4.2: Generator Maintainability

**User Story:** As a Rust SDK maintainer, I want generation to be reproducible and
reviewable, so that schema updates cannot hide handwritten fixes or accidental churn.

#### Acceptance Criteria

1. WHEN generated behaviour changes, THE implementation SHALL originate in generator
   logic or templates rather than direct edits to `gen.rs`.
2. WHEN the Target_Revision changes, THE repository generation command SHALL regenerate
   all committed Rust bindings from checked inputs.
3. WHEN generated output differs from committed output, THE verification pipeline
   SHALL fail with a reviewable diff.
4. WHEN a schema edge case is fixed, THE generator test suite SHALL retain a minimal
   regression fixture for it.
5. WHEN handwritten and generated code interact, THE crate boundary SHALL keep
   lifecycle and policy logic outside generated types.

---

## Feature 5: Engine SDK Integration

### Requirement 5.1: Rust SDK Resolution and Initialization

**User Story:** As a Dagger module author, I want the engine to recognize Rust as an
SDK, so that standard Dagger project commands can initialize and develop Rust modules.

#### Acceptance Criteria

1. WHEN a module selects SDK name `rust`, THE engine SHALL resolve the supported Rust
   Module_SDK implementation.
2. WHEN an unknown or unsupported Rust SDK version is requested, THE engine SHALL
   return a diagnostic identifying the unresolved SDK reference.
3. WHEN `dagger init --sdk rust` targets an empty source tree, THE Module_SDK SHALL
   create the minimal documented Rust module project.
4. WHEN initialization targets an existing compatible Cargo project, THE Module_SDK
   SHALL preserve unrelated user files and settings.
5. WHEN a Rust module enters development, THE Module_SDK SHALL generate required
   bindings and entrypoint artifacts through engine-managed changesets.
6. WHEN generated module artifacts are written, THE Module_SDK SHALL make their
   ownership and regeneration policy explicit.

### Requirement 5.2: Engine Codegen and Runtime Contracts

**User Story:** As a Dagger engine maintainer, I want Rust to satisfy the same engine
SDK contracts as Go, so that Rust participates in standard codegen and runtime flows.

#### Acceptance Criteria

1. WHEN the engine requests Rust library generation, THE Rust backend SHALL satisfy the
   `GenerateLibrary` contract.
2. WHEN the engine requests Rust module generation, THE Rust backend SHALL satisfy the
   `GenerateModule` contract.
3. WHEN the engine requests Rust client generation, THE Rust backend SHALL satisfy the
   `GenerateClient` contract.
4. WHEN the engine requests a Rust module entrypoint, THE Rust backend SHALL satisfy
   the `GenerateEntrypoint` contract.
5. WHEN the engine requests a module runtime, THE Module_SDK SHALL return a reproducible
   container runtime for the Target_Revision.
6. WHEN the runtime builds user source, THE Module_SDK SHALL use the project's declared
   toolchain and locked dependency policy.
7. WHEN the runtime executes a module, THE Module_SDK SHALL expose the engine protocol
   endpoint expected by `ModuleRuntime`.
8. WHEN implementation assets are packaged, THE Module_SDK SHALL avoid depending on an
   unpublished local repository checkout.

---

## Feature 6: Rust Module Authoring and Dispatch

### Requirement 6.1: Complete Module Type Discovery

**User Story:** As a Rust developer, I want to declare Dagger objects and functions in
idiomatic Rust, so that the engine can expose my module without a parallel schema
language.

#### Acceptance Criteria

1. WHEN a Rust declaration is explicitly exported as a Dagger object, THE Module_SDK
   SHALL emit its object TypeDef and documentation.
2. WHEN an exported object has state, THE Module_SDK SHALL emit every supported state
   field with its correct Dagger type.
3. WHEN an exported function is synchronous, THE Module_SDK SHALL expose it through the
   same engine function model as an asynchronous function.
4. WHEN an exported function is asynchronous, THE Module_SDK SHALL preserve async
   execution without blocking the runtime executor.
5. THE Rust authoring model SHALL provide a type-safe equivalent or a ledgered
   inapplicability decision for every Go-supported module input and output type.
6. WHEN a function argument is optional or defaulted, THE emitted TypeDef SHALL
   preserve omission, nullability, and default semantics.
7. WHEN a declaration is not eligible for export, THE Module_SDK SHALL return a
   source-located diagnostic during development.
8. WHEN exported API documentation is present, THE Module_SDK SHALL propagate it to the
   engine TypeDef.
9. WHEN an exported name conflicts after Rust-to-Dagger normalization, THE Module_SDK
   SHALL return a source-located collision diagnostic.

### Requirement 6.2: Stateful and Dependency-Aware Dispatch

**User Story:** As a Rust module author, I want reliable invocation of my module
functions, so that state, dependencies, values, and failures behave like modules in the
Definitive_Go_SDK.

#### Acceptance Criteria

1. WHEN the engine supplies a current function call, THE Rust entrypoint SHALL dispatch
   it to the uniquely matching exported function.
2. WHEN an instance function is invoked, THE Rust entrypoint SHALL reconstruct its
   parent object state before execution.
3. WHEN a function argument references a core or dependency object, THE Rust entrypoint
   SHALL decode it through the generated typed binding.
4. WHEN a function returns a supported value, THE Rust entrypoint SHALL encode it
   through the engine's current-call return path.
5. WHEN a function returns an application error, THE Rust entrypoint SHALL report it
   through the engine's current-call error path.
6. WHEN a function panics, THE Rust entrypoint SHALL convert the boundary failure into
   a diagnosable module error without corrupting the protocol session.
7. WHEN module code calls the Core_Schema, THE module-scoped client SHALL reuse the
   active engine session.
8. WHEN module code calls itself or a dependency, THE generated bindings SHALL preserve
   the target module's types and namespace.
9. WHEN concurrent calls execute in one runtime, THE dispatch layer SHALL isolate
   call-scoped state and errors.

---

## Feature 7: Standalone Client and Dependency Generation

### Requirement 7.1: Complete Generated Client Projects

**User Story:** As a Rust application author, I want generated clients for Dagger core
and modules, so that I can consume typed Dagger APIs outside a module runtime.

#### Acceptance Criteria

1. WHEN generating a Core_Schema client, THE Rust backend SHALL emit compilable
   bindings and connection support for the Target_Revision.
2. WHEN generating a module client, THE Rust backend SHALL emit the module's complete
   public TypeDef surface.
3. WHEN a module has dependencies, THE Rust backend SHALL keep the generated client
   limited to Core plus its one selected module and SHALL require an independently
   bound client for each dependency surface a consumer selects.
4. WHEN generated names from different modules collide, THE Rust backend SHALL place
   them in deterministic non-conflicting namespaces.
5. WHEN a generated client project requires Cargo metadata, THE Rust backend SHALL emit
   compatible dependency versions and feature selections.
6. WHEN generation runs inside an existing Cargo project, THE Rust backend SHALL
   preserve unrelated manifest entries and source files.
7. WHEN a dependency or schema changes, THE regeneration result SHALL remove obsolete
   owned artifacts without removing user-owned files.

### Requirement 7.2: Generated Client Usability

**User Story:** As a Rust consumer, I want generated clients to integrate with normal
Cargo workflows, so that using Dagger does not require repository-specific build
knowledge.

#### Acceptance Criteria

1. WHEN a supported client is generated, THE output SHALL pass formatting, checking,
   documentation, and tests under the declared toolchain.
2. WHEN the client depends on the released Dagger SDK, THE generated manifest SHALL
   select the exact Git revision bound to the compatible Rust_SDK_Release_Tag.
3. WHEN a user follows the generated-client quickstart, THE example SHALL execute an
   engine query without manual edits to generated code.
4. WHEN generated code exposes async operations, THE public signatures SHALL compose
   with standard Rust async runtimes according to the documented runtime policy.

---

## Feature 8: Conformance, Platform, and Security Gates

### Requirement 8.1: Executable Go-Level Conformance

**User Story:** As a release reviewer, I want automated cross-SDK conformance evidence,
so that completeness regressions cannot ship behind a passing compile check.

#### Acceptance Criteria

1. THE test suite SHALL contain or link an executable verification at the appropriate
   unit, generation, integration, or end-to-end layer for every complete
   Completeness_Ledger item.
2. WHEN Go tests establish an observable edge case, THE Rust suite SHALL verify the
   equivalent behaviour or ledgered Idiomatic_Equivalence.
3. WHEN generated schema fixtures change, THE conformance suite SHALL report newly
   added, removed, or reclassified surface area.
4. WHEN a Rust module fixture is exercised, THE end-to-end suite SHALL cover
   initialization, development, code generation, execution, and dependency use.
5. WHEN a standalone client fixture is exercised, THE end-to-end suite SHALL build and
   run it outside the Dagger repository workspace.
6. WHEN the repository claims Go-level completeness, THE release pipeline SHALL fail
   if any applicable ledger item lacks passing evidence.

### Requirement 8.2: Supported Platform and Security Baseline

**User Story:** As a Rust SDK adopter, I want supported platforms and dependencies to be
continuously verified, so that completeness does not trade away supply-chain or runtime
safety.

#### Acceptance Criteria

1. BEFORE exact-target release sign-off, THE Portable_Platform_Matrix SHALL contain
   current applicable engine-free observations from Linux, macOS, and Windows.
2. WHEN Cargo resolves the workspace, THE verification pipeline SHALL use the committed
   lockfile.
3. WHEN dependency policy is evaluated, THE verification pipeline SHALL reject active
   advisories, unapproved licenses, wildcard dependencies, unknown registries, and
   unknown Git sources according to `deny.toml`.
4. WHEN a dependency exception is necessary, THE exception record SHALL identify the
   advisory, reachability rationale, upstream remediation, owner, and expiry condition.
5. WHEN Rust code is compiled, THE workspace SHALL continue to deny unsafe code unless
   a separately reviewed boundary documents and tests its safety invariant.
6. WHEN generated or runtime artifacts are built, THE pipeline SHALL verify pinned
   external artifact identities and checksums.
7. WHEN tests, errors, traces, or snapshots are produced, THE verification pipeline
   SHALL detect disclosure of configured test secrets and session credentials.
8. WHEN routine fork pull-request CI runs, THE platform workflow SHALL exercise Linux
   and macOS without requiring a paid Windows runner integration.
9. WHEN ultimate SDK sign-off refreshes the Windows observation, THE platform workflow
   SHALL use the separately dispatched GitHub-hosted engine-free Windows preflight.

### Requirement 8.3: Bounded Reusable Exact-Target Sign-Off

**User Story:** As a Rust SDK release reviewer, I want one reusable exact-target
sign-off execution, so that complete engine-backed evidence is reproducible without
rebuilding Dagger or unrelated SDKs for every case and retry.

#### Acceptance Criteria

1. WHEN Rust_SDK_Signoff begins for a Target_Revision and platform, THE verification
   pipeline SHALL build or import exactly one Exact_Target_Signoff_Artifact.
2. THE Exact_Target_Signoff_Artifact SHALL bind the immutable Target_Revision,
   platform, engine and CLI input identity, engine-packaged Go SDK runtime identity,
   Rust SDK manifest and descriptor identity, and toolchain identity.
3. WHEN Rust_SDK_Signoff retries a case against unchanged bound identities, THE
   verification pipeline SHALL reuse the same Exact_Target_Signoff_Artifact digest.
4. WHEN the Exact_Target_Signoff_Artifact is built, THE verification pipeline SHALL
   build the engine binary at most once for that artifact identity.
5. WHEN the Exact_Target_Signoff_Artifact is built, THE verification pipeline SHALL
   build the CLI binary at most once for that artifact identity.
6. WHEN the Exact_Target_Signoff_Artifact is built, THE verification pipeline SHALL
   build the mandatory engine-packaged Go SDK runtime content at most once for that
   artifact identity.
7. WHEN the Exact_Target_Signoff_Artifact is built, THE verification pipeline SHALL
   build the Rust SDK content at most once for that artifact identity.
8. THE Rust_SDK_Signoff graph SHALL exclude unrelated SDK builders.
9. THE Rust_SDK_Signoff graph SHALL exclude unrelated SDK test suites.
10. THE Rust_SDK_Signoff graph SHALL exclude unrelated SDK generation.
11. THE Rust_SDK_Signoff graph SHALL exclude distribution-wide build paths.
12. THE Rust_SDK_Signoff graph SHALL not run the complete Definitive_Go_SDK test
    suite.
13. WHEN Rust-specific Go adapter behaviour requires verification, THE
   Implementation_Closure_Evidence SHALL identify the exact bounded Go packages and
   tests that supplied that evidence.
14. WHEN Rust_SDK_Signoff evaluates a source identity, THE verification pipeline SHALL
   require matching Implementation_Closure_Evidence before starting an engine.
15. THE Rust_SDK_Signoff graph SHALL not replay engine-free implementation, fixture,
    hygiene, or security suites already admitted by matching
    Implementation_Closure_Evidence.
16. WHEN engine-backed cases execute, THE verification pipeline SHALL start exactly
    one engine service for the Exact_Target_Signoff_Artifact.
17. WHEN the Installed_Rust_Baseline is prepared, THE verification pipeline SHALL
    materialize it exactly once before case fan-out.
18. WHEN cases require isolated workspaces, THE verification pipeline SHALL branch
    each workspace from the Installed_Rust_Baseline without constructing another
    engine or artifact.
19. WHEN Rust_SDK_Signoff completes, THE verification pipeline SHALL emit one atomic
    verdict bound to the artifact digest, Target_Revision, platform, Rust SDK manifest
    and descriptor identities, Implementation_Closure_Evidence digest, and complete
    case outcomes.
20. IF any required case is skipped, unknown, failed, or absent, THEN THE
    Rust_SDK_Signoff verdict SHALL fail without admitting partial sign-off evidence.
21. WHEN Rust_SDK_Signoff executes, THE verification pipeline SHALL record separate
    durations for artifact build or import, engine startup, Rust SDK installation, and
    every case.
22. IF the observed sign-off graph constructs more than one target artifact, THEN THE
    verification pipeline SHALL reject the run before admitting its verdict.
23. IF the observed sign-off graph starts more than one engine service, THEN THE
    verification pipeline SHALL reject the run before admitting its verdict.
24. IF the observed sign-off graph constructs the Installed_Rust_Baseline more than
    once, THEN THE verification pipeline SHALL reject the run before admitting its
    verdict.
25. IF the observed sign-off graph enters an unrelated SDK or distribution build path,
    THEN THE verification pipeline SHALL reject the run before admitting its verdict.

---

## Feature 9: Distribution, Documentation, and Stable Release

### Requirement 9.1: Coherent Git Revision and Version Release

**User Story:** As a Rust SDK consumer, I want one immutable Git revision for the
complete SDK package graph, so that Cargo resolves every supported feature from the
exact reviewed release source.

#### Acceptance Criteria

1. WHEN release architecture is finalized, THE workspace SHALL classify every Rust
   package as public API or private implementation detail.
2. THE fork release pipeline SHALL not publish a Rust package to crates.io.
3. THE fork release pipeline SHALL not publish an engine or SDK image to GHCR.
4. WHEN a Rust SDK version is prepared, THE release automation SHALL update workspace,
   lockfile, embedded engine version, generated metadata, and documentation references
   consistently.
5. WHEN version-bearing files disagree, THE release pipeline SHALL fail before
   publication.
6. WHEN a Rust SDK release is created, THE release automation SHALL create one
   immutable namespaced Rust_SDK_Release_Tag bound to the reviewed release commit.
7. WHEN a downstream Cargo consumer selects the Rust SDK, THE supported installation
   SHALL pin the exact commit behind Rust_SDK_Release_Tag through `rev`.
8. WHEN Git-sourced installation is rehearsed, THE release pipeline SHALL resolve and
   compile the complete supported package and feature graph from a clean external
   fixture without repository-relative path dependencies.
9. IF future maintainers propose registry publication, THEN the release policy SHALL
   require a separately reviewed specification change before enabling it.
10. WHEN the declared MSRV or edition changes, THE release notes SHALL state the
   compatibility impact and migration path.

### Requirement 9.2: Stable Documentation and Adoption Readiness

**User Story:** As a Rust SDK adopter, I want trustworthy Rust SDK and module guides,
so that I can adopt Dagger without reverse-engineering Go examples or experimental
internals.

#### Acceptance Criteria

1. WHEN the Rust SDK is presented as stable, THE README and architecture documentation
   SHALL no longer describe implemented capability as experimental or missing.
2. WHEN a user follows the client quickstart, THE documented project SHALL build and
   execute in automated documentation tests.
3. WHEN a user follows the module quickstart, THE documented project SHALL initialize,
   develop, and call a Rust Dagger module in automated documentation tests.
4. WHEN documenting public APIs, THE Rust documentation SHALL explain ownership,
   shutdown, async runtime, error, compatibility, and regeneration semantics.
5. WHEN an intentional difference from Go exists, THE migration documentation SHALL
   describe the Rust Idiomatic_Equivalence rather than presenting it as an omission.
6. WHEN a user upgrades between supported Rust SDK versions, THE changelog SHALL
   identify breaking changes, deprecations, engine requirements, and migration steps.
7. WHEN the release candidate is evaluated by a downstream consumer, THE acceptance
   suite SHALL run a representative external workflow through the exact Git-revision
   installation path.
8. WHEN every Release_Gate passes, THE project documentation SHALL identify the Rust
   SDK as Go-level complete for the declared Target_Revision.

### Requirement 9.3: Exact Assets, Provenance, and Immutable Publication

**User Story:** As a release consumer, I want every downloadable byte tied to the
sign-off that approved it, so that release presentation cannot replace verified
content with an untested rebuild.

#### Acceptance Criteria

1. WHEN Feature 9 assembles a platform asset, THE release pipeline SHALL consume the
   exact payload bytes bound by that platform's Release_Handoff_Record.
2. THE release pipeline SHALL not rebuild a signed-off engine payload during release
   assembly.
3. WHEN the release claims a platform, THE Release_Manifest SHALL bind that platform
   to its own passing Feature 8 verdict.
4. THE Release_Manifest SHALL bind the Rust_SDK_Release_Tag and its exact commit.
5. THE Release_Manifest SHALL bind every asset to its checksum.
6. THE Release_Manifest SHALL bind every asset to an SPDX or CycloneDX SBOM.
7. THE Release_Manifest SHALL bind every asset to its Feature 8 artifact manifest,
   security report, and Atomic_Signoff_Verdict.
8. THE Release_Manifest SHALL bind every release workflow, action, container image,
   toolchain, and scanner to an immutable commit or digest.
9. WHEN release automation executes, THE workflow SHALL separate build, attestation,
   verification, and publication permissions according to least privilege.
10. WHEN an asset and SBOM are admitted, THE release pipeline SHALL create GitHub
    artifact attestations for their exact subject digests.
11. WHEN release assets are assembled, THE release pipeline SHALL attach them to a
    draft GitHub Release before public publication.
12. BEFORE the draft release is published, AN independent verification job SHALL
    download every asset and verify its checksum, SBOM binding, attestation, manifest,
    Feature 8 handoff, tag, and commit.
13. IF a required provenance or verification job is missing, skipped, neutral,
    cancelled, or failed, THEN THE release pipeline SHALL reject publication.
14. THE release pipeline SHALL not mark a required release gate `continue-on-error`.
15. WHEN every release gate passes, THE release pipeline SHALL publish the GitHub
    Release with immutable releases enabled.
16. IF the repository cannot enforce immutable releases and release-tag protection,
    THEN THE release pipeline SHALL reject public publication.
17. THE release documentation SHALL not claim SLSA level 3 unless the isolated builder,
    provenance, and independent verification satisfy the complete claimed contract.
18. THE release pipeline SHALL use the typed Rust admission model as the release-policy
    authority without adding an external policy engine merely for presentation.
19. IF GitHub-independent or offline signature verification becomes a requirement,
    THEN the release design SHALL explicitly add and verify a non-redundant Sigstore or
    Cosign bundle before publication.
20. THE Release_Manifest SHALL bind the exact Release_Capability_Report digest.

### Requirement 9.4: Apple Silicon and Initial Platform Distribution

**User Story:** As an Apple Silicon consumer, I want an honest platform bundle and
clear trust posture, so that engine and native CLI evidence are not conflated.

#### Acceptance Criteria

1. THE initial Feature 8 exact-engine verdict SHALL remain scoped to Linux/amd64.
2. WHEN Feature 9 claims Apple Silicon support, THE release pipeline SHALL require a
   separate exact-target verdict for the Linux/arm64 engine OCI payload.
3. WHEN Feature 9 assembles the Apple Silicon consumer bundle, THE Release_Manifest
   SHALL distinguish its Linux/arm64 engine OCI payload from its Darwin/arm64 CLI.
4. THE initial release pipeline SHALL not create a Windows SDK or engine artifact.
5. WHEN the target-compatible official Dagger Darwin/arm64 CLI is reused unchanged,
   THE Release_Manifest SHALL bind its upstream checksum and provenance.
6. WHEN the fork builds a Darwin/arm64 CLI, THE Release_Manifest SHALL record whether
   the binary is unsigned, signed, or signed and notarized.
7. IF a release claims that a fork-built Darwin/arm64 CLI is Gatekeeper-ready, THEN THE
   release pipeline SHALL require Developer ID signing and successful Apple
   notarization.
8. IF no authorized Apple Developer identity is available, THEN THE release pipeline
   SHALL label the fork-built Darwin/arm64 CLI as unsigned.
9. IF no authorized Apple Developer identity is available, THEN THE release pipeline
   SHALL avoid a Gatekeeper-ready claim.
10. THE release pipeline SHALL avoid applying Apple code-signing requirements to the
    Linux/arm64 engine OCI payload.
11. THE release documentation SHALL avoid presenting GitHub or Sigstore attestation as
    a substitute for Apple Developer ID signing.

### Requirement 9.5: Humble Evidence-Backed Capability Report

**User Story:** As a Rust SDK adopter, I want an honest account of what the release can
do and what remains, so that I can evaluate it without marketing inflation or an
unfair comparison.

#### Acceptance Criteria

1. WHEN Feature 9 publishes the release, THE GitHub Release and versioned documentation
   SHALL include one Release_Capability_Report.
2. THE Release_Capability_Report SHALL bind the Target_Revision, Subject_Revision,
   Rust_SDK_Release_Tag, Atomic_Signoff_Verdict, and Completeness_Ledger digest.
3. WHEN the report compares the Rust SDK with the Definitive_Go_SDK, THE comparison
   SHALL organize claims by observable capability and behaviour rather than Go syntax,
   package layout, or implementation mechanism.
4. WHEN the report describes conformance, THE report SHALL render the exact ledger
   totals and status partition from admitted evidence.
5. WHEN the report renders `Inapplicable`, THE report SHALL distinguish justified
   non-Rust obligations from implemented Rust behaviour.
6. WHEN the report compares with the Existing_Rust_SDK_Baseline, THE comparison SHALL
   acknowledge inherited foundations and describe additions or changes factually.
7. THE Existing_Rust_SDK_Baseline comparison SHALL avoid disparaging, competitive, or
   triumphalist language.
8. THE Release_Capability_Report SHALL exclude unmerged proposals and pull-request
   implementations from its comparison baselines.
9. THE Release_Capability_Report SHALL avoid mentioning the historical Rust module
   proposal as a competing SDK version.
10. WHEN a capability claim is made, THE report SHALL link it to the relevant ledger,
    assertion, case, or documented Idiomatic_Equivalence evidence.
11. WHEN limitations, residual `Missing` or `Partial` rows, unsupported platforms, or
    qualified evidence remain, THE report SHALL present them prominently and without
    euphemism.
12. THE Release_Capability_Report SHALL avoid claiming source parity, universal
    superiority, or completeness beyond the admitted verdict's exact target and
    platform scope.
13. WHEN ledger evidence changes, THE release pipeline SHALL regenerate the report's
    counts and evidence links reproducibly rather than hand-editing favourable totals.

## Iteration and Feedback Notes

- Requirements approval is the consent gate before any child design is authored.
- Child specs may split delivery into smaller reviewable pull requests, but each child
  must retain traceability to its feature and numbered acceptance criteria here.
- PR #12229 should be re-evaluated during Features 5 and 6 for reusable tests, failure
  discoveries, and authoring ergonomics; its architecture remains subject to the same
  source-of-truth order as new work.
- The Completeness_Ledger is the first implementation artifact because it turns later
  scope decisions and the final stable-release claim into reviewable evidence.
