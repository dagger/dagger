# Implementation Plan

- [x] 1. Bootstrap the internal completeness crate and its durable model
  - [x] 1.1 Register the internal crate and locked dependencies
    - Add `crates/dagger-sdk-completeness` to the Rust workspace with a library and thin
      binary, `publish = false`, inherited workspace lints, and no dependency on
      `dagger-sdk`, `dagger-codegen`, or `dagger-bootstrap`.
    - Add `semver` to the crate and `proptest` to the workspace development dependencies;
      preserve the locked dependency graph and `unsafe_code = "deny"` policy.
    - _Requirements: 3.1, 8.10, 11.1, 11.4_
  - [x] 1.2 Implement validated scalar and identity types
    - Add strict newtypes for commit SHAs, digests, repository identities, relative paths,
      source locators, versions, target digests, capability/check/evidence/rule IDs,
      platforms, and Feature 2–9 ownership.
    - Reject unknown values, malformed revisions and digests, absolute or escaping paths,
      unsupported feature owners, and invalid Dagger/Rust version spellings without
      panicking.
    - _Requirements: 1.2, 5.1, 7.2, 7.3, 10.4–10.11, 11.7_
  - [x] 1.3 Implement the complete durable model surface
    - Add `TargetDescriptor`, authority, source-item, schema, capability, classification,
      harness, evidence, transition, compatibility, report, and extension-scenario types.
    - Apply `serde(deny_unknown_fields)`, exact policy enum spellings, absent optional
      fields instead of `null`, and validated constructors before values enter the core.
    - _Requirements: 1.1, 2.2, 3.2–3.10, 4.1–4.9, 5.4, 6.1, 7.1, 9.1, 12.3_
  - [x] 1.4 Implement stable diagnostics and accumulating validation
    - Add every external diagnostic code from the requirements as a closed Rust enum,
      `ContractDiagnostic`, deterministic ordering, independent-error accumulation, and
      redacted `ToolError` handling with exit status `2`.
    - Add fixed tests for every diagnostic and durable enum serialization spelling.
    - _Requirements: 1.2–1.5, 1.11, 2.5, 2.6, 2.9, 2.11, 3.12, 4.10, 5.6–5.9, 6.9, 6.10, 6.14, 7.2–7.4, 7.7, 8.3–8.5, 8.13, 9.3–9.5, 11.7, 12.8, 12.9, 12.14, 12.16_
  - [x] 1.5 Add shared property-test generators and regression persistence
    - Build valid-first `proptest` strategies for all durable models plus targeted single-
      and multi-condition mutations, and configure at least 256 cases with a checked-in
      deterministic regression corpus.
    - _Requirements: 3.13, 8.10_

- [x] 2. Implement canonical artifacts and immutable target validation
  - [x] 2.1 Implement canonical JSON and domain-separated digests
    - Produce UTF-8, LF, two-space-indented JSON with recursively ordered keys, sorted
      set-like arrays, one trailing newline, canonical relative paths, and distinct SHA-256
      domains for target, source, capability, artifact, rule, and compatibility digests.
    - Provide deserialize/validate/serialize round-trips and byte comparison helpers for
      checked-in artifacts.
    - _Requirements: 1.7, 3.13, 5.3, 5.5, 8.10, 11.8_
  - [x] 2.2 Implement target identity validation
    - Validate all Target Descriptor fields, exact Dagger/schema/Go/harness/Rust identities,
      workspace metadata, schema and source digests, the engine-selected Go literal, and
      optional immutable Go-label resolution evidence.
    - Keep the misleading `v0.21.7` comment outside source identity and require the harness
      CLI/engine identity to match the selected target.
    - _Requirements: 1.1–1.11, 11.7, 12.1, 12.5, 12.14_
  - [x] 2.3 Property test: Property 1 — canonical artifact determinism
    - Implement a `proptest` with at least 256 cases over valid durable values and
      permutations of unordered inputs; compare bytes, domain-separated digests, and
      round-tripped values.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 1: canonical artifact determinism`
    - _Requirements: 3.13, 8.10_
  - [x] 2.4 Property test: Property 2 — immutable target identity
    - Implement a valid-reference-model `proptest` with at least 256 cases, then mutate
      required fields, revisions, source/schema digests, labels, workspace metadata, and
      harness target identity independently and in combination.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 2: immutable target identity`
    - _Requirements: 1.1–1.11, 11.7, 12.1, 12.5, 12.14_

- [x] 3. Checkpoint: foundations compile and pure tests are green
  - Run formatting, locked workspace checking, the completeness crate unit tests, and
    clippy for the implemented targets; require no warnings, panics, uncommitted generated
    output, or dependency from the contract crate to a public Rust SDK crate.

- [x] 4. Implement authority loading, containment, and source coverage primitives
  - [x] 4.1 Implement the in-memory `SourceBundle` and authority validator
    - Resolve every authority class exactly once, enforce unique IDs and target-matching
      revisions, expand non-empty includes, validate exact exclusions and rationales, and
      recompute normalized source digests independent of enumeration order.
    - _Requirements: 2.1–2.6, 2.10, 2.11_
  - [x] 4.2 Implement secure repository source loading
    - Validate the target and registry before opening selected paths, canonicalize every
      path beneath its registered repository root, reject traversal and symlink escape,
      and pass exact bytes to extractors without allowing extractor filesystem access.
    - Keep the normal-verification loader network-free and separate immutable-transition
      retrieval behind an explicit adapter.
    - _Requirements: 1.8, 2.4–2.6, 7.2–7.4, 8.1_
  - [x] 4.3 Implement the common source-item and coverage model
    - Represent active, deprecated, skipped, removed, and harness-self items uniformly;
      require each selected item to be a primary capability source, reference anchor, or
      exact reviewed exclusion, and reject uncovered items and stale exclusions.
    - _Requirements: 2.3, 2.7–2.10, 4.4–4.9, 4.11, 4.12_
  - [x] 4.4 Property test: Property 3 — authority registry totality and containment
    - Implement a reference-model `proptest` with at least 256 generated registries,
      bundles, path trees, include/exclude sets, and target identities, including traversal
      and symlink-containment fixture mutations.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 3: authority registry totality and containment`
    - _Requirements: 2.1–2.6, 2.10, 2.11_

- [x] 5. Implement independent authority extractors
  - [x] 5.1 Implement strict engine-schema extraction
    - Add independent introspection response types matching the canonical query, including
      schema version, deprecated elements, full nested TypeRefs, relationships, defaults,
      descriptions, directives, interfaces, possible types, and enum values.
    - Emit atomic, deterministically ordered schema SourceItems; apply public/meta-type
      exclusions only through registered policy and reject dangling relationships.
    - _Requirements: 1.6, 1.7, 3.1–3.13_
  - [x] 5.2 Property test: Property 4 — complete schema extraction
    - Implement a reference-model `proptest` with at least 256 generated valid
      introspection graphs, ordering permutations, deprecated items, nested list/nullability
      shapes, and targeted dangling or unknown relationship mutations.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 4: complete schema extraction`
    - _Requirements: 3.1–3.12_
  - [x] 5.3 Implement the standard-library Go source-item helper
    - Under `completeness/extractors/go`, use `go/ast`, `go/parser`, `go/token`, and
      `go/format` to emit exported declarations and signatures, methods, type parameters,
      constants, deprecation state, test/subtest identities, skipped state, normalized AST
      fingerprints, locators, and the evaluated `goSDKLibVersion` literal.
    - Preserve dynamic test tables under stable parent identities and add no Go module
      dependency.
    - _Requirements: 1.3–1.5, 2.7–2.9, 4.1–4.8_
  - [x] 5.4 Implement and test the Rust Go-helper adapter
    - Strictly validate the helper format, reject unknown or malformed output, and
      canonicalize helper SourceItems before coverage resolution.
    - Add fixed fixtures proving extraction uses the Go commit literal rather than its
      adjacent version comment and preserves active, skipped, and removed-test state.
    - _Requirements: 1.3–1.5, 2.7–2.9, 4.1–4.8_
  - [x] 5.5 Implement pinned `sdk-sdk` source extraction
    - Add a string/comment-aware Dang scanner for public `@check` functions with balanced
      signature/body fingerprinting and explicit failure on unsupported syntax.
    - Cross-check scanner identities against pinned `dagger check --list` refresh data and
      preserve the public `check`, `SdkTarget`, and `mod-test` integration boundaries.
    - _Requirements: 2.10, 2.11, 8.13, 12.1–12.3, 12.10, 12.12, 12.15_
  - [x] 5.6 Implement engine SDK, codegen, test-handoff, and Rust-policy extraction
    - Extract stable source items for selected engine SDK/generator contracts, active Go
      tests, every `future/sdk-tests.md` handoff row and recovery commit, and every approved
      Rust policy clause without treating removed/skipped tests as passing evidence.
    - _Requirements: 2.7–2.9, 4.2–4.9, 10.4–10.11_

- [x] 6. Build the canonical inventory and resolve authority precedence
  - [x] 6.1 Implement stable capability identity and fingerprinting
    - Derive schema IDs from canonical coordinates and reviewed behavioural/policy IDs from
      authority plus semantic coordinate, with reversible percent-encoding and no source
      line, version label, or implementation path in identity.
    - Hash normalized semantic signatures, preserve identity across incidental movement,
      and reject one ID with competing signatures.
    - _Requirements: 4.10, 5.1–5.3_
  - [x] 6.2 Implement exhaustive inventory construction
    - Combine schema extraction with reviewed behavioural and policy definitions; map every
      selected SourceItem to atomic capabilities or registered exclusions and preserve all
      non-redundant reference anchors.
    - Exclude generated bindings only through schema-backed policy and retain behaviours
      omitted by the common harness when another authority declares them.
    - _Requirements: 2.3, 2.7–2.9, 3.2–3.12, 4.1–4.9, 4.11, 4.12_
  - [x] 6.3 Implement peer-authority overlap resolution
    - For a common lifecycle capability, make a target-compatible harness assertion the
      primary semantic definition and retain Go as reference evidence; reject target
      incompatibility and competing primary definitions instead of choosing silently.
    - _Requirements: 4.10, 12.13, 12.14_
  - [x] 6.4 Property test: Property 5 — exhaustive source-item coverage
    - Implement a reference-model `proptest` with at least 256 generated source inventories,
      definition/anchor mappings, exact exclusions, tests, harness assertions, policy items,
      and schema-backed generated-binding exclusions.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 5: exhaustive source-item coverage`
    - _Requirements: 2.7–2.9, 4.1–4.9, 4.11, 4.12_
  - [x] 6.5 Property test: Property 6 — stable capability identity and semantic fingerprinting
    - Implement a `proptest` with at least 256 cases that independently varies incidental
      locations/order and semantic shape, and generates colliding identities with equal and
      unequal signatures.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 6: stable capability identity and semantic fingerprinting`
    - _Requirements: 4.10, 5.1–5.3_
  - [x] 6.6 Property test: Property 12 — authority precedence without silent conflict
    - Implement a reference-model `proptest` with at least 256 overlapping Go/harness
      authority pairs, including target-compatible, incompatible, reference-only, and
      competing-signature cases.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 12: authority precedence without silent conflict`
    - _Requirements: 12.13, 12.14_

- [x] 7. Checkpoint: all authority extraction and inventory tests are green
  - Run the Go helper tests and formatting, completeness crate unit/property tests, locked
    workspace checks, and clippy; require deterministic output from repeated fixture
    extraction and zero uncovered fixture SourceItems.

- [ ] 8. Implement classification, evidence, ownership, and child-spec traceability
  - [ ] 8.1 Implement exact classification-rule expansion
    - Add the restricted conjunction-only selector language, expected ordered ID set or
      digest, exact-ID overrides, overlap/staleness detection, and one resolved row per
      Active_Capability.
    - Reject regex/scripts, negative or output-dependent predicates, new/lost matches,
      stale overrides, duplicates, and unclassified capabilities.
    - _Requirements: 5.4–5.9_
  - [ ] 8.2 Implement the five-state status-entry validator
    - Enforce the exact gap, owner, implementation, verification, and reviewed-decision
      evidence shapes for `Missing`, `Partial`, `Implemented`, `Idiomatic_Equivalent`, and
      `Inapplicable`; reject planning statuses and completion by source/docs alone.
    - _Requirements: 6.1–6.10, 10.1–10.3_
  - [ ] 8.3 Implement evidence provenance and locator auditing
    - Validate kind-specific fields, registered repositories, immutable revisions,
      contained paths, exact source locators, claims, argv-only commands, repository-relative
      working directories, environment allowlists, outcomes, targets, platforms, and exact
      proved Capability_ID sets.
    - Expand shared evidence back to each ledger row and keep skipped, removed, failed,
      documentation, issue, PR, and harness-self records ineligible as passing verification.
    - _Requirements: 2.9, 6.10–6.12, 7.1–7.10_
  - [ ] 8.4 Implement deterministic blocking-work ownership
    - Route every `Missing` and `Partial` capability to exactly one Feature 2–9 domain,
      preserving `initClient` and standalone/dependency client-generation gaps under
      Feature 7 and unverified platform obligations under Feature 8.
    - _Requirements: 10.4–10.11, 10.15, 10.16_
  - [ ] 8.5 Implement downstream traceability validation
    - Validate child-spec Capability_ID declarations and candidate status changes against
      the current inventory; require status-appropriate implementation and verification
      evidence in the same candidate contract.
    - _Requirements: 10.12, 10.13_
  - [ ] 8.6 Property test: Property 7 — exact classification-rule expansion
    - Implement a simple-reference-resolver `proptest` with at least 256 generated
      inventories, rules, expected sets/digests, selectors, and overrides, including every
      drift and overlap condition.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 7: exact classification-rule expansion`
    - _Requirements: 5.4–5.9_
  - [ ] 8.7 Property test: Property 8 — status-entry state machine
    - Implement a table-backed reference-model `proptest` with at least 256 capability and
      evidence combinations across all five statuses and invalid planning/documentation-only
      variants.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 8: status-entry state machine`
    - _Requirements: 6.1–6.10, 10.1–10.3_
  - [ ] 8.8 Property test: Property 9 — evidence provenance and scope
    - Implement a valid-first `proptest` with at least 256 evidence/source registry cases,
      mutating each provenance, locator, command/outcome, target, platform, and reverse-row
      relationship independently and in combination.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 9: evidence provenance and scope`
    - _Requirements: 6.11, 6.12, 7.1–7.8_
  - [ ] 8.9 Property test: Property 17 — blocking-work ownership
    - Implement a reference-routing `proptest` with at least 256 generated blocking
      capabilities from every umbrella domain, including `initClient`, dependency client
      generation, and platform-only gaps.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 17: blocking-work ownership`
    - _Requirements: 10.4–10.11, 10.15, 10.16_
  - [ ] 8.10 Property test: Property 18 — downstream traceability preservation
    - Implement a `proptest` with at least 256 generated child declarations and candidate
      ledger changes, covering unknown IDs, unchanged rows, and every valid/invalid
      status-evidence transition.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 18: downstream traceability preservation`
    - _Requirements: 10.12, 10.13_

- [ ] 9. Implement bounded official-harness integration
  - [ ] 9.1 Implement exhaustive harness mapping validation
    - Require exactly one complete mapping for every pinned public check and no extras;
      bind source fingerprint, revision, target, platform, invocation, expected outcome,
      limitations, and optional evidence.
    - Enforce non-empty exact Capability_ID sets for `subject-conformance` and an empty set
      for `harness-self`; detect added, removed, and semantically changed checks.
    - _Requirements: 8.13, 12.2, 12.3, 12.12, 12.15_
  - [ ] 9.2 Implement harness-result normalization and evidence admission
    - Admit only a passing subject result whose check kind, revision, target, explicitly
      selected CLI/engine and verified artifact, platform, expected outcome, and capability
      scope exactly match its mapping.
    - Keep omitted behaviours/platforms, failures, and self results from proving Rust
      completeness; model expected subject failures as blocker evidence without making
      Integrity fail by themselves.
    - _Requirements: 1.10, 1.11, 6.13, 6.14, 7.9, 7.10, 12.4–12.9, 12.16_
  - [ ] 9.3 Implement the argv-only per-check harness runner
    - Execute the pinned public `dagger check <check-id> --no-generate` interface against
      the Rust workspace with the Target Descriptor's exact CLI/engine, immutable module
      revision, controlled environment, and explicit platform.
    - Record only normalized command/result identities and output digests; keep raw logs
      ephemeral and redact process failures and secrets.
    - _Requirements: 7.5–7.10, 12.4–12.9_
  - [ ] 9.4 Implement the portable Feature 8 extension boundary
    - Validate `ConformanceScenario` values using exactly one public `SdkTarget` or
      `mod-test` adapter, an exact non-empty Capability_ID set, source anchors, normalized
      observable behaviour, and Rust-valid invocation independent of obsolete or
      Go-specific CLI syntax.
    - _Requirements: 12.10, 12.11_
  - [ ] 9.5 Property test: Property 10 — harness inventory partition
    - Implement a reference-partition `proptest` with at least 256 generated check
      inventories and mappings, including missing/extra/changed checks and every valid or
      invalid subject/self capability partition.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 10: harness inventory partition`
    - _Requirements: 8.13, 12.2, 12.3, 12.12, 12.15_
  - [ ] 9.6 Property test: Property 11 — harness evidence containment
    - Implement a valid-first `proptest` with at least 256 mapping/result pairs, mutating
      each target, artifact, platform, assertion, outcome, kind, revision, and capability
      boundary and modelling expected subject-check failure separately from Integrity.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 11: harness evidence containment`
    - _Requirements: 1.10, 1.11, 6.13, 6.14, 7.9, 7.10, 12.4–12.9, 12.16_
  - [ ] 9.7 Property test: Property 21 — portable conformance extensions
    - Implement a `proptest` with at least 256 generated Rust extension scenarios covering
      both adapters, exact/empty/stale capability sets, preserved observable behaviour, and
      command-shaped ports that lack a semantic mapping.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 21: portable conformance extensions`
    - _Requirements: 12.10, 12.11_

- [ ] 10. Checkpoint: ledger and harness containment tests are green
  - Run locked unit/property/integration-fixture tests and clippy; require every generated
    harness outcome to remain inside its mapped target/platform/assertion boundary and every
    expected subject failure to leave Integrity unaffected.

- [ ] 11. Implement target transitions, stability, and compatibility claims
  - [ ] 11.1 Implement semantic target-transition differencing
    - Compare validated target, capability, authority-source, and Harness_Check identities
      and fingerprints; emit complete ordered added, removed, changed, authority-changed,
      and harness-changed sets.
    - Preserve prior rows and evidence for removals, invalidate changed-row evidence until
      revalidated, and require explicit classifications for additions.
    - _Requirements: 8.1–8.9, 8.13, 12.12_
  - [ ] 11.2 Implement Rust stability and migration classification
    - Validate stable, experimental, internal, and not-applicable states; compute none,
      additive, deprecation, or breaking SemVer effects; require graduation/removal
      conditions and Feature 9 migration references where specified.
    - _Requirements: 8.11, 8.12, 11.4–11.6_
  - [ ] 11.3 Implement compatibility validation and release-data derivation
    - Validate exact target sets or inclusive ranges with ordered full target boundaries,
      passing evidence at every claimed target/boundary, a typed outside-range capability,
      canonical claim digest, and release metadata derived from the validated claim.
    - _Requirements: 11.1–11.3, 11.7, 11.8_
  - [ ] 11.4 Property test: Property 13 — semantic drift and target-transition diff
    - Implement a simple-set/reference-diff `proptest` with at least 256 validated contract
      pairs and generated capability, authority, and harness additions/removals/changes,
      including historical preservation and evidence revalidation.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 13: semantic drift and target-transition diff`
    - _Requirements: 8.1–8.9, 8.13, 12.12_
  - [ ] 11.5 Property test: Property 14 — stability and migration classification
    - Implement a reference-table `proptest` with at least 256 public Rust transitions
      across stability states, compatible/incompatible changes, experimental conditions,
      and present/missing Feature 9 migrations.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 14: stability and migration classification`
    - _Requirements: 8.11, 8.12, 11.4–11.6_
  - [ ] 11.6 Property test: Property 19 — compatibility-claim truthfulness
    - Implement a reference-model `proptest` with at least 256 exact-set and bounded-range
      claims, boundary orderings and evidence sets, outside-range capabilities, claim
      digests, and derived release metadata.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 19: compatibility-claim truthfulness`
    - _Requirements: 11.1–11.3, 11.7, 11.8_

- [ ] 12. Implement reports, gates, CLI, and artifact-preserving staging
  - [ ] 12.1 Implement deterministic report aggregation
    - Build exact counts by primary authority, kind, all five statuses including zeroes,
      and Feature 2–9 owner; emit deterministically ordered diagnostics, blockers, and
      decision-backed complete exceptions.
    - Compute Integrity solely from integrity diagnostics and Completeness solely from true
      Integrity plus absence of `Missing`/`Partial` rows.
    - _Requirements: 9.1, 9.3–9.7_
  - [ ] 12.2 Implement the human report as a pure JSON-report projection
    - Render stable headings, target identity, verdicts, counts, blockers, exceptions, and
      diagnostics solely from `CompletenessReport`; test exact parity with `report.json`,
      including zero-count rendering.
    - _Requirements: 9.1, 9.2_
  - [ ] 12.3 Implement gate selection and the thin CLI
    - Add `verify`, `render`, `transition`, and `import-evidence` with the designed argv,
      stdout/stderr separation, selected integrity/completeness gate semantics, and exit
      statuses `0`, `1`, and `2`.
    - Make `verify` read-only and network-free; keep the initial CI profile on Integrity and
      expose Completeness for Feature 9 without enabling it as the F1 required gate.
    - _Requirements: 1.8, 9.8–9.11, 10.14_
  - [ ] 12.4 Implement isolated staging and atomic output adapters
    - Refuse a non-empty output directory; render and import into a destination-filesystem
      temporary staging tree; never edit or replace the active contract tree; and keep
      immutable network retrieval exclusive to explicit transitions.
    - Execute subprocesses directly from argv with a secret-free environment allowlist and
      retain no raw durable logs.
    - _Requirements: 7.5–7.7, 8.1–8.5, 9.3–9.5_
  - [ ] 12.5 Property test: Property 15 — verdict and report aggregation
    - Implement a simple-reference-aggregation `proptest` with at least 256 generated
      inventories, ledgers, statuses, owners, exceptions, and diagnostic sets.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 15: verdict and report aggregation`
    - _Requirements: 9.1–9.7_
  - [ ] 12.6 Property test: Property 16 — gate selection
    - Implement a truth-table `proptest` with at least 256 generated reports and gate
      selections, including initial Integrity CI and Feature 9 Completeness release profiles.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 16: gate selection`
    - _Requirements: 9.8–9.11, 10.14_
  - [ ] 12.7 Property test: Property 20 — rejection is artifact-preserving
    - Implement a filesystem-fixture `proptest` with at least 256 invalid contract,
      evidence-import, and transition cases; snapshot every active byte before/after and
      compare the full deterministic diagnostic set.
    - Tag: `// Feature: rust-sdk-completeness-contract, Property 20: rejection is artifact-preserving`
    - _Requirements: 7.5–7.7, 8.1–8.5, 9.3–9.5_

- [ ] 13. Checkpoint: transitions, reports, and command surfaces are green
  - Run formatting, locked checks, all implemented PBTs, CLI fixture tests, clippy, and
    rustdoc; require deterministic diagnostics and byte-identical outputs across repeated
    runs and distinct absolute fixture roots.

- [ ] 14. Add pinned contract inputs and wire Dagger automation
  - [ ] 14.1 Add authored target, authority, compatibility, and vendored-source inputs
    - Pin Dagger `25300124ca110612edc09c43f89cb5fad6028170`, Go
      `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`, and `sdk-sdk`
      `8c164424b7a8a37b33a77367ef7547490d5b87b5`; add the raw schema snapshot and exact
      byte-for-byte harness source subset needed for offline validation.
    - Record the target's real Rust workspace metadata and exact harness CLI/engine identity;
      do not encode the divergent Go version comment as source identity.
    - _Requirements: 1.1–1.11, 2.1–2.6, 2.10, 2.11, 11.1–11.3, 12.1_
  - [ ] 14.2 Add completeness sources to the Rust toolchain input boundary
    - Extend the existing source filter for `sdk/rust/completeness/**`, preserve the pinned
      Rust execution image, and add a digest-pinned Go helper container compatible with the
      root Go toolchain.
    - Pass only normalized helper JSON between Go and Rust execution boundaries.
    - _Requirements: 1.8, 2.6, 7.5, 8.1, 8.10_
  - [ ] 14.3 Implement `CompletenessIntegrity` and artifact generation
    - Add the `+check` offline Integrity gate and the `+generate`
      `CompletenessArtifacts` path using the existing engine introspection capture; return
      only a Dagger Changeset and never mutate active artifacts directly.
    - _Requirements: 1.6–1.8, 8.1–8.10, 9.8–9.10, 10.14_
  - [ ] 14.4 Implement the callable baseline harness profile
    - Add `CompletenessHarness` without `+check`; run the exact pinned profile and return a
      normalized evidence file for staged import while retaining expected subject failures
      as baseline blockers rather than Integrity failures.
    - _Requirements: 1.10, 1.11, 6.13, 6.14, 7.9, 7.10, 12.4–12.9, 12.16_

- [ ] 15. Populate the truthful Rust baseline and contributor guidance
  - [ ] 15.1 Author the reviewed capability definitions and harness mappings
    - Account for every extracted schema, Go client, engine SDK, codegen, active/removed
      test, harness, and Rust-policy SourceItem with atomic capabilities, reference anchors,
      or exact reviewed exclusions.
    - Map all 18 pinned checks: 17 `subject-conformance` checks with exact capability and
      limitation scopes, and `initModuleRendersRootType` as `harness-self` with no
      Capability_IDs; record `initClient` and non-`linux/amd64` omissions explicitly.
    - _Requirements: 2.3–2.10, 3.2–3.12, 4.1–4.12, 6.13, 6.14, 12.2, 12.3, 12.8, 12.9, 12.13, 12.15, 12.16_
  - [ ] 15.2 Classify every initial capability and attach conservative evidence
    - Resolve exactly one row per capability; use `Partial` for code without sufficient
      behavioural evidence and `Missing` for absent behaviour, with exact gaps and Feature
      2–9 owners.
    - Attach only pinned, scope-valid implementation, verification, and reviewed decision
      evidence; preserve failed subject checks as blocker state and never use harness-self,
      skipped, or removed tests as passing Rust evidence.
    - _Requirements: 6.1–6.14, 7.1–7.10, 10.1–10.11, 10.15, 10.16, 12.6–12.9, 12.16_
  - [ ] 15.3 Generate and check in the initial derived artifacts
    - Produce canonical source-items, inventory, resolved ledger, JSON report, and Markdown
      report with a true Integrity_Verdict and the truthful expected false
      Completeness_Verdict; verify exact counts, blockers, exceptions, ownership, and
      digests.
    - _Requirements: 5.4–5.9, 8.1–8.10, 9.1–9.10, 10.1, 10.14_
  - [ ] 15.4 Add exact initial-target regression fixtures
    - Lock tests for the three source commits, Go literal-vs-comment selection, 18-check
      and 17/1 partition, `initClient` omission, `linux/amd64` scope, explicit CLI
      selection, five statuses, Feature 2–9 routing boundaries, schema TypeRefs, malformed
      paths/revisions, and compatibility range boundaries.
    - _Requirements: 1.1–1.11, 3.10–3.12, 6.1–6.10, 7.2–7.4, 10.4–10.11, 10.15, 10.16, 11.1–11.7, 12.1, 12.5, 12.8, 12.9, 12.15_
  - [ ] 15.5 Align contributor documentation with the executable authority model
    - Add `completeness/README.md` for artifact ownership, offline verification, staged
      refresh/import, and later-feature status updates; revise Rust `AGENTS.md` and
      `CONTRIBUTING.md` so engine schema, scoped `sdk-sdk` conformance, and scoped Go parity
      are described as peer authorities with no contradictory precedence.
    - _Requirements: 4.8, 4.9, 6.10–6.12, 10.12, 10.13, 12.10–12.14_

- [ ] 16. Complete cross-boundary integration and repository verification
  - [ ] 16.1 Test hermetic verification and canonical regeneration end to end
    - From clean synthetic checkouts with network unavailable, run `verify` and `render`,
      compare every checked-in byte, and repeat from different absolute roots and source
      enumeration orders.
    - _Requirements: 1.8, 3.13, 7.5, 8.1, 8.2, 8.10, 9.1–9.5_
  - [ ] 16.2 Test Go extraction and engine-schema capture through Dagger
    - Exercise the standard-library Go helper through Rust revalidation and capture engine
      introspection through `RustSdkDev.CompletenessArtifacts`; require the returned
      Changeset to match canonical artifacts exactly.
    - _Requirements: 1.3–1.8, 2.1–2.10, 3.1–3.13, 4.1–4.9_
  - [ ] 16.3 Test harness-self, expected subject failures, and portable extensions
    - Exercise one passing self check, expected subject-SDK failures, and per-check
      target/platform mismatch rejection; prove no self result or failure becomes Rust
      completion evidence and no expected incompleteness alone breaks Integrity.
    - Exercise one scenario through each `SdkTarget` and `mod-test` adapter and reject a
      command-shaped port with no observable-behaviour mapping.
    - _Requirements: 6.13, 6.14, 7.9, 7.10, 12.4–12.11, 12.15, 12.16_
  - [ ] 16.4 Test staged transitions and evidence imports end to end
    - Cover capability/authority/harness add, remove, and semantic change; historical
      preservation; evidence revalidation; explicit new classification; SemVer/migration
      decisions; and failed import/transition with byte-identical active artifacts.
    - _Requirements: 7.5–7.8, 8.1–8.13, 11.4–11.6, 12.12_
  - [ ] 16.5 Test report equivalence and command exit policy end to end
    - Compare JSON and human verdict/count/exception/error content and exercise statuses
      `0`, `1`, and `2` for both gate selections without conflating incomplete Rust
      capability results with tool failure.
    - _Requirements: 9.1–9.11, 10.14_
  - [ ] 16.6 Run the complete Rust and supply-chain verification suite
    - From `sdk/rust`, run `cargo fmt --all --check`, locked workspace check/test/clippy,
      warning-denied rustdoc, and `cargo deny check`; run the Dagger Integrity check,
      artifact regeneration comparison, and pinned harness profile.
    - Require all F1 integrity checks to pass while reporting—not concealing—the expected
      subject-conformance blockers and false initial Completeness_Verdict.
    - _Requirements: 6.1–6.14, 8.10, 9.3–9.10, 10.1–10.16, 12.4–12.9, 12.16_

- [ ] 17. Checkpoint: F1 implementation is complete and ready for review
  - Require all 21 tagged property tests to run at least 256 cases, all fixed and
    integration tests to pass, every authored/derived artifact to reproduce byte-for-byte,
    the Dagger Integrity gate to be green, the initial Completeness gate to remain
    truthfully red where blockers exist, and the workspace security/licensing checks to be
    clean.

## Task Dependency Graph

Top-level tasks are ordered by the following prerequisite graph; subtasks within each
top-level task execute in listed order unless their text names a stronger dependency.

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
  "17": ["16"]
}
```

## Notes

- This plan implements the F1 measuring and governance system. A subject SDK check that
  currently fails is captured as truthful `Missing` or `Partial` state; making that Rust
  capability pass belongs to its owning Feature 2–9 specification.
- Every property task is mandatory, uses the workspace-standard `proptest` library, runs
  at least 256 generated cases, and carries the exact feature/property tag shown above.
- Reference models should remain deliberately simpler than production logic. Generated
  values start valid; targeted mutations supply rejection coverage and shrink useful
  counterexamples into the persisted regression corpus.
- Normal verification is offline and read-only. Network retrieval is permitted only for
  explicit immutable target transitions, and every write-producing command targets an
  empty staging directory or Dagger Changeset.
- The initial required CI signal is Integrity. Completeness is still calculated and
  reported, but Feature 9—not F1—owns promoting it to the stable-release gate.
