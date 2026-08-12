# Requirements Document: Rust SDK Standalone Client Generation

## Introduction

Feature 7 turns the bounded `GenerateClient` seam delivered by Feature 5 into a
complete, idiomatic Rust client experience. A user must be able to initialize a Rust
client in a Dagger workspace, bind it to one local or pinned remote module, regenerate
it safely, use the complete Core API and the selected module's public API through
normal Cargo workflows, and update generated artifacts without sacrificing authored
files.

The Dagger engine at commit `25300124ca110612edc09c43f89cb5fad6028170` (the
Target_Revision) is authoritative for workspace client records, initialization,
module-source resolution, the `ClientGenerator` ABI, generator scoping, and the
Client_Visible_Schema. The generated Go client and Go generator at that revision, plus
`github.com/dagger/dagger-go-sdk` commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5`, define observable client and generation
behaviour where the engine contract does not settle it. Go modules, packages, mutable
overlay implementation, and global client helpers are evidence rather than a Rust
project design.

Rust owns the public project and API shape. Generated projects use Cargo, the exact
engine-selected `dagger-sdk` dependency, Rust 1.97.1, edition 2024, explicit ownership
metadata, typed async operations, and the session and error model already delivered by
Features 2–4. The generated client reuses the public Core_Schema bindings from
`dagger-sdk`; it does not copy those bindings into a second incompatible runtime.

The target engine makes one especially important distinction. Its executable
client-schema path installs the selected module under a namespaced `Query` field and
deliberately excludes that module's transitive dependencies. Feature 7 therefore uses
**dependency-bound client** to mean an independently generated client whose selected
module is itself a local or pinned remote dependency. It does not merge a selected
module's dependency graph into one generated client. The older prose in
`core/sdk.go:42-47` saying otherwise is contradicted by
`core/schema/modulesource.go:3804-3842` and its exact integration test at
`core/integration/generators_test.go:1236-1266`; the executable schema contract and
test govern this specification.

Feature 7 depends on Feature 1's completeness contract, Feature 2's client ownership,
Feature 3's transport and reliability, Feature 4's exact Core projection, Feature 5's
engine operation and workspace seams, and Feature 6's module TypeDef surface. Feature
8 owns cross-platform and complete engine-backed conformance, including the bounded
exact-target SDK sign-off. Feature 9 owns publication, release migration, and stable
release presentation.

Every Feature 7 implementation checkpoint is engine-free and Rust-first. Production
schema composition, project reconciliation, formatting, compilation, query
construction, workspace selection, diagnostics, and evidence admission must be
exercisable directly through Rust fixtures. Checked Core artifacts are reused unless
an owning input digest changes. A Dagger engine is reserved for final SDK sign-off
unless a direct model is proven insufficient and the exception is separately recorded
and approved.

## Glossary

- **Authored_File:** A file or manifest entry not proven to be owned by the current
  Generated_Client_Manifest.
- **Bound_Module:** The single local or pinned remote module selected by one
  Workspace_Client_Record.
- **Capability_Scope:** The exact Feature 1 Capability_ID set owned or consumed by
  Feature 7, including its target, evidence domain, and terminal-status policy.
- **Client_Initializer:** The Rust SDK implementation of the target engine's
  `initClient(ws, path, module, ...) -> Changeset` contract.
- **Client_Visible_Schema:** The engine-supplied introspection schema containing the
  complete client-visible Core_Schema and, where the Bound_Module has a runtime, that
  one module installed under its namespaced `Query` field.
- **Core_Client_Surface:** The exact public Core_Schema bindings supplied by the
  selected `dagger-sdk` dependency and reachable from a generated client.
- **Dependency_Bound_Client:** A Standalone_Client_Project whose Bound_Module is a
  selected local or pinned remote module dependency; it is still bound to exactly one
  module.
- **Exact_Target:** The Target_Descriptor selected by
  `sdk/rust/completeness/target.json`, including Target_Revision, engine version
  `v1.0.0-beta.10`, Rust SDK version `1.0.0-beta.10`, Rust 1.97.1, and edition 2024.
- **Feature_7_Local_Checkpoint:** Any implementation checkpoint, feature-end check, or
  local regression command used to claim Feature 7 progress or closure.
- **Generated_Client_Manifest:** The canonical record of a generated client's target,
  Bound_Module, Client_Visible_Schema, SDK dependency, owned paths, semantic bindings,
  artifact digests, and generator identity.
- **Generated_Module_Surface:** The typed Rust bindings and root integration generated
  for the Bound_Module coordinates in the Client_Visible_Schema.
- **Implementation_Closure:** The boundary at which production Feature 7 logic,
  engine-free client fixtures, scoped hygiene, security, and completeness checks pass
  without constructing or invoking a Dagger engine.
- **Legacy_Client_Path:** The target-compatible ModuleSource client-generation path
  that invokes `ClientGenerator.GenerateClient` directly.
- **Modern_Workspace_Path:** The `dagger api client init` and workspace `GenerateClients`
  path driven by SDK-owned client records in `dagger.toml`.
- **Published_SDK_Dependency:** The exact registry version or immutable Git URL and
  full revision selected by the engine-packaged Rust SDK; never an ambient path or
  mutable branch.
- **SDK_Signoff:** The later bounded exact-target gate that consumes matching
  Implementation_Closure evidence and runs only the real-engine cases that direct
  Rust fixtures cannot prove.
- **Standalone_Client_Project:** A path-confined Cargo project containing a reusable
  Core_Client_Surface, one optional Generated_Module_Surface, and no module-runtime or
  dispatch entrypoint.
- **Target_Revision:** Dagger commit
  `25300124ca110612edc09c43f89cb5fad6028170`.
- **Workspace_Client_Record:** One engine-owned `SDKManagedClient` entry containing a
  workspace-relative path, one module reference, its resolved pin when applicable, and
  SDK-specific options.
- **Wire_Name:** The exact case-sensitive GraphQL name used in schema coordinates,
  selections, arguments, enum values, and response decoding.

## Target State

`dagger api client init rust <path> <module>` records one Rust-owned client in the
workspace, calls the Rust Client_Initializer, and—unless generation is disabled—runs
generation for only that newly initialized path. `--no-generate` leaves a valid,
documented Cargo scaffold without pretending that bindings exist. Initialization can
adopt a compatible existing Cargo project, but it never replaces unrelated manifest
entries or Authored_Files.

Each Workspace_Client_Record resolves its Bound_Module against the workspace that owns
the record. Local references remain workspace-relative. Remote references are bound to
their resolved immutable pin. Generation below a workspace current directory selects
only managed clients at or below that directory, orders them canonically, and keeps
their module sources, schema identities, output roots, and changesets isolated.

The Client_Visible_Schema contains every target-visible Core coordinate, including
core types hidden from module-authoring schemas. If the Bound_Module has a runtime, it
also contains that module's complete public TypeDef closure and one namespaced root
field on `Query`. The module's own dependencies are absent. A caller wanting another
module generates another client bound to that module.

The Standalone_Client_Project reuses `dagger-sdk` for the Core_Client_Surface,
transport, connection ownership, telemetry, errors, scalar codecs, IDs, and query
execution. It generates only the additional module-owned bindings and the smallest
Rust-native integration needed to reach the namespaced module root. Module names,
types, fields, arguments, optionality, enums, interfaces, inputs, IDs, documentation,
deprecations, and query semantics remain faithful to the supplied schema while public
ownership and namespacing remain idiomatic Rust.

Generation is deterministic and transactional. The Generated_Client_Manifest is the
only authority for replacement or deletion. Repeated generation with identical inputs
is byte-identical; changed schemas remove obsolete generated artifacts; user files and
unknown manifest entries survive; collisions, malformed manifests, path escapes,
symlinks, stale targets, or ambiguous ownership fail before publication.

The generated project composes with ordinary Cargo commands and a standard Rust async
runtime under the documented `dagger-sdk` runtime policy. Engine-free fixtures compile
the exact candidate, construct representative Core and module queries through a
recording transport, and verify the emitted GraphQL contract. SDK_Signoff later proves
workspace initialization, exact engine schema delivery, one local client, one pinned
remote client, regeneration, and one real query without broadening local checkpoints.

Feature 7 does not change module authoring or dispatch, duplicate Core bindings,
publish a crate, run a platform matrix, claim transitive module-dependency generation,
or treat a successful engine hook as proof of complete generated content.

## Evidence From Current Code

Repository citations use Target_Revision unless another revision is stated. Current
Rust citations describe `main` after Feature 6 at commit
`341515e2ec2f91da386c69d7c6371ec588c914fb`.

- **Engine client ABI:** `core/sdk.go:13-81` defines standalone client generation,
  finite required host files, the `modSource`, `introspectionJSON`, and `outputDir`
  inputs, directory output, and the published-library expectation.
- **Engine initialization ABI:** `core/sdk.go:113-140` defines
  `initClient(ws, path, module, ...) -> Changeset` as the ClientInitializer contract.
- **Workspace record contract:** `core/workspace/config.go:58-93` defines SDK-managed
  clients as `path`, `module`, optional `pin`, and options under the installed SDK's
  `as-sdk` role.
- **Initialization behaviour:** `core/schema/workspace_client.go:26-155` validates path,
  SDK, and module; resolves the target; records its pin; calls ClientInitializer;
  creates the client directory; and returns a scope naming only the new client.
  `core/integration/generators_test.go:623-661` proves automatic scoped generation and
  `--no-generate` scaffolding.
- **Client-source binding:** `core/current_module_as_sdk.go:102-220` binds each client
  record to the workspace used for SDK discovery so local module resolution survives
  persistence and reload.
- **Client schema contract:** `core/schema/modulesource.go:3804-3842` starts from the
  complete default Core schema, installs only the Bound_Module as a normal namespaced
  module, and excludes transitive dependencies. The integration test at
  `core/integration/generators_test.go:1236-1266` proves visibility of `Host` and
  `EngineCache`, presence of `Query.<module>`, and absence of promoted module
  functions.
- **Workspace generation behaviour:** `core/integration/generators_test.go:319-428`
  proves SDK-owned client discovery, local and remote records, pin visibility,
  workspace-bound local resolution, list output, and generated paths.
- **Definitive Go project evidence:** `cmd/codegen/generator/go/generate_client.go` at
  Target_Revision creates a separate client module, pins the Dagger library to the
  engine version, preserves an existing manifest semantically, avoids an unreleased
  dependency network resolution during generation, and confines generated source to
  the requested client directory. Cargo shape is Rust-owned.
- **Definitive client lifecycle anchor:** `dagger.gen.go:15666` at the
  Definitive_Go_SDK revision exposes `Workspace.WithInitClient`; Feature 1 records it
  as `behavior/go-client/init-client-lifecycle`.
- **Current Feature 5 workspace seam:** `sdk/rust/runtime/main.go:93-143` discovers
  Rust-managed clients, filters them by workspace current directory, resolves each
  module source and Client_Visible_Schema, and dispatches `generate-client` into the
  production Rust operation runner.
- **Current Feature 5 direct hook:** `sdk/rust/runtime/main.go:293-329` exposes finite
  required-file metadata and a confined `GenerateClient` implementation. There is no
  Rust `InitClient` method, so `dagger api client init rust` cannot yet complete its
  ClientInitializer contract.
- **Current bounded renderer:**
  `sdk/rust/crates/dagger-codegen/src/engine/client.rs:1-81` emits a fixed package name,
  `Cargo.toml`, `src/lib.rs`, and operation-scoped bindings under
  `src/dagger_generated`, but labels itself an engine-hook baseline and does not prove
  final typed module-root composition, existing-project adoption, regeneration, or
  usability.
- **Current finite-input policy:**
  `sdk/rust/crates/dagger-codegen/src/engine/metadata.rs:11-88` validates canonical
  required-host-file metadata; the checked baseline currently requires no additional
  ignored host files.
- **Current schema compiler:**
  `sdk/rust/crates/dagger-codegen/src/engine/visible.rs` validates the complete supplied
  schema against the exact Core manifest and classifies operation-scoped extension
  coordinates. `engine/render.rs` renders extension-owned types and a semantic binding
  catalog but cannot add inherent methods to the Core `dagger_sdk::Query` type; Feature
  7 must supply an idiomatic composition boundary rather than shadowing that type.
- **Current public runtime:** `sdk/rust/crates/dagger-sdk/**` supplies the checked Core
  bindings, Shared_Session, Query_Builder, connection lifecycle, typed errors,
  transport, and observability reused by generated clients.
- **Completeness correction:** the current ledger assigns two rows to Feature 7:
  `behavior/go-client/init-client-lifecycle` is Missing and the pinned Go
  `TestProvision` row is Partial. `sdk/go/provision_test.go:28-145` proves concurrent
  CLI cache reuse and belongs to Feature 3, whose requirements already ground that
  behaviour; it is not standalone-client generation evidence.

## Completeness Contract Policy

### Existing Scope and Ownership Correction

Feature 7 retains one existing authority capability:
`behavior/go-client/init-client-lifecycle`, currently Missing with fingerprint
`sha256:1dfbf33549038de9fd9fbac8a12574d88658764e0c9732cd5a7996d14a3beb37`.

The current Feature 7 assignment for
`behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2550rovision`
is corrected to Feature 3. Its fingerprint is
`sha256:42cf3a1fb160841bd3237cbf44dd394c9fff5d661a9361b44a518b34e1bde26d`.
This correction changes ownership only; it does not change status or evidence.

Feature 5 continues to own the ClientGenerator hook, operation dispatch, target
identity, output confinement, and immutable Published_SDK_Dependency input. Feature 7
owns the ClientInitializer behaviour and the generated project's semantic contents,
Cargo integration, regeneration, and usability. Hook evidence cannot close content
capabilities.

### Rust Policy Capabilities Added by Feature 7

Feature 7 adds these stable `rust-policy` capabilities:

```text
policy/rust-policy/client-capability-scope
policy/rust-policy/client-initialization
policy/rust-policy/client-scoped-initial-generation
policy/rust-policy/client-workspace-record
policy/rust-policy/client-pinned-module-resolution
policy/rust-policy/client-single-bound-module
policy/rust-policy/client-transitive-dependency-exclusion
policy/rust-policy/client-visible-schema-closure
policy/rust-policy/client-core-runtime-reuse
policy/rust-policy/client-module-root-composition
policy/rust-policy/client-module-surface-closure
policy/rust-policy/client-rust-namespace-isolation
policy/rust-policy/client-cargo-project-adoption
policy/rust-policy/client-immutable-sdk-dependency
policy/rust-policy/client-generated-ownership-manifest
policy/rust-policy/client-user-file-preservation
policy/rust-policy/client-obsolete-artifact-removal
policy/rust-policy/client-deterministic-regeneration
policy/rust-policy/client-workspace-cwd-scoping
policy/rust-policy/client-multi-client-isolation
policy/rust-policy/client-query-usability
policy/rust-policy/client-diagnostic-and-secret-safety
policy/rust-policy/client-engine-free-local-checkpoint
policy/rust-policy/client-exact-engine-signoff-boundary
```

The Feature 7 mapping artifact connects every retained and added capability to one
requirement, implementation subject, allowed terminal status, and finite evidence
domain. Added policy rows supplement rather than replace engine or Definitive_Go_SDK
authority rows.

### Authority and Evidence Boundary

| Claim | Authority | Minimum implementation evidence | Engine evidence policy |
|---|---|---|---|
| Rust supports client initialization | Engine ClientInitializer and workspace contracts | Direct adapter fixture plus path-confined scaffold reconciliation | Exact CLI path at SDK_Signoff |
| A supplied schema is client-visible | Engine client-schema construction and exact target manifest | Canonical schema fixture proving Core closure, one namespaced module, and dependency exclusion | Exact delivered schema at SDK_Signoff |
| Generated Core behaviour is complete | Feature 4 Core bindings plus Features 2–3 runtime | Reuse identity plus existing Core property evidence | Not re-proved by copying symbols |
| Generated module behaviour is complete | Bound_Module TypeDefs and Rust policy | Exhaustive semantic catalog, typed compile fixtures, and recording-transport query properties | One local and one pinned remote client at SDK_Signoff |
| Existing Cargo state is preserved | Rust/Cargo policy plus target generator preservation behaviour | Permuted manifest and filesystem reconciliation properties | No engine required |
| A client query is correct | Client_Visible_Schema Wire_Names plus public Rust runtime | Production generated API against a recording transport | One real representative query at SDK_Signoff |
| Feature 7 is implementation-complete | This specification and Rust repository policy | Complete engine-free client harness plus scoped hygiene/security | No engine is started |
| Feature 7 is release-signed-off | Exact_Target engine plus admitted closure evidence | All local evidence plus the bounded exact-engine client cases | Required only at SDK_Signoff |

## Contract Policy

### Workspace Client Record

| Field | Target policy | Error if invalid | Persistence or side-effect impact |
|---|---|---|---|
| `path` | Normalized workspace-relative client root below the workspace | Reject empty, absolute, escaping, symlink-escaping, or conflicting paths | Engine persists the normalized path; SDK writes only beneath it |
| `module` | One local workspace-relative or canonical remote module reference | Reject empty, escaping local, unresolved, or non-module references | Engine persists the user-facing ref; generator resolves exactly one Bound_Module |
| `pin` | Exact resolved pin for a remote Bound_Module; empty for mutable local source | Reject a remote record whose declared pin disagrees with resolution | Engine persists the pin; schema and manifest identity include it |
| `options` | Closed Rust client-initialization options declared by this feature | Reject unknown, duplicate, or invalid values before scaffold publication | Persist only engine-supported options; never store credentials |

### ClientInitializer Request

| Input | Target policy | Error if invalid | Persistence or side-effect impact |
|---|---|---|---|
| `ws` | Required workspace bound to the engine-owned client record | Reject nil or inaccessible workspace | Read existing project state; return an SDK-owned Changeset only |
| `path` | Same normalized root recorded by the engine | Reject any path outside the workspace or incompatible existing package | Scaffold only the selected client root |
| `module` | Same user-facing module ref recorded by the engine | Reject a value inconsistent with the initialized record | May inform package naming and documentation; does not resolve a second module |
| SDK args | Closed, versioned Rust initialization arguments | Reject unknown keys and invalid combinations | Affect only documented SDK-owned scaffold choices |

### GenerateClient Request

| Input | Target policy | Error if invalid | Persistence or side-effect impact |
|---|---|---|---|
| `modSource` | Exact implementation-scoped Bound_Module identity | Reject missing identity, invalid source path, or target mismatch | Source digest enters Generated_Client_Manifest |
| `introspectionJSON` | Complete Client_Visible_Schema for Exact_Target | Reject malformed schema, missing/changed Core coordinates, multiple module roots, or unexpected dependency coordinates | Schema digest and semantic catalog enter the manifest |
| `outputDir` | Normalized requested client root | Reject absolute, escaping, symlinked, or overlapping ownership | Candidate writes remain confined beneath the root |
| Published SDK dependency | Exact registry version or immutable Git URL and full revision | Reject path, wildcard, tag-only, or branch-only sources | Exact descriptor is emitted into Cargo metadata and manifest |

### Client-Visible Schema Surface

| Surface | Required projection | Invalid state | Runtime impact |
|---|---|---|---|
| Core named types and fields | Reuse the exact public `dagger-sdk` Core_Client_Surface | Missing or incompatible target coordinate | Generation fails before publication |
| Core types hidden from module schemas | Retain them because client schemas expose the complete Core contract | Missing target-visible type | Generation fails as schema drift |
| Bound module root field | Expose one typed namespaced path from the generated client root | Missing, promoted, duplicate, or ambiguous root | Query selects exact `Query.<module>` Wire_Name |
| Bound module objects | Generate typed lazy handles with exact fields and arguments | Unsupported wrapper, collision, or unresolved reference | Reuse the shared session and query builder |
| Bound module interfaces | Generate an idiomatic trait and typed client handle | Incomplete implementation relation or collision | Preserve interface selection and expected-type semantics |
| Bound module enums and scalars | Generate exact wire codecs under the module namespace | Duplicate/unknown member or unsupported scalar policy | Preserve exact JSON and GraphQL values |
| Bound module input objects | Generate owned typed inputs with omission distinct from explicit values | Invalid recursion, wrapper, or field collision | Encode exact argument JSON |
| Bound module documentation and stability metadata | Preserve semantic docs, deprecation, and experimental notices | Invalid metadata shape | Documentation only |
| Bound module transitive dependencies | Exclude them from this client | Any dependency-owned coordinate appears | Require a separate Dependency_Bound_Client |

### Generated Project and Ownership

| Artifact class | New client root | Existing compatible client root | Invalid or conflicting state |
|---|---|---|---|
| `Cargo.toml` | Create a non-publishable package with Exact_Target edition, Rust version, metadata, and Published_SDK_Dependency | Semantically update SDK-owned keys while preserving unrelated package, dependency, feature, workspace, and profile entries | Return a typed manifest or dependency conflict |
| User-facing library root | Create the documented stable import boundary | Preserve authored contents and apply only a previously declared SDK-owned edit | Return an ownership conflict rather than replace unknown content |
| Generated bindings | Publish the complete Core integration and Bound_Module surface | Replace only paths owned by the previous compatible manifest | Reject unknown, escaped, or symlinked collisions |
| Generated client manifest | Create exact target, source, schema, dependency, binding, path, and artifact identities | Validate before using it as ownership authority | Reject malformed, stale, incomplete, or target-mismatched manifests |
| `Cargo.lock` | Leave dependency resolution to the consumer unless a reviewed generated-lock policy is selected | Preserve caller-owned resolution byte-for-byte | Never perform hidden network resolution or silent lockfile rewrite |
| Toolchain file | Reuse a compatible enclosing policy or create the exact approved standalone declaration | Preserve a compatible caller-owned declaration | Return a typed toolchain conflict |
| Documentation or quickstart | Create concise compile-and-query guidance with generated ownership markers | Refresh only the SDK-owned section | Preserve unrelated prose and reject ambiguous ownership |
| Obsolete generated paths | None on first generation | Remove only paths proved obsolete by the previous manifest | Never infer ownership from filename or directory alone |

### Local Checkpoint and Sign-off Boundary

| Activity | Feature 7 local policy | Deferred SDK sign-off policy |
|---|---|---|
| Schema tests | Direct production compiler over Core-only, local-module, and dependency-bound fixtures | Confirm exact engine supplies the same schema classes |
| Project tests | Temporary Cargo roots and semantic filesystem reconciliation | Confirm engine Changesets land at the intended workspace roots |
| Compile and query tests | Exact generated candidate plus local package resolver and recording transport | Run one real Core and Bound_Module query path |
| Regeneration | Invoke only when target, schema, module, dependency, generator, or ownership input changes | Confirm one update through the real CLI path |
| Workspace selection | Pure Rust workspace records and cwd fixtures | Confirm one multi-client workspace selection case |
| Rust hygiene | Scoped fmt, locked tests, warning-denied clippy/rustdoc, package policy, and security checks | Consume admitted closure evidence without replaying it |
| Dagger engine | Not constructed, started, or invoked | Reuse the one Exact_Target artifact and engine service |
| Other SDKs | Not built, tested, generated, or distributed | Build only the engine-required Go runtime content once |

## Requirements

### Requirement 1: Exact Capability Scope and Ground-Truth Accountability

**User Story:** As a release reviewer, I want every client-generation capability owned
and evidenced precisely, so that a passing engine hook cannot conceal an unusable Rust
client.

#### Acceptance Criteria

1. THE Feature 7 scope SHALL retain `behavior/go-client/init-client-lifecycle` with its
   exact fingerprint.
2. THE ownership correction SHALL route the pinned `TestProvision` capability to
   Feature 3 without changing its status.
3. THE Feature 7 scope SHALL add every declared Rust policy capability without
   replacing an existing authority capability.
4. THE Feature 7 mapping SHALL bind each capability to one implementation subject.
5. THE Feature 7 mapping SHALL bind each capability to one non-empty evidence-domain
   set.
6. THE Feature 7 mapping SHALL declare one allowed terminal status for each capability.
7. THE Feature 7 mapping SHALL preserve Feature 5 ownership of the engine hook and
   operation boundary.
8. IF hook evidence lacks generated-content evidence, THEN THE completeness registry
   SHALL retain the content capability as blocking.
9. WHEN a capability status changes, THE evidence SHALL enumerate the exact proved
   Capability_ID set.
10. IF evidence is stale, skipped, failed, incomplete, or target-incompatible, THEN THE
    completeness registry SHALL reject it.
11. THE rendered completeness report SHALL distinguish client initialization,
    generated content, local implementation closure, and SDK sign-off.
12. THE umbrella correction SHALL define dependency generation as independently bound
    clients rather than one transitive dependency graph.

### Requirement 2: Path-Confined Client Initialization

**User Story:** As a Rust application author, I want `dagger api client init rust` to
create or adopt a Cargo client safely, so that initialization fits an ordinary
workspace without overwriting my project.

#### Acceptance Criteria

1. WHEN the engine queries the Rust SDK's client-initialization capability, THE Rust
   adapter SHALL expose Client_Initializer.
2. WHEN Client_Initializer receives a valid new client root, THE initializer SHALL
   return a Changeset containing the documented Rust client scaffold.
3. THE Client_Initializer SHALL leave the engine-owned workspace client record to the
   engine.
4. WHEN the client root contains a compatible Cargo project, THE initializer SHALL
   preserve every unrelated manifest entry.
5. WHEN the client root contains Authored_Files, THE initializer SHALL preserve their
   bytes unless a reviewed SDK-owned edit covers them.
6. IF a required scaffold path contains unknown incompatible content, THEN THE
   initializer SHALL return a typed ownership conflict.
7. IF an SDK initialization argument is unknown, THEN THE initializer SHALL return a
   typed argument diagnostic.
8. IF the requested path escapes the workspace, THEN THE initializer SHALL reject the
   request before producing a Changeset.
9. WHEN initialization runs with generation enabled, THE workspace SHALL generate only
   the newly initialized client scope.
10. WHEN initialization runs with `--no-generate`, THE workspace SHALL omit generated
    bindings.
11. WHEN initialization runs with `--no-generate`, THE initializer SHALL leave a
    documented scaffold that explains the missing generation step.
12. WHEN initialization fails, THE initializer SHALL avoid publishing a partial
    scaffold.
13. THE Client_Initializer SHALL avoid embedding the session token, registry
    credentials, host paths, or other secrets in its output.

### Requirement 3: Exact Bound-Module and Client-Schema Semantics

**User Story:** As a generated-client user, I want its schema identity to match one
selected module and my engine, so that types cannot silently come from another module,
revision, or dependency graph.

#### Acceptance Criteria

1. WHEN a Workspace_Client_Record names a local module, THE generator SHALL resolve it
   against the record's bound workspace.
2. WHEN a Workspace_Client_Record names a remote module, THE generator SHALL resolve
   the recorded immutable pin.
3. IF a remote module resolves to a different pin, THEN THE generator SHALL return a
   target-identity diagnostic.
4. THE generator SHALL bind each Standalone_Client_Project to exactly one
   Bound_Module.
5. THE generator SHALL validate every target-visible Core coordinate in the
   Client_Visible_Schema.
6. THE generator SHALL retain Core types exposed only to client schemas.
7. WHERE the Bound_Module has a runtime, THE Client_Visible_Schema SHALL expose one
   namespaced module field on `Query`.
8. WHERE the Bound_Module has a runtime, THE Client_Visible_Schema SHALL contain its
   complete public TypeDef closure.
9. THE Client_Visible_Schema SHALL exclude the Bound_Module's transitive dependencies.
10. IF a transitive dependency coordinate appears in the supplied schema, THEN THE
    generator SHALL return a schema-scope diagnostic.
11. IF a module function is promoted directly onto the Query root, THEN THE generator
    SHALL return a schema-scope diagnostic.
12. WHEN the supplied schema contains no runtime-backed Bound_Module, THE generator
    SHALL produce a Core-only client surface.
13. THE Generated_Client_Manifest SHALL bind the target, module source, pin, schema,
    and SDK dependency identities.

### Requirement 4: Idiomatic Core and Module API Composition

**User Story:** As a Rust consumer, I want one coherent typed client for Core and my
selected module, so that module access feels native without introducing a parallel
runtime.

#### Acceptance Criteria

1. THE Standalone_Client_Project SHALL reuse `dagger-sdk` for every Core_Client_Surface
   symbol.
2. THE Standalone_Client_Project SHALL reuse `dagger-sdk` for connection ownership and
   query execution.
3. THE generated project SHALL avoid emitting a duplicate Core object, interface,
   enum, scalar, or input type.
4. WHERE a Bound_Module is present, THE generated project SHALL expose one typed
   module-root accessor selecting its exact Query Wire_Name.
5. THE Generated_Module_Surface SHALL expose every public Bound_Module object field.
6. THE Generated_Module_Surface SHALL expose every public Bound_Module interface
   operation.
7. THE Generated_Module_Surface SHALL expose every public Bound_Module enum member.
8. THE Generated_Module_Surface SHALL expose every public Bound_Module input field.
9. THE Generated_Module_Surface SHALL preserve every GraphQL wrapper and nullability
   distinction.
10. THE Generated_Module_Surface SHALL preserve omission separately from explicit
    false, zero, empty-string, empty-list, and null values.
11. THE Generated_Module_Surface SHALL preserve exact field and argument Wire_Names in
    every query.
12. THE Generated_Module_Surface SHALL preserve object and interface ID re-entry
    semantics through the shared runtime.
13. THE generated project SHALL place Bound_Module symbols in a deterministic Rust
    namespace distinct from the Core_Client_Surface.
14. IF two generated public names collide within that namespace, THEN THE generator
    SHALL return a coordinate-bearing collision diagnostic.
15. THE generated project SHALL avoid process-global mutable client or query state.
16. THE generated project SHALL expose async operations through the documented
    `dagger-sdk` runtime policy.
17. THE generated public API SHALL carry semantic rustdoc for every public item whose
    contract is not evident from its type.

### Requirement 5: Cargo-Native Project and Dependency Policy

**User Story:** As a Rust maintainer, I want generated clients to behave like normal
Cargo projects, so that they can be checked, documented, and consumed without
repository-specific patching.

#### Acceptance Criteria

1. WHEN no client manifest exists, THE generator SHALL create a valid Cargo package
   manifest.
2. THE generated manifest SHALL declare edition 2024.
3. THE generated manifest SHALL declare Rust 1.97.1 as its minimum version.
4. THE generated manifest SHALL mark the generated client package as non-publishable.
5. THE generated manifest SHALL select the exact Published_SDK_Dependency.
6. IF the SDK dependency is a registry dependency, THEN THE manifest SHALL use its
   exact version requirement.
7. IF the SDK dependency is a Git dependency, THEN THE manifest SHALL use its full
   immutable revision.
8. THE generated manifest SHALL reject an ambient path dependency for `dagger-sdk`.
9. THE generated manifest SHALL reject a wildcard or branch-only dependency for
   `dagger-sdk`.
10. WHEN adopting an existing manifest, THE generator SHALL preserve unrelated
    dependencies and features.
11. WHEN adopting an existing manifest, THE generator SHALL preserve unrelated
    workspace and profile configuration.
12. IF an existing `dagger-sdk` dependency conflicts with the Published_SDK_Dependency,
    THEN THE generator SHALL return a typed dependency conflict.
13. THE generated package name SHALL be deterministic and valid under Cargo naming
    rules.
14. THE generation operation SHALL avoid network dependency resolution.
15. THE generation operation SHALL avoid silently rewriting a caller-owned
    `Cargo.lock`.
16. IF no compatible toolchain policy applies, THEN THE initializer SHALL create the
    exact standalone toolchain declaration.
17. IF an enclosing toolchain policy is incompatible, THEN THE initializer SHALL
    return a typed toolchain conflict.

### Requirement 6: Transactional Ownership and Regeneration

**User Story:** As a client maintainer, I want regeneration to update only generated
artifacts, so that schema evolution never deletes my work or leaves stale bindings.

#### Acceptance Criteria

1. THE Generated_Client_Manifest SHALL enumerate every generator-owned path.
2. THE Generated_Client_Manifest SHALL record a digest for every generator-owned
   artifact.
3. THE Generated_Client_Manifest SHALL record the semantic binding catalog.
4. WHEN generation inputs are unchanged, THE generator SHALL emit byte-identical
   artifacts.
5. WHEN schema declaration order changes without semantic change, THE generator SHALL
   emit byte-identical artifacts.
6. WHEN an owned binding changes, THE generator SHALL replace only artifacts owned by
   the previous compatible manifest.
7. WHEN an owned binding disappears, THE generator SHALL remove its obsolete owned
   artifacts.
8. WHEN an Authored_File shares the client root, THE generator SHALL preserve its
   bytes.
9. IF an unknown file occupies a required generated path, THEN THE generator SHALL
   return an ownership conflict.
10. IF the previous manifest is malformed, THEN THE generator SHALL return a manifest
    diagnostic before mutation.
11. IF the previous manifest targets another Dagger revision, THEN THE generator SHALL
    reject it as ownership authority.
12. IF a generated path is absolute, escaping, or symlink-escaping, THEN THE generator
    SHALL reject the candidate.
13. WHEN any generation phase fails, THE operation SHALL publish no partial candidate.
14. WHEN generation succeeds, THE operation SHALL publish artifacts and manifest as
    one logical transaction.
15. THE generator SHALL avoid inferring ownership solely from a filename or directory
    name.

### Requirement 7: Workspace Selection and Multi-Client Isolation

**User Story:** As a workspace maintainer, I want each managed Rust client generated
from its own record and module, so that nested or multi-client workspaces cannot leak
schemas or changes across outputs.

#### Acceptance Criteria

1. WHEN workspace generation starts, THE Rust SDK SHALL discover clients from the
   engine-owned SDK role data.
2. THE Rust SDK SHALL select only clients at or below the workspace current directory.
3. THE Rust SDK SHALL order selected clients canonically before generation.
4. THE Rust SDK SHALL resolve each selected client's Bound_Module independently.
5. THE Rust SDK SHALL derive each selected client's Client_Visible_Schema independently.
6. THE Rust SDK SHALL confine each selected client's output to its registered path.
7. THE Rust SDK SHALL keep each selected client's Generated_Client_Manifest distinct.
8. IF selected client roots overlap, THEN THE Rust SDK SHALL reject the generation set
   before rendering.
9. WHEN two clients bind the same module at different paths, THE Rust SDK SHALL produce
   independently owned outputs.
10. WHEN two clients bind different modules, THE Rust SDK SHALL avoid sharing
    module-owned schema coordinates between them.
11. WHEN one client fails, THE workspace generator SHALL avoid publishing a partial
    multi-client Changeset.
12. THE workspace generator SHALL avoid regenerating SDK-managed modules during a
    client-only operation.
13. THE workspace generator SHALL avoid entering an unrelated SDK's generator.
14. WHEN the Modern_Workspace_Path and Legacy_Client_Path receive equivalent inputs,
    THE Rust backend SHALL produce semantically equivalent client content.

### Requirement 8: Typed Diagnostics and Security Boundaries

**User Story:** As a Rust user, I want generation failures to be precise and safe, so
that I can repair my project without exposing credentials or guessing what was
overwritten.

#### Acceptance Criteria

1. THE Feature 7 diagnostic taxonomy SHALL assign a stable code to every declared
   failure class.
2. WHEN a schema failure has a coordinate, THE diagnostic SHALL include that exact
   coordinate.
3. WHEN a project failure has a path, THE diagnostic SHALL include a normalized
   client-relative path.
4. WHEN a manifest conflict names a key, THE diagnostic SHALL include its semantic
   TOML key path.
5. THE diagnostic renderer SHALL order multiple diagnostics deterministically.
6. THE diagnostic renderer SHALL sanitize terminal control characters.
7. THE diagnostic renderer SHALL redact session tokens and authorization values.
8. THE diagnostic renderer SHALL redact registry and Git credentials.
9. THE generated project SHALL contain no session token or authorization header.
10. THE generated project SHALL contain no absolute repository or developer-machine
    path.
11. THE generated project SHALL contain no ambient local SDK dependency.
12. THE generated library path SHALL contain no unsafe Rust.
13. IF a dependency source is unapproved, THEN THE generator SHALL reject it before
    publication.
14. IF required host-file metadata is non-canonical, THEN THE adapter SHALL reject it
    before mounting project content.

### Requirement 9: Engine-Free Generated-Client Usability

**User Story:** As a Rust application author, I want generated output proven through
ordinary Rust tools before engine sign-off, so that routine defects are found quickly
without an expensive build graph.

#### Acceptance Criteria

1. WHEN a Core-only client fixture is generated, THE candidate SHALL compile under the
   Exact_Target toolchain.
2. WHEN a local-module client fixture is generated, THE candidate SHALL compile under
   the Exact_Target toolchain.
3. WHEN a dependency-bound client fixture is generated, THE candidate SHALL compile
   under the Exact_Target toolchain.
4. THE generated candidate SHALL pass rustfmt checking.
5. THE generated candidate SHALL pass warning-denied Clippy for its supported target
   set.
6. THE generated candidate SHALL pass warning-denied rustdoc.
7. WHEN a generated Core query runs through a recording transport, THE request SHALL
   match the exact expected GraphQL operation.
8. WHEN a generated module query runs through a recording transport, THE request SHALL
   select the exact namespaced module root.
9. WHEN generated optional arguments are omitted, THE request SHALL omit their Wire_Names.
10. WHEN generated optional arguments carry explicit values, THE request SHALL encode
    those values without zero-value loss.
11. THE generated quickstart SHALL compile without editing generated code.
12. THE generated quickstart SHALL use the public connection and lifecycle API from
    `dagger-sdk`.
13. THE engine-free resolver fixture SHALL leave the generated dependency declaration
    unchanged.
14. THE generated-client harness SHALL exercise the production renderer and public
    runtime rather than a test-only client implementation.

### Requirement 10: Engine-Free Checkpoints and Deferred Exact-Target Sign-off

**User Story:** As a maintainer, I want fast Rust-first checkpoints separated from one
bounded engine sign-off, so that development remains efficient without weakening the
release claim.

#### Acceptance Criteria

1. THE Feature_7_Local_Checkpoint SHALL run without constructing a Dagger engine.
2. THE Feature_7_Local_Checkpoint SHALL run without invoking a Dagger module.
3. THE Feature_7_Local_Checkpoint SHALL run without building or testing another SDK.
4. THE Feature_7_Local_Checkpoint SHALL run without unscoped repository generation.
5. THE Feature_7_Local_Checkpoint SHALL reuse checked Core artifacts when their owning
   identity is unchanged.
6. WHEN an owning generation input changes, THE checkpoint SHALL regenerate only the
   affected Rust client fixture or checked artifact.
7. THE Feature_7_Local_Checkpoint SHALL record commands and elapsed phase timings.
8. THE Feature_7_Local_Checkpoint SHALL record the generated-asset reuse decision.
9. IF a proposed local check requires an engine, THEN the exception record SHALL prove
   why direct Rust fixtures are insufficient.
10. IF a proposed local check requires an engine, THEN the exception SHALL require
    explicit approval before execution.
11. WHEN Implementation_Closure is evaluated, THE closure report SHALL require every
    Feature 7 production, fixture, hygiene, security, and evidence gate.
12. WHEN SDK_Signoff is evaluated, THE sign-off SHALL consume matching
    Implementation_Closure evidence without replaying local checks.
13. THE SDK_Signoff inventory SHALL include one initialized local-module client.
14. THE SDK_Signoff inventory SHALL include one pinned remote dependency-bound client.
15. THE SDK_Signoff inventory SHALL include one regeneration after a schema change.
16. THE SDK_Signoff inventory SHALL include one representative Core query.
17. THE SDK_Signoff inventory SHALL include one representative namespaced module query.
18. THE SDK_Signoff SHALL reuse the umbrella's one exact-target artifact, engine
    service, and installed Rust baseline.
19. IF any required sign-off case is absent, stale, skipped, or failed, THEN THE final
    Rust SDK sign-off SHALL fail atomically.

## Out of Scope

- Generating a selected module's transitive dependency graph into one client.
- Redesigning Feature 2 client ownership or Feature 3 transport and provisioning.
- Reimplementing Core_Schema bindings already owned by Feature 4.
- Changing Feature 5's engine ABI, runtime container, or generic operation runner.
- Changing Feature 6 module authoring, TypeDef registration, or dispatch.
- Running the Feature 8 platform, cross-SDK, or complete engine conformance matrix.
- Publishing crates, synchronizing release versions, or presenting the stable release.
- Treating repository-relative dependency patches as valid generated output.
- Claiming Feature 7 SDK sign-off from engine-free Implementation_Closure alone.

## Iteration and Feedback Notes

- The umbrella Feature 7 wording that implies one client contains every transitive
  dependency surface must be refined to the engine's one-Bound_Module contract when
  these requirements are approved.
- The final Rust API composition mechanism is intentionally deferred to design. It must
  provide typed access to the namespaced module root without shadowing
  `dagger_sdk::Query`, duplicating the Core surface, or copying Go's package layout.
- `Cargo.lock` remains caller-owned in the requirements. If design research proves a
  generated standalone lockfile is necessary, that change requires explicit review
  because it introduces dependency resolution and update policy.
