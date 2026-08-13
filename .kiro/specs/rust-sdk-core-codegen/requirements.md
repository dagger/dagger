# Requirements Document: Rust SDK Core Schema Code Generation

## Introduction

This specification defines Feature 4 of the approved
`rust-sdk-complete-implementation` umbrella: a target-pinned, exhaustive, and
idiomatic Rust projection of Dagger's Core_Schema. It turns the broad but weakly
verified generated client already present in `dagger-sdk` into a reproducible public
contract whose schema coverage, Rust mapping, generated source, and completeness
evidence agree.

The engine schema at Dagger commit
`25300124ca110612edc09c43f89cb5fad6028170` is authoritative for wire names, type
wrappers, defaults, directives, deprecations, and the available Core_Schema surface.
The generated library at `github.com/dagger/dagger-go-sdk` commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5` is authoritative for the observable
generated-client behaviours that the schema alone does not settle: lazy object
selection, object-ID conversion, typed re-entry, option omission, enum validation,
and public capability coverage. Rust ownership, type-system constraints, naming,
traits, fallible conversion, documentation, and async composition define the public
shape. Go pointers, variadic option structs, zero-value tests, aliases, and helper
methods are evidence of behaviour, not templates for Rust source.

Feature 4 depends on Feature 1's executable Completeness_Ledger and reuses the owned
Shared_Session, Query_Builder, Loadable contract, Into_ID conversion, and typed query
failures delivered by Features 2 and 3. Feature 4 owns the pure Core_Schema-to-Rust
projection and the committed core binding artifacts. Feature 5 owns engine SDK
registration and the `GenerateLibrary`, `GenerateModule`, `GenerateClient`, and
`GenerateEntrypoint` hooks. Feature 6 owns Rust source discovery, module TypeDef
emission, and dispatch. Feature 7 owns standalone, module, and dependency client
project generation. Feature 8 owns the closing multi-platform and end-to-end
conformance matrix. Feature 9 owns immutable Git-tagged distribution, migration
material, release assets, and the stable release gate.

The current `sdk-sdk` harness has no check that enumerates Core_Schema coordinates or
directly exercises the generated typed bindings. Its generation and module-load checks
provide smoke coverage, and it remains authoritative for the common checks it declares,
but a green harness result is not evidence for a Feature 4 status transition. Feature 4
therefore supplies coordinate-complete generator, compile, serialization,
query-projection, and exact-target engine evidence.

The current ledger routes 3,329 `Partial` capabilities to Feature 4. Ground-truth
review shows that 68 of those rows belong to Features 3, 5, or 6. After that ownership
correction, Feature 4 owns 3,261 existing blocking capabilities: all 1,567 active
engine-schema coordinates, 1,673 generated Go-client declarations, and 21 shared
schema-to-client code-generation behaviours. The feature also adds the Rust-specific
policy capabilities omitted by the Feature 1 baseline. Statuses move only when their
own evidence passes; this specification does not inflate `Implemented` by treating
ownership changes or a single successful compile as conformance.

## Glossary

- **Active_Schema_Coordinate:** One schema root, named type, field, argument, input
  field, enum value, directive, or directive argument in the Exact_Target inventory.
- **Binding_Kind:** The reviewed Rust projection category for a capability, such as
  Handle, Interface_Trait, Enum, Input_Object, Scalar, Method, Option, Directive_Policy,
  or Idiomatic_Equivalent.
- **Binding_Record:** One Generated_Binding_Manifest entry connecting authoritative
  Capability_IDs to their Rust symbol or reviewed policy, implementation fingerprint,
  and executable evidence.
- **Blocking_Status:** `Missing` or `Partial` under the Feature 1 status policy.
- **Canonical_Schema:** The checked, digest-verified engine introspection snapshot
  selected by the Exact_Target.
- **Complete_Status:** `Implemented`, `Idiomatic_Equivalent`, or a justified
  `Inapplicable` classification under the Feature 1 status policy.
- **Core_Schema:** The Dagger engine GraphQL schema before user-module or dependency
  types are merged into it.
- **Defaulted_Argument:** A GraphQL argument with a non-null `defaultValue` in the
  Canonical_Schema; omission delegates value selection to the engine.
- **Definitive_Go_SDK:** `github.com/dagger/dagger-go-sdk` at commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`.
- **Exact_Target:** The Target_Descriptor selected by
  `sdk/rust/completeness/target.json`, including Dagger revision
  `25300124ca110612edc09c43f89cb5fad6028170` and engine version
  `v1.0.0-beta.10`.
- **Generated_Artifact:** A source or manifest file wholly owned by the Core_Generator,
  marked as generated, and replaceable during explicit regeneration.
- **Generated_Binding:** A public Rust type, trait, method, option, implementation, or
  reviewed no-symbol policy emitted or enforced from the Canonical_Schema.
- **Generated_Binding_Manifest:** The deterministic machine-readable index of every
  Feature 4 capability, its Binding_Kind, authoritative wire coordinate, Rust symbol
  or policy, and evidence scope.
- **Generated_Handle:** A cloneable object or interface client value carrying a
  Selection and a lease on the same Shared_Session as its originating Client.
- **Identifier_Reentry:** Reconstruction of a typed Generated_Handle from an engine ID
  through the Core_Schema `Query.node` selection and the required inline fragment.
- **Input_Object:** A GraphQL `INPUT_OBJECT` represented by an owned serializable Rust
  value.
- **Into_ID:** The Feature 2 asynchronous conversion contract that accepts a raw ID or
  a compatible Generated_Handle and resolves the unified engine ID.
- **Nullable_Handle:** The result of probing a nullable object or interface selection
  and returning either no value or a correctly rooted Generated_Handle.
- **Owned_Output_Set:** The complete, declared set of Generated_Artifacts that one
  regeneration may create, replace, or remove.
- **Required_Argument:** A GraphQL argument whose outer wrapper is `NON_NULL` and which
  has no engine default.
- **Rust_Name_Map:** The deterministic mapping from an authoritative GraphQL name to a
  valid Rust item, method, field, parameter, or enum-variant identifier while retaining
  the original wire name.
- **Schema_Wrapper:** One layer of GraphQL `NON_NULL` or `LIST` around a named type.
- **Selection:** Feature 2's immutable GraphQL query path bound to one Shared_Session.
- **Target_Revision:** Dagger commit
  `25300124ca110612edc09c43f89cb5fad6028170`.
- **Wire_Name:** The exact case-sensitive GraphQL type, field, argument, input-field,
  enum-value, or directive name from the Canonical_Schema.

## Target State

The Core_Generator consumes the Canonical_Schema as validated data and produces the
complete Owned_Output_Set without network access or dependence on a running engine.
Every Active_Schema_Coordinate and every retained generated Go-client or shared
code-generation capability has exactly one Binding_Record. The manifest makes
coverage mechanical: adding, removing, changing, duplicating, or silently ignoring a
coordinate fails verification.

The public API is Rust-native. Named object types are lightweight generated handles;
interfaces have usable traits plus concrete client handles; enums are closed Rust
enums with exact wire serialization; input objects are owned values; built-in and
Dagger scalars use stable platform-independent representations; GraphQL wrapper shape
is preserved recursively; and nullable values never rely on generated `unwrap` or
sentinel data. Object selection stays lazy where non-nullability makes a handle valid.
Nullable object selection resolves existence before returning `Option<Handle>`. Lists
of objects resolve IDs and return correctly re-rooted handles in engine order.

Required arguments cannot be omitted through the generated signature. Optional and
defaulted arguments can be omitted, while explicitly supplied `false`, zero, empty
string, empty list, or non-default enum values are serialized rather than mistaken for
absence. The definitive Go SDK's zero-value omission is not copied where it loses a
meaningful explicit value, such as `Query.git(keepGitDir: false)` against the engine's
`true` default. The generated convenience surface need not add an explicit GraphQL
null sentinel that the Definitive_Go_SDK does not expose; Feature 2's Raw_Request
remains available when a caller deliberately needs a raw `null` literal.

Generated code contains complete, warning-free rustdoc derived from sanitized schema
documentation and precise generated contract notes. Deprecation and experimental
metadata remain visible. Rust-safe identifiers never change Wire_Names. The generated
module does not suppress `missing_docs`, broken links, malformed HTML, or compiler
warnings at module scope to make external text pass.

The two single-underscore engine metadata objects and their four fields remain active
manifest coordinates but receive reviewed no-symbol policies. This matches the
Definitive_Go_SDK visitor's `strings.HasPrefix(t.Name, "_")` exclusion in
`cmd/codegen/introspection/visitor.go` without letting those coordinates disappear
from completeness accounting.

Generation is fallible, order-independent, byte-stable after the pinned formatter,
and side-effect-free until explicit publication. Malformed or unsupported schema
input returns a coordinate-bearing diagnostic rather than panicking or emitting a
partial file. Verification regenerates into private temporary state, compares the
complete output set, checks the public API and query projections, and leaves the
worktree unchanged.

Feature 4 does not register Rust with the engine generator, parse user Rust source,
emit module TypeDefs or dispatch code, create standalone Cargo projects, publish a
crate, or claim Feature 8's multi-platform closure. It supplies the core generator and
bindings those later features consume.

## Evidence From Current Code

Repository citations for authoritative behaviour use Target_Revision unless the
Definitive_Go_SDK revision is stated. Current Rust citations describe `main` after
Features 1–3.

- **Canonical contract and current ledger:**
  `sdk/rust/completeness/snapshots/schema.json` contains the checked Exact_Target
  introspection snapshot. It yields 1 query root, 111 public named types, 720 fields,
  611 arguments, 14 input fields, 84 enum values, 12 directives, and 14 directive
  arguments. `sdk/rust/completeness/classifications.json` currently assigns the full
  engine-schema authority, all 83 Go code-generator declarations, and nearly all
  `dagger.gen.go` declarations to Feature 4 with one coarse `Partial` gap.
- **Definitive generated library:**
  `sdk/go/generate.go:1-3` invokes the engine's `generate-library` operation.
  `sdk/go/dagger.gen.go` at Definitive_Go_SDK commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5` supplies the generated object, interface,
  input, scalar, enum, option, load/reference, and query-selection behaviour. Its
  `Container.Stat`, `EngineCache.Prune`, and related option loops omit Go zero values;
  Rust must preserve omission without copying that inability to send a meaningful
  explicit zero value.
- **Schema-to-library edge evidence:**
  `cmd/codegen/generator/go/templates/format.go` at Target_Revision defines Go's
  scalar, list, object, input, and enum projection. The tests in
  `templates/object_test.go`, `enum_test.go`, `interface_surface_test.go`, and
  `param_names_test.go` establish expected-type ID conversion, interface clients,
  deprecation propagation, and separation of language identifiers from Wire_Names.
  These behaviours inform Rust mappings without defining Rust syntax.
- **Generator contract boundary:**
  `cmd/codegen/generator/generator.go:17-36` at Target_Revision defines
  `GenerateModule`, `GenerateClient`, `GenerateLibrary`, and `GenerateEntrypoint` as
  engine generator operations. The umbrella assigns those hooks to Feature 5, even
  though their current ledger rows are coarsely owned by Feature 4. Module source
  introspection tests under `cmd/codegen/generator/go/templates` belong to Feature 6.
- **Current introspection model:**
  `sdk/rust/crates/dagger-sdk/src/core/introspection.rs:137-311` can deserialize named
  types, fields, arguments, default values, deprecations, directive applications, and
  `@expectedType`. Directive definitions remain private and partly dead-code allowed;
  validation is not an explicit generator phase.
- **Current wrapper gap:**
  `sdk/rust/crates/dagger-codegen/src/functions.rs:87-146,250-261` strips `NON_NULL`
  while formatting named types and decides optionality only from the outer kind.
  `sdk/rust/crates/dagger-codegen/src/rust/templates/input_tmpl.rs:16-42` formats input
  fields as outputs. The generated signatures therefore do not recursively preserve
  all nullable output and input shapes.
- **Current scalar gap and authority conflict:**
  `sdk/rust/crates/dagger-codegen/src/rust/format.rs:18-75` maps GraphQL `Int` to
  platform-dependent `isize`. The Canonical_Schema carries GraphQL's standard
  signed-32-bit description, but `dagql/types.go:265-340` at Target_Revision defines
  the engine scalar as `int64`, and engine cache fields can exceed 2 GiB. The
  Definitive Go binding uses `int`, which is 64-bit on Dagger's supported Go targets.
  Rust therefore uses explicit `i64` and records this reviewed target-behaviour
  decision instead of copying either the stale description or a platform-sized type.
  `rust/templates/scalar_tmpl.rs:15-86` emits `Void` as a string newtype, while
  `core/void.go:12-45` and `core/integration/module_go_test.go:113-122` at
  Target_Revision establish that Void has no represented value and is returned as
  JSON `null`.
- **Current identifier gap:**
  `sdk/rust/crates/dagger-codegen/src/rust/functions.rs:13-31` normalizes names through
  `convert_case` and recognizes only five reserved identifiers. It has no exhaustive
  Rust 2024 keyword policy or explicit post-normalization collision diagnostic.
- **Current object and ID foundation:**
  `sdk/rust/crates/dagger-codegen/src/rust/functions.rs:237-337` already distinguishes
  lazy object handles, list-of-object ID re-entry, and `@expectedType` self-return
  conversion. `sdk/rust/crates/dagger-sdk/src/client.rs:128-191` supplies sealed
  `Loadable`, lazy reference, and checked load paths. Feature 4 completes and verifies
  these mechanisms rather than replacing them with a parallel client model.
- **Current generated surface:**
  `sdk/rust/crates/dagger-sdk/src/gen.rs` is a 15,000-line monolithic output containing
  broad object, interface, input, scalar, option, and enum coverage. Its heading
  suppresses `missing_docs`, and many public options, variants, and fallback mappings
  lack caller-facing contract documentation. Presence therefore supports `Partial`,
  not a complete claim.
- **Current traversal and failure behaviour:**
  `sdk/rust/crates/dagger-codegen/src/visitor.rs:31-167` sorts some type categories but
  uses `unwrap` for malformed schema structure and has no handler for union or unknown
  kinds. `sdk/rust/crates/dagger-bootstrap/src/cli_generate.rs:20-43` unwraps CLI and
  schema values before directly writing the requested output.
- **Current repository generation path:**
  `toolchains/rust-sdk-dev/main.go:244-259` obtains live engine introspection, writes
  `gen.rs`, then applies `cargo fix` and `cargo fmt`. Feature 4 requires the generator
  to own semantic output and verification to compare against checked target input;
  compiler fix-ups cannot remain an undocumented semantic generation stage.
- **Current lifecycle boundary:**
  `sdk/rust/crates/dagger-sdk/src/query.rs:32-204` provides deterministic immutable
  Selection construction, lazy ID arguments, typed build errors, response decoding,
  and the Shared_Session execution fence. Generated bindings must continue through
  this path so close, timeout, transport, GraphQL, and engine-domain behaviour remain
  Feature 2/3 owned.
- **Rust policy:**
  `sdk/rust/AGENTS.md` requires idiomatic Rust over Go transliteration, generated-code
  ownership, complete public documentation, panic-free library paths, denied unsafe
  code, exact authority evidence, and generator plus engine-backed tests.

## Completeness Contract Policy

### Existing Capability Scope After Ownership Correction

The following table partitions all existing capabilities currently routed to Feature
4 and is the human-readable policy. The Generated_Binding_Manifest is the exhaustive
machine-readable list of individual Capability_IDs.

| Authority partition | Current rows | Feature 4 rows | Target policy |
|---|---:|---:|---|
| `engine-schema` | 1,567 | 1,567 | Retain every Active_Schema_Coordinate under Feature 4 |
| `go-client` generated `dagger.gen.go` surface | 1,679 | 1,673 | Retain generated binding, load/reference, and common object-interface behaviours; route trace and execution-error rows to Feature 3 |
| `go-codegen` | 83 | 21 | Retain only shared schema-to-client mapping behaviours; route engine generator operations to Feature 5 and module source/introspection behaviours to Feature 6 |
| **Total** | **3,329** | **3,261** | Ownership change alone does not change status |

The lexicographically sorted compact-JSON list of the 3,261 retained existing
Capability_IDs has scope digest
`sha256:2b46180b54356faf2071a91198afd1a0e40a757b57a1686f579d2f9ab6ed583f`.

### Go Client Ownership Correction

| Definitive Go declarations | Count | Correct owner | Rationale |
|---|---:|---|---|
| `Tracer` | 1 | Feature 3 | Trace construction and propagation are observability behaviour |
| `ExecError` plus `Error`, `Extensions`, `Message`, and `Unwrap` | 5 | Feature 3 | Typed engine-domain failure projection was delivered by Feature 3 |
| Remaining generated library declarations | 1,673 | Feature 4 | They define or support the Core_Schema generated client surface |

Routing these six rows does not itself assert that Feature 3 evidence is sufficient for
a Complete_Status. Any status change remains a separate evidence-backed ledger edit.

### Go Code-Generator Ownership Correction

This source-by-source partition accounts for all 83 current `go-codegen` rows.
`GenerateClient` remains Feature 5-owned as an engine generator hook; Feature 7 owns
the generated standalone project's contents and usability.

| Source group at Target_Revision | Count | Correct owner | Rationale |
|---|---:|---|---|
| `templates/enum_test.go`, `format.go`, `interface_surface_test.go`, `object_test.go`, `param_names_test.go` | 21 | Feature 4 | Shared schema-to-client mapping and generated public-surface behaviour |
| `generator/generator.go`, `go/generate_{client,entrypoint,library,module}.go`, `generate_module_test.go`, `go/generator.go`, `loader.go`, `mount.go`, `templates/templates.go` | 19 | Feature 5 | Engine generator interface, backend orchestration, overlays, and post-generation operations |
| `templates/functions.go`, `introspect_emit_test.go`, `module_interfaces_test.go`, `module_objects_test.go`, `module_types.go`, `modules_test.go`, `visit_determinism_test.go` | 43 | Feature 6 | User-source discovery, module TypeDef/introspection emission, and module traversal |

### Omitted Rust Policy Capabilities

Feature 1 did not inventory the Rust-specific correctness obligations needed to call a
generated client complete. Feature 4 adds the following stable IDs under the
`rust-policy` authority and routes them to Feature 4:

```text
policy/rust-policy/core-codegen-atomic-publication
policy/rust-policy/core-codegen-authority-containment
policy/rust-policy/core-codegen-collision-detection
policy/rust-policy/core-codegen-default-omission
policy/rust-policy/core-codegen-directive-accounting
policy/rust-policy/core-codegen-documentation
policy/rust-policy/core-codegen-exhaustive-manifest
policy/rust-policy/core-codegen-fallible-input
policy/rust-policy/core-codegen-identifier-roundtrip
policy/rust-policy/core-codegen-input-order-invariance
policy/rust-policy/core-codegen-list-object-reentry
policy/rust-policy/core-codegen-no-handwritten-fixes
policy/rust-policy/core-codegen-nullability
policy/rust-policy/core-codegen-scalar-wire-types
policy/rust-policy/core-codegen-target-drift
policy/rust-policy/core-codegen-toolchain-compatibility
```

### Status Evidence Boundary

Source presence or a matching generated filename can establish only `Partial`. A
Complete_Status requires a Binding_Record, implementation evidence, and executable
verification evidence scoped to every affected Capability_ID. One property may prove a
shared generator rule for many coordinates only when the manifest records why every
coordinate falls within that property's generated domain. A compile of `gen.rs` alone
does not prove wire names, argument omission, nullable decoding, ID conversion,
directive projection, or public reachability.

Feature 4 evidence may establish completion for generator and generated-binding
capabilities without waiting for Feature 8's platform matrix when it is deterministic,
target-scoped, and capability-complete. Feature 8 remains responsible for cross-SDK,
multi-platform, and full application end-to-end closure. An unrelated `sdk-sdk` check
cannot be attached as Feature 4 verification evidence.

## Core Schema Contract Policy

The Canonical_Schema itself is the exhaustive field-level contract. The tables below
partition its 1,567 coordinates without copying thousands of machine-derived rows into
prose. Every coordinate appears individually in the Generated_Binding_Manifest.

### Schema Coordinate Policy

| Coordinate kind | Exact count | Target policy | Error if invalid or unaccounted | Side-effect impact |
|---|---:|---|---|---|
| Query root | 1 | Emit the typed root handle reached from the owned Client | `SCHEMA_ROOT_INVALID` | None during projection |
| Named types | 111 | Account for 76 generated objects, 2 target-private metadata objects, 3 interfaces, 18 enums, 8 scalars, and 4 input objects | `SCHEMA_TYPE_UNSUPPORTED` | Adds a generated symbol, reviewed scalar policy, or target-private no-symbol policy |
| Fields | 720 | Emit 716 reachable Rust methods with exact output and argument semantics and retain 4 target-private metadata fields as no-symbol policies | `SCHEMA_FIELD_UNMAPPED` | Builds a Selection only for emitted methods |
| Arguments | 611 | Preserve requiredness, default omission, Wire_Name, type, directives, and documentation | `SCHEMA_ARGUMENT_UNMAPPED` | Adds a query argument only when required or explicitly supplied |
| Input fields | 14 | Preserve wrapper shape, default omission, Wire_Name, and serialization | `SCHEMA_INPUT_FIELD_UNMAPPED` | Affects serialization only |
| Enum values | 84 | Emit one unambiguous Rust variant with exact wire serialization | `SCHEMA_ENUM_VALUE_UNMAPPED` | None until serialized or decoded |
| Directive definitions | 12 | Apply the explicit directive policy below or record target-inactive containment | `SCHEMA_DIRECTIVE_UNMAPPED` | Metadata only |
| Directive arguments | 14 | Validate each definition and interpret every argument used by an active application | `SCHEMA_DIRECTIVE_ARGUMENT_INVALID` | Metadata only |

The Exact_Target contains no public union type, mutation root, or subscription root.
Their absence is a target fact, not a permanent silent exclusion. A future appearance
is authority drift and must receive an explicit mapping before generation can pass.

### GraphQL Type and Wrapper Policy

| Schema shape | Rust policy | Invalid-data behaviour | Runtime impact |
|---|---|---|---|
| `Boolean` | `bool` | Decode or encode failure remains typed | Scalar query or argument |
| `Float` | `f64` | Reject non-representable JSON numbers | Scalar query or argument |
| `Int` | `i64`, matching Target_Revision `dagql.Int` | Reject values outside the target engine's signed 64-bit representation | Platform-independent scalar query or argument |
| `String` | Owned `String` output with ergonomic owned conversion on input | Reject non-string wire data | Scalar query or argument |
| `ID` | Opaque owned `Id` with typed Into_ID and Loadable integration | Reject non-string IDs and incompatible expected types | May resolve a lazy identifier |
| `JSON` | Lossless Rust scalar representation of the engine's JSON-encoded wire value | Reject malformed scalar wire data | Scalar query or input serialization |
| `Platform` | Opaque owned newtype preserving the exact engine string | Reject non-string wire data | Scalar query or argument |
| `Void` | Idiomatic unit result for the engine's represented `null` | Reject a non-null represented payload | Executes the field and returns `()` on success |
| `ENUM` | Closed Rust enum with exact wire names | Reject an unknown wire value with typed decoding failure | Scalar-like query or argument |
| `INPUT_OBJECT` | Owned serializable Rust value with exact field names and wrapper shape | Reject missing required fields or invalid nested values | Argument serialization only |
| `OBJECT` | Generated_Handle | Reject an object lacking the ID required by an eager nullable/list probe | Lazy or ID-probing selection |
| `INTERFACE` | Rust trait plus concrete interface client handle | Reject an inconsistent possible-type declaration | Lazy or ID-probing selection |
| Single-underscore target metadata object | Validated no-symbol policy matching the definitive Go generator | Reject a new kind, reference, or public reachability without review | None |
| `NON_NULL(T)` | Represent `T` directly | Reject a `null` response as typed decoding failure | No generated runtime unwrap |
| Nullable `T` | Represent response or input absence explicitly | Reject a non-`T` concrete value | Returns or stores `Option<T>` except Void |
| `LIST(T)` | `Vec<T>` with wrapper recursion at list and element levels | Reject an invalid element with its typed decode context | Preserves order and cardinality |

### Field and Argument Policy

| Contract shape | Target policy | Error if invalid | Side-effect impact |
|---|---|---|---|
| Non-null object or interface field | Return a lazy Generated_Handle without engine I/O | `OBJECT_HANDLE_MAPPING_INVALID` | Extends Selection only |
| Nullable object or interface field | Probe the selected ID and return `Result<Option<Handle>, QueryError>` | Typed query/decode failure | Performs one query before constructing a handle |
| List of objects or interfaces | Resolve selected IDs and re-enter typed handles in response order | `LIST_REENTRY_TYPE_INVALID` or typed query failure | Performs one ID query; returned handles stay lazy |
| Scalar, enum, input-value, or Void field | Execute through Shared_Session and return the wrapper-correct typed result | Existing typed QueryError | Performs one GraphQL request |
| `ID @expectedType(name: Parent)` self-return field | Resolve the ID and return the declared parent handle rooted through `node` | `EXPECTED_TYPE_INVALID` | Performs one ID query |
| Required argument | Expose a compile-time-required Rust input | `REQUIRED_ARGUMENT_OMITTED` in generated compile-fail evidence | Encoded on every call |
| Nullable or defaulted argument | Expose a typed optional path distinct from every concrete value | `OPTION_ARGUMENT_MAPPING_INVALID` | Omitted when absent; encoded when present |
| Concrete zero-like optional value | Encode the supplied `false`, zero, empty string, or empty list | Typed argument-encoding failure | Overrides an engine default where applicable |
| Defaulted argument omitted | Send no GraphQL argument | None | Engine applies its target-defined default |
| Object-typed `ID @expectedType` argument | Accept compatible raw IDs and handles through Into_ID | `EXPECTED_TYPE_INVALID` or lazy identifier error | Resolves before the request document is sent |
| List of object-typed IDs | Resolve each compatible value once and preserve input order | Lazy identifier error | Sends the complete ID list or sends no request |
| Rust-safe renamed argument | Use the Rust_Name_Map in source and the original Wire_Name in GraphQL | `WIRE_NAME_MISMATCH` | No wire-visible rename |

### Directive Policy

| Directive definitions | Active target applications | Target policy | Error if invalid | Side-effect impact |
|---|---:|---|---|---|
| `expectedType(name:)` | 90 | Validate the referenced object/interface and drive typed ID input or self-return conversion | `EXPECTED_TYPE_INVALID` | Type mapping only |
| `deprecated(reason:)` | 13 | Emit deprecation metadata and caller-visible rustdoc at the nearest representable Rust item | `DEPRECATION_DIRECTIVE_INVALID` | Documentation/compiler warning only |
| `experimental(reason:)` | 10 | Emit a prominent rustdoc stability note without inventing a Rust feature gate | `EXPERIMENTAL_DIRECTIVE_INVALID` | Documentation only |
| `enumValue(value:)` | 23 | Treat the decorated enum Wire_Name as an input alias of the named canonical sibling value, matching the pinned Go SDK's `Name`/`MarshalJSON` behaviour | `SCHEMA_DIRECTIVE_ARGUMENT_INVALID` | Enum decoding accepts the alias; encoding uses the canonical value |
| `cache`, `check`, `defaultAddress`, `defaultPath`, `generate`, `ignorePatterns`, `sourceMap`, `up` | 0 | Validate their 8 definitions and 10 arguments as target-contained inactive core-client metadata | `TARGET_INACTIVE_DIRECTIVE_CHANGED` | None |

If a currently inactive directive gains a Core_Schema application, generation fails as
target drift until its client projection is reviewed. Feature 4 does not silently
discard new directive semantics that happen not to affect the old target.

### Generated Output Policy

| Surface | Target policy | Error if invalid | Side-effect impact |
|---|---|---|---|
| Rust names | Deterministic Rust 2024 mapping with exact Wire_Name retention | `RUST_NAME_COLLISION` or `RUST_NAME_INVALID` | Public source only |
| Public documentation | Sanitized schema contract plus omission, default, deprecation, experimental, and mapping notes | `GENERATED_DOCUMENTATION_INVALID` | Rustdoc only |
| Generated artifact header | Mark ownership and identify Exact_Target plus schema digest | `GENERATED_PROVENANCE_INVALID` | Review metadata only |
| Verification mode | Generate into private temporary state and compare the complete Owned_Output_Set | `GENERATED_OUTPUT_DRIFT` | No worktree mutation |
| Update mode | Atomically replace only declared Generated_Artifacts after successful generation and formatting | `GENERATED_PUBLICATION_FAILED` | Mutates owned generated files only |
| Formatter | Use the pinned Rust toolchain's canonical formatter without semantic compiler fix-ups | `GENERATED_FORMAT_FAILED` | Temporary state before publication |

## Requirements

### Requirement 1: Exact and Honest Completeness Scope

**User Story:** As a Rust SDK release reviewer, I want every Feature 4 capability
identified and correctly owned, so that the large completeness movement is real rather
than a bulk relabel.

#### Acceptance Criteria

1. WHEN Feature 4 begins implementation, THE Completeness_Ledger SHALL route the 3,261
   retained existing capability IDs to Feature 4.
2. WHEN the Go-client ownership correction is applied, THE Completeness_Ledger SHALL
   route the six trace and execution-error declarations to Feature 3.
3. WHEN the Go-codegen ownership correction is applied, THE Completeness_Ledger SHALL
   route the 19 engine-generator declarations to Feature 5.
4. WHEN the Go-codegen ownership correction is applied, THE Completeness_Ledger SHALL
   route the 43 module-source and introspection declarations to Feature 6.
5. WHEN ownership changes without new evidence, THE Completeness_Ledger SHALL preserve
   the capability's prior status.
6. WHEN Feature 4 policy is registered, THE rust-policy authority SHALL include the 16
   omitted core-codegen capability IDs.
7. THE Generated_Binding_Manifest SHALL contain exactly one Binding_Record for every
   Feature 4-owned Active_Capability.
8. IF a Binding_Record cites a materially different Rust public shape, THEN THE
   Completeness_Ledger SHALL require reviewed Idiomatic_Equivalent decision evidence.
9. WHEN a capability receives a Complete_Status, THE Completeness_Ledger SHALL cite
   capability-scoped implementation and executable verification evidence.
10. IF evidence proves only source presence or compilation, THEN THE
    Completeness_Ledger SHALL retain `Partial` for behaviours not proved by that
    evidence.
11. WHEN the Exact_Target changes, THE completeness validator SHALL reject every
    unreconciled Feature 4 capability addition, removal, fingerprint change, or owner
    change.

### Requirement 2: Validated Canonical Schema Input

**User Story:** As a generator maintainer, I want checked and validated schema input,
so that malformed or wrong-target data cannot produce plausible Rust source.

#### Acceptance Criteria

1. WHEN generation starts, THE Core_Generator SHALL verify the Canonical_Schema digest
   against the Exact_Target authority registry.
2. WHEN generation starts, THE Core_Generator SHALL verify the Exact_Target identity
   before rendering Rust source.
3. THE Core_Generator SHALL validate every public named type required by the
   Active_Schema_Coordinates.
4. THE Core_Generator SHALL validate every field, argument, input field, enum value,
   directive definition, and directive argument required by the
   Active_Schema_Coordinates.
5. IF a required name or type reference is absent, THEN THE Core_Generator SHALL return
   a coordinate-bearing `SCHEMA_REFERENCE_INVALID` diagnostic.
6. IF a wrapper graph is malformed or cyclic, THEN THE Core_Generator SHALL return a
   coordinate-bearing `SCHEMA_WRAPPER_INVALID` diagnostic.
7. IF a default literal cannot be interpreted for its declared type, THEN THE
   Core_Generator SHALL return a coordinate-bearing `SCHEMA_DEFAULT_INVALID`
   diagnostic.
8. IF a directive application contradicts its definition, THEN THE Core_Generator
   SHALL return a coordinate-bearing `SCHEMA_DIRECTIVE_ARGUMENT_INVALID` diagnostic.
9. IF an unsupported public schema kind appears, THEN THE Core_Generator SHALL return
   `SCHEMA_TYPE_UNSUPPORTED` before rendering output.
10. IF validated input is semantically equivalent but differently ordered, THEN THE
    Core_Generator SHALL construct the same canonical schema model.
11. IF schema validation fails, THEN THE Core_Generator SHALL produce no
    Generated_Artifact publication side effect.
12. WHEN caller-controlled schema input is malformed, THE Core_Generator SHALL return
    an error without panic, `unwrap`, or invariant-free `expect` termination.

### Requirement 3: Recursive Rust Type and Nullability Mapping

**User Story:** As a Rust SDK user, I want signatures that preserve the engine's real
type constraints, so that invalid omission and null assumptions are caught before or
during a typed query.

#### Acceptance Criteria

1. THE Core_Generator SHALL map GraphQL `Boolean` to Rust `bool`.
2. THE Core_Generator SHALL map GraphQL `Float` to Rust `f64`.
3. THE Core_Generator SHALL map GraphQL `Int` to platform-independent Rust `i64`.
4. THE Core_Generator SHALL map GraphQL `String` to an owned UTF-8 Rust string value.
5. THE Core_Generator SHALL map GraphQL `ID` to the opaque generated `Id` contract.
6. THE Core_Generator SHALL map GraphQL `JSON` to a lossless Rust representation of
   the engine scalar wire value.
7. THE Core_Generator SHALL map GraphQL `Platform` to an opaque owned Rust newtype.
8. THE Core_Generator SHALL map GraphQL `Void` results to idiomatic Rust unit.
9. WHEN a named output is nullable, THE Core_Generator SHALL expose its absence without
   runtime unwrapping.
10. WHEN a named output is non-null, THE Core_Generator SHALL omit an unnecessary
    outer `Option`.
11. WHEN a list is nullable, THE Core_Generator SHALL represent list absence
    independently from an empty list.
12. WHEN a list element is nullable, THE Core_Generator SHALL represent each element's
    absence independently from the list.
13. WHEN `NON_NULL` and `LIST` wrappers are nested, THE Core_Generator SHALL preserve
    their order recursively in the Rust type.
14. IF a non-null response contains `null`, THEN THE generated binding SHALL return a
    typed decoding failure.
15. IF a scalar response violates its declared wire representation, THEN THE generated
    binding SHALL return a typed decoding failure.
16. WHEN a Required_Argument is represented, THE generated Rust signature SHALL make
    omission a compile-time error.
17. WHEN an Input_Object field is required, THE generated construction path SHALL make
    omission a compile-time error.

### Requirement 4: Complete Object, Interface, and Field Bindings

**User Story:** As a Rust SDK user, I want every core object and field reachable through
one coherent client, so that I never need an untyped workaround for a generated Dagger
capability.

#### Acceptance Criteria

1. WHEN a Client exposes its generated query root, THE generated `Query` handle SHALL
   retain the Client's Shared_Session lease.
2. THE Core_Generator SHALL emit one Generated_Handle for every public Core_Schema
   object type.
3. THE Core_Generator SHALL emit one public Rust trait for every public Core_Schema
   interface type.
4. THE Core_Generator SHALL emit one concrete Generated_Handle for every public
   Core_Schema interface type.
5. WHEN an object declares an interface, THE Core_Generator SHALL implement the
   corresponding Rust interface trait for that object's Generated_Handle.
6. THE Core_Generator SHALL emit one reachable Rust operation for every public
   Core_Schema field.
7. WHEN a non-null field returns an object or interface, THE generated operation SHALL
   extend Selection without engine I/O.
8. WHEN a nullable field returns an object or interface, THE generated operation SHALL
   return `None` for a target `null` response.
9. WHEN a nullable field returns a present object or interface, THE generated operation
   SHALL return a correctly rooted Generated_Handle.
10. WHEN a field returns a list of objects or interfaces, THE generated operation SHALL
    preserve engine response order in the returned handles.
11. WHEN a field returns a scalar, enum, input value, or Void, THE generated operation
    SHALL execute through the originating Shared_Session.
12. WHEN a generated operation executes after session close, THE generated operation
    SHALL return the existing typed lifecycle failure.
13. WHEN generated execution receives GraphQL or engine-domain errors, THE generated
    operation SHALL preserve the Feature 3 typed QueryError behaviour.
14. WHEN generated execution uses a configured timeout, THE generated operation SHALL
    remain inside the Feature 2 request timeout fence.
15. IF a field cannot be mapped without losing its schema wrapper or Wire_Name, THEN
    THE Core_Generator SHALL fail rather than omit that field.

### Requirement 5: Required, Optional, and Defaulted Arguments

**User Story:** As a Rust SDK caller, I want generated arguments to distinguish absence
from concrete values, so that engine defaults and explicit overrides behave correctly.

#### Acceptance Criteria

1. THE Core_Generator SHALL emit a typed input for every public Core_Schema argument.
2. WHEN an argument is required, THE generated operation SHALL require it outside the
   optional-options path.
3. WHEN an argument is nullable, THE generated operation SHALL provide a typed omission
   state.
4. WHEN an argument has an engine default, THE generated operation SHALL provide a
   typed omission state.
5. WHEN a nullable or defaulted argument is omitted, THE generated query document
   SHALL omit its Wire_Name.
6. WHEN `false` is explicitly supplied for an optional Boolean, THE generated query
   document SHALL encode `false`.
7. WHEN zero is explicitly supplied for an optional numeric argument, THE generated
   query document SHALL encode zero.
8. WHEN an empty string is explicitly supplied for an optional String, THE generated
   query document SHALL encode the empty string.
9. WHEN an empty list is explicitly supplied for an optional list, THE generated query
   document SHALL encode the empty list.
10. WHEN an enum value is explicitly supplied for a defaulted enum argument, THE
    generated query document SHALL encode that exact wire value.
11. WHEN a defaulted argument is omitted, THE generated binding SHALL avoid
    materializing the schema default on the client.
12. WHEN Rust ownership does not require borrowing, THE generated options value SHALL
    own its caller-supplied data.
13. WHEN a Rust argument name differs from its Wire_Name, THE generated operation SHALL
    encode only the Wire_Name.
14. IF argument serialization fails, THEN THE generated operation SHALL return the
    existing typed argument-encoding failure before transport execution.
15. WHEN two calls reuse the same options value, THE generated binding SHALL preserve
    immutable caller-observable option state.

### Requirement 6: Typed IDs, Expected Types, and Re-entry

**User Story:** As a Rust SDK caller, I want object handles and IDs to compose safely,
so that references can cross generated calls without losing type or session semantics.

#### Acceptance Criteria

1. WHEN a public object or interface exposes `id`, THE Core_Generator SHALL make its
   Generated_Handle compatible with Into_ID.
2. WHEN a public object or interface can be loaded through `Query.node`, THE
   Core_Generator SHALL make its Generated_Handle compatible with Loadable.
3. WHEN a raw `Id` is supplied to a compatible expected-type argument, THE generated
   operation SHALL accept it without an engine lookup solely for conversion.
4. WHEN a compatible Generated_Handle is supplied to an expected-type argument, THE
   generated operation SHALL resolve its ID through Into_ID.
5. WHEN a list of compatible handles or IDs is supplied, THE generated operation SHALL
   preserve input order in the encoded ID list.
6. WHEN a list element ID resolution fails, THE generated operation SHALL return a
   typed lazy-identifier error.
7. IF any required ID resolution fails, THEN THE generated operation SHALL send no
   GraphQL request for the containing operation.
8. WHEN `@expectedType` decorates a self-return ID field, THE generated operation SHALL
   return the declared parent Generated_Handle.
9. WHEN Identifier_Reentry constructs a handle, THE generated operation SHALL retain
   the originating Shared_Session lease.
10. WHEN Identifier_Reentry constructs a concrete type, THE generated Selection SHALL
    include that exact GraphQL inline-fragment Wire_Name.
11. IF an `@expectedType` name does not identify a compatible target type, THEN THE
    Core_Generator SHALL return `EXPECTED_TYPE_INVALID`.
12. IF list re-entry targets a type without the required ID surface, THEN THE
    Core_Generator SHALL return `LIST_REENTRY_TYPE_INVALID`.

### Requirement 7: Enums, Input Objects, Scalars, and Directive Semantics

**User Story:** As a Rust SDK user, I want non-object schema values to round-trip
without hidden aliases or omissions, so that generated queries remain faithful to the
engine contract.

#### Acceptance Criteria

1. THE Core_Generator SHALL emit one Rust enum type for every public Core_Schema enum.
2. THE Core_Generator SHALL emit one unambiguous Rust variant for every public
   Core_Schema enum value.
3. WHEN an enum variant is serialized, THE generated enum SHALL emit its exact
   case-sensitive Wire_Name.
4. WHEN an unknown enum wire value is decoded, THE generated enum SHALL return a typed
   decoding failure.
5. THE Core_Generator SHALL emit one owned Rust input type for every public Core_Schema
   Input_Object.
6. THE Core_Generator SHALL emit one serializable Rust field for every public
   Input_Object field.
7. WHEN an optional Input_Object field is absent, THE generated serialization SHALL
   omit that field.
8. WHEN an optional Input_Object field contains a concrete zero-like value, THE
   generated serialization SHALL retain that value.
9. WHEN `expectedType` is active, THE Core_Generator SHALL apply the typed ID policy.
10. WHEN `deprecated` is active, THE Core_Generator SHALL apply the generated
    deprecation policy.
11. WHEN `experimental` is active, THE Core_Generator SHALL apply the generated
    stability-documentation policy.
12. WHILE a target directive has no active Core_Schema application, THE
    Generated_Binding_Manifest SHALL record its validated target-inactive policy.
13. IF a target-inactive directive gains an application, THEN THE Core_Generator SHALL
    fail until a reviewed projection policy is registered.
14. WHEN a custom scalar round-trips through generated serialization, THE generated
    scalar SHALL preserve its exact public value.
15. WHEN `enumValue` names a canonical sibling value, THE Core_Generator SHALL account
    for the decorated Wire_Name as a decoding alias and SHALL serialize the canonical
    value, matching `sdk/go/dagger.gen.go` at the Target_Revision.

### Requirement 8: Rust Names, Deprecation, and Public Documentation

**User Story:** As a Rust developer, I want generated APIs that read like maintained
Rust rather than raw schema output, so that the large surface remains discoverable and
safe to evolve.

#### Acceptance Criteria

1. WHEN a GraphQL type name is projected, THE Rust_Name_Map SHALL produce a valid
   UpperCamelCase Rust type identifier.
2. WHEN a GraphQL field or argument name is projected, THE Rust_Name_Map SHALL produce
   a valid snake_case Rust identifier.
3. WHEN a GraphQL enum value is projected, THE Rust_Name_Map SHALL produce a valid
   UpperCamelCase Rust variant identifier.
4. WHEN a projected name is a Rust 2024 keyword, THE Rust_Name_Map SHALL use a stable
   Rust-safe representation.
5. WHEN a Rust-safe representation differs from the source name, THE generated binding
   SHALL retain the exact Wire_Name for serialization or selection.
6. IF two distinct source names collide after normalization, THEN THE Core_Generator
   SHALL return `RUST_NAME_COLLISION` with both schema coordinates.
7. THE Core_Generator SHALL document every public generated type, trait, method,
   options value, options field, scalar, enum, and enum variant.
8. WHEN schema documentation exists, THE Core_Generator SHALL preserve its semantic
   content in sanitized rustdoc.
9. WHEN schema documentation is absent, THE Core_Generator SHALL generate a precise
   coordinate and contract note rather than suppress missing documentation.
10. WHEN a field or argument is deprecated, THE generated binding SHALL expose the
    engine-provided reason at the nearest representable public Rust item.
11. WHEN a field or argument is experimental, THE generated binding SHALL expose the
    engine-provided reason in rustdoc.
12. WHEN rustdoc parses schema-authored markup, THE generated documentation SHALL
    remain warning-free without module-wide rustdoc suppression.
13. WHEN the generated module is compiled, THE generated source SHALL remain
    warning-free without a module-wide `missing_docs` suppression.
14. WHEN a generated public API correction is breaking, THE change SHALL include the
    repository's required Rust SDK breaking-change fragment.
15. WHERE the `gen` feature is disabled, THE handwritten raw client surface SHALL
    compile without generated bindings.

### Requirement 9: Deterministic and Atomic Generation

**User Story:** As a Rust SDK maintainer, I want regeneration to be boring and
reviewable, so that schema changes produce only intentional generated diffs.

#### Acceptance Criteria

1. WHEN semantically equivalent schema elements are permuted, THE Core_Generator SHALL
   emit byte-identical formatted output.
2. WHEN identical Canonical_Schema input is generated repeatedly, THE Core_Generator
   SHALL emit byte-identical formatted output.
3. WHEN generation succeeds, THE Core_Generator SHALL identify the Exact_Target and
   schema digest in generated provenance.
4. WHEN verification mode runs, THE generation command SHALL leave the worktree
   unchanged.
5. WHEN verification output differs, THE generation command SHALL report every added,
   removed, or changed Generated_Artifact.
6. WHEN update mode runs, THE generation command SHALL mutate only the Owned_Output_Set.
7. WHEN update mode succeeds, THE generation command SHALL publish each changed
   Generated_Artifact atomically.
8. IF generation or formatting fails, THEN THE generation command SHALL preserve the
   previously committed Generated_Artifacts.
9. WHEN an owned generated artifact becomes obsolete, THE generation command SHALL
   remove it only during explicit update mode.
10. WHEN source generation changes semantics, THE semantic change SHALL originate in
    generator logic or templates rather than compiler fix-up output.
11. WHEN source formatting is required, THE generation command SHALL use the formatter
    from the pinned Rust toolchain.
12. THE repository generation workflow SHALL regenerate all core Generated_Artifacts
    from checked target input.
13. THE repository verification workflow SHALL fail on generated output drift.
14. WHEN direct bootstrap generation receives a bad path, invalid JSON, or invalid
    schema, THE bootstrap command SHALL exit with a diagnostic rather than panic.
15. WHEN concurrent verification processes run against the same checkout, THE
    generation workflow SHALL avoid shared mutable temporary output.

### Requirement 10: Capability-Complete Verification and Evidence

**User Story:** As a release reviewer, I want executable proof behind every generated
binding status, so that thousands of green ledger rows mean more than “the file
compiled.”

#### Acceptance Criteria

1. WHEN the Canonical_Schema is verified, THE manifest test SHALL prove exact equality
   between Active_Schema_Coordinates and schema Binding_Records.
2. WHEN retained Go-client capabilities are verified, THE manifest test SHALL prove
   exact equality between their Capability_IDs and generated Binding_Records.
3. WHEN retained Go-codegen capabilities are verified, THE manifest test SHALL prove
   exact equality between their Capability_IDs and mapping-policy Binding_Records.
4. WHEN generated public reachability is verified, THE compile suite SHALL reference
   every emitted public Rust symbol through the supported `dagger-sdk` namespace.
5. WHEN required arguments and fields are verified, THE compile-fail suite SHALL prove
   that their omission is rejected.
6. WHEN optional and defaulted arguments are verified, THE query-projection suite SHALL
   cover omission plus concrete zero-like values.
7. WHEN field operations are verified, THE query-projection suite SHALL cover every
   field and argument Wire_Name in the Exact_Target.
8. WHEN wrapper mapping is verified, THE property suite SHALL cover nullable,
   non-null, list, and nested-list composition.
9. WHEN enum mapping is verified, THE property suite SHALL cover every active enum
   value's bidirectional wire representation.
10. WHEN Input_Object mapping is verified, THE property suite SHALL cover every active
    input field's omission and concrete-value policy.
11. WHEN expected-type mapping is verified, THE property suite SHALL cover every active
    expected-type application.
12. WHEN list re-entry is verified, THE property suite SHALL prove order, cardinality,
    type name, session identity, and all-or-nothing ID resolution.
13. WHEN name mapping is verified, THE property suite SHALL cover Rust 2024 keywords,
    case conversion, acronym boundaries, raw identifiers, and collision detection.
14. WHEN directive mapping is verified, THE suite SHALL cover every active directive
    application and every target-inactive directive definition.
15. WHEN generated documentation is verified, THE documentation build SHALL deny
    rustdoc warnings under the declared toolchain.
16. WHEN generated source compatibility is verified, THE compile suite SHALL use the
    workspace MSRV and declared features without undocumented flags.
17. WHEN exact-target engine evidence runs, THE integration suite SHALL exercise
    representative scalar, enum, input, object, interface, nullable, list-object,
    expected-type, and Void paths.
18. WHEN generated bindings are tested, THE suite SHALL preserve Feature 2/3 lifecycle,
    timeout, transport, GraphQL, and engine-domain error behaviour.
19. WHEN a verification result is registered, THE evidence record SHALL identify the
    Exact_Target, subject revision, capability scope, command, and result identity.
20. IF any Feature 4-owned capability lacks its required passing evidence, THEN THE
    completeness report SHALL retain that capability as `Missing` or `Partial`.

## Iteration and Feedback Notes

- Requirements-first workflow selected for Feature 4.
- Ground truth reviewed against the Exact_Target schema snapshot, the Definitive Go
  generated library, target-pinned Go generator sources and tests, current Rust
  generator templates, current generated output, and the Feature 1 ledger.
- The reviewed boundary corrects 68 coarsely routed rows before implementation: six
  Go-client trace/error declarations, 19 engine-generator declarations, and 43 module
  source/introspection declarations.
- Feature 4 closes 3,261 existing blocking rows when their evidence passes. The final
  `Implemented` versus `Idiomatic_Equivalent` split remains evidence-led; the spec does
  not optimize the label at the expense of idiomatic Rust.
- Explicit GraphQL `null` as a generated argument is not added beyond the Definitive Go
  surface. Raw_Request remains the lossless escape hatch. Concrete zero-like values are
  explicitly supported because Rust can preserve them without copying Go's zero-value
  omission weakness.
- The target contains no public union, mutation root, subscription root, or nullable
  list element. Regression fixtures still cover wrapper recursion and unsupported-kind
  drift so later targets fail explicitly rather than generating partial source.
