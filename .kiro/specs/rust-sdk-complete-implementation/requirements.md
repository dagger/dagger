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
- **Existing_Session:** A Dagger engine session supplied through the standard session
  environment rather than started by the Rust process.
- **Generated_Bindings:** Rust source emitted from engine introspection for GraphQL
  objects, interfaces, enums, scalars, inputs, field arguments, and selections.
- **Go_Level:** Complete according to the Completeness_Ledger, not source-compatible
  with Go.
- **Idiomatic_Equivalence:** Rust API shape that preserves behaviour while following
  accepted Rust ownership, error, naming, async, and type-system conventions.
- **Module_Dispatch:** Decoding an engine function call, invoking the matching Rust
  function against module state, and returning either its value or structured error.
- **Module_SDK:** The code generator and runtime implementation used by the engine to
  develop and execute Dagger modules written in Rust.
- **Owned_Client:** A client handle whose session lifetime can outlive a single
  closure and whose shutdown semantics are explicit and deterministic.
- **Release_Gate:** A check that must pass before the Rust SDK can be presented as
  stable and Go-level complete.
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
crates and documentation under a coherent stability and compatibility policy.

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
  declares `dagger-codegen` publishable, requiring an explicit crate-graph policy.
- **Current security baseline:** the Rust workspace denies unsafe code in
  `sdk/rust/Cargo.toml`, has Cargo Deny policy in `sdk/rust/deny.toml`, and is covered
  by repository dependency and vulnerability automation. These controls are a
  foundation, not evidence of feature completeness.
- **Historical module evidence:** upstream pull request #12229, open and unmerged as
  of 2026-08-05, proposes Rust SDK registration, a Go-based module runtime,
  procedural-macro authoring, call dispatch, and nullable/Void fixes.

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
| Security gates | Strong baseline exists but must cover every new component | Feature 8 | Locked, denied, audited, and secret-safe checks pass |
| Crate publication graph | `dagger-codegen` policy conflicts with SDK-only publication | Feature 9 | Validated public/private graph and publish rehearsal |
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
3. WHEN a module has dependencies, THE Rust backend SHALL emit bindings for every
   selected dependency surface.
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
2. WHEN the client depends on a published Dagger crate, THE generated manifest SHALL
   select a version compatible with the Target_Revision.
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

1. WHEN a change affects client transport or CLI provisioning, THE CI matrix SHALL run
   the applicable tests on Linux, macOS, and Windows.
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

---

## Feature 9: Distribution, Documentation, and Stable Release

### Requirement 9.1: Coherent Crate and Version Release

**User Story:** As a Rust SDK consumer, I want a coherent published crate set, so that
Cargo can resolve every supported feature from crates.io at matching versions.

#### Acceptance Criteria

1. WHEN release architecture is finalized, THE workspace SHALL classify every crate as
   public and publishable or private and non-publishable.
2. WHEN a public crate depends on another workspace crate, THE release pipeline SHALL
   publish dependencies in a resolvable order.
3. WHEN a private crate is packaged, THE public crate graph SHALL remain buildable
   without that crate from crates.io.
4. WHEN a Rust SDK version is prepared, THE release automation SHALL update workspace,
   lockfile, embedded engine version, generated metadata, and documentation references
   consistently.
5. WHEN version-bearing files disagree, THE release pipeline SHALL fail before
   publication.
6. WHEN publication is rehearsed, THE release pipeline SHALL package every public crate
   from a clean source tree and compile the packaged result.
7. WHEN a stable crate is published, THE release pipeline SHALL verify its crates.io
   metadata and docs.rs build.
8. WHEN the declared MSRV or edition changes, THE release notes SHALL state the
   compatibility impact and migration path.

### Requirement 9.2: Stable Documentation and Adoption Readiness

**User Story:** As a Tokeira developer, I want trustworthy Rust SDK and module guides,
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
7. WHEN the release candidate is evaluated for Tokeira, THE acceptance suite SHALL run
   a representative Tokeira workflow through the published-package installation path.
8. WHEN every Release_Gate passes, THE project documentation SHALL identify the Rust
   SDK as Go-level complete for the declared Target_Revision.

## Iteration and Feedback Notes

- Requirements approval is the consent gate before any child design is authored.
- Child specs may split delivery into smaller reviewable pull requests, but each child
  must retain traceability to its feature and numbered acceptance criteria here.
- PR #12229 should be re-evaluated during Features 5 and 6 for reusable tests, failure
  discoveries, and authoring ergonomics; its architecture remains subject to the same
  source-of-truth order as new work.
- The Completeness_Ledger is the first implementation artifact because it turns later
  scope decisions and the final stable-release claim into reviewable evidence.
