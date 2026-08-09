# Requirements Document: Rust SDK Engine Integration

## Introduction

Feature 5 makes Rust a first-class Dagger engine SDK at the exact target selected by
`sdk/rust/completeness/target.json`. A user must be able to install the Rust SDK into a
workspace, initialize a Rust module, run engine-managed generation, and obtain a
container runtime through the same current engine contracts used by the other language
SDKs. The target is an engine-coherent Rust integration with deterministic provenance,
not a Rust spelling of Go's generator structs or a resurrection of the legacy
`dagger init --sdk rust` command.

The authoritative Dagger 1.0 workflow is `dagger sdk install rust` followed by
`dagger module init rust <name>`. Workspace initialization, SDK-owned changesets,
generation, and runtime selection are defined by `core/sdk.go`, `core/sdk/**`, and the
workspace integration tests at Dagger revision
`25300124ca110612edc09c43f89cb5fad6028170` (the Target_Revision). The four historical
backend operations in `cmd/codegen/generator/generator.go` at that revision remain
behavioural evidence for operation selection, overlays, post-generation work, and
repeatability. They do not require Rust to import the Go code generator or reproduce
its package layout.

Feature 5 depends on Feature 1's executable completeness contract and Feature 4's
fallible schema compiler and generated Core_Schema client. It consumes Feature 2's
owned session/query primitives and Feature 3's authenticated, observable transport.
It establishes the engine-facing seams that later work fills:

- Feature 6 owns Rust source discovery, authoring annotations or macros, TypeDef
  construction, user-function dispatch, state encoding, and the semantic contents of
  the generated runtime entrypoint;
- Feature 7 owns complete standalone Core, module, and dependency client projects,
  while Feature 5 owns the engine hook and lossless operation boundary used to request
  them;
- Feature 8 owns the full cross-platform and cross-SDK release matrix; and
- Feature 9 owns final crate publication, version synchronization, user migration, and
  stable-release presentation.

The engine contract, checked target, and Definitive_Go_SDK are peer authorities within
their declared scopes. Rust policy owns Cargo project shape, dependency sources,
toolchain selection, filesystem ownership, diagnostics, and security. A Go mechanism
is evidence only when it contributes observable semantics. Pull request #12229 is
historical evidence that Rust SDK resolution, a module runtime, and Rust-side dispatch
can be connected; its Go-authored runtime, repository path mounts, 2021-edition
template, and provisional procedural macros do not define this specification.

## Glossary

- **Bare_Rust_Reference:** The exact SDK reference `rust`, without an `@` suffix.
- **Builtin_Rust_SDK:** The Rust SDK implementation selected by the engine for a
  Bare_Rust_Reference and bound to that engine build's provenance.
- **Capability_Scope:** The exact Feature 1 Capability_ID set owned or consumed by this
  feature, including its authority, target, evidence domain, and status policy.
- **Cargo_Project:** The package or workspace member selected as the Rust module's
  source root, including its manifest, lockfile, toolchain declaration, authored source,
  and generated artifacts.
- **Checked_Generated_Mode:** The current `dagger-module.toml` contract in which
  generated files are committed and runtime construction must not regenerate them.
- **Codegen_Operation:** One of Generate_Library, Generate_Module, Generate_Client, or
  Generate_Entrypoint.
- **Core_Binding_Compiler:** Feature 4's pure, fallible
  `dagger-codegen` schema-to-Rust projection and rendering pipeline.
- **Engine_Backend:** The engine-side adapter that resolves Rust capabilities and
  invokes the Rust generator or runtime without exposing Go implementation types to
  Rust users.
- **Engine_SDK_Contract:** The SDK, ModuleInitializer, ClientInitializer,
  RuntimeTarget, CodeGenerator, ClientGenerator, ModuleRuntime, Runtime, and ModuleTypes
  interfaces in `core/sdk.go` at the Target_Revision.
- **Engine_Source_Descriptor:** Immutable build metadata identifying the repository,
  full revision, engine version, Rust SDK version, and dependency source selected for
  the Builtin_Rust_SDK.
- **Existing_Cargo_Project:** A Cargo_Project containing caller-authored files before
  Rust SDK initialization begins.
- **Generate_Client:** The backend operation that receives the client-visible schema
  and output directory. Feature 5 owns lossless engine dispatch and output confinement;
  Feature 7 owns the complete standalone project contents.
- **Generate_Entrypoint:** The backend operation that receives validated module TypeDef
  input and renders the runtime entrypoint. Feature 5 owns operation dispatch and file
  publication; Feature 6 owns dispatch semantics.
- **Generate_Library:** The backend operation that renders reusable Rust bindings for
  the supplied Visible_Schema.
- **Generate_Module:** The backend operation that composes bindings, Cargo integration,
  generated module glue, and any post-generation work for a Rust module project.
- **Generated_Artifact:** A file whose complete contents are owned by a generator and
  carry machine-readable provenance.
- **Generated_Code_Result:** The engine `GeneratedCode` value containing generated
  code plus the declared VCS-generated and VCS-ignored path sets.
- **Integration_Evidence:** Target-bound evidence produced by engine construction and
  end-to-end SDK operations rather than source-name comparison.
- **Legacy_Runtime_Codegen_Mode:** The `dagger.json` compatibility contract in which
  runtime construction may generate into private ephemeral state before building.
- **Locked_Dependency_Contract:** The rule that existing Cargo.lock resolution is
  consumed with `--locked`, newly initialized resolution is materialized explicitly,
  and runtime construction never silently rewrites committed dependency state.
- **Module_Protocol_Probe:** A private integration fixture that proves the generated
  Rust entrypoint can connect to the nested engine session, distinguish registration
  from invocation, and report a result without defining Feature 6's public authoring
  model.
- **Operation_Input:** The complete, validated values supplied to a Codegen_Operation,
  including schema, module source, output path, module identity, SDK dependency source,
  and entrypoint TypeDef input where applicable.
- **Operation_Manifest:** A canonical record of a Codegen_Operation's target, input
  identities, owned output paths, output digests, post-generation commands, and
  provenance.
- **Packaged_Integration_Assets:** Private code generator, templates, runtime support,
  and toolchain metadata made available to an engine build without publishing internal
  Rust crates.
- **Published_SDK_Dependency:** The immutable dependency descriptor generated into a
  Cargo_Project: either an exact registry version or a Git URL plus full revision. It
  is never an ambient local path.
- **Runtime_Container:** A Dagger container returned through Runtime and wrapped by the
  engine's ContainerRuntime implementation.
- **Runtime_Entrypoint:** The executable configured as the Runtime_Container entrypoint
  and invoked through ModuleRuntime.Call.
- **Runtime_Provenance:** The target, toolchain, base image, dependency-source, lockfile,
  source, generated-artifact, and binary identities that determine a Runtime_Container.
- **Runtime_Target:** The canonical engine runtime reference recorded for a newly
  initialized module when the workspace-facing SDK implementation and execution runtime
  are separated.
- **Rust_Initialization:** SDK-owned workspace changes applied by `dagger module init
  rust <name>` before the scoped generator run.
- **Rust_SDK_Config:** The engine SDK configuration associated with a Rust module,
  excluding generic engine fields such as source, debug, and experimental state.
- **SDK_Owned_Changeset:** The Changeset returned by an SDK initializer or generator,
  confined to the module or client path selected by the engine.
- **Target_Revision:** Dagger commit
  `25300124ca110612edc09c43f89cb5fad6028170`.
- **User_Owned_File:** A file that the SDK may create when absent or semantically amend
  under an explicit policy, but must never replace wholesale after the caller owns it.
- **Versioned_Rust_Shorthand:** A reference shaped as `rust@<value>` rather than a full
  external module reference.
- **Visible_Schema:** The introspection schema supplied by the engine for the current
  module or client, containing the Core_Schema plus the dependencies and self types
  visible in that operation.
- **Workspace_SDK_Installation:** The workspace entry installed for the user-facing
  SDK name `rust`, including its immutable source provenance and generator ownership.

## Target State

At Feature 5 completion, the target engine recognizes `rust` as a supported SDK and
advertises it in the same canonical metadata used for resolution, diagnostics, and
workspace installation. `dagger sdk install rust` installs an engine-coherent SDK
implementation, and `dagger module init rust <name>` creates or adopts a Cargo project
through one path-confined Changeset. The initialization and its automatically scoped
generator run do not mutate any other module or client in the workspace.

The Builtin_Rust_SDK is selected by immutable engine build provenance. A fork or
development engine can embed a different immutable Rust SDK dependency source without
rewriting generated projects to an ambient checkout. The default target rejects
Versioned_Rust_Shorthand because the Builtin_Rust_SDK and engine move in lockstep;
callers needing another implementation use an explicit immutable module or Git
reference instead of an ambiguous shorthand.

Rust generation accepts the engine-supplied Visible_Schema rather than assuming the
checked Core_Schema snapshot is the whole graph. It reuses Feature 4's canonical
validation, naming, type projection, and rendering rules for every compatible schema
coordinate. Core coordinates must remain compatible with the Target_Revision, while
module and dependency coordinates are operation-scoped additions. No code generator
can silently weaken Feature 4's collision, nullability, directive, documentation,
output-ownership, or deterministic-ordering policies.

The engine-facing backend exposes the operation contracts required for initialization,
module codegen, runtime construction, and client-generation delegation. Each operation
is fallible, deterministic, path-confined, and represented by an Operation_Manifest.
Feature 5 proves Generate_Client and Generate_Entrypoint dispatch with test backends and
real engine inputs without claiming Feature 7's final client contents or Feature 6's
public dispatch behaviour.

Runtime construction builds the selected Cargo_Project with an exact toolchain and the
Locked_Dependency_Contract. Checked_Generated_Mode consumes committed artifacts and
fails with a repair instruction if they are absent or stale. Legacy_Runtime_Codegen_Mode
may regenerate only in private container state. The final Runtime_Container contains a
Runtime_Entrypoint and runtime necessities, but not Cargo credentials, source-control
credentials, registry caches, compiler caches, mutable SDK source mounts, or an
unpublished path dependency.

A private Module_Protocol_Probe proves the complete engine boundary: the
Runtime_Entrypoint connects through the nested session, receives engine call context,
distinguishes type registration from function invocation, and reports a result through
the target protocol. That probe is not the Rust authoring API. Feature 6 replaces its
fixed fixture behaviour with complete source discovery and dispatch without changing
the Feature 5 runtime or engine-selection contract.

All Feature 5 status changes remain evidence-derived. Source presence, a green Cargo
build, or engine registration alone cannot close a capability. Exact-target engine
integration, negative boundary tests, packaged-asset provenance, and capability-local
evidence must all agree before the ledger records Implemented or
Idiomatic_Equivalent.

## Evidence From Current Code

All pinned citations below refer to the Target_Revision unless another revision is
shown explicitly.

- **Checked target (authoritative):** `sdk/rust/completeness/target.json` binds Dagger
  revision `25300124ca110612edc09c43f89cb5fad6028170`, engine
  `v1.0.0-beta.10`, Rust SDK `1.0.0-beta.10`, Rust `1.97.1`, Go SDK revision
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`, and sdk-sdk revision
  `8c164424b7a8a37b33a77367ef7547490d5b87b5`.
- **Engine SDK interface shape (authoritative):** `core/sdk.go:13-440` defines
  ClientGenerator, ModuleInitializer, ClientInitializer, RuntimeTarget, CodeGenerator,
  ModuleRuntime, ContainerRuntime, Runtime, ModuleTypes, and SDK.
- **SDK resolution behaviour (authoritative):** `core/sdk/loader.go:21-300` resolves
  built-in names before external refs, reports both failure paths for an invalid SDK,
  applies built-in version rules, and loads packaged or repository-backed module SDKs.
- **Canonical built-in list (authoritative):** `core/sdk/sdkmeta/sdkmeta.go:1-28` lists
  Go, Dang, Python, TypeScript, PHP, Elixir, and Java but omits Rust.
- **Workspace installation mapping (authoritative):**
  `core/sdk/workspace_module.go:9-56` maps built-in runtime names to SDK workspace
  modules and applies the `dagger-` installation prefix.
- **Module-backed SDK adaptation (authoritative):** `core/sdk/module.go:30-379`
  instantiates an SDK module, applies typed configuration, detects implemented engine
  functions, clones state for a ModuleSource, and adapts runtime, codegen, client,
  initialization, and runtime-target functions.
- **Changeset initialization (authoritative):** `core/sdk/module_init.go:19-170`
  injects standard workspace/name/path/module inputs, decodes SDK-specific arguments,
  rejects unknown arguments deterministically, and returns the SDK Changeset.
- **Codegen and client invocation (authoritative):**
  `core/sdk/module_code_generator.go:17-67` and
  `core/sdk/module_client_generator.go:18-97` scope ModuleSource values and forward the
  exact introspection file and output directory into SDK operations.
- **Runtime adaptation (authoritative):** `core/sdk/module_runtime.go:20-92` selects
  committed-file versus runtime-codegen behaviour and wraps the returned container in
  ContainerRuntime; `core/sdk.go:241-307` clones runtime container state before each
  ModuleRuntime.Call and invokes the configured entrypoint with execution metadata.
- **Runtime-driven TypeDef path (authoritative):**
  `core/sdk/go_sdk.go:55-66` deliberately reports no ModuleTypes implementation because
  current Go registration uses the generated runtime entrypoint's empty-call path;
  `core/sdk/module_typedefs.go:23-170` defines the alternative dedicated ModuleTypes
  adapter.
- **Generator operation model (reference evidence):**
  `cmd/codegen/generator/generator.go:12-50` defines language selection, four generator
  operations, overlay output, post-commands, and optional regeneration. The Go backend
  under `cmd/codegen/generator/go/**` supplies target examples for module, client,
  library, entrypoint, template, and dependency-update behaviour.
- **Scoped initialization and generation (authoritative tests):**
  `core/integration/generators_test.go:430-662` proves SDK-owned init Changesets,
  automatic generation for only the newly initialized path, and `--no-generate`
  behaviour; `core/integration/workspace_modules_test.go:278-359` proves workspace SDK
  ownership, uninstall cleanup, and cwd-correct generation.
- **Generated-files runtime policy (authoritative tests):**
  `core/integration/module_runtime_codegen_test.go:1-190` proves committed generated
  files for native module configuration, actionable failure when they are missing, and
  private runtime codegen for legacy configuration.
- **Invalid SDK diagnostics (authoritative tests):**
  `core/integration/module_definition_test.go:22-46` proves the readable invalid-SDK
  error and built-in version-suffix rejection pattern.
- **Packaged engine SDK content (authoritative):**
  `toolchains/engine-dev/build/sdk.go:19-263` builds OCI content for current bundled SDK
  implementations and binds each content manifest digest into the engine environment;
  `engine/distconsts/consts.go:21-24` owns those stable environment names.
- **Current Rust compiler boundary:** `sdk/rust/crates/dagger-codegen/src/lib.rs`
  accepts checked Core_Schema bytes and returns a deterministic in-memory candidate;
  it has no filesystem, process, network, engine, or ledger authority.
- **Current Rust orchestration boundary:**
  `sdk/rust/crates/dagger-bootstrap/src/**` supports checked repository generation and
  atomic publication but has no engine Codegen_Operation protocol.
- **Current Rust integration gap:** no Rust entry exists in sdkmeta, loader selection,
  workspace SDK mapping, engine build content, or engine integration fixtures; no Rust
  crate implements a module runtime or an engine-operation adapter.
- **Current publication policy:** `sdk/rust/ARCHITECTURE.md` and
  `.github/workflows/rust-sdk-security.yml` make `dagger-sdk` the sole publishable Rust
  crate. `dagger-codegen`, `dagger-bootstrap`, and `dagger-sdk-completeness` are private
  repository tooling.
- **Historical evidence only:** upstream pull request #12229 adds `rust` loader
  resolution, a Go-authored SDK module, Cargo templates, provisional macros, and local
  mounts of private Rust crates. It is open and unmerged; those choices are not target
  behaviour.

## Completeness Contract Policy

### Existing Feature 5 Scope

The post-Feature-4 ledger routes 31 capabilities to `feature-5`. Their lexicographically
sorted compact-JSON Capability_ID list has scope digest
`sha256:1f502e06f809fcfd90a8b9a3912eece3384585ad5c88963fac7681acb79c8cb3`.
This document does not change a status merely by restating that scope.

| Authority | Rows | Current status | Feature 5 policy |
|---|---:|---|---|
| `go-codegen` | 19 | 19 Partial | Preserve the four operation behaviours, deterministic output-state semantics, language selection, template/overlay containment, and dependency-update safety through Rust-native implementations |
| `go-engine-sdk` | 12 | 12 Missing | Prove Rust selection and use of the initializer, codegen, runtime, client, SDK cloning, runtime-target, ModuleTypes strategy, and ContainerRuntime call boundaries |
| **Total** | **31** | **19 Partial; 12 Missing** | Close only capability-local rows whose implementation and exact-target evidence are both complete |

The `go-codegen` source rows include Go-named constants, structs, and helpers because
Feature 1 inventories the definitive implementation. Feature 5 does not create Rust
types named `GoGenerator`, `MountedFS`, or `PackageInfo`. It maps their observable
responsibilities to Operation_Input, Operation_Manifest, ordered candidate artifacts,
typed dependency source policy, and fallible orchestration. A passing mapping may be
Idiomatic_Equivalent where public or internal Rust shape deliberately differs.

The ClientGenerator and Generate_Client engine hook remain Feature 5-owned. Complete
standalone project contents and usability remain Feature 7-owned. The
Generate_Entrypoint engine hook remains Feature 5-owned, while source discovery and
dispatch semantics remain Feature 6-owned. Evidence for a hook cannot be reused as
evidence for the content capability it delegates to.

### Rust Policy Capabilities Added by Feature 5

Feature 5 SHALL add stable `rust-policy` capability rows for these omitted obligations:

```text
policy/rust-policy/engine-bare-sdk-resolution
policy/rust-policy/engine-build-provenance-selection
policy/rust-policy/engine-version-shorthand-rejection
policy/rust-policy/engine-workspace-sdk-installation
policy/rust-policy/engine-init-changeset-confinement
policy/rust-policy/engine-existing-project-preservation
policy/rust-policy/engine-user-generated-file-ownership
policy/rust-policy/engine-visible-schema-core-compatibility
policy/rust-policy/engine-operation-input-completeness
policy/rust-policy/engine-operation-output-confinement
policy/rust-policy/engine-operation-determinism
policy/rust-policy/engine-runtime-toolchain-selection
policy/rust-policy/engine-locked-dependency-closure
policy/rust-policy/engine-immutable-sdk-dependency-source
policy/rust-policy/engine-committed-generated-runtime
policy/rust-policy/engine-legacy-runtime-codegen-isolation
policy/rust-policy/engine-runtime-protocol-boundary
policy/rust-policy/engine-runtime-cache-isolation
policy/rust-policy/engine-packaged-asset-provenance
policy/rust-policy/engine-credential-safe-diagnostics
policy/rust-policy/engine-exact-target-integration-evidence
policy/rust-policy/engine-scope-drift-closure
```

The mapping artifact must join every existing and added row to one requirement,
implementation subject, required evidence domain, and permitted final status. Added
rows cannot replace or alias existing authority rows.

### Status Evidence Boundary

| Claim | Minimum evidence |
|---|---|
| Rust is a built-in engine SDK | Exact target engine advertises and resolves `rust`; invalid and suffixed references take the declared diagnostic paths |
| Rust initialization is complete | Empty and existing Cargo projects initialize through path-confined Changesets; automatic generation and `--no-generate` both match target workspace semantics |
| Rust codegen operation is complete | Exact Operation_Input reaches the selected backend; output and post-work are deterministic, confined, provenance-bound, and failure-atomic |
| Rust runtime construction is complete | Exact target engine builds the Cargo_Project under declared toolchain/lock/dependency policy and returns the expected ContainerRuntime |
| Runtime protocol boundary is complete | Module_Protocol_Probe registers and reports a fixed invocation result through a real nested target session |
| Generate_Client hook is complete | A real engine request forwards the complete client operation input and returns only the backend's path-confined output; Feature 7 content remains separately blocking |
| Generate_Entrypoint hook is complete | A real operation forwards validated TypeDef input and atomically publishes the backend result; Feature 6 dispatch remains separately blocking |
| Packaged assets are releasable | Engine build manifest binds every private asset, toolchain, image, and dependency source; generated user manifests contain no unpublished path dependency |

Compile-only evidence cannot close engine resolution or runtime rows. A runtime probe
cannot close module authoring or arbitrary dispatch rows. One Codegen_Operation cannot
close a sibling operation. Evidence from a nearby Dagger revision, schema digest,
engine version, Rust toolchain, SDK dependency source, or engine content manifest is
stale for this target.

## Engine Integration Contract Policy

### SDK Reference Policy

This policy follows `core/sdk/loader.go:43-82,126-193,255-300` at the Target_Revision.

| Reference | Target policy | Error if invalid | Side-effect boundary |
|---|---|---|---|
| absent SDK reference | Preserve the engine's missing-SDK error | `no sdk ref provided` | No SDK load or workspace mutation |
| `rust` | Resolve the Builtin_Rust_SDK selected by Engine_Source_Descriptor | Propagate a provenance-bearing load error | May load only target-bound integration assets |
| `rust@<value>` | Reject Versioned_Rust_Shorthand at this target | `the rust sdk does not currently support selecting a specific version` | No fallback to an external ref |
| explicit local module path | Preserve the engine's external SDK resolution path | Preserve contextual module-source diagnostics | Access only the explicitly selected workspace path |
| explicit immutable Git module ref | Preserve the engine's external SDK resolution path | Preserve contextual Git/module diagnostics | Resolve exactly the caller-provided ref |
| unknown bare name | Try normal external resolution after built-in lookup | Final diagnostic identifies the invalid SDK and both resolution failures | No partial workspace mutation |

### Engine SDK Surface Policy

Every row accounts for a surface in `core/sdk.go:13-440` at the Target_Revision.

| Surface | Rust integration policy | Deferred content | Evidence boundary |
|---|---|---|---|
| SDK | Provide independently clonable engine integration state with explicit supported-surface queries | None | Clone and attachment isolation tests |
| ModuleInitializer | Return Rust_Initialization as an SDK_Owned_Changeset | Feature 6 owns the final public starter authoring style | Empty and existing Cargo project tests |
| ClientInitializer | Expose a lossless initializer delegation seam | Feature 7 owns final standalone client scaffolding | Fixture backend input/output test |
| RuntimeTarget | Return the canonical engine runtime ref when workspace SDK and runtime are split; otherwise report the surface absent | No user dispatch semantics | Workspace config provenance test |
| CodeGenerator | Forward scoped ModuleSource and Visible_Schema and return Generated_Code_Result | Feature 6 owns source discovery/dispatch content | Exact engine codegen test |
| ClientGenerator.RequiredClientGenerationFiles | Return only host files actually needed by the Rust client generator | Feature 7 may extend the finite set | Missing-file and path-confinement tests |
| ClientGenerator.GenerateClient | Forward scoped ModuleSource, Visible_Schema, and output directory without semantic loss | Feature 7 owns complete project contents | Test backend plus engine request |
| Runtime | Build a Runtime_Container from module source, generated artifacts, target metadata, and Cargo policy | Feature 6 owns arbitrary user-function behaviour | Exact engine runtime-construction test |
| ModuleRuntime | Use ContainerRuntime call semantics and a Rust Runtime_Entrypoint | Feature 6 owns general dispatch | Module_Protocol_Probe |
| ModuleTypes | Use exactly one target-supported registration strategy; the runtime empty-call path is preferred when no dedicated hook is needed | Feature 6 owns discovered TypeDefs | Registration-path selection test |
| SDK.CloneForModuleSource | Copy mutable configuration and result handles without cross-module aliasing | None | Mutation/isolation property test |
| SDK.AsModule | Reflect the selected packaged adapter architecture truthfully | None | Surface-query test |
| SDK.AttachDependencyResults | Retain only cache-backed results actually owned by the selected integration | None | Attach/clone property test |

### Initialization File Policy

The exact generated filenames may be refined by the approved design, but every file
class and ownership transition is fixed here.

| File class | Empty project | Existing compatible project | Invalid/conflicting state |
|---|---|---|---|
| Cargo package manifest | Create a valid package manifest for the target edition, Rust version, module binary/library layout, and Published_SDK_Dependency | Semantically add only missing SDK-owned keys and preserve unrelated package/workspace/dependency/profile settings | Return a typed conflict without replacing the manifest |
| Cargo lockfile | Materialize the first reviewed resolution in the initialization/generation Changeset | Preserve a compatible committed resolution unless declared dependency changes require an explicit regenerated lockfile | Return an actionable stale/incompatible-lock diagnostic |
| Rust toolchain declaration | Create the exact target declaration when no enclosing project policy exists | Honor one compatible project or enclosing-workspace declaration | Return a typed toolchain-compatibility diagnostic |
| User starter source | Create only when the selected module target has no authored source | Preserve every existing source file byte-for-byte | Return a conflict if a required SDK-owned path is occupied by incompatible user content |
| Generated bindings and glue | Publish the complete generator-owned set with provenance | Replace only paths owned by the previous compatible Operation_Manifest | Reject unknown, symlinked, escaped, or user-owned collisions |
| VCS ignore/attributes | Add only entries required by the approved generated-file policy | Preserve unrelated rules and ordering semantics | Return a typed parse/conflict diagnostic rather than overwriting |
| Engine module/workspace config | Leave engine-owned config edits to the engine | Leave engine-owned config edits to the engine | Never emit a competing config file from the SDK Changeset |

### Codegen Operation Policy

| Operation | Required input | Feature 5 output responsibility | Sibling boundary |
|---|---|---|---|
| Generate_Library | target, complete Visible_Schema, schema version, dependency source policy, output root | Deterministic reusable binding artifacts plus Operation_Manifest | No module authoring or standalone package scaffolding |
| Generate_Module | target, scoped ModuleSource, complete Visible_Schema, module identity, Cargo_Project state, dependency source policy | Bindings, Cargo integration, generated module glue, declared post-work, Generated_Code_Result | Feature 6 owns discovered TypeDefs and general dispatch logic |
| Generate_Client | target, scoped ModuleSource, complete client-visible schema, output directory, dependency source policy | Lossless backend dispatch, confined result, Operation_Manifest | Feature 7 owns complete client contents and usability |
| Generate_Entrypoint | target, validated TypeDef document, module root, source root, SDK import/dependency policy, output file | Lossless backend dispatch, one owned entrypoint result, Operation_Manifest | Feature 6 owns generated dispatch semantics |

### Runtime Build Policy

| Input | Target policy | Invalid-input behaviour | Identity/side-effect impact |
|---|---|---|---|
| target engine and SDK metadata | Require exact Engine_Source_Descriptor compatibility | Return typed target-compatibility failure | Included in Runtime_Provenance |
| project Cargo.toml | Select exactly one module package/target | Return typed missing/ambiguous/conflicting project failure | Read-only during runtime construction |
| Cargo.lock | Require committed compatible lock in Checked_Generated_Mode | Return stale/missing lock failure with generation instruction | Digest included in Runtime_Provenance |
| rust-toolchain.toml or enclosing toolchain policy | Use compatible exact target or target default | Return typed unsupported-toolchain failure | Exact resolved toolchain included in Runtime_Provenance |
| generated artifact manifest | Require complete, current, non-symlink owned set | Return missing/stale/collision failure with generation instruction | Manifest and byte digests included in Runtime_Provenance |
| Published_SDK_Dependency | Accept exact registry version or immutable Git URL/revision from engine build policy | Reject path, wildcard, branch-only, or unknown source | Descriptor included in generated manifest and provenance |
| Cargo registry/Git credentials | Mount only through secret-safe build channels when required | Return redacted dependency-fetch failure | Never included in output or runtime filesystem |
| Cargo registry/git/compiler caches | Key by non-secret compatibility inputs | Discard or isolate an incompatible cache | Removed from final Runtime_Container |
| module source | Mount only the implementation-scoped source selected by the engine | Return path/symlink/confinement failure | Source digest included in Runtime_Provenance |
| debug mode | Enable target-approved terminal/debug behaviour | Reject unsupported debug combination before build | Does not change dependency or source provenance |

## Requirements

### Requirement 1: Exact and Honest Engine-Integration Scope

**User Story:** As a Rust SDK maintainer, I want Feature 5's ownership and evidence
boundary to be exhaustive, so that engine integration cannot inflate unrelated module
or client completeness.

#### Acceptance Criteria

1. THE Feature 5 scope manifest SHALL enumerate the 31 existing Capability_IDs with
   scope digest `sha256:1f502e06f809fcfd90a8b9a3912eece3384585ad5c88963fac7681acb79c8cb3`.
2. THE Feature 5 scope manifest SHALL enumerate every Rust policy capability declared
   in this document.
3. THE Feature 5 scope manifest SHALL map every capability to one implementation
   subject.
4. THE Feature 5 scope manifest SHALL map every capability to one required evidence
   domain set.
5. WHEN a Go-specific mechanism has no Rust public equivalent, THE Feature 5 mapping
   SHALL record an Idiomatic_Equivalent rationale.
6. IF an engine hook delegates its output semantics to Feature 6 or Feature 7, THEN THE
   Feature 5 mapping SHALL exclude those delegated content capabilities from the hook's
   passing evidence.
7. WHEN a current capability changes owner, THE completeness contract SHALL require a
   reviewed one-to-one replacement mapping.
8. IF a current or newly extracted engine SDK capability is absent from the scope
   manifest, THEN THE completeness contract SHALL fail before status rendering.
9. IF a source row disappears at the same Target_Revision, THEN THE completeness
   contract SHALL fail rather than silently removing the capability.
10. WHEN implementation source exists without exact-target evidence, THE ledger SHALL
    retain a blocking non-complete status.
11. WHEN exact-target evidence covers only one Codegen_Operation, THE registry SHALL
    restrict that evidence to that operation's capability IDs.
12. THE completeness report SHALL distinguish engine-hook completion from module
    authoring and standalone-client completion.

### Requirement 2: Rust SDK Resolution, Versioning, and Provenance

**User Story:** As a Dagger user, I want `rust` to resolve predictably to the SDK paired
with my engine, so that a module cannot silently use code from another release or
repository.

#### Acceptance Criteria

1. WHEN a module selects Bare_Rust_Reference, THE engine SDK loader SHALL resolve the
   Builtin_Rust_SDK before attempting external module resolution.
2. THE canonical engine SDK metadata SHALL advertise `rust` exactly once.
3. THE workspace SDK mapping SHALL expose the user-facing SDK name `rust`.
4. THE workspace SDK mapping SHALL use the collision-resistant installation name
   `dagger-rust-sdk`.
5. WHEN the Builtin_Rust_SDK loads, THE engine SHALL bind it to an
   Engine_Source_Descriptor embedded by the engine build.
6. THE Engine_Source_Descriptor SHALL contain the full Dagger revision.
7. THE Engine_Source_Descriptor SHALL contain the Rust SDK version.
8. THE Engine_Source_Descriptor SHALL contain the immutable Published_SDK_Dependency.
9. IF Versioned_Rust_Shorthand is supplied, THEN THE engine SHALL return
   `the rust sdk does not currently support selecting a specific version`.
10. IF Versioned_Rust_Shorthand is supplied, THEN THE engine SHALL avoid external SDK
    fallback.
11. WHEN an explicit immutable external SDK reference is supplied, THE engine SHALL
    preserve the normal external SDK resolution path.
12. IF an unknown SDK cannot load through either resolution path, THEN THE engine SHALL
    include `rust` in the available built-in SDK list.
13. IF the packaged Rust SDK target differs from the running engine target, THEN THE
    engine SHALL return a typed compatibility diagnostic before workspace mutation.
14. IF engine build provenance is incomplete, THEN THE engine build SHALL fail before
    producing a distributable engine image.

### Requirement 3: Workspace Installation and Rust Initialization

**User Story:** As a module author, I want current Dagger workspace commands to create
or adopt a Rust project safely, so that I can begin from an empty directory or an
existing Cargo codebase.

#### Acceptance Criteria

1. WHEN `dagger sdk install rust` succeeds, THE workspace SHALL contain one
   Workspace_SDK_Installation named `dagger-rust-sdk`.
2. WHEN the same Rust SDK source is installed again, THE workspace mutation SHALL be
   idempotent.
3. IF an installation name is occupied by another source, THEN THE workspace mutation
   SHALL return the target collision diagnostic.
4. WHEN `dagger module init rust <name>` targets the default path, THE engine SHALL
   confine the new module to that path.
5. WHEN Rust_Initialization runs, THE Builtin_Rust_SDK SHALL return an
   SDK_Owned_Changeset.
6. THE Rust_Initialization Changeset SHALL exclude engine-owned workspace and module
   configuration edits.
7. THE Rust_Initialization Changeset SHALL exclude paths outside the initialized
   module root.
8. WHEN initialization succeeds without `--no-generate`, THE engine SHALL run only the
   generator scope for the newly initialized module.
9. WHEN initialization succeeds with `--no-generate`, THE engine SHALL omit generated
   artifacts from the applied result.
10. WHEN initialization succeeds with `--no-generate`, THE engine SHALL retain the
    SDK-owned scaffold.
11. WHEN initialization receives Rust-specific arguments, THE initializer SHALL decode
    them through the engine's declared function input types.
12. IF initialization receives an unknown Rust-specific argument, THEN THE initializer
    SHALL report the sorted unknown argument names.
13. IF initialization fails before Changeset application, THEN THE workspace SHALL
    remain byte-identical.
14. WHEN a Rust-managed module is uninstalled, THE workspace SHALL remove its ownership
    record from the Rust SDK installation.
15. WHEN a Rust-managed module is uninstalled, THE workspace SHALL preserve other Rust
    modules and clients.
16. WHEN generation is invoked from a nested workspace cwd, THE Rust generator SHALL
    write paths relative to the workspace root selected by the engine.

### Requirement 4: Cargo Project Adoption and File Ownership

**User Story:** As a Rust developer, I want Dagger to respect normal Cargo projects and
my authored files, so that adopting Dagger does not replace project configuration or
source code.

#### Acceptance Criteria

1. WHEN initialization finds no Cargo package manifest, THE Rust initializer SHALL
   create a target-compatible Cargo package manifest.
2. WHEN initialization finds one compatible package manifest, THE Rust initializer
   SHALL preserve every unrelated semantic setting.
3. WHEN initialization runs inside a Cargo workspace, THE Rust initializer SHALL select
   the module package by the engine-provided source path.
4. IF zero Cargo packages match the module source path, THEN THE Rust initializer SHALL
   return a typed missing-package diagnostic.
5. IF more than one Cargo package matches the module source path, THEN THE Rust
   initializer SHALL return a typed ambiguous-package diagnostic.
6. WHEN the target package lacks a Dagger SDK dependency, THE Rust initializer SHALL
   add the Published_SDK_Dependency from Engine_Source_Descriptor.
7. IF the target package declares a conflicting Dagger SDK source or version, THEN THE
   Rust initializer SHALL return a typed dependency-conflict diagnostic.
8. THE Rust initializer SHALL reject wildcard Dagger SDK dependencies.
9. THE Rust initializer SHALL reject mutable branch-only Dagger SDK dependencies.
10. THE Rust initializer SHALL reject ambient filesystem path dependencies for the
    Dagger SDK.
11. WHEN no compatible toolchain declaration exists, THE Rust initializer SHALL create
    the exact target toolchain declaration.
12. WHEN a compatible project or enclosing-workspace toolchain declaration exists, THE
    Rust initializer SHALL preserve it.
13. WHEN no authored module source exists, THE Rust initializer SHALL create the
    approved minimal starter source.
14. WHEN authored Rust source exists, THE Rust initializer SHALL preserve every authored
    source file byte-for-byte.
15. WHEN a generator-owned path already contains unknown content, THE Rust initializer
    SHALL return an ownership-conflict diagnostic.
16. WHEN a compatible previous Operation_Manifest owns a generated path, THE Rust
    generator SHALL replace that path only through complete generated publication.
17. WHEN initialization changes dependency resolution, THE SDK_Owned_Changeset SHALL
    include the resulting Cargo.lock.
18. IF dependency resolution fails, THEN THE Rust initializer SHALL avoid publishing a
    partial manifest or lockfile.
19. WHEN VCS policy needs a new entry, THE Rust initializer SHALL preserve unrelated
    ignore and attribute rules.
20. THE generated ownership documentation SHALL identify how each Generated_Artifact is
    regenerated.

### Requirement 5: Visible Schema and Operation Input Integrity

**User Story:** As an engine maintainer, I want Rust operations to consume the exact
schema and module source selected by the engine, so that cached or nearby inputs cannot
generate a plausible but incompatible project.

#### Acceptance Criteria

1. WHEN an engine Codegen_Operation supplies a Visible_Schema, THE Rust backend SHALL
   decode the complete supplied schema.
2. THE Rust backend SHALL validate every Core_Schema coordinate in the Visible_Schema
   against the Target_Revision compatibility policy.
3. THE Rust backend SHALL treat module and dependency coordinates as operation-scoped
   additions rather than target Core_Schema replacements.
4. IF a target Core_Schema coordinate changes incompatibly, THEN THE Rust backend SHALL
   return a coordinate-bearing compatibility diagnostic.
5. IF a Visible_Schema contains an unresolved reference, THEN THE Rust backend SHALL
   fail before rendering.
6. IF a Visible_Schema introduces a Rust name collision, THEN THE Rust backend SHALL
   fail before rendering.
7. WHEN equivalent Visible_Schema documents differ only in source array order, THE Rust
   backend SHALL produce the same semantic projection.
8. WHEN the engine scopes a ModuleSource for an SDK operation, THE Rust backend SHALL
   consume only that implementation-scoped source.
9. THE Operation_Input SHALL include the exact engine target identity.
10. THE Operation_Input SHALL include the exact Visible_Schema identity.
11. THE Operation_Input SHALL include the exact scoped module source identity when the
    operation has module source.
12. THE Operation_Input SHALL include the exact Published_SDK_Dependency.
13. THE Operation_Input SHALL include the normalized output root.
14. IF an output root escapes the engine-selected operation root, THEN THE Rust backend
    SHALL return a path-confinement diagnostic.
15. IF a supplied file or directory input is symlinked across the operation boundary,
    THEN THE Rust backend SHALL return a path-confinement diagnostic.
16. WHEN one Operation_Input field changes, THE Operation_Manifest SHALL change its
    canonical input identity.

### Requirement 6: Fallible and Deterministic Codegen Operations

**User Story:** As a Rust SDK maintainer, I want one typed operation model for every
engine generation path, so that generation is reviewable and failures cannot leak
partial files.

#### Acceptance Criteria

1. THE Rust backend SHALL implement a distinct typed selector for every
   Codegen_Operation.
2. IF an unknown operation selector is supplied, THEN THE Rust backend SHALL return a
   typed unknown-operation diagnostic.
3. WHEN Generate_Library succeeds, THE Rust backend SHALL return a complete ordered
   binding artifact set.
4. WHEN Generate_Module succeeds, THE Rust backend SHALL return one
   Generated_Code_Result.
5. WHEN Generate_Client is requested, THE Rust backend SHALL forward every required
   input to the selected client renderer.
6. WHEN Generate_Entrypoint is requested, THE Rust backend SHALL forward every required
   input to the selected entrypoint renderer.
7. WHEN a test renderer returns a finite artifact set, THE Rust backend SHALL preserve
   every artifact byte in the confined operation result.
8. WHEN a test renderer returns declared post-generation work, THE Rust backend SHALL
   execute only the allowlisted command shape.
9. IF post-generation work requests an unapproved executable or argument class, THEN
   THE Rust backend SHALL return a typed command-policy diagnostic.
10. WHEN post-generation work changes project files, THE Operation_Manifest SHALL record
    every changed owned path.
11. WHEN an operation requires a second projection pass, THE Rust backend SHALL cap the
    pass count at the declared operation policy.
12. IF an operation exceeds its declared projection pass count, THEN THE Rust backend
    SHALL return a typed non-convergence diagnostic.
13. WHEN identical Operation_Input is processed twice, THE Rust backend SHALL produce
    byte-identical artifacts and Operation_Manifest bytes.
14. THE Rust backend SHALL order artifacts independently of filesystem enumeration.
15. THE Rust backend SHALL order diagnostics independently of filesystem enumeration.
16. IF rendering, formatting, post-work, or manifest assembly fails, THEN THE Rust
    backend SHALL publish no output path.
17. WHEN generated output differs from a previous owned set, THE operation result SHALL
    identify every added, changed, and removed path.
18. THE Rust backend SHALL preserve Feature 4's public naming, nullability, directive,
    documentation, and serialization policies for reused bindings.
19. THE Rust backend SHALL keep filesystem, process, and engine I/O outside the
    Core_Binding_Compiler.

### Requirement 7: Engine Codegen, Client, and Registration Hooks

**User Story:** As a Dagger engine maintainer, I want Rust to expose truthful engine
capabilities, so that workspace and runtime control flow does not depend on accidental
method presence.

#### Acceptance Criteria

1. WHEN the engine queries the Rust SDK's codegen capability, THE integration SHALL
   report the CodeGenerator surface available.
2. WHEN the engine invokes CodeGenerator, THE integration SHALL return the Rust
   Generated_Code_Result.
3. WHEN the engine queries the Rust SDK's client-generation capability, THE integration
   SHALL report the Feature 5 Generate_Client hook available.
4. WHEN the engine invokes Generate_Client, THE integration SHALL preserve the exact
   requested output directory.
5. WHEN the client renderer declares no required host files, THE integration SHALL
   return an empty required-file set.
6. WHEN the client renderer declares required host files, THE integration SHALL return
   only normalized relative paths.
7. IF a required host file path is absolute or escaping, THEN THE integration SHALL
   return a path-confinement diagnostic.
8. WHEN the engine queries the Rust SDK's module-initialization capability, THE
   integration SHALL report Rust_Initialization available.
9. WHEN the engine queries the Rust SDK's client-initialization capability, THE
   integration SHALL reflect the configured Feature 7 delegation state truthfully.
10. WHEN workspace-facing SDK implementation and runtime are separate, THE integration
    SHALL return the canonical Runtime_Target.
11. WHEN runtime-driven registration is selected, THE integration SHALL report the
    dedicated ModuleTypes surface absent.
12. WHEN dedicated ModuleTypes registration is selected, THE integration SHALL report
    the ModuleTypes surface available.
13. THE integration SHALL select exactly one module-registration strategy.
14. WHEN SDK state is cloned for two ModuleSource values, THE integration SHALL prevent
    mutable configuration from aliasing between those clones.
15. WHEN cache-backed SDK results are attached, THE integration SHALL retain only
    results owned by the cloned SDK state.
16. WHEN the integration is not implemented as a Dagger module, THE integration SHALL
    report AsModule unavailable.
17. WHEN the integration is implemented as a Dagger module, THE integration SHALL
    return its exact module result through AsModule.

### Requirement 8: Reproducible Rust Runtime Construction

**User Story:** As a Rust module author, I want Dagger to build a deterministic runtime
from my committed Cargo project, so that execution does not depend on hidden generator
or host state.

#### Acceptance Criteria

1. WHEN the engine requests Runtime, THE Builtin_Rust_SDK SHALL return a
   Runtime_Container.
2. THE Runtime_Container SHALL be bound to one Runtime_Provenance record.
3. THE Runtime_Provenance record SHALL include the exact Engine_Source_Descriptor.
4. THE Runtime_Provenance record SHALL include the exact resolved Rust toolchain.
5. THE Runtime_Provenance record SHALL include the exact base image digest.
6. THE Runtime_Provenance record SHALL include the exact Cargo.lock digest.
7. THE Runtime_Provenance record SHALL include the exact scoped module source digest.
8. THE Runtime_Provenance record SHALL include the exact generated-artifact manifest
   digest.
9. THE Runtime_Provenance record SHALL include the exact compiled Runtime_Entrypoint
   binary digest.
10. WHEN a Cargo_Project declares one compatible exact toolchain, THE runtime builder
    SHALL use that toolchain.
11. WHEN no Cargo_Project toolchain is declared, THE runtime builder SHALL use Rust
    `1.97.1` for this target.
12. IF a Cargo_Project toolchain is below the SDK MSRV, THEN THE runtime builder SHALL
    return a typed unsupported-toolchain diagnostic.
13. IF a Cargo_Project toolchain cannot be resolved immutably, THEN THE runtime builder
    SHALL return a typed non-reproducible-toolchain diagnostic.
14. WHEN a compatible Cargo.lock is present, THE runtime builder SHALL invoke Cargo
    with `--locked`.
15. IF Checked_Generated_Mode lacks Cargo.lock, THEN THE runtime builder SHALL return an
    actionable missing-lock diagnostic.
16. IF Cargo.lock is stale for the selected manifest, THEN THE runtime builder SHALL
    return an actionable stale-lock diagnostic.
17. THE runtime builder SHALL compile the engine-selected module target rather than a
    caller-controlled arbitrary binary.
18. THE runtime builder SHALL configure the compiled Runtime_Entrypoint as the container
    entrypoint.
19. THE Runtime_Container SHALL use the engine runtime workdir expected by
    ContainerRuntime.
20. WHEN equivalent Runtime_Provenance inputs are built twice, THE runtime builder SHALL
    select the same semantic container construction.

### Requirement 9: Generated-Files and Runtime-Codegen Modes

**User Story:** As a module maintainer, I want committed generation to be authoritative
for current projects while legacy projects remain operable, so that runtime execution
never hides stale source in modern workflows.

#### Acceptance Criteria

1. WHILE Checked_Generated_Mode is active, THE runtime builder SHALL consume committed
   generated artifacts.
2. WHILE Checked_Generated_Mode is active, THE runtime builder SHALL avoid requesting
   introspection solely to regenerate module artifacts.
3. IF a required generated artifact is missing in Checked_Generated_Mode, THEN THE
   runtime builder SHALL return an error naming `dagger generate` as the repair action.
4. IF a required generated artifact is stale in Checked_Generated_Mode, THEN THE
   runtime builder SHALL return an error naming `dagger generate` as the repair action.
5. IF an unknown file occupies a generated-owned path in Checked_Generated_Mode, THEN
   THE runtime builder SHALL return an ownership-conflict diagnostic.
6. WHILE Legacy_Runtime_Codegen_Mode is active, THE runtime builder SHALL generate only
   in private ephemeral container state.
7. WHILE Legacy_Runtime_Codegen_Mode is active, THE runtime builder SHALL leave the host
   module source unchanged.
8. WHEN Legacy_Runtime_Codegen_Mode generation fails, THE runtime builder SHALL discard
   the private partial output.
9. THE integration SHALL derive runtime-codegen mode from the target module config
   format rather than a hidden environment switch.
10. WHEN current module configuration replaces legacy configuration, THE integration
    SHALL select Checked_Generated_Mode.
11. THE Operation_Manifest SHALL distinguish committed generation from private runtime
    generation.
12. THE completeness evidence SHALL distinguish committed generation from private
    runtime generation.

### Requirement 10: Runtime Entrypoint and Engine Protocol Boundary

**User Story:** As an engine maintainer, I want the Rust runtime to participate in the
real module call protocol, so that later authoring and dispatch work builds on an
executed boundary rather than an untested container.

#### Acceptance Criteria

1. WHEN the Runtime_Entrypoint starts under ModuleRuntime.Call, THE Rust process SHALL
   connect through the supplied nested engine session.
2. THE Rust process SHALL use Feature 2 and Feature 3 session, transport, and error
   paths for nested engine communication.
3. WHEN the engine supplies an empty function name for registration, THE
   Module_Protocol_Probe SHALL execute the registration branch.
4. WHEN the engine supplies a fixed probe function, THE Module_Protocol_Probe SHALL
   execute the invocation branch.
5. WHEN registration succeeds, THE Module_Protocol_Probe SHALL report the target module
   identity through the engine protocol.
6. WHEN fixed probe invocation succeeds, THE Module_Protocol_Probe SHALL report the
   expected fixed result through FunctionCall.
7. IF nested session metadata is absent or malformed, THEN THE Runtime_Entrypoint SHALL
   return a typed session diagnostic.
8. IF engine call context is malformed, THEN THE Runtime_Entrypoint SHALL return a typed
   protocol diagnostic.
9. IF result reporting fails, THEN THE Runtime_Entrypoint SHALL preserve the underlying
   typed engine error as its source.
10. THE Module_Protocol_Probe SHALL remain private to integration verification.
11. THE Module_Protocol_Probe SHALL avoid defining a public Rust authoring annotation or
    macro.
12. THE Module_Protocol_Probe SHALL avoid claiming arbitrary function dispatch.
13. WHEN Feature 6 replaces the probe, THE Feature 5 engine and Runtime_Container
    contracts SHALL remain compatible.
14. WHEN ModuleRuntime.Call invokes the same Runtime_Container concurrently, THE engine
    SHALL preserve per-call filesystem and execution metadata isolation.

### Requirement 11: Packaging, Dependency, Cache, and Credential Safety

**User Story:** As a release engineer, I want the engine to carry a hermetic Rust
integration without publishing internal tooling or leaking build credentials, so that
the SDK can ship safely from either the canonical repository or an immutable fork.

#### Acceptance Criteria

1. THE engine build SHALL package every private Rust integration asset required at
   runtime or codegen.
2. THE engine build SHALL record a content digest for the packaged Rust integration
   assets.
3. THE engine build SHALL bind the content digest to the produced engine image.
4. THE engine build SHALL pin every base image by digest.
5. THE engine build SHALL pin the Rust toolchain used by packaged integration assets.
6. THE engine build SHALL preserve `dagger-sdk` as the sole publishable Rust workspace
   crate.
7. THE engine build SHALL consume `dagger-codegen` and `dagger-bootstrap` as private
   build inputs.
8. THE generated Cargo_Project SHALL depend on an exact registry version or immutable
   Git revision of `dagger-sdk`.
9. THE generated Cargo_Project SHALL avoid a dependency on the engine repository's
   local filesystem.
10. THE generated Cargo_Project SHALL avoid a dependency on unpublished private Rust
    crates.
11. WHEN a fork engine supplies an immutable fork dependency descriptor, THE generated
    Cargo_Project SHALL preserve that descriptor exactly.
12. WHEN a canonical release engine supplies a registry dependency descriptor, THE
    generated Cargo_Project SHALL preserve that exact version requirement.
13. THE runtime builder SHALL keep Cargo registry credentials out of generated files.
14. THE runtime builder SHALL keep Git credentials out of generated files.
15. THE runtime builder SHALL keep credentials out of Runtime_Provenance.
16. THE runtime builder SHALL key caches without secret values.
17. THE final Runtime_Container SHALL exclude Cargo registry caches.
18. THE final Runtime_Container SHALL exclude Cargo Git caches.
19. THE final Runtime_Container SHALL exclude compiler caches.
20. THE final Runtime_Container SHALL exclude mutable SDK source mounts.
21. THE final Runtime_Container SHALL exclude build-only credentials.
22. IF a dependency fetch fails, THEN THE diagnostic SHALL redact credential-bearing
    URLs and headers.
23. IF a build command fails, THEN THE diagnostic SHALL avoid rendering session tokens
    or secret environment values.
24. THE Rust integration dependency graph SHALL pass the repository's locked
    cargo-deny policy.

### Requirement 12: Typed Failures and Failure-Atomic Integration

**User Story:** As a Rust module author, I want actionable failures at the layer that
caused them, so that a failed initialization, generation, or runtime build never leaves
an ambiguous half-state.

#### Acceptance Criteria

1. THE Rust integration SHALL distinguish SDK resolution failures from external SDK
   resolution failures.
2. THE Rust integration SHALL distinguish target compatibility failures from schema
   validation failures.
3. THE Rust integration SHALL distinguish Cargo project selection failures from Cargo
   dependency resolution failures.
4. THE Rust integration SHALL distinguish generated ownership failures from generated
   drift failures.
5. THE Rust integration SHALL distinguish toolchain failures from compilation failures.
6. THE Rust integration SHALL distinguish runtime construction failures from runtime
   protocol failures.
7. WHEN wrapping an engine, Cargo, formatter, or filesystem failure, THE diagnostic
   SHALL preserve the underlying error as a source.
8. WHEN reporting an Operation_Input error, THE diagnostic SHALL identify the operation
   and stable input coordinate.
9. WHEN reporting a generated path error, THE diagnostic SHALL identify the normalized
   relative path.
10. IF initialization fails, THEN THE SDK_Owned_Changeset SHALL contain no partial
    mutation.
11. IF generation fails, THEN THE operation result SHALL contain no partial generated
    publication.
12. IF runtime construction fails, THEN THE engine SHALL avoid registering a partial
    runtime result.
13. WHEN a process is cancelled, THE Rust integration SHALL terminate and reap the
    process it started.
14. WHEN a process exits unsuccessfully, THE Rust integration SHALL capture bounded
    credential-safe diagnostics.
15. WHEN a diagnostic is rendered twice, THE Rust integration SHALL preserve stable
    ordering and redaction.
16. THE production integration path SHALL avoid panic, unchecked unwrap, and unsafe
    Rust.

### Requirement 13: Exact-Target Verification and Evidence Admission

**User Story:** As a Rust SDK consumer, I want engine integration claims backed by the
actual target engine, so that a green unit suite cannot conceal an unusable SDK.

#### Acceptance Criteria

1. THE Rust integration test suite SHALL build an engine from the exact Target_Revision.
2. THE Rust integration test suite SHALL assert that canonical SDK metadata contains
   `rust` exactly once.
3. THE Rust integration test suite SHALL execute `dagger sdk install rust` against the
   exact target engine.
4. THE Rust integration test suite SHALL execute Rust initialization for an empty
   project.
5. THE Rust integration test suite SHALL execute Rust initialization for an existing
   compatible Cargo project.
6. THE Rust integration test suite SHALL prove that initialization preserves unrelated
   workspace files.
7. THE Rust integration test suite SHALL prove that automatic generation touches only
   the initialized module.
8. THE Rust integration test suite SHALL prove `--no-generate` omits generated output.
9. THE Rust integration test suite SHALL exercise every Codegen_Operation through its
   real operation selector.
10. THE Rust integration test suite SHALL exercise the Generate_Client hook with a
    finite test renderer.
11. THE Rust integration test suite SHALL exercise the Generate_Entrypoint hook with a
    finite test renderer.
12. THE Rust integration test suite SHALL build one Runtime_Container under
    Checked_Generated_Mode.
13. THE Rust integration test suite SHALL build one Runtime_Container under
    Legacy_Runtime_Codegen_Mode.
14. THE Rust integration test suite SHALL execute Module_Protocol_Probe registration.
15. THE Rust integration test suite SHALL execute Module_Protocol_Probe invocation.
16. THE Rust integration test suite SHALL cover invalid Rust shorthand.
17. THE Rust integration test suite SHALL cover missing generated files.
18. THE Rust integration test suite SHALL cover a stale Cargo.lock.
19. THE Rust integration test suite SHALL cover an incompatible Rust toolchain.
20. THE Rust integration test suite SHALL cover an escaping output path.
21. THE Rust integration test suite SHALL cover a symlink escaping its operation
    boundary.
22. THE Rust integration test suite SHALL cover an unknown ownership collision.
23. THE Rust integration test suite SHALL cover credential redaction failures.
24. WHEN exact-target observations pass, THE evidence producer SHALL bind their result
    to the exact engine revision, engine version, schema digest, Rust SDK source digest,
    toolchain, and packaged-asset digest.
25. WHEN exact-target observations pass, THE evidence producer SHALL enumerate the
    exact proved Capability_ID set.
26. IF an observation is skipped, stale, failed, or produced against another target,
    THEN THE evidence registry SHALL reject it.
27. IF an observation claims a sibling Feature 6 or Feature 7 content capability, THEN
    THE evidence registry SHALL reject it.
28. WHEN evidence admission changes a status, THE completeness renderer SHALL derive
    that status through the Feature 1 transition policy.
29. THE committed integration report SHALL identify remaining Feature 5 blockers
    without relabeling them for presentation.
30. THE final Feature 5 checkpoint SHALL require a clean worktree after scoped engine
    generation and all committed derived artifacts.
31. THE final Feature 5 checkpoint SHALL require repository formatting checks for all
    changed Rust and Go sources.
32. THE final Feature 5 checkpoint SHALL require locked Rust checks and tests.
33. THE final Feature 5 checkpoint SHALL require warning-denied clippy and rustdoc.
34. THE final Feature 5 checkpoint SHALL require the repository cargo-deny gate.
35. THE final Feature 5 checkpoint SHALL require tests for every changed Go package.
36. THE final Feature 5 checkpoint SHALL require focused exact-target engine
    integration tests.
37. THE final Feature 5 checkpoint SHALL require the repository Rust security checks.

## Iteration and Feedback Notes

- Requirements workflow selected: feature, requirements-first.
- The umbrella's legacy `dagger init --sdk rust` wording is superseded here by the
  Target_Revision workspace workflow: `dagger sdk install rust` and
  `dagger module init rust <name>`.
- The current 31-row Feature 5 scope is retained. Hook evidence is explicitly prevented
  from closing Feature 6 authoring/dispatch or Feature 7 standalone-project content.
- Historical PR #12229 informed the negative policies around Go-authored runtime
  coupling, unpublished path dependencies, mutable repository mounts, old edition
  templates, and premature macro commitment. It is not a behavioural authority.
- Design remains consent-gated. In particular, it must decide whether the
  workspace-facing SDK implementation is packaged code, a module, or a split
  implementation plus Runtime_Target while preserving every requirement above.
