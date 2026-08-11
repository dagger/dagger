# Requirements Document: Rust SDK Module Authoring and Dispatch

## Introduction

Feature 6 replaces Feature 5's fixed private protocol probe with a complete, idiomatic
Rust module-authoring and dispatch contract at the exact target selected by
`sdk/rust/completeness/target.json`. A Rust developer must be able to declare module
objects, interfaces, enums, state, constructors, and functions in Rust; receive
source-located diagnostics before runtime; and execute those functions through the
engine's current-call protocol without writing a parallel schema or hand-authored
dispatcher.

The Dagger engine at commit `25300124ca110612edc09c43f89cb5fad6028170` (the
Target_Revision) is authoritative for TypeDef registration, module namespacing,
`FunctionCall` input and return semantics, source maps, metadata, and module runtime
behaviour. The module generator under `cmd/codegen/generator/go/templates/**` at that
revision and `github.com/dagger/dagger-go-sdk` commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5` are definitive for observable module
authoring behaviours where the engine contract alone does not settle them. Go
reflection, exported-item discovery, pointer conventions, package globals, generated
panic paths, and template structure are implementation evidence, not a Rust API
design.

Rust owns the public authoring shape. Exports are explicit and compiler-checked;
`Option<T>`, `Result<T, E>`, traits, enums, async functions, owned state, and a scoped
module context retain their Rust meanings. In particular, Rust does not recreate the
Go SDK's process-global `dag` client or global marshal context. The complete generated
Core_Schema surface remains available to module code through one call-scoped context
bound to the active session.

Feature 6 depends on Feature 1's executable completeness contract, Feature 4's
fallible schema compiler and generated Core_Schema bindings, and Feature 5's engine
operation, project, runtime, and nested-session seams. It fills the semantic contents
of Generate_Module and Generate_Entrypoint without changing their engine ABI. Feature
7 owns complete standalone Core, module, and dependency client projects. Feature 8
owns the full engine-backed, cross-SDK, and cross-platform conformance matrix. Feature
9 owns publication, migration, and stable-release presentation.

Local development and every Feature 6 implementation checkpoint are engine-free by
contract. Production source analysis, descriptor construction, TypeDef projection,
entrypoint generation, dispatch, state conversion, result conversion, cancellation,
and failure isolation must all be executable through a direct Rust harness. A Dagger
engine is reserved for final SDK sign-off unless a contract is proven impossible to
model locally and the exception is separately documented and explicitly approved.
Routine uncertainty, regeneration convenience, or similarity to another SDK is not
such an exception.

Pull request #12229 is useful historical evidence that Rust-side module annotations,
entrypoint generation, and engine dispatch can be connected. Its provisional macro
surface, Go-authored runtime, repository path mounts, global state, and old Cargo
defaults do not define this specification.

`MChorfa/dagger-zig` commit `1ae0304f173fc2f617960cd67a7daad1729357bb`
is comparative implementation evidence that a native-language Dagger module can use
its repository SDK to build and test that SDK, while an offline production-dispatch
harness remains separate from live-engine verification. Its Zig comptime reflection,
implicit public-method exports, positional arguments, raw defaults, Go code generator,
and target version are not authorities for Rust behaviour or API shape. Its documented
v0.3.4 packaging failure is evidence that SDK sign-off must exercise only packaged SDK
contents rather than accidentally succeeding through repository-relative paths.

## Glossary

- **Active_Call:** The one engine `FunctionCall` currently owned by a generated Rust
  entrypoint invocation.
- **Authoring_Compiler:** The production, engine-free Rust component that validates the
  Authoring_Surface and produces a Module_Descriptor plus generated dispatch assets.
- **Authoring_Surface:** The explicit Rust declarations and metadata through which a
  user exports module objects, fields, interfaces, enums, constructors, and functions.
- **Call_Envelope:** An engine-independent value carrying the Active_Call identity,
  parent type, function name, parent JSON, named argument JSON, cancellation signal,
  and a result sink.
- **Capability_Scope:** The exact Feature 1 Capability_ID set owned or consumed by this
  feature, including authority, target, evidence domain, and status policy.
- **Complete_Status:** `Implemented`, `Idiomatic_Equivalent`, or a justified
  `Inapplicable` classification under Feature 1's status policy.
- **Core_Schema:** The target Dagger GraphQL schema before module and dependency types
  are merged into it.
- **Definitive_Go_SDK:** `github.com/dagger/dagger-go-sdk` at commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`.
- **Dispatch_Registry:** The deterministic generated mapping from normalized parent and
  function wire names to typed Rust invocation adapters.
- **Engine_Call_Adapter:** The narrow boundary that reads a real `FunctionCall` into a
  Call_Envelope and publishes its terminal outcome back to the engine.
- **Exact_Target:** The Target_Descriptor selected by
  `sdk/rust/completeness/target.json`, including Target_Revision, engine version
  `v1.0.0-beta.10`, Rust SDK version `1.0.0-beta.10`, Rust `1.97.1`, and edition 2024.
- **Explicit_Export:** A user-authored Rust declaration intentionally marked for the
  Dagger module surface rather than inferred merely from `pub` visibility.
- **Feature_6_Local_Checkpoint:** Any implementation checkpoint, feature-end check, or
  local regression command used to claim progress or closure for this feature.
- **Generated_Module_Assets:** The complete generator-owned Rust source and descriptor
  set required to register and dispatch one Cargo module.
- **Implementation_Closure:** The Feature 6 boundary at which the production authoring
  and dispatch implementation, direct Rust evidence, scoped hygiene, and security
  checks pass without constructing or executing a Dagger engine.
- **Injected_Module_Context:** A call-scoped, non-TypeDef function parameter that gives
  module code typed access to the active Core_Schema, self, dependencies,
  cancellation, and telemetry without reconnecting or using process-global state.
- **Local_Module_Type:** An explicitly exported object, interface, enum, or scalar
  declared by the current Rust module.
- **Module_Descriptor:** The canonical, target-bound, deterministic representation of
  discovered module types, functions, metadata, wire names, Rust symbols, source maps,
  state codecs, and dispatch entries.
- **Module_Introspection:** The canonical GraphQL introspection representation derived
  from the same Module_Descriptor as engine TypeDef registration.
- **Module_Root:** The one exported object whose normalized wire name identifies the
  module and whose constructor is exposed on `Query`.
- **Module_SDK:** The Rust SDK used for Dagger module authoring and execution.
- **Module_State:** The JSON representation of one local object passed through
  `FunctionCall.parent` and returned when a function produces a local object.
- **Normalization:** The deterministic mapping from Rust identifiers or explicit names
  to Dagger wire names and namespaced GraphQL type names.
- **Packaged_Self_Consumer:** A Rust-authored Dagger module fixture that resolves the
  Rust SDK only from the exact engine-packaged contents and uses that SDK to run a
  bounded Rust SDK build-and-test workflow without repository-relative dependencies.
- **Pure_Rust_Module_Harness:** Engine-free Rust fixtures that exercise production
  authoring analysis, descriptor projection, generated entrypoints, Dispatch_Registry,
  codecs, module context, concurrency, and failures through Call_Envelopes.
- **Result_Sink:** A single-assignment engine-independent boundary accepting either one
  canonical JSON value or one structured application error.
- **SDK_Signoff:** The later release-readiness gate that builds the Exact_Target engine
  and runs the complete engine-backed integration and conformance matrix.
- **Source_Coordinate:** A normalized Cargo-relative file, one-based line, and one-based
  column identifying an authored declaration or metadata item.
- **Target_Revision:** Dagger commit
  `25300124ca110612edc09c43f89cb5fad6028170`.
- **TypeDef_Projection:** The canonical engine registration and introspection values
  derived from a Module_Descriptor.
- **Visible_Schema:** The Core_Schema plus the current module, selected dependencies,
  and other engine-provided types visible to generation.
- **Wire_Name:** The exact case-sensitive Dagger or GraphQL name emitted into TypeDefs,
  call envelopes, selections, and JSON state.

## Target State

Rust modules use a small, documented Authoring_Surface built from normal Rust structs,
traits, enums, impl blocks, functions, rustdoc, and explicit Dagger metadata. Public
Rust visibility remains a language encapsulation choice; only Explicit_Exports become
engine API. Unsupported signatures and ambiguous names fail at their authored
Source_Coordinates. A user never maintains a second schema or switch statement.

The Authoring_Compiler evaluates the selected Cargo module using the declared target,
features, and configuration. It discovers every explicit root declaration and the
transitive Local_Module_Types referenced by exported fields and signatures. The same
canonical Module_Descriptor drives TypeDef registration, Module_Introspection, dispatch
generation, state codecs, documentation, and completeness evidence. File discovery
order, hash-map order, formatting, and repeated generation cannot change semantic or
byte output.

The Authoring_Surface is Rust-native. Module functions may be synchronous or
asynchronous and may return a value, unit, `Result<T, E>`, or `Result<(), E>`.
`Option<T>` represents Dagger omission or nullability where the engine type permits
it; it is never inferred from a Go pointer rule. `Vec<T>` preserves list shape.
Objects and interfaces cross function boundaries as typed handles backed by IDs,
while local object parent state remains ordinary serializable Rust state. Enums remain
closed Rust enums with exact wire serialization. The design may employ attributes,
derives, procedural macros, generated traits, or a combination, but one declaration
must not be interpreted differently by source analysis and Rust compilation.

The generated entrypoint converts the real engine call into a Call_Envelope and then
uses the same production dispatcher exercised by the Pure_Rust_Module_Harness.
Registration and invocation are distinct closed branches. Invocation reconstructs the
correct parent, decodes named arguments, injects the scoped module context, awaits the
user function, encodes exactly one terminal outcome, and closes the active session
without replacing a primary failure. Unknown parents, unknown functions, duplicate or
unknown arguments, invalid state, invalid values, application errors, panics,
cancellation, and result-publication failures remain distinguishable.

Module code reaches core, self, and dependency operations through the
Injected_Module_Context and Feature 4/7 generated bindings. Every selection reuses the
active session. The context is isolated per call and cannot be retained as serialized
Module_State. Concurrent calls cannot observe each other's arguments, state,
cancellation, telemetry, filesystem clone, result, error, or client lifecycle.

Generated_Module_Assets are refreshed only when a declared authoring input,
Visible_Schema, Exact_Target identity, or owning generator changes. Checkpoints consume
the checked assets and deterministic fixtures directly; they do not continuously
regenerate the whole SDK and do not build unrelated SDKs. Exact-engine execution
remains mandatory at SDK_Signoff, not at Feature 6 Implementation_Closure.

Feature 6 does not redesign Feature 5's engine SDK ABI or runtime-container builder,
generate complete standalone clients, publish crates, or claim full platform
conformance. It supplies the public module model and the general dispatcher those
later gates consume. SDK_Signoff additionally runs one Packaged_Self_Consumer to prove
the packaged authoring/runtime boundary. Feature 8 owns promotion of that bounded case
into exhaustive engine-backed consumer conformance; Feature 9 owns any claim that the
Rust SDK builds, tests, and releases itself.

## Evidence From Current Code

Repository citations use Target_Revision unless another revision is stated. Current
Rust citations describe `main` after Feature 5.

- **Engine call contract:** `core/typedef.go:2387-2513` defines `FunctionCall.name`,
  `parentName`, `parent`, ordered input argument objects, single-assignment return
  state, JSON number-preserving return decoding, null returns, and distinct value and
  error paths. `core/schema/module.go:2658-2671` exposes those terminal paths.
- **Current-node contract:** `core/typedef.go:2395-2409` retains the engine-side typed
  receiver for `Query.currentNode` while keeping top-level and constructor calls
  receiver-free. Parent JSON and current-node identity are related but distinct
  contracts.
- **Definitive module discovery:**
  `cmd/codegen/generator/go/templates/visit.go:35-176` visits module types and their
  transitive signature types, rejects unsupported foreign/unexported Go types, avoids
  duplicates, and delegates object, interface, and enum handling. Rust preserves the
  observable closure and diagnostics without interpreting `pub` as export.
- **Deterministic discovery:**
  `cmd/codegen/generator/go/templates/visit_determinism_test.go:16-134` proves stable
  type and method order across source-file permutations.
- **Type and optionality evidence:**
  `cmd/codegen/generator/go/templates/module_types.go` and
  `introspect_emit_test.go:52-626` establish primitive, list, object, interface, enum,
  unit, expected-type ID, default, optional, namespacing, and introspection shapes.
  Go pointer exceptions are not transplanted into Rust.
- **Object and state evidence:**
  `cmd/codegen/generator/go/templates/module_objects.go` emits public state and
  functions, keeps private fields out of TypeDefs, preserves state serialization, and
  converts local and imported interface fields. `module_objects_test.go:11-44` checks
  imported interface state reconstruction.
- **Interface evidence:**
  `cmd/codegen/generator/go/templates/module_interfaces.go` defines interface TypeDefs,
  concrete ID-backed handles, JSON conversion, and module-namespaced selections.
  `module_interfaces_test.go:33-141` proves discovery of a local interface using the
  generated Dagger object contract.
- **Enum evidence:** `cmd/codegen/generator/go/templates/module_enums.go` distinguishes
  enums from scalar aliases, strips a common member prefix where possible, carries
  docs, deprecation and source maps, and rejects unknown serialized members.
- **Constructor and dispatch evidence:**
  `cmd/codegen/generator/go/templates/modules.go` treats `New` as the root constructor,
  emits registration on the empty call name, reconstructs parents, decodes named
  inputs, dispatches by parent and function, and supports void, error-only, value-only,
  and value-plus-error returns. Rust preserves those outcomes without copying generated
  reflection or panic helpers.
- **Function metadata evidence:**
  `cmd/codegen/generator/go/templates/module_funcs.go:171-296,318-542` emits
  descriptions, cache policies, source maps, deprecation, check/generator/up flags,
  argument defaults, default paths, default addresses, ignore patterns, and optional
  arguments while excluding injected context from TypeDefs.
- **Introspection evidence:**
  `cmd/codegen/generator/go/templates/introspect_emit.go:523-562` creates the `Query`
  constructor and module type set from the same parsed model. The tests in
  `introspect_emit_test.go:367-414` prove parseability and merge round trips.
- **Module-global helper evidence:** `dag/dag.gen.go:13-265` at the
  Definitive_Go_SDK revision exposes 36 generated root and lifecycle helpers through a
  lazily initialized process-global client. The capabilities are authoritative; the
  singleton and panic-on-connect mechanism are not. Rust maps them exhaustively to
  Injected_Module_Context, entrypoint-owned lifecycle, or a reviewed inapplicability.
- **Common harness boundary:** `sdk-sdk.dang:91-289` at sdk-sdk commit
  `8c164424b7a8a37b33a77367ef7547490d5b87b5` declares installation,
  initialization, generation, and module-load checks. Those are Feature 5 lifecycle
  and SDK_Signoff claims, not evidence that a general authoring surface or dispatcher
  is complete.
- **Current generated protocol surface:**
  `sdk/rust/crates/dagger-sdk/src/gen/function_call.rs` exposes call name, parent,
  parent name, argument objects, `return_value`, and `return_error` on the shared
  session. `function_call_arg_value.rs` exposes each argument name and JSON value.
- **Current Feature 5 seam:**
  `sdk/rust/crates/dagger-codegen/src/engine/entrypoint.rs` renders only the fixed
  `RustSdkProtocolProbe`; `engine/model.rs` rejects every other entrypoint document.
  `sdk/rust/crates/dagger-sdk-engine/src/protocol.rs` supplies a pure closed model of
  that probe plus call isolation. Feature 6 generalizes these seams rather than adding
  a second runtime path.
- **Rust policy:** `sdk/rust/AGENTS.md`, `sdk/rust/ARCHITECTURE.md`, and
  `sdk/rust/CONTRIBUTING.md` require Rust-native public APIs, generated-artifact
  ownership, complete public documentation, typed errors, panic-free library paths,
  no unsafe code, target-bound evidence, and direct Rust checks.
- **Historical evidence only:** pull request #12229 proposes module support through a
  provisional procedural-macro API and Go-authored runtime. It is not merged at the
  Target_Revision and is not an authority for public syntax or structure.
- **Comparative self-hosting evidence only:** `MChorfa/dagger-zig` commit
  `1ae0304f173fc2f617960cd67a7daad1729357bb` routes its GitHub CI through the
  repository SDK via `.github/workflows/ci.yml` and `ci/pipeline/dagger.json`; its
  `tests/module_e2e.zig` exercises production TypeDef, dispatch, serde, and invocation
  plumbing without an engine; and `sdk/main.go` retains the Go bootstrap boundary.
  `docs/blog/v0.3.4-community-update.md` records how repository-relative SDK placement
  let offline work coexist with a completely broken packaged module runtime. These
  sources inform the Packaged_Self_Consumer and harness separation but do not change
  the engine, definitive Go SDK, sdk-sdk, or Rust-policy authority order.

## Completeness Contract Policy

### Existing Scope and Ownership Correction

The current ledger coarsely routes 96 capabilities to `feature-6`: 43 `go-codegen`
rows, 36 `go-client` module-global rows, and all 17 `sdk-contract-harness` rows. Their
lexicographically sorted compact-JSON Capability_ID list has scope digest
`sha256:7a6c0d55f2e189a64a880e45120552e18cc548adcb2c01aafe3475720c8cc44f`.
Ground-truth review shows that the 17 harness rows test SDK installation,
initialization, generation, options, dependency listing, engine-version reporting, and
scaffold loading. They belong to Feature 5's engine integration and SDK_Signoff, not
Feature 6 authoring or dispatch.

After that ownership correction, Feature 6 owns 79 existing capabilities with scope
digest `sha256:2e78e144a19072d7e85483d7496b987904c91f99f2b9f7e567af2f4b6163b7a9`.
This document does not change a status merely by restating or rerouting the scope.

| Authority | Rows | Current status | Feature 6 policy |
|---|---:|---|---|
| `go-codegen` | 43 | 43 Partial | Preserve module traversal, TypeDef/introspection shape, naming, metadata, interface/object/enum conversion, pragma semantics, and deterministic order through a Rust-native authoring compiler |
| `go-client` module globals | 36 | 36 Partial | Provide exhaustive capability mappings through Injected_Module_Context, entrypoint-owned lifecycle, or reviewed inapplicability without a process-global mutable client |
| **Feature 6 total** | **79** | **79 Partial** | Close only rows whose implementation and target-compatible direct evidence are capability-local and complete |
| `sdk-contract-harness` correction | 17 | 17 Missing | Route to Feature 5 and later SDK_Signoff; do not use broad lifecycle passes to close authoring or dispatch rows |

The 43 `go-codegen` rows are inventory anchors rather than a complete enumeration of
the public Rust contract. Most definitive Go module-parser helpers are unexported and
therefore absent from the Feature 1 declaration inventory. The Rust-specific policy
rows below make those observable obligations explicit instead of hiding them behind
one parser test.

### Rust Policy Capabilities Added by Feature 6

Feature 6 adds stable `rust-policy` capability rows for these omitted obligations:

```text
policy/rust-policy/module-explicit-export
policy/rust-policy/module-authoring-single-source
policy/rust-policy/module-source-discovery-closure
policy/rust-policy/module-source-coordinate-diagnostics
policy/rust-policy/module-wire-name-collision
policy/rust-policy/module-root-constructor
policy/rust-policy/module-object-state
policy/rust-policy/module-private-state
policy/rust-policy/module-interface-contract
policy/rust-policy/module-enum-contract
policy/rust-policy/module-custom-scalar-contract
policy/rust-policy/module-type-mapping-closure
policy/rust-policy/module-optional-default-semantics
policy/rust-policy/module-function-shape-closure
policy/rust-policy/module-function-metadata
policy/rust-policy/module-canonical-descriptor
policy/rust-policy/module-typedef-introspection-equivalence
policy/rust-policy/module-dispatch-totality
policy/rust-policy/module-parent-state-decoding
policy/rust-policy/module-named-argument-decoding
policy/rust-policy/module-object-id-reentry
policy/rust-policy/module-result-single-assignment
policy/rust-policy/module-application-error-reporting
policy/rust-policy/module-panic-containment
policy/rust-policy/module-active-session-context
policy/rust-policy/module-self-dependency-context
policy/rust-policy/module-call-isolation
policy/rust-policy/module-cancellation
policy/rust-policy/module-generated-asset-ownership
policy/rust-policy/module-change-triggered-regeneration
policy/rust-policy/module-engine-free-local-checkpoint
policy/rust-policy/module-exact-engine-signoff-boundary
```

The Feature 6 mapping artifact connects every existing and added capability to one
requirement, implementation subject, allowed final status, and evidence domain. Added
rows cannot replace or alias an existing authority row.

### Authority and Evidence Boundary

| Claim | Authority | Minimum implementation evidence | Engine evidence policy |
|---|---|---|---|
| A Rust declaration is a valid module export | Rust language rules plus the Authoring_Surface contract | Compile-pass/compile-fail fixture at an exact Source_Coordinate | Not required for local closure |
| A descriptor is complete and deterministic | Target Go module behaviour plus Rust policy | Production Authoring_Compiler over permuted multi-file fixtures and manifest closure | Not required for local closure |
| A TypeDef has the correct target shape | Engine TypeDef contract plus target module generator | Canonical structural comparison from Module_Descriptor to checked target fixtures | Final sign-off confirms registration |
| A call dispatches correctly | Engine `FunctionCall` contract plus Rust signature semantics | Production dispatcher through Call_Envelope fixtures | Final sign-off confirms adapter wiring |
| Core, self, and dependency calls reuse one session | Feature 2-4 session/query contracts plus target module behaviour | Fake transport observations from Injected_Module_Context | Final sign-off confirms nested session |
| A module-global Go helper is accounted for | Definitive_Go_SDK `dag/dag.gen.go` row | Exhaustive mapping to context method, lifecycle ownership, or inapplicability | No symbol-name parity requirement |
| Engine installation and generation work | Target engine SDK contract and sdk-sdk | Feature 5 evidence plus SDK_Signoff | Never inferred from Feature 6 unit tests |
| Feature 6 is implementation-complete | This specification and Rust repository policy | Complete Pure_Rust_Module_Harness plus scoped hygiene/security | No engine is started |
| Feature 6 is release-signed-off | Exact_Target engine and admitted ledger evidence | All local evidence plus exact-engine matrix | Required only at SDK_Signoff |

### Authoring and Discovery Policy

| Declaration | Export rule | Discovery closure | Invalid state |
|---|---|---|---|
| Module_Root | Exactly one explicit root object matching the module identity after Normalization | Root state, constructor, methods, and transitive signature types | Missing root, multiple roots, or incompatible normalized name |
| Object | Explicit object export or transitive reference from an exported signature/state field | Public state, exported methods, implemented local interfaces, and referenced types | Unsupported generic, lifetime, foreign type, or duplicate wire name |
| State field | Explicitly exposed or explicitly private persistent field under the object contract | Field type and nested wrappers | Unsupported codec, skipped field needed for construction, or collision |
| Interface | Explicit trait export and explicit object implementation relation | Exported interface methods and referenced types | Non-object-safe authoring shape, unsupported associated item, or incomplete implementation mapping |
| Enum | Explicit enum export | Every eligible variant and its metadata | Payload variant, duplicate wire value, empty unsupported enum, or unknown default |
| Custom scalar | Explicit transparent newtype contract | Underlying supported scalar codec | Non-transparent shape or unsupported underlying type |
| Constructor | Explicit root constructor or declared safe default construction | Arguments and referenced types | Multiple constructors, wrong return object, or fallible default hidden as infallible |
| Function | Explicit method/function export on one discovered object/interface | Arguments, result, metadata, and referenced types | Unsupported receiver, generic function, variadic ambiguity, or normalized collision |
| Ordinary `pub` item | Not exported solely by visibility | None unless transitively required by an Explicit_Export | No diagnostic unless it is referenced by an exported contract |
| Generated core/dependency type | Recognized through checked generated metadata | Reuse the generated type and ID contract | Impostor type or stale target metadata |

### Rust-to-Dagger Type Policy

| Rust authoring form | Dagger meaning | Input conversion | Output/state conversion |
|---|---|---|---|
| `String` | `String` scalar | Decode owned UTF-8 JSON string | Encode JSON string |
| `i64` | `Integer` scalar | Decode losslessly with range checks | Preserve the full target integer range |
| `bool` | `Boolean` scalar | Decode JSON boolean | Encode JSON boolean |
| `f64` | `Float` scalar | Decode finite target-supported JSON number | Encode target-supported JSON number |
| `()` | `Void` return | Not a data argument | Encode JSON `null` |
| Checked SDK scalar/newtype | Matching target scalar | Use its checked codec | Use its checked codec |
| Explicit custom scalar newtype | Named scalar | Use the declared transparent codec | Use the declared transparent codec |
| `Vec<T>` | Recursive non-null list shape selected by `T` | Decode elements in engine order | Preserve element order and wrapper shape |
| `Option<T>` | Explicit omission/nullability for the represented position | Distinguish absent or null where the call contract can represent it | Encode null only where the TypeDef permits it |
| Local object | Namespaced object TypeDef and Module_State | Reconstruct parent state or typed object value as appropriate | Encode declared object state |
| Local interface | Namespaced interface TypeDef | Decode concrete ID-backed implementation | Encode an accepted implementation identity |
| Local enum | Namespaced enum TypeDef | Validate exact wire member | Emit exact wire member |
| Generated core object/interface | `ID` plus target `@expectedType` semantics | Re-enter through generated typed binding | Resolve and encode engine ID |
| Generated dependency/self object/interface | Namespaced `ID` plus target type identity | Re-enter through the matching visible binding | Preserve module namespace and encode engine ID |
| `Result<T, E>` | Function outcome, not a TypeDef wrapper | Not valid as an argument or state field | Route `Ok(T)` to value and `Err(E)` to application error |
| Unsupported tuple, map, union, unconstrained generic, or borrowed state | No implicit mapping | Source-located rejection | No generated fallback or JSON erasure |

Nested `Option`, list, object, and interface shapes are validated against what the
target TypeDef and call protocols can represent. The Authoring_Compiler never accepts a
Rust shape by silently flattening a distinction or substituting `serde_json::Value`.

### Function Shape and Metadata Policy

| Rust function form | TypeDef result | Dispatch policy |
|---|---|---|
| synchronous `T` | TypeDef for `T` | Invoke without blocking an async executor worker on hidden I/O |
| asynchronous `T` | TypeDef for `T` | Await on the active call task |
| synchronous or asynchronous `()` | `Void` | Publish JSON `null` |
| synchronous or asynchronous `Result<T, E>` | TypeDef for `T` | Publish value or structured application error |
| synchronous or asynchronous `Result<(), E>` | `Void` | Publish JSON `null` or structured application error |
| constructor returning Module_Root | Query constructor result | Create root state and publish it |
| constructor returning `Result<Module_Root, E>` | Query constructor result | Publish root state or structured application error |
| injected Module_Context parameter | No TypeDef argument | Supply the Active_Call context before invocation |
| unsupported multiple semantic returns | No TypeDef | Source-located rejection |

| Metadata class | Supported target values | Validation policy |
|---|---|---|
| Documentation | Rustdoc on module, type, field, variant, function, and argument | Sanitize once and preserve semantic text |
| Source map | Cargo-relative file, one-based line and column | Derive from authored syntax; generated files cannot impersonate user source |
| Deprecation | Optional reason on object, field, enum member, function, or optional argument | Reject a deprecated required argument as target-incompatible |
| Cache | default, never, per-session, or validated TTL | Reject invalid TTL or conflicting declarations |
| Function role | ordinary, check, generator, or up as allowed by target combinations | Reject contradictory or target-unsupported combinations |
| Argument default | Canonical JSON value valid for the declared type | Validate enums by member and preserve explicit false/zero/empty values |
| Default path | Target string semantics | Mark the argument omittable without changing its Rust value type silently |
| Default address | Target string semantics | Mark the argument omittable without changing its Rust value type silently |
| Ignore patterns | Ordered list of target patterns | Preserve values and reject invalid metadata shape |
| Optionality | Explicit `Option<T>` or target metadata that supplies omission | Keep omission distinct from a supplied zero value |
| Private state | Not present in TypeDef; present in Module_State when declared persistent | Never expose the field merely because a codec needs it |
| Explicit wire rename | Valid target wire identifier | Apply before collision analysis and retain the Rust symbol separately |

### Registration and Dispatch Policy

| Phase | Input | Required result | Failure boundary |
|---|---|---|---|
| Source analysis | Cargo project, Exact_Target, Visible_Schema, declared authoring inputs | Canonical Module_Descriptor | Typed source diagnostic; no partial descriptor |
| Registration projection | Module_Descriptor | Module TypeDefs and equivalent Module_Introspection | Structural diagnostic; no engine call locally |
| Engine registration | Empty `FunctionCall.name` | Serve the complete projected module definition | Engine adapter error at SDK_Signoff |
| Invocation selection | Parent wire name plus function wire name | Exactly one Dispatch_Registry entry | Unknown/ambiguous dispatch diagnostic |
| Parent decode | Parent JSON plus selected local object codec | One reconstructed receiver or an empty root constructor input | Parent-state diagnostic |
| Argument decode | Named JSON arguments plus selected signature | One typed argument set | Missing, duplicate, unknown, or invalid-value diagnostic |
| Context injection | Active call/session identity | One scoped context excluded from TypeDefs and state | Context/session diagnostic |
| User execution | Receiver, arguments, context, cancellation | Value, application error, cancellation, or contained panic | Preserve the originating failure class |
| Result encode | Supported Rust value | Canonical JSON compatible with the TypeDef | Result-codec diagnostic |
| Result publish | Encoded value or structured error | Exactly one Result_Sink assignment | Publication diagnostic; no retry that can double-return |
| Close | Completed operation attempt | Session resources closed once | Preserve primary operation error over secondary close failure |

### Local Checkpoint and Regeneration Policy

| Activity | Feature 6 local policy | Deferred policy |
|---|---|---|
| Source discovery tests | Direct production Authoring_Compiler over fixture Cargo projects | Exact engine not permitted |
| TypeDef/introspection tests | Direct structural comparison from one Module_Descriptor | Registration smoke at SDK_Signoff |
| Compile-pass/fail authoring tests | Scoped Cargo/rustc fixture compilation | No whole-repository SDK build |
| Dispatch tests | Production Dispatch_Registry through Call_Envelopes and fake Result_Sinks | Engine adapter smoke at SDK_Signoff |
| Core/self/dependency tests | Fake transport on the active Shared_Session | Nested engine session at SDK_Signoff |
| Concurrency/cancellation tests | Deterministic Rust scheduling and property tests | Runtime-container isolation at SDK_Signoff |
| Regeneration | Explicit, scoped, input-digest-triggered refresh | No unconditional generation in ordinary test loops |
| Rust hygiene | Changed-crate fmt, locked tests, warning-denied clippy/rustdoc, security gates | Broader repository matrix at Feature 8/release |
| Dagger engine | Not constructed, started, or invoked | Exact_Target engine only at SDK_Signoff |

## Requirements

### Requirement 1: Capability Scope and Ground-Truth Accountability

**User Story:** As a release reviewer, I want every inherited and Rust-specific module
capability accounted for, so that an attractive authoring API cannot conceal missing
dispatch behaviour.

#### Acceptance Criteria

1. THE Feature 6 scope SHALL enumerate every retained `go-codegen` and module-global
   `go-client` Capability_ID.
2. THE ownership correction SHALL route all 17 `sdk-contract-harness` lifecycle rows
   to Feature 5 and SDK_Signoff.
3. THE Feature 6 scope SHALL add every declared Rust policy capability without
   replacing an authority capability.
4. THE Feature 6 mapping SHALL bind each capability to one requirement and one
   implementation subject.
5. THE Feature 6 mapping SHALL identify the allowed terminal status for each
   capability.
6. THE Feature 6 mapping SHALL identify the minimum evidence domain for each
   capability.
7. WHEN a Go mechanism has no idiomatic Rust analogue, THE mapping SHALL record a
   behavioural equivalent or justified inapplicability.
8. WHEN a capability status changes, THE evidence SHALL enumerate the exact proved
   Capability_ID set.
9. IF evidence is stale, skipped, failed, or target-incompatible, THEN THE completeness
   registry SHALL reject it.
10. THE rendered completeness report SHALL retain every unclosed Feature 6 blocker.

### Requirement 2: Explicit and Coherent Rust Authoring Surface

**User Story:** As a Rust developer, I want exports to be intentional and
compiler-checked, so that normal Rust visibility does not accidentally become a public
Dagger API.

#### Acceptance Criteria

1. THE Authoring_Surface SHALL require an Explicit_Export for every root object,
   object, interface, enum, custom scalar, constructor, and function exposed directly.
2. THE Authoring_Surface SHALL avoid treating an ordinary `pub` declaration as an
   export by itself.
3. THE Authoring_Surface SHALL use stable Rust syntax supported by the Exact_Target
   toolchain.
4. THE Authoring_Surface SHALL preserve normal Rust module visibility and privacy
   rules.
5. THE Authoring_Surface SHALL keep authored declarations type-checkable by standard
   Cargo and rustc tooling.
6. THE source-analysis interpretation SHALL agree with the compiled Rust
   interpretation of every authoring declaration.
7. WHEN authoring metadata is malformed, THE Authoring_Compiler SHALL report its exact
   Source_Coordinate.
8. WHEN authoring metadata is unknown for the selected target, THE Authoring_Compiler
   SHALL reject it rather than ignore it.
9. WHEN two authoring mechanisms specify the same property incompatibly, THE
   Authoring_Compiler SHALL report a conflict.
10. THE public authoring API SHALL document guarantees, restrictions, generated
    ownership, and failure behaviour.
11. THE public authoring API SHALL avoid requiring users to write a parallel schema or
    dispatch table.
12. THE public authoring API SHALL avoid exposing Go reflection, Go pointer, or Go
    package concepts.

### Requirement 3: Complete and Deterministic Source Discovery

**User Story:** As a module author, I want all referenced module types discovered
reliably, so that moving Rust code between files does not change the Dagger API.

#### Acceptance Criteria

1. WHEN a Cargo module is analyzed, THE Authoring_Compiler SHALL discover the one
   Module_Root and every direct Explicit_Export.
2. WHEN an exported field or signature references a Local_Module_Type, THE
   Authoring_Compiler SHALL include that type in the discovery closure.
3. WHEN a newly discovered type references another Local_Module_Type, THE
   Authoring_Compiler SHALL continue discovery to a fixed point.
4. WHEN the same type is reached by multiple paths, THE Authoring_Compiler SHALL emit
   it exactly once.
5. WHEN equivalent source files are presented in a different discovery order, THE
   Module_Descriptor SHALL remain byte-identical.
6. WHEN exported methods are declared across multiple impl blocks, THE
   Authoring_Compiler SHALL produce deterministic method order.
7. WHEN target configuration excludes a declaration, THE Authoring_Compiler SHALL
   omit it under the same declared Cargo configuration used for compilation.
8. WHEN an exported contract references an unsupported foreign type, THE
   Authoring_Compiler SHALL identify the reference Source_Coordinate.
9. WHEN an exported contract references a checked generated core or dependency type,
   THE Authoring_Compiler SHALL reuse its target identity.
10. WHEN a generated type lacks matching target provenance, THE Authoring_Compiler
    SHALL reject it as stale or foreign.
11. IF the module has no valid root, THEN THE Authoring_Compiler SHALL return a typed
    missing-root diagnostic.
12. IF the module has multiple valid roots, THEN THE Authoring_Compiler SHALL return a
    typed ambiguous-root diagnostic.
13. THE Authoring_Compiler SHALL avoid executing user code during source discovery.
14. THE Authoring_Compiler SHALL avoid network access and a Dagger engine during source
    discovery.

### Requirement 4: Objects, State, and Root Construction

**User Story:** As a Rust module author, I want ordinary typed state and constructors,
so that Dagger can reconstruct my objects without unsafe initialization or hidden
zero-value assumptions.

#### Acceptance Criteria

1. WHEN an object is exported, THE TypeDef_Projection SHALL emit its normalized object
   name, documentation, deprecation, source map, fields, functions, and implemented
   interfaces.
2. WHEN an object field is exposed, THE TypeDef_Projection SHALL emit its Wire_Name,
   type, documentation, deprecation, and source map.
3. WHEN an object field is declared private persistent state, THE TypeDef_Projection
   SHALL omit it from the public TypeDef.
4. WHEN an object field is declared private persistent state, THE state codec SHALL
   preserve it across parent reconstruction.
5. WHEN an object field is ordinary Rust-private implementation detail and not
   persistent state, THE state codec SHALL omit it.
6. WHEN a field has an explicit wire rename, THE state codec SHALL use the same
   Wire_Name as its descriptor.
7. WHEN a local interface value appears in state, THE state codec SHALL preserve its
   concrete target identity through a supported ID-backed representation.
8. WHEN a generated core or dependency handle appears in state, THE state codec SHALL
   preserve its typed engine identity.
9. WHEN object state cannot be encoded or decoded losslessly, THE Authoring_Compiler
   SHALL reject the field contract at its Source_Coordinate.
10. THE Module_Root SHALL have exactly one explicit constructor or one declared safe
    default-construction path.
11. WHEN a default-construction path is selected, THE generated code SHALL construct
    only values valid under normal Rust initialization rules.
12. WHEN a constructor is explicit, THE Authoring_Compiler SHALL require its successful
    value to be the Module_Root.
13. WHEN a constructor is fallible, THE dispatcher SHALL preserve its application
    error path.
14. WHEN a constructor is exposed, THE Module_Introspection SHALL place it on `Query`
    under the normalized module name.
15. THE generated implementation SHALL avoid unsafe, zeroed, or uninitialized object
    construction.

### Requirement 5: Interfaces, Enums, and Custom Scalars

**User Story:** As a Rust developer, I want traits, enums, and newtypes to retain their
language guarantees, so that module APIs remain strongly typed.

#### Acceptance Criteria

1. WHEN an interface trait is exported, THE TypeDef_Projection SHALL emit its
   namespaced interface name, documentation, source map, and exported functions.
2. WHEN a local object implements an exported interface, THE Module_Descriptor SHALL
   record that implementation relationship exactly once.
3. WHEN an interface value crosses a call boundary, THE generated codec SHALL preserve
   the accepted concrete object's target identity.
4. WHEN a core or dependency interface is referenced, THE generated adapter SHALL use
   the checked visible binding rather than invent a local interface.
5. WHEN an exported trait contains an unsupported associated item or generic method,
   THE Authoring_Compiler SHALL return a source-located diagnostic.
6. WHEN an enum is exported, THE TypeDef_Projection SHALL emit every supported unit
   variant with its Wire_Name, documentation, deprecation, and source map.
7. WHEN enum variants share a removable conventional type-name prefix, THE
   Normalization policy SHALL produce stable concise member names.
8. WHEN an enum member is decoded, THE enum codec SHALL reject every unknown wire
   value.
9. WHEN an enum member is encoded, THE enum codec SHALL emit its exact declared wire
   value.
10. WHEN an enum variant carries payload data, THE Authoring_Compiler SHALL reject it
    as unsupported by the target enum contract.
11. WHEN a custom scalar newtype is exported, THE Authoring_Compiler SHALL require one
    transparent supported scalar representation.
12. WHEN a custom scalar codec is not lossless, THE Authoring_Compiler SHALL reject its
    declaration.

### Requirement 6: Exhaustive Rust Type and Wrapper Semantics

**User Story:** As a module author, I want Rust types to map predictably to Dagger
types, so that nullability, lists, values, and object identities survive round trips.

#### Acceptance Criteria

1. THE Authoring_Compiler SHALL implement every row of the Rust-to-Dagger Type Policy.
2. THE Feature 6 type manifest SHALL classify every Go-supported module input and
   output behaviour as Implemented, Idiomatic_Equivalent, or justified Inapplicable.
3. WHEN `String`, `i64`, `bool`, or `f64` is used, THE TypeDef_Projection SHALL emit the
   matching target scalar.
4. WHEN unit is returned, THE result codec SHALL emit target Void as JSON `null`.
5. WHEN `Vec<T>` is used, THE TypeDef_Projection SHALL preserve its recursive element
   wrapper shape.
6. WHEN `Option<T>` is used in a representable position, THE TypeDef_Projection SHALL
   preserve explicit omission or nullability semantics.
7. WHEN a supplied optional value is `false`, zero, an empty string, or an empty list,
   THE argument codec SHALL preserve it as present.
8. WHEN a default is declared, THE Authoring_Compiler SHALL validate its canonical JSON
   value against the declared type.
9. WHEN an enum default is declared, THE TypeDef_Projection SHALL emit the normalized
   enum member name.
10. WHEN a local object or interface appears in a signature, THE TypeDef_Projection
    SHALL emit its namespaced type identity.
11. WHEN a core, self, or dependency object appears as an argument, THE TypeDef_Projection
    SHALL preserve target expected-type ID semantics.
12. WHEN a core, self, or dependency object is returned, THE result codec SHALL resolve
    and encode its engine ID.
13. WHEN a wrapper shape cannot be represented without losing a Rust distinction, THE
    Authoring_Compiler SHALL reject it at the Source_Coordinate.
14. WHEN a numeric value exceeds the supported target range, THE codec SHALL return a
    typed range diagnostic.
15. WHEN a JSON value has the wrong scalar or structural kind, THE codec SHALL return a
    typed value diagnostic.
16. THE generated type path SHALL avoid an untyped JSON fallback for a supported typed
    contract.

### Requirement 7: Functions, Arguments, and Target Metadata

**User Story:** As a module author, I want sync and async Rust functions with complete
target metadata, so that Dagger presents the API I actually declared.

#### Acceptance Criteria

1. WHEN a synchronous value function is exported, THE TypeDef_Projection SHALL expose
   its declared result type.
2. WHEN an asynchronous value function is exported, THE TypeDef_Projection SHALL expose
   the same engine function model.
3. WHEN a function returns `Result<T, E>`, THE TypeDef_Projection SHALL expose `T` as
   the result type.
4. WHEN a function returns unit or `Result<(), E>`, THE TypeDef_Projection SHALL expose
   Void as the result type.
5. WHEN an Injected_Module_Context parameter is present, THE TypeDef_Projection SHALL
   omit it from engine arguments.
6. WHEN a data argument is present, THE TypeDef_Projection SHALL emit its Wire_Name,
   type, documentation, source map, optionality, defaults, ignore patterns, and
   deprecation metadata.
7. WHEN function rustdoc is present, THE TypeDef_Projection SHALL preserve its
   sanitized semantic text.
8. WHEN target cache metadata is present, THE TypeDef_Projection SHALL preserve default,
   never, per-session, or validated TTL semantics.
9. WHEN check, generator, or up metadata is present, THE TypeDef_Projection SHALL emit
   the matching target role.
10. WHEN function deprecation is present, THE TypeDef_Projection SHALL preserve its
    reason.
11. WHEN a required argument is marked deprecated, THE Authoring_Compiler SHALL reject
    the target-incompatible declaration.
12. WHEN default-path or default-address metadata is present, THE TypeDef_Projection
    SHALL preserve the target string and omission semantics.
13. WHEN ignore metadata is present, THE TypeDef_Projection SHALL preserve the ordered
    pattern values.
14. WHEN explicit wire names collide after Normalization, THE Authoring_Compiler SHALL
    identify every conflicting Source_Coordinate.
15. WHEN a function is generic or has an unsupported receiver, THE Authoring_Compiler
    SHALL return a source-located signature diagnostic.
16. WHEN function metadata has a target-invalid combination, THE Authoring_Compiler
    SHALL reject it rather than choose precedence silently.
17. THE generated async path SHALL avoid blocking the runtime executor for ordinary
    function execution.

### Requirement 8: Canonical TypeDef and Introspection Projection

**User Story:** As an engine integrator, I want registration and introspection derived
from one model, so that generated clients and runtime dispatch cannot disagree about
the module surface.

#### Acceptance Criteria

1. THE Authoring_Compiler SHALL produce one canonical Module_Descriptor for each valid
   authoring input set.
2. THE Module_Descriptor SHALL include every discovered type, field, function,
   argument, enum member, metadata item, codec, and dispatch coordinate.
3. THE Module_Descriptor SHALL retain both Rust symbols and exact Wire_Names.
4. THE Module_Descriptor SHALL retain target, source-input, Visible_Schema, and
   authoring-surface identities.
5. THE TypeDef_Projection SHALL derive registration values only from the
   Module_Descriptor.
6. THE Module_Introspection SHALL derive its schema only from the same
   Module_Descriptor.
7. WHEN registration and introspection represent the same item, THE structural type,
   wrapper, metadata, and Wire_Name SHALL agree.
8. THE Module_Introspection SHALL contain one `Query` type with the Module_Root
   constructor when a root is valid.
9. WHEN a local type conflicts with a core or dependency type after target
   namespacing, THE Authoring_Compiler SHALL return a collision diagnostic.
10. WHEN equivalent authoring inputs are repeated, THE Module_Descriptor SHALL remain
    byte-identical.
11. WHEN source-file or declaration order changes without semantic change, THE
    Module_Descriptor SHALL remain byte-identical.
12. WHEN one semantic authoring input changes, THE descriptor provenance SHALL identify
    the changed input domain.
13. IF projection fails, THEN THE generator SHALL publish no partial TypeDef or
    introspection artifact.

### Requirement 9: Total and Typed Dispatch Selection

**User Story:** As a module consumer, I want each engine call to select exactly one
Rust function, so that malformed or unknown calls never fall through unpredictably.

#### Acceptance Criteria

1. THE generated entrypoint SHALL distinguish registration from invocation using the
   target empty-name contract.
2. WHEN registration is requested, THE generated entrypoint SHALL serve the complete
   TypeDef_Projection.
3. WHEN invocation is requested, THE Engine_Call_Adapter SHALL construct one
   Call_Envelope from the Active_Call.
4. THE Dispatch_Registry SHALL contain exactly one entry for every callable
   parent-and-function Wire_Name pair.
5. WHEN a valid parent-and-function pair is supplied, THE Dispatch_Registry SHALL
   select its uniquely matching typed adapter.
6. WHEN a parent Wire_Name is unknown, THE dispatcher SHALL return a typed unknown-parent
   diagnostic.
7. WHEN a function Wire_Name is unknown for a valid parent, THE dispatcher SHALL return
   a typed unknown-function diagnostic.
8. WHEN a dispatch coordinate is duplicated, THE Authoring_Compiler SHALL reject the
   module before entrypoint generation.
9. WHEN a constructor call is selected, THE dispatcher SHALL avoid reconstructing an
   instance parent.
10. WHEN an instance method call is selected, THE dispatcher SHALL require the matching
    parent object type.
11. THE production entrypoint SHALL use the same Dispatch_Registry implementation as
    the Pure_Rust_Module_Harness.
12. THE dispatcher SHALL avoid reflection, stringly typed fallback invocation, and
    user-authored switch statements.

### Requirement 10: Parent, Argument, and Handle Reconstruction

**User Story:** As a module author, I want typed values reconstructed before my code
runs, so that malformed engine input cannot enter ordinary Rust functions.

#### Acceptance Criteria

1. WHEN an instance call is selected, THE dispatcher SHALL decode `FunctionCall.parent`
   through the selected object's state codec.
2. WHEN parent JSON is malformed, THE dispatcher SHALL return a typed parent-state
   diagnostic.
3. WHEN parent JSON names an incompatible object shape, THE dispatcher SHALL return a
   typed parent-type diagnostic.
4. WHEN call arguments are read, THE dispatcher SHALL index them by exact Wire_Name.
5. WHEN every required argument is present exactly once, THE dispatcher SHALL decode
   them through their declared codecs.
6. WHEN an optional argument is omitted, THE dispatcher SHALL construct its declared
   omitted Rust representation.
7. WHEN a target default is resolved into the call input, THE dispatcher SHALL decode
   it through the same typed codec as an explicit value.
8. WHEN a required argument is missing, THE dispatcher SHALL return a typed
   missing-argument diagnostic.
9. WHEN an argument is duplicated, THE dispatcher SHALL return a typed
   duplicate-argument diagnostic.
10. WHEN an argument is unknown, THE dispatcher SHALL return a typed unknown-argument
    diagnostic.
11. WHEN an argument value is malformed, THE dispatcher SHALL identify the parent,
    function, argument Wire_Name, and value failure class.
12. WHEN a core object ID is supplied, THE dispatcher SHALL re-enter through its
    Feature 4 generated binding on the active session.
13. WHEN a self or dependency object ID is supplied, THE dispatcher SHALL re-enter
    through its matching namespaced generated binding.
14. WHEN an interface ID is supplied, THE dispatcher SHALL preserve its concrete
    visible type identity.
15. THE dispatcher SHALL complete all validation before invoking user code.

### Requirement 11: Values, Application Errors, and Panic Containment

**User Story:** As a module consumer, I want one reliable terminal outcome, so that a
panic or conversion error cannot corrupt the active engine call.

#### Acceptance Criteria

1. WHEN a function returns a supported value, THE dispatcher SHALL encode it as
   canonical JSON compatible with its declared TypeDef.
2. WHEN a function returns unit, THE dispatcher SHALL encode JSON `null`.
3. WHEN a function returns a local object, THE dispatcher SHALL encode its declared
   Module_State.
4. WHEN a function returns a core, self, or dependency handle, THE dispatcher SHALL
   resolve and encode its engine ID.
5. WHEN a function returns an interface value, THE dispatcher SHALL preserve an
   accepted concrete identity.
6. WHEN a function returns an application error, THE dispatcher SHALL convert it to
   the engine's structured error path.
7. WHEN result encoding fails, THE dispatcher SHALL retain the selected function and
   result-type coordinates in the diagnostic.
8. WHEN user code panics, THE generated runtime boundary SHALL contain the unwind
   before it crosses the engine protocol boundary.
9. WHEN a panic is contained, THE dispatcher SHALL report a credential-safe module
   failure without exposing an arbitrary payload.
10. WHEN a Result_Sink accepts a terminal outcome, THE dispatcher SHALL avoid a second
    publication attempt.
11. WHEN result publication fails, THE Engine_Call_Adapter SHALL preserve the
    underlying query or transport error as its source.
12. WHEN user execution fails and session close also fails, THE entrypoint SHALL retain
    the user or protocol failure as primary.
13. WHEN user execution succeeds and session close fails, THE entrypoint SHALL return
    the close failure.
14. THE production library path SHALL avoid unchecked unwrap, deliberate panic, and
    unsafe Rust.

### Requirement 12: Active-Session Module Context

**User Story:** As a Rust module author, I want typed access to Dagger inside my
function, so that core, self, and dependency calls are convenient without global
mutable state or reconnection.

#### Acceptance Criteria

1. WHEN a function requests Injected_Module_Context, THE dispatcher SHALL supply the
   context for the Active_Call.
2. THE Injected_Module_Context SHALL reuse the Shared_Session established by the
   generated entrypoint.
3. THE Injected_Module_Context SHALL expose the complete Feature 4 Core_Schema query
   surface available to the module.
4. THE Injected_Module_Context SHALL expose current-call, current-module, current-node,
   current-type-def, current-workspace, version, and default-platform operations when
   present in the Exact_Target.
5. WHEN self bindings are present in Visible_Schema, THE Injected_Module_Context SHALL
   preserve their target module namespace.
6. WHEN dependency bindings are present in Visible_Schema, THE
   Injected_Module_Context SHALL preserve each dependency namespace.
7. WHEN a context operation builds a lazy object selection, THE resulting handle SHALL
   retain the same Shared_Session lease.
8. WHEN a context operation executes immediately, THE operation SHALL use the same
   Active_Call cancellation and telemetry scope.
9. THE module-global capability mapping SHALL account for all 36 definitive Go helper
   rows.
10. WHEN a Go helper is a Core_Schema root operation, THE mapping SHALL identify its
    typed Module_Context path.
11. WHEN a Go helper owns connection close, THE mapping SHALL assign lifecycle to the
    generated entrypoint rather than user module code.
12. WHEN a Go helper has no sound Rust symbol, THE mapping SHALL record a reviewed
    behavioural inapplicability.
13. THE Injected_Module_Context SHALL avoid a process-global mutable client.
14. THE Injected_Module_Context SHALL avoid reconnecting to the engine for an active
    call.
15. THE Module_State codec SHALL reject serialization of Injected_Module_Context.

### Requirement 13: Concurrency, Cancellation, and Call Isolation

**User Story:** As an engine operator, I want concurrent Rust module calls isolated,
so that one call cannot leak state, errors, or cancellation into another.

#### Acceptance Criteria

1. WHEN multiple calls execute concurrently, THE runtime SHALL allocate a distinct
   Call_Envelope and Result_Sink for each call.
2. WHEN multiple calls execute concurrently, THE runtime SHALL isolate parent state and
   decoded arguments by call identity.
3. WHEN multiple calls execute concurrently, THE runtime SHALL isolate module context,
   telemetry, and cancellation by call identity.
4. WHEN one call mutates its local receiver value, THE runtime SHALL keep every sibling
   receiver unchanged.
5. WHEN one call fails, THE runtime SHALL keep sibling result and error paths usable.
6. WHEN one call panics, THE runtime SHALL keep sibling tasks and protocol state usable.
7. WHEN one call is cancelled, THE runtime SHALL stop or abandon its user future
   according to the documented cancellation contract.
8. WHEN one call is cancelled, THE runtime SHALL avoid publishing a successful value
   afterward.
9. WHEN cancellation races with result publication, THE Result_Sink SHALL resolve the
   race through one deterministic terminal state.
10. WHEN a call starts child work owned by the SDK, THE runtime SHALL terminate and
    reap that work on cancellation.
11. WHEN a call completes, THE runtime SHALL release its session lease and call-scoped
    resources exactly once.
12. THE runtime SHALL avoid process-global parent state, argument state, result state,
    marshal context, and cancellation state.

### Requirement 14: Typed Diagnostics and Failure-Atomic Generation

**User Story:** As a module developer, I want failures tied to my Rust source and
operation phase, so that repair does not require reading generated code.

#### Acceptance Criteria

1. THE Feature 6 diagnostic model SHALL distinguish discovery, metadata, type,
   naming, descriptor, projection, state, argument, dispatch, execution, result,
   publication, cancellation, and session failures.
2. WHEN a failure originates in authored Rust, THE diagnostic SHALL include its
   Source_Coordinate.
3. WHEN a failure originates in a wire contract, THE diagnostic SHALL include the
   parent, function, field, argument, or type Wire_Name.
4. WHEN a failure wraps Cargo, rustc, filesystem, codec, query, or transport behaviour,
   THE diagnostic SHALL preserve the underlying error as a source.
5. WHEN multiple diagnostics are independent, THE Authoring_Compiler SHALL render them
   in stable Source_Coordinate and code order.
6. WHEN a diagnostic is rendered, THE renderer SHALL exclude secrets, tokens,
   credential-bearing URLs, arbitrary panic payloads, and unbounded user values.
7. IF source discovery fails, THEN THE Authoring_Compiler SHALL publish no partial
   Module_Descriptor.
8. IF descriptor projection fails, THEN THE generator SHALL publish no partial
   Generated_Module_Assets.
9. IF entrypoint generation fails, THEN THE generator SHALL preserve the prior valid
   owned asset set.
10. WHEN a generated diagnostic points back to authored code, THE diagnostic SHALL
    avoid making a generated file the primary repair coordinate.
11. THE Feature 6 production path SHALL deny unsafe code.
12. THE Feature 6 production path SHALL avoid panic and unchecked unwrap outside a
    contained user-panic boundary.

### Requirement 15: Generated Assets and Scoped Regeneration

**User Story:** As a contributor, I want module generation to be owned and
change-triggered, so that checkpoints do not spend hours rebuilding unchanged SDK
surfaces.

#### Acceptance Criteria

1. THE Generated_Module_Assets manifest SHALL enumerate every Feature 6-owned output
   path and digest.
2. THE Generated_Module_Assets SHALL carry Exact_Target and authoring-input
   provenance.
3. THE generator SHALL distinguish generator-owned assets from user-owned Rust source.
4. WHEN generation succeeds, THE publisher SHALL replace only paths owned by the prior
   compatible manifest.
5. WHEN an owned generated path becomes obsolete, THE publisher SHALL remove it only
   after validating prior ownership.
6. WHEN an unknown or user-owned path collides, THE publisher SHALL return a typed
   ownership diagnostic.
7. WHEN authoring inputs and generator identity are unchanged, THE generation check
   SHALL consume the checked assets without rerunning semantic generation.
8. WHEN an authoring input changes, THE regeneration selector SHALL refresh only the
   affected module asset domain.
9. WHEN Visible_Schema changes, THE regeneration selector SHALL refresh only assets
   whose descriptor or bindings consume that schema.
10. WHEN the Exact_Target or owning generator changes, THE regeneration selector SHALL
    invalidate the affected provenance domain explicitly.
11. WHEN scoped regeneration repeats over identical inputs, THE complete owned output
    set SHALL remain byte-identical.
12. THE ordinary Feature 6 test loop SHALL avoid regenerating the complete Core_Schema
    SDK surface.
13. THE ordinary Feature 6 test loop SHALL avoid building unrelated language SDKs.

### Requirement 16: Engine-Free Local Checkpoints

**User Story:** As a contributor, I want fast deterministic local checkpoints, so that
most defects are found in Rust before expensive engine sign-off.

#### Acceptance Criteria

1. THE Pure_Rust_Module_Harness SHALL exercise the production Authoring_Compiler.
2. THE Pure_Rust_Module_Harness SHALL exercise the production Module_Descriptor and
   TypeDef_Projection.
3. THE Pure_Rust_Module_Harness SHALL exercise the production Dispatch_Registry.
4. THE Pure_Rust_Module_Harness SHALL exercise the production state, argument, handle,
   result, and application-error codecs.
5. THE Pure_Rust_Module_Harness SHALL exercise the production Injected_Module_Context
   through a fake Shared_Session transport.
6. THE Pure_Rust_Module_Harness SHALL exercise registration and invocation through
   engine-independent Call_Envelopes.
7. THE Pure_Rust_Module_Harness SHALL cover sync, async, unit, value, fallible,
   constructor, stateful, core, self, dependency, interface, enum, optional, default,
   panic, cancellation, and concurrent-call cases.
8. THE Pure_Rust_Module_Harness SHALL cover malformed source, metadata, names, state,
   arguments, IDs, results, dispatch coordinates, and duplicate terminal outcomes.
9. THE Feature 6 compile harness SHALL include representative compile-pass fixtures.
10. THE Feature 6 compile harness SHALL include Source_Coordinate-checked compile-fail
    fixtures.
11. THE Feature 6 property suite SHALL vary declaration order, file order, wrapper
    shape, argument order, failure order, and call interleaving.
12. THE Feature_6_Local_Checkpoint SHALL execute no Dagger engine process.
13. THE Feature_6_Local_Checkpoint SHALL execute no Dagger module invocation.
14. THE Feature_6_Local_Checkpoint SHALL execute no network-backed engine graph.
15. THE Feature_6_Local_Checkpoint SHALL avoid rebuilding unrelated SDKs.
16. THE Feature_6_Local_Checkpoint SHALL use checked generated assets unless an owning
    input digest changed.
17. THE Feature_6_Local_Checkpoint SHALL report its scoped commands, elapsed time, and
    generated-asset decision.
18. IF a proposed pre-signoff check needs an engine, THEN THE exception record SHALL
    identify the exact contract that the Pure_Rust_Module_Harness cannot model.
19. IF a proposed pre-signoff check needs an engine, THEN THE exception record SHALL
    receive explicit maintainer approval before execution.

### Requirement 17: Implementation Closure and SDK Sign-off Boundary

**User Story:** As a release engineer, I want local implementation closure separated
from exact-engine sign-off, so that fast development evidence is neither undervalued
nor misrepresented as end-to-end conformance.

#### Acceptance Criteria

1. THE Feature 6 Implementation_Closure SHALL require the complete
   Pure_Rust_Module_Harness.
2. THE Feature 6 Implementation_Closure SHALL require all compile-pass and compile-fail
   authoring fixtures.
3. THE Feature 6 Implementation_Closure SHALL require formatting checks for every
   changed Rust source and generated asset.
4. THE Feature 6 Implementation_Closure SHALL require locked tests for every changed
   Rust crate.
5. THE Feature 6 Implementation_Closure SHALL require warning-denied clippy and
   rustdoc for the changed public surface.
6. THE Feature 6 Implementation_Closure SHALL require the repository Rust security
   and dependency-policy checks.
7. THE Feature 6 Implementation_Closure SHALL require generated-asset drift and
   ownership checks.
8. THE Feature 6 Implementation_Closure SHALL avoid constructing or executing a
   Dagger engine.
9. THE SDK_Signoff suite SHALL build an engine from the exact Target_Revision.
10. THE SDK_Signoff suite SHALL register the complete Feature 6 TypeDef_Projection
    through the real Feature 5 adapter.
11. THE SDK_Signoff suite SHALL invoke the Packaged_Self_Consumer plus representative
    constructor, sync, async, stateful, core, self, dependency, interface, enum,
    default, error, panic, cancellation, and concurrent-call cases.
12. THE SDK_Signoff suite SHALL execute the applicable pinned sdk-sdk checks without
    treating them as exhaustive authoring coverage.
13. WHEN SDK_Signoff observations pass, THE evidence producer SHALL bind them to the
    engine revision, engine version, schema digest, Rust SDK source digest, toolchain,
    generated-asset digest, and packaged-runtime digest.
14. WHEN SDK_Signoff observations pass, THE evidence producer SHALL enumerate only the
    Capability_IDs directly proved by each observation.
15. IF an engine observation is produced against another target or stale generated
    asset set, THEN THE evidence registry SHALL reject it.
16. IF a local harness result claims engine registration, runtime-container, or
    cross-platform conformance, THEN THE evidence registry SHALL reject it.
17. IF an engine smoke result claims exhaustive source, type, or dispatch closure,
    THEN THE evidence registry SHALL reject it.
18. THE final Feature 6 report SHALL distinguish Implementation_Closure from
    SDK_Signoff status.

## Out of Scope

- Redesigning Feature 5's engine-side SDK interfaces, operation transport, workspace
  installation, Cargo project adoption, or Runtime_Container construction.
- Generating complete standalone Core_Schema, module, or dependency client projects;
  those are Feature 7 responsibilities.
- Claiming all target integration tests, platforms, architectures, or supported engine
  versions; those are Feature 8 responsibilities.
- Publishing crates, selecting final stable package versions, writing migration
  material, or cutting the `1.0.0` release; those are Feature 9 responsibilities.
- Running ordinary Feature 6 checkpoints through a self-hosted Dagger pipeline or
  claiming complete build, test, conformance, and release self-hosting; Features 8 and
  9 own those engine-backed and release-wide gates.
- Copying Go package globals, pointer optionality, reflection, variadic option structs,
  generated panic conventions, or PR #12229's provisional public API into Rust.
- Starting a Dagger engine during ordinary Feature 6 development or checkpoints.

## Iteration and Feedback Notes

- Requirements workflow selected: feature, requirements-first.
- The umbrella's nine Feature 6 criteria are expanded here into source discovery,
  public authoring, TypeDef/introspection equivalence, typed dispatch, module context,
  isolation, diagnostics, generated ownership, and evidence boundaries.
- Ground-truth review corrects the coarse 17-row sdk-sdk allocation from Feature 6 to
  Feature 5 and SDK_Signoff. Broad lifecycle checks remain authoritative within their
  declared scope but do not prove module authoring or dispatch.
- The 36 Definitive_Go_SDK `dag` helpers are retained as capability obligations. Their
  public Rust equivalent is a call-scoped module context plus entrypoint-owned
  lifecycle, not a global mutable singleton.
- `Option<T>` expresses Rust optionality directly. Go pointer exceptions remain
  behavioural evidence only where required by the engine wire contract.
- Feature 5's fixed protocol probe is a proven seam, not a foundation for a second
  dispatcher. Feature 6 generalizes the same operation and runtime boundary.
- The dagger-zig self-consumer and offline module test validate the selected separation
  as comparative evidence. Feature 6 adds only a bounded packaged consumer case at
  SDK_Signoff; it does not move an engine into local checkpoints or claim release
  self-hosting.
- Local checkpoints are deliberately engine-free and change-triggered. Exact-engine
  execution remains mandatory at SDK_Signoff and cannot be replaced by unit evidence.
- Design remains consent-gated. It must select the precise attribute, derive,
  procedural-macro, generated-trait, descriptor, and fixture architecture while
  preserving every requirement above.
