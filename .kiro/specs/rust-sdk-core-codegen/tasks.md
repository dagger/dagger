# Implementation Plan

- [x] 1. Establish the exact Feature 4 scope and Rust code-generation foundations
  - [x] 1.1 Apply the reviewed ownership correction and policy inventory
    - Add the exact transition that retains 3,261 capabilities under Feature 4, routes
      six trace/execution-error declarations to Feature 3, 19 generator-operation
      declarations to Feature 5, and 43 module-source/introspection declarations to
      Feature 6 without changing any existing status.
    - Register the 16 approved `rust-policy/core-codegen-*` capability IDs and the
      retained-scope digest; reject additions, removals, reordering, owner drift,
      fingerprint drift, and duplicate policy declarations.
    - Add golden fixtures preserving the pre-transition ledger and proving every
      capability outside the 68 corrected rows and 16 new policies is byte-equivalent.
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.11_
  - [x] 1.2 Reshape crate dependencies around a pure code generator
    - Remove `dagger-codegen`'s dependency on `dagger-sdk` and its `eyre`/`genco`
      rendering boundary; add the approved workspace-pinned `proc-macro2`, `quote`,
      `syn`, `graphql-parser`, `sha2`, `serde`, `serde_json`, and `thiserror`
      dependencies with the minimum required features.
    - Move the raw introspection wire model into `dagger-codegen` without exposing it
      from the application SDK; establish typed diagnostic, target, schema,
      projection, catalog, and renderer modules with no filesystem, process, network,
      engine, or ledger I/O.
    - Remove `dagger-bootstrap`'s dependency on `dagger-sdk`, connect it to the private
      completeness crate, and retain `proptest`, `trybuild`, and `tempfile` as the
      workspace-standard verification/orchestration tools.
    - Update the locked graph and cargo-deny policy only where the approved dependency
      graph requires it; preserve Rust 1.97.1, edition 2024, Apache-2.0, publishing
      boundaries, and `unsafe_code = "deny"`.
    - _Requirements: 2.11, 2.12, 9.10, 9.11, 9.14, 10.16_
  - [x] 1.3 Add shared code-generation strategies and recording test components
    - Add valid-first `proptest` strategies for target descriptors, raw and canonical
      schema fragments, recursive wrappers, names, defaults, directives, projection
      catalogs, artifact sets, capability mappings, and evidence records.
    - Add deterministic recording selection/session/formatter/filesystem components;
      centralize 256-case pure and 128-case filesystem/concurrency defaults while
      keeping every property at or above the 100-case floor.
    - Persist minimized regressions and keep target-wide finite inventories available
      for exhaustive iteration rather than random sampling.
    - _Requirements: 2.3, 2.4, 2.10, 2.12, 9.1, 9.2, 9.15, 10.1-10.20_
  - [x] 1.4 Property test: Property 1 — ownership correction is exact and status-neutral
    - Implement a reference-transition `proptest` with at least 256 generated owner,
      status, order, fingerprint, and policy mutations around the exact source ledger;
      accept only the approved 3,261/6/19/43/16 partition and require status identity.
    - Test identifier: `property_01_ownership_correction_exact_status_neutral`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 1: Ownership correction is exact and status-neutral`
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6_

- [x] 2. Checkpoint: scope, dependencies, and test foundations are green
  - Run formatting, locked checking, completeness/codegen unit and property tests,
    clippy, rustdoc, and cargo-deny; require Property 1 to pass, the prior ledger to
    remain status-identical, and `dagger-codegen` to compile without `dagger-sdk` or
    an I/O/runtime dependency.

- [x] 3. Implement exact-target input and the canonical schema compiler
  - [x] 3.1 Add target identity, bounded schema input, and digest verification
    - Decode `CodegenTarget` exclusively from `completeness/target.json`, including
      Dagger/engine/schema, Go SDK, sdk-sdk, Rust SDK, edition, toolchain, and schema
      digest identities; validate revisions and versions before schema projection.
    - Bound snapshot bytes before decoding, hash the checked bytes, accept only the
      approved introspection envelopes, and keep absent/unknown raw fields observable
      for validation diagnostics.
    - Reject target, snapshot, and caller disagreement before rendering or any
      publication-capable result exists.
    - _Requirements: 1.11, 2.1, 2.2, 2.11, 2.12_
  - [x] 3.2 Build the canonical named-type and recursive-wrapper model
    - Add ordered canonical values for the query root, scalars, objects, interfaces,
      enums, input objects, fields, arguments, input fields, enum values, interface
      edges, descriptions, deprecations, and exact source coordinates.
    - Represent wrappers only as recursive `TypeUse { nullable, shape }`; bound depth,
      detect repeated active nodes, resolve every named reference, reject duplicate
      Wire_Names, and diagnose unsupported public roots/kinds without partial output.
    - Exclude introspection `__*` types from public binding coordinates only after
      validating a structurally sound response.
    - _Requirements: 2.3, 2.4, 2.5, 2.6, 2.9, 2.10, 2.11, 2.12, 3.9-3.13_
  - [x] 3.3 Parse defaults and validate directive definitions/applications
    - Parse GraphQL constants with `graphql-parser`, typecheck them recursively against
      scalar, enum, list, input-object, and nullability definitions, and retain the
      normalized value for documentation/fingerprints without creating a Rust default.
    - Validate all 12 directive definitions, 14 directive arguments, active
      applications, legacy deprecation fields, and the exact canonical coordinate
      inventory; accumulate independent sorted diagnostics within each validation
      phase.
    - Add fixed fixtures for every stable schema diagnostic and target-specific count.
    - _Requirements: 2.3-2.9, 3.11-3.13, 5.4, 5.11, 7.9-7.13_
  - [x] 3.4 Implement stable, safe generator diagnostics
    - Add typed diagnostic codes, schema/path coordinates, related-coordinate context,
      stable sorting, and safe CLI rendering across identity, schema, projection,
      naming, completeness, formatting, drift, and publication boundaries.
    - Ensure caller-controlled bad JSON, wrappers, defaults, references, directives,
      paths, and schema kinds return errors without panic, `unwrap`, invariant-free
      `expect`, environment disclosure, or partial canonical values.
    - _Requirements: 2.5-2.9, 2.11, 2.12, 4.15, 6.11, 6.12, 7.13, 8.6, 9.14_
  - [x] 3.5 Property test: Property 3 — target identity gates all publication
    - Generate at least 256 target/snapshot/scope/source-revision mutations around the
      approved descriptor; compare to a reference identity chain and assert rejection
      produces no candidate publication or repository event.
    - Test identifier: `property_03_target_identity_gates_publication`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 3: Target identity gates all publication`
    - _Requirements: 1.11, 2.1, 2.2, 2.11_
  - [x] 3.6 Property test: Property 4 — schema validation is total and coordinate-complete
    - Generate at least 256 malformed schema graphs spanning missing coordinates,
      invalid references, wrapper cycles/depth, defaults, directives, duplicate names,
      and unsupported kinds; compare codes/coordinates to a small reference validator
      and assert no panic or render event.
    - Test identifier: `property_04_schema_validation_total_coordinate_complete`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 4: Schema validation is total and coordinate-complete`
    - _Requirements: 2.3, 2.4, 2.5, 2.6, 2.7, 2.8, 2.9, 2.12_
  - [x] 3.7 Property test: Property 5 — canonicalization and rendering ignore source order
    - Generate at least 256 independent permutations of types, fields, arguments,
      input fields, enum values, interface edges, directives, and directive arguments;
      require equal canonical models and byte-identical repeated rendered candidates.
    - Test identifier: `property_05_canonicalization_rendering_ignore_source_order`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 5: Canonicalization and rendering ignore source order`
    - _Requirements: 2.10, 9.1, 9.2_

- [x] 4. Checkpoint: exact-target canonical schema compilation is green
  - Run formatting, locked codegen/completeness tests, schema fixtures, Properties 3-5,
    clippy, rustdoc, and cargo-deny; require the checked target's complete coordinate
    inventory to validate and every malformed fixture to remain rejection-atomic.

- [x] 5. Implement recursive type, naming, scalar, and directive projection
  - [x] 5.1 Add recursive Rust type and scalar policies
    - Project Boolean/bool, Float/f64, target Int/i64, String/owned `String`, ID/`Id`,
      JSON/`Json`, Platform/`Platform`, Void/unit, enum, input-object, object, and
      interface leaves through the recursive wrapper graph.
    - Keep list and element absence independent, omit only redundant outer `Option`,
      and attach typed decode strategies for non-null violations, invalid scalar wire
      values, unknown enum values, and invalid list elements.
    - _Requirements: 3.1-3.17, 7.14, 10.8_
  - [x] 5.2 Implement the complete Rust 2024 name map
    - Tokenize underscores, case transitions, acronym boundaries, and digits; produce
      deterministic UpperCamelCase and snake_case identifiers, raw identifiers where
      legal, and stable suffix forms for `self`, `Self`, `super`, and `crate`.
    - Reserve primary types, traits, handles, methods, arguments, fields, variants,
      options, setters, constructors, module paths, test helpers, and handwritten
      crate-root exports before rendering; report both coordinates for every collision.
    - Retain the exact Wire_Name independently from every Rust identifier.
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, 10.13_
  - [x] 5.3 Project active and target-inactive directives
    - Apply registered `expectedType`, `deprecated`, `experimental`, and `enumValue`
      policies only after definition/application validation; fingerprint every
      inactive target directive definition and reject its change or first application
      until reviewed.
    - Carry typed-ID targets, deprecation reasons, stability notes, and validated enum
      aliases into projection records without inventing feature gates, duplicate Rust
      variants, or silently dropped directive metadata.
    - _Requirements: 6.8, 6.11, 7.9-7.13, 7.15, 8.10, 8.11, 10.14_
  - [x] 5.4 Build the semantic projection catalog and fingerprints
    - Produce exact binding keys and implementation fingerprints from canonical wire
      coordinates, Rust signatures, wrappers, arguments, directives, execution
      strategies, symbol paths, and evidence domains; exclude formatting from semantic
      fingerprints while preserving it in artifact digests.
    - Require one projection or diagnostic for every public coordinate and forbid
      catch-all/name-only compatibility mapping.
    - _Requirements: 1.7-1.10, 4.15, 10.1-10.3, 10.7_
  - [x] 5.5 Property test: Property 6 — recursive wrappers preserve independent absence
    - Generate at least 256 bounded named/list/nullability trees and compare projected
      Rust types and required construction paths to a recursive reference model;
      combine with compile-fail fixtures for required method/input positions.
    - Test identifier: `property_06_recursive_wrappers_preserve_independent_absence`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 6: Recursive wrappers preserve independent absence`
    - _Requirements: 3.9, 3.10, 3.11, 3.12, 3.13, 3.16, 3.17, 10.5, 10.8_
  - [x] 5.6 Property test: Property 7 — scalar projection and decoding are exact
    - Generate at least 256 supported/invalid scalar wire values and wrapper positions;
      compare projection and round-trip behaviour to the explicit scalar table and
      require typed failures for invalid or non-null-null responses.
    - Test identifier: `property_07_scalar_projection_decoding_exact`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 7: Scalar projection and decoding are exact`
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.14, 3.15, 7.14_
  - [x] 5.7 Property test: Property 19 — directive projection is explicit and drift-sensitive
    - Exhaust every active application and inactive target definition, then run at
      least 256 generated definition, argument, reason, target-name, fingerprint, and
      first-application mutations; compare to the closed directive-policy model.
    - Test identifier: `property_19_directive_projection_explicit_drift_sensitive`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 19: Directive projection is explicit and drift-sensitive`
    - _Requirements: 7.9, 7.10, 7.11, 7.12, 7.13, 10.14_
  - [x] 5.8 Property test: Property 20 — Rust naming is valid, exact, and collision-free
    - Generate at least 1,024 GraphQL names, acronyms, digits, Rust 2024 keywords,
      forbidden raw identifiers, case contexts, and collision pairs; parse emitted
      tokens with `syn`, compare case conversion to the reference tokenizer, and assert
      exact Wire_Name retention.
    - Test identifier: `property_20_rust_naming_valid_exact_collision_free`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 20: Rust naming is valid, exact, and collision-free`
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, 10.13_

- [x] 6. Implement object, interface, argument, enum, and input-object projection
  - [x] 6.1 Project every object, interface, edge, and field strategy
    - Produce one object handle, interface trait, interface client, declared object
      implementation, and reachable field operation for every public Exact_Target
      coordinate; retain the two single-underscore metadata objects and their four
      fields as exact, fingerprinted no-symbol policies matching the definitive Go
      generator.
    - Select lazy non-null handle, nullable ID probe, ordered list re-entry, executing
      value, or expected-type self-return as one total field strategy; validate the ID
      and concrete-type surface required by each strategy.
    - _Requirements: 4.2-4.10, 4.15, 6.8-6.12, 10.7, 10.12_
  - [x] 6.2 Project required and omittable argument APIs
    - Place every non-null/no-default argument directly in method signatures and every
      nullable/defaulted argument in an owned field-specific options value; retain
      parsed defaults only for docs/fingerprints.
    - Generate ordinary and `_opts` method plans, exact Wire_Names, `Option<T>` omission,
      typed-ID/list encoders, and all-or-nothing serialization/lazy-resolution plans.
    - _Requirements: 5.1-5.15, 6.3-6.7, 10.5, 10.6, 10.11_
  - [x] 6.3 Project closed enums and owned input objects
    - Emit one exact-wire enum variant plan for every canonical target value, attach
      every validated `enumValue` Wire_Name as a decode alias without creating a
      colliding Rust variant, and retain typed unknown decode failure; emit owned,
      non-exhaustive input objects whose constructors require required fields and whose
      setters/serialization omit only absent fields.
    - Preserve optional zero-like values, recursive wrappers, documentation, and exact
      serde Wire_Names.
    - _Requirements: 3.17, 7.1-7.8, 8.3-8.5, 10.9, 10.10_
  - [x] 6.4 Property test: Property 8 — named-type and field projection is exhaustive
    - Exhaust all target named types, interface edges, and fields and run at least 256
      generated mini-schema variations; assert exactly one applicable public or
      target-private projection per coordinate and a diagnostic rather than omission
      for every lossy case.
    - Test identifier: `property_08_named_type_field_projection_exhaustive`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 8: Named-type and field projection is exhaustive`
    - _Requirements: 4.2, 4.3, 4.4, 4.5, 4.6, 4.15_
  - [x] 6.5 Property test: Property 13 — argument omission is distinct from zero-like values
    - Generate at least 256 required/nullable/defaulted argument plans and Boolean,
      numeric, string, list, enum, and input values; compare emitted structured
      arguments to a reference omission model and require defaults to remain absent.
    - Test identifier: `property_13_argument_omission_distinct_zero_like_values`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 13: Argument omission is distinct from zero-like values`
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 5.8, 5.9, 5.10, 5.11, 10.6_
  - [x] 6.6 Property test: Property 17 — enum mapping preserves canonical wire values and aliases
    - Exhaust all 84 target enum values and run at least 256 generated enum/name/value
      variations; require exact-case canonical encode/decode, alias decode to the
      canonical variant, exhaustive coordinate accounting, and typed rejection of
      every value outside the generated closed set.
    - Test identifier: `property_17_enum_mapping_preserves_canonical_values_aliases`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 17: Enum mapping preserves canonical wire values and aliases`
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.15, 10.9_
  - [x] 6.7 Property test: Property 18 — input objects preserve requiredness and concrete values
    - Exhaust all target input fields and run at least 256 generated required/optional
      wrapper/value combinations; compare serialization to a reference object model,
      retain zero-like values, and pair with required-field compile failures.
    - Test identifier: `property_18_input_objects_preserve_requiredness_concrete_values`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 18: Input objects preserve requiredness and concrete values`
    - _Requirements: 7.5, 7.6, 7.7, 7.8, 10.10_

- [x] 7. Checkpoint: the complete pure projection plan is green
  - Run formatting, locked codegen tests, exact-target inventory tests, Properties
    6-8/13/17-20, compile fixtures, clippy, rustdoc, and cargo-deny; require every
    target coordinate to have one strategy and no Rust source to be rendered from an
    invalid or incomplete plan.

- [x] 8. Implement handwritten scalar, typed-ID, and handle re-entry support
  - [x] 8.1 Add the public scalar newtypes and unit mapping support
    - Add documented private-storage `Id`, `Json`, and `Platform` newtypes with exact
      transparent serde, owned/string conversions, accessors, display, and value
      semantics; preserve JSON-encoded and platform strings without reinterpretation.
    - Integrate `Id` with the existing identity/Loadable contracts and map successful
      Void execution to `()` while rejecting a represented non-null payload.
    - _Requirements: 3.5, 3.6, 3.7, 3.8, 3.14, 3.15, 6.1, 6.2, 7.14_
  - [x] 8.2 Add target-typed replayable `IdInput<T>`
    - Implement the private ready-ID/lazy-resolver representation with cloneable,
      secret-safe `Debug`; accept a raw `Id` for any target without lookup and expose
      generated conversions only for exact objects/interfaces and declared implementors.
    - Resolve each list element once in input order, retain indexed typed failures, and
      complete all required resolutions before the containing document can execute.
    - _Requirements: 6.1-6.7, 6.11, 10.11_
  - [x] 8.3 Add one session-preserving identifier re-entry primitive
    - Extend the immutable selection/query support with crate-private typed
      `Query.node(id)` plus exact inline-fragment reconstruction; reuse it for nullable
      probes, object/interface lists, Loadable construction, and expected-type self
      returns.
    - Preserve the originating `SessionHandle`, response order/cardinality, concrete
      type Wire_Name, and typed errors without storing a connection/token/process in a
      generated handle.
    - _Requirements: 4.1, 4.7-4.10, 6.2, 6.8-6.12, 10.12_
  - [x] 8.4 Property test: Property 9 — lazy handles preserve the originating lease
    - Generate at least 256 client/session/selection/field combinations using recording
      executors; assert root and extended handles preserve session identity and perform
      zero I/O until an executing operation is awaited.
    - Test identifier: `property_09_lazy_handles_preserve_originating_lease`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 9: Lazy handles preserve the originating lease`
    - _Requirements: 4.1, 4.7, 6.9_
  - [x] 8.5 Property test: Property 10 — nullable handles reflect target presence
    - Generate at least 256 nullable ID-probe responses, selections, and sessions;
      require null to map to `None`, present IDs to correctly rooted same-session
      handles, and invalid responses to typed failures without partial handles.
    - Test identifier: `property_10_nullable_handles_reflect_target_presence`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 10: Nullable handles reflect target presence`
    - _Requirements: 4.8, 4.9_
  - [x] 8.6 Property test: Property 11 — object-list re-entry preserves structure
    - Generate at least 256 ordered ID lists, wrapper plans, concrete types, and session
      identities; compare cardinality/order/selection fragments to a reference re-entry
      model and reject generated schemas without the required ID surface.
    - Test identifier: `property_11_object_list_reentry_preserves_structure`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 11: Object-list re-entry preserves structure`
    - _Requirements: 4.10, 6.9, 6.10, 6.12, 10.12_
  - [x] 8.7 Property test: Property 15 — typed ID compatibility is closed and all-or-nothing
    - Generate at least 256 object/interface compatibility graphs, raw IDs, handle
      lists, orderings, and resolver failures; compare to a closed reference relation,
      assert zero lookup for raw IDs and zero containing request after any failure, and
      add compile-fail cases for incompatible handles.
    - Test identifier: `property_15_typed_id_compatibility_closed_all_or_nothing`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 15: Typed ID compatibility is closed and all-or-nothing`
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 10.11_
  - [x] 8.8 Property test: Property 16 — expected-type self return is type- and selection-safe
    - Generate at least 256 expected-type applications, parent/interface graphs,
      selections, and invalid targets; require exact same-session parent reconstruction
      and inline fragments for valid cases and `EXPECTED_TYPE_INVALID` otherwise.
    - Test identifier: `property_16_expected_type_self_return_type_selection_safe`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 16: Expected-type self return is type- and selection-safe`
    - _Requirements: 6.8, 6.9, 6.10, 6.11_

- [x] 9. Wire generated execution through the existing query runtime
  - [x] 9.1 Add reusable wrapper-correct execution helpers
    - Extend selection decoding only where required for nullable/list wrapper context,
      enum/custom scalar values, ID probes, and Void; preserve Feature 2/3 error sources
      and request timeout/close fences.
    - Keep non-null object handles lazy, make nullable/list probes perform their one
      documented request, and never return partially decoded values or retry a request.
    - _Requirements: 3.9-3.15, 4.7-4.14, 6.6, 6.7, 10.18_
  - [x] 9.2 Preserve argument encoding and failure ordering
    - Route concrete values through the existing encoder and typed IDs through lazy
      arguments; use Wire_Names only, omit only absent options, retain zero-like
      values, and complete document construction before session execution.
    - Add fixed tests for close-before-call, timeout, transport, GraphQL, engine-domain,
      decoding, argument-encoding, and lazy-identifier failures through representative
      generated-like operations.
    - _Requirements: 4.11-4.14, 5.5-5.15, 6.5-6.7, 10.18_
  - [x] 9.3 Property test: Property 12 — executing fields preserve runtime behaviour
    - Generate at least 128 field/output strategies and lifecycle, timeout, transport,
      GraphQL, engine-domain, decode, and cancellation schedules against a recording
      session; compare exact typed results/events to the Feature 2/3 reference path.
    - Test identifier: `property_12_executing_fields_preserve_runtime_behaviour`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 12: Executing fields preserve runtime behaviour`
    - _Requirements: 4.11, 4.12, 4.13, 4.14, 10.18_

- [x] 10. Checkpoint: generated-runtime primitives are green
  - Run formatting, locked SDK/codegen unit and property tests, Properties 9-12/15-16,
    compile-fail ID compatibility tests, rustdoc, clippy, and cargo-deny; require lazy
    paths to remain request-free, executing paths to preserve typed Feature 2/3 errors,
    and every ID failure to be request-atomic.

- [x] 11. Render the idiomatic per-type generated Rust API
  - [x] 11.1 Replace the dynamic generator with a syntax-aware renderer
    - Render a complete in-memory candidate from `ProjectionPlan` with `quote`, parse
      every file with `syn`, and return coordinate/artifact diagnostics for any invalid
      tokens; make rendering incapable of reopening raw schema or changing a mapping
      decision.
    - Emit `dagger-sdk/src/gen/mod.rs` plus one stable module per public named type,
      sorted private declarations and public re-exports, and machine-readable
      provenance naming target revision, schema digest, generator format, and ownership.
    - Treat legacy `dagger-sdk/src/gen.rs` as the one explicit predecessor, not as a
      permanent wildcard or a reason to adopt unknown files.
    - _Requirements: 4.2-4.6, 8.5, 9.1-9.3, 9.9, 9.10, 10.4_
  - [x] 11.2 Render options, arguments, enums, and input objects
    - Emit owned, cloneable, debuggable, defaultable, non-exhaustive options with public
      `Option<T>` fields, fluent `with_*` setters, ordinary required-only methods, and
      borrowing `_opts` forms; do not promise equality for lazy ID resolvers.
    - Emit exact-wire closed enums and owned/non-exhaustive input objects with required
      constructors, fluent optional setters, recursive types, and precise serde rename/
      omit attributes; use existing argument encoders rather than hand-built GraphQL.
    - _Requirements: 3.16, 3.17, 5.1-5.15, 7.1-7.8, 10.5, 10.6, 10.9, 10.10_
  - [x] 11.3 Render handles, interface traits, and executing operations
    - Emit the uniform session/selection handle representation, one complete statically
      dispatched trait and concrete handle per interface, declared object trait
      implementations, sealed Loadable/Into_ID integration, and every field operation.
    - Use return-position futures where a public interface method must promise `Send`;
      do not impose object safety or expose a second connection/runtime abstraction.
    - Route each method through its approved lazy, probe, re-entry, self-return, or
      executing strategy and preserve exact Wire_Names independently of Rust names.
    - _Requirements: 4.1-4.15, 6.1-6.12, 8.1-8.6, 10.4, 10.7, 10.11, 10.12_
  - [x] 11.4 Render complete, sanitized public documentation
    - Normalize schema text, preserve paragraphs/code, make URLs explicit, escape
      untrusted markup, close fences deterministically, and reject unsupported control
      text with its coordinate.
    - Document every public module/type/trait/method/options value/options field/scalar/
      enum/variant, including Wire_Name, omission/default behaviour, deprecation, and
      experimental reasons, without module-wide `missing_docs` or rustdoc suppression.
    - _Requirements: 7.10, 7.11, 8.7-8.13, 10.15_
  - [x] 11.5 Property test: Property 14 — options are owned, wire-exact, and reusable
    - Generate at least 256 options plans and two-call reuse schedules with ordinary,
      zero-like, input-object, enum, raw-ID, and lazy-handle values; compare both
      documents/events to a reference model and assert caller-observable state is
      unchanged and encoding failure precedes transport.
    - Test identifier: `property_14_options_owned_wire_exact_reusable`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 14: Options are owned, wire-exact, and reusable`
    - _Requirements: 5.12, 5.13, 5.14, 5.15_
  - [x] 11.6 Property test: Property 21 — generated documentation is complete and warning-free
    - Generate at least 256 descriptions with links, brackets, code fences, control
      text, missing content, deprecations, and experimental reasons; compare sanitized
      semantics to a reference policy, parse rendered files, and deny rustdoc warnings.
    - Test identifier: `property_21_generated_documentation_complete_warning_free`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 21: Generated documentation is complete and warning-free`
    - _Requirements: 7.10, 7.11, 8.7, 8.8, 8.9, 8.10, 8.11, 8.12, 8.13, 10.15_

- [x] 12. Generate exhaustive compile and query-projection verification
  - [x] 12.1 Generate the positive public-reachability program
    - Derive a test program from the semantic catalog that references every generated
      public type, trait, handle, options value, field, method, scalar, enum, variant,
      and input constructor through the supported `dagger_sdk` namespace without
      executing an engine request.
    - Inspect final source with `syn`, prove exact equality between expected and
      referenced public symbols, and record the covering test entry on each binding.
    - _Requirements: 4.2-4.6, 8.7, 10.4, 10.16_
  - [x] 12.2 Add generated and representative compile-fail contracts
    - Generate required method/input omission cases and add focused `trybuild` cases for
      compatible/incompatible expected-type handles, interface implementors, ordinary
      features, and the no-`gen` handwritten raw client boundary.
    - Keep compiler fixtures stable at the error-class/public-name level and use the
      declared MSRV/features without undocumented flags.
    - _Requirements: 3.16, 3.17, 6.1-6.4, 8.15, 10.5, 10.11, 10.16_
  - [x] 12.3 Generate the complete structured query-projection suite
    - Derive one recording-executor case for every Exact_Target field and argument;
      parse structured documents and assert exact field/argument Wire_Names, wrappers,
      required presence, omission, and category-appropriate concrete values.
    - Record request counts, lazy/probe/executing boundaries, ID-resolution ordering,
      nullable/list re-entry roots, inline fragments, and typed pre-transport failures.
    - _Requirements: 4.6-4.15, 5.1-5.15, 6.3-6.10, 10.6, 10.7, 10.11, 10.12, 10.18_
  - [x] 12.4 Property test: Property 22 — the supported public surface respects release policy
    - Generate at least 256 synthetic public API plans and exercise the fixed target
      compile matrix; require exact supported-namespace reachability, declared MSRV/
      features, breaking-fragment detection, and a compiling handwritten raw client
      when `gen` is disabled.
    - Test identifier: `property_22_supported_public_surface_respects_release_policy`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 22: The supported public surface respects release policy`
    - _Requirements: 8.14, 8.15, 10.4, 10.16_
  - [x] 12.5 Property test: Property 28 — query projection covers every wire coordinate
    - Exhaust all 720 fields and 611 arguments and run at least 256 generated concrete/
      omission value plans; assert exact equality between catalog and observed
      coordinates, exact Wire_Names, and zero unknown or unobserved coordinates.
    - Test identifier: `property_28_query_projection_covers_every_wire_coordinate`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 28: Query projection covers every wire coordinate`
    - _Requirements: 10.7_

- [x] 13. Checkpoint: generated public source and exhaustive verification are green
  - Generate into private test state, then run formatting, locked SDK/codegen tests,
    Properties 14/21-22/28, positive and negative compile suites, all target projection
    cases, no-default-features checking, rustdoc with warnings denied, clippy, and
    cargo-deny; require no module-wide lint suppression or handwritten generated fix.

- [ ] 14. Implement deterministic check/update orchestration and confined publication
  - [ ] 14.1 Add the typed bootstrap generation command
    - Implement mutually exclusive `dagger-rust generate --workspace ... --check` and
      `--update` modes with repository-relative checked defaults and narrow fixture
      overrides that cannot widen ownership outside an explicit temporary test root.
    - Read only regular no-follow target/schema/ledger/mapping/manifest inputs, return
      sorted stable diagnostics for bad paths/UTF-8/JSON/schema/mappings, print nothing
      on successful check, and report changed paths without contents/secrets on update.
    - _Requirements: 2.11, 2.12, 9.4-9.6, 9.12-9.15_
  - [ ] 14.2 Format and finalize the complete candidate in private state
    - Use a unique per-process temporary directory, resolve `rustfmt` through the pinned
      toolchain, validate its version, format every Rust candidate, reparse output, and
      compute final artifact/provenance digests before manifest assembly.
    - Prohibit `cargo fix` and any semantic compiler rewrite; ensure formatting failure
      cannot touch the checkout or committed manifest.
    - _Requirements: 9.1-9.4, 9.8, 9.10, 9.11, 9.15_
  - [ ] 14.3 Compare the exhaustive owned output set in check mode
    - Validate normalized repository-relative artifact paths and define ownership only
      from candidate manifest paths, prior manifest paths, and the explicit legacy
      predecessor; reject unknown generated-looking files, symlinks, non-regular paths,
      traversal, and destinations outside declared roots.
    - Report the complete sorted added/removed/changed set without mutation and fail on
      any generated source, test, or manifest drift.
    - _Requirements: 9.3, 9.4, 9.5, 9.6, 9.9, 9.12, 9.13_
  - [ ] 14.4 Add transactional update, rollback, and recovery
    - Acquire the update-only publication lock, revalidate planning inputs, stage and
      flush all candidates beside their destinations, atomically replace each changed
      file, and retire only previously declared obsolete paths.
    - Record rollback state, restore every completed replacement/retirement after any
      failure, diagnose incomplete rollback, recover or reject stale transactions, and
      never broad-delete a generated directory.
    - _Requirements: 9.6, 9.7, 9.8, 9.9, 9.15_
  - [ ] 14.5 Property test: Property 23 — provenance and output ownership are exhaustive
    - Generate at least 256 prior/candidate manifest and artifact-tree combinations,
      including unknown files, traversal, symlinks, legacy predecessor, target drift,
      and obsolete files; compare the admitted change set to a no-wildcard ownership
      reference model and validate provenance on every candidate.
    - Test identifier: `property_23_provenance_output_ownership_exhaustive`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 23: Provenance and output ownership are exhaustive`
    - _Requirements: 9.3, 9.6, 9.9, 9.12_
  - [ ] 14.6 Property test: Property 24 — verification is pure, complete, and concurrency-safe
    - Run at least 128 generated concurrent check schedules and artifact differences
      with deterministic barriers/private temporary roots; assert zero worktree writes,
      complete sorted drift, independent state, and failure for every changed artifact.
    - Test identifier: `property_24_verification_pure_complete_concurrency_safe`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 24: Verification is pure, complete, and concurrency-safe`
    - _Requirements: 9.4, 9.5, 9.13, 9.15_
  - [ ] 14.7 Property test: Property 25 — publication is atomic and failure-preserving
    - Inject at least 128 generated validation, format, flush, rename, retirement, and
      rollback failures across artifact sets; assert each visible file is complete,
      failures restore prior bytes, and no undeclared path changes.
    - Test identifier: `property_25_publication_atomic_failure_preserving`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 25: Publication is atomic and failure-preserving`
    - _Requirements: 9.7, 9.8_
  - [ ] 14.8 Property test: Property 26 — semantic source and formatting have single owners
    - Generate at least 256 projection/token/format combinations; require semantic
      changes to alter the pre-format generator output, formatting-only changes to
      retain the semantic fingerprint, and final bytes to match the pinned formatter
      without compiler fix-up events.
    - Test identifier: `property_26_semantic_source_formatting_single_owners`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 26: Semantic source and formatting have single owners`
    - _Requirements: 9.10, 9.11_
  - [ ] 14.9 Property test: Property 27 — bootstrap input failure is diagnostic
    - Generate at least 128 invalid path, symlink, file-type, UTF-8, JSON, target, schema,
      formatter, and permission cases; assert stable non-zero diagnostics, no panic,
      no secret/environment disclosure, and byte-identical owned outputs.
    - Test identifier: `property_27_bootstrap_input_failure_diagnostic`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 27: Bootstrap input failure is diagnostic`
    - _Requirements: 9.14_

- [ ] 15. Checkpoint: repository generation is deterministic and failure-atomic
  - Run formatting, locked bootstrap/codegen/completeness tests, Properties 23-27,
    concurrent check/update integration cases, rustdoc, clippy, and cargo-deny; require
    two identical private generations to be byte-identical, check mode to produce no
    writes, and every injected update failure to preserve the prior artifact set.

- [ ] 16. Close the exhaustive binding manifest and evidence contract
  - [ ] 16.1 Add closed compatibility mappings for retained Go capabilities
    - Add `completeness/core-codegen-mappings.json` with exact reviewed rules for schema
      operations, enums, options, inputs, interface conversions, ID/load/re-entry,
      serialization, 21 shared Go-codegen policies, and 16 Rust policies.
    - Match authority kind plus semantic signature and approved binding/evidence scope;
      reject catch-all, name-only, duplicate, ambiguous, wrong-owner, extra, missing,
      and fingerprint-drifted mappings.
    - _Requirements: 1.7, 1.8, 1.11, 10.1, 10.2, 10.3_
  - [ ] 16.2 Assemble the generated binding manifest as an exact join
    - Join the target descriptor, corrected ledger, reviewed mappings, projection
      catalog, and formatted artifacts into canonical JSON at
      `completeness/artifacts/core-codegen-bindings.json`.
    - Require exactly one fingerprint-matching record for every active Feature 4
      capability, explicit binding kind/symbol-or-policy/implementation fingerprint,
      non-empty evidence domains, and reviewed decision IDs for every materially
      different Rust public shape; do not let the manifest assign completeness status.
    - _Requirements: 1.7-1.11, 9.1-9.3, 10.1, 10.2, 10.3, 10.19, 10.20_
  - [ ] 16.3 Verify evidence freshness and conservative ledger transitions
    - Join only implementation/property/compile/projection/documentation/exact-target
      evidence matching target, subject revision, command, result digest, capability
      scope, projection fingerprint, implementation fingerprint, and required domain.
    - Keep source-only/compile-only/unrelated sdk-sdk evidence partial, expire drifted
      records, and leave every capability `Missing` or `Partial` until all record-specific
      evidence closes through Feature 1's sole status transition engine.
    - _Requirements: 1.8, 1.9, 1.10, 1.11, 10.19, 10.20_
  - [ ] 16.4 Property test: Property 2 — binding closure is a capability bijection
    - Generate at least 256 ledgers, catalogs, mapping rules, decisions, and evidence
      domain combinations around the exact retained target; compare manifest acceptance
      and resulting conservative status to an independent set/bijection model.
    - Test identifier: `property_02_binding_closure_capability_bijection`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 2: Binding closure is a capability bijection`
    - _Requirements: 1.7, 1.8, 1.9, 1.10, 10.1, 10.2, 10.3, 10.19, 10.20_
  - [ ] 16.5 Property test: Property 30 — evidence cannot outlive its subject
    - Generate at least 256 accepted records and single/multiple mutations of target,
      subject revision, command identity, result digest, capability scope, projection,
      implementation, and evidence domain; independently recompute freshness and reject
      every mismatched claim.
    - Test identifier: `property_30_evidence_cannot_outlive_subject`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 30: Evidence cannot outlive its subject`
    - _Requirements: 1.9, 10.19, 10.20_

- [ ] 17. Wire repository generation and exact-target core conformance
  - [ ] 17.1 Replace the live-schema/cargo-fix Dagger generation path
    - Change `toolchains/rust-sdk-dev` so `WithGeneratedClient` invokes checked-input
      `dagger-rust generate --update`, returns only the declared change set, and never
      mounts live introspection or runs `cargo fix`.
    - Add `GeneratedClientCheck` for check mode, positive/negative compile suites,
      exhaustive query projection, warning-denied rustdoc, and binding/evidence
      verification; keep format/check/test/clippy/doc/deny functions independently
      visible.
    - _Requirements: 9.4, 9.10, 9.11, 9.12, 9.13, 10.4-10.16_
  - [ ] 17.2 Add focused exact-target generated-client conformance
    - Start and verify the engine revision from `target.json`, connect through Feature
      3, and exercise generated scalar/custom scalar, enum, input object, lazy object,
      interface, nullable handle, ordered object list, raw/handle expected type,
      self-return re-entry, and Void operations.
    - Exercise explicit zero-like options including `keepGitDir: false` plus close,
      timeout, GraphQL, engine-domain, and decode failures; treat Git URL/ref strings as
      opaque and do not parse or choose CLI/module-source `@` versus GitRef-setting `#`.
    - Emit deterministic capability-scoped evidence only for behaviours actually
      observed against the exact target.
    - _Requirements: 4.1, 4.7-4.14, 5.5-5.11, 6.3-6.10, 7.1-7.14, 10.17, 10.18, 10.19_
  - [ ] 17.3 Property test: Property 29 — exact-target conformance spans every generated category
    - Generate at least 256 conformance/evidence admission records with target, command,
      category, operation, result, and capability-scope mutations; compare acceptance to
      a reference category matrix and exhaust the finite live Exact_Target category set
      before any run can satisfy Feature 4 evidence.
    - Test identifier: `property_29_exact_target_conformance_spans_every_generated_category`.
    - Tag: `// Feature: rust-sdk-core-codegen, Property 29: Exact-target conformance spans every generated category`
    - _Requirements: 10.17_

- [ ] 18. Checkpoint: manifest closure and exact-target conformance are green
  - Run formatting, locked workspace tests, Properties 2/29-30, generated check, all
    compile/projection suites, exact-target `CoreConformance`, rustdoc, clippy, and
    cargo-deny; require every passing evidence record to name the exact target and only
    its observed capability scope, with unclosed rows remaining honestly blocking.

- [ ] 19. Stabilize documentation, public compatibility, and committed outputs
  - [ ] 19.1 Document the durable generated-client and maintenance contracts
    - Add `//!` ownership/invariant documentation to handwritten generator/bootstrap/
      completeness/runtime modules and `///` guarantees, caller assumptions, failure,
      omission, lifecycle, and security behaviour to every handwritten public item.
    - Add a maintainer guide for target refresh, mapping review, check/update, localized
      generated diffs, manifest/evidence interpretation, rollback recovery, exact-target
      conformance, and dependency/security review.
    - Keep production comments focused on durable WHY/invariants; do not refer to spec
      feature names, task numbers, or property IDs outside test traceability tags.
    - _Requirements: 8.7-8.13, 9.3-9.15, 10.15, 10.19, 10.20_
  - [ ] 19.2 Fence the intended public and feature surface
    - Update the Rust public API snapshot, crate-root re-exports, examples, and feature
      matrix; include the required Rust SDK breaking-change fragment for corrections
      that change the existing generated public shape.
    - Prove `gen` on/off, all-features, ordinary MSRV, and documentation builds; add
      source-policy tests forbidding generated edits, module-wide lint suppression,
      panic shortcuts, compiler fix-ups, and planning metadata in production comments.
    - _Requirements: 8.12-8.15, 9.10, 10.4, 10.15, 10.16_
  - [ ] 19.3 Publish the first complete generated artifact set into the worktree
    - Run explicit update from the checked target, assert the localized diff equals the
      manifest-declared change set, remove only the manifest-owned legacy `gen.rs`, and
      verify a subsequent check and `dagger generate -y` are clean.
    - Register only passing implementation/test/documentation/exact-target evidence,
      derive ledger statuses through Feature 1, and regenerate committed reports; do
      not optimize the `Implemented` count or relabel any capability lacking its own
      required evidence.
    - _Requirements: 1.7-1.11, 9.1-9.13, 10.1-10.20_
  - [ ] 19.4 Fence dependency and generated-surface security policy
    - Extend cargo-deny and repository Rust security configuration/tests for the final
      locked graph, direct dependency/features, generated serde/documentation, path
      confinement, process invocation, and credential-safe diagnostics.
    - Keep generated/runtime code under `unsafe_code = "deny"`; add regression tests
      for the approved boundaries and resolve every actionable advisory or policy
      failure before final evidence is accepted.
    - _Requirements: 2.12, 8.12, 8.13, 9.6-9.15, 10.16_

- [ ] 20. Final checkpoint: Feature 4 is releasable
  - Run `cargo fmt --all --check`, locked workspace check/test/clippy, warning-denied
    rustdoc, no-default-features SDK tests, cargo-deny, direct generation check,
    `GeneratedClientCheck`, `CoreConformance`, repository Dagger generation, and Rust
    security checks.
  - Require all 30 property identifiers, all target-wide finite inventories, compile
    pass/fail suites, 720-field/611-argument projection coverage, exact-target evidence,
    binding-manifest bijection, regenerated reports, public API/doc fences, and a clean
    generated-output check to pass; every capability missing any declared domain must
    remain `Missing` or `Partial`.

## Task Dependency Graph

```text
1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 -> 8 -> 9 -> 10
  -> 11 -> 12 -> 13 -> 14 -> 15 -> 16 -> 17 -> 18 -> 19 -> 20
```

The checkpoint sequence is intentionally strict. Pure target/schema/projection logic
precedes runtime support; runtime support precedes rendering; rendered candidates and
their exhaustive tests precede repository publication; publication precedes manifest
closure; exact-target evidence precedes status movement. Subtasks within an
implementation task may proceed together only after their shared types settle and
their property tests remain independently runnable.

## Notes

- Every property-test subtask is mandatory. Pure/model properties run at least 256
  cases, filesystem/concurrency properties at least 128 cases, and fixed Exact_Target
  inventories are exhausted in addition to generated cases.
- Property tags live in tests only. Production comments explain durable schema,
  ownership, safety, query, and publication invariants without mentioning this spec,
  its feature number, tasks, or properties.
- The generated binding manifest is a completeness join, not a status assertion. Its
  existence cannot move a row; matching executable evidence must close every domain.
- Go remains definitive for generated-client behaviour not settled by the schema, but
  Rust naming, ownership, errors, traits, options, async composition, and documentation
  remain idiomatic Rust decisions.
- `sdk-sdk` results apply only to checks the pinned harness actually declares. They do
  not substitute for coordinate-complete compile, projection, or exact-target evidence.
- Checkpoint commits are the preferred review boundaries. Generated source replacement
  occurs only after the pure compiler, runtime support, renderer, exhaustive tests, and
  publication transaction are independently green.
