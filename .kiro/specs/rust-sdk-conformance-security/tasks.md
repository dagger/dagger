# Implementation Plan

- [x] 1. Implement and validate the provider-neutral sign-off host preflight
  - [x] 1.1 Add the strict host profile, plan, observation, and record models
    - Add `ConformanceFormatVersion`, `PlatformDescriptor`, bounded resource and timing
      scalars, `SignoffHostProfile`, the closed `HostPreflightStep` set,
      `HostPreflightPlan`, `HostStepObservation`, and `HostPreflightRecord` under
      `dagger-sdk-completeness/src/conformance/preflight.rs`.
    - Require canonical versioned JSON, unknown-field rejection, domain-separated
      digests, exact Linux/amd64 profile matching, minimum CPU/memory/workspace policy,
      container-daemon policy, persistence/export/cache policy, immutable smoke
      identities, ordered start/probe/stop, phase budgets, and bounded safe output.
    - Keep provider names, Namespace account/box identity, personal paths, ambient
      toolchain selection, credentials, target source, Capability_IDs, and Dagger graph
      object IDs out of durable models.
    - _Requirements: 2.1-2.6, 2.16-2.20_
  - [x] 1.2 Add the typed private preflight host binary and probe adapter
    - Add `dagger-rust-sdk-signoff preflight` as a private
      `dagger-sdk-completeness` binary using `std::process` behind the closed
      `HostProbe` interface; accept one checked profile and output path, not arbitrary
      commands or provider arguments.
    - Implement platform/resource observation, Docker API/storage observation,
      persistent canary restart, export/import round trip, identical cache reuse,
      pinned prebuilt smoke-engine start/readiness/service probe/stop, timeout
      enforcement, guaranteed cleanup, and retained-output canary scanning.
    - Record immutable preflight CLI and smoke-engine provenance. Ensure every failure,
      including cleanup failure, produces a stable phase diagnostic and no target
      build, SDK install, case execution, or capability claim.
    - _Requirements: 2.1-2.20, 10.1-10.17_
  - [x] 1.3 Property test: Property 3 — host planning is provider-neutral and fail-fast
    - Implement `property_03_host_planning_provider_neutral_fail_fast` in
      `signoff_preflight_properties.rs` over at least 256 provider labels, profiles,
      platforms, resources, daemon identities, timing schedules, stale identities,
      and retained-output mutations.
    - Compare production planning/admission with an independent required-step and
      threshold model; prove provider-name invariance, Namespace metadata exclusion,
      pre-artifact failure, record invalidation, and sign-off prevention.
    - _Requirements: 2.1-2.6, 2.16-2.20_
  - [x] 1.4 Property test: Property 4 — preflight proves infrastructure without claiming conformance
    - Implement `property_04_preflight_infrastructure_only` over at least 256 valid and
      fault-injected step sequences, smoke counts, reachability outcomes, reap states,
      payload/cache digests, target-work probes, claims, and output chunk boundaries.
    - Require one complete smoke lifecycle, unchanged canary round trips, observed
      cache reuse, cleanup after failure, and rejection of every target, SDK, case, or
      capability event.
    - _Requirements: 2.7-2.15_
  - [x] 1.5 Perform the first bounded live validation on the dedicated Namespace XL host
    - Build the private binary and checked Linux/amd64 profile, execute the same
      provider-neutral plan through `devbox exec`, and retrieve only the canonical
      redacted record. Do not encode the provider command, account, or box ID into that
      record or repository policy.
    - Confirm the observed 32-vCPU, 64-GiB, 200-GB-class persistent host and Docker
      daemon satisfy the profile; treat ambient Go 1.25.3 as non-authoritative because
      later artifact construction supplies Go 1.26.1.
    - Run one pinned prebuilt smoke engine only, prove service reachability and reap,
      preserve exact phase timings, and stop before any Exact_Target artifact work.
      This is infrastructure preflight, not a local checkpoint or conformance evidence.
    - _Requirements: 2.1-2.20_

- [x] 2. Establish the Feature 8 model, diagnostics, and property-strategy foundation
  - [x] 2.1 Add strict common identifiers and canonical wire boundaries
    - Add validated `AssertionId`, `SignoffCaseId`, `FixtureContextId`,
      `ReviewedFixtureId`, `ProvenanceId`, `FindingId`, `NetworkPolicyId`, non-zero
      duration/count scalars, platform/toolchain roles, and safe diagnostic coordinates.
    - Reuse Feature 1 `CapabilityId`, target, status, canonical set, and digest types;
      reject controls, absolute paths, mutable refs, ambiguous platform spellings, and
      unknown fields at decode.
    - Keep these types private to `dagger-sdk-completeness`; add no dependency or API
      change to `dagger-sdk` or `dagger-sdk-macros`.
    - _Requirements: 1.1-1.18, 3.1-3.24, 12.1-12.35_
  - [x] 2.2 Add the total Feature 8 diagnostic set and safe renderer
    - Implement the design's scope, applicability, assertion, catalog, preflight,
      closure, artifact, engine, baseline, case, retry, platform, security, checkpoint,
      and verdict codes; reuse existing Feature 1/checkpoint codes where their
      distinction remains sufficient.
    - Preserve stable capability/assertion/case/finding/phase coordinates, sorted
      de-duplicated multi-errors, bounded safe messages, cleanup errors alongside
      primary failures, and redaction of raw process/host/secret text.
    - Add fixed tests for one instance of every error-table row and hostile control,
      path, credential, and output-size inputs.
    - _Requirements: 1.9-1.16, 2.16-2.20, 3.17-3.24, 9.18-9.23, 10.10-10.17, 12.20-12.35_
  - [x] 2.3 Add valid-first property strategies and independent reference models
    - Add shared generators for exact scope sets, applicability decisions, assertion
      graphs, case catalogs, host plans, child closures, artifact event logs, platform
      matrices, Cargo/security graphs, findings/exceptions, canary chunks, case
      attempts, counters, timings, and complete verdict trees.
    - Keep target builds, Docker, Cargo, Dagger, network, native-process operations, and
      scanner execution outside randomized loops. Reference models must use simple set
      joins, truth tables, and state folds rather than production validators.
    - Configure at least 256 cases for scope/graph/artifact/isolation/canary/verdict
      properties and at least 100 for all remaining properties.
    - _Requirements: 1.1-1.18, 2.1-2.20, 3.1-3.24, 5.1-5.20, 6.1-6.22, 8.1-8.21, 9.1-9.25, 10.1-10.17, 12.1-12.35_

- [x] 3. Register the exact existing and Rust-policy Feature 8 inventory
  - [x] 3.1 Pin the existing 1,081-capability authority scope
    - Verify the current 1,072 `Missing` integration rows and nine `Partial` definitive
      Go-client rows against the exact target, fingerprints, authorities, source
      locators, and reviewed scope digest
      `sha256:2969bd8fde19fc17d327cef637b9d848eca01040e88caffc09a4e9a4ad9bc5f9`.
    - Add a checked canonical scope artifact and deterministic drift renderer which
      reports exact added, removed, moved, or fingerprint-changed rows before accepting
      a new digest.
    - Preserve every row's current status and blocker while mappings are incomplete;
      inventory existence is never implementation evidence.
    - _Requirements: 1.1-1.3, 1.13, 1.15-1.18_
  - [x] 3.2 Add the exact 21 Rust policy capabilities
    - Add every approved conformance, host, artifact, closure, single-engine/baseline,
      isolation/retry, atomic/duplicate/timing, platform, locked/provenance/scan/canary,
      and expiring-exception capability with stable fingerprints, Feature 8 ownership,
      exact target, blocking state, and reviewed requirement coordinate.
    - Keep authority-derived and Rust-policy scope separate in canonical accounting so
      neither count can hide drift in the other.
    - Regenerate only the affected completeness inventory/report artifacts and retain
      every new row as blocking until its required evidence is admitted.
    - _Requirements: 1.4, 1.18_
  - [x] 3.3 Add the reviewed applicability and policy artifact schemas
    - Add checked empty-validity-impossible scaffolds for
      `conformance-applicability.json`, `conformance-assertions.json`, and
      `conformance-cases.json`, with canonical format versions and exact target/scope
      identities.
    - Permit tooling to scaffold one placeholder per exact ID but make every
      placeholder, inherited file-wide disposition, wildcard route, catch-all
      rationale, or unknown terminal policy fail admission.
    - _Requirements: 1.5-1.16, 3.1-3.24_

- [x] 4. Checkpoint: host preflight and Feature 8 foundations are green
  - Run formatting; locked focused tests for the new common/preflight models,
    diagnostics, exact inventory, policy scope, and Properties 3–4; warning-denied
    Clippy/rustdoc for `dagger-sdk-completeness`; and the preflight binary's in-memory
    adapter fixtures.
  - Validate the retrieved Namespace record against the same checked provider-neutral
    profile. Do not rerun its live smoke when profile/tool/daemon identities are
    unchanged; record reuse by digest.
  - Record exact commands, elapsed times, Cargo counts, source/generated-asset
    decisions, and evidence identity. Run Cargo Deny only if Cargo/security inputs
    changed.
  - Require no Dagger command, engine, module, other SDK, unscoped generation,
    distribution build, or network in this local checkpoint; the completed live
    preflight remains a separately identified infrastructure action.
  - _Requirements: 1.1-1.18, 2.1-2.20, 10.1-10.17, 11.1-11.20_

- [x] 5. Implement exact scope and applicability admission
  - [x] 5.1 Add `ConformanceScopeInput` and the exact scope compiler
    - Validate the existing authority count/set/digest and distinct policy inventory
      before decoding decisions; build private canonical maps and reverse indexes only
      after the complete input is valid.
    - Require exactly one current `ApplicabilityRecord` per existing ID, exact authority
      anchor/fingerprint, one closed disposition, disposition-compatible assertion/case
      routes, decision evidence, terminal policy, and blocker semantics.
    - Reject duplicates, omissions, additions, stale anchors, generic rationales,
      engine-owned rows with Rust effects, foreign-only rows with unrouted shared
      invariants, and unsupported status transitions as complete-set failures.
    - _Requirements: 1.1-1.18_
  - [x] 5.2 Add capability-local equivalence and inapplicability decision models
    - Model same-mechanism, idiomatic-equivalent, engine-owned-no-Rust-obligation, and
      foreign-SDK-no-Rust-obligation dispositions without source-language mechanism
      leakage into the public Rust contract.
    - Require equivalence decisions to name observable behaviour and Rust mechanism;
      engine-owned decisions to prove no Rust input/output/lifecycle/compatibility
      effect; and foreign decisions to name the exact foreign mechanism plus every
      routed shared assertion.
    - Add fixed valid and invalid records for each disposition and terminal policy.
    - _Requirements: 1.6-1.15, 7.1-7.4_
  - [x] 5.3 Property test: Property 1 — existing and Rust-policy scope is exact
    - Implement `property_01_existing_and_policy_scope_exact` over at least 256 active
      ledger/policy permutations and mutations; compare production scope derivation
      with exact set/count/digest reference logic.
    - Accept only the pinned 1,081 existing rows plus the complete distinct policy set,
      and require an exact drift result before any changed inventory is admitted.
    - _Requirements: 1.1-1.4, 1.16_

- [x] 6. Review shared engine, CLI, workspace, and module authority rows
  - [x] 6.1 Classify direct shared module semantics capability by capability
    - Review the exact rows from call, path-input, runtime-behaviour, type, loading,
      configuration, workspace, definition, self-call, dependency-runtime,
      constructor, interface, current-module, private-dependency, validation,
      deprecation, engine-version, terminal, error, and module-up sources.
    - Map every Rust-observable result to a stable assertion/fixture context and retain
      genuine engine-only observations only with local decision evidence. Separate
      benchmark correctness assertions from non-gating measurements.
    - Preserve exact authority anchors and fingerprints for every expanded record; use
      shared assertions only where normalized predicate and fixture context match.
    - _Requirements: 1.5-1.17, 7.1-7.4, 7.8-7.18_
  - [x] 6.2 Classify CLI, initialization, custom SDK, and suite-scaffolding rows
    - Review exact module-init, SDK-init, SDK-selection, introspection, module CLI,
      custom-SDK, runtime-codegen, dependency-CLI, TUI, and suite-lifecycle rows.
    - Route public CLI/SDK lifecycle effects to Rust cases, retain presentation-only or
      harness-scaffolding mechanisms as justified inapplicable, and prevent a suite
      name or helper type from becoming an automatic Rust obligation.
    - _Requirements: 1.5-1.17, 3.5-3.13, 7.1-7.7_
  - [x] 6.3 Add reviewed exact-ID grouping and audit output
    - Permit one rationale/assertion to be authored once for an explicit sorted ID set,
      then expand it to one complete record per ID with its local anchor and
      fingerprint. Reject globs, file-wide defaults, predicate mismatches, or hidden
      unmatched rows.
    - Render per-source counts, disposition counts, assertion sharing, residual
      blockers, and the complete canonical record digest for review without treating
      grouping as evidence.
    - _Requirements: 1.5-1.18_

- [x] 7. Review foreign-SDK mechanisms and definitive Go-client behaviours
  - [x] 7.1 Classify language-specific integration rows without language-shaped Rust
    - Review every exact TypeScript, Python, Go, Dang, Java, PHP, Elixir, built-in Dang,
      and custom-language row. Name foreign package managers, templates, reflection,
      runtime bootstraps, and source-language syntax precisely where inapplicable.
    - Route shared Dagger module, type, dependency, filesystem, error, and lifecycle
      invariants to idiomatic Rust assertions. Never classify a complete language file
      in one step or run a foreign SDK to prove the Rust result.
    - _Requirements: 1.5-1.17, 7.1-7.4_
  - [x] 7.2 Map all nine definitive Go-client behaviours to public Rust assertions
    - Add exact directory, Git, container, container-mutation, list, typed exec-error,
      and three exec-error subtest mappings with the pinned Go anchors/fingerprints.
    - Preserve observable results and typed error fields through idiomatic Rust APIs;
      do not copy Go globals, pointer options, zero-value omission, or package layout.
    - _Requirements: 1.1-1.17, 3.13, 7.1-7.4, 7.17-7.18_
  - [x] 7.3 Property test: Property 2 — applicability is total, local, and evidence-gated
    - Implement `property_02_applicability_total_local_evidence_gated` over at least
      256 complete/mutated record sets, anchors, fingerprints, decisions, assertions,
      case routes, terminal policies, and Rust-effect flags.
    - Compare with a disposition truth-table/reference join; prove totality, locality,
      decision requirements, blocking preservation, and zero unjustified blockers at
      closure.
    - _Requirements: 1.5-1.15, 1.17-1.18_

- [x] 8. Checkpoint: the complete applicability decision surface is green
  - Run formatting, locked scope/applicability tests, Properties 1–2, fixed authority
    and nine-Go-client mappings, exact count/digest drift tests, source-policy tests,
    and focused warning-denied Clippy/rustdoc for `dagger-sdk-completeness`.
  - Recompute the applicability artifact once from the reviewed exact records; require
    1,081/1,081 authority rows and every policy row accounted with no placeholder,
    wildcard, catch-all, stale fingerprint, or unreviewed blocker transition.
  - Record commands, timings, Cargo counts, generated-artifact decision, and neutral
    disposition/remaining-blocker observations. Do not present justified
    `Inapplicable` rows as implemented Rust.
  - Require the engine-free local boundary and reuse the admitted host preflight record
    without starting its smoke engine.
  - _Requirements: 1.1-1.18, 7.1-7.4, 11.1-11.20_
  - Checkpoint evidence: `sdk/rust/completeness/evidence/conformance-applicability-checkpoint.json`
    records the six bounded Cargo commands, 1,081/1,081 expanded authority records,
    21/21 policy rows, exact disposition counts, retained current blockers, and reuse
    of the unchanged host preflight without a Dagger command or engine start.

- [x] 9. Build the Rust-observable assertion catalog and fixture registry
  - [x] 9.1 Add closed observable predicates and assertion compilation
    - Add result, typed-error, lifecycle, filesystem, query, metadata, omission,
      isolation, and compatibility predicate variants with exact authority anchors,
      capability sets, fixture context, and optional idiomatic decision identity.
    - Compile assertions as canonical private maps; merge only equal normalized
      predicate plus fixture context; reject prose-only, foreign-code, orphaned,
      duplicated, conflicting, or anchor-mismatched assertions.
    - _Requirements: 1.6-1.12, 3.5, 3.14-3.20, 7.1-7.4, 7.16-7.18_
  - [x] 9.2 Add the reviewed fixture registry and exact executor identities
    - Register stable fixture IDs and digests for common harness, stable connector,
      Core shapes, Feature 5, Feature 6, Feature 7, definitive Go-client, and remaining
      integration-assertion families.
    - Bind each fixture to one closed `CaseProgram`, network policy, immutable inputs,
      and permitted assertion family. Reject runtime paths, arbitrary commands,
      unknown selectors, mutable remotes, foreign SDK executors, and digest drift.
    - _Requirements: 3.5-3.24, 7.5-7.15_
  - [x] 9.3 Add deterministic authority/assertion drift reporting
    - Compare current selected sources with assertion anchors and report exact added,
      removed, reclassified, merged, split, and fingerprint-changed assertion scope.
    - Require an updated reviewed assertion and applicability artifact before a changed
      authority can enter the case compiler.
    - _Requirements: 1.15-1.16, 7.16_

- [x] 10. Compile the complete closed sign-off case catalog
  - [x] 10.1 Add `CaseProgram`, `CaseDefinition`, and policy models
    - Implement the eight closed case families, exact fixed child-case enums, fixture
      digest, assertion/capability sets, non-zero timeout, typed retry classes,
      network policy, and concurrency class. Catalog JSON must contain no command text.
    - Import Feature 6 and Feature 7 provisional deferred inventories into umbrella
      cases while retaining their closure identities; stop treating their provisional
      artifact/counter/verdict shapes as independently releasable.
    - _Requirements: 3.1-3.16, 7.5-7.15_
  - [x] 10.2 Add total forward/reverse catalog validation
    - Require exact target, Subject_Revision/source digest, platform, all applicable
      assertions, all 17 sdk-sdk subject checks, stable connector, complete Core shape
      family, Feature 5/6/7 inventories, nine Go-client behaviours, and all additional
      integration assertions before producing a catalog digest.
    - Reject the sdk-sdk harness-self check, missing routes, overbroad claims,
      duplicated/unknown IDs, fixture mismatch, complete foreign suites, unrelated SDK
      work, repository-wide generation, and declaration-order dependence.
    - _Requirements: 3.1-3.24_
  - [x] 10.3 Property test: Property 5 — case catalog is closed, complete, and deterministic
    - Implement `property_05_case_catalog_closed_complete_deterministic` over at least
      256 scope/assertion/case graph permutations, fixed inventory mutations, fixture
      identities, policies, and forbidden programs.
    - Compare production compilation with independent forward/reverse set joins and
      fixed-family required sets; require one canonical digest or a stable complete
      diagnostic set.
    - _Requirements: 3.1-3.24_

- [x] 11. Assemble matching engine-free implementation closure evidence
  - [x] 11.1 Add exact Feature 2–7 closure references and compatibility policy
    - Define the closed six-child set, target/subject-or-asset/closure identities,
      outcome, engine-free marker, generated-asset map, native matrix identity, and
      ordinary Rust security identity.
    - Add adapters for current Feature 2–7 evidence formats. Treat historical
      engine-backed Feature 5 evidence honestly: consume only its direct implementation
      closure here and keep its exact cases in the umbrella catalog.
    - _Requirements: 4.1-4.13, 4.19_
  - [x] 11.2 Add fail-fast closure admission without replay
    - Validate exact child set, shared target, Subject_Revision or reviewed compatible
      asset identity, passed current outcomes, generated assets, complete platform
      matrix, and Rust hygiene/security before artifact work.
    - Reject missing, duplicated, skipped, failed, stale, mismatched, or falsely
      engine-free evidence. Ensure the plan contains zero Rust unit/fixture, format,
      Clippy, rustdoc, Cargo Deny, or direct-Go replay actions.
    - _Requirements: 4.1-4.19_
  - [x] 11.3 Property test: Property 6 — closure consumes exactly current engine-free evidence
    - Implement `property_06_closure_exact_current_engine_free` over at least 256 child
      sets, targets, subjects/assets, outcomes, generated assets, platform/security
      identities, and replay-event mutations.
    - Compare with a six-member required-set and identity-compatibility model; accept
      only complete current evidence with zero replay and reject before engine startup.
    - _Requirements: 4.1-4.19_

- [x] 12. Checkpoint: assertions, catalog, and closure planning are green
  - Run formatting, locked assertion/catalog/closure tests, Properties 5–6, fixed child
    inventory and sdk-sdk boundary tests, source-policy tests, and focused
    warning-denied Clippy/rustdoc.
  - Render the complete case catalog and closure-plan fixtures; require all applicable
    capability/assertion routes, exact fixed families, and zero executable shell,
    another SDK, engine action, or replay action.
  - Record commands, timings, Cargo counts, reviewed artifact reuse/regeneration, and
    catalog/closure digests. Regenerate only changed Feature 8 artifacts.
  - Keep the checkpoint engine-free and consume—not rerun—the current preflight record.
  - _Requirements: 3.1-3.24, 4.1-4.19, 7.5-7.18, 11.1-11.20_
  - Checkpoint evidence: `sdk/rust/completeness/evidence/conformance-catalog-checkpoint.json`
    records six bounded Cargo commands, the 1,047-assertion/1,047-fixture/672-case
    catalog, Properties 5–6 at 256 cases each, zero authority drift, the consume-only
    closure-plan identity, and reuse of the unchanged host preflight without a Dagger
    command, engine start, another SDK, or replayed child closure.

- [x] 13. Implement native and descriptor platform closure
  - [x] 13.1 Add the exact platform policy and pure descriptor matrix
    - Define Linux, macOS, Windows, amd64, arm64, native domain, runner/toolchain/source/
      lockfile/test identities, and exact-engine platform-claim models.
    - Exercise archive/executable descriptor selection for all six OS/architecture
      pairs as pure Rust. Reject unknown aliases, missing pairs, and attempts to infer
      native semantics from descriptor simulation.
    - _Requirements: 8.4-8.9, 8.18, 8.20-8.21_
  - [x] 13.2 Add native engine-free OS observation and matrix admission
    - Extend existing process, discovery, cache publication, archive/path/link,
      child-reap, control-line, diagnostic, and redaction fixtures to emit one canonical
      bounded observation on each native OS under Rust 1.97.1 and committed lockfiles.
    - Assemble exactly one current passed Linux, macOS, and Windows job plus all six
      descriptors; allow documented native equivalents such as Windows reparse/ACL
      behaviour but reject skips, simulation, another SDK, Docker, Dagger, or an engine.
    - _Requirements: 8.1-8.3, 8.10-8.19_
  - [x] 13.3 Property test: Property 16 — descriptor and exact-engine platform claims never widen
    - Implement `property_16_descriptor_and_exact_engine_platform_claims_never_widen`
      over at least 100 descriptor/matrix/artifact/verdict platform permutations.
    - Require all six descriptors, exact identity binding, initial Linux/amd64, and a
      separate artifact/verdict for every later platform.
    - _Requirements: 8.4-8.9, 8.18, 8.20-8.21_
  - [x] 13.4 Property test: Property 17 — native OS closure proves native behaviour without an engine
    - Implement `property_17_native_os_closure_engine_free` over at least 100 native
      job/domain/identity/outcome permutations and forbidden-event sets.
    - Compare with the exact three-OS/native-domain model; reject missing, duplicated,
      stale, skipped, failed, simulated, engine-backed, or other-SDK evidence.
    - _Requirements: 8.1-8.3, 8.10-8.17, 8.19_

- [x] 14. Implement locked dependency, provenance, and vulnerability policy
  - [x] 14.1 Make Rust root and automation coverage exact
    - Enumerate every supported root/example Cargo manifest and committed lockfile,
      require `--locked`, all Cargo Deny classes, approved licenses/sources, no
      unapproved wildcard or active reachable advisory, and workspace unsafe denial.
    - Correct Dependabot to cover actual Cargo roots and remove the inapplicable npm
      `/sdk/rust` entry as false Rust coverage. Retain read-only/minimum workflow token
      permissions and immutable packaged dependency policy.
    - _Requirements: 9.1-9.10, 9.24-9.25_
  - [x] 14.2 Add the checked external provenance registry
    - Record role, publisher, repository, immutable digest/checksum, and independent
      review evidence for builder/base images, Rust/Go toolchains, preflight CLI/engine,
      CLI archives, scanner, and vulnerability database source.
    - Validate the existing Trivy 0.69.3 image only after its Aqua Security publisher,
      repository, and exact digest provenance are reviewed; reject tag-only,
      unknown-publisher, missing-review, or role-mismatched inputs.
    - _Requirements: 5.20, 9.11-9.16_
  - [x] 14.3 Add finding and machine-expiring exception admission
    - Decode canonical scanner findings against the exact artifact payload and model
      fixed-date, target-revision, patched-version, and advisory-withdrawal expiry
      predicates.
    - Retain every finding; reject unexcepted high/critical findings and exceptions
      missing exact finding, reachability, impact, owner, upstream remediation, or a
      currently false expiry condition. Reject stale exceptions automatically.
    - _Requirements: 9.16-9.23_
  - [x] 14.4 Property test: Property 18 — Rust dependency security is locked, complete, and least-privileged
    - Implement `property_18_rust_dependency_security_locked_complete_least_privileged`
      over at least 100 Cargo roots, graph/license/source/advisory/unsafe/automation/
      permission/package observations.
    - Compare with exact root and security truth-table models; admit only complete
      locked current policy and narrowly proved unsafe exceptions.
    - _Requirements: 9.1-9.10, 9.24-9.25_
  - [x] 14.5 Property test: Property 19 — external provenance and exact-payload vulnerability policy fail closed
    - Implement `property_19_external_provenance_exact_payload_vulnerability_fail_closed`
      over at least 256 provenance, payload, scanner/database, finding, and exception
      mutations.
    - Require exact payload scanning without rebuild, immutable reviewed provenance,
      current finding-specific exceptions, and rejection on every expired or
      unexcepted high/critical result.
    - _Requirements: 9.11-9.23_

- [x] 15. Prove secret, diagnostic, and evidence safety
  - [x] 15.1 Add the ephemeral canary harness and exhaustive inspection domains
    - Generate independent high-entropy non-production canaries for session, registry,
      Git, environment, trace, and URL credential boundaries; pass values only to the
      live scanner and persist only their canonical set digest.
    - Inspect source/generated/packaged files, artifact entries, cache/provenance keys,
      stdout/stderr, errors/Debug, diagnostics/traces, reports, and draft verdicts
      across arbitrary chunk boundaries.
    - _Requirements: 10.1-10.10_
  - [x] 15.2 Add durable evidence sanitization and bounded failure retention
    - Reject any persisted canary value, real credential, absolute host path, personal
      or provider identity, terminal control, unbounded output, or unredacted failure
      source. Retain only canary category, inspection domain, and safe relative
      coordinate for a detected leak.
    - Ensure artifact and final verdict contain no live credentials and that inability
      to prove redaction prevents evidence admission.
    - _Requirements: 10.10-10.17_
  - [x] 15.3 Property test: Property 20 — canaries and host identity never enter retained evidence
    - Implement `property_20_canaries_and_host_identity_never_persist` over at least 256
      canary sets, output domains, arbitrary byte chunking, diagnostic causes,
      artifacts, caches, traces, reports, verdicts, paths, and identities.
    - Compare with an independent streaming matcher and safe-field whitelist; any leak
      or unsafe identity must reject the whole observation.
    - _Requirements: 10.1-10.17_

- [x] 16. Checkpoint: development native-platform and security policy are green
  - Run formatting; locked platform, security, provenance, exception, and canary tests;
    Properties 16–20; Cargo Deny when dependency/security inputs changed; source-policy
    tests; and focused warning-denied Clippy/rustdoc.
  - Run the native job locally only for the current OS as a fixture producer. Ordinary
    fork CI may exercise Linux and macOS independently but SHALL NOT aggregate or claim
    a complete portable matrix without an exact current Windows observation.
  - Record commands, timings, Cargo counts, provenance/security artifact decisions,
    and platform/security digests. Keep canary values out of the record.
  - Require no Dagger, engine, module, another SDK, target artifact build, unscoped
    generation, or distribution work.
  - _Requirements: 8.1-8.21, 9.1-9.25, 10.1-10.17, 11.1-11.20_
  - Checkpoint evidence: `sdk/rust/completeness/evidence/conformance-platform-security-checkpoint.json`
    records 156 passed native macOS/arm64 tests with one exact-engine test and one
    subprocess-helper test ignored by their owning parent cases,
    Properties 16–20, all locked Cargo roots, current Cargo Deny, checked Aqua Security
    provenance, and zero Dagger, Docker, engine, module, foreign-SDK, target-artifact, or
    distribution work. The local checkpoint retains only the current native observation
    and does not simulate Linux or Windows. The `PortablePlatformMatrix` remains
    deliberately unadmitted: ultimate SDK sign-off must expressly run Linux, macOS, and
    Windows and aggregate their exact current observations before closing Requirement 8.

- [x] 17. Implement the exact-target artifact manifest and state machine
  - [x] 17.1 Add every artifact manifest, component, and provenance field
    - Implement target/subject/platform, separate engine/CLI/Go-runtime/Rust manifest/
      Rust descriptor inputs, Rust/Go/base/builder/scanner toolchains, exact component
      input/content/provenance maps, payload digest/size, and provenance digest.
    - Require a full reachable Subject_Revision whose focused source digest matches the
      workspace; reject dirty, mutable, unreachable, cross-target, or cross-platform
      identities.
    - _Requirements: 5.1-5.6, 5.10-5.11, 5.17, 5.20, 12.2-12.10_
  - [x] 17.2 Add exclusive Build/Import planning and counter admission
    - Model Build and Import as mutually exclusive state machines. Build permits one
      construction, zero imports, and at most one component build; Import permits one
      import, zero construction/component builds, and exact manifest/payload/component
      verification.
    - Reject fallback between strategies, duplicate work, digest-only missing content,
      unrelated SDK builders/tests/generation, complete Go tests, distribution paths,
      and imported-byte mismatch before engine startup.
    - _Requirements: 5.1-5.20_
  - [x] 17.3 Add canonical real-byte bundle assembly and round-trip fixtures
    - Define deterministic outer tar membership for `manifest.json`,
      `provenance.json`, `engine.oci.tar.zst`, and non-recursive checksums with fixed
      order, modes, ownership, timestamps, and compression policy.
    - Round-trip real small OCI/canary payload bytes through export/import and prove
      payload identity survives process/session restart. Do not use Dagger object IDs
      or host cache keys as portable content.
    - _Requirements: 2.14-2.15, 5.7-5.11, 5.18-5.20_
  - [x] 17.4 Property test: Property 7 — artifact identity accounts for every immutable byte source
    - Implement `property_07_artifact_identity_accounts_every_immutable_source` over at
      least 256 targets, subjects, platforms, component/toolchain/provenance sets,
      payload bytes, and manifest mutations.
    - Require deterministic admission only for the complete compatible manifest and
      actual bytes; every semantic input or payload mutation must change identity.
    - _Requirements: 5.1-5.6, 5.10-5.11, 5.17-5.18, 5.20_
  - [x] 17.5 Property test: Property 8 — Build and Import are exclusive at-most-once state machines
    - Implement `property_08_build_import_exclusive_at_most_once` over at least 256
      event sequences, counters, verification results, and forbidden graph entries.
    - Compare with an independent two-branch automaton; reject mixed/fallback/
      duplicated/mismatched/unrelated sequences.
    - _Requirements: 5.7-5.9, 5.12-5.16, 5.19_

- [x] 18. Add one focused Dagger artifact build/export/import graph
  - [x] 18.1 Extend the existing focused engine builder with an exportable sign-off object
    - Reuse one `RustSDKContent` and one fully configured focused target container whose
      OCI bytes include the exact engine, exact CLI, mandatory target Go content, and
      packaged Rust SDK. Retain base-support equivalence validation and focused source
      exclusions.
    - Export the container archive once, observe component/toolchain identities, and
      assemble the Rust-authored manifest/provenance/bundle without running a target
      service or another SDK suite.
    - _Requirements: 5.1-5.6, 5.10-5.20_
  - [x] 18.2 Add verified artifact import and exact CLI extraction
    - Accept one host bundle only for the Import strategy; verify envelope, checksums,
      canonical manifest, payload, components, target, subject, platform, and
      provenance before `Container.Import`.
    - Return one verified target container and the CLI file extracted from those bytes;
      never call engine, CLI, Go-runtime, or Rust-content builders on import.
    - _Requirements: 5.7-5.11, 5.17-5.20, 6.2-6.6_
  - [x] 18.3 Add engine-free Go graph-construction tests
    - Extend `toolchains/rust-sdk-dev/internal/enginefree` and add
      `internal/signoff` AST/fixture tests proving exactly one build/import branch, one
      OCI export/import site, one focused source closure, no unrelated SDK/distribution
      entry, and the same artifact file identity supplied to later scanner/runner
      seams.
    - These tests inspect graph construction only; they must not initialize generated
      Dagger bindings or start an engine.
    - _Requirements: 5.1-5.20, 11.1-11.10_

- [x] 19. Bind exact artifact scanning to the reusable payload
  - [x] 19.1 Extend `toolchains/security` with canonical scanner/database observations
    - Accept the exact existing OCI archive file, run the digest-pinned reviewed Trivy
      image read-only, record scanner version/image digest and database metadata
      digest, and emit bounded canonical JSON findings plus elapsed time.
    - Do not invoke an engine/Rust SDK builder, reconstruct a container from source, or
      broaden to repository/source scanning in the exact-artifact function.
    - _Requirements: 9.11-9.18, 12.10, 12.18_
  - [x] 19.2 Add scanner-result translation and security report assembly
    - Decode scanner output through the Rust security model, require exact payload
      identity, apply only current finding-specific exceptions, join ordinary Rust
      security/provenance/canary evidence, and derive one `ArtifactSecurityReport`.
    - Preserve all findings and database identity; reject malformed/unknown severity,
      payload mismatch, rebuild counters, missing timing, stale exception, or canary
      leak.
    - _Requirements: 9.14-9.23, 10.3-10.17, 12.10, 12.18, 12.28-12.29_
  - [x] 19.3 Add engine-free scanner fixture and graph-source tests
    - Use checked canonical Trivy JSON/database metadata fixtures and a small OCI
      canary archive to prove payload binding, finding/exception policy, and no rebuild
      without pulling or scanning the target engine at local checkpoints.
    - Source-audit the Dagger security function to ensure it consumes the passed file
      once and retains the immutable scanner digest.
    - _Requirements: 9.14-9.23, 11.1-11.10_

- [x] 20. Checkpoint: artifact planning, export/import graph, and scan policy are green
  - Run formatting; locked artifact/security tests and Properties 7–8; small real-byte
    bundle round trips; engine-free Go graph/source tests; Cargo Deny when inputs
    changed; and focused warning-denied Clippy/rustdoc.
  - Validate build/import counters and scanner translation with fixtures only. Do not
    construct or scan the Exact_Target artifact at this checkpoint.
  - Record commands, elapsed times, Cargo counts, checked-artifact decisions, and pure
    manifest/payload/security fixture digests.
  - Require no Dagger command, engine, module, other SDK, target build, target scan,
    unscoped generation, or distribution build.
  - _Requirements: 5.1-5.20, 9.11-9.23, 11.1-11.20_
  - Checkpoint evidence: `sdk/rust/completeness/evidence/conformance-artifact-security-checkpoint.json`
    records the canonical real-byte artifact model, exclusive Build/Import properties,
    focused Go graph audit, exact-payload scanner translation, and current Cargo Deny,
    Clippy, rustdoc, Actionlint, and Zizmor results. The same checked-in scripts passed
    natively on macOS/arm64 and on the dedicated Linux/amd64 host with 156 tests passed,
    two intentional fixture/sign-off ignores, and zero Dagger, Docker, engine,
    foreign-SDK, target-artifact, or distribution work. Windows remains deliberately
    deferred to ultimate SDK sign-off. The top-level generated Dagger adapter was not
    regenerated because this engine-free checkpoint forbids a Dagger command; the
    source graph and its generated dependency binding compile, and scoped public
    dispatch generation remains owned by the executable sign-off facade.

- [x] 21. Materialize one exact installed Rust baseline and honest connector case
  - [x] 21.1 Add exact engine and CLI identity validation before installation
    - Validate the imported/built container's engine version, target revision, Rust
      manifest/descriptor, platform, and extracted CLI digest before service or
      baseline creation.
    - Create one clean runner/Git workspace using only the exact artifact CLI and one
      `dagger sdk install --here rust`; reject ambient repository path dependencies,
      host CLI use, duplicate installs, and stale installed configuration.
    - _Requirements: 6.1-6.6, 6.11-6.13, 6.21-6.22_
  - [x] 21.2 Add immutable baseline identity and workspace branching
    - Bind artifact, exact engine, CLI, installed config, dependency descriptor, and
      runner-image digests into `InstalledRustBaseline`. Branch every case from that
      immutable value with a distinct workspace/environment/cache/session namespace.
    - Refactor Feature 5 `resolution` to use this common baseline instead of a separate
      install path.
    - _Requirements: 6.3-6.6, 6.11-6.15, 6.17-6.22_
  - [x] 21.3 Implement the stable default connector observation
    - Leave explicit local CLI selection unset, place only the exact artifact CLI on
      `PATH`, run the production distribution path, validate the target version and an
      authenticated query, close the client, and prove child reaping.
    - Record beta.10 403/404 `Compatibility_PATH_Fallback` without claiming
      `Verified_Download`; automatically require verified download when the production
      checksum manifest becomes available.
    - _Requirements: 6.6-6.10, 7.1-7.3_
  - [x] 21.4 Property test: Property 9 — exact CLI installation and distribution observation are honest
    - Implement `property_09_exact_cli_install_distribution_honest` over at least 100
      artifacts, engine/CLI/descriptor identities, installed states, explicit-local
      flags, production HTTP outcomes, selected CLIs, and claim variants.
    - Admit only one exact non-path-backed baseline and the truthful verified-download
      or exact PATH-fallback result.
    - _Requirements: 6.2-6.10_

- [x] 22. Implement isolated case execution and retry honesty
  - [x] 22.1 Add execution binding, namespace, and attempt models
    - Derive one execution binding from catalog case, artifact manifest/payload, exact
      engine, and baseline; derive distinct workspace/environment/cache namespaces from
      binding plus attempt number.
    - Add ordered contiguous attempts, assertion/infrastructure outcome variants,
      aggregate timing, final outcome, and safe diagnostics with canonical decoding.
    - _Requirements: 6.11-6.19, 12.14, 12.19-12.22_
  - [x] 22.2 Add one-service bounded fan-out and cleanup adapter
    - Start one exact target service after all pre-engine gates, clone every case from
      the common immutable baseline, dispatch with catalog-bounded concurrency, retain
      results by stable index, isolate all mutable state, and stop/reap once on complete
      or abort.
    - Preserve sibling workspaces after one failure and record zero/multiple target
      starts, baseline materializations, cross-case state, or cleanup failures as
      atomic failures.
    - _Requirements: 6.1, 6.3, 6.11-6.15, 6.19-6.22_
  - [x] 22.3 Add the closed retry state machine
    - Permit only declared orchestration-transport, immutable-remote-fetch, and
      runner-capacity infrastructure classes within each case's attempt bound.
    - Make assertion failure absorbing; retain every failed attempt; require identical
      artifact/engine/baseline/case identity; reject catch-all transient failures,
      attempt gaps, retry after assertion failure, or a retry needing new shared work.
    - _Requirements: 6.16-6.18, 12.14, 12.20-12.26_
  - [x] 22.4 Property test: Property 10 — fan-out uses one engine and one immutable baseline
    - Implement `property_10_fanout_one_engine_one_baseline_isolated` over at least 256
      catalogs, schedules, mutable namespace assignments, failures, cross-access
      probes, start/install/reap counts, and completion orders.
    - Compare with an immutable shared-ID/unique-namespace model; require one target
      engine and one baseline with total isolation and cleanup.
    - _Requirements: 6.1, 6.3-6.5, 6.11-6.15, 6.19-6.22_
  - [x] 22.5 Property test: Property 11 — retry history cannot erase failure or duplicate work
    - Implement `property_11_retry_history_absorbing_and_reused` over at least 256
      policies and attempt sequences; compare with an absorbing assertion-failure list
      fold.
    - Accept only permitted infrastructure retries on unchanged shared identities and
      reject every assertion retry or duplicate artifact/engine/baseline event.
    - _Requirements: 6.16-6.18_

- [x] 23. Implement the fixed exact-engine case programs
  - [x] 23.1 Add the common harness and Core generated API programs
    - Dispatch all 17 pinned sdk-sdk subject checks and no harness-self check; map each
      result only to reviewed harness capabilities.
    - Add scalar, enum, input, object, interface, nullable, list-object, expected-type,
      and Void Core programs through the public generated Rust API and exact packaged
      SDK.
    - _Requirements: 3.6-3.9, 7.5-7.7, 7.10, 7.14_
  - [x] 23.2 Refactor the complete Feature 5 matrix onto the shared service/baseline
    - Retain resolution, empty/existing/no-generate initialization, operations,
      checked/legacy runtime, generated-lock-toolchain negatives, path/ownership
      negatives, and redaction with exact existing production assertions.
    - Remove per-case content/service/install construction and return typed safe
      observations, operation provenance, and timings through the umbrella executor.
    - _Requirements: 3.10, 6.1-6.22, 7.17_
  - [x] 23.3 Add the complete Feature 6 packaged authoring programs
    - Exercise the packaged self-consumer and initialization/development/generation/
      loading/execution/dependency lifecycle plus constructor, sync, async, state,
      Core, self, dependency, interface, enum, default, error, panic, cancellation,
      and concurrent calls through production TypeDef/dispatcher paths.
    - Resolve SDK content only from the artifact and return capability-local assertions
      without checkout paths or fixture-only dispatcher substitutes.
    - _Requirements: 3.11, 7.8-7.9, 7.11_
  - [x] 23.4 Add the five Feature 7 standalone-client programs
    - Run initialized local client, immutable pinned remote dependency client, schema
      regeneration, public Core query, and namespaced bound-module query outside the
      repository Cargo workspace.
    - Prove authored preservation/owned change, exact Git revision, packaged SDK
      dependency, public generated APIs, and no ambient path dependency.
    - _Requirements: 3.12, 7.7, 7.12-7.15_
  - [x] 23.5 Add the nine definitive Go-client observable programs
    - Execute equivalent Rust directory, Git, container, mutation, list, and typed
      execution-error cases through public Rust APIs and compare exact observable
      results/error fields.
    - Keep Go source as authority evidence only; do not build or run the Go SDK suite.
    - _Requirements: 3.13, 7.1-7.4, 7.17-7.18_

- [x] 24. Checkpoint: baseline, executor, retry, and fixed case construction are green
  - Run formatting; locked baseline/connector/execution model tests and Properties
    9–11; fixed case-program fixture tests; engine-free Go graph/registry tests;
    source-policy tests; and focused warning-denied Clippy/rustdoc.
  - Exercise all case programs against direct models, recording transports, small
    fixtures, and AST graph seams only. Do not start the Exact_Target or smoke engine.
  - Record commands, timings, Cargo counts, fixture/asset decisions, fixed inventory
    digest, and proof of one service/baseline construction site.
  - Require no Dagger command, engine, module invocation, other SDK suite, target
    artifact work, unscoped generation, or distribution build.
  - _Requirements: 3.6-3.13, 6.1-6.22, 7.1-7.15, 11.1-11.20_
  - Checkpoint evidence: `sdk/rust/completeness/evidence/conformance-baseline-executor-checkpoint.json`
    records the exact installed-baseline model, one-service isolated fan-out, absorbing
    retry state machine, canonical 60-program registry, and Properties 9–11 at 256
    cases each. The complete engine-free matrix passed on macOS/arm64 and the dedicated
    Linux/amd64 XL host with 156 native tests passed and only the two intentional
    helper/exact-engine ignores. The transferred Linux tree was byte-checked before
    execution, contained zero AppleDouble sidecars, and used a fresh Cargo target
    namespace so compile-time repository paths could not reference an earlier tree.
    No Dagger command, engine, module, foreign SDK, target artifact, unscoped
    generation, or distribution build ran; Windows remains deferred to ultimate SDK
    sign-off.

- [x] 25. Complete the additional integration assertion fixtures and observable parity
  - [x] 25.1 Implement every remaining applicable reviewed assertion program
    - Add Rust-owned fixtures for every applicable integration assertion not already
      covered by fixed case families, reusing one program for exact equivalent
      capability sets only when fixture context and observable predicate match.
    - Execute through public Rust client, generated Core, module authoring, packaged
      runtime, or CLI boundaries as assigned. Do not instantiate foreign SDKs or copy
      their language mechanisms.
    - Add catalog/registry totality tests proving every applicable assertion has one
      executable reviewed fixture and every fixture claims only routed capabilities.
    - _Requirements: 3.5, 3.14-3.24, 7.1-7.4, 7.16-7.18_
  - [x] 25.2 Property test: Property 12 — authority mechanisms translate to observable Rust contracts
    - Implement `property_12_authority_translates_to_observable_rust` over at least 100
      authority mechanisms, Rust effects, predicates, equivalence decisions,
      assertion/case outcomes, and drift mutations.
    - Preserve observable results, reject copied foreign mechanisms/unrouted effects,
      report exact drift, and fail completeness for every unproved applicable claim.
    - _Requirements: 7.1-7.4, 7.16-7.18_
  - [x] 25.3 Property test: Property 13 — common harness and standalone clients remain bounded
    - Implement `property_13_harness_and_clients_bounded` over at least 100 harness
      inventories, claim maps, client workspace/dependency states, and foreign-suite
      additions.
    - Require exactly 17 mapped subject checks, exclude harness-self/client-generation
      overclaiming, and require standalone external workspaces with immutable SDK
      dependencies.
    - _Requirements: 7.5-7.7_
  - [x] 25.4 Property test: Property 14 — module authoring covers the complete production semantic matrix
    - Implement `property_14_module_authoring_complete_semantic_matrix` over at least
      100 valid module inputs and required-case outcome sets.
    - Compare with the exact lifecycle/dispatch/handle/error/concurrency required set
      and require the packaged self-consumer to use artifact SDK content only.
    - _Requirements: 7.8-7.9, 7.11_
  - [x] 25.5 Property test: Property 15 — Core and standalone-client cases use public generated APIs
    - Implement `property_15_core_and_clients_use_public_generated_apis` over at least
      100 Core shape, remote revision, schema-regeneration, authored/owned tree, and
      query-route observations.
    - Require every selected Core shape, immutable remote, owned-only regeneration,
      public Core query, and generated namespaced module query.
    - _Requirements: 7.10, 7.12-7.15_

- [x] 26. Extend engine-free checkpoint planning and evidence accounting
  - [x] 26.1 Add closed Feature 8 checkpoint actions and phase budgets
    - Extend `dagger-sdk-engine/src/checkpoint.rs` with only the Feature 8 Rust package,
      named test, source-policy, direct-Go signoff-adapter, native-evidence aggregation,
      Cargo Deny, documentation, and clean-output actions required by the design.
    - Keep Dagger, engine, module, network graph, another SDK, unscoped generation,
      target artifact/scan, distribution, and arbitrary shell actions unrepresentable.
      Keep the preflight as a separately approved non-local infrastructure record.
    - _Requirements: 11.1-11.11, 11.16-11.18_
  - [x] 26.2 Add change-triggered evidence reuse, timings, counts, and closure gating
    - Bind every action to its owning input, prior passed observation, elapsed budget,
      Cargo invocation expectation, generated-asset reuse/refresh decision, and
      forbidden-event observation.
    - Reuse matching passed evidence; schedule missing/failed/stale actions; terminate
      an over-budget phase distinctly; require complete format/check/test/Clippy/
      rustdoc/Cargo Deny/source/evidence/native-matrix closure; and never claim SDK
      sign-off.
    - _Requirements: 11.10-11.20_
  - [x] 26.3 Property test: Property 21 — checkpoints are engine-free by construction
    - Implement `property_21_checkpoints_engine_free_by_construction` over at least 256
      action/package/target expansions, asset states, exception requests, and forbidden
      boundaries.
    - Admit only the closed direct model and adapter fixtures; reject every engine,
      Dagger, module, other SDK, generation, distribution, target, or network proposal.
    - _Requirements: 11.1-11.11, 11.16-11.18_
  - [x] 26.4 Property test: Property 22 — checkpoint evidence is timed, counted, reusable, and complete
    - Implement `property_22_checkpoint_evidence_timed_counted_reusable_complete` over
      at least 256 plans, prior observations, outcomes, timings, Cargo counts, asset
      decisions, and closure-domain mutations.
    - Compare with an exact required-action/change-trigger model; accept only complete
      current bounded evidence with no sign-off claim.
    - _Requirements: 11.12-11.15, 11.19-11.20_

- [x] 27. Derive the one atomic digest-bound verdict
  - [x] 27.1 Add immutable run-plan and complete raw observation models
    - Bind target, reachable Subject_Revision, Linux/amd64, host profile/preflight,
      Build/Import plan, closure, catalog, network policies, concurrency, total budget,
      artifact/security/platform identities, exact engine, baseline, counters, timings,
      attempts, and forbidden events.
    - Distinguish one preflight smoke record, one sign-off Orchestration_Engine
      invocation, and exactly one Exact_Target_Engine start. Reject dirty source,
      unknown network, zero timing, unsafe diagnostic, and undecodable excess input.
    - _Requirements: 12.1-12.19, 12.23-12.29_
  - [x] 27.2 Add total passed/failed verdict derivation
    - Recompute every digest/relation, validate Build/Import counters, one target
      engine/baseline, zero closure replay/unrelated work, complete policy-compliant
      case histories, platform/security gates, canary absence, and exact capability
      claim set.
    - Return one canonical failed verdict for every decodable invalid observation with
      no successful subset/status change. Return a passed verdict only when every gate
      succeeds. Hash the complete verdict with its digest field omitted and recheck on
      decode.
    - _Requirements: 12.1-12.35_
  - [x] 27.3 Add canonical verdict JSON and neutral Markdown rendering
    - Render exact phase identities, artifact/build/import/engine/baseline counts,
      phase timings, case attempts, platform/security closure, and safe diagnostics.
    - Distinguish implementation, platform, security, applicability, and exact-engine
      closure; never label justified `Inapplicable` as implemented or omit an earlier
      assertion failure.
    - _Requirements: 12.1-12.35_
  - [x] 27.4 Property test: Property 23 — verdict binds every identity, counter, outcome, and timing
    - Implement `property_23_verdict_binds_all_identities_counts_outcomes_timings` over
      at least 256 complete observations, declaration orders, and single-field
      mutations.
    - Require deterministic equality under ordering and a changed digest for every
      bound semantic mutation.
    - _Requirements: 12.1-12.19_
  - [x] 27.5 Property test: Property 24 — sign-off admission is atomic and fail-closed
    - Implement `property_24_signoff_atomic_fail_closed` over at least 256 passed and
      arbitrarily malformed observation trees covering every failure class.
    - Compare with an independent all-gates conjunction; every missing/skipped/failed/
      stale/duplicate/leak/overclaim/unrelated condition must yield one failed verdict,
      zero admitted subset/status change, and a blocked release gate.
    - _Requirements: 12.20-12.35_

- [x] 28. Checkpoint: complete Feature 8 policy and execution models are green
  - Run formatting; locked additional assertion, checkpoint, run-plan, and verdict
    tests; Properties 12–15 and 21–24; complete fixed/error fixtures; engine-free Go
    signoff graph tests; Cargo Deny; source-policy tests; and warning-denied
    Clippy/rustdoc.
  - Compile the exact applicability/assertion/case artifacts and assemble current
    closure/platform/security fixtures without replaying matching child evidence.
  - Record commands, phase timings, Cargo counts, generated-asset decisions, artifact
    digests, and clean-output status. This is the final broad engine-free model
    checkpoint, not SDK sign-off.
  - Require no Dagger command, engine, module invocation, other SDK, target build/scan,
    unscoped generation, or distribution build.
  - _Requirements: 1.1-1.18, 3.1-3.24, 4.1-4.19, 6.1-6.22, 7.1-7.18, 11.1-11.20, 12.1-12.35_
  - Checkpoint evidence: `sdk/rust/completeness/evidence/conformance-policy-execution-checkpoint.json`
    records the closed observable-program registry, engine-free checkpoint planner,
    atomic verdict model, commands, timings, Cargo counts, scoped generated-asset
    decision, current closure/platform/security fixture assembly, and artifact
    identities. Properties 12–15 passed at 128 cases each and Properties 21–24 at 256
    cases each on macOS/arm64 and the dedicated isolated Linux/amd64 runner; the Linux
    native observation remained byte-identical to its current 156-test evidence and
    macOS native evidence was reused because its owning inputs did not change. Cargo
    Deny passed on both platforms with only the existing reviewed duplicate-version
    and `serde_graphql_input` metadata warnings. No Dagger command, engine, module,
    foreign SDK, target artifact/scan, unscoped generation, or distribution build ran;
    this evidence does not claim SDK sign-off.

- [ ] 29. Wire completeness transitions, reports, and durable operator documentation
  - [ ] 29.1 Add Feature 8 evidence admission and status derivation
    - Admit only one passed atomic verdict plus its exact scope, closure, platform,
      security, artifact, and catalog identities. Derive `Implemented`,
      `Idiomatic_Equivalent`, or justified `Inapplicable` through Feature 1 policy for
      only the exact proved capability set.
    - A failed/missing verdict must retain every unsupported blocker. Reject stale
      verdicts, unproved assertions, inapplicability without decision evidence, and any
      direct ledger edit.
    - _Requirements: 1.13-1.18, 7.17-7.18, 12.30-12.35_
  - [ ] 29.2 Add honest Feature 8 completeness reporting
    - Render independent applicability, implementation, native-platform, security, and
      exact-engine sections; include neutral status counts, remaining blockers,
      artifact/verdict digests, timings, and reproducibility result.
    - Update checked report/inventory/ledger artifacts only from admitted transitions
      and require a clean reproducible diff.
    - _Requirements: 1.17-1.18, 12.31-12.35_
  - [ ] 29.3 Write the durable conformance and sign-off workflow
    - Add `sdk/rust/CONFORMANCE_SIGNOFF.md` covering applicability review, engine-free
      checkpoints, native evidence, host preflight, Namespace as a replaceable example,
      artifact build/export/import after restart, exact sign-off, counters/timings,
      scanner/database identity, failure inspection, and clean report reproduction.
    - Update `ARCHITECTURE.md`, `CONTRIBUTING.md`, completeness README, umbrella
      requirements, and relevant workflow docs. Use placeholders rather than personal
      paths/account/box IDs and distinguish Orchestration_Engine from
      Exact_Target_Engine.
    - _Requirements: 2.19-2.20, 5.10-5.18, 10.10-10.17, 11.1-11.20, 12.30-12.35_

- [ ] 30. Add native CI aggregation and record Feature 8 implementation closure
  - [ ] 30.1 Add the engine-free Linux/macOS/Windows workflow
    - Create `.github/workflows/rust-sdk-platform.yml` with read-only permissions,
      Rust 1.97.1, committed lockfiles, native package/fixture tests, bounded canonical
      observation upload, and no Dagger/Docker/engine/other SDK work.
    - Add an aggregation job which verifies exact source/toolchain/test identities and
      assembles the matrix through the production Rust model; reject missing, stale,
      skipped, failed, or duplicate native evidence.
    - _Requirements: 8.1-8.21, 9.25_
  - [ ] 30.2 Run and record the complete change-triggered engine-free gate
    - Reuse matching checkpoint observations and execute only missing/failed/stale
      format, locked check/test, Clippy, rustdoc, Cargo Deny, source policy, direct-Go,
      platform, security, documentation, and clean-output domains.
    - Assemble the exact six-child closure bundle plus current generated assets,
      native matrix, and Rust security identity. Prove zero engine/Dagger/module/
      another-SDK/target/replay events and record commands, timings, Cargo counts, and
      reuse decisions.
    - _Requirements: 4.1-4.19, 8.1-8.21, 9.1-9.10, 9.24-9.25, 11.1-11.20_
  - [ ] 30.3 Add closure/report/documentation regression tests
    - Pin canonical implementation-closure JSON and neutral Markdown, exact phase
      separation, provider-neutral preflight wording, single-artifact/engine/baseline
      promises, and Feature 9's requirement for a passed Feature 8 verdict.
    - Ensure implementation closure alone leaves exact-engine blockers and cannot
      render SDK sign-off complete.
    - _Requirements: 4.13-4.19, 11.19-11.20, 12.30-12.35_

- [ ] 31. Complete the production one-artifact, one-engine sign-off facade
  - [ ] 31.1 Wire the canonical run plan and evidence inputs into `rust-sdk-dev`
    - Add a no-selector top-level sign-off function which accepts only canonical Rust
      plan/catalog/closure/platform inputs plus optional artifact file according to the
      declared strategy.
    - Validate input digests before graph construction, reject an incomplete catalog
      or closure before target work, use one pinned Orchestration_Engine invocation,
      and return typed raw observations rather than a Go-computed pass.
    - _Requirements: 3.1-3.24, 4.1-4.19, 5.1-5.20, 12.1-12.19_
  - [ ] 31.2 Connect artifact, scan, service, baseline, and complete case fan-out once
    - Build or import one artifact, pass the same payload to scanning and target
      import, start one exact target service, materialize one baseline, execute the
      complete catalog with bounded isolation/retries, stop/reap, and return all
      counters/timings/attempts/security observations.
    - Remove or fence old feature-local entrypoints which could construct a second
      service, installation, artifact, or partial passing verdict. Focused selectors
      may remain development-only but cannot produce admissible evidence.
    - _Requirements: 5.1-5.20, 6.1-6.22, 7.1-7.18, 9.14-9.23, 12.1-12.35_
  - [ ] 31.3 Add total engine-free construction audits for the final graph
    - Prove one artifact materialization branch, one scanner payload edge, one service
      creation, one baseline install, complete fixed/dynamic case registry, bounded
      fan-out, one cleanup edge, all raw observation fields, and zero unrelated SDK or
      distribution paths by AST/fixture inspection.
    - Keep this audit engine-free; exact behaviour is reserved for Task 32.
    - _Requirements: 5.1-5.20, 6.1-6.22, 11.1-11.20, 12.1-12.35_

- [ ] 32. Final checkpoint: produce one imported exact-target SDK sign-off verdict
  - Refresh the provider-neutral host preflight only if its profile, smoke tool/engine,
    container daemon, or host class identity changed. Require it to pass before target
    artifact work and stop its smoke engine.
  - Require the complete current engine-free implementation closure, native platform
    matrix, Rust security/provenance evidence, case catalog, and clean committed
    Subject_Revision before starting the final workflow.
  - Build the Linux/amd64 Exact_Target artifact once without starting its target
    service, export the real bundle to the persistent host workspace, record component
    and phase counters/timings, and terminate the producing session.
  - In a fresh invocation, import and verify those exact bytes with zero component
    builds, scan that payload, start exactly one Exact_Target_Engine, materialize one
    installed Rust baseline, execute every isolated required case, and stop/reap once.
    Do not run the complete Go SDK, target integration suite, another SDK, unrelated
    generation, or a distribution build.
  - Feed raw observations to the Rust verdict model. Require one passing atomic verdict
    bound to target, Subject_Revision, host/preflight, artifact bytes, closure, catalog,
    platform, security, all attempts/counts/timings, and zero leak/duplicate/unrelated
    event. A failure produces no partial status change and remains the final result.
  - Derive Feature 1 transitions and checked reports from that verdict, run the clean
    reproducibility gate, update task evidence with exact commands/timings/digests, and
    confirm Feature 9—not Feature 8—owns publication and stable release presentation.
  - _Requirements: 1.17-1.18, 2.1-2.20, 3.1-3.24, 4.1-4.19, 5.1-5.20, 6.1-6.22, 7.1-7.18, 8.20-8.21, 9.11-9.25, 10.1-10.17, 12.1-12.35_

## Final SDK Sign-off Gate

Task 32 is the only implementation task authorized to run the Feature 8 exact-target
SDK sign-off. Task 1's smoke engine is an earlier infrastructure-only preflight and
claims no SDK capability. Tasks 4, 8, 12, 16, 20, 24, and 28 are engine-free local
checkpoints. Task 30 records engine-free implementation closure and native CI evidence;
it is not release sign-off.

The authoritative verdict uses the imported artifact path so artifact production and
SDK execution are separated across a host/session restart. The artifact-producing
invocation builds each component at most once and starts no Exact_Target_Engine. The
authoritative invocation imports once, builds no component, starts one
Exact_Target_Engine, installs one Rust baseline, and executes the complete catalog.

## Task Dependency Graph

```json
{
  "1": [],
  "2": ["1"],
  "3": ["2"],
  "4": ["1", "2", "3"],
  "5": ["3", "4"],
  "6": ["5"],
  "7": ["5", "6"],
  "8": ["5", "6", "7"],
  "9": ["7", "8"],
  "10": ["9"],
  "11": ["10"],
  "12": ["9", "10", "11"],
  "13": ["2", "4"],
  "14": ["2", "4"],
  "15": ["14"],
  "16": ["13", "14", "15"],
  "17": ["14", "16"],
  "18": ["17"],
  "19": ["14", "15", "17", "18"],
  "20": ["17", "18", "19"],
  "21": ["10", "18", "20"],
  "22": ["10", "21"],
  "23": ["10", "21", "22"],
  "24": ["21", "22", "23"],
  "25": ["7", "9", "10", "23", "24"],
  "26": ["11", "13", "14", "15", "17", "22", "25"],
  "27": ["11", "13", "14", "15", "17", "19", "22", "25"],
  "28": ["25", "26", "27"],
  "29": ["5", "7", "11", "13", "14", "15", "27", "28"],
  "30": ["13", "14", "15", "26", "28", "29"],
  "31": ["18", "19", "21", "22", "23", "25", "27", "28"],
  "32": ["1", "29", "30", "31"]
}
```

The critical path is:

```text
1 → 4 → 5 → 7 → 9 → 10 → 11 → 17 → 18 → 21 → 22 → 23 → 25 → 27 → 28 → 29 → 30 → 31 → 32
```

Platform/security policy work (13–16) can proceed after foundations and joins the
artifact/security path at Task 17. Checkpoint planning (26) joins the final engine-free
gate at Task 28. No task may use parallel implementation to mutate the same checked
evidence artifact without first splitting ownership by file and digest.

## Notes

- Requirements and design are approved. This plan is the final consent-gated Kiro
  artifact before implementation.
- The first top-level implementation task builds and validates the provider-neutral
  preflight on Namespace. Namespace remains a replaceable execution host, not a
  repository or SDK dependency.
- Local checkpoints at Tasks 4, 8, 12, 16, 20, 24, and 28 are strictly engine-free.
  Matching current evidence is reused; complete coverage is required, blind replay is
  not.
- Task 32 alone performs exact-target SDK sign-off. It is intentionally import-first
  for the authoritative verdict: build/export once, restart, import with zero builds,
  then start one target engine and install one baseline.
- The Orchestration_Engine is infrastructure needed to evaluate the Dagger graph. The
  Exact_Target_Engine is the product under test. Both identities/counts are recorded;
  only the latter is the exact-one target-service invariant.
- The 1,081 authority rows are reviewed individually but need not create 1,081 engine
  calls. Exact rows may share a reviewed assertion and case only when observable
  predicate and fixture context are equal.
- Feature-local sign-off types from Features 6 and 7 are migration inputs. Feature 8
  removes parallel release-verdict paths after their callers use the umbrella model.
- The definitive Go SDK and selected Dagger integration tests establish observable
  authority. Public Rust API shape and implementation remain idiomatic Rust.
- Stable property function identifiers preserve traceability. Per the repository's
  approved documentation policy, implementation source comments explain enduring WHY
  invariants and do not contain feature/task labels.
- Every property task uses `proptest` with the iteration minimum stated in that task.
  Expensive Cargo, Docker, scanner, network, Dagger, and engine operations remain
  outside randomized loops.
- Checkpoint PR cadence is deliberately four top-level tasks: 1–4, 5–8, 9–12, 13–16,
  17–20, 21–24, 25–28, and 29–32. A checkpoint may be committed independently; the
  exact sign-off verdict remains atomic and cannot be stacked from partial passes.
- Any conformance-discovered implementation defect is routed back to its owning
  Feature 2–7 capability and fixed with matching engine-free tests before sign-off is
  retried. Feature 8 does not conceal defects by reclassifying them.
- Cargo Deny and unchanged security jobs are reused at intermediate checkpoints by
  owning-input digest but are always current at Feature 8 implementation closure.
- No checked evidence contains personal paths, account identifiers, provider box IDs,
  live credentials, or Secret_Canary_Set values.
