# Implementation Plan

- [x] 1. Establish the public package boundary and canonical module-authoring models
  - [x] 1.1 Add the exact-version procedural-macro companion
    - Add `dagger-sdk-macros` as an edition-2024, Rust-1.97.1 proc-macro crate with
      matching version, repository, Apache-2.0 metadata, workspace lints, README, and
      no runtime dependency on `dagger-sdk`.
    - Re-export the companion attributes from `dagger-sdk`, pin the path dependency to
      the exact workspace version, update the lockfile, and keep bootstrap, codegen,
      engine, and completeness crates private.
    - Amend `sdk/rust/ARCHITECTURE.md`, package-policy tests, cargo-deny roots, and
      `.github/workflows/rust-sdk-security.yml` from one publishable package to the
      approved two-package public graph without publishing either crate.
    - _Requirements: 2.3, 2.5, 17.3, 17.6_
  - [x] 1.2 Add the minimal public error and hidden authoring bridge ABI
    - Add documented `ModuleError`/`ModuleErrorDetail` construction, inspection,
      `Display`, `std::error::Error`, and standard `Into` support needed by fallible
      macro bridges, without yet coupling it to call dispatch.
    - Add exact-version, `#[doc(hidden)]` authoring fingerprints, object persistent-
      tuple access, scalar wrap/unwrap, method result, and generated-support re-export
      traits in `dagger-sdk`; keep JSON, wire naming, source interpretation, and runtime
      dispatch out of this bridge.
    - Add a minimal checked `crate::dagger_generated::__private` fixture prelude so
      macro expansion tests compile before the complete renderer exists.
    - _Requirements: 2.5, 4.3-4.6, 5.11-5.12, 11.6, 14.10-14.12_
  - [x] 1.3 Add strict canonical authoring wire models and shared strategies
    - Add pure typed target, package, source path/document, cfg, source coordinate,
      Rust symbol, wire name, digest, descriptor, registration, introspection, call,
      generated-assets, scope, and evidence values in their owning crates.
    - Use strict versioned serde, unknown-field rejection, canonical JSON, sorted maps
      and sets, typed digests, normalized package-relative paths, and explicit domain
      separators; keep host paths, credentials, I/O handles, sessions, and source
      contents out of durable provenance.
    - Add valid-first `proptest` strategies with 256-case pure defaults and 128-case
      filesystem/concurrency defaults, both above the 100-case minimum.
    - _Requirements: 8.1-8.4, 14.1, 15.1, 15.2, 17.13-17.15_
  - [x] 1.4 Property test: Property 31 — public package graph is closed and version-coherent
    - Generate at least 256 package graphs, dependency aliases, version/source
      mutations, feature sets, metadata variants, and private-crate reachability edges;
      admit only the exact-version two-package public graph with no runtime cycle or
      engine-checkout path.
    - Test identifier: `property_31_public_package_graph_closed_version_coherent`.
    - Add fixed `cargo metadata` and `cargo package --list` cases separately; do not
      spawn Cargo once per generated property case.
    - _Requirements: 2.3, 2.5, 17.3, 17.6_
  - [x] 1.5 Property test: Property 32 — canonical wire models round-trip without semantic loss
    - Generate at least 256 valid and invalid snapshots, descriptors, projections, call
      envelopes, asset manifests, and evidence observations; require strict
      encode/decode equality and digest equality while rejecting unknown fields,
      invalid enums, malformed paths/digests, noncanonical JSON, and unsupported
      format/ABI versions.
    - Test identifier: `property_32_canonical_wire_models_round_trip_without_semantic_loss`.
    - _Requirements: 8.1-8.4, 14.1, 15.1-15.2, 17.13-17.15_

- [x] 2. Register the exact capability scope and typed diagnostic foundation
  - [x] 2.1 Correct and register the module-authoring capability inventory
    - Return the 17 lifecycle rows to Feature 5 or SDK sign-off ownership, retain the
      exact 79 existing module-authoring rows, and add all 32 approved Rust policy
      capabilities without changing status merely because source exists.
    - Record one owning requirement, authority, rationale, allowed terminal status,
      evidence domain, target identity, and blocker state for every row; update the
      derived scope digest and fixtures.
    - Reject missing, duplicate, moved, stale, delegated, name-only, catch-all,
      wrong-target, and out-of-domain mappings as a complete-set failure.
    - _Requirements: 1.1-1.10_
  - [x] 2.2 Add the closed compiler/runtime/evidence diagnostic taxonomy
    - Add typed discovery, cfg/path, export visibility, metadata, name, type, state,
      projection, dispatch, argument, handle, result, cancellation, publication,
      package, checkpoint, and evidence codes described by the design.
    - Retain normalized source and wire coordinates, typed safe source chains,
      deterministic multi-error ordering, generated-to-authored mappings, stable
      remediation, and redacted rendering.
    - Establish panic/unwrap/unsafe source-policy checks before feature code grows; the
      exhaustive taxonomy property is completed after all producing layers exist.
    - _Requirements: 14.1-14.6, 14.10-14.12_
  - [x] 2.3 Property test: Property 1 — capability scope is exact and evidence-local
    - Implement a reference-set PBT over at least 256 row, owner, authority, status,
      evidence-domain, target, ordering, correction, and omission mutations; admit only
      the approved 79/32 partition and keep all 17 corrected lifecycle rows outside the
      scope.
    - Test identifier: `property_01_capability_scope_exact_evidence_local`.
    - Require skipped, stale, failed, sibling, and out-of-domain observations to leave
      ledger state unchanged and every unclosed blocker visible.
    - _Requirements: 1.1-1.10_

- [x] 3. Implement the shared authoring grammar and thin procedural bridges
  - [x] 3.1 Implement the explicit authoring attribute grammar
    - Implement re-exported `object`, `interface`, `enum_type`, `scalar`, and `methods`
      attributes plus nested `field`, `state`, `constructor`, `function`, `context`,
      typed default, rename, documentation, deprecation, and target-metadata items.
    - Accept only stable target Rust syntax; reject unknown, malformed, duplicate, or
      conflicting shared metadata at its span and require exported types to be
      `pub(crate)` or `pub` without treating accessibility itself as export.
    - Keep unmarked `pub` items absent, preserve private fields/methods/error types, and
      never ask authors for a parallel schema or manual dispatcher.
    - _Requirements: 2.1-2.5, 2.7-2.12_
  - [x] 3.2 Emit crate-local access, invocation, and fingerprint bridges
    - Generate `crate::dagger_generated::__private` bridge implementations rather than
      hardcoding the SDK dependency name or inspecting the macro environment.
    - For objects, expose only owned persistent-state tuples plus safe reconstruction;
      for scalars, expose transparent wrap/unwrap; for functions, add uniquely named
      crate-private typed wrappers and perform `Into<ModuleError>` conversion within
      the declaring module.
    - Compute the normalized shared-grammar fingerprint while leaving Rust name/trait
      resolution, JSON, wire names, and all Dagger codecs to source compilation and
      descriptor-generated code.
    - _Requirements: 2.4-2.9, 4.3-4.6, 5.11-5.12, 11.6, 14.10-14.12_
  - [x] 3.3 Implement the source-side authoring parser over the same ABI
    - Parse the shared attribute grammar from immutable source documents, produce the
      same normalized fingerprints and source coordinates, and retain target/type
      validation as a compiler-only responsibility.
    - Add fixed malformed-span cases and shared generated strategies used by both
      source/parser and proc-macro tests; ensure a dependency rename still expands
      through the generated crate-local support module.
    - _Requirements: 2.6-2.9, 3.6, 14.10_
  - [x] 3.4 Property test: Property 2 — export is explicit and preserves Rust visibility
    - Generate at least 256 marked/unmarked, public/crate/private type, field, method,
      constructor, and function combinations; export exactly marked accessible types
      and marked members, diagnose inaccessible marked types, and prove no bridge
      broadens authored visibility.
    - Test identifier: `property_02_export_explicit_preserves_rust_visibility`.
    - _Requirements: 2.1-2.5, 2.10-2.12_
  - [x] 3.5 Property test: Property 3 — source and macro interpretations converge
    - Generate at least 256 shared-grammar declarations, dependency aliases, metadata
      mutations, and fingerprint mismatches; require equal normalized models for valid
      grammar, equal coordinates for malformed shared metadata, source-only semantic
      ownership, and compile failure for fingerprint or typed-signature disagreement.
    - Test identifier: `property_03_source_macro_interpretations_converge`.
    - Exercise macro expansion directly for generated cases and retain bounded
      representative rustc fixtures; do not spawn a compiler per PBT case.
    - _Requirements: 2.6-2.9, 3.6, 14.10_

- [x] 4. Checkpoint: package, scope, grammar, and bridge foundations are green
  - Run formatting, locked check/test for `dagger-sdk-macros`, the new module-authoring
    slices of `dagger-codegen`, `dagger-sdk`, and `dagger-sdk-completeness`, Properties
    1-3 and 31-32, representative macro compile fixtures, package-policy tests,
    warning-denied clippy/rustdoc for these packages, and `cargo deny check`.
  - Record exact commands, elapsed time, package selection, and the no-generation
    decision. Require no Dagger command, engine process, module invocation,
    network-backed graph, unrelated SDK build, or Core-binding regeneration.
  - Checkpoint evidence (2026-08-11, local cache state as executed, elapsed wall time):
    - `cargo fmt --all --check` — 0.77s.
    - `cargo check -p dagger-sdk-macros -p dagger-codegen -p dagger-sdk -p
      dagger-sdk-completeness --all-features --locked` — 0.65s.
    - Focused locked unit tests for `dagger-sdk-macros`, `dagger-codegen`, `dagger-sdk`,
      and `dagger-sdk-completeness` — 0.31s, 0.22s, 9.85s, and 3.35s respectively.
    - `cargo test -p dagger-sdk-completeness --test module_authoring_properties
      --locked` — 3.66s; Properties 1-3 and 31-32 passed with the documented generated
      case counts.
    - `cargo test -p dagger-sdk --test module_authoring_compile --locked` — 2.87s;
      the renamed-dependency pass fixture and five representative compile-fail fixtures
      passed.
    - Locked source-policy, package-policy, and engine packaging-graph tests — 0.90s,
      0.96s, and 7.89s respectively.
    - `cargo clippy -p dagger-sdk-macros -p dagger-codegen -p dagger-sdk -p
      dagger-sdk-engine -p dagger-sdk-completeness --all-targets --all-features
      --locked -- -D warnings` — 8.45s.
    - `RUSTDOCFLAGS="-D warnings" cargo doc -p dagger-sdk-macros -p dagger-codegen -p
      dagger-sdk -p dagger-sdk-engine -p dagger-sdk-completeness --all-features
      --no-deps --locked` — 9.43s; `cargo deny check` — 1.46s.
    - Offline package verification for `dagger-sdk-macros` and `dagger-sdk` — 0.54s
      and 5.91s; the latter used only the local exact-version macro patch because the
      companion has not been published yet. A locked no-default-features SDK check also
      passed in 6.30s.
    - Package selection remained confined to the five touched Rust packages and their
      Cargo dependencies. No Dagger command, engine process, module invocation,
      network-backed graph, unrelated SDK build, code generation, or Core-binding
      regeneration ran.

- [x] 5. Build the immutable source snapshot and deterministic discovery compiler
  - [x] 5.1 Implement the confined I/O builder for the pure source snapshot model
    - Keep the canonical `ModuleSourceSnapshot`, `ModuleSourcePath`, documents, cfg,
      package identity, and digest defined by Task 1 in `dagger-codegen`; add the
      filesystem builder only in
      `dagger-sdk-engine` after Feature 5 exact-one package selection.
    - Enforce lexical and resolved containment, reject symlink escape, bound file count,
      file size, and total bytes, and exclude targets, VCS state, generated output, and
      unrelated packages before constructing immutable UTF-8 inputs.
    - Execute no build script, Cargo/rustc command, user code, network request, or
      engine operation during discovery.
    - _Requirements: 3.7, 3.13, 3.14, 14.2, 14.7_
  - [x] 5.2 Implement Rust module, cfg, import, and alias resolution
    - Follow inline and explicit `mod` declarations plus confined `#[path]`; evaluate
      standard target/features and explicit custom cfg while rejecting unresolved
      build-script cfg.
    - Resolve `crate`/`self`/`super`, nested/grouped/renamed/glob imports, re-exports,
      Cargo dependency aliases, and terminating fully-applied type aliases; diagnose
      ambiguity, recursion, missing modules, cycles, and uninspectable exported macro
      output at authored coordinates.
    - Preserve ordinary Rust privacy and avoid pretending to implement arbitrary macro
      expansion or all of rustc.
    - _Requirements: 2.4-2.6, 3.5-3.7, 3.11-3.14_
  - [x] 5.3 Implement root and transitive local-type closure
    - Require exactly one explicit root, merge exported methods across impl blocks,
      traverse fields/signatures/interface implementations to a unique canonical local
      closure, and retain compatible checked generated Core/dependency references.
    - Reject missing/multiple roots, unsupported foreign types, stale generated
      provenance, and repeated incompatible symbols before descriptor construction;
      canonicalize independently of file, declaration, map, and traversal order.
    - _Requirements: 3.1-3.12_
  - [x] 5.4 Property test: Property 4 — source discovery is closed, deterministic, and inert
    - Generate at least 256 bounded module/import/type graphs and permutations plus at
      least 128 confined filesystem/symlink snapshots; compare closure, cfg/path
      resolution, diagnostics, and ordering to independent graph and containment
      models.
    - Test identifier: `property_04_source_discovery_closed_deterministic_inert`.
    - Assert zero process, user-code, network, engine, and out-of-root filesystem events
      for every accepted and rejected case.
    - _Requirements: 3.1-3.14_

- [x] 6. Implement the complete Rust type, object, interface, enum, and scalar policy
  - [x] 6.1 Add the exhaustive Rust-to-Dagger type-policy manifest and codecs
    - Implement typed recursive mappings for `String`, `i64`, `bool`, `f64`, `()`,
      `Vec<T>`, representable `Option<T>`, local objects/interfaces/enums/scalars, and
      checked generated object/interface handles.
    - Classify every Go-supported input/output row as equivalent, idiomatic equivalent,
      target-unsupported, or deferred with owner; reject unsupported integers,
      references, tuples, maps, generics, ranges, wrong JSON kinds, and lossy wrapper
      shapes without raw JSON fallback.
    - Preserve explicit false/zero/empty values, target numeric bounds, ID direction,
      recursive nullability/list shape, and Void as JSON null.
    - _Requirements: 6.1-6.16_
  - [x] 6.2 Generate descriptor-owned object state and root construction
    - Render object TypeDefs and codecs from the descriptor, using macro access bridges
      only to move persistent tuples across private fields; expose marked fields,
      persist marked private state, omit/default unmarked fields, and share explicit
      wire renames between TypeDef and state JSON.
    - Preserve local-interface concrete identity and generated-handle IDs, require one
      constructor or declared safe default, propagate constructor application errors,
      place the root constructor on Query, and generate no unsafe/zeroed/uninitialized
      state.
    - _Requirements: 4.1-4.15_
  - [x] 6.3 Implement interfaces and local implementation closure
    - Project exported trait docs, source maps, methods, and exact-once implementation
      relationships; validate object method shape against each interface.
    - Encode interface values as closed ID-plus-concrete-identity handles, reuse checked
      generated adapters for Core/dependency interfaces, and reject associated items,
      generics, and unsupported trait shapes.
    - _Requirements: 5.1-5.5_
  - [x] 6.4 Implement unit enums and transparent scalar newtypes
    - Project enum variants, docs, deprecation, source maps, explicit names, and the
      target common-prefix normalization; encode exact values and reject unknown wire
      values or payload variants.
    - Generate scalar codecs from the descriptor and macro wrap/unwrap bridge only for
      transparent one-field newtypes over supported scalar representations; reject
      transforming, multi-field, unit, or otherwise lossy declarations.
    - _Requirements: 5.6-5.12_
  - [x] 6.5 Property test: Property 5 — object state and construction are lossless and safe
    - Generate at least 256 object/field policies, values, identities, constructors,
      renames, and invalid state shapes; compare TypeDef/state projection and round-trip
      behavior to a field-policy reference model.
    - Test identifier: `property_05_object_state_construction_lossless_safe`.
    - _Requirements: 4.1-4.15_
  - [x] 6.6 Property test: Property 6 — interface projection and identity are closed
    - Generate at least 256 interface/object implementation graphs, method shapes,
      local/generated IDs, and unsupported associated items; require exact projection,
      unique relationships, identity round trips, and pre-projection rejection.
    - Test identifier: `property_06_interface_projection_identity_closed`.
    - _Requirements: 5.1-5.5_
  - [x] 6.7 Property test: Property 7 — enum and scalar codecs are exact
    - Generate at least 256 unit/payload enums, prefix/name layouts, transparent and
      invalid scalar shapes, valid values, and unknown wire values; require exact
      round trips or typed rejection without coercion.
    - Test identifier: `property_07_enum_scalar_codecs_exact`.
    - _Requirements: 5.6-5.12_
  - [x] 6.8 Property test: Property 8 — recursive type semantics preserve Rust distinctions
    - Generate at least 256 recursive supported/unsupported type trees, typed defaults,
      valid/invalid JSON values, explicit zero/empty values, IDs, nullability, lists,
      and numeric boundaries; compare projection and codecs to the closed algebra.
    - Test identifier: `property_08_recursive_type_semantics_preserve_rust_distinctions`.
    - _Requirements: 6.1-6.16_

- [x] 7. Implement functions, typed defaults, and target metadata
  - [x] 7.1 Compile sync, async, value, unit, and fallible function shapes
    - Merge explicit constructors and methods across impl blocks, preserve concrete
      receivers/arguments, classify sync versus async without blocking, expose only the
      successful side of `Result<T, E>`, and map unit success to target Void.
    - Recognize at most one marked generated `ModuleContext` parameter, omit it from
      TypeDefs, retain every data argument, and leave error conversion to the declaring
      macro bridge using standard `Into<ModuleError>`.
    - Reject generic functions and unsupported receiver/signature shapes before
      rendering.
    - _Requirements: 7.1-7.6, 7.15, 7.17_
  - [x] 7.2 Parse and canonicalize typed Rust default expressions
    - Accept only primitive literals, arrays, `None`/`Some(...)`, enum variants, and
      transparent scalar constructors; resolve their ordinary imports/aliases and check
      them against the declared argument type without execution.
    - Reject arbitrary calls, blocks, closures, macros, ambient constants, wrong kinds,
      invalid enum members, and out-of-range numbers; store canonical JSON and decode it
      through the runtime input codec.
    - _Requirements: 6.7-6.9, 7.6, 7.16_
  - [x] 7.3 Project complete target function and argument metadata
    - Preserve rustdoc, source maps, cache/default policy, check/generator/up flags,
      deprecation, optionality, typed default, default path/address, ordered ignore
      patterns, and explicit wire names through typed target enums.
    - Reject required deprecated arguments, normalization collisions, unknown metadata,
      and target-invalid combinations at the narrowest authored coordinate.
    - _Requirements: 7.7-7.16_
  - [x] 7.4 Property test: Property 9 — function shape is independent of execution syntax
    - Generate at least 256 equivalent sync/async, value/unit,
      infallible/fallible/context/data-argument declarations and unsupported signatures;
      require equal target success shapes and exact typed bridge invocation semantics.
    - Test identifier: `property_09_function_shape_independent_execution_syntax`.
    - _Requirements: 7.1-7.6, 7.15, 7.17_
  - [x] 7.5 Property test: Property 10 — function and argument metadata is exact and target-valid
    - Generate at least 256 valid/invalid metadata combinations, declaration orders,
      rename collisions, typed defaults, receivers, and generic shapes; compare exact
      canonical projection or source-located rejection to a typed policy model.
    - Test identifier: `property_10_function_argument_metadata_exact_target_valid`.
    - _Requirements: 7.7-7.16_

- [x] 8. Checkpoint: source discovery and the complete authoring type system are green
  - Run formatting and locked tests for `dagger-codegen`, `dagger-sdk-macros`, and the
    source-snapshot slice of `dagger-sdk-engine`, Properties 4-10, fixed source/cfg/path
    diagnostics, and representative generated codec/function compile fixtures.
  - Run package-scoped `cargo check` only for changed consumers. Record commands,
    elapsed time, and checked-asset reuse; do not run workspace clippy/rustdoc/security
    again unless this checkpoint changed dependencies or public package policy.
  - Require no engine, network graph, user/build-script execution, Core regeneration,
    unrelated SDK build, or out-of-root access.
  - Checkpoint evidence (2026-08-11, local cache state as executed, elapsed wall time):
    - `cargo fmt --all -- --check` — 0.92s.
    - `cargo test -p dagger-codegen --all-features --locked` — 261.03s. All 28 unit
      tests, every integration test, and Properties 4-10 passed; the new discovery and
      authoring-type unit slice completed in 15.73s, while the existing visible-schema
      Property 10 accounted for 143.47s. The isolated generated-candidate compile and
      warning-denied rustdoc fixture passed in 48.00s after its copied workspace was
      corrected to include the exact-version local macro companion.
    - `cargo test -p dagger-sdk-macros --locked` — 0.28s; all four unit tests passed.
    - `cargo test -p dagger-sdk-engine project::source_snapshot::tests --locked` —
      12.83s; both the 128-case confined snapshot property and fixed symlink rejection
      passed.
    - `cargo test -p dagger-sdk --test module_authoring_compile --locked` — 20.75s;
      the renamed-dependency fixture, async function, explicit interface implementation,
      and five representative compile-fail cases passed.
    - `cargo check -p dagger-codegen -p dagger-sdk-macros -p dagger-sdk-engine -p
      dagger-sdk --all-features --locked` — 4.53s.
    - The Rust artifact digest changed only because the checked Rust source set changed,
      from `sha256:3a60ac6ec8b62545e074da25092811bebab34c9d5dd2848509519b17ab72f848`
      to `sha256:3b6a6fdd2647164f9ab33fc02370b078d99b40c0d3fa4a2051b6fc44a16d8698`.
      The pinned baseline and all 18 harness evidence bindings were reconciled, and the
      focused locked `initial_baseline` guard passed without inventory, ledger, status,
      or evidence-claim changes.
    - No dependencies or public package policy changed, so workspace clippy, rustdoc,
      security, and `cargo deny` were not repeated. No Dagger command, engine process,
      network graph, module build script, module user code, Core regeneration, unrelated
      SDK build, or out-of-root access ran.

- [x] 9. Add the minimal stable module runtime surface and exact-version generated ABI
  - [x] 9.1 Complete query-to-module error conversion
    - Complete the foundational `ModuleError` with bounds and safe deterministic detail
      ordering, then implement `From<QueryError>` with single-GraphQL-error message/
      extension preservation and already-redacted fallback behavior; never insert
      `Debug`, panic payloads, credentials, or opaque transport data automatically.
    - _Requirements: 11.6, 11.9, 12.7, 14.4, 14.6_
  - [x] 9.2 Implement call-scoped cancellation, telemetry, and current-call values
    - Add clonable `ModuleCancellation` with `is_cancelled`/`cancelled`, SDK-owned
      `TelemetryContext`, and documented `CurrentCall` coordinates/operations.
    - Add `#[doc(hidden)]` clonable `ModuleContextBase` over one active `QueryBuilder`,
      cancellation signal, telemetry context, and current call; expose only the exact
      bridge accessors generated code needs and no connect/close/global behavior.
    - _Requirements: 12.1-12.8, 12.13-12.15, 13.3, 13.7-13.11_
  - [x] 9.3 Complete the hidden call, codec, registry, and sink ABI
    - Complete the canonical models from Task 1 with exact-version, `#[doc(hidden)]`
      `ModuleJson`, `CallSelector`, `CallEnvelope`,
      arguments, prepared calls, descriptor/registration views, codec/access traits,
      registry/sink traits, futures, receipts, and typed adapter errors.
    - Fence the public API manifest so only attributes, generated types/context/query,
      cancellation, telemetry, current-call, module error, and documented scalar/detail
      values are stable author-facing extensions.
    - _Requirements: 2.5, 9.1-9.3, 9.11-9.12, 11.10, 12.1-12.15, 14.11-14.12_

- [x] 10. Build the canonical descriptor, registration, and introspection projections
  - [x] 10.1 Assemble and hash the complete canonical `ModuleDescriptor`
    - Intern every type, field, implementation, function, argument, metadata item,
      source coordinate, Rust symbol, wire name, state/access bridge, dispatch
      coordinate, helper mapping, target, cfg, source, schema, generator, and macro ABI.
    - Normalize semantic ordering, compute typed IDs and domain-separated strict digest,
      retain change-owning provenance, and reject invariant/strict-decode failure before
      projection.
    - _Requirements: 8.1-8.4, 8.10-8.12_
  - [x] 10.2 Derive equivalent registration and module introspection
    - Project both views only from the descriptor; require structural equality for wire
      name, type/list/nullability, docs, deprecation, source map, metadata, arguments,
      defaults, and implementation relationships.
    - Emit exactly one Query root constructor, merge self introspection into the visible
      schema before Feature 4 rendering, reject Core/dependency collisions, and return
      neither partial projection on failure.
    - _Requirements: 4.14, 8.5-8.9, 8.13, 9.2_
  - [x] 10.3 Property test: Property 11 — descriptor identity is canonical and change-sensitive
    - Generate at least 256 valid requests, file/declaration/impl/container orderings,
      and single-domain mutations; require byte-identical equivalent descriptors and
      exact digest/provenance changes for every semantic input mutation.
    - Test identifier: `property_11_descriptor_identity_canonical_change_sensitive`.
    - _Requirements: 8.1-8.4, 8.10-8.12_
  - [x] 10.4 Property test: Property 12 — registration and introspection are equivalent projections
    - Generate at least 256 descriptors, metadata/type shapes, implementation graphs,
      root counts, schema collisions, and projection failures; compare both views to an
      independent structural model and require all-or-nothing output.
    - Test identifier: `property_12_registration_introspection_equivalent_projections`.
    - _Requirements: 8.5-8.9, 8.13_

- [x] 11. Render typed module assets, registry, ownership, and scoped regeneration
  - [x] 11.1 Render the complete generator-owned module tree
    - Replace the fixed probe content with descriptor, registration, introspection,
      concrete `ModuleContext`, concrete `ModuleQuery`, visible Core/self/dependency
      bindings, dispatch registry, crate-local support re-exports, generic entrypoint,
      catalogs, and strict generated-assets manifest.
    - Resolve the actual Cargo SDK dependency alias, keep public generated signatures
      rooted in `dagger_generated`, emit complete module/public docs, and keep authored
      source outside ownership.
    - _Requirements: 2.5, 8.5-8.8, 9.4-9.5, 12.3-12.6, 15.1-15.3_
  - [x] 11.2 Generate the closed typed dispatch registry
    - Emit exactly one parent/function match arm per callable descriptor item, typed
      fingerprint/signature assertions, receiver/argument/context bridge calls, async
      awaits, and typed success/application-error conversion.
    - Diagnose duplicate coordinates during compilation and generate distinct unknown
      parent/function failures without reflection, dynamic downcast, or stringly typed
      fallback.
    - _Requirements: 9.4-9.12_
  - [x] 11.3 Extend manifest-owned failure-atomic publication
    - Record each owned output, digest, semantic owner, input-domain digest, and
      regeneration class; preserve unknown/user bytes, remove only manifest-proven
      obsolete paths, stage complete candidates, and publish the manifest last.
    - Retain the prior valid tree for source/descriptor/render/format/publication
      failures and preserve the primary cause when rollback also fails.
    - _Requirements: 14.7-14.9, 15.1, 15.3-15.6_
  - [x] 11.4 Implement change-domain-scoped regeneration
    - Make identical inputs a byte-identical no-op; map authoring, visible-schema,
      target, and generator changes to only their owning outputs and consumers.
    - Keep ordinary tests on checked assets, avoid complete Core regeneration and every
      unrelated SDK build, and render a stable scoped repair for missing/stale assets.
    - _Requirements: 15.2, 15.7-15.13_
  - [x] 11.5 Property test: Property 13 — dispatch registry is a total closed mapping
    - Generate at least 256 callable descriptors, duplicate/unknown coordinates, and
      source orders; require exact-one arms, exact typed bridge selection, distinct
      unknown errors, duplicate rejection, and zero reflection/fallback paths.
    - Test identifier: `property_13_dispatch_registry_total_closed_mapping`.
    - _Requirements: 9.4-9.8, 9.11, 9.12_
  - [x] 11.6 Property test: Property 24 — rejection and generation failure are atomic
    - Inject at least 128 discovery, descriptor, render, format, flush, rename, removal,
      manifest, and rollback failures across generated trees; require no partial view or
      asset and byte-identical prior valid state on rejection.
    - Test identifier: `property_24_rejection_generation_failure_atomic`.
    - _Requirements: 14.7-14.9, 15.1, 15.3-15.6_
  - [x] 11.7 Property test: Property 25 — regeneration is scoped, deterministic, and convergent
    - Generate at least 256 input-domain change sequences, owned/unknown paths, stale
      outputs, and repeated runs; compare selected outputs/removals to a domain graph
      and require byte convergence with zero Core-wide or other-SDK events.
    - Test identifier: `property_25_regeneration_scoped_deterministic_convergent`.
    - _Requirements: 15.2, 15.7-15.13_

- [x] 12. Checkpoint: descriptor projections and generated module assets are green
  - Run formatting and locked `dagger-codegen`, `dagger-sdk-macros`, `dagger-sdk`, and
    `dagger-sdk-engine` tests for Properties 11-13 and 24-25, fixed projection/collision
    cases, generated source checks, publication fault injection, and one checked
    representative generated-module compile.
  - Compare checked Core bindings rather than regenerating them. Record commands,
    elapsed time, package selection, and generation-domain decision; require no engine,
    Dagger module, network graph, unrelated SDK build, or unowned diff.
  - Checkpoint evidence (2026-08-11, warm local cache as executed, elapsed wall time):
    - `cargo fmt --all -- --check` — 0.72s; package-scoped locked `cargo check` for
      `dagger-sdk-macros`, `dagger-codegen`, `dagger-sdk`, and `dagger-sdk-engine` —
      3.98s.
    - `cargo test -p dagger-sdk-macros --locked` — 0.26s; all four exact-version
      attribute/fingerprint ABI tests passed. `cargo test -p dagger-codegen --lib
      --locked` — 22.70s; all 29 compiler unit/property tests passed, including
      production rejection of malformed generated Rust before publication.
    - `cargo test -p dagger-codegen --test module_authoring_assets --locked` — 13.50s;
      Properties 11-13, 24, and 25 passed, including 256-case canonical/projection/
      registry/regeneration models, actual pure-pipeline rejection paths, and the
      offline representative generated-module compile.
    - The focused `dagger-sdk` runtime, compile-fixture, source-policy, and stable public
      API commands passed in 0.34s, 19.87s, 6.70s, and 1.72s respectively.
    - `cargo test -p dagger-sdk-engine --test module_publication_properties --locked` —
      19.00s; Property 24 passed across 128 publication fault schedules, successful
      publication preserved unknown paths and converged, and an inconsistent prior
      manifest could not authorize a different path.
    - The six engine-free module-authoring completeness properties passed in 3.63s.
      The changed Rust artifact digest was reconciled to
      `sha256:981f04f71c822648be0c68388ef2f56f13f2d40960b66be8911eb4c8ff83e4a3`;
      the root-independent byte-exact baseline guard passed in 35.53s with no inventory,
      status, or evidence-claim change.
    - Warning-denied package-scoped clippy and rustdoc passed in 6.28s and 4.48s.
      No dependency or package-publication policy changed, so security and `cargo deny`
      were not repeated.
    - The generation domain was module-owned assets only. Checked Core bindings were
      copied and compared, never regenerated; no Dagger command, engine process, module
      build, network graph, unrelated SDK build, user code, or out-of-root access ran.

- [ ] 13. Complete the concrete module context and definitive helper mapping
  - [ ] 13.1 Wire generated `ModuleContext` and `ModuleQuery` to the active session
    - Construct the concrete generated context only from `ModuleContextBase`; clone the
      one active `QueryBuilder` into the generated query root and preserve cancellation,
      telemetry, and current-call state.
    - Expose complete checked Core/self/dependency root methods, current-call/module/
      node/engine/local-context operations, lazy handles, and immediate scalars without
      reconnecting, global mutable state, or context serialization.
    - _Requirements: 12.1-12.8, 12.13-12.15_
  - [ ] 13.2 Map every definitive Go helper capability
    - Account for all 36 pinned helper capabilities exactly once as a generated
      `ModuleQuery` operation, scoped current-context operation, entrypoint-owned close,
      or reviewed target-bound Rust inapplicability.
    - Add exact fixed inventory tests and reject missing, added, duplicate, or unmapped
      helper rows without recreating a global client or Go symbol names unnecessarily.
    - _Requirements: 12.9-12.12_
  - [ ] 13.3 Property test: Property 19 — module context is scoped to the active call
    - Generate at least 256 visible schemas, active sessions, call contexts, lazy/
      immediate operations, cancellation/telemetry values, and serialization attempts;
      require exact typed exposure, one-session reuse, no reconnect/global state, and
      typed context-state rejection.
    - Test identifier: `property_19_module_context_scoped_active_call`.
    - _Requirements: 12.1-12.8, 12.13-12.15_
  - [ ] 13.4 Property test: Property 20 — definitive helper capabilities are exhaustively mapped
    - Generate at least 256 mutations of the fixed 36-helper inventory and mapping
      categories; accept only exact-once exhaustive assignments with reviewed rationale
      for every inapplicability.
    - Test identifier: `property_20_definitive_helper_capabilities_exhaustively_mapped`.
    - _Requirements: 12.9-12.12_

- [ ] 14. Implement parent, argument, handle, and successful-result conversion
  - [ ] 14.1 Implement total call preparation before user execution
    - Select the generated parent/function coordinate, distinguish constructor and
      instance calls, decode compatible parent state, preserve argument input as a list
      until duplicate detection, and validate the complete named set before invocation.
    - Apply omitted optional/default values, retain explicit zero/false/empty values,
      and report distinct malformed parent, missing/duplicate/unknown/invalid argument
      errors with callable and typed-value coordinates.
    - _Requirements: 9.9, 9.10, 10.1-10.11, 10.15_
  - [ ] 14.2 Re-enter Core, self, dependency, and interface handles
    - Decode target-compatible IDs into the exact checked generated handles on the
      active `QueryBuilder`, preserve interface concrete identity, and reject wrong/
      stale/malformed IDs without a new connection or untyped substitute.
    - _Requirements: 10.12-10.14, 12.2, 12.14_
  - [ ] 14.3 Encode successful values through typed result codecs
    - Encode primitives/lists/options/enums/scalars, unit as null, local object state,
      interfaces with concrete identity, and generated handles after resolving their IDs
      through the active session.
    - Keep selection/function/result-type coordinates on encoding/re-entry failure and
      produce no partial `CallOutcome`.
    - _Requirements: 11.1-11.5, 11.7, 11.10_
  - [ ] 14.4 Property test: Property 15 — parent and argument validation precedes execution
    - Generate at least 256 selected callables, parent shapes, argument multisets,
      orderings, omissions/defaults, duplicates, unknowns, and invalid values; compare
      validation to a reference model and require zero user/sink events on rejection.
    - Test identifier: `property_15_parent_argument_validation_precedes_execution`.
    - _Requirements: 10.1-10.11, 10.15_
  - [ ] 14.5 Property test: Property 16 — handle reconstruction retains identity and session
    - Generate at least 256 valid/invalid Core/self/dependency/object/interface IDs,
      concrete identities, schemas, and active sessions; require exact typed re-entry on
      the same session or the matching typed error.
    - Test identifier: `property_16_handle_reconstruction_retains_identity_session`.
    - _Requirements: 10.12-10.14, 12.2, 12.14_
  - [ ] 14.6 Property test: Property 17 — successful values encode exactly once
    - Generate at least 256 supported values, units, local states, handles, interfaces,
      and injected encoding/ID failures; require one canonical value or no value with
      exact safe coordinates.
    - Test identifier: `property_17_successful_values_encode_exactly_once`.
    - _Requirements: 11.1-11.5, 11.7, 11.10_

- [ ] 15. Implement the production dispatcher, result election, failure precedence, and isolation
  - [ ] 15.1 Add application-error, panic, publication, and close handling
    - Invoke exactly one typed generated bridge, await async functions without blocking,
      convert `Into<ModuleError>` values to target Error plus sorted `withValue`
      selections, and publish values/errors through one `ResultElection`.
    - Contain user-future unwind without rendering its payload, preserve query/transport
      publication sources, attempt no second terminal kind, and retain primary operation
      failure over secondary close failure while making close primary after success.
    - _Requirements: 11.6-11.14_
  - [ ] 15.2 Implement cancellation versus publication as one closed state machine
    - Race only the call-local user future, encoding, cancellation, and sink acceptance;
      prevent success when cancellation wins, preserve an accepted sink outcome when
      publication wins, and terminate or abandon SDK-owned child work before return.
    - Avoid sleeps and global result/cancellation state; expose deterministic hooks for
      `loom` and direct async tests.
    - _Requirements: 13.7-13.12_
  - [ ] 15.3 Isolate overlapping calls and call-local leases
    - Allocate distinct contexts, receivers, argument maps, telemetry, cancellation,
      fixture roots, result elections, and session leases for every call; keep sibling
      execution usable after one error or contained panic and release all call-local
      resources on completion.
    - _Requirements: 13.1-13.6, 13.10-13.12_
  - [ ] 15.4 Property test: Property 18 — failure and close precedence is deterministic
    - Generate at least 256 application/panic/cancel/encode/publish/close outcomes and
      orderings; compare selected primary, safe terminal kind, source retention, close
      fact, and publication count to the closed precedence model.
    - Test identifier: `property_18_failure_close_precedence_deterministic`.
    - _Requirements: 11.6-11.14_
  - [ ] 15.5 Property test: Property 21 — concurrent calls remain isolated
    - Generate at least 128 finite overlapping call sets with distinct state/context/
      outcome inputs; use deterministic barriers rather than sleeps and require each
      observation to remain attributable to exactly one call with all resources
      released.
    - Test identifier: `property_21_concurrent_calls_remain_isolated`.
    - _Requirements: 13.1-13.6, 13.10-13.12_
  - [ ] 15.6 Property test: Property 22 — cancellation and publication have one winner
    - Use `loom` to exhaust modeled scheduler interleavings and `proptest` for at least
      128 state/input combinations; require exactly one permitted terminal transition,
      immutable sink acceptance, and no successful value after cancellation wins.
    - Test identifier: `property_22_cancellation_publication_one_winner`.
    - _Requirements: 13.7-13.10_

- [ ] 16. Checkpoint: call-scoped context, conversion, dispatch, and concurrency are green
  - Run formatting and locked `dagger-sdk` plus generated-fixture tests for Properties
    15-22, ModuleError/QueryError conversion, context/helper inventory, call validation,
    result codecs, panic containment, close precedence, and loom concurrency models.
  - Compile the checked representative module assets once; do not regenerate. Record
    commands and elapsed time, and require no engine, Dagger module, network graph,
    unrelated SDK build, leaked task/session, duplicate outcome, or credential-bearing
    diagnostic.

- [ ] 17. Replace the fixed probe with the general registration/invocation adapter
  - [ ] 17.1 Generalize Feature 5 operation inputs behind the stable ABI
    - Replace fixed-probe `EntrypointInput`/protocol content with target-bound source
      snapshot, descriptor, registration, generated-assets, and generic-entrypoint
      inputs while preserving Generate_Module/Generate_Entrypoint operation selectors,
      runtime target, project adoption, and Go transport ABI.
    - Keep schema/source semantics in Rust; make the Go adapter marshal closed inputs
      and apply returned changesets only.
    - _Requirements: 8.4, 9.1-9.3, 9.11, 15.1-15.2_
  - [ ] 17.2 Wire every real call through one engine-independent envelope
    - Have the generated binary connect once to the existing nested session, read name,
      parent name/state, and the complete argument list, derive registration only from
      an empty name, and create one `CallEnvelope` for both branches.
    - Route registration through the descriptor `RegistrationSink`, invocation through
      the production dispatcher/`ResultSink`, then close once with the approved failure
      precedence; re-enter all IDs through the active query builder.
    - _Requirements: 9.1-9.3, 9.9-9.11, 10.1-10.14, 11.10-11.13_
  - [ ] 17.3 Add direct Rust and static Go adapter fixtures
    - Exercise closed operation decoding, complete input forwarding, registration and
      invocation branch selection, query/transport sources, and Go ABI shape with
      recording Rust adapters and direct Go tests only.
    - Assert that Go contains no Rust authoring grammar, descriptor, codec, dispatch,
      default, state, metadata, or evidence policy and that tests construct no engine.
    - _Requirements: 9.1-9.12, 14.4, 16.6, 17.8_
  - [ ] 17.4 Property test: Property 14 — registration and invocation branches are disjoint
    - Generate at least 256 call names, parent/function coordinates, constructor/
      instance shapes, registration plans, and adapter failures; require the empty name
      to serve only complete registration, nonempty names to invoke only dispatch, and
      production/harness use of the same registry.
    - Test identifier: `property_14_registration_invocation_branches_disjoint`.
    - _Requirements: 9.1-9.3, 9.9-9.11_

- [ ] 18. Fence the public authoring contract with bounded compile fixtures
  - [ ] 18.1 Add the reusable compile-fixture project and dependency cache
    - Build checked fixture crates from the same generated asset inputs and exact
      dependency alias policy as production; isolate Cargo target/cache state from
      package source while reusing it across cases.
    - Add a bounded batched compiler driver so generated PBT cases exercise parsing and
      rendering in memory while representative rustc/trybuild cases compile by semantic
      category rather than spawning one Cargo build per random case.
    - _Requirements: 16.9-16.11_
  - [ ] 18.2 Add representative compile-pass coverage
    - Cover public/crate-private exports, private fields/methods/errors, multiple impls,
      nested modules/imports/aliases, renamed SDK dependency, constructors/state,
      interfaces/enums/scalars, every type-policy row, sync/async/result/unit, typed
      defaults/metadata, context/query/error conversion, and fingerprint convergence.
    - _Requirements: 2.1-2.12, 4.1-4.15, 5.1-5.12, 6.1-6.16, 7.1-7.17, 16.9_
  - [ ] 18.3 Add source-coordinate compile-fail coverage
    - Pin stable diagnostic codes and authored coordinates for roots, visibility,
      metadata, cfg/import/alias/name/type/state/interface/enum/scalar/default/function/
      context/fingerprint failures while normalizing only toolchain decoration and
      temporary absolute roots.
    - _Requirements: 2.7-2.9, 3.7-3.12, 4.9-4.15, 5.5, 5.8, 5.10-5.12, 6.13-6.16, 7.11, 7.14-7.16, 14.2-14.6, 16.10_
  - [ ] 18.4 Property test: Property 27 — compile fixtures fence the public authoring contract
    - Generate at least 128 fixture models across every supported/rejected authoring
      category, require deterministic pass/fail classification and source-coordinate
      rendering, and cross-check each category against at least one real batched
      compile fixture.
    - Test identifier: `property_27_compile_fixtures_fence_public_authoring_contract`.
    - Keep random generation in memory and compiler invocations bounded by category so
      this property cannot recreate the previous multi-hour build loop.
    - _Requirements: 16.9-16.11_

- [ ] 19. Build the complete engine-free production module harness
  - [ ] 19.1 Add recording registration, result, transport, and session fixtures
    - Drive the real `Client`, `QueryBuilder`, generated handles, context, descriptor,
      registry, codecs, cancellation, dispatcher, and sinks through Rust fixture values
      with deterministic ID/query responses and failure injection.
    - Record process/network/engine events and fail the harness if a Dagger executable,
      module invocation, network-backed engine graph, Go behavioral model, or unrelated
      SDK build is requested.
    - _Requirements: 16.1-16.6, 16.12-16.15_
  - [ ] 19.2 Exercise the complete positive and negative direct matrix
    - Cover registration, constructors, state, Core/self/dependency handles, interfaces,
      enums, scalars, optional/default/zero values, sync/async/unit/value/error/panic,
      context/current-node, malformed source/metadata/name/state/argument/ID/result,
      unknown dispatch, duplicate outcomes, cancellation, concurrency, publication,
      and close failures against production layers.
    - Reuse checked generated assets and one compiled fixture set; do not regenerate
      between cases.
    - _Requirements: 16.1-16.11_
  - [ ] 19.3 Property test: Property 26 — the direct harness exercises production semantics
    - Generate at least 128 fixture descriptors and call sequences spanning every
      required execution/result/failure class; assert that observations traverse the
      production compiler, projections, registry, codecs, context, dispatcher, and sink
      rather than a substitute implementation.
    - Test identifier: `property_26_direct_harness_exercises_production_semantics`.
    - _Requirements: 16.1-16.8_

- [ ] 20. Checkpoint: general adapter, compile contract, and direct harness are green
  - Run formatting; locked focused tests for `dagger-sdk-macros`, `dagger-codegen`,
    `dagger-sdk`, `dagger-sdk-engine`, and `dagger-sdk-completeness`; Properties 14 and
    26-27; the bounded compile-pass/fail corpus; the full direct production matrix; and
    direct static/compile tests under `sdk/rust/runtime`.
  - Reuse the one compiled fixture/checked asset set across cases. Record commands,
    elapsed time, package selection, and no-generation decision; require no engine,
    Dagger module, network graph, other SDK build, continuous regeneration, or
    unaccounted generated diff.

- [ ] 21. Make engine-free checkpoints and implementation closure executable evidence
  - [ ] 21.1 Add a closed Rust-only checkpoint planner and recorder
    - Encode package, test-target, property, fixture, formatting, clippy/rustdoc,
      security, generated-drift, and clean-output commands as closed typed actions with
      elapsed time and generated-asset decisions.
    - Reject Dagger/engine/module/network-graph commands, unscoped workspace generators,
      other language SDK builds, undeclared package expansion, and engine exceptions
      lacking the exact unmodellable contract plus explicit approval.
    - _Requirements: 16.12-16.19_
  - [ ] 21.2 Add implementation-closure evidence assembly
    - Admit only passed exact-target compiler/dispatcher properties, compile fixtures,
      changed-workspace format/check/test/clippy/rustdoc, cargo-deny, repository Rust
      security, asset drift/ownership, package, and clean-output observations.
    - Record skipped/stale/failed gates and any engine-backed local observation as
      non-closure; preserve all engine-dependent capability blockers.
    - _Requirements: 17.1-17.8_
  - [ ] 21.3 Property test: Property 28 — local checkpoints are observably engine-free and scoped
    - Generate at least 256 checkpoint plans, command/package expansions, asset states,
      elapsed records, and proposed exception records; admit only scoped checked-asset
      Rust plans with zero engine/network/other-SDK events and explicitly approved
      necessity for any deferred sign-off exception.
    - Test identifier: `property_28_local_checkpoints_observably_engine_free_scoped`.
    - _Requirements: 16.12-16.19_
  - [ ] 21.4 Property test: Property 29 — implementation closure admits only complete local evidence
    - Generate at least 256 complete/incomplete, passed/skipped/stale/failed,
      engine-free/engine-backed closure observations and capability claims; admit only
      the complete local gate set without changing engine-dependent status.
    - Test identifier: `property_29_implementation_closure_only_complete_local_evidence`.
    - _Requirements: 17.1-17.8_

- [ ] 22. Implement the deferred exact-target SDK-sign-off suite and claim boundary
  - [ ] 22.1 Define the reusable one-engine sign-off case inventory
    - Add code for registration, constructor/state, execution shapes, types,
      handles/context, negative dispatch, concurrency/cancellation, a packaged
      self-consumer, and applicable pinned common-harness cases against engine revision
      `25300124ca110612edc09c43f89cb5fad6028170`.
    - Make the packaged self-consumer a Rust-authored Dagger module that resolves only
      the exact engine-packaged Rust SDK, uses the generated Core surface to run a
      bounded Rust SDK build-and-test workflow, and fails on every repository-relative
      or unpackaged SDK dependency. Keep full consumer/platform conformance in Feature
      8 and release self-hosting in Feature 9.
    - Build one exact engine content object for later fan-out, bind every case to target,
      implementation, generated-assets, runtime, and case digests, and keep the suite
      outside all local checkpoint selectors.
    - _Requirements: 17.9-17.12_
  - [ ] 22.2 Add strict sign-off observation and evidence admission
    - Require every selected case to pass, enumerate only its proved capabilities, and
      reject stale, cross-target, skipped, failed, local-only, sibling, or overbroad
      smoke claims without partial admission.
    - Keep common lifecycle checks in their declared domain and distinguish final
      Implementation_Closure from unexecuted/passed SDK_Signoff in derived reports.
    - _Requirements: 17.13-17.18_
  - [ ] 22.3 Property test: Property 30 — SDK sign-off is exact-target and claim-bounded
    - Generate at least 256 sign-off manifests, target/digest mutations, case outcomes,
      capability subsets, harness scopes, and smoke overclaims; admit only the complete
      exact-target observation and preserve the closure/sign-off distinction.
    - Test identifier: `property_30_sdk_signoff_exact_target_claim_bounded`.
    - This property tests pure manifest/admission logic only; it does not construct or
      execute the engine whose later observation it validates.
    - _Requirements: 17.9-17.18_

- [ ] 23. Complete diagnostics, documentation, security, and derived reporting
  - [ ] 23.1 Finish total diagnostics across every producing layer
    - Route every compiler, macro, cfg/path, type/state, projection, generation,
      dispatch, codec, application, panic, cancellation, publication, package,
      checkpoint, and evidence failure to exactly one typed code and safe source/wire
      coordinate.
    - Sort independent diagnostics, preserve safe underlying causes, map generated
      locations to authored source, bound values, and exclude credentials, host paths,
      environment secrets, transport content, arbitrary panic payloads, and opaque
      debug text.
    - _Requirements: 14.1-14.12_
  - [ ] 23.2 Document the durable authoring and engine-free workflow contracts
    - Add `//!` boundary/invariant docs and caller-relevant `///` guarantees for every
      new module and public item; explain explicit export/accessibility, typed defaults,
      thin macro versus descriptor ownership, state identity, active-session context,
      result/cancellation election, generated ownership, and error precedence.
    - Update Rust architecture, contribution, module-authoring README/examples,
      generated ownership/regeneration instructions, two-package policy, local focused
      commands, feature-end closure, and separate SDK-signoff reproduction.
    - Keep obvious narration and specification feature/task labels out of production,
      generated, and invariant comments.
    - _Requirements: 2.10, 14.10-14.12, 15.1, 16.17-16.19, 17.3-17.8_
  - [ ] 23.3 Derive completeness and security outputs without overclaiming
    - Emit compiler, fixture, dispatcher, checkpoint, package, dependency, cargo-deny,
      source-policy, and implementation-closure observations through Feature 1
      admission; regenerate only declared derived reports and retain every unproved
      engine-dependent blocker.
    - Update the repository Rust security workflow for both public packages, complete
      package contents, locked dependency roots, generated source, redaction, and
      unsafe/panic/unwrap policy without adding an engine job.
    - _Requirements: 1.7-1.10, 14.6, 14.11-14.12, 17.1-17.8, 17.13-17.18_
  - [ ] 23.4 Property test: Property 23 — diagnostics are typed, stable, ordered, and redacted
    - Generate at least 256 failures from every taxonomy domain, coordinate/order
      permutations, generated/authored maps, safe/unsafe sources, secret-shaped values,
      and panic payloads; require the exact code, stable ordering, safe source retention,
      authored mapping, and complete redaction.
    - Test identifier: `property_23_diagnostics_typed_stable_ordered_redacted`.
    - _Requirements: 14.1-14.6, 14.10-14.12_

- [ ] 24. Final checkpoint: Feature 6 implementation is engine-free complete
  - Run `cargo fmt --all --check`; locked workspace check/test; warning-denied workspace
    clippy and rustdoc; `cargo deny check`; both public package-policy/package-content
    checks; repository Rust security test commands; direct `sdk/rust/runtime` Go tests;
    all 32 property identifiers; the bounded compile corpus; the complete direct module
    harness; generated-asset drift/ownership checks; derived-report verification; and
    clean-output inspection.
  - Run only the Rust workspace under `sdk/rust` plus the Rust SDK's direct Go ABI
    package. Reuse checked Core and module assets unless an owning digest changed; do
    not invoke Dagger, build an engine, execute a module, run sdk-sdk, build another
    language SDK, or perform unscoped regeneration.
  - Require all 239 acceptance criteria, the exact 79/32 capability scope, both public
    packages, every descriptor/projection/type/metadata row, registration and invocation
    models, context/helper mappings, conversion/error/concurrency/cancellation
    boundaries, compile fixtures, closure evidence, security policy, and byte-clean
    derived output to pass. Record commands, elapsed time, and generation decisions.
  - Any capability requiring engine registration, runtime-container, common-harness, or
    platform evidence remains honestly blocked until the separate SDK-signoff gate
    actually executes and passes.

## Deferred SDK Sign-off Gate

The code and pure admission policy for Requirements 17.9-17.18 are implemented by Task
22, but execution is deliberately outside Feature 6 Implementation_Closure. SDK
sign-off builds the exact Target Revision once, fans the one reusable engine content
object across the complete case inventory, runs the bounded packaged self-consumer,
executes the applicable pinned common harness, admits only passed exact-target
observations, regenerates derived reports, and verifies the clean result. No
engine-dependent row may move before that gate passes. Feature 8 owns expanding the
self-consumer into exhaustive engine/platform conformance; Feature 9 owns published
release self-hosting.

## Task Dependency Graph

```json
{
  "1": [],
  "2": ["1"],
  "3": ["1", "2"],
  "4": ["3"],
  "5": ["4"],
  "6": ["5"],
  "7": ["6"],
  "8": ["7"],
  "9": ["8"],
  "10": ["8", "9"],
  "11": ["10"],
  "12": ["11"],
  "13": ["12"],
  "14": ["12", "13"],
  "15": ["14"],
  "16": ["15"],
  "17": ["16"],
  "18": ["17"],
  "19": ["17", "18"],
  "20": ["19"],
  "21": ["20"],
  "22": ["20", "21"],
  "23": ["21", "22"],
  "24": ["23"],
  "sdk-signoff": ["24"]
}
```

The six checkpoints are strict bounded review boundaries. Public package/model/grammar
foundations precede discovery; discovery precedes the type/function compiler; runtime
primitives precede descriptor-generated consumers; checked generated assets precede
dispatch; dispatch precedes the engine ABI adapter and compile/direct harnesses; those
harnesses precede closure/sign-off evidence and final hygiene.

## Notes

- Every property-test subtask is mandatory. Pure/reference-model properties run at
  least 256 cases; filesystem, compile-model, async, and concurrency properties run at
  least 128; `loom` exhausts modeled schedules. Fixed target inventories and compile
  categories are exhaustive in addition to generated cases.
- Stable `property_NN_*` test identifiers plus task/requirement citations provide the
  property traceability. Per the user's approved documentation policy and
  `sdk/rust/AGENTS.md`, source comments explain invariants without `Feature`, task, or
  planning labels; the specification itself retains the mapping.
- Checkpoints run the narrow owning package and fixture slices. They do not repeatedly
  run workspace clippy, rustdoc, security, packaging, or complete compile matrices;
  those run after dependency/public-boundary changes and once at Task 24. A failing
  checkpoint is analyzed against fixture/contract behavior before another broad build.
- Generated Core and module assets are change-triggered. Documentation, tests, fixture
  internals, runtime logic, and implementation-only Go ABI edits do not authorize
  regeneration. When an owning source/schema/target/generator digest changes, perform
  one scoped refresh, inspect it, then return to checked assets.
- No local checkpoint or Implementation_Closure constructs or executes a Dagger engine.
  If a contract appears impossible to model directly, stop and write the exact gap,
  evidence of model insufficiency, and minimal proposed sign-off case for explicit
  approval; uncertainty or convenience is not sufficient.
- The Go runtime layer remains an ABI shim. Rust owns source discovery, authoring
  grammar, descriptor, TypeDef/introspection projection, codecs, dispatch, context,
  diagnostics, generated ownership, security policy, and evidence admission.
- Checkpoints are preferred commit/review boundaries. Keep commits coherent at the
  independently compiling layer; do not stack unverified implementation merely to
  postpone review, and do not rerun a broader graph merely to create a checkpoint.
- Implementation_Closure and SDK_Signoff are separate evidence states. Green local
  compiler/dispatcher/harness results cannot close engine registration or runtime
  claims; a later engine smoke cannot replace exhaustive local authoring/dispatch
  evidence.
