# Implementation Plan

- [x] 1. Establish the exact transport contract and Rust test foundations
  - [x] 1.1 Generalize completeness feature-scope validation
    - Replace Feature 2 constants embedded in `contract.rs` and `traceability.rs` with
      a data-driven `FeatureScopePolicy` covering heading, status IDs, scope digest,
      policy IDs, and the expected prior blocking owner of each row.
    - Preserve Feature 2's accepted declaration exactly through a descriptor and golden
      fixtures; reject duplicate, reordered, omitted, extra, malformed, and cross-owner
      declarations without weakening its existing integrity checks.
    - Add Feature 3's exact 32 status IDs and digest
      `sha256:0b4246157f75b8ce179d8fec3476256fa939ccdf69d29d1fcafaf93f160013b3`.
    - _Requirements: 1.1, 1.2, 1.4, 1.8, 1.9_
  - [x] 1.2 Register the transport authority and policy inventory
    - Add the approved requirements source to `authorities.json` and extract all 26
      transport policy anchors by exact stable ID and normalized statement.
    - Add the 21 Feature 3-owned and 11 Feature 2-owned prior-blocker expectations;
      keep both Feature 8-only rows outside this scope.
    - Add digest-fenced source/test routing candidates without changing a status until
      its routed implementation, test, and target evidence exists.
    - Add fixtures proving an unrelated `sdk-sdk` result cannot satisfy a transport
      evidence route.
    - _Requirements: 1.2-1.12_
  - [x] 1.3 Register minimal production and development dependencies
    - Add workspace-pinned `opentelemetry`, minimally featured `opentelemetry_sdk`,
      `tracing-opentelemetry`, `zip`, and `fs4`; keep the OpenTelemetry family on one
      compatible version line and disable unused SDK signals/runtime/exporters.
    - Retain `async-trait` only for the existing object-safe `Connector` and
      `EngineConnection` boundaries; use native `async fn` for new statically dispatched
      private traits.
    - Update the locked graph and cargo-deny policy where required; preserve
      Apache-2.0 licensing, Rust 1.97.1, edition 2024, publishing boundaries, and
      `unsafe_code = "deny"`.
    - _Requirements: 4.13-4.15, 9.7-9.12, 14.9, 14.20_
  - [x] 1.4 Add shared transport strategies and recording components
    - Add valid-first `proptest` strategies for process snapshots, native paths,
      target descriptors, byte streams/chunking, manifests, archives, GraphQL values,
      version identities, diagnostics, and phase-specific failure schedules.
    - Add statically dispatched recording provisioner, launcher, transport, clock, and
      HTTP components plus event logs; do not add mutable production globals or a
      runtime `Arc<dyn Trait>` dependency graph.
    - Persist minimized regressions and centralize the 256-case pure / 128-case I/O
      defaults while keeping every property at or above 100 generated cases.
    - _Requirements: 4.13-4.15, 14.1-14.12_
  - [x] 1.5 Property test: Property 1 — exact feature-scope extraction
    - Implement a reference-contract `proptest` with at least 256 generated mutations
      of status IDs, order, digest, policy IDs, and normalized statements; accept only
      the exact declaration.
    - Test identifier: `property_01_exact_feature_scope_extraction`.
    - _Requirements: 1.1-1.3_
  - [x] 1.6 Property test: Property 2 — evidence-closed and owner-correct transitions
    - Implement a reference-status `proptest` with at least 256 candidate transitions
      across prior owners, implementation/test/target evidence, residual blockers, and
      out-of-scope rows; require the approved cross-feature owner map and complete
      routed evidence.
    - Test identifier: `property_02_evidence_closed_owner_correct_transitions`.
    - _Requirements: 1.4-1.12_

- [x] 2. Checkpoint: contract, dependencies, and test scaffolding are green
  - Run formatting, locked checking, completeness unit/property tests, clippy, and
    cargo-deny; require Feature 2's accepted scope to remain byte-for-byte equivalent,
    all 26 policies to extract exactly, and no pre-existing capability status to change at
    this checkpoint.

- [x] 3. Implement exact targets, source planning, native discovery, and descriptors
  - [x] 3.1 Generate and fence the exact runtime target
    - Generate private engine version, CLI version, and Dagger revision constants from
      `completeness/target.json`; fail checking when either generated output or target
      metadata drifts independently.
    - Parse the target once into validated SemVer and revision newtypes and reject an
      internal mismatch before cache, process, or network work.
    - _Requirements: 4.1, 4.2, 12.15_
  - [x] 3.2 Extend the single process-input snapshot and source decision
    - Capture session port/token, explicit-local CLI, runner inputs, propagation inputs,
      native PATH/PATHEXT/home/current-directory observations, and safe observation
      failures once after explicit-connection selection.
    - Express implicit selection as one enum decision—Existing Session, Explicit Local
      CLI, or compiled release—rather than a candidate loop; retain the selected value
      across all later failures and environment changes.
    - Keep irrelevant observations as data so an Existing Session or absolute local
      path cannot fail because an unused discovery input was unavailable.
    - _Requirements: 2.1-2.13_
  - [x] 3.3 Implement typed Existing Session validation and ownership
    - Validate native port text, integer range `1..=65535`, and token presence/non-empty
      shape into secret-bearing session parameters without formatting raw values.
    - Build the externally owned session resource marker and ensure close/abort release
      only SDK transport state and can never signal the external engine.
    - Add fixed tests for token-without-port, non-native text, boundaries, missing/empty
      token, formatting, and external ownership.
    - _Requirements: 3.1-3.8_
  - [x] 3.4 Implement pure native explicit-local and PATH discovery
    - Add home-marker expansion, path-shaped validation, and bare-name lookup over the
      captured native snapshot, including Windows PATHEXT and native symlink resolution.
    - Return an owned unmanaged `LaunchExecutable`; make an empty present value and
      every lookup/executable-shape failure typed and terminal for that selected source.
    - Keep explicit-local and compatibility PATH functions separate so the former can
      never enter provisioning and the latter can resolve only the canonical Dagger name.
    - _Requirements: 3.9-3.15, 7.3, 7.7, 7.8_
  - [x] 3.5 Implement the exact platform/archive descriptor model
    - Add normalized OS/architecture/archive enums and a total mapping for Linux,
      macOS, and Windows crossed with amd64/arm64.
    - Construct the exact release basename, expected `dagger`/`dagger.exe` member, and
      fixed HTTPS `dl.dagger.io` manifest/archive URLs; reject unsupported targets
      before side effects.
    - Add an exhaustive six-target table test and fixed unsupported-target cases.
    - _Requirements: 4.3-4.15, 14.3_
  - [x] 3.6 Add the foundational typed discovery and target errors
    - Add non-exhaustive, cloneable safe-kind errors for Existing Session, native
      discovery, unsupported platform, descriptor construction, and target drift.
    - Hand-write credential-safe `Display`/`Debug`; retain only safe sources and path
      roles, never raw environment values.
    - _Requirements: 3.2-3.6, 3.12-3.13, 4.2, 4.9, 11.1-11.2, 11.21-11.22_
  - [x] 3.7 Property test: Property 3 — source precedence is a pure reference function
    - Implement a truth-table/reference `proptest` with at least 256 explicit/session/
      local/download presence combinations, selected-source failures, and post-snapshot
      mutations; compare the unique decision and zero lower-source events.
    - Test identifier: `property_03_source_precedence_reference_function`.
    - _Requirements: 2.1-2.13_
  - [x] 3.8 Property test: Property 4 — Existing Session validation is total and secret-safe
    - Generate at least 256 native port/token combinations and external close schedules;
      compare typed validation to a small reference parser and search all ordinary
      formatting while asserting zero engine-shutdown events.
    - Test identifier: `property_04_existing_session_total_secret_safe`.
    - _Requirements: 3.1-3.8_
  - [x] 3.9 Property test: Property 6 — platform descriptors are exact and side-effect free
    - Exhaust the six supported pairs and run at least 256 generated unsupported/drift
      cases against a descriptor reference table; assert exact URLs/names/members and
      zero cache, HTTP, and process events on rejection.
    - Test identifier: `property_06_platform_descriptors_exact_side_effect_free`.
    - _Requirements: 4.1-4.15_

- [x] 4. Checkpoint: target and source foundations are green
  - Run formatting, locked SDK/completeness unit and property tests, clippy, rustdoc,
    public error formatting tests, and cargo-deny; require deterministic source
    snapshots, exact target generation, and no lower-source work on failure.

- [ ] 5. Implement bounded release acquisition and archive validation
  - [ ] 5.1 Add bounded, cancellable HTTP acquisition adapters
    - Stream the manifest with an 8 MiB hard limit and the archive with a 1 GiB
      compressed-byte limit, checking cancellation while reading and returning typed
      HTTP/status/size failures without retaining response secrets.
    - Keep URL construction fixed to the validated release descriptor and prevent
      redirects or caller-supplied release hosts from widening provisioning authority.
    - _Requirements: 5.1-5.4, 5.16-5.18, 11.1-11.6_
  - [ ] 5.2 Parse the release manifest into one exact checksum
    - Parse UTF-8 lines without unbounded allocation, accept only the exact archive
      basename, require one unambiguous SHA-256 value, and reject missing, malformed,
      duplicated, or conflicting matches.
    - Add fixed fixtures for ordinary Dagger manifests, line-ending variants, boundary
      sizes, malformed digests, duplicates, and irrelevant entries.
    - _Requirements: 5.1-5.4, 5.17-5.18_
  - [ ] 5.3 Implement streaming checksum and bounded exact-member extraction
    - Hash every compressed byte before acceptance; extract only `dagger` or
      `dagger.exe` from the descriptor-selected tar.gz/ZIP format into a private file.
    - Reject absent/duplicate members, links, non-regular entries, traversal/absolute
      paths, format corruption, checksum mismatch, and output beyond 1 GiB; remove all
      private state on every exit and never execute an unverified byte.
    - Add archive fixtures at entry/count/path/type/size boundaries for both formats.
    - _Requirements: 5.5-5.18, 14.4_
  - [ ] 5.4 Property test: Property 7 — manifest parsing is bounded and total
    - Generate at least 256 manifests with arbitrary line structure, exact-name
      multiplicity, digest syntax, line endings, and size-boundary metadata; compare
      against a bounded reference parser and assert no panic or archive request on error.
    - Test identifier: `property_07_manifest_parsing_bounded_total`.
    - _Requirements: 5.1-5.4, 5.17-5.18_
  - [ ] 5.5 Property test: Property 8 — archive acceptance is integrity-gated, bounded, and confined
    - Generate at least 128 tar.gz/ZIP entry plans and streamed chunk schedules covering
      digest, member, link, traversal, compressed/extracted limit, and corruption cases;
      assert accepted bytes exactly equal the one verified member and all writes remain
      beneath the private directory.
    - Test identifier: `property_08_archive_integrity_bounded_confined`.
    - _Requirements: 5.5-5.15, 5.17-5.18, 14.4_
  - [ ] 5.6 Property test: Property 9 — provisioning cancellation removes private state
    - Inject cancellation at at least 128 generated manifest, download, checksum,
      extraction, flush, and pre-publication boundaries; assert prompt typed cancellation,
      zero publication/execution, and no surviving private artifact.
    - Test identifier: `property_09_provisioning_cancellation_removes_private_state`.
    - _Requirements: 5.16, 6.20_

- [ ] 6. Implement the native cache publication transaction
  - [ ] 6.1 Add no-follow cache validation and native permissions
    - Use the platform-native cache root and a target-derived entry name; on Unix create
      private directories/files with `0700` semantics and reject symlinks and every
      non-regular executable shape without following them.
    - Validate a cache hit without network access and return an owned execution lease
      that prevents replacement or pruning through process spawn.
    - _Requirements: 6.1-6.6, 14.5-14.6_
  - [ ] 6.2 Add cross-process locked, atomic publication
    - Coordinate publishers with a cache-wide `fs4` lock, download outside the lock,
      then lock, revalidate, flush bytes and metadata, set executable permissions, and
      atomically rename one complete file into place.
    - Treat a concurrent valid winner as success, replace only an invalid exact-target
      entry, release all locks on cancellation/error, and add deterministic helper-
      process tests using barriers rather than timing.
    - _Requirements: 6.7-6.13, 6.20, 14.5-14.6_
  - [ ] 6.3 Add locked, best-effort retention
    - While holding the cache lock, prune only SDK-owned compiled-release entries beyond
      the declared retention bound; preserve the current target, leased executables,
      explicit-local paths, and every unrelated file/directory.
    - Make cleanup failure non-fatal, secret-safe, and observable through diagnostics.
    - _Requirements: 6.14-6.19_
  - [ ] 6.4 Property test: Property 10 — cache validation is no-follow and network-free on hits
    - Generate at least 128 native cache entry shapes and permission states, including
      symlink swaps at controlled boundaries; assert valid hits acquire a lease with
      zero HTTP events and unsafe shapes are never followed or executed.
    - Test identifier: `property_10_cache_validation_no_follow_network_free`.
    - _Requirements: 6.1-6.6_
  - [ ] 6.5 Property test: Property 11 — concurrent publication has one atomic result
    - Generate at least 128 cross-process publisher schedules and failure/cancellation
      points with deterministic barriers; assert observers see no partial executable,
      all successful publishers converge on identical verified bytes, and locks/private
      state are released.
    - Test identifier: `property_11_concurrent_publication_one_atomic_result`.
    - _Requirements: 6.7-6.13, 6.20, 14.5-14.6_
  - [ ] 6.6 Property test: Property 12 — retention is locked, confined, and non-destructive
    - Generate at least 128 cache trees with owned, unrelated, current, leased, and
      adversarial entries plus cleanup failures; assert deletion is confined to eligible
      owned entries and never changes connector success.
    - Test identifier: `property_12_retention_locked_confined_non_destructive`.
    - _Requirements: 6.14-6.19_

- [ ] 7. Checkpoint: provisioning and cache transaction are green
  - Run formatting, locked SDK tests, cross-process cache tests, clippy, rustdoc, and
    cargo-deny; require all archive formats, injected cancellation points, and cache
    schedules to leave no partial or unverified executable.

- [ ] 8. Implement fallback, spawn policy, and complete CLI launch projection
  - [ ] 8.1 Add the finite provisioning fallback policy
    - Permit PATH compatibility fallback only when the release manifest returns HTTP
      403 or 404; emit a safe warning, resolve only the canonical Dagger executable,
      and retain both failures in a compound error when fallback discovery fails.
    - Make every other manifest, archive, integrity, cache, cancellation, and spawn
      failure terminal for its selected source.
    - _Requirements: 7.1-7.13_
  - [ ] 8.2 Add narrow, bounded spawn retry
    - Retry only native `ETXTBSY`, at most ten total spawn attempts, with cancellable
      backoff capped at 100 ms; return the final typed spawn failure with safe attempt
      metadata and never retry any other process error.
    - Preserve the `LaunchExecutable` cache lease until the spawn attempt has safely
      transferred executable ownership to the child.
    - _Requirements: 7.14-7.19_
  - [ ] 8.3 Project the complete CLI launch contract
    - Build arguments/environment from typed values, including the exact runner,
      labels exactly once, W3C child propagation, and collision handling; pipe stdin,
      stdout, and stderr and make ambient inputs explicit.
    - Use statically dispatched concrete production components with generic seams for
      tests; do not introduce trait-object indirection into the connector hot path.
    - _Requirements: 8.1-8.5, 9.10, 14.9_
  - [ ] 8.4 Property test: Property 5 — explicit-local selection is authoritative
    - Generate at least 256 explicit-local values, native discovery snapshots, mutations,
      and failure shapes; compare lookup to the native reference model and assert zero
      provisioning/PATH-compatibility events after selection.
    - Test identifier: `property_05_explicit_local_authoritative`.
    - _Requirements: 3.9-3.15_
  - [ ] 8.5 Property test: Property 13 — fallback follows the finite policy table
    - Generate at least 256 source/stage/status/fallback-result combinations; compare
      events and terminal error composition against the policy table, including the
      exclusive 403/404 manifest transition.
    - Test identifier: `property_13_fallback_finite_policy_table`.
    - _Requirements: 7.1-7.13_
  - [ ] 8.6 Property test: Property 14 — spawn retry is narrow, bounded, and cancellable
    - Generate at least 256 spawn outcome/cancellation sequences using a virtual clock;
      assert retry count, error identity, backoff ceiling, cancellation responsiveness,
      and lease lifetime exactly match the reference state machine.
    - Test identifier: `property_14_spawn_retry_narrow_bounded_cancellable`.
    - _Requirements: 7.14-7.19_
  - [ ] 8.7 Property test: Property 16 — CLI launch projection is complete and collision-free
    - Generate at least 256 runner/label/environment/propagation combinations; compare
      exact argv/env/pipe configuration to a reference projection and assert one value
      per reserved input with deterministic collision resolution.
    - Test identifier: `property_16_cli_launch_projection_complete_collision_free`.
    - _Requirements: 8.1-8.5, 9.10, 14.9_

- [ ] 9. Implement session startup, resource ownership, and diagnostic containment
  - [ ] 9.1 Add typed pending-resource ownership and startup transfer
    - Introduce `PendingResources` as the sole pre-session owner of child, pipe, cache
      lease, and worker handles; make success transfer each resource exactly once and
      make every startup error/cancellation converge through one cleanup path.
    - Use a small Rust helper binary to exercise portable child protocols and ownership
      transitions without shell-specific behavior.
    - _Requirements: 8.6-8.7, 8.14-8.20_
  - [ ] 9.2 Parse the first stdout control record in isolation
    - Read one newline-terminated control record with a 64 KiB limit, validate required
      port/token fields and tolerate unknown fields, then transfer all remaining stdout
      bytes to diagnostics without ever diagnosing control bytes or token values.
    - Return typed EOF, oversize, UTF-8, JSON, field-shape, and range failures through
      `PendingResources` cleanup.
    - _Requirements: 8.8-8.13, 10.1-10.5, 14.7_
  - [ ] 9.3 Add sealed streaming redaction and bounded diagnostic tails
    - Build a token-aware redactor with chunk carry so secrets split across arbitrary
      read boundaries cannot escape; feed stdout remainder and stderr independently
      into fixed 1 MiB tails and an optional fallible/panicking sink.
    - Contain sink failures and panics, record a typed `StreamOutcome` for EOF/read/sink/
      panic results, avoid secret-bearing allocations in ordinary errors, and preserve
      background outcomes for later observation.
    - _Requirements: 10.1-10.20, 14.8, 14.11_
  - [ ] 9.4 Property test: Property 17 — control input is parsed once and never diagnosed
    - Generate at least 128 chunkings of valid and invalid control records, unknown
      fields, boundary lengths, and stdout suffixes; assert one parse, exact suffix
      handoff, no control bytes in any diagnostic sink, and cleanup on rejection.
    - Test identifier: `property_17_control_input_parsed_once_never_diagnosed`.
    - _Requirements: 8.8-8.13, 10.1-10.5, 14.7_
  - [ ] 9.5 Property test: Property 18 — pending resources have one owner and one transfer
    - Generate at least 128 startup/cancellation/failure schedules over every owned
      resource; assert each handle is transferred or cleaned exactly once, never both,
      and no child, task, pipe, or cache lease survives failure.
    - Test identifier: `property_18_pending_resources_one_owner_one_transfer`.
    - _Requirements: 8.6-8.7, 8.14-8.20_
  - [ ] 9.6 Property test: Property 21 — diagnostics are isolated, redacted, bounded, and contained
    - Generate at least 128 arbitrary secret placements, chunk boundaries, channel
      interleavings, over-capacity streams, read failures, sink errors, and sink panics;
      assert zero secret/control leakage, channel isolation, exact tail bounds, and
      continued cleanup/progress.
    - Test identifier: `property_21_diagnostics_isolated_redacted_bounded_contained`.
    - _Requirements: 10.1-10.13, 10.18-10.20, 14.8_
  - [ ] 9.7 Property test: Property 22 — background outcomes remain observable
    - Generate at least 128 worker completion orders and close/read races; assert every
      `StreamOutcome` is retained, reported at most once in deterministic order, and
      cannot be lost merely because it occurs after startup succeeds.
    - Test identifier: `property_22_background_outcomes_remain_observable`.
    - _Requirements: 10.14-10.17, 14.11_

- [ ] 10. Checkpoint: launch and session startup are green
  - Run formatting, locked SDK tests, helper-process integration tests, clippy, rustdoc,
    and cargo-deny; require secret-scanning assertions over all ordinary output and no
    child/task/lease leak across the complete startup failure matrix.

- [ ] 11. Implement local W3C propagation and confined loopback HTTP
  - [ ] 11.1 Add instance-local OpenTelemetry propagation
    - Use `tracing` as the public instrumentation facade, `opentelemetry` for context
      and carrier APIs, `tracing-opentelemetry` for the bridge, and only the minimal
      `opentelemetry_sdk` W3C Trace Context/Baggage propagators.
    - Prefer a valid active tracing/OpenTelemetry context as one coherent source,
      otherwise validate and canonicalize inherited process values; inject child
      environment and each request from fresh carriers without reading or mutating the
      global propagator/provider/exporter.
    - Add integration coverage with one workspace-compatible crate family so dependency
      skew and bridge behavior fail visibly.
    - _Requirements: 9.7-9.12, 9.17, 14.9_
  - [ ] 11.2 Add the private Reqwest loopback transport
    - Build a client with `no_proxy`, redirects disabled, and a connection-only timeout;
      confine requests to `http://127.0.0.1:<validated-port>/query` using HTTP Basic with
      the token as username and an empty password.
    - Send one JSON GraphQL body with fresh W3C headers per execution, treat non-success
      status and invalid GraphQL response shape as typed errors, and retain the complete
      `RawResponse` only in the domain result—not in ordinary formatting.
    - _Requirements: 9.1-9.6, 9.13-9.17_
  - [ ] 11.3 Make request execution structurally at-most-once
    - Keep connection setup/retry policy separate from body transmission; once sending
      begins, never replay the GraphQL request for transport, status, decode, or engine
      errors.
    - Add an adversarial loopback server that counts accepted connections, received
      bodies, redirects, proxy attempts, and truncated/failed replies.
    - _Requirements: 7.20, 14.10_
  - [ ] 11.4 Property test: Property 15 — request transmission is at most once
    - Generate at least 128 server failure schedules before, during, and after body
      receipt; assert the transport observes at most one complete/partial body and the
      connector never converts ambiguous delivery into a replay.
    - Test identifier: `property_15_request_transmission_at_most_once`.
    - _Requirements: 7.20, 14.10_
  - [ ] 11.5 Property test: Property 19 — implicit HTTP is confined and authenticated
    - Generate at least 128 proxy/redirect/port/token/status/body combinations against
      instrumented local servers; assert the only destination is the validated IPv4
      loopback query endpoint, redirects/proxies receive nothing, and Basic auth/body
      framing are exact without diagnostic credential leakage.
    - Test identifier: `property_19_implicit_http_confined_authenticated`.
    - _Requirements: 9.1-9.6, 9.13-9.17_
  - [ ] 11.6 Property test: Property 20 — W3C propagation has coherent precedence and request isolation
    - Generate at least 256 active/attached/inherited valid-invalid context combinations
      and at least 128 concurrent request schedules; compare canonical child/request
      carriers to a local reference and assert no global mutation or cross-request data.
    - Test identifier: `property_20_w3c_propagation_coherent_isolated`.
    - _Requirements: 9.7-9.12, 14.9_

- [ ] 12. Complete the public failure taxonomy and engine-domain mapping
  - [ ] 12.1 Implement layered, cloneable, secret-safe public errors
    - Add non-exhaustive top-level errors and non-exhaustive safe-kind accessors for
      discovery, provisioning, startup, protocol, transport, compatibility, timeout,
      shutdown, and query failures; preserve typed causes with `Arc` where terminal
      close result sharing requires cloneability.
    - Hand-write stable ordinary `Display`/`Debug` that expose safe stages, roles,
      attempts, and semantic identities but never tokens, authorization, raw environment,
      unredacted responses, stdout, or stderr.
    - Remove `eyre` and production `unwrap`/`expect`/panic paths from the connector and
      add catch-unwind/fault-injection coverage over every public operation boundary.
    - _Requirements: 11.1-11.6, 11.21-11.22_
  - [ ] 12.2 Add conservative, lossless `EXEC_ERROR` mapping
    - Require the definitive extension type marker; parse known message/exit-code/
      command/stdout/stderr fields into private `ExecError` state while retaining unknown
      extensions and the original `RawResponse`.
    - Expose typed read-only accessors; if a known field is malformed, retain the full
      response as generic GraphQL failure instead of guessing, panicking, or losing data.
    - _Requirements: 11.7-11.20_
  - [ ] 12.3 Property test: Property 23 — failure taxonomy is total, stable, and panic-free
    - Generate at least 256 leaf failures, source chains, adversarial strings, and public
      operation fault points; assert total kind mapping, clone-equivalent terminal
      results, stable safe formatting, zero secret/output substrings, and no unwind.
    - Test identifier: `property_23_failure_taxonomy_total_stable_panic_free`.
    - _Requirements: 11.1-11.6, 11.21-11.22_
  - [ ] 12.4 Property test: Property 24 — engine-domain mapping is lossless and conservative
    - Generate at least 256 GraphQL responses with error ordering, marker variants,
      valid/malformed known fields, arbitrary extensions, and raw data; assert exact
      `Exec` recognition/accessors or exact generic fallback with unchanged `RawResponse`.
    - Test identifier: `property_24_engine_domain_mapping_lossless_conservative`.
    - _Requirements: 11.7-11.20_

- [ ] 13. Enforce exact target compatibility in the concrete connector
  - [ ] 13.1 Implement constant raw compatibility validation
    - Execute `query RustSdkCompatibility { version }` before exposing a new session;
      normalize and require semantic version `v1.0.0-beta.10` and clean build metadata
      `+25300124`, exactly matching the target revision prefix.
    - Distinguish version mismatch, revision mismatch, and unverified provenance for
      transport/GraphQL/shape, absent, malformed, dirty, or unknown-format evidence;
      never include the raw response in ordinary output.
    - _Requirements: 12.1-12.11_
  - [ ] 13.2 Assemble the stable default connector path
    - Compose the concrete snapshot, discovery, provisioner, launcher, propagation,
      loopback transport, validator, diagnostics, and resource owner behind the existing
      public builder/client surface while retaining compile-time generic test seams.
    - Validate newly launched and compatibility-fallback sessions before success; clean
      up SDK-owned children on rejection, leave Existing Session engines externally
      owned, and expose only an explicit documented bypass for unverified compatibility.
    - _Requirements: 12.12-12.15_
  - [ ] 13.3 Property test: Property 25 — compatibility accepts exactly the declared target
    - Generate at least 256 version strings across SemVer normalization, prerelease,
      build metadata, revision prefix/case/length, dirty/unknown formats, response shapes,
      ownership sources, and bypass state; compare outcomes and cleanup to the exact
      target reference predicate.
    - Test identifier: `property_25_compatibility_accepts_exact_declared_target`.
    - _Requirements: 12.1-12.15_

- [ ] 14. Implement bounded, convergent shutdown
  - [ ] 14.1 Add the single terminal shutdown state machine
    - Close child stdin, wait up to 300 seconds, kill and reap on expiry, join diagnostic
      workers, release connection/cache resources, and aggregate all cleanup outcomes in
      deterministic order without short-circuiting later cleanup.
    - Make close, abort, drop-triggered cleanup, concurrent callers, and startup rollback
      converge on the same owned state; all callers observe the same terminal result and
      Existing Session never receives process control.
    - _Requirements: 13.1-13.17_
  - [ ] 14.2 Add portable shutdown integration cases
    - Extend the Rust helper binary with clean-exit, hung-stdin, ignore-termination,
      delayed-worker, pipe-failure, and already-exited modes; drive time with injectable
      clocks/signals where possible and bound the remaining native integration tests.
    - _Requirements: 13.1-13.17_
  - [ ] 14.3 Property test: Property 26 — shutdown is bounded, exhaustive, and repeatable
    - Generate at least 128 close/abort/drop/concurrent-call schedules and cleanup
      failure combinations using a model state machine; assert each action occurs at
      most once, all applicable actions are attempted, results/order are identical, the
      deadline is bounded, and no owned resource survives.
    - Test identifier: `property_26_shutdown_bounded_exhaustive_repeatable`.
    - _Requirements: 13.1-13.17_

- [ ] 15. Checkpoint: complete runtime connector is green
  - Run formatting, locked SDK unit/property/integration tests, rustdoc, clippy, public
    API/error snapshots, compatibility matrices, and cargo-deny; require the default
    connector to provision, launch, validate, query, and shut down without leak, replay,
    global telemetry mutation, or credential disclosure.

- [ ] 16. Stabilize the public API, documentation, and source contract
  - [ ] 16.1 Document every module and public item at contract depth
    - Add `//!` ownership/invariant documentation to each transport module and `///`
      guarantees, caller assumptions, failure behavior, cancellation, ownership, and
      security notes to every public item; use inline comments only for non-obvious WHY,
      especially no-follow, atomicity, redaction, propagation, replay, and cleanup rules.
    - Do not refer to spec feature/task identifiers in production comments; keep source
      durable by naming the actual external contract or invariant.
    - Add end-to-end rustdoc examples for Existing Session and implicit connection,
      query execution, `ExecError` inspection, diagnostics, tracing propagation,
      compatibility bypass, and explicit shutdown.
    - _Requirements: 4.13-4.15, 14.17-14.24_
  - [ ] 16.2 Fence the intended stable surface
    - Add public API snapshots and `trybuild` compile-pass/fail cases for builders,
      error matching, owned resources, non-Send/borrow misuse where relevant, and the
      absence of accidental provisioning/telemetry internals from the public namespace.
    - Add source-policy tests for public docs, forbidden panic shortcuts, secret-bearing
      derives/formatting, and production comments that reference spec task metadata.
    - _Requirements: 14.17-14.24_
  - [ ] 16.3 Property test: Property 28 — stable surface and documentation preserve the contract
    - Generate at least 256 public-item/source-policy observations and exercise the fixed
      compile matrix; assert snapshots expose only approved symbols, every public item
      has substantive contract docs, examples compile, and forbidden implementation or
      spec metadata is absent from production source.
    - Test identifier: `property_28_stable_surface_documentation_preserve_contract`.
    - _Requirements: 4.13-4.15, 14.17-14.24_

- [ ] 17. Close the completeness ledger with reproducible exact-target evidence
  - [ ] 17.1 Add deterministic transport observation records
    - Define machine-readable evidence for source, acquisition, cache, launch, protocol,
      HTTP, propagation, compatibility, error mapping, and shutdown observations; record
      only values actually asserted by tests and reject stale/unknown evidence shapes.
    - Keep records reproducible and non-live: no credentials, timestamps, host paths,
      ports, network responses, or nondeterministic ordering.
    - _Requirements: 14.1-14.3, 14.12-14.16_
  - [ ] 17.2 Add the isolated exact-target conformance path
    - In a sanitized helper process, clear Existing Session and explicit-local inputs,
      run the stable default connector so it downloads/starts the exact CLI, execute
      generated `Query.version`, and close/reap the session.
    - Assert the observed engine version/revision, ownership, request, propagation,
      diagnostic, and cleanup facts before emitting evidence; keep an explicit offline
      fixture path for deterministic local verification without claiming live provenance.
    - _Requirements: 14.1-14.3, 14.12-14.16_
  - [ ] 17.3 Update Feature 3 completeness candidates truthfully
    - Attach accepted deterministic evidence only to the 58 scoped candidate
      capabilities, derive each status/reason/owner through the policy engine, leave all
      out-of-scope rows—including Feature 8—unchanged, and regenerate committed reports.
    - Add regression fixtures for evidence removal, owner mismatch, partial observation,
      exact observation, target drift, and untouched-ledger identity.
    - _Requirements: 1.1-1.12, 14.1-14.3, 14.12-14.16_
  - [ ] 17.4 Property test: Property 27 — evidence declares what it actually observes
    - Generate at least 256 evidence/observation/target/status combinations; independently
      recompute claims and assert no record can overstate an observation, drifted or
      partial evidence cannot implement a capability, and every out-of-scope row remains
      byte-equivalent.
    - Test identifier: `property_27_evidence_declares_actual_observations`.
    - _Requirements: 14.1-14.3, 14.12-14.16_

- [ ] 18. Final checkpoint: Feature 3 is releasable
  - Run `cargo test -p dagger-sdk`, `cargo test -p dagger-sdk-completeness`,
    `cargo test -p dagger-sdk --doc`, `cargo deny check`, the repository's Rust format,
    clippy, Dagger Rust test, and Dagger Rust completeness commands.
  - Require all 28 property identifiers, portable integration suites, public API/doc
    fences, exact-target evidence, regenerated reports, security checks, and the
    untouched-scope guard to pass with a clean worktree diff.

## Dependency graph

```text
1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 -> 8 -> 9 -> 10
   -> 11 -> 12 -> 13 -> 14 -> 15 -> 16 -> 17 -> 18
```

The sequence is intentionally strict at the checkpoint level: later stages consume
the typed ownership, error, and evidence contracts established earlier. Subtasks within
one implementation task may proceed together only when their shared public types have
settled and their property tests remain independently runnable.

## Notes

- Every property-test subtask is mandatory; none is marked optional.
- Pure/model properties run at least 256 cases. Filesystem, process, HTTP, cancellation,
  and concurrency properties run at least 128 cases, always above the 100-case floor.
- Cross-process publication, process lifecycle, and HTTP behavior use deterministic
  barriers/helper protocols in addition to generated schedules; property testing does
  not replace the portable integration proof.
- Production comments explain the durable contract or invariant and never cite this
  feature name, task number, or property identifier. Stable test identifiers provide
  requirements traceability without leaking planning metadata into runtime code.
- Evidence is accepted only after the observing test passes against the exact pinned
  target; generated status counts are outcomes, never implementation goals.
