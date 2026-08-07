# Implementation Plan

- [x] 1. Establish the exact Feature 2 contract scope and test foundations
  - [x] 1.1 Add source-path-bounded completeness classification
    - Extend `ClassificationSelector` with an exact repository-relative source-path
      predicate and validate it against the selected `SourceItem` set.
    - Replace `baseline/go-client` with non-overlapping, digest-fenced exact-path rules
      covering `client.go`, `dagger.gen.go`, `dag/dag.gen.go`, `fs.go`, each selected
      `engineconn` file, Go tests, examples, and the small mixed-file overrides; fail
      closed on a new or lost match.
    - Add fixtures for exact paths, mixed-source capabilities, overlapping rules, stale
      overrides, and changed rule-expansion digests.
    - _Requirements: 1.1, 1.2, 1.3_
  - [x] 1.2 Implement Feature 2 scope-declaration validation
    - Add `FeatureScopeDeclaration` parsing for the named requirements headings, the
      existing-capability digest, and the two fenced Capability_ID lists.
    - Validate the exact 23 existing IDs, digest
      `sha256:81ad1a4f2efe1604b9091468bd6a6006d598a2a8ae54a94a974acf08d74b8b40`,
      and exact 14 Rust-policy IDs without accepting duplicate, reordered, malformed,
      or extra entries.
    - Keep the parser narrow to the authored convention; unrelated Markdown is not a
      second configuration language.
    - _Requirements: 1.2, 1.3, 1.4_
  - [x] 1.3 Add the Rust lifecycle policy capabilities and correct ownership
    - Add the 14 reviewed `policy/rust-policy/client-*` definitions with pinned spec and
      `sdk/rust/AGENTS.md` anchors, stable fingerprints, and complete source coverage.
    - Route the exact 23 existing rows and 14 policy rows to Feature 2; route every other
      current `go-client` row to Feature 3–9 according to the approved boundaries.
    - Prove ownership-only corrections preserve the prior status, fingerprint,
      implementation/verification/decision evidence, and every authority anchor.
    - Record reviewed inapplicability for Requirements 2.13–2.15 because no closure
      helper remains in the target API.
    - _Requirements: 1.1–1.7, 2.13–2.15_
  - [x] 1.4 Extend downstream status and blocker validation for Feature 2
    - Bind Feature 2 status changes to its parsed declaration and require target-scoped
      implementation plus verification evidence in the same candidate change.
    - Preserve an exact residual blocker wherever a row still depends on Feature 3, 4,
      8, or 9 behaviour; reject local evidence that attempts to erase a sibling gap.
    - Add success/failure fixtures for all Complete_Status forms and for the final
      no-blocker condition.
    - _Requirements: 1.8, 1.9, 1.10, 1.11_
  - [x] 1.5 Register Feature 2 runtime and development dependencies
    - Add direct workspace `url` use for private runner-host validation, enable
      `proptest` for `dagger-sdk`, and add `loom` plus `trybuild` as development-only
      dependencies.
    - Preserve the locked graph, Apache-2.0 workspace licence, pinned Rust toolchain,
      `unsafe_code = "deny"`, cargo-deny policy, and publishing boundaries.
    - Add shared valid-first strategies, deterministic 256-case configuration, a
      checked regression corpus, and recording connection/connector/resource fixtures.
    - _Requirements: 2.3–2.6, 5.3, 10.3, 10.4_
  - [x] 1.6 Property test: Property 1 — exact feature scope and routing preservation
    - Implement a reference-routing `proptest` with at least 256 generated scope
      declarations, path-rule expansions, row states, fingerprints, and evidence sets;
      mutate IDs, digests, routes, and preserved fields independently and in combination.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 1: exact feature scope and routing preservation`
    - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7_
  - [x] 1.7 Property test: Property 2 — complete status is evidence-closed
    - Implement a reference-status `proptest` with at least 256 candidate Feature 2
      transitions across target identity, status-required evidence kind/scope/outcome,
      sibling blockers, and final no-blocker states.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 2: complete status is evidence-closed`
    - _Requirements: 1.8, 1.9, 1.10, 1.11_

- [x] 2. Checkpoint: contract scope and test scaffolding are green
  - Run formatting, locked checking, the completeness crate tests, new Feature 2
    property tests, and clippy; require the ownership-only artifact diff to preserve
    status, fingerprints, and evidence and to leave the Implemented count unchanged.

- [x] 3. Implement public values, typed errors, raw GraphQL, and configuration
  - [x] 3.1 Replace public catch-all errors with stable typed families
    - Implement non-exhaustive `ConfigError`, `ConnectError`, `RequestError`,
      `QueryError`, `CloseError`, and `EngineConnectionError` families with intentional
      source relationships and static redacted `Display`/`Debug` output.
    - Remove `eyre::Error` from every public signature while retaining private context
      only where it cannot cross the facade.
    - Define typed timeout phases, connection-error kinds, GraphQL codec failures,
      lifecycle failures, and cloneable terminal close failures.
    - Add fixed variant/source/redaction tests without asserting unstable third-party
      prose.
    - _Requirements: 3.4–3.7, 3.11, 3.12, 5.3, 8.7, 8.8, 9.4, 9.7, 9.9, 10.1–10.4, 10.7, 10.8_
  - [x] 3.2 Implement the raw GraphQL request, response, and wire models
    - Add `RawRequest` with query, optional variables, and optional operation name plus
      read-only accessors and consuming builder methods.
    - Add `ResponseData::{Absent, Null, Value}`, `RawResponse`, ordered
      `GraphQlError`, locations, typed field/index path segments, extensions, and
      constructors suitable for injected connections.
    - Use private wire types with an explicit presence marker so missing and JSON null
      never collapse; preserve partial data alongside errors and extensions.
    - Map encoding and malformed response shapes to typed errors without discarding the
      raw causal context.
    - _Requirements: 7.4, 8.1–8.11, 10.17_
  - [x] 3.3 Define the stable connection and diagnostic contracts
    - Add the documented `EngineConnection: Send + Sync + 'static` async execute/close
      interface and required non-blocking `abort` backstop.
    - Add constructible, typed `EngineConnectionError` values which retain an opaque
      source while ordinary rendering omits it.
    - Add `Diagnostic`, `DiagnosticStream`, `DiagnosticSink`, and
      `DiagnosticSinkError`; document prompt callback, ownership-transfer,
      cancellation, close, abort, and secret-handling obligations.
    - _Requirements: 6.14–6.16, 7.1–7.5, 7.13, 10.6–10.9, 10.14, 10.18_
  - [x] 3.4 Implement `ClientConfig` and its fallible builder
    - Replace public mutable `Config` fields with immutable private state and consuming
      builder methods for every Client Configuration Policy row.
    - Store all time values as `Duration`, retain explicitness separately from effective
      defaults, and provide the exact 300-second startup, 10-second HTTP-connect, and
      absent execution-timeout defaults.
    - Validate workspace/version/runner URI/timeout/verbosity/environment structure and
      explicit-connection conflicts without filesystem, process-input, network, CLI, or
      process access.
    - Preserve ordered native environment values internally while custom `Debug` emits
      keys and option presence only.
    - Remove `config_path`, `timeout_ms`, and `execute_timeout_ms` from the stable type.
    - _Requirements: 5.1–5.4, 5.7–5.12, 5.16, 5.17, 6.10–6.13, 7.11, 9.1–9.3, 10.5, 10.16_
  - [x] 3.5 Add shared public-value and configuration test strategies
    - Generate valid and targeted-invalid native paths, workspace/version/URI strings,
      explicitness combinations, durations, verbosity values, environment entries,
      bounded JSON trees, GraphQL paths, errors, extensions, and secret markers.
    - Keep reference normalization and wire models independent of production builders
      and codecs so a shared defect cannot certify itself.
    - _Requirements: 5.1–5.12, 6.9–6.13, 8.2–8.11, 9.1–9.3, 10.3_
  - [x] 3.6 Property test: Property 11 — configuration construction is total and side-effect free
    - Implement a valid-first/reference-validation `proptest` with at least 256 builder
      inputs, defaults, explicitness states, and targeted invalid scalar mutations;
      record and assert zero external-work events.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 11: configuration construction is total and side-effect free`
    - _Requirements: 5.1, 5.2, 5.3, 5.4, 5.6, 5.7, 5.8, 5.9, 5.10, 5.11, 5.12, 9.1, 9.2, 9.3, 10.3_
  - [x] 3.7 Property test: Property 15 — additional environment validation is portable
    - Implement a reference-normalization `proptest` with at least 256 ordered native
      key/value vectors, ASCII-case variants, non-ASCII units, NUL/equals mutations,
      duplicates, and every reserved key; assert accepted order and redacted failures.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 15: additional environment validation is portable`
    - _Requirements: 6.9, 6.10, 6.11, 6.12, 6.13_
  - [x] 3.8 Property test: Property 19 — raw GraphQL round-trips protocol information
    - Implement a codec/reference-model `proptest` with at least 256 bounded requests
      and responses covering absent/null/value data, partial data plus errors, ordered
      locations and paths, extension objects, and malformed wire mutations.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 19: raw GraphQL round-trips protocol information`
    - _Requirements: 8.1, 8.2, 8.3, 8.4, 8.5, 8.6, 8.7, 8.8, 8.9, 8.10, 8.11_

- [x] 4. Checkpoint: public value, error, and configuration foundations are green
  - Run formatting, locked `dagger-sdk` unit/property tests, clippy, warning-denied
    rustdoc for the new public values, and cargo-deny; require no public `eyre`, no
    unit-encoded timeout fields, and no secret marker in ordinary rendered output.

- [x] 5. Implement deterministic preflight, source planning, launch projection, and diagnostics
  - [x] 5.1 Implement two-phase config validation and process-input snapshots
    - Add private `ValidatedConfig` and `ProcessInputs`; keep structural builder
      validation pure and validate an implicit-source workdir against current filesystem
      state during preflight.
    - Reject an empty, missing, or non-directory workdir before any connector action and
      expose read-only seams for recording discovery, network, and process attempts.
    - Detect an explicit connection before reading Dagger session environment or probing
      the workdir.
    - _Requirements: 5.5, 5.6, 5.13–5.15, 7.6–7.10_
  - [x] 5.2 Implement fail-closed connection-plan selection
    - Project each validated config into exactly one `ConnectionPlan::{Explicit,
      Existing, NewCli}` using the approved compatibility table.
    - Allow only GraphQL execution timeout beside an explicit connection; reject each
      explicitly configured incompatible input before any implicit-source work.
    - Reject Existing_Session workspace state and every other ineffective CLI-only
      option with the specific typed conflict instead of ignoring it.
    - Transfer an explicit connection out of config exactly once without cloning or
      debug-formatting it.
    - _Requirements: 6.17, 6.18, 7.6–7.11_
  - [x] 5.3 Implement canonical CLI launch projection
    - Add `CliLaunchRequest` with ordered argument and native environment vectors.
    - Render workdir, workspace, workspace-module opt-in, version, and untruncated
      verbosity exactly once; omit disabled module loading and obsolete project flags.
    - Add the managed runner-host variable and then accepted additional environment in
      insertion order, without rendering values in diagnostics.
    - Make repeated projection from equal config/process inputs byte-equivalent.
    - _Requirements: 6.1–6.13, 6.19_
  - [x] 5.4 Implement ordered, non-fatal diagnostic dispatch
    - Add one private dispatcher which preserves ingestion order and per-stream bytes,
      discards progress when no sink exists, and never forwards the session-parameter
      control line.
    - Catch sink errors and unwinding panics, disable the failed sink, and emit only a
      static redacted tracing event while connection and close continue.
    - Ensure final-handle destruction never invokes caller sink code.
    - _Requirements: 4.10, 6.14, 6.15, 6.16, 10.9_
  - [x] 5.5 Property test: Property 12 — preflight failure precedes external work
    - Implement a recording-boundary `proptest` with at least 256 structurally valid
      configs, filesystem fixtures, process-input snapshots, and independent preflight
      failures; assert discovery/network/spawn/connection counters remain zero.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 12: preflight failure precedes external work`
    - _Requirements: 5.5, 5.13, 5.14, 5.15_
  - [x] 5.6 Property test: Property 14 — CLI launch projection is deterministic and complete
    - Implement a simple-reference-projection `proptest` with at least 256 valid
      non-explicit configs and process inputs; compare ordered arguments/environment,
      exact multiplicity, module opt-in/omission, and repeated output bytes.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 14: CLI launch projection is deterministic and complete`
    - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 6.8, 6.9, 6.19_
  - [x] 5.7 Property test: Property 16 — diagnostic delivery is ordered and non-fatal
    - Implement a sequence/reference `proptest` with at least 256 progress streams,
      ingestion interleavings, sink-presence states, returned-error schedules, and
      unwinding sink schedules; compare operation outcomes and delivered safe events.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 16: diagnostic delivery is ordered and non-fatal`
    - _Requirements: 6.14, 6.15, 6.16_
  - [x] 5.8 Property test: Property 17 — source compatibility fails closed
    - Implement a truth-table/reference `proptest` with at least 256 config/source pairs,
      option explicitness combinations, and recording side-effect adapters; assert exact
      conflicts, bypass behaviour, and unique connection transfer.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 17: source compatibility fails closed`
    - _Requirements: 6.17, 6.18, 7.6, 7.7, 7.8, 7.9, 7.10, 7.11_

- [x] 6. Checkpoint: validation, source planning, and launch projection are green
  - Run formatting, locked config/planning/diagnostic unit and property tests, clippy,
    and rustdoc; require deterministic launch requests across repeated runs and zero
    side-effect events for every rejected or explicit-connection fixture.

- [x] 7. Implement the shared lifecycle, close election, request fence, and drop cleanup
  - [x] 7.1 Implement `SharedSession` and explicit lease accounting
    - Add private `LifecycleState`, `SharedSession`, and `SessionHandle` with atomic
      `Open`, `Closing`, and `Closed` state plus a separate external lease count.
    - Increment/decrement leases through manual `Clone`/`Drop`; never use
      `Arc::strong_count` as the ownership decision and never expose lifecycle fields.
    - Document the state, lease, memory-ordering, and internal-Arc invariants with WHY
      comments at each non-obvious synchronization boundary.
    - _Requirements: 2.2–2.9, 3.1, 3.10, 4.1, 4.2, 10.11, 10.15_
  - [x] 7.2 Implement single-flight close and terminal-result publication
    - Elect `Open -> Closing` with one compare-exchange and launch exactly one SDK-owned
      close task; make every `Client::close` call a cancellation-safe waiter.
    - Publish one cloneable success/failure to `OnceLock` before the Release transition
      to `Closed`, register notification before rechecking the result, and wake all
      waiters without a lost-wake window.
    - Add `CloseCompletionGuard` so connection failure, unwinding panic, task
      abandonment, or runtime teardown records a terminal result and invokes the abort
      backstop at most once.
    - _Requirements: 3.1–3.7, 3.10, 3.14, 4.9_
  - [x] 7.3 Implement lifecycle-gated request execution
    - Add the final Acquire gate before connection invocation, return `ClientClosed`
      without transport work once state leaves `Open`, and avoid a global request mutex.
    - Allow requests admitted before close to complete or map resource cancellation to
      `InterruptedByClose`; catch an injected execute panic as a typed redacted failure.
    - Keep caller cancellation local to that request and leave the shared session usable.
    - _Requirements: 3.11, 3.12, 8.15, 8.16, 9.12, 10.3, 10.4_
  - [x] 7.4 Implement non-blocking final-lease cleanup
    - When the lease decrement identifies the final handle, use the same close election
      and schedule graceful shutdown on a compatible Tokio runtime without waiting in
      `Drop`.
    - Without a compatible runtime, invoke the connection's non-blocking abort backstop
      through one guarded path; catch an unwinding implementation and emit only redacted
      static tracing data.
    - Ensure non-final drop performs no state transition and `SharedSession` retains an
      ultimate resource-drop backstop.
    - _Requirements: 4.1–4.4, 4.9–4.11_
  - [x] 7.5 Add a Loom lifecycle model and deterministic operation-sequence fixture
    - Model lease clone/drop, close election, terminal publication, waiter registration,
      request admission, abort election, and internal shutdown ownership with a simpler
      reference state machine.
    - Exercise the production-equivalent atomic protocol under Loom and retain Tokio
      barriers for async task/cancellation observations Loom cannot model.
    - _Requirements: 2.2, 2.7–2.9, 3.1–3.7, 3.10–3.12, 4.1–4.4_
  - [x] 7.6 Property test: Property 5 — close linearizes once
    - Implement a reference-state-machine `proptest` with at least 256 generated clone,
      close, cancel-waiter, resource-result, resource-panic, and task-abandon sequences;
      supplement it with exhaustive Loom schedules.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 5: close linearizes once`
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.10, 3.14_
  - [x] 7.7 Property test: Property 7 — the close fence prevents new transport work
    - Implement a barrier-controlled `proptest` with at least 256 request/close schedules
      and connection outcomes; compare gate order, connection-call counts, and typed
      results to the reference linearization.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 7: the close fence prevents new transport work`
    - _Requirements: 3.11, 3.12, 8.16_
  - [x] 7.8 Property test: Property 8 — only the final lease initiates implicit cleanup
    - Implement a handle-tree/reference `proptest` with at least 256 clone/derive/drop
      orders, runtime availability states, close outcomes, and abort panics; assert one
      non-blocking cleanup election and no non-final state change.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 8: only the final lease initiates implicit cleanup`
    - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.9, 4.11_

- [x] 8. Implement connection-resource ownership, cancellation cleanup, timeouts, and owned Client wiring
  - [x] 8.1 Implement the internal `Connector` and armed `PendingConnection`
    - Add a private connector seam and create an armed guard before child/process I/O
      resources can exist.
    - On ordinary failure, terminate and reap the child and join/abort every owned I/O
      task before returning; on future cancellation, immediately start termination and
      transfer reaping to an owned cleanup task.
    - Configure the child kill-on-drop backstop and disarm the guard only when a complete
      connection resource transfers into `SharedSession`.
    - _Requirements: 4.5–4.8, 9.5, 9.6, 9.11_
  - [x] 8.2 Adapt new-CLI and Existing_Session resources to `EngineConnection`
    - Replace the detached optional process plus GraphQL-client pair with concrete
      connection resources that own exactly the transport/process/tasks allowed by the
      Session Resource Policy.
    - Make graceful new-CLI close finish child reap and stdout/stderr joins before
      success; make Existing_Session close release only the SDK transport and never
      signal the external engine.
    - Keep Reqwest, process handles, stdin, session parameters, and credentials private.
    - _Requirements: 2.10–2.12, 3.8, 3.9, 3.13, 4.4, 10.6–10.8_
  - [x] 8.3 Enforce the three independent timeout boundaries
    - Apply startup timeout around connector establishment, HTTP-connect timeout only in
      the SDK HTTP transport, and optional GraphQL execution timeout around each shared
      connection execute future including injected connections.
    - Map each elapsed phase to its own typed error; preserve session usability after
      HTTP-connect/request timeout or caller cancellation.
    - Route startup timeout through the armed pending guard so child termination and
      reaping follow the same cancellation path.
    - _Requirements: 7.12, 9.1–9.12_
  - [x] 8.4 Implement the owned Client connection facade
    - Replace callback-scoped `connect` with `connect() -> Result<Client, ConnectError>`
      and add `connect_with(ClientConfig)`; consume config and return only after a
      complete `SharedSession` exists.
    - Implement `Client::clone`, `Client::execute`, and `Client::close` over one
      `SessionHandle`; keep raw execution available when generated bindings are disabled.
    - Remove callback control flow rather than layering owned lifetime underneath a
      second stable connection model.
    - _Requirements: 2.1–2.4, 2.9, 2.13–2.15, 3.1–3.14, 8.1, 8.16_
  - [x] 8.5 Wire explicit connections through the owned Client
    - Move the boxed connection directly from `ConnectionPlan::Explicit` into
      `SharedSession` with no environment lookup, discovery, download, process creation,
      or concrete transport construction.
    - Delegate request, graceful close, execution timeout, and abort fallback through
      the stable trait; retain its error as the typed source while ordinary output stays
      redacted.
    - _Requirements: 7.1–7.13, 10.6–10.8, 10.18_
  - [x] 8.6 Property test: Property 6 — close respects resource ownership
    - Implement a resource-event/reference `proptest` with at least 256 new-CLI and
      Existing_Session close sequences, child/task outcomes, and failures; assert the
      required ordering and absence of external-engine termination.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 6: close respects resource ownership`
    - _Requirements: 3.8, 3.9, 3.13_
  - [x] 8.7 Property test: Property 9 — pending connection resources cannot escape cancellation
    - Implement a staged-resource `proptest` with at least 256 failure, timeout, and
      cancellation points before/after child and each I/O task creation; compare
      terminate/reap/task-end events to the reference guard model.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 9: pending connection resources cannot escape cancellation`
    - _Requirements: 4.5, 4.6, 4.7, 4.8, 9.5, 9.6, 9.11_
  - [x] 8.8 Property test: Property 18 — injected execution preserves its abstraction
    - Implement a recording-connection `proptest` with at least 256 request sequences,
      timeout choices, close outcomes, abort fallbacks, and typed injected failures;
      assert request fidelity, exact operation counts, and retained source identity.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 18: injected execution preserves its abstraction`
    - _Requirements: 7.1, 7.2, 7.3, 7.4, 7.5, 7.12, 7.13_
  - [x] 8.9 Property test: Property 21 — timeout phases are independent and non-poisoning
    - Implement a paused-time/reference `proptest` with at least 256 positive timeout
      triples, phase delays, request cancellation points, and later-success schedules;
      assert only the elapsed phase fails and reusable sessions stay open.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 21: timeout phases are independent and non-poisoning`
    - _Requirements: 9.4, 9.7, 9.8, 9.9, 9.10, 9.12_

- [x] 9. Checkpoint: lifecycle, owned Client, resources, and timeout tests are green
  - Run formatting, locked unit/property/Loom/Tokio tests, clippy, and rustdoc; require
    zero leaked fake resources, one terminal close result under every generated
    schedule, and continued client usability after non-lifecycle request failures.

- [x] 10. Refactor query construction and generated handles onto the shared session
  - [x] 10.1 Make private selection construction fallible and panic-free
    - Replace eager serialization `unwrap` and lazy ID `unwrap` with stored
      `QueryBuildError` results surfaced by document construction or execution.
    - Keep selection paths immutable, make argument ordering deterministic, and remove
      the transport parameter from private execution.
    - Add fixed tests for aliases, inline fragments, arrays, absent fields, lazy IDs,
      failed serialization, and failed selected-data decoding.
    - _Requirements: 8.7, 8.12–8.15, 10.3, 10.4, 10.12_
  - [x] 10.2 Add the stable session-bound `QueryBuilder`
    - Implement immutable select, alias, argument, document, and typed execute methods
      over a private `Selection` and cloned `SessionHandle`.
    - Route execution through `SharedSession::execute`; keep construction free of session
      locks and return typed build/request/GraphQL/decode errors.
    - Preserve complete `RawResponse` inside generated GraphQL error results so partial
      data is inspectable.
    - _Requirements: 8.4–8.15, 10.12, 10.17_
  - [x] 10.3 Change generator storage to private session leases
    - Update object, interface, root Query, loadable, and function templates so every
      generated handle contains only private `SessionHandle` plus `Selection` fields.
    - Seal internal construction while retaining the public generic bounds required by
      root `r#ref` and `load`; remove public process and GraphQL-client fields.
    - Ensure every derived handle clones the session lease and generated request methods
      execute through it.
    - _Requirements: 2.5–2.12, 8.13, 10.10–10.12_
  - [x] 10.4 Wire `Client::query` and generated execution
    - With the `gen` feature, construct a fresh root `Query` on the client's shared
      session without I/O; keep `Client` free of `Deref` and generated forwarding methods.
    - Route generated and `QueryBuilder` operations through the raw request pipeline and
      map raw response/GraphQL/selected-data failures to typed query errors.
    - Preserve root-drop/derived-handle usability and concurrent raw/generated execution.
    - _Requirements: 2.7–2.9, 8.12–8.15_
  - [x] 10.5 Regenerate bindings and lock generator fixtures
    - Regenerate `dagger-sdk/src/gen.rs` from the target introspection snapshot and update
      generator golden tests for object, interface, root, loadable, lazy ID, and method
      execution shapes.
    - Require deterministic generated output and prove the change is storage/execution
      wiring only, with no schema capability added, removed, or reinterpreted.
    - _Requirements: 2.5–2.12, 8.13, 10.10–10.12_
  - [x] 10.6 Property test: Property 3 — handles share exactly one session
    - Implement a handle-tree/reference `proptest` with at least 256 client clones,
      generated/query-builder derivations, requests, and non-final drop orders; assert
      one connector/session identity and usability while open.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 3: handles share exactly one session`
    - _Requirements: 2.1, 2.2, 2.7, 2.8, 2.9_
  - [x] 10.7 Property test: Property 20 — every query surface uses the same session concurrently
    - Implement a mixed-operation `proptest` with at least 256 raw/generated/compositional
      request sets, construction interleavings, and response shapes; assert one recording
      connection and no construction serialization.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 20: every query surface uses the same session concurrently`
    - _Requirements: 8.12, 8.13, 8.14, 8.15_

- [x] 11. Seal, document, harden, and snapshot the stable public API
  - [x] 11.1 Replace beta exports with the intentional 1.0 facade
    - Make implementation modules private, remove `pub mod core`, and explicitly
      re-export only `Client`, config/builder, connection/diagnostic contracts, raw/query
      values, generated API, and public error families.
    - Remove legacy callback signatures, `connect_opts`, `DaggerConn`, mutable `Config`,
      and concrete transport/process/logger re-exports; retain raw-only client use without
      `gen` and gate generated root access precisely.
    - Add fixed compile-pass and compile-fail consumers for supported and removed paths.
    - _Requirements: 2.10–2.15, 5.16, 5.17, 10.1, 10.2, 10.10–10.13_
  - [x] 11.2 Complete panic containment, redaction, and source auditing
    - Catch unwinding caller implementations at execute/close/abort/sink boundaries and
      map them to typed static failures without rendering panic payloads.
    - Hand-write secret-safe `Debug`/`Display` for config, connection/error wrappers, and
      lifecycle diagnostics; ensure auth headers, session tokens, and environment values
      never enter ordinary tracing fields.
    - Add an automated source audit for production lifecycle/connector/request/shutdown
      paths which rejects unsafe, `unwrap`, `expect`, and `panic!` outside exact reviewed
      test-only exclusions.
    - _Requirements: 4.9, 4.10, 10.3–10.9_
  - [x] 11.3 Add the beta-to-stable migration input
    - Record callback `connect`, `connect_opts`, `DaggerConn`, `Config`, `config_path`,
      `timeout_ms`, `execute_timeout_ms`, public generated storage fields, and their
      stable replacements in the machine-readable migration input consumed by Feature 9.
    - Validate removed/renamed items against the old public-API snapshot and target
      facade so stale or missing migration rows fail tests.
    - _Requirements: 2.13–2.15, 5.16, 5.17, 5.18, 10.13_
  - [x] 11.4 Add public documentation, API snapshots, and UI fixtures
    - Document every public item plus lifecycle, configuration, raw GraphQL, query, and
      connection modules; state defaults, ownership, cancellation, partial-data,
      redaction, side-effect, and advanced-testing contracts.
    - Add warning-denied rustdoc/doctests for owned connect, generated query, raw request,
      injection, concurrent clone use, and explicit close.
    - Check in a normalized public-API snapshot and `trybuild` pass/fail fixtures proving
      Send + Sync and inaccessible lifecycle/process/transport/credential/selection/beta
      fields.
    - _Requirements: 2.3–2.6, 2.10–2.12, 7.1–7.5, 8.1, 8.12, 10.10–10.18_
  - [x] 11.5 Property test: Property 4 — public handles are safely shareable and encapsulated
    - Implement a manifest-driven `proptest` with at least 256 generated handle samples
      and public-path mutations, backed by generic Send + Sync assertions and trybuild
      fixtures for private fields and forbidden concrete types.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 4: public handles are safely shareable and encapsulated`
    - _Requirements: 2.3, 2.4, 2.5, 2.6, 2.10, 2.11, 2.12, 10.10, 10.11, 10.12_
  - [x] 11.6 Property test: Property 10 — implicit-cleanup diagnostics are secret-safe
    - Implement a marker/redaction `proptest` with at least 256 token, header,
      environment, opaque-source, cleanup-failure, sink-failure, and panic-payload
      combinations; search every ordinary display/debug/trace/sink capture.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 10: implicit-cleanup diagnostics are secret-safe`
    - _Requirements: 4.9, 4.10, 10.5, 10.6, 10.7, 10.8, 10.9_
  - [x] 11.7 Property test: Property 13 — stable configuration contains no beta unit/path fields
    - Implement a public-snapshot/migration-map `proptest` with at least 256 removed,
      renamed, extra, stale, and missing item combinations, supplemented by compile-fail
      fixtures for `config_path` and both `*_ms` fields.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 13: stable configuration contains no beta unit/path fields`
    - _Requirements: 5.16, 5.17, 5.18_
  - [x] 11.8 Property test: Property 22 — public failure paths are typed and panic-free
    - Implement a failure-schedule `proptest` with at least 256 invalid inputs, injected
      errors/panics, task failures, request/decode failures, and shutdown outcomes;
      assert the exact public error family, zero escaping unwind, and source-audit result.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 22: public failure paths are typed and panic-free`
    - _Requirements: 10.1, 10.2, 10.3, 10.4_
  - [x] 11.9 Property test: Property 23 — the stable surface is documented and intentionally exported
    - Implement a public-manifest `proptest` with at least 256 item/subset/mutation cases
      comparing re-exports, stability labels, module ownership, required documentation,
      and forbidden implementation modules; pair it with warning-denied rustdoc.
    - Tag: `// Feature: rust-sdk-client-lifecycle, Property 23: the stable surface is documented and intentionally exported`
    - _Requirements: 10.10, 10.13, 10.14, 10.15, 10.16, 10.17, 10.18_

- [x] 12. Checkpoint: generated handles and stable public API are green
  - Run deterministic code generation twice, formatting, locked workspace tests including
    all UI/doctest/property/Loom suites, clippy, warning-denied rustdoc, public-API diff,
    and cargo-deny; require byte-identical generated output and no unintended public item.

- [x] 13. Integrate end-to-end verification, evidence, and truthful ledger updates
  - [x] 13.1 Add owned-client end-to-end test fixtures
    - Cover default and configured connect, root query derivation, a generated request,
      raw partial-data response, concurrent clones, query-builder execution, explicit
      close, repeated close, root drop with a live generated handle, and final-handle
      cleanup through public API only.
    - Add raw-only no-`gen` coverage and an injected-connection example which proves all
      implicit source counters stay zero.
    - _Requirements: 2.1–2.12, 3.1–3.14, 7.1–7.13, 8.1–8.16_
  - [x] 13.2 Add cancellation, timeout, and resource integration fixtures
    - Pause connection after child creation and after each I/O task start, then exercise
      ordinary failure, caller cancellation, startup timeout, graceful close, dropped
      close waiter, HTTP-connect timeout, execution timeout, and cancelled request.
    - Assert child/task event ordering, no external-engine termination, terminal-result
      reuse, no leaked fake resource, and later request usability where required.
    - _Requirements: 3.3–3.14, 4.1–4.11, 9.4–9.12_
  - [x] 13.3 Extend the Rust Dagger toolchain verification boundary
    - Include the exact Feature 2 spec files and new API/UI/property/Loom fixtures in the
      Rust toolchain source filter.
    - Ensure `check` runs locked compile/clippy/rustdoc/API/source-audit checks and `test`
      runs all 23 tagged property tests, fixed tests, UI tests, and target-compatible
      connector fixtures in the pinned Rust image.
    - Keep engine-backed Feature 3 verification separately identifiable so its absence
      cannot be mistaken for Feature 2 evidence.
    - _Requirements: 1.3, 1.8–1.11, 10.3, 10.4, 10.13–10.18_
  - [x] 13.4 Record target-scoped implementation and verification evidence
    - Add evidence records only for exact Feature 2 capabilities proved by the passing
      implementation, property, UI, documentation, and integration checks at the current
      Target_Revision.
    - Preserve `Partial` plus an exact blocker for every Go option or resource behaviour
      still awaiting Feature 3/8 engine-backed verification; do not use docs, source
      presence, or an unrelated passing test as completion evidence.
    - Record reviewed decision evidence for the intentionally absent closure helper.
    - _Requirements: 1.8, 1.9, 1.10, 2.13–2.15_
  - [x] 13.5 Regenerate and verify completeness artifacts
    - Resolve the final path-bounded ownership rules, policy additions, truthful status
      transitions, evidence graph, ledger, JSON report, and Markdown report.
    - Require the exact Feature 2 scope/digest, preservation checks, evidence-closed
      statuses, residual sibling blockers, deterministic artifacts, and no change caused
      solely by ownership correction.
    - _Requirements: 1.1–1.11_
  - [x] 13.6 Add fixed regression coverage for the stable contract
    - Lock exact tests for the three timeout defaults, every config conflict/error leaf,
      all reserved environment keys, missing/null/partial raw data, GraphQL path shapes,
      callback API removal, private generated fields, close-result reuse, no-runtime
      abort, and redacted token/header/environment output.
    - Keep exact third-party error wording and scheduler timing out of assertions.
    - _Requirements: 3.1–3.14, 4.1–4.11, 5.1–5.18, 6.1–6.19, 7.1–7.13, 8.1–8.16, 9.1–9.12, 10.1–10.18_

- [x] 14. Checkpoint: Feature 2 is complete and ready for review
  - From `sdk/rust`, run `cargo fmt --all --check`, locked workspace check/test/clippy,
    warning-denied rustdoc, public-API/UI/source-audit checks, and `cargo deny check`.
  - Run the repository Dagger Rust SDK check, test, and completeness-verification
    functions; require all 23 tagged property tests to execute at least 256 cases, every
    generated/artifact byte to reproduce, and all Feature 2-owned evidence to validate.
  - Require the ledger to increase completion only for actually proved capabilities and
    to retain truthful `Partial` blockers for every unverified Feature 3/4/8/9 dependency.

## Task Dependency Graph

Top-level tasks follow this prerequisite graph. Subtasks within a top-level task execute
in listed order unless their text names a stronger prerequisite.

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
  "14": ["13"]
}
```

## Notes

- Every property task is mandatory, uses workspace-standard `proptest`, runs at least
  256 generated cases, persists minimized regressions, and carries the exact
  feature/property tag shown above. Loom, trybuild, rustdoc, source audit, and fixed
  tests supplement rather than replace the required PBT.
- Reference models remain deliberately smaller than production code. Strategies generate
  valid states first; targeted mutations create rejection cases and useful shrink paths.
- Checkpoints are implementation boundaries: a later slice does not start while the
  preceding checkpoint is red. Generated files and completeness artifacts must be clean
  at every checkpoint which touches them.
- The public API uses a Tokio-compatible runtime, but runtime absence remains a required
  destructor test because drop cleanup cannot assume an executor is available.
- Feature 2 changes generated storage and execution wiring only. Any schema mapping gap
  discovered during implementation is recorded for Feature 4 rather than silently fixed
  under this scope.
- Feature 3 owns real source precedence, provisioning, authentication, observability,
  retry, and detailed transport semantics. Feature 2 builds and tests the stable seams
  they consume; a row remains `Partial` until its required cross-feature evidence exists.
- Feature 9 owns published migration prose and the final release gate. Feature 2 supplies
  the checked migration input and stable public-API snapshot it will consume.
