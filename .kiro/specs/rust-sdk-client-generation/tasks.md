# Implementation Plan

- [x] 1. Establish the canonical standalone-client models and diagnostic foundation
  - [x] 1.1 Add strict client operation, module, project, and ownership wire models
    - Add `ClientModuleIdentity`, optional resolved pin, `ClientProjectIdentity`,
      `ClientSchemaSurface`, module-root/namespace records, semantic amendment records,
      and the optional client section of `OperationManifest` in their owning crates.
    - Extend the private request protocol with `InitializeClient` and the additional
      `GenerateClient` identities while retaining strict versioning, unknown-field
      rejection, canonical JSON, domain-separated digests, and backwards-compatible
      decoding of existing non-client and Feature 5 baseline manifests.
    - Keep raw module refs, credentials, absolute paths, sessions, filesystem handles,
      and engine objects out of Rust durable models.
    - _Requirements: 2.1-2.3, 3.13, 5.5-5.9, 6.1-6.3, 8.7-8.13_
  - [x] 1.2 Add the client compiler, engine, fixture, checkpoint, and evidence codes
    - Add only the new stable codes approved by the design for initialization, pin,
      project, root-overlap, module-root, schema-scope, fixture, closure, and sign-off
      failures; reuse existing target, schema, wrapper, naming, Cargo, dependency,
      toolchain, path, ownership, publication, cancellation, checkpoint, and
      completeness codes everywhere else.
    - Preserve exact schema coordinates, normalized relative paths, semantic manifest
      keys, deterministic multi-diagnostic ordering, bounded safe messages, and typed
      underlying causes without forwarding Cargo, Git, GraphQL, or module-ref text.
    - Extend source-policy tests so new production code cannot introduce unsafe Rust,
      panic/unwrap fallbacks, process-global client state, or credential-bearing debug
      output.
    - _Requirements: 4.14-4.17, 6.9-6.14, 8.1-8.13_
  - [x] 1.3 Add valid-first client property strategies and reference models
    - Add shared strategies for exact-target identities, Core-plus-module schema
      graphs, wrapper trees, module/local names, Cargo documents, authored trees,
      manifests/amendments, workspace records/pins, checkpoint actions, and
      closure/sign-off observations.
    - Use at least 256 cases for pure schema, naming, Cargo, ownership, publication,
      diagnostic, and evidence models and at least 128 for filesystem, Cargo-process,
      and async transport models, all above the 100-case requirement.
    - Keep expensive compiler/Cargo invocations outside per-case loops: properties
      compare pure plans first and use a bounded representative compile corpus.
    - _Requirements: 3.4-3.13, 4.4-4.14, 5.1-5.17, 6.1-6.15, 7.1-7.14, 8.1-8.14, 10.1-10.19_

- [x] 2. Register the exact Feature 7 capability scope and ownership correction
  - [x] 2.1 Add the client-generation mapping and Rust policy inventory
    - Retain `behavior/go-client/init-client-lifecycle` with its pinned fingerprint,
      add exactly the 24 approved client Rust-policy capabilities, and map every row to
      one requirement, implementation subject, authority, rationale, evidence domain,
      allowed terminal status, target identity, and blocker state.
    - Move the pinned Go `TestProvision` row to Feature 3 without changing its
      fingerprint, status, or evidence; preserve Feature 5 ownership of the operation
      hook and reject hook-only evidence for generated-content claims.
    - Update checked scope digests and generated report fixtures without promoting any
      status merely because a mapping or source file exists.
    - _Requirements: 1.1-1.11_
  - [x] 2.2 Implement dependency-scope and evidence-domain validation
    - Encode the approved rule that one standalone client binds Core plus exactly one
      selected local or pinned remote module and that a dependency needs its own
      independently bound client.
    - Reject missing, duplicated, moved, catch-all, name-only, wrong-target,
      out-of-domain, hook-only, stale, skipped, failed, and incomplete capability
      evidence as complete-set failures; retain every unresolved blocker in rendered
      output.
    - _Requirements: 1.4-1.12_
  - [x] 2.3 Property test: Property 1 — capability scope is exact, attributable, and evidence-gated
    - Implement `property_01_capability_scope_exact_attributable_evidence_gated` as a
      reference-set PBT over at least 256 capability, ownership, fingerprint, mapping,
      authority, terminal-status, target, evidence, and dependency-wording mutations.
    - Admit only the retained initialization row plus the 24 policy rows, preserve the
      corrected Feature 3 row and Feature 5 hook scope, and prove that invalid evidence
      leaves status unchanged while all blockers remain visible.
    - _Requirements: 1.1-1.12_

- [x] 3. Define finite host metadata and client project identity inputs
  - [x] 3.1 Replace the empty client-generation baseline with the reviewed finite set
    - Emit the canonical sorted patterns for client Cargo manifests, README,
      `.gitattributes`, exact toolchain declarations, and library roots needed by the
      legacy host-scoping path; retain no `Cargo.lock`, credential file, generated
      output, target directory, or unrestricted host include.
    - Keep the modern workspace path on the same metadata decoder and the same Rust
      project discovery even though it already receives its workspace snapshot.
    - Regenerate the packaged `client-generation.json` only once after this owning
      input changes and update its asset digest/packaging evidence atomically.
    - _Requirements: 5.14-5.17, 8.10-8.14, 10.5-10.8_
  - [x] 3.2 Add deterministic package/crate identity and initialization requests
    - Normalize an existing Cargo package identity or derive `<client-basename>-dagger-client`,
      falling back to the normalized bound-module name only when the path has no valid
      basename; reject a result that is not a valid Cargo package and Rust crate name.
    - Add a closed `ClientInitializationRequest` containing only exact target, confined
      client root, deterministic package name, and immutable SDK dependency; keep the
      potentially credential-bearing module reference at the Go adapter boundary.
    - Add strict encode/decode and canonical digest tests for new request/model variants
      while proving old non-client protocol fixtures remain byte-compatible.
    - _Requirements: 2.2-2.8, 5.1-5.9, 5.13, 8.7-8.13_
  - [x] 3.3 Property test: Property 22 — required host-file metadata is finite and canonical
    - Implement `property_22_required_host_file_metadata_finite_canonical` over at
      least 256 valid and hostile pattern sets, ordering/duplicate mutations, path
      escapes, controls, aliases, credential paths, lockfiles, generated paths, and
      unsupported format versions.
    - Require canonical JSON round-trip and exact adapter projection of only the
      reviewed finite set; fixed tests separately pin the checked asset bytes.
    - _Requirements: 8.14_

- [x] 4. Checkpoint: client models, scope, metadata, and protocol foundations are green
  - Run formatting and only the new client model/protocol tests in `dagger-codegen`,
    `dagger-sdk-engine`, and `dagger-sdk-completeness`, Property 1, Property 22, the
    packaging metadata test, focused warning-denied Clippy/rustdoc, and direct metadata
    decoder tests in `sdk/rust/runtime`.
  - Regenerate `client-generation.json` and its packaged digest exactly once because
    Task 3 changes its owning input; inspect the scoped diff and record the new input
    and output digests. Do not regenerate Core or module bindings.
  - Record exact commands, elapsed phase times, selected packages/targets, and the asset
    decision. Run `cargo deny check` only if Tasks 1-3 changed Cargo or security-policy
    inputs.
  - Require no Dagger command, engine process, module invocation, another SDK,
    unscoped repository generation, distribution build, or network resolution.
  - Checkpoint evidence (2026-08-12, warm local cache as executed, elapsed wall time):
    - `cargo fmt --all -- --check` passed in 1.08s. The locked package-scoped check for
      `dagger-codegen`, `dagger-sdk-engine`, and `dagger-sdk-completeness` with all
      features passed in 8.24s.
    - The four focused `dagger-codegen` targets (`client_models`, `diagnostics`,
      `engine_operations`, and `client_metadata_properties`) passed in 11.52s. This
      includes 256-case Property 22 and the exact checked-asset byte regression.
    - The three focused `dagger-sdk-engine` targets (`client_models`,
      `canonical_models`, and `packaging_properties`) passed in 5.30s. They exercised
      the 256-case canonical protocol/project corpus, legacy non-client byte
      compatibility, and the packaged security graph.
    - `cargo test -p dagger-sdk-completeness --test client_generation_scope --test
      initial_baseline --locked` passed in 39.59s. Property 1 ran 256 cases; the
      root-independent baseline proved the ownership-only `TestProvision` correction,
      unchanged status/fingerprint/evidence, and byte-exact derived artifacts.
    - The complete source-policy target passed in 0.48s. Direct Go metadata decoder
      tests passed from cache in 0.55s with only normal machine Go-cache access.
    - Warning-denied, no-dependency Clippy for the three selected packages passed in
      12.37s; pre-existing unused-code warnings from the unselected `dagger-sdk`
      dependency remained visible but were not attributed to this slice. Warning-denied
      rustdoc passed in 12.68s.
    - The finite host-file semantic input digest is
      `sha256:2ce5e5bd829fcc948239284c8abdf213d4eef84f3b0b3b77e361d25226615e76`;
      its one scoped `client-generation.json` output digest is
      `sha256:1a9c795a25e5f7c90333b75e761105bb682d5c8465fd43da17a2e94f4263ade9`.
      No Core or module binding was regenerated.
    - The Rust harness artifact identity advanced once to
      `sha256:3e0635e53de76565e15c2500cf3328cfc67298712921ea0ee985ecae1bad4c42`.
      All 18 existing mappings were reconciled without changing target, check scope,
      outcome, capability evidence, or platform. The locked Integrity gate passed in
      17.37s with ledger digest
      `sha256:0ca7dd487feb996d122d7ad13635601a76ad470ef996dc63a53fe26cf89d4e44`.
    - No Cargo manifest, lockfile, dependency, source policy, or security-policy input
      changed, so `cargo deny check` was not repeated. No Dagger command, engine
      process, module invocation, another SDK, unscoped generation, distribution
      build, or network resolution ran.
  - _Requirements: 1.1-1.12, 5.14, 8.1-8.14, 10.1-10.10_

- [x] 5. Implement the exact client-visible schema scope compiler
  - [x] 5.1 Validate complete Core and identify the optional selected-module root
    - Reuse `VisibleSchemaPlan` and the exact Feature 4 Core manifest to retain every
      target-visible Core coordinate, including `Host` and the `Engine*` family hidden
      only from module-facing schemas.
    - Accept an empty extension set only for the observable Core-only/runtime-less
      surface; otherwise require exactly one non-promoted `Query.<module>` field whose
      Wire_Name matches the engine-normalized selected module and whose unwrapped
      return is its root object.
    - Reject missing/changed Core, promoted module functions, multiple or differently
      named roots, malformed wrappers, and extra Query extensions before rendering.
    - _Requirements: 3.4-3.8, 3.11-3.12_
  - [x] 5.2 Compute and validate the complete selected-module closure
    - Traverse non-Core object/interface fields, arguments, interface implementations,
      interface possible types, enum values, custom scalars, input fields, and
      recursive wrappers from the selected root using canonical graph order. Public
      unions remain rejected by the exact-target canonical schema policy.
    - Require every non-Core coordinate to be both reachable and owned by the exact
      selected-module namespace rule; reject dependency-root or dependency-type
      leakage, unreachable extensions, unsupported references, and cycles that exceed
      the existing wrapper/graph bounds.
    - Produce `ClientSchemaSurface`, `ModuleRoot`, and a canonical extension-coordinate
      set without filesystem, process, network, Cargo, or engine access.
    - _Requirements: 3.7-3.13_
  - [x] 5.3 Property test: Property 5 — client-visible schema is exactly Core plus one reachable module closure
    - Implement `property_05_client_visible_schema_exact_core_one_module_closure` as a
      graph-reference PBT over at least 256 exact-Core mutations, hidden-Core rows,
      runtime-less cases, root/promoted fields, module/dependency graphs, wrappers,
      ownership prefixes, and declaration permutations.
    - Accept exactly Core or Core plus one reachable selected-module closure and require
      deterministic coordinate diagnostics for every invalid mutation.
    - _Requirements: 3.5-3.12_

- [x] 6. Project the complete idiomatic Rust module surface
  - [x] 6.1 Resolve every Core reference to the checked public SDK catalog
    - Map every Core named type, field result/input, scalar, ID, error, lifecycle, and
      transport role to its exact Feature 4 catalog fingerprint and public
      `dagger_sdk` path; reject missing or mismatched fingerprints as target drift.
    - Prevent the client plan and artifact set from containing a local Core type,
      session, transport, error, scalar, or ID implementation.
    - _Requirements: 3.5-3.6, 4.1-4.3, 8.9-8.12_
  - [x] 6.2 Plan the collision-free selected-module namespace
    - Derive the snake-case module namespace, `<Module>Ext` trait, namespaced root
      `Client`, and every object/interface/enum/input/scalar/helper path as one complete
      set before rendering.
    - Remove the exact PascalCase module prefix only when the non-empty result is legal
      and globally unique; handle ordinary Rust keywords with raw identifiers and
      reject collisions with generated roles or other wire coordinates without
      order-dependent suffixes.
    - _Requirements: 4.4, 4.13-4.14_
  - [x] 6.3 Project typed fields, options, inputs, codecs, and handle re-entry
    - Reuse Feature 4 recursive wrapper, directive, documentation, deprecation,
      experimental, field-strategy, omission, and exact Wire_Name semantics while
      resolving leaves to Core or module-local paths.
    - Emit required typed parameters, owned non-exhaustive options/builders, exact enum
      and input codecs, custom scalar policy, object/interface handles, `IntoID`,
      target-typed `IdInput`, scalar execution, lazy handles, and complete nullable/list
      ID re-entry shapes.
    - Reject unsupported/lossy wrappers, missing object IDs, incomplete interface
      relations, untyped JSON fallback for supported types, and any public module
      coordinate without one semantic binding.
    - _Requirements: 4.5-4.12, 4.16-4.17, 9.7-9.10_
  - [x] 6.4 Property test: Property 6 — Core is reused by identity rather than regenerated
    - Implement `property_06_core_reused_by_identity_not_regenerated` over at least 256
      valid and mutated Core catalog/extension plans; require a unique matching SDK
      fingerprint/path for every Core reference and prove no local Core/runtime/session
      artifact or unsafe/secret/host-path source can enter the result.
    - _Requirements: 4.1-4.3, 8.9-8.12_
  - [x] 6.5 Property test: Property 8 — generated module surface is an exhaustive typed closure
    - Implement `property_08_generated_module_surface_exhaustive_typed_closure` over at
      least 256 object, interface, implementation, enum, scalar, input, metadata, and
      public-coordinate graph variants; compare emitted/catalog bindings with the
      reachable reference set and reject missing, duplicate, or untyped substitutions.
    - _Requirements: 4.5-4.8, 4.17_
  - [x] 6.6 Add the pure wrapper, omission, and re-entry reference model
    - Generate nested wrapper trees, argument states, explicit false/zero/empty/null
      values, IDs, response shapes, and schema-order permutations, and expose a small
      independent model shared by projection and the later recording-transport PBT.
    - Add focused projection examples now; defer Property 9 until Task 10 can compare
      the emitted runtime request, request-admission boundary, and same-session handle.
    - _Requirements: 4.9-4.12, 9.7-9.10_
  - [x] 6.7 Property test: Property 10 — module-local public naming is deterministic and collision-free
    - Implement `property_10_module_public_naming_deterministic_collision_free` over at
      least 256 module names, keywords, prefix-removal candidates, generated/helper
      roles, wire-name collisions, and declaration permutations.
    - Require the same unique namespace/path map or the same sorted complete collision
      diagnostics independently of input order, with no symbol in the Core namespace.
    - _Requirements: 4.13-4.14_

- [x] 7. Render the standalone generated subtree and exhaustive semantic catalog
  - [x] 7.1 Replace the hook baseline renderer with the production client renderer
    - Move client-specific pure logic under `dagger-codegen/src/client`, retain the
      existing `OperationRenderer` seam as a thin adapter, and change the content
      domain from `EngineHookBaseline` to `StandaloneClient`.
    - Emit the library-adjacent `dagger_client` module, private generated index, one
      selected-module namespace, deterministic per-type files, complete binding
      catalog, and compile-checked quickstart source; leave Cargo, authored library
      roots, README, VCS amendments, and filesystem publication to the engine layer.
    - Parse every rendered Rust file with `syn`, attach semantic rustdoc to every public
      generated item, and request pinned formatting only for the exact owned Rust set.
    - _Requirements: 4.4-4.17, 5.1-5.4, 6.1-6.5, 9.4-9.6, 9.11-9.12_
  - [x] 7.2 Emit lifecycle/Core composition and the local extension-trait plan
    - Re-export exact `dagger-sdk` connection/client configuration at
      `dagger_client`, expose it as `core`, re-export one module namespace, and render
      a local `<Module>Ext` trait for both `dagger_sdk::Client` and `QueryBuilder` plus
      a prelude `as _` import.
    - Ensure root selection uses the exact Query Wire_Name, creates no second session,
      performs no I/O, and carries enough semantic catalog identity for Task 10 to
      prove the runtime bridge.
    - _Requirements: 4.1-4.4, 4.13, 4.15-4.17_
  - [x] 7.3 Add fixed renderer shape, documentation, and drift tests
    - Pin representative Core-only, local-module, and dependency-bound artifact trees,
      public paths, catalog counts/digests, quickstart imports, generated header
      provenance, and absence of transitive dependency namespaces.
    - Prove schema declaration permutations render byte-identically and that no output
      contains module refs, credentials, absolute paths, ambient SDK paths, unsafe
      blocks, placeholder prose, or the obsolete hook-baseline claim.
    - _Requirements: 3.9-3.13, 4.13-4.17, 6.1-6.5, 8.7-8.13_

- [x] 8. Checkpoint: pure client schema, projection, naming, and rendering are green
  - Run formatting, locked `dagger-codegen` client schema/API/renderer tests,
    Properties 5, 6, 8, and 10, fixed source-policy and drift cases, and focused
    warning-denied Clippy/rustdoc for `dagger-codegen` only.
  - Reuse checked Core bindings and the packaged client metadata unless their owning
    target/schema/metadata digests changed in this checkpoint. If one changed, perform
    one scoped refresh, inspect it, and record the input/output identity; never invoke
    repository-wide generation.
  - Record exact commands, elapsed times, package/target selection, generated-asset
    decision, and the engine-free boundary.
  - Require no Dagger command, engine process, module invocation, Cargo project
    compilation, another SDK, distribution build, or network resolution.
  - Checkpoint evidence (2026-08-12, warm local cache as executed, elapsed wall time):
    - `cargo fmt --all -- --check && cargo check -p dagger-codegen --all-features
      --locked` passed in 4.58s. Only `dagger-codegen` and its existing locked
      dependency graph were checked.
    - The eight focused test targets (`client_models`, `diagnostics`,
      `projection_properties`, `client_compiler_properties`, `client_renderer`,
      `client_source_policy`, `engine_operations`, and
      `operation_dispatch_properties`) passed in 32.75s: 33 tests total. Properties
      5, 6, 8, and 10 each ran 256 pure cases; bounded fixed cases covered exact Core
      mutations, schema/name permutations, interface relations, custom scalars,
      omission versus explicit null/false, artifact drift, and shared-renderer
      totality.
    - Warning-denied `cargo clippy -p dagger-codegen --all-targets --all-features
      --locked -- -D warnings` passed in 6.34s. Warning-denied no-dependency rustdoc
      passed in 3.36s.
    - The checked target, Core schema, and packaged client-metadata inputs did not
      change, so their existing bindings and assets were reused without regeneration.
      No Cargo manifest, lockfile, dependency, or security-policy input changed, so
      `cargo deny check` was not repeated.
    - No Dagger command, engine process, module invocation, generated-client Cargo
      project compilation, another SDK, repository-wide generation, distribution
      build, or network resolution ran.
  - _Requirements: 3.4-3.13, 4.1-4.17, 6.1-6.5, 8.7-8.13, 10.1-10.10_

- [x] 9. Add the exact-version external generated-code bridge to `dagger-sdk`
  - [x] 9.1 Expose only the hidden serde and query operations generated clients need
    - Re-export the exact SDK serde implementation under `dagger_sdk::__private` and
      add the version-locked bridge for constructing a sealed Core handle from the
      current selection, creating a root `node(id:)` re-entry builder on the same
      session, and adding lazily resolved ID shapes.
    - Keep `SessionHandle`, `Selection`, Core's sealed `Loadable` constructor, transport
      admission, and lifecycle mutation private; generated code may receive only
      another `QueryBuilder` or a checked Core handle.
    - Document why each hidden operation preserves selection/session identity and why
      it is not a stable author-facing API.
    - _Requirements: 4.1-4.3, 4.9-4.12, 4.15-4.17, 8.12_
  - [x] 9.2 Generalize target-typed lazy ID inputs without exposing their resolver
    - Add the hidden constructor used by generated local handles and a sealed recursive
      `GeneratedIdInputShape` for `IdInput<T>`, `Option<S>`, and `Vec<S>` so every
      supported wrapper resolves before the containing request is admitted.
    - Preserve deterministic sequential list resolution, indexed error sources,
      target-type separation, no partial request on failure, and credential-safe
      `Debug`; add no blanket conversion between unrelated generated handle types.
    - _Requirements: 4.9-4.12, 8.7-8.9, 9.7-9.10_
  - [x] 9.3 Add source-policy and fixed bridge invariants
    - Prove external code cannot name or manufacture a session/selection, implement
      Core's sealed loadable contract, splice a handle onto another session, or access
      raw lazy resolver state.
    - Pin fixed Core-handle, local-handle, nullable/list re-entry, lazy-ID failure,
      clone/drop, close-precedence, and no-extra-connect examples using the existing
      injected connector and recording transport.
    - _Requirements: 4.2-4.3, 4.12, 4.15-4.16, 8.7-8.12_

- [x] 10. Complete and prove the generated Rust client API on the public runtime
  - [x] 10.1 Wire rendered handles and extension traits to the runtime bridge
    - Generate immutable module object/interface handles around `QueryBuilder`, exact
      typed method return paths for Core and local values, local `IntoID` implementations,
      enum/input/scalar codecs, and complete ID-shape reconstruction.
    - Make `Client::<module>()` and `QueryBuilder::<module>()` extension methods retain
      one shared session lease, select exact Wire_Names, perform no construction I/O,
      and use ordinary async `QueryError`/lifecycle behavior.
    - Ensure `dagger_client::core`, explicit lifecycle exports, module namespace, and
      `prelude::*` compile together without shadowing or a global helper.
    - _Requirements: 4.1-4.17, 9.7-9.12_
  - [x] 10.2 Property test: Property 7 — module-root composition preserves one shared Rust client
    - Implement `property_07_module_root_composition_preserves_shared_client` over at
      least 128 generated selection/clone/drop/close schedules, Core/module branches,
      and extension-import states using the real generated plan and recording
      transport.
    - Require the exact root Wire_Name, one session identity, zero construction I/O,
      public async/lifecycle behavior, no mutable global state, and semantic rustdoc for
      every non-obvious public generated item.
    - _Requirements: 4.4, 4.15-4.17_
  - [x] 10.3 Property test: Property 9 — wrappers, omission, Wire_Names, and ID re-entry are faithful
    - Implement `property_09_wrappers_omission_wire_names_id_reentry_faithful` over at
      least 256 wrapper/argument/schema-order cases from Task 6's reference model plus
      at least 128 runtime response and lazy-ID cases.
    - Compare public signatures and recorded requests with the reference model; require
      exact omission versus explicit values, complete response/ID shapes, same-session
      re-entry, and zero containing request or partial collection after ID failure.
    - _Requirements: 4.9-4.12, 9.7-9.10_
  - [x] 10.4 Add fixed Core/module coexistence and public API tests
    - Cover a module returning/accepting Core objects, a Core query beside a module
      query, interface values, custom scalars, nested optional/list inputs and outputs,
      reused options, explicit `false` against a `true` engine default, and lifecycle
      close/error precedence.
    - Assert public paths and rustdoc rather than private generated layout so tests
      protect the supported Rust API without freezing implementation-only modules.
    - _Requirements: 4.1-4.17, 9.7-9.12_

- [x] 11. Establish compile-time API and generated-source compatibility fixtures
  - [x] 11.1 Add bounded `trybuild` pass and compile-fail cases
    - Pass fixtures cover the extension prelude, explicit trait import, Core and module
      use, objects/interfaces/enums/inputs/scalars, required/optional/list IDs, async
      results, an adopted crate name, and a custom library root.
    - Compile-fail fixtures cover wrong target IDs, missing trait import, private handle
      construction, unsupported transitive dependency namespaces, invalid options
      construction, and attempts to implement or use the private runtime bridge.
    - Keep representative compiler processes bounded; do not spawn rustc once per
      property-generated case.
    - _Requirements: 3.9-3.12, 4.4-4.17, 8.12, 9.1-9.6, 9.11-9.12_
  - [x] 11.2 Add generated public-surface and documentation policy tests
    - Parse generated source to require module-level ownership/purpose documentation,
      semantic rustdoc for every public item, exact wire/omission notes where needed,
      and absence of narrated control-flow comments, placeholder text, spec/task labels,
      unsafe Rust, hidden runtime internals, and globally initialized clients.
    - Require generated source to depend only on its declared public SDK/runtime graph
      and never reach `dagger-codegen`, `dagger-sdk-engine`, completeness, bootstrap, or
      another SDK.
    - _Requirements: 4.15-4.17, 8.9-8.13, 9.5-9.6_

- [x] 12. Checkpoint: exact-version bridge and generated public API are green
  - Run formatting, locked focused tests for `dagger-sdk` generated bridge/query
    behavior and `dagger-codegen` client rendering, Properties 7 and 9, the bounded
    `trybuild` corpus, public-source policy, and warning-denied Clippy/rustdoc only for
    `dagger-sdk` and `dagger-codegen`.
  - Reuse checked Core and module assets. Changes to the hidden runtime bridge or client
    renderer do not by themselves authorize Core regeneration; record that reuse
    decision and its owning digests.
  - Record exact commands, elapsed phase times, package/target selection, and fixture
    compiler count so an unexpectedly broad Cargo graph is visible before the next
    checkpoint.
  - Require no Dagger command, engine process, module invocation, project publication,
    another SDK, distribution build, unscoped generation, or network resolution.
  - Checkpoint evidence (2026-08-12, warm local cache as executed, elapsed wall time):
    - `cargo fmt --all -- --check` passed in 0.92s. Locked `dagger-sdk` query unit
      tests passed 9/9 in 11.60s, and the production-generated recording-transport
      target passed 4/4 in 3.53s. Properties 7 and 9 exhaustively exercised 128
      lifecycle/session schedules, 256 omission/value/order schedules, and 128
      response/lazy-ID schedules.
    - The bounded `generated_client_compile` target passed in 7.64s with exactly ten
      compiler fixtures: two pass cases and eight compile-fail cases. It covered the
      prelude and explicit trait paths, the adopting crate's custom test root,
      Core/module/interface/enum/input/scalar/async use, wrong IDs, missing imports,
      restricted constructors/options/generated namespaces, private runtime state,
      inaccessible lazy resolution, and sealed Core loading.
    - `client_renderer` passed 6/6 in 3.03s, `client_compiler_properties` passed 8/8
      in 12.67s, and `dagger-sdk` source policy passed 4/4 in 0.41s. The checked
      generated fixture is produced by the production renderer, formatted once, and
      guarded by provenance, token-order, path-set, source-policy, and compiler drift
      checks; property schedules do not invoke a compiler or regenerate it.
    - Warning-denied `cargo clippy --locked -p dagger-sdk -p dagger-codegen
      --all-targets --all-features -- -D warnings` passed in 7.77s. Warning-denied
      no-dependency rustdoc for the same two selected packages passed in 7.37s. The
      successful checkpoint phases totalled 54.94s.
    - Core generation was not run. Existing Core assets retained target revision
      `25300124ca110612edc09c43f89cb5fad6028170`, schema digest
      `sha256:7d6f61426d0c65454a32059732deed8927471c92e906f4ac7b31dd8ff8214306`,
      retained-scope digest
      `sha256:2b46180b54356faf2071a91198afd1a0e40a757b57a1686f579d2f9ab6ed583f`,
      and projection fingerprint
      `sha256:55ac56ce5186829195465c3f20adf04255c39f640c85a47a1137277084afe3c7`.
      One scoped pure refresh updated only the new generated-client test fixture at
      schema digest
      `sha256:9ee1f1eeccf3db6eacb6690f6c097fe6ff27d366c4598025d7df1ddc99134787`.
    - Cargo test, Clippy, and rustdoc phases set `CARGO_NET_OFFLINE=true` and used the
      checked lockfile. No Cargo manifest, lockfile, dependency, or security-policy
      input changed, so `cargo deny check` was not repeated. No Dagger command, engine
      process, module invocation, project publication, another SDK, distribution
      build, repository-wide generation, or network resolution ran.
  - _Requirements: 4.1-4.17, 8.7-8.13, 9.5-9.12, 10.1-10.10_

- [ ] 13. Implement conservative Cargo, library-root, documentation, and toolchain adoption
  - [ ] 13.1 Discover one bounded client project snapshot
    - Read only the selected root's Cargo manifest, selected/default library root,
      README, `.gitattributes`, caller-owned lockfile digest, and deterministic nearest
      toolchain declarations without following symlinks or entering another package.
    - Support a confined custom `[lib].path` and binary-only package by selecting or
      creating one library root; reject virtual-only manifests, multiple candidates,
      invalid UTF-8/TOML/Rust, escaping paths, and ambiguous project ownership.
    - Produce byte-only `ClientProjectSnapshot` and deterministic package/crate identity
      before invoking the pure renderer.
    - _Requirements: 2.4-2.8, 5.1, 5.10-5.13, 6.8-6.12_
  - [ ] 13.2 Create or format-preservingly adopt the Cargo package
    - For a new package, emit deterministic version `0.1.0`, `publish = false`, edition
      2024, `rust-version = "1.97.1"`, exact `dagger-sdk`, documented compatible Tokio
      runtime, and no local/mutable dependency or lockfile mutation.
    - For an existing package, use `toml_edit` to add or validate only the owned keys,
      preserve unrelated package/dependency/feature/target/profile/metadata/workspace
      entries and formatting, reject conflicting publication/edition/MSRV/dependency
      policy, and make a repeated plan a no-op.
    - Reject inherited, path, wildcard, range, tag-only, branch-only, wrong registry,
      wrong URL, or wrong revision SDK dependencies before publication.
    - _Requirements: 5.1-5.15, 8.11, 8.13_
  - [ ] 13.3 Reconcile the authored library root, README, and VCS policy semantically
    - Add or adopt one ordinary `pub mod dagger_client;` item only after parsing the
      selected library root; reject an incompatible identifier/path and leave all
      unrelated source bytes unchanged.
    - Create or validate the body-digested `dagger-client-quickstart-v1` README region,
      preserve all unmarked prose, and use the existing line-preserving VCS planner for
      exactly the actual generated subtree and ignored target path.
    - Distinguish complete candidate bytes from the semantic items owned by the SDK so
      touching an authored file never becomes whole-file ownership.
    - _Requirements: 2.4-2.6, 2.11, 5.10-5.11, 6.8-6.10, 6.15, 9.11-9.12_
  - [ ] 13.4 Select or create the exact compatible toolchain and preserve the lockfile
    - Reuse the nearest exact compatible declaration or create client-local
      `rust-toolchain.toml` selecting 1.97.1 only when none exists; reject moving,
      malformed, ambiguous, below-MSRV, or incompatible declarations without shadowing
      caller policy.
    - Treat every existing `Cargo.lock` byte as caller-owned and prove no initialization
      or generation plan contains `GenerateLockfile`, metadata resolution, update,
      network, or other dependency-resolution post-work.
    - _Requirements: 5.14-5.17_
  - [ ] 13.5 Property test: Property 11 — Cargo creation and adoption preserve caller policy
    - Implement `property_11_cargo_creation_adoption_preserve_caller_policy` over at
      least 256 absent/existing Cargo documents, comments/layouts, custom roots, owned
      and unrelated entries, source files, and repeated plans.
    - Compare semantic TOML and exact unaffected byte slices with a reference amendment
      model; accept only the approved package policy and return the exact conflict
      coordinate instead of rewriting incompatible caller state.
    - _Requirements: 2.4-2.6, 5.1-5.4, 5.10-5.13_
  - [ ] 13.6 Property test: Property 12 — SDK dependency is exact, immutable, and fixture-independent
    - Implement `property_12_sdk_dependency_exact_immutable_fixture_independent` over
      at least 256 approved/mutated registry, Git, inherited, local, and mutable
      declarations plus fixture resolver states.
    - Require exact emitted/manifest descriptors, reject every unapproved source before
      publication, and prove local fixture materialization leaves candidate manifest
      bytes and digest unchanged.
    - _Requirements: 5.5-5.9, 8.11, 8.13, 9.13_
  - [ ] 13.7 Property test: Property 13 — toolchain and lockfile policy is reproducible without resolution
    - Implement `property_13_toolchain_lockfile_reproducible_without_resolution` over at
      least 256 declaration precedence/policy mutations, lockfile bytes, operation
      outcomes, and post-work plans.
    - Require exact compatible selection or one exact declaration, reject incompatible
      policies, preserve every lockfile byte on success/failure, and prove no network or
      dependency-resolution action can be represented.
    - _Requirements: 5.14-5.17_

- [ ] 14. Extend manifest-authorized ownership to semantic amendments and regeneration
  - [ ] 14.1 Add backwards-compatible client manifest and amendment verification
    - Extend format version 1 with omitted-by-default client/amendment sections so old
      non-client manifests decode and serialize unchanged while new client manifests
      bind module/pin, package/crate, namespace/root, catalog count/digest, artifacts,
      amendments, target, schema, dependency, generator, and output identity.
    - Reparse current authored files and compare canonical semantic digests for every
      Cargo key, library item, README region, and VCS line; do not authorize an edit or
      deletion by filename, marker name alone, or directory convention.
    - _Requirements: 3.13, 6.1-6.3, 6.8-6.12, 6.15_
  - [ ] 14.2 Publish generated artifacts and amended files as one transaction
    - Extend the existing ownership verifier and journaled publisher to validate all
      artifact/amendment authority before staging, combine multiple semantic edits into
      one complete file candidate, back up and rename in canonical order, roll back
      every injected failure, and publish the acyclic manifest last.
    - Preserve unrelated authored edits between generations while rejecting modified
      owned values, unknown generated destinations, path/symlink escapes, concurrent
      manifest changes, and incomplete removal sets.
    - _Requirements: 2.12, 6.6-6.15_
  - [ ] 14.3 Implement authenticated baseline migration and obsolete-artifact removal
    - Recognize the Feature 5 `EngineHookBaseline` only by exact target/request identity
      plus a fresh pure projection whose path/digest set matches every old manifest
      record; reject all near matches.
    - Transfer only approved Cargo/library semantics to amendment ownership, replace
      the old generated subtree, remove exactly obsolete manifest-owned artifacts, and
      expose the migration atomically as an ordinary new client manifest.
    - Add schema add/rename/remove plans that retain unchanged bytes and never remove an
      authored or unrecorded path.
    - _Requirements: 6.4-6.15_
  - [ ] 14.4 Property test: Property 14 — generated manifest is exhaustive and generation is deterministic
    - Implement `property_14_generated_manifest_exhaustive_generation_deterministic`
      over at least 256 semantic inputs, schema/filesystem/map permutations, no-op prior
      states, project identities, artifact sets, amendments, and post-format digests.
    - Require byte-identical catalogs/artifacts/manifests and an exact bijection between
      owned outputs/semantic values and manifest records.
    - _Requirements: 6.1-6.5_
  - [ ] 14.5 Property test: Property 15 — regeneration changes only proven ownership
    - Implement `property_15_regeneration_changes_only_proven_ownership` over at least
      256 compatible/stale/malformed prior manifests, authored edits, schema changes,
      unknown collisions, target mutations, and baseline migration candidates.
    - Compare with a set-difference plus semantic-amendment reference model and prove
      exact owned replacement/removal, authored preservation, and rejection without
      adoption or filename-inferred authority.
    - _Requirements: 6.6-6.11, 6.15_
  - [ ] 14.6 Property test: Property 16 — all client mutations are confined and failure-atomic
    - Implement `property_16_client_mutations_confined_failure_atomic` over at least 256
      initial trees, artifact/amendment candidates, path/alias/symlink mutations, and
      every publication/rollback fault checkpoint.
    - Compare the observable tree with a copy-on-write transaction model; every failure
      retains exact prior bytes/manifest and every success exposes the complete sorted
      candidate with manifest last.
    - _Requirements: 2.5-2.6, 2.8, 2.12, 6.12-6.14_

- [ ] 15. Implement the path-confined client initializer
  - [ ] 15.1 Plan a valid no-bindings client scaffold
    - Add `plan_client_initialization` over the project snapshot and exact descriptor,
      reusing Task 13 Cargo/toolchain/docs policies while creating a minimal documented
      library only when absent.
    - Emit no `dagger_client` generated subtree, binding catalog, operation manifest,
      generated VCS claim, lockfile, Cargo-resolution work, or prose pretending that
      bindings already exist; document the later `dagger generate` command.
    - Preserve every existing source/file and reject incompatible required paths or
      semantic regions before a result exists.
    - _Requirements: 2.2-2.8, 2.10-2.13, 5.1-5.17_
  - [ ] 15.2 Execute initialization only from a complete immutable candidate
    - Add `EngineExecutionRequest::InitializeClient`, result kind, confined runner, and
      private CLI dispatch; read from the immutable project copy and return touched
      paths only after the full scaffold candidate validates.
    - Perform no external post-work. Ensure cancellation, I/O, path, ownership, or
      project failure yields no mutation-capable result for the Go adapter.
    - Retain module initialization as a separate request and prevent its binary/runtime,
      lockfile-generation, or module-config policy from entering client scaffolds.
    - _Requirements: 2.1-2.8, 2.11-2.13_
  - [ ] 15.3 Property test: Property 2 — client initialization is confined, conservative, and idempotent
    - Implement `property_02_client_initialization_confined_conservative_idempotent`
      over at least 256 new/adopted authored trees, paths, closed arguments, descriptor
      mutations, toolchains, credentials, and injected planning/publication failures.
    - Require exact confined scaffold/adoption, authored preservation, deterministic
      replay, no workspace-record ownership, no secrets/absolute paths, and no result
      for every invalid or failed case.
    - _Requirements: 2.1-2.8, 2.11-2.13_
  - [ ] 15.4 Property test: Property 3 — initial generation obeys the engine-owned scope switch
    - Implement `property_03_initial_generation_obeys_engine_scope_switch` over at
      least 256 initialized workspace/scaffold states and generation booleans using a
      reference engine-scope model.
    - Require exactly the new client scope, bindings only when generation is enabled,
      and a valid honestly documented no-bindings Cargo scaffold under `--no-generate`.
    - _Requirements: 2.9-2.11_

- [ ] 16. Checkpoint: project adoption, ownership, publication, and initialization are green
  - Run formatting, locked focused `dagger-sdk-engine` client project,
    initialization, ownership, publication, and baseline-migration tests; Properties
    2, 3, and 11-16; bounded rustfmt post-work cases; and warning-denied Clippy/rustdoc
    for `dagger-sdk-engine` plus the client compiler dependency.
  - Use only temporary local project roots and pure/fixture runners. Do not invoke Cargo
    dependency resolution or compile the complete generated-client fixture yet.
  - Reuse checked generated assets unless their owning inputs changed; record exact
    commands, phase times, package/target selection, asset decision, and transaction
    fault coverage. Run `cargo deny check` only if Cargo/security inputs changed.
  - Require no Dagger command, engine process, module invocation, another SDK,
    unscoped generation, distribution build, or network resolution.
  - _Requirements: 2.1-2.13, 5.1-5.17, 6.1-6.15, 10.1-10.10_

- [ ] 17. Wire client initialization and multi-client workspace generation through the thin Go ABI
  - [ ] 17.1 Expose the exact `InitClient` module function
    - Add `RustSDK.InitClient(ctx, ws, path, module)` with the target engine signature,
      require a non-nil workspace plus confined non-empty path/module, derive only the
      deterministic package candidate, and invoke the private `InitializeClient`
      request over the immutable workspace snapshot.
    - Return only the SDK-owned changeset. Do not persist or edit the engine-owned
      workspace record, interpret `--no-generate`, resolve another module, forward the
      module ref into Rust/generated output, or expose dynamic SDK arguments.
    - Regenerate only the scoped Rust SDK runtime API client needed to expose the new
      module function, inspect it, and keep Core SDK bindings unchanged.
    - _Requirements: 2.1-2.13_
  - [ ] 17.2 Add one Rust-owned workspace-client-set preflight
    - Add a private `PlanClientSet` request/result which accepts cwd, normalized client
      paths, stable record indices, safe module-reference digests, and stored pins;
      selects only descendants, sorts canonically, rejects duplicates/prefix overlap,
      and returns no filesystem mutation.
    - Have `GenerateClients` call this preflight once, retain engine objects transiently
      by record index, and avoid duplicating selection/overlap semantics in Go.
    - Keep raw module references and Dagger objects out of the Rust request and all
      durable diagnostics/evidence.
    - _Requirements: 7.1-7.3, 7.6-7.8, 8.7-8.10_
  - [ ] 17.3 Resolve and generate every selected client independently and atomically
    - Resolve each record's own module source, module name/original name/source digest,
      and resolved pin; reject a remote stored/resolved mismatch before schema
      compilation and retain empty pins for mutable local sources.
    - Ask the engine for each source's own `ClientSchemaIntrospectionJSON`, construct an
      isolated exact request/output root, and never reuse schema, module source,
      catalog, or manifest state between records.
    - Start every operation from one immutable workspace-before snapshot and make the
      aggregate changeset available only after all disjoint client operations succeed;
      do not generate managed modules or enter another SDK.
    - _Requirements: 3.1-3.4, 3.13, 7.4-7.13_
  - [ ] 17.4 Converge modern workspace and legacy client adapters
    - Extend the shared request encoder with resolved pin and selected project identity
      so equivalent modern and direct `GenerateClient` inputs reach the same Rust
      compiler/reconciler/publication path.
    - Validate the legacy output root and finite host snapshot, read
      `ModuleSource.Pin`, and compare path-relativized generated source, catalog,
      project semantics, artifacts, and provenance while permitting only root-relative
      control-path differences.
    - _Requirements: 3.1-3.4, 7.14, 8.14_
  - [ ] 17.5 Property test: Property 4 — one workspace record resolves to one exact bound module
    - Implement `property_04_workspace_record_resolves_one_exact_bound_module` over at
      least 256 local/remote records, source identities, stored/resolved pins,
      resolution failures, extra-module attempts, and manifest bindings.
    - Require exactly one identity plus complete target/module/pin/schema/dependency
      provenance, and reject mismatch or ambiguity before compilation/publication.
    - _Requirements: 3.1-3.4, 3.13, 7.4_
  - [ ] 17.6 Property test: Property 17 — workspace cwd selection is canonical and Rust-only
    - Implement `property_17_workspace_cwd_selection_canonical_rust_only` over at least
      256 cwd/record trees, SDK ownerships, orderings, normalized/invalid paths, and
      operation expansions using the production preflight.
    - Select exactly Rust clients at/below cwd in canonical order, retain independent
      roots/manifests, and prove no module-generation or other-SDK action can appear.
    - _Requirements: 7.1-7.7, 7.12-7.13_
  - [ ] 17.7 Property test: Property 18 — multiple client operations are isolated and all-or-nothing
    - Implement `property_18_multiple_clients_isolated_all_or_nothing` over at least
      256 equal/overlapping/disjoint path sets, same/different module bindings, isolated
      schema/catalog states, operation orderings, and injected client failures.
    - Require preflight rejection before work for overlap, independent manifests for
      every disjoint root, no cross-client semantic state, and no aggregate changeset
      after any sibling failure.
    - _Requirements: 7.8-7.11_
  - [ ] 17.8 Property test: Property 19 — modern and legacy generation converge on one semantic result
    - Implement `property_19_modern_legacy_generation_semantically_converge` over at
      least 256 equivalent target/module/pin/schema/dependency/project/output inputs and
      adapter-root permutations.
    - Compare path-relativized source/catalog/Cargo/amendment/artifact/provenance output
      exactly and allow differences only in explicitly classified control paths.
    - _Requirements: 7.14_

- [ ] 18. Build the complete engine-free generated-client usability harness
  - [ ] 18.1 Materialize exact candidates through the production stack
    - Add a fixture harness that invokes the real `ClientCompiler`, project identity,
      renderer, reconciler, formatter post-work, ownership verifier, and publisher into
      temporary roots for Core-only, local-module, dependency-bound, and adopted
      projects.
    - Resolve the exact registry/Git SDK descriptor to the checked local public SDK
      outside the candidate tree, reuse one materialized dependency baseline across
      cases, and reject any mutation of candidate Cargo bytes or generated provenance.
    - Record per-phase and per-Cargo-invocation timings so duplicate SDK builds or an
      unexpectedly broad package graph are visible.
    - _Requirements: 5.5-5.9, 5.14-5.17, 9.1-9.6, 9.11-9.14, 10.5-10.8_
  - [ ] 18.2 Add the representative pass, adoption, regeneration, and failure corpus
    - Cover Core-only/runtime-less, a rich local module, a pinned dependency-bound
      module without its transitive namespace, custom library roots, existing Cargo and
      source/docs/VCS policy, caller lockfiles, schema add/rename/remove, and identical
      replay.
    - Cover Core drift, promoted/multiple roots, dependency leakage, name collision,
      mutable dependency, pin mismatch, overlapping roots, stale/malformed manifest,
      unknown destination, path/symlink escape, format failure, and every transaction
      fault, proving no partial candidate.
    - Keep fixture schemas/trees recorded and generated only when their owning semantic
      inputs change.
    - _Requirements: 2.4-2.12, 3.5-3.13, 5.1-5.17, 6.4-6.15, 9.1-9.6, 9.11-9.14_
  - [ ] 18.3 Exercise generated Core and module operations through recording transport
    - Construct a normal public `Client` via the existing injected connector, import
      the generated extension trait, execute representative Core and module operations,
      and assert exact raw GraphQL documents, omission/explicit values, response
      traversal, typed errors, lifecycle fences, and close behavior.
    - Use production generated types and bridge methods; do not construct private
      sessions/selections or substitute fixture-only handles/query code.
    - Compile and run the generated quickstart through the same public lifecycle route
      without editing generated source.
    - _Requirements: 4.4-4.16, 9.7-9.14_
  - [ ] 18.4 Property test: Property 23 — every generated client class passes the scoped Cargo contract
    - Implement `property_23_generated_client_classes_pass_scoped_cargo_contract` over
      at least 128 Core-only/local/dependency schema and new/adopted project candidates,
      while batching a bounded representative set into each Cargo execution.
    - Require pinned rustfmt, check, warning-denied Clippy/rustdoc, compiled quickstart,
      immutable dependency declaration, and evidence that production renderer/runtime
      paths—not fixture substitutes—were invoked.
    - _Requirements: 9.1-9.6, 9.11-9.14_
  - [ ] 18.5 Property test: Property 24 — generated Core and module queries use one public transport contract
    - Implement `property_24_generated_queries_one_public_transport_contract` over at
      least 128 generated Core/module operations, aliases, wrappers, argument/omission
      states, responses, GraphQL/transport errors, and lifecycle schedules.
    - Compare recorded requests/decoded outcomes to the canonical query model and prove
      the quickstart/public API never enters a hidden fixture transport route.
    - _Requirements: 9.7-9.12_

- [ ] 19. Complete client diagnostics, security boundaries, and checkpoint planning
  - [ ] 19.1 Audit every rejection site against the total diagnostic table
    - Map every schema/name/codec, workspace/pin/path, project/Cargo/toolchain,
      ownership/publication, fixture/checkpoint, closure, and sign-off rejection to one
      stable primary code and the approved safe coordinate class.
    - Aggregate compiler/completeness diagnostics deterministically, retain typed safe
      causes for engine diagnostics, and add exhaustive fixed cases for every error
      table row without matching implementation prose as API.
    - _Requirements: 8.1-8.6_
  - [ ] 19.2 Enforce secret, source, path, unsafe, and dependency boundaries
    - Generate credential-shaped module refs, Git/registry URLs, environment/session
      values, Cargo/Git stderr, GraphQL values, hostile docs/coordinates, and host paths;
      require redaction or pre-output rejection across requests, diagnostics, manifests,
      generated source/docs, checkpoints, and completeness evidence.
    - Extend package/source/security policy tests for no ambient SDK path, unapproved
      source, unsafe Rust, global client state, raw authorization/session values, and
      accidental private-crate dependency.
    - _Requirements: 2.13, 8.6-8.14_
  - [ ] 19.3 Extend the typed engine-free checkpoint planner for generated clients
    - Add only the Feature 7 package/test/fixture/direct-Go-ABI actions to the existing
      closed planner, with explicit checked-asset input/output digests, reuse versus
      scoped-regeneration decision, package/target identity, Cargo invocation count,
      outcome, and elapsed phase timing.
    - Reject Dagger/engine/module commands, another SDK, unscoped generation,
      distribution builds, network resolution, duplicate/empty actions, incomplete
      observations, and executable engine exceptions. Model an exception only as
      separately approvable sign-off evidence.
    - _Requirements: 10.1-10.10_
  - [ ] 19.4 Property test: Property 20 — diagnostics are total, stable, ordered, and safely located
    - Implement `property_20_diagnostics_total_stable_ordered_safely_located` over at
      least 256 invalid-domain values from every producing layer, discovery orders,
      duplicate causes, control characters, and coordinate mutations.
    - Require exactly one declared primary code per condition, exact safe
      schema/path/semantic-key coordinates, deterministic sorting/deduplication, and no
      terminal control output.
    - _Requirements: 8.1-8.6_
  - [ ] 19.5 Property test: Property 21 — credentials and host identity never cross the client boundary
    - Implement `property_21_credentials_host_identity_never_cross_client_boundary`
      over at least 256 request/environment/dependency/diagnostic/generated/evidence
      combinations seeded with credential and absolute-path shapes.
    - Search every observable encoded/rendered byte and require rejection or redaction,
      allowing only approved immutable dependency URLs with userinfo removed and never
      unsafe Rust or an ambient local SDK source.
    - _Requirements: 2.13, 8.7-8.13_
  - [ ] 19.6 Property test: Property 25 — local checkpoints are observably engine-free and change-triggered
    - Implement `property_25_local_checkpoints_engine_free_change_triggered` over at
      least 256 action/package expansions, asset states, observations, timing records,
      Cargo counts, forbidden boundaries, and exception records.
    - Admit only complete scoped plans with the correct reuse/regeneration decision and
      timings; prove an engine proposal remains non-executable pending separate proof
      and approval.
    - _Requirements: 10.1-10.10_

- [ ] 20. Checkpoint: production client path is engine-free integration-complete
  - Run formatting; locked focused client tests in `dagger-codegen`, `dagger-sdk`,
    `dagger-sdk-engine`, and `dagger-sdk-completeness`; direct `sdk/rust/runtime` Go ABI
    tests; Properties 4, 17-25; the bounded Cargo/recording-transport corpus; and
    warning-denied Clippy/rustdoc for only the four Rust packages.
  - Execute the generated-client fixture phase once with one materialized exact SDK
    baseline, record every Cargo invocation and phase time, and fail the checkpoint if
    another SDK package or duplicate SDK baseline enters the graph.
  - Reuse checked generated assets unless an owning digest changed; if the scoped Rust
    runtime API client changed for `InitClient`, refresh it once and record/inspect the
    exact diff. Do not regenerate Core bindings.
  - Run `cargo deny check` and the focused Rust security/source/package-policy slices if
    dependency, public package, generated source, or security inputs changed; otherwise
    record their matching previous evidence for the final gate.
  - Require no Dagger command, engine process, module invocation, unrelated SDK,
    repository-wide generation, distribution build, or network resolution.
  - _Requirements: 2.1-2.13, 3.1-3.13, 4.1-4.17, 5.1-5.17, 6.1-6.15, 7.1-7.14, 8.1-8.14, 9.1-9.14, 10.1-10.10_

- [ ] 21. Implement client-generation closure and deferred sign-off evidence admission
  - [ ] 21.1 Add the exact engine-free Implementation Closure gate
    - Add `ClientGenerationClosureObservation`, exact required evidence-domain set,
      implementation/capability/catalog/manifest/checkpoint identities, canonical
      digest, and admission function in `dagger-sdk-completeness`.
    - Require passed matching compiler, project, API, query, diagnostic/security,
      Cargo hygiene, direct ABI, and checkpoint observations with no local engine or
      other-SDK event; reject missing, stale, skipped, failed, duplicate, mismatched, or
      unplanned evidence as one complete-set failure.
    - Advance only mapped policy capabilities whose allowed evidence domain is fully
      admitted; retain the engine initialization lifecycle blocker until SDK sign-off.
    - _Requirements: 1.4-1.11, 10.11-10.12_
  - [ ] 21.2 Add the bounded client SDK sign-off inventory validator
    - Add typed cases for one initialized local client, pinned remote dependency-bound
      client, schema regeneration, Core query, and namespaced module query plus exact
      target artifact, build/start/install counts, isolated outcomes, phase timings,
      and one digest-bound verdict.
    - Consume one matching local closure without replaying it; reject absent, stale,
      skipped, failed, cross-target, duplicate-build, duplicate-engine, multiple
      baseline, incomplete timing, or non-atomic verdict observations.
    - Keep the validator pure and its execution deferred to Feature 8.
    - _Requirements: 10.12-10.19_
  - [ ] 21.3 Property test: Property 26 — Implementation Closure consumes only complete matching local evidence
    - Implement `property_26_implementation_closure_complete_matching_local_evidence`
      over at least 256 evidence-set permutations, exact-target and implementation
      digests, catalog/manifest identities, checkpoint boundaries, outcomes, and
      unrelated engine/SDK observations.
    - Admit only the complete matching engine-free set and require canonical reusable
      closure output without replaying local work.
    - _Requirements: 10.11-10.12_
  - [ ] 21.4 Property test: Property 27 — SDK sign-off inventory is bounded, reused, and atomic
    - Implement `property_27_sdk_signoff_inventory_bounded_reused_atomic` over at least
      256 closure/inventory/build/start/baseline/case/timing/verdict mutations.
    - Require all five exact cases, at-most-once builds, one engine/baseline, isolated
      results, complete timings, and one matching atomic verdict; reject the complete
      candidate after any invalid observation.
    - _Requirements: 10.13-10.19_
  - [ ] 21.5 Render the honest Feature 7 completeness result
    - Regenerate the mapping/policy/evidence-derived report only from admitted records,
      distinguish initialization, generated content, Cargo integration, regeneration,
      query usability, local closure, and sign-off, and retain every unexecuted
      engine-backed blocker.
    - Verify the `TestProvision` correction and dependency-scope wording survive all
      derived inventories and no Feature 5 hook evidence is reused as content proof.
    - _Requirements: 1.1-1.12, 10.11-10.19_

- [ ] 22. Document the durable standalone-client and contributor workflow
  - [ ] 22.1 Add `sdk/rust/CLIENT_GENERATION.md`
    - Document initialization, `--no-generate`, generation/regeneration, local versus
      pinned remote binding, separately bound dependency clients, `dagger_client`,
      `core`, module namespace, extension trait/prelude, public lifecycle, Cargo and
      lockfile policy, ownership manifest, diagnostics, and authored-file preservation.
    - Include a compile-checked quickstart and troubleshooting flow without presenting
      generated implementation layout or an engine run as the normal development API.
    - _Requirements: 2.1-2.13, 3.1-3.13, 4.1-4.17, 5.1-5.17, 6.1-6.15, 9.11-9.12_
  - [ ] 22.2 Update architecture, contribution, crate README, and generated guidance
    - Add the pure compiler/reconciler/runtime-bridge/publication boundaries and the
      engine-free fixture/checkpoint commands to `ARCHITECTURE.md`, `CONTRIBUTING.md`,
      owning crate READMEs, and generated package README regions.
    - Explain change-triggered checked-asset reuse, one materialized fixture SDK
      baseline, scoped Cargo invocations, semantic amendment ownership, and the
      requirement to investigate fixture behavior before a broader rerun.
    - Apply the repository documentation rule to new handwritten modules/public items
      and correctness-critical WHY comments without embedding spec feature/task labels
      in source comments.
    - _Requirements: 4.17, 6.1-6.15, 9.11-9.14, 10.1-10.12_
  - [ ] 22.3 Correct and strengthen the umbrella contract
    - Replace the stale implication that one client contains transitive dependency
      surfaces with the approved independent bound-client model.
    - Place the engine-free Rust-first checkpoint rule and the bounded reusable SDK
      sign-off contract together near the top: one exact-target artifact, at-most-once
      engine/CLI/Go-runtime/Rust content, one engine and installed Rust baseline, no
      unrelated SDK work, reused closure evidence, isolated cases, phase timings, and
      an atomic verdict rejecting duplicate builds/starts.
    - Keep Feature 8 as sign-off owner and Feature 9 as publication/release owner.
    - _Requirements: 1.12, 10.1-10.19_
  - [ ] 22.4 Add documentation and command-drift tests
    - Verify every documented local command resolves to the typed checkpoint action set,
      paths/test names exist, quickstarts compile, dependency and ownership claims match
      production constants, and no local section invokes Dagger, an engine, another
      SDK, unscoped generation, distribution, or network resolution.
    - _Requirements: 5.1-5.17, 9.11-9.14, 10.1-10.19_

- [ ] 23. Wire and record the complete engine-free feature-end gate
  - [ ] 23.1 Add the executable scoped gate and derived-evidence recorder
    - Compose the exact typed feature-end evidence requirements from the design:
      format; focused client properties/unit/compile/fixture tests in the four Rust
      packages; direct Go ABI tests; warning-denied Clippy/rustdoc; Cargo Deny;
      source/package/security policy; generated drift; completeness derivation; and
      clean-output validation.
    - Compare each checkpoint observation with its own current owning-input digest and
      schedule only missing, failed, or stale actions. Reuse a passed Task 20
      compiler/runtime/project/fixture/security observation when Tasks 21-22 changed
      only completeness or documentation inputs; do not turn “feature end” into an
      unconditional replay of the expensive generated-client corpus.
    - Reuse one materialized fixture SDK baseline and checked Core/module assets,
      schedule regeneration only for changed owning digests, and reject duplicate Cargo
      baseline builds or action expansion outside the approved graph.
    - Record command identity, selected packages/targets, outcomes, elapsed phase times,
      Cargo counts, generated-asset decisions, and complete implementation digest for
      closure admission.
    - _Requirements: 9.1-9.14, 10.1-10.12_
  - [ ] 23.2 Produce canonical closure evidence and leave sign-off explicitly unexecuted
    - Admit only passed current feature-end observations, write canonical Feature 7
      evidence and report artifacts, and prove regeneration of those derived files is
      deterministic and leaves the worktree byte-clean.
    - Render the exact-engine local/pinned/regeneration/Core/module inventory as pending
      Feature 8 work; do not synthesize, skip-as-pass, or execute an engine observation.
    - _Requirements: 1.4-1.11, 10.11-10.19_

- [ ] 24. Final checkpoint: Feature 7 is engine-free implementation-complete
  - Run the executable Task 23 gate once from the documented working directories. Its
    admitted current evidence must cover all 27 required property tests at their
    declared case counts, fixed and `trybuild` cases, the complete generated-client
    Cargo/recording-transport corpus, direct Go ABI tests, fmt, warning-denied
    Clippy/rustdoc, Cargo Deny, source/package/security policy, generated
    ownership/drift, completeness derivation, documentation commands, and clean output.
  - Execute only actions whose owning inputs changed since their recorded checkpoint.
    In the expected path after Task 20, rerun the new closure/sign-off properties,
    documentation/derived-report checks, format, and clean-output checks while reusing
    matching compiler/runtime/project/fixture/security observations. If a later task
    touched one of those domains, rerun that domain's scoped action—not the whole graph.
  - Require one materialized exact SDK fixture baseline and report every Cargo
    invocation/phase time. Do not broaden to `cargo test --workspace` or another SDK
    merely to create confidence already supplied by scoped packages and closure
    evidence.
  - Reuse checked Core/module assets unless an owning digest changed; if any scoped
    runtime API or client metadata refresh was required, verify it occurred once and
    its inspected output identity matches the manifest/evidence record.
  - Confirm no Dagger command, engine process, module invocation, other SDK, unscoped
    generation, distribution build, or network resolution occurred. Confirm the
    current report admits Feature 7 Implementation Closure while retaining every
    Feature 8 SDK-sign-off blocker.
  - Record exact commands and elapsed times in this task when executed; do not copy
    planned commands into evidence as though they ran.
  - _Requirements: 1.1-1.12, 2.1-2.13, 3.1-3.13, 4.1-4.17, 5.1-5.17, 6.1-6.15, 7.1-7.14, 8.1-8.14, 9.1-9.14, 10.1-10.19_

## Deferred SDK Sign-off Gate

Feature 7 implements and property-tests the sign-off inventory/admission contract but
does not execute it. Feature 8 consumes the admitted matching Implementation Closure,
builds one reusable exact-target artifact and engine/CLI/Go-runtime/Rust content at
most once, starts one engine, installs one Rust baseline, runs the isolated initialized
local, pinned remote, regeneration, Core-query, and namespaced-module-query cases, and
emits one atomic digest-bound verdict with phase timings. It rejects duplicate builds
or engine starts and does not replay Feature 7's compiler, Cargo, fixture, hygiene,
security, or other-SDK work.

## Task Dependency Graph

```json
{
  "1": [],
  "2": ["1"],
  "3": ["1"],
  "4": ["2", "3"],
  "5": ["4"],
  "6": ["5"],
  "7": ["6"],
  "8": ["7"],
  "9": ["8"],
  "10": ["9"],
  "11": ["10"],
  "12": ["11"],
  "13": ["12"],
  "14": ["13"],
  "15": ["14"],
  "16": ["15"],
  "17": ["16"],
  "18": ["17"],
  "19": ["18"],
  "20": ["19"],
  "21": ["20"],
  "22": ["21"],
  "23": ["21", "22"],
  "24": ["23"],
  "sdk-signoff": ["24"]
}
```

The six checkpoints are the bounded review boundaries: models/scope/metadata; pure
schema/projection/rendering; exact-version runtime composition; project ownership and
initialization; full engine-free production integration; and final closure. Each
checkpoint compiles and proves only the graph introduced since the prior boundary.

## Notes

- Every Property 1-27 task is mandatory. Pure/reference properties use at least 256
  cases; filesystem, Cargo, async, and compile-model properties use at least 128;
  bounded fixed/compile corpora supplement rather than replace generated cases.
- Stable `property_NN_*` test identifiers plus the design/task requirement citations
  preserve property traceability. In accordance with the approved repository policy,
  Rust/Go source comments explain enduring invariants and do not contain Feature, task,
  checkpoint, or planning labels; the trace stays in spec and test names.
- Checkpoints run the narrowest owning packages and targets. Workspace-wide test,
  Clippy, rustdoc, security, package, and compile matrices are not repeated after every
  checkpoint; affected security/package slices run on owning-input changes and the
  complete scoped set runs once at Task 24.
- Generated assets are change-triggered. Client metadata changes once in Task 3; the
  module API client changes once when `InitClient` is exposed; Core bindings do not
  regenerate unless their actual target/schema/generator identity changes. Every
  refresh is scoped, inspected, and recorded before returning to checked assets.
- The fixture harness materializes the exact SDK dependency once per implementation
  identity and reuses it across client classes, project variants, query cases, and
  retries. Cargo invocation counts and phase timings are observable checkpoint data,
  not informal performance notes.
- No local checkpoint or Implementation Closure constructs or invokes a Dagger engine.
  If a contract appears impossible to model directly, stop and record the exact gap,
  evidence that the production direct model is insufficient, and the smallest proposed
  Feature 8 sign-off case for explicit approval. Convenience or uncertainty is not
  sufficient.
- The Go runtime remains an ABI shim around Dagger objects. Rust owns client-set
  preflight, schema validation, public API shape, Cargo/project reconciliation,
  ownership/publication, diagnostics/security, checkpoint planning, and evidence
  admission.
- Checkpoints are suitable coherent commit/review boundaries. Diagnose a failed
  fixture against its contract and sequencing before running a broader graph; do not
  accumulate unverified layers or repeat expensive suites merely to reach a PR.
- Implementation Closure and SDK Sign-off are distinct evidence states. Green local
  code cannot close the engine-owned initialization lifecycle; a later engine smoke
  cannot replace exhaustive local compiler/project/runtime evidence.
