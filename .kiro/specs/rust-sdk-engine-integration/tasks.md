# Implementation Plan

- [ ] 1. Establish the exact engine-integration scope, private crate, and fast test foundation
  - [ ] 1.1 Make Rust SDK engine work lazy and independently testable
    - Remove the unconditional Dagger engine/client installation from
      `toolchains/rust-sdk-dev.New`; retain a Rust-only base and introduce an explicit
      engine-bearing path used only by engine-content and integration cases.
    - Add the `engine-unit` function for focused Rust engine-tool and Go adapter tests,
      include the approved Feature 5 specifications and required engine source in the
      toolchain input filter, and prove that this function does not construct an engine.
    - Keep existing public Rust checks behaviorally unchanged and document why test-only
      changes cannot invalidate the packaged compiler/toolchain layer.
    - _Requirements: 13.31, 13.32, 13.33, 13.35_
  - [ ] 1.2 Add the private Rust engine-tool crate and approved dependency graph
    - Add `dagger-sdk-engine` with `publish = false`, binary
      `dagger-rust-engine`, library entrypoint, README, Apache-2.0 metadata, module
      documentation, `unsafe_code = "deny"`, and workspace lint inheritance.
    - Add the reviewed workspace-pinned `toml_edit` dependency and reuse existing
      serde, error, hashing, async, CLI, filesystem-locking, temporary-file, and test
      dependencies with the minimum required features.
    - Keep `dagger-sdk` as the sole publishable crate, update `Cargo.lock`, and extend
      cargo-deny source/license policy only where the approved locked graph requires it.
    - _Requirements: 11.6, 11.7, 11.24, 12.16_
  - [ ] 1.3 Align property traceability and source-documentation guidance
    - Reconcile `sdk/rust/AGENTS.md` with the approved no-feature-label policy: property
      identity lives in stable `property_NN_*` test names, while comments explain the
      invariant without naming a specification feature, task, or planning phase.
    - Require `//!` ownership and invariant documentation on every new module, `///`
      guarantees on caller-relevant items, and inline WHY comments for digest domains,
      path confinement, ownership, precedence, cancellation, credential boundaries,
      and deliberate engine/Rust translations.
    - _Requirements: 12.8, 12.9, 12.15, 12.16, 13.33_
  - [ ] 1.4 Add strict canonical engine-integration models and shared strategies
    - Add the target identity, relative path, operation request/kind, module input,
      operation plan/manifest, artifact record, engine source/dependency descriptor,
      discovered/runtime Cargo typestates, runtime provenance input/result, packaged
      asset, capability mapping, and integration evidence models.
    - Use strict versioned serde boundaries, canonical lexical object/set ordering,
      lowercase SHA-256 identities, immutable registry/Git dependency variants, and
      typed normalized relative paths; keep filesystem/process/Dagger I/O out of
      `dagger-codegen`.
    - Add valid-first `proptest` strategies for each canonical model, dependency source,
      target mutation, schema identity, path, Cargo graph, artifact set, evidence
      subject, and stable diagnostic coordinate; centralize 256-case pure and 128-case
      filesystem/concurrency defaults above the 100-case floor.
    - _Requirements: 2.5-2.8, 5.8-5.16, 6.1, 6.2, 6.10, 8.2-8.9, 12.8, 12.9, 12.15_
  - [ ] 1.5 Register the exact engine-integration scope and policy inventory
    - Add the 22 approved `policy/rust-policy/engine-*` capabilities and the exact
      31-row existing Feature 5 scope without changing a status.
    - Add closed implementation-subject, owner, evidence-domain, delegated-content,
      and `IdiomaticEquivalent` mapping records; reject missing, duplicate, moved,
      name-only, catch-all, wrong-target, fingerprint-drifted, and out-of-scope rows.
    - Preserve hook evidence separately from Feature 6 dispatch and Feature 7 client
      content evidence, and retain the approved scope digest
      `sha256:f74b60b146fe70876a7aac39810eb1e3c4da49fc7fc1cfa9075293c1c8906fdc`.
    - _Requirements: 1.1-1.12_
  - [ ] 1.6 Property test: Property 1 — exact capability scope and evidence separation
    - Implement a reference-set `proptest` with at least 256 generated row, owner,
      evidence-domain, status, ordering, delegation, and policy mutations; accept only
      the approved 31/22 partition and require status identity before evidence exists.
    - Test identifier: `property_01_exact_capability_scope_evidence_separation`.
    - The one-line invariant comment explains why hook and delegated-content evidence
      cannot close one another, without naming a specification feature.
    - _Requirements: 1.1-1.12_
  - [ ] 1.7 Property test: Property 30 — canonical models round-trip without semantic loss
    - Generate at least 256 valid and invalid operation, descriptor, manifest,
      provenance, path, dependency, and evidence models; require strict
      encode/decode equality and digest equality while rejecting unknown fields,
      invalid enums, mutable refs, malformed digests, and non-canonical paths.
    - Test identifier: `property_30_canonical_models_round_trip_without_semantic_loss`.
    - _Requirements: 2.6-2.8, 5.9-5.16, 6.10, 8.2-8.9, 13.24-13.26_

- [ ] 2. Checkpoint: scope, canonical models, and engine-free development are green
  - Run Rust formatting, locked check/test for the new private crate and completeness
    modules, Properties 1 and 30, warning-denied clippy/rustdoc, cargo-deny, focused Go
    toolchain tests, and `rust-sdk-dev engine-unit`.
  - Require `engine-unit` to avoid engine construction/network fetch for engine assets,
    the pre-change ledger to remain status-identical, every canonical model to reject
    ambiguous input, and the worktree to contain no generated engine content.

- [ ] 3. Implement the pure visible-schema operation compiler and four renderer seams
  - [ ] 3.1 Generalize Feature 4 validation to exact core plus visible extensions
    - Add `ExactCoreWithExtensions` beside the existing exact-target mode, generated
      from the checked core-coordinate manifest rather than name prefixes.
    - Require every core semantic fingerprint, admit operation-scoped module/dependency
      coordinates without redefinition, resolve the complete reference closure, and
      reuse Feature 4 wrapper, default, directive, naming, collision, documentation,
      projection, and canonical-ordering policy unchanged.
    - _Requirements: 5.1-5.7, 6.18_
  - [ ] 3.2 Add the closed operation selector and input matrix
    - Implement `OperationKind`, `OperationProjectionRequest`, and `OperationPlan` for
      GenerateLibrary, GenerateModule, GenerateClient, and GenerateEntrypoint.
    - Enforce required/forbidden module, schema, output, dependency, and TypeDef inputs
      before rendering; retain the exact engine target and normalized output identity,
      and return typed diagnostics for every unconstructable selector/input pair.
    - Keep the pure facade free of filesystem, process, Cargo, engine, network,
      completeness, and publication I/O.
    - _Requirements: 5.8-5.13, 6.1, 6.2, 6.5-6.7, 6.19_
  - [ ] 3.3 Implement production library/module renderers and bounded hook baselines
    - Render visible-schema library bindings and module extension bindings through the
      existing semantic projection/token pipeline, resolving core types through public
      `dagger-sdk` and never embedding transport code.
    - Render the module-owned `src/dagger_generated/**` tree and private
      `src/bin/dagger-module.rs` target while leaving starter/authored source outside
      generator ownership; reserve operation-manifest creation for the runner.
    - Add a valid Cargo client baseline carrying the `engine-hook-baseline` content
      domain and an entrypoint renderer that accepts only the checked private protocol
      probe TypeDef; neither renderer may claim sibling content completeness.
    - _Requirements: 4.13-4.16, 4.20, 6.3-6.7, 6.18, 7.2-7.6, 10.10-10.13_
  - [ ] 3.4 Add Rust-owned client-generation metadata
    - Derive canonical required-host-file metadata from renderer configuration, validate
      every entry as a normalized relative path, and emit the baseline empty set as
      `client-generation.json` input for engine packaging.
    - Add fixed cases for empty, finite, duplicate, absolute, traversal, and
      normalization-equivalent required file sets; Go must consume this metadata rather
      than own a second client policy.
    - _Requirements: 7.3-7.7_
  - [ ] 3.5 Property test: Property 10 — visible schema validation is compatible and order-invariant
    - Generate at least 256 exact-core/extension schemas, core mutations, unresolved
      references, collisions, and independent array permutations; compare admission,
      canonical projection, artifacts, and diagnostics to a reference closure model.
    - Test identifier: `property_10_visible_schema_compatible_order_invariant`.
    - _Requirements: 5.1-5.7, 6.18_
  - [ ] 3.6 Property test: Property 12 — operation dispatch is total and lossless
    - Use recording renderers over at least 256 selector/input combinations; require
      exactly one matching invocation with byte/value-identical schema, module,
      dependency, TypeDef, output, artifacts, and operation-specific inputs, and zero
      renderer calls for unknown or invalid selectors.
    - Test identifier: `property_12_operation_dispatch_total_lossless`.
    - _Requirements: 6.1-6.7_

- [ ] 4. Checkpoint: pure engine code generation is green
  - Run formatting, locked codegen/engine-tool tests, Properties 10 and 12, fixed
    operation-matrix and renderer fixtures, warning-denied clippy/rustdoc, cargo-deny,
    and `rust-sdk-dev engine-unit`.
  - Require every renderer to consume one shared `VisibleSchemaPlan`, the existing
    Feature 4 exact-target generation/check to remain byte-clean, and no pure crate to
    acquire an I/O or public-runtime dependency.

- [ ] 5. Implement immutable dependency policy and Cargo project adoption
  - [ ] 5.1 Validate engine source and published SDK dependency descriptors
    - Decode strict engine repository/revision, engine/Rust SDK version, toolchain,
      schema, asset-manifest, and dependency coordinates; accept only exact registry
      `dagger-sdk` versions or HTTPS Git URLs with a full immutable revision.
    - Reject wildcard, branch, tag, default-revision, local path, userinfo-bearing URL,
      wrong package, incomplete provenance, and target mismatch before planning project
      changes.
    - _Requirements: 2.5-2.8, 2.13, 2.14, 4.6-4.10, 11.8-11.12_
  - [ ] 5.2 Discover Cargo workspaces and packages through versioned metadata
    - Run only the pinned `cargo metadata --format-version 1 --no-deps
      --manifest-path <candidate>` shape with cancellation, bounded output, and a
      narrow unknown-field-tolerant serde view.
    - Normalize returned roots against the operation capability and select exactly the
      workspace member whose package root owns the engine-selected module source;
      reject zero/multiple matches and symlink escape without recursive manifest scans.
    - Model pre-initialization state as `DiscoveredCargoProject`; promote it to
      `RuntimeCargoProject` only after lockfile, exact toolchain, generated manifest,
      and engine-selected binary verification.
    - _Requirements: 4.1-4.5, 8.14-8.17_
  - [ ] 5.3 Plan format-preserving Cargo manifest and exact toolchain amendments
    - Use `toml_edit` to create a new edition-2024/MSRV package or semantically amend
      only the owning `dagger-sdk` dependency and generated `dagger-module` binary
      target in an existing package/workspace.
    - Preserve comments, decoration, ordering, unrelated dependencies/features,
      profiles, patches, workspace inheritance, package fields, compatible edition,
      compatible Rust version, and enclosing exact toolchain declarations.
    - Select package-local exact declaration, enclosing exact declaration, then target
      default Rust 1.97.1; diagnose below-MSRV, moving, ambiguous, or unresolvable
      toolchains without rewriting caller policy.
    - _Requirements: 4.2, 4.6-4.12, 8.10-8.13, 11.8-11.12_
  - [ ] 5.4 Plan narrow VCS and authored-file changes
    - Add line-preserving `.gitignore`/`.gitattributes` edits for only missing generated
      and ignored paths, retain every unrelated byte, and document the regeneration
      command for each owned generated artifact.
    - Create starter source only when no authored target exists; never pass existing
      authored Rust source to a renderer or formatter and never infer ownership from a
      filename/header/directory.
    - _Requirements: 4.13-4.16, 4.19, 4.20_
  - [ ] 5.5 Property test: Property 7 — Cargo package selection has exactly-one semantics
    - Generate at least 256 Cargo metadata/workspace graphs, nested source paths,
      manifest hints, orderings, and zero/multiple match cases; compare to an
      independent normalized ownership model.
    - Test identifier: `property_07_cargo_package_selection_exactly_one`.
    - _Requirements: 4.1-4.5_
  - [ ] 5.6 Property test: Property 8 — Cargo adoption preserves caller policy
    - Generate at least 256 compatible/incompatible manifests, workspace inheritance
      layouts, dependency forms, comments/decorations, editions, Rust versions, and
      toolchain declarations; require only approved semantic edits and exact immutable
      dependency rendering.
    - Test identifier: `property_08_cargo_adoption_preserves_caller_policy`.
    - _Requirements: 4.2, 4.6-4.12, 11.8-11.12_

- [ ] 6. Implement confined execution, post-work, ownership, and atomic publication
  - [ ] 6.1 Add lexical and symlink-aware operation-root capabilities
    - Implement canonical relative-path parsing separately from the private
      `OperationRoot` filesystem capability; reject absolute paths, empty-forbidden
      paths, dot/dot-dot, separator aliases, case-fold collisions, non-regular inputs,
      path aliases, and any symlink crossing the scoped real root.
    - Revalidate every resolved parent immediately before access/publication and keep
      absolute/container paths out of canonical request, manifest, provenance, and
      diagnostic subjects.
    - _Requirements: 3.6, 3.7, 5.13-5.15, 7.4, 7.6, 7.7, 12.9_
  - [ ] 6.2 Implement new/existing project initialization planning
    - Compose Cargo, toolchain, starter source, initialization VCS, and lockfile plans
      into one SDK-owned candidate confined to the engine-selected module path; do not
      run or embed the generated renderer in the initialization Changeset.
    - Exclude engine-owned `dagger.toml` and `dagger-module.toml`, preserve unrelated
      workspace/module bytes, leave the later scoped generation decision to the engine,
      and return no Changeset-capable result until dependency resolution succeeds.
    - _Requirements: 3.4-3.10, 3.13, 4.1-4.20_
  - [ ] 6.3 Add the closed, bounded post-work executor
    - Implement only `FormatRust`, `GenerateLockfile`, and `VerifyLockedMetadata` with
      runner-authored argument vectors, fixed executable paths, allowlisted environment,
      secret mounts, cancellation/reaping, bounded redacted output, and no shell.
    - Record every post-work-mutated owned digest, permit at most two projection passes,
      require the second to converge, and diagnose any third candidate without
      publication.
    - _Requirements: 4.17, 4.18, 6.8-6.12, 12.13-12.15_
  - [ ] 6.4 Add manifest-authorized ownership and failure-atomic publication
    - Validate every prior owned path and current digest; compute the complete sorted
      add/change/remove set; reject unknown content, stale/incompatible manifests,
      generator-looking but unowned files, traversal, and symlink/path collisions.
    - Stage and flush complete candidates beside destinations, publish the acyclic
      operation manifest last, record rollback state, restore all completed changes on
      failure, and retain the primary source if rollback also fails.
    - Keep the operation manifest outside its own artifact map while recording target,
      mode, all semantic inputs, owned paths/digests, post-work, and generator identity.
    - _Requirements: 4.15-4.18, 5.16, 6.10, 6.13-6.17, 9.5, 9.8, 9.11, 12.11_
  - [ ] 6.5 Add the closed operation CLI and stable private diagnostics
    - Implement `dagger-rust-engine execute` with fixed request/schema/descriptor/project
      inputs, strict decoding, operation-specific validation, bounded sorted stderr,
      stable diagnostic codes, and no generic executable/argument or hidden production
      override.
    - Preserve typed filesystem, Cargo, formatter, schema, process, and source errors;
      identify only stable operation/path coordinates; redact credentials, headers,
      environment secrets, session data, and response/source contents.
    - _Requirements: 6.1, 6.2, 6.16, 12.1-12.9, 12.13-12.16_
  - [ ] 6.6 Property test: Property 9 — authored and generated ownership never cross
    - Generate at least 256 authored trees, compatible/stale prior manifests, unknown
      collisions, generated sets, and VCS files; compare admitted replacements and
      narrow edits to a manifest-only ownership reference model.
    - Test identifier: `property_09_authored_generated_ownership_never_cross`.
    - _Requirements: 4.13-4.16, 4.19, 4.20, 9.5_
  - [ ] 6.7 Property test: Property 11 — operation identities are complete and path-confined
    - Run at least 128 generated lexical/symlink filesystem trees and at least 256
      identity mutations; require exact identity retention/digest sensitivity and zero
      access outside the operation root for every rejected path.
    - Test identifier: `property_11_operation_identities_complete_path_confined`.
    - _Requirements: 5.8-5.16, 7.4, 7.6, 7.7, 13.20, 13.21_
  - [ ] 6.8 Property test: Property 13 — post-work is closed, bounded, and convergent
    - Generate at least 128 allowlisted/rejected post-work plans, argument mutations,
      output changes, failure points, and projection sequences; compare process events
      and fixed-point behavior to the closed two-pass model.
    - Test identifier: `property_13_post_work_closed_bounded_convergent`.
    - _Requirements: 6.8-6.12_
  - [ ] 6.9 Property test: Property 14 — generation is deterministic and failure-atomic
    - Inject at least 128 generated renderer, enumeration, format, post-work, manifest,
      flush, rename, removal, and rollback schedules; require identical successful
      candidates and byte-identical prior trees after every rejected run.
    - Test identifier: `property_14_generation_deterministic_failure_atomic`.
    - _Requirements: 6.13-6.17, 6.19, 12.11, 12.15_

- [ ] 7. Checkpoint: Cargo adoption and the private operation runner are green
  - Run formatting, locked engine-tool/codegen tests, Properties 7-9 and 11-14,
    metadata/TOML/VCS/diagnostic fixtures, cancellation and publication fault tests,
    warning-denied clippy/rustdoc, cargo-deny, and `rust-sdk-dev engine-unit`.
  - Require existing projects to remain semantically preserved, every failure to leave
    the initial tree unchanged, all child processes to be reaped, and no engine build to
    be required yet.

- [ ] 8. Package the Rust integration and wire built-in engine resolution
  - [ ] 8.1 Build acyclic, target-bound Rust SDK engine content
    - Add the Rust SDK content builder under `toolchains/engine-dev/build/sdk.go`; build
      `dagger-rust-engine` with Rust 1.97.1 from the locked workspace in a digest-pinned
      image and copy only the final executable into a fresh content root.
    - Package the explicit `runtime/` Go module, descriptor, client metadata, declared
      templates, optional non-secret seed, license, and payload asset manifest; exclude
      tests, targets, `.git`, credentials, completeness artifacts, and private source.
    - Hash payload assets excluding the two metadata files, embed that manifest digest
      in `engine-source.json`, then compute the complete OCI digest; bind OCI and
      descriptor digests separately to the engine image to avoid a hash cycle.
    - _Requirements: 2.5-2.8, 2.13, 2.14, 11.1-11.7_
  - [ ] 8.2 Add the thin module-backed Go ABI adapter foundation
    - Add `sdk/rust/runtime` with generated Dagger bindings, required non-nil
      `sdkSourceDir`, immutable adapter state, descriptor/client-metadata access, and
      fixed private container paths.
    - Keep the adapter declarative: it may build Dagger object graphs, run the packaged
      executable without a shell, and translate result objects, but may not parse
      schema, mutate Cargo, render Rust, infer ownership, select dependency policy, or
      manufacture evidence.
    - Omit `targetRuntime` because the installed SDK module owns `moduleRuntime`; omit
      `moduleTypes` because runtime empty-call registration is the one selected
      strategy; expose `AsModule` through the ordinary module-backed SDK path.
    - _Requirements: 7.10-7.13, 7.16, 7.17_
  - [ ] 8.3 Register Rust once in metadata, loader, distribution constants, and workspace mapping
    - Add canonical `rust` metadata and the Rust manifest-digest environment constant;
      load bare Rust through `loadBuiltinSDK`, pass the complete packaged source to the
      adapter, and fail with provenance context for absent/malformed content.
    - Reject `rust@<value>` before external fallback/network access, retain explicit
      immutable external refs through the ordinary external loader, and preserve both
      causes for genuinely unresolved SDKs.
    - Map the workspace-facing name to `dagger-rust-sdk` with persisted source `rust`,
      validate it as an installed `AsSDK` module, preserve idempotent reinstall and
      collision behavior, and rely on the running engine descriptor rather than adding
      a workspace provenance field.
    - _Requirements: 2.1-2.14, 3.1-3.3_
  - [ ] 8.4 Derive the complete packaged/security audit graph
    - Traverse publishable SDK, generator, engine tool, Go runtime adapter, packaged
      binaries, asset metadata, toolchain image, and dependency roots; require every
      reachable Rust subject in the locked cargo-deny and repository security inputs.
    - Assert that generated projects reference only public `dagger-sdk`, that private
      crates remain unpublished build inputs, and that a fork engine's immutable Git
      descriptor is preserved without a local checkout dependency.
    - _Requirements: 11.1-11.12, 11.24, 13.34, 13.37_
  - [ ] 8.5 Property test: Property 2 — deterministic Rust SDK resolution
    - Use Rust `proptest` to generate at least 256 bare, versioned, immutable external,
      mutable external, unknown, and ambiguous loader registries and canonical replay
      cases; execute those cases through focused Go loader model tests and compare the
      selected path, network events, and ordered causes to the reference precedence
      model.
    - Test identifier: `property_02_deterministic_rust_sdk_resolution`.
    - _Requirements: 2.1-2.4, 2.9-2.12_
  - [ ] 8.6 Property test: Property 3 — engine source provenance is complete and target-bound
    - Generate at least 256 descriptor/build/asset/target mutations; require complete
      compatible coordinates, digest sensitivity, acyclic asset closure, and rejection
      before SDK exposure for every absent or mismatched coordinate.
    - Test identifier: `property_03_engine_source_provenance_complete_target_bound`.
    - _Requirements: 2.5-2.8, 2.13, 2.14, 11.1-11.5_
  - [ ] 8.7 Property test: Property 4 — workspace installation is collision-safe and reversible
    - Use Rust `proptest` to generate at least 256 workspace SDK maps and install/
      reinstall/uninstall sequences as canonical replay cases; run the corresponding Go
      workspace model tests and compare the canonical Rust entry, no-op behavior,
      collision rejection, and ownership-only removal to an independent state machine.
    - Test identifier: `property_04_workspace_installation_collision_safe_reversible`.
    - _Requirements: 3.1-3.3, 3.14, 3.15_
  - [ ] 8.8 Property test: Property 23 — packaged assets and public dependencies form a closed graph
    - Generate at least 256 asset/dependency/publication graphs; require every runtime
      payload beneath the content digest, exactly one publishable crate, no private or
      checkout dependency in generated projects, and exact fork/canonical descriptors.
    - Test identifier: `property_23_packaged_assets_public_dependencies_closed_graph`.
    - _Requirements: 11.1-11.12_
  - [ ] 8.9 Property test: Property 25 — security audit roots cover the shipped graph
    - Generate at least 256 dependency and packaged-asset graphs with reachable,
      unreachable, duplicate, and omitted subjects; compare derived audit roots to an
      independent graph traversal and reject every missing locked security input.
    - Test identifier: `property_25_security_audit_roots_cover_shipped_graph`.
    - _Requirements: 11.24, 13.34, 13.37_

- [ ] 9. Checkpoint: built-in resolution and reusable engine content are green
  - Run formatting, locked Rust workspace tests, Properties 2-4/23/25, focused changed
    Go package tests, warning-denied clippy/rustdoc, cargo-deny, repository Rust security
    checks, `rust-sdk-dev engine-unit`, and `rust-sdk-dev engine-content`.
  - Reuse the single produced `RustEngineContent` object in the same top-level Dagger
    invocation for a focused exact-target `resolution` case; record its digest as
    evidence and prove canonical metadata, bare install, reinstall, shorthand
    rejection, packaged asset/descriptor integrity, and no repository-checkout
    dependency.

- [ ] 10. Wire Rust initialization, codegen, and client hooks through the packaged adapter
  - [ ] 10.1 Add one data-only adapter operation helper
    - Scope Workspace/ModuleSource directories through the existing engine objects,
      preserve normalized module/output subpaths, mount fixed request/schema/project/
      descriptor/executable inputs, and invoke only the closed `execute` subcommand.
    - Decode the canonical operation manifest/result, select the successful resulting
      directory, and map it to Changeset, GeneratedCode, or Directory without parsing
      schema/Cargo/source in Go; preserve the Rust diagnostic and Dagger exec source.
    - Enable privileged nesting only for an operation that actually needs nested engine
      access, and keep secret/session inputs out of request JSON and command arguments.
    - _Requirements: 5.8-5.13, 6.1-6.7, 7.1-7.8, 12.7-12.9_
  - [ ] 10.2 Implement Rust module initialization as one scoped SDK Changeset
    - Expose `InitModule(ws, name, path)` and declare no provisional Rust-specific
      arguments; rely on target engine argument decoding to reject every unknown key in
      sorted order rather than inventing an authoring choice before Feature 6.
    - Invoke the Cargo adoption/initialization plan against the engine-selected path,
      return only SDK-owned files, exclude engine workspace/module configs, preserve
      existing authored bytes, and leave automatic generation/`--no-generate` selection
      to the engine's existing scoped workflow.
    - Resolve nested workspace cwd to the same normalized module source and ensure a
      failure before Changeset application returns no mutation-capable result.
    - _Requirements: 3.4-3.13, 3.16, 4.1-4.20, 12.10_
  - [ ] 10.3 Implement module codegen and client generation surfaces
    - Expose Codegen with exact scoped ModuleSource and introspection file, run
      GenerateModule, and return the complete generated directory plus explicit
      generated/ignored path sets from the operation plan.
    - Expose GenerateClient with exact client-visible schema and requested output
      directory, run the baseline finite renderer, and return only that confined
      directory; read RequiredClientGenerationFiles from packaged canonical metadata.
    - Keep ClientInitializer absent until Feature 7 supplies its implementation and test
      that surface discovery reports the configured absence truthfully.
    - _Requirements: 6.3-6.7, 7.1-7.9_
  - [ ] 10.4 Verify module surface, clone, and attachment state before runtime
    - Exercise the ordinary module-backed SDK reflection/instantiation path and assert
      CodeGenerator, ModuleInitializer, ClientGenerator, and AsModule presence;
      RuntimeTarget, ClientInitializer, and ModuleTypes remain absent at this stage.
    - Clone two independently scoped ModuleSource configurations through existing
      `core/sdk/module.go` behavior, mutate each fixture state, and attach cache-backed
      results; require no cross-clone alias or foreign result retention.
    - _Requirements: 7.1-7.9, 7.11-7.17_
  - [ ] 10.5 Property test: Property 5 — initialization changes are confined and failure-atomic
    - Run at least 128 generated empty/existing workspace trees, module subpaths,
      generation modes, authored files, ownership states, and injected failure phases;
      compare the returned Changeset to a path-confined reference diff and require
      rejected initial trees to remain byte-identical.
    - Test identifier: `property_05_initialization_confined_failure_atomic`.
    - _Requirements: 3.4-3.10, 3.13, 4.13-4.18, 12.10_
  - [ ] 10.6 Property test: Property 6 — initialization argument and working-directory semantics
    - Generate at least 256 empty valid Rust argument sets, unknown-key/value maps, and
      nested workspace cwd layouts; require exact empty decoding, stable sorted rejection
      for every unknown argument, no generator event on rejection, and the same scoped
      module identity from every equivalent cwd.
    - Test identifier: `property_06_initialization_arguments_working_directory_semantics`.
    - _Requirements: 3.11, 3.12, 3.16_
  - [ ] 10.7 Property test: Property 16 — cloned SDK state is isolated
    - Use Rust `proptest` to generate at least 128 canonical clone/configuration/result-
      attachment schedules with two or more ModuleSource identities; replay them through
      focused Go module-state tests, compare to a deep-copy reference model, and reject
      every alias or foreign attachment.
    - Test identifier: `property_16_cloned_sdk_state_isolated`.
    - _Requirements: 7.14, 7.15_

- [ ] 11. Build a reproducible, credential-safe Rust runtime container
  - [ ] 11.1 Derive checked versus legacy generation mode from module configuration
    - Make `introspectionJson` optional on ModuleRuntime so the target engine may omit it
      only for current committed-generation configuration; nil selects checked mode and
      a present file selects private legacy regeneration.
    - In checked mode, verify the committed operation manifest/artifact set without a
      schema request or generation; return path-specific `dagger generate` repairs for
      missing, stale, or unknown-owned files.
    - In legacy mode, run GenerateModule only in the private container filesystem,
      discard partial output on failure, and never return generated changes to the host.
    - _Requirements: 9.1-9.12_
  - [ ] 11.2 Verify the exact runtime Cargo project, lockfile, toolchain, and binary target
    - Add `verify-runtime` to promote only a valid discovered project into
      `RuntimeCargoProject`, require current compatible Cargo.lock and generated
      manifest, resolve the exact project/enclosing/default toolchain, and reject
      below-MSRV or mutable toolchain selection.
    - Produce the complete shell-free Cargo argument vector with fixed manifest,
      package, `dagger-module` binary, release, `--locked`, and SDK-owned target paths;
      do not allow user selection of an executable, target directory, args, or workdir.
    - Emit a canonical pre-build plan containing no secret, arbitrary environment, or
      absolute host path.
    - _Requirements: 8.10-8.19, 9.1-9.5, 12.3-12.6_
  - [ ] 11.3 Finalize runtime provenance only after the selected binary exists
    - Add the closed `finalize-runtime` subcommand accepting only a verified plan and
      the fixed engine-selected binary; revalidate that path, hash post-strip bytes,
      and emit canonical RuntimeProvenance without permitting coordinate amendments.
    - Include exact engine descriptor, toolchain, digest-pinned base, lockfile, scoped
      module source, operation manifest, final binary, target, and runtime mode
      identities; reject absent/mismatched input before final image construction.
    - _Requirements: 8.1-8.9, 12.5, 12.6_
  - [ ] 11.4 Compose build and clean runtime containers in the Go adapter
    - Mount registry, Git, and compiler caches only at fixed build paths with non-secret
      compatibility keys; pass credentials only through Dagger secret-safe channels and
      keep them out of args, serialized plans, provenance, and diagnostic context.
    - Execute the verified Cargo vector without a shell, strip only through a pinned
      approved tool, finalize provenance, then create a fresh digest-pinned runtime base
      containing only the binary, provenance, and declared runtime necessities.
    - Set `core.RuntimeWorkdirPath`, clear inherited default args, set the binary as
      entrypoint, and prove source, Cargo homes, target directories, SDK mounts, build
      sockets, caches, and credentials are absent from the final image.
    - _Requirements: 8.1, 8.17-8.20, 11.13-11.23_
  - [ ] 11.5 Add bounded credential-safe dependency/build failure handling
    - Redact URL userinfo/query credentials, authorization/header values, known secret
      bytes, session tokens, and secret environment values before a Cargo/exec source
      may enter a diagnostic; cap captured stdout/stderr deterministically.
    - Test registry/Git dependency failures, compiler failures, cancellation, and
      redaction-failure fallback without retaining cache, partial runtime, or process.
    - _Requirements: 11.13-11.23, 12.5-12.7, 12.13-12.15_
  - [ ] 11.6 Property test: Property 17 — runtime provenance is complete and secret-free
    - Generate at least 256 successful provenance inputs plus single/multiple coordinate
      omissions/mutations and secret-bearing ambient state; require complete exact
      records, digest sensitivity, and zero forbidden data.
    - Test identifier: `property_17_runtime_provenance_complete_secret_free`.
    - _Requirements: 8.1-8.9, 11.13-11.16_
  - [ ] 11.7 Property test: Property 18 — runtime toolchain, lockfile, and target selection is reproducible
    - Generate at least 256 Cargo/toolchain/lock/target configurations; compare selected
      exact toolchain and argument vector to a precedence/reference model, reject every
      stale/mutable/below-MSRV/arbitrary target, and retain checked state unchanged.
    - Test identifier: `property_18_runtime_toolchain_lock_target_reproducible`.
    - _Requirements: 8.10-8.19_
  - [ ] 11.8 Property test: Property 19 — equivalent runtime inputs produce equivalent construction
    - Permute at least 256 equal semantic provenance maps, source enumeration orders,
      and ambient host states; require identical ordered container operations, mounts,
      arguments, entrypoint, workdir, and non-secret cache keys.
    - Test identifier: `property_19_equivalent_runtime_inputs_equivalent_construction`.
    - _Requirements: 8.20, 11.16_
  - [ ] 11.9 Property test: Property 20 — generated-file mode is an explicit state machine
    - Generate at least 256 current/legacy configs and artifact states; compare mode,
      schema/generation events, repair diagnostics, private discard, and host writes to
      an explicit two-state reference model.
    - Test identifier: `property_20_generated_file_mode_explicit_state_machine`.
    - _Requirements: 9.1-9.12_
  - [ ] 11.10 Property test: Property 24 — build credentials and caches cannot cross the runtime boundary
    - Run at least 128 generated secret/cache/mount/build-result combinations, including
      failure output containing every secret; inspect generated files, plans,
      provenance, keys, diagnostics, and final runtime filesystem for non-interference.
    - Test identifier: `property_24_build_credentials_caches_cannot_cross_runtime`.
    - _Requirements: 11.13-11.23_

- [ ] 12. Checkpoint: initialization, codegen, and reproducible runtime construction are green
  - Run formatting, locked workspace/adapter tests, Properties 5-6/16-20/24, changed Go
    package tests, warning-denied clippy/rustdoc, cargo-deny, Rust security checks,
    `engine-unit`, one shared `RustEngineContent` object, and focused exact-target
    `init-empty`, `init-existing`, `init-no-generate`, `operations`, `runtime-checked`,
    and `runtime-legacy` cases.
  - Require every case to consume the same in-DAG content object and report its digest,
    scoped initialization/codegen changes, exact dependency/toolchain/lock behavior,
    clean final images, and no leaked secret, cache, or source material.

- [ ] 13. Execute the private module protocol boundary and close cross-layer failures
  - [ ] 13.1 Generate only the fixed private protocol probe
    - Commit canonical fixture TypeDef JSON and digest for one object and one
      zero-argument scalar function; accept exactly that document and reject every other
      object/function/signature without defining a public macro, annotation, source
      parser, registry, state model, or arbitrary dispatch branch.
    - Emit an edition-2024 Tokio current-thread entrypoint with durable target/probe/
      generator provenance but no specification feature labels in generated comments.
    - _Requirements: 10.3-10.13_
  - [ ] 13.2 Connect and execute registration/invocation through public Rust SDK paths
    - Use `dagger_sdk::connect()` and Feature 2/3 session/transport/error behavior;
      obtain current FunctionCall through generated bindings and branch on the engine
      function name.
    - For an empty name, construct the exact fixed Module/TypeDef and serve registration;
      for the one probe name, report canonical JSON through FunctionCall.returnValue;
      return typed errors for every other name/context and close the client explicitly.
    - Preserve query/result sources and precedence over a later close failure without
      rendering session metadata, parent JSON, response bodies, or secrets.
    - _Requirements: 10.1-10.13, 12.6-12.9_
  - [ ] 13.3 Prove per-call runtime isolation and cancellation cleanup
    - Invoke one RuntimeContainer through overlapping ModuleRuntime.Call executions with
      distinct execution metadata, call IDs, filesystem writes, success/failure, and
      cancellation barriers; assert that target engine cloning isolates every call.
    - Ensure cancelled Rust/Cargo/probe processes are terminated and reaped and cannot
      publish a result or contaminate another call.
    - _Requirements: 10.14, 12.12, 12.13_
  - [ ] 13.4 Close truthful engine surface and diagnostic taxonomy tests
    - Re-run module reflection after runtime implementation and require exactly the
      selected surfaces: ModuleInitializer, CodeGenerator, ClientGenerator, Runtime,
      ModuleRuntime, and AsModule present; ClientInitializer, RuntimeTarget, and
      ModuleTypes absent; exactly one runtime registration strategy.
    - Map every requirements error condition to one stable Rust/private or engine
      diagnostic, retain sources, operation/path coordinates, ordering, redaction, and
      bounded output; prohibit production panic, unchecked unwrap, and unsafe code.
    - _Requirements: 7.1-7.17, 12.1-12.16_
  - [ ] 13.5 Property test: Property 15 — engine capability surfaces report only implemented hooks
    - Use Rust `proptest` to generate at least 256 canonical adapter/reflection and
      callable/placeholder combinations; replay them through focused Go surface-
      detection tests and compare discovered surfaces, the single registration
      strategy, and exact returned results to a presence/reference model.
    - Test identifier: `property_15_engine_surfaces_report_only_implemented_hooks`.
    - _Requirements: 7.1-7.13, 7.16, 7.17_
  - [ ] 13.6 Property test: Property 21 — protocol branch and result behavior follows call context
    - Generate at least 256 valid/malformed session and call contexts, empty/fixed/
      unknown names, result/close failures, and values; compare branch events, module
      identity, result, and typed source precedence to the private probe model.
    - Test identifier: `property_21_protocol_branch_result_follows_call_context`.
    - _Requirements: 10.1-10.13_
  - [ ] 13.7 Property test: Property 22 — concurrent runtime calls remain isolated
    - Run at least 128 generated concurrent call schedules with deterministic barriers;
      require each call to observe only its own metadata/filesystem/result/cancellation
      and all started processes to be reaped.
    - Test identifier: `property_22_concurrent_runtime_calls_remain_isolated`.
    - _Requirements: 10.14, 12.13_
  - [ ] 13.8 Property test: Property 26 — diagnostics have a stable typed taxonomy
    - Generate at least 256 failures across resolution, target/schema, Cargo project/
      dependency, ownership/drift, toolchain/build, runtime/protocol, path, and
      redaction classes; compare variant/coordinate/source/order/rendering to a total
      reference table and require return rather than panic.
    - Test identifier: `property_26_diagnostics_stable_typed_taxonomy`.
    - _Requirements: 12.1-12.9, 12.14-12.16_
  - [ ] 13.9 Property test: Property 27 — rejection and cancellation expose no partial result
    - Inject at least 128 generated initialization, generation, publication, legacy
      regeneration, runtime build, registration, process, and cancellation failure
      points; assert no partial Changeset/artifact/runtime/result/process and exact prior
      state preservation.
    - Test identifier: `property_27_rejection_cancellation_no_partial_result`.
    - _Requirements: 4.17, 4.18, 6.16, 9.8, 12.10-12.13_

- [ ] 14. Bind exact engine observations into the completeness contract
  - [ ] 14.1 Finalize closed capability-to-implementation/evidence mappings
    - Join the exact checked target, 31 existing rows, 22 policy rows, Go/Rust reviewed
      equivalences, packaged assets, operation manifests, hook outputs, and delegated
      sibling boundaries into `engine-integration-mappings.json`.
    - Require one implementation subject, required evidence-domain set, and allowed
      terminal classification per capability; reject missing/extra/duplicate/name-only/
      wrong-owner/drifted mappings and prohibit hook-to-content substitution.
    - _Requirements: 1.1-1.12, 13.24-13.29_
  - [ ] 14.2 Assemble target-bound integration manifests and observations
    - Generate canonical engine-integration manifest/report artifacts containing scope,
      target, descriptor, schema, SDK dependency, Rust toolchain, packaged OCI asset,
      operation input/output, runtime provenance, case result, evidence, and exact
      proved Capability IDs.
    - Reject skipped, stale, failed, wrong-engine/version/schema/source/toolchain/asset,
      sibling, and out-of-domain observations atomically before calling Feature 1's
      transition API.
    - _Requirements: 13.24-13.27_
  - [ ] 14.3 Derive honest statuses and reports through Feature 1 only
    - Separate engine-hook, checked-generation, legacy-generation, protocol, and
      delegated-content evidence IDs; admit only capability-local domains and let the
      existing transition policy derive every status.
    - Render remaining blocker identities without presentation relabeling and ensure a
      source/build/test/report alone cannot move a row lacking all declared evidence.
    - _Requirements: 1.6, 1.10-1.12, 9.12, 13.28, 13.29_
  - [ ] 14.4 Property test: Property 28 — evidence admission is exact-target and capability-local
    - Generate at least 256 valid observations and target/version/schema/source/
      toolchain/asset/case/capability mutations; compare all-or-nothing admission and
      unchanged rejection state to an independent subject/domain set model.
    - Test identifier: `property_28_evidence_admission_exact_target_capability_local`.
    - _Requirements: 13.24-13.27_
  - [ ] 14.5 Property test: Property 29 — completeness reports are derived rather than presented
    - Generate at least 256 prior ledgers, admitted evidence sets, missing domains, hook/
      content overlaps, and checked/legacy observations; require Feature 1 transition
      equality and exact remaining blocker/evidence-domain rendering.
    - Test identifier: `property_29_completeness_reports_derived_not_presented`.
    - _Requirements: 1.6, 1.10-1.12, 9.12, 13.28, 13.29_

- [ ] 15. Checkpoint: real protocol execution and evidence admission are green
  - Run formatting, locked Rust workspace tests, Properties 15 and 21-22/26-29, changed
    Go package tests, warning-denied clippy/rustdoc, cargo-deny, Rust security checks,
    `engine-unit`, one shared `RustEngineContent` object, focused runtime/protocol
    registration and invocation cases, and the negative proof that `engine-evidence`
    refuses an incomplete case matrix.
  - Require both probe branches to pass through ModuleRuntime.Call, overlapping calls to
    remain isolated, every rejection to retain prior state, and no evidence/status to be
    committed before the complete exact-target matrix passes.

- [ ] 16. Complete the focused exact-target engine matrix and evidence workflow
  - [ ] 16.1 Finish the four-stage Rust SDK engine development workflow
    - Expose independently callable `engine-unit`, `engine-content`,
      `engine-integration --cases <closed-names>`, and `engine-evidence` functions with
      generated Dagger bindings and focused help/documentation; a singleton case remains
      the ordinary development path.
    - Return `RustEngineContent` with the actual OCI root and target-bound engine inputs,
      key it by target/descriptor/source/lock/adapter/dependency/image/platform
      identities, and pass that object—not only its digest—to parallel case branches
      inside one top-level Dagger DAG.
    - Record the digest for evidence and cache identity, but never assume it can recover
      bytes from another runner; prove test-only changes do not rebuild the Rust
      toolchain/content layer.
    - Refuse unknown case names and require evidence to consume the complete named case
      set rather than treating an omitted/skipped case as success.
    - _Requirements: 13.1, 13.24-13.27, 13.36_
  - [ ] 16.2 Implement the positive exact-target cases
    - Against revision `25300124ca110612edc09c43f89cb5fad6028170`, verify canonical
      Rust metadata once, `dagger sdk install rust`, empty/existing project init,
      unrelated-file preservation, scoped automatic generation, `--no-generate`, all
      four real selectors, finite client/entrypoint renderers, checked and legacy
      RuntimeContainers, and protocol registration/invocation.
    - Record deterministic case/result/provenance subjects without claiming Feature 6
      arbitrary dispatch or Feature 7 complete client content.
    - _Requirements: 13.1-13.15_
  - [ ] 16.3 Implement the negative exact-target boundary cases
    - Cover unsupported Rust shorthand without external access, missing committed
      generation, stale lockfile, incompatible toolchain, lexical output escape,
      symlink escape, unknown ownership collision, and injected credential-bearing
      build/dependency failures.
    - Assert typed actionable diagnostics, redaction, no external writes, no host source
      mutation, no partial runtime/result, and process cleanup for every case.
    - _Requirements: 13.16-13.23_
  - [ ] 16.4 Admit and regenerate exact-target engine evidence
    - After all cases pass against one shared content object and matching digest,
      assemble the observation with
      exact engine/version/schema/SDK source/toolchain/assets and capability IDs, run
      Feature 1 admission/transitions, and regenerate the committed integration report.
    - Add negative evidence fixtures for skipped/stale/failed/wrong-target and sibling
      claims; require report counts/statuses to be derived and unchanged on rejection.
    - _Requirements: 13.24-13.29_
  - [ ] 16.5 Add repository check registration for focused and final gates
    - Register cheap engine-unit checks separately from cached content and per-case
      integration checks; make the final Rust security/evidence check depend on the
      complete matrix rather than serially rebuilding content for each observation.
    - Preserve existing security workflow semantics and pinned actions/images; include
      every new private crate, packaged binary, Go adapter package, and committed
      evidence artifact in the appropriate gate.
    - _Requirements: 13.31-13.37_

- [ ] 17. Stabilize documentation, security policy, and committed derived outputs
  - [ ] 17.1 Document the durable engine-integration contracts
    - Complete `//!` boundary/invariant docs and caller-relevant `///` guarantees across
      codegen engine modules, private runner, descriptor/project/publication/runtime/
      protocol layers, Go adapter, engine loader/builder, completeness integration, and
      development workflow.
    - Explain why packaged metadata is acyclic, why the Go layer is only an ABI adapter,
      why manifests—not names—own generated files, why provenance is two-phase, why the
      runtime is clean, and why hook evidence cannot close delegated content.
    - Keep obvious narration and specification feature/task/property labels out of
      production/generated comments.
    - _Requirements: 4.20, 6.19, 10.10-10.13, 11.1-11.24, 12.16_
  - [ ] 17.2 Add the reproducible maintainer workflow requested for engine integration
    - Document prerequisites, target/descriptor refresh, fork versus canonical
      dependency descriptors, engine-unit/content/per-case/evidence commands, cache
      identities, local failure triage, generated ownership repair, protocol probe,
      security review, evidence interpretation, and clean-output verification.
    - Include the exact steps for reproducing the packaged content and matrix without
      requiring ambient local crate paths or exposing credentials; distinguish ordinary
      checkpoint work from the one final complete matrix.
    - _Requirements: 2.5-2.8, 4.20, 8.2-8.9, 11.8-11.23, 13.24-13.37_
  - [ ] 17.3 Publish only manifest-owned generated and evidence artifacts
    - Run scoped Rust SDK generation/update, inspect the exact declared changeset, commit
      operation/asset/integration manifests, client metadata, generated adapter/runtime
      bindings, exact-target observations, and derived reports, and then require a
      second check/evidence render to be byte-clean.
    - Remove or replace only paths authorized by compatible prior manifests; keep
      caller-authored Cargo/source/VCS files and unrelated repository outputs unchanged.
    - _Requirements: 4.13-4.20, 6.13-6.17, 9.1-9.12, 13.29, 13.30_
  - [ ] 17.4 Fence final dependency, release-note, and public-surface policy
    - Complete cargo-deny and Rust security coverage for the locked graph, immutable
      Git/registry sources, credential redaction, confined paths, subprocesses, cache/
      runtime exclusion, generated serde/docs, and unsafe/panic/unwrap rules.
    - Add the appropriate Changie fragment for built-in Rust engine SDK integration
      without claiming Feature 6 authoring, Feature 7 complete clients, stable crate
      publication, or a wider platform/release matrix.
    - Confirm the public `dagger-sdk` API/dependency snapshot changes only where the
      private probe/runtime needs already-approved Feature 2-4 surfaces.
    - _Requirements: 10.10-10.13, 11.6-11.24, 12.14-12.16, 13.34, 13.37_

- [ ] 18. Final checkpoint: Rust SDK engine integration is exact-target complete
  - Run `cargo fmt --all --check`, locked workspace check/test, warning-denied clippy
    and rustdoc, cargo-deny, no-default-features public SDK checks, all changed Go package
    tests, generated-output checks, `engine-unit`, one shared `RustEngineContent` object,
    every positive/negative `engine-integration` case fanned out from that object,
    `engine-evidence`, scoped repository generation, and repository Rust security checks.
  - Require all 30 property identifiers, all 237 acceptance criteria, exact 31/22 scope
    closure, four operation selectors, both runtime modes, both private probe branches,
    concurrency/cancellation/redaction boundaries, exact-target evidence admission,
    derived blocker reporting, and a clean worktree after regeneration.
  - Any capability lacking its own declared exact-target evidence domain remains
    honestly `Missing` or `Partial`; a green build, registered hook, source presence, or
    sibling implementation cannot close it.

## Task Dependency Graph

```json
{
  "1": [],
  "2": ["1"],
  "3": ["2"],
  "4": ["3"],
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
  "18": ["17"]
}
```

The seven checkpoints are strict review boundaries. Pure models and renderers precede
Cargo/project I/O; project planning precedes publication; a tested private runner
precedes packaging; packaged resolution precedes engine hooks; hooks precede runtime;
runtime precedes protocol/evidence; exact-target evidence precedes committed status and
documentation closure.

## Notes

- Every property-test subtask is mandatory. Pure/reference-model properties run at
  least 256 cases; filesystem, subprocess, and concurrency properties run at least 128;
  all remain above the 100-case floor. Fixed target inventories and engine cases are
  exhausted in addition to generated cases.
- Stable `property_NN_*` test identifiers and requirement citations provide source-to-
  spec traceability. Per the approved documentation policy, production/generated
  comments and invariant comments do not name specification features, task numbers, or
  planning phases.
- Checkpoint 2 deliberately removes the eager engine dependency. Checkpoints 2, 4, and
  7 are engine-free; Checkpoint 9 creates the first reusable `RustEngineContent` object.
  Later checkpoint cases share the actual object within one top-level DAG and record its
  digest as evidence; cross-runner cache availability is never a correctness premise.
- Checkpoints are the preferred commit and pull-request boundaries. A checkpoint may be
  split only when its independently compiling dependency layer is itself reviewable;
  do not stack unverified engine changes merely to avoid a long final pull request.
- The Go adapter is an engine ABI shim, not the definitive Go SDK and not an authority
  for Rust API shape. Rust owns Cargo, schema, rendering, ownership, diagnostics,
  provenance, security, and runtime verification policy.
- The private probe proves a real nested-session registration/invocation boundary only.
  It must not grow a provisional public authoring model; Feature 6 replaces its renderer
  behind the stable runtime seam.
- The client baseline proves lossless engine dispatch only. Feature 7 owns complete
  standalone client content and may extend the packaged required-host-file metadata.
- The integration report is derived evidence, not presentation. Do not optimize the
  `Implemented` count or relabel remaining blockers; each status moves only through
  Feature 1's admitted capability-local evidence.
