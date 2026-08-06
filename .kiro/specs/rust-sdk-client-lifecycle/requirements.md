# Requirements Document: Rust SDK Client Lifecycle and Configuration

## Introduction

This specification defines Feature 2 of the approved
`rust-sdk-complete-implementation` umbrella: a stable, owned Rust client and its
complete public configuration contract. It replaces the current closure-only session
scope with an idiomatic client that can be retained, cloned, shared across async tasks,
closed deterministically, and allowed to clean itself up safely. It also provides the
configuration, explicit-connection injection, raw GraphQL, and query-construction
surfaces required by the definitive Go SDK.

The behavioural authority is `github.com/dagger/dagger-go-sdk` commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5`, mirrored under `sdk/go/**` at Dagger
Target_Revision `25300124ca110612edc09c43f89cb5fad6028170`. Go defines what the
client can do. Rust ownership, `Duration`-based time, typed errors, cancellation,
thread safety, and encapsulation define how that behaviour is expressed. The approved
umbrella adds deterministic drop cleanup, idempotent close, preflight validation, and
the existing Rust timeout controls to the parity target.

Feature 2 depends on Feature 1's executable Completeness_Ledger. It defines the public
contract consumed by Feature 3, while Feature 3 owns session-source precedence, CLI
discovery and download, HTTP authentication, OpenTelemetry propagation, transport
retry behaviour, and the detailed transport error taxonomy. Feature 4 owns complete
schema-derived binding generation; Feature 8 owns the closing platform, conformance,
and security matrices; Feature 9 owns release migration material and the final stable
publication gate. Feature 2 may change generated handle storage through the generator
when lifecycle ownership requires it, but it does not add or alter schema mappings.

The F1 baseline deliberately used a coarse classification rule that assigned every
`go-client` declaration except `initClient` to Feature 2. That routes 1,782 ledger
rows here, including 1,679 declarations from `dagger.gen.go`, CLI provisioning,
transport, examples, and unrelated tests. This contradicts the approved feature
boundaries. Feature 2 therefore begins with a checked ownership correction: it keeps
the exact client lifecycle and configuration capabilities listed below, adds the
previously omitted Rust lifecycle-policy capabilities, and routes every other row to
its actual owning feature without changing its status merely because ownership was
corrected.

## Glossary

- **Client:** The stable, owned root handle returned after a successful Dagger
  connection. This is the Rust expression of the Definitive_Go_SDK `Client` and the
  umbrella's `Owned_Client`.
- **Client_Config:** An immutable, validated configuration value used to establish a
  Client.
- **Client_Handle:** A Client clone or a Generated_Handle that retains a lease on the
  same Shared_Session.
- **Client_State:** The shared lifecycle state `Open`, `Closing`, or `Closed` observed
  by every Client_Handle.
- **Complete_Status:** `Implemented`, `Idiomatic_Equivalent`, or a justified
  `Inapplicable` classification under the F1 status policy.
- **Definitive_Go_SDK:** `github.com/dagger/dagger-go-sdk` at commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`.
- **Diagnostic_Sink:** A caller-supplied, thread-safe destination for CLI progress and
  lifecycle diagnostics. It never receives session credentials.
- **Existing_Session:** A Dagger session selected from `DAGGER_SESSION_PORT` and
  `DAGGER_SESSION_TOKEN` rather than started by this process.
- **Explicit_Connection:** A caller-created implementation of the stable
  Engine_Connection abstraction whose ownership is transferred to the Client.
- **External_Work:** CLI discovery, download, process creation, connection attempts,
  or GraphQL requests. Pure parsing and local path validation are not External_Work.
- **Generated_Handle:** A schema-generated query, object, or interface handle derived
  from a Client.
- **GraphQL_Execution_Timeout:** An optional bound on one complete GraphQL request.
- **HTTP_Connect_Timeout:** The bound on establishing the HTTP connection used by a
  GraphQL request.
- **Raw_Request:** A caller-authored GraphQL document, optional variables, and optional
  operation name.
- **Raw_Response:** The GraphQL `data`, `errors`, and `extensions` values returned for
  a Raw_Request.
- **Reserved_Environment_Key:** A case-insensitively matched environment key owned by
  Dagger session selection, runner selection, authentication, or trace propagation.
- **Session_Resource:** A closable transport plus any child process and I/O tasks owned
  by one connection.
- **Session_Startup_Timeout:** The bound on establishing a newly selected connection,
  including waiting for CLI session parameters.
- **Shared_Session:** The internal, reference-counted lifecycle and request state used
  by every Client_Handle. It is an actual ownership requirement, not general-purpose
  shared mutable state.
- **Terminal_Close_Result:** The success or typed failure recorded by the single
  shutdown attempt and returned by later close calls.
- **Target_Revision:** Dagger commit
  `25300124ca110612edc09c43f89cb5fad6028170`.

## Target State

An ordinary async Rust application can establish a Client, retain it in application
state, clone it into concurrent tasks, derive Generated_Handles, issue generated or raw
GraphQL requests, and close it explicitly. Every Client_Handle shares one lifecycle:
explicit close is single-flight and deterministic, while dropping the final handle
initiates non-blocking best-effort cleanup. A cancelled or failed connection attempt
does not leak a child process or its I/O tasks.

Client_Config exposes an idiomatic equivalent for every non-deprecated Go `ClientOpt`,
an idiomatic representation of the deprecated workspace-module skip behaviour, and
three distinct timeout contracts. It validates values and conflicts before
External_Work. CLI-only values reach only a newly started CLI session. An
Explicit_Connection bypasses CLI selection and provisioning. Configuration and
lifecycle errors are typed and redact session tokens and environment values.

The primary public surface consists of the Client, Client_Config and its builder,
Engine_Connection, Raw_Request, Raw_Response, query-construction access, and their
typed errors. Concrete HTTP clients, child processes, session credentials, mutable
query internals, and internal lifecycle synchronization are not public API. These
surfaces are stable at the Rust SDK 1.0 boundary. Version override and runner-host
configuration remain documented as advanced testing facilities, but their public
Rust shape remains covered by SemVer.

Feature 2 does not claim that connection-source selection, provisioning,
observability, every generated schema binding, or the full release matrix is complete.
Where a Feature 2 capability depends on unverified Feature 3 behaviour, its ledger row
remains `Partial` until the required cross-feature verification exists. Ownership
correction alone never increases the Implemented count.

## Evidence From Current Code

All repository citations use Target_Revision unless an external revision is stated.

- **Owned client and raw execution authority:** `sdk/go/client.go:15-172` defines the
  owned `Client`, all non-deprecated connection options, explicit connection
  injection, `Close`, raw `Do`, GraphQL client access, and query-builder access.
  `sdk/go/client.go:174-208` defines the raw request and response field contract.
- **Connection abstraction authority:** `sdk/go/engineconn/engineconn.go:15-37` defines
  `EngineConn`, its close responsibility, connection configuration, and session
  parameters. `sdk/go/engineconn/engineconn.go:39-95` proves that an explicit
  connection bypasses every implicit connection source and that workspace overrides
  are invalid for an Existing_Session.
- **CLI option effects:** `sdk/go/engineconn/session.go:71-120` maps workdir, workspace,
  version, workspace-module loading, verbosity, runner host, extra environment, and
  trace propagation into a newly started CLI session. Its 300-second session-parameter
  bound is at `sdk/go/engineconn/session.go:252-266`.
- **Lifecycle authority:** `sdk/go/engineconn/session.go:20-45` closes a CLI-owned
  connection by cancelling the child, waiting for it, and waiting for its I/O task.
  `sdk/go/engineconn/env.go:27-43` closes an Existing_Session connection without
  terminating an externally owned engine.
- **Reserved environment authority:** `sdk/go/engineconn/env.go:10-24` owns
  `DAGGER_SESSION_PORT` and `DAGGER_SESSION_TOKEN`;
  `sdk/go/engineconn/cli.go:48-61` owns `_EXPERIMENTAL_DAGGER_CLI_BIN`;
  `sdk/go/engineconn/session.go:108-120` owns
  `_EXPERIMENTAL_DAGGER_RUNNER_HOST` and trace propagation; and
  `sdk/go/engineconn/otel.go:12-24` identifies `TRACEPARENT`, `TRACESTATE`, and
  `BAGGAGE` as propagated environment values.
- **Go option tests:** `sdk/go/client_test.go:12-32` verifies workspace and workspace
  module configuration. `sdk/go/engineconn/session_test.go:10-54` verifies CLI
  forwarding and Existing_Session conflicts.
- **Current closure-only Rust lifecycle:**
  `sdk/rust/crates/dagger-sdk/src/client.rs:14-55` exposes `connect` and `connect_opts`
  only through a caller closure and shuts down after the closure returns. It cannot
  return an owned client to an application.
- **Current shutdown gap:**
  `sdk/rust/crates/dagger-sdk/src/core/cli_session.rs:35-73` provides only async
  `shutdown`; it has no `Drop` cleanup or idempotent lifecycle state. Connection setup
  at `sdk/rust/crates/dagger-sdk/src/core/cli_session.rs:123-209` can outlive a
  cancelled caller and uses panic-prone `unwrap` calls in background I/O tasks.
- **Current public configuration:**
  `sdk/rust/crates/dagger-sdk/src/core/config.rs:5-73` exposes mutable public fields for
  workdir, legacy project config path, millisecond timeouts, workspace-module loading,
  and logger. It lacks workspace reference, explicit connection, version, verbosity,
  runner host, and additional environment support.
- **Timeout ground truth:** Rust `Config::timeout_ms` is documented as connection
  establishment at `sdk/rust/crates/dagger-sdk/src/core/config.rs:15-18`, but
  `sdk/rust/crates/dagger-sdk/src/core/graphql_client.rs:27-41` wires it only to
  Reqwest's per-request HTTP connect timeout. CLI session establishment at
  `sdk/rust/crates/dagger-sdk/src/core/cli_session.rs:123-208` has no timeout. The
  target therefore preserves the 10-second HTTP connect default and separately adds
  the Go-compatible 300-second Session_Startup_Timeout.
- **Current raw GraphQL gap:**
  `sdk/rust/crates/dagger-sdk/src/core/graphql_client.rs:15-20` accepts only a query
  string. `sdk/rust/crates/dagger-sdk/src/core/gql_client.rs:182-192,317-387` omits
  operation name and response extensions and turns GraphQL errors into an error path
  that cannot preserve partial data.
- **Current encapsulation gap:** generated `Query` fields publicly expose the child
  process, selection, and transport at
  `sdk/rust/crates/dagger-sdk/src/gen.rs:11654-11659`; their generator source is
  `sdk/rust/crates/dagger-codegen/src/rust/templates/object_tmpl.rs:32-48`.
- **Current F1 routing:** `sdk/rust/completeness/classifications.json:23-46` assigns
  the entire `go-client` authority to Feature 2 through one baseline rule. The derived
  ledger contains 1,782 Feature 2 rows, of which only the exact subset below belongs
  to this feature.
- **Rust policy:** `sdk/rust/AGENTS.md` requires typed public errors, real ownership
  rather than pervasive shared state, panic-free library paths, secret-safe output,
  documented public contracts, and WHY comments for lifecycle and concurrency
  invariants.

## Completeness Contract Policy

### Existing Capability_IDs Whose Status Feature 2 Intends to Change

The following 23 IDs are the exact current-ledger status scope. The scope digest is
`sha256:81ad1a4f2efe1604b9091468bd6a6006d598a2a8ae54a94a974acf08d74b8b40`,
computed over the compact JSON encoding of this lexicographically sorted list.

```text
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2543onnect
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43oad%2557orkspace%254%44odules
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43og%254%46utput
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2543onn
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2545nvironment%2556ariable
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2552unner%2548ost
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2553kip%2557orkspace%254%44odules
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556erbosity
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556ersion%254%46verride
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkdir
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkspace
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2543lose
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2544o
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2547raph%2551%254%43%2543lient
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2551uery%2542uilder
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%254%43oad%2557orkspace%254%44odules
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fdagger%2F%2554est%2557ith%2557orkspace
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2543lient
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2543lient%254%46pt
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2552equest
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fdagger%2F%2552esponse
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2543onfig
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2545ngine%2543onn
```

### Omitted Policy_Capabilities to Add and Complete

F1 did not inventory the approved umbrella's Rust-specific client obligations. The
following 14 stable IDs are added under the `rust-policy` authority before Feature 2
status changes are accepted:

```text
policy/rust-policy/client-beta-config-migration
policy/rust-policy/client-cancelled-connect-cleanup
policy/rust-policy/client-close-idempotency
policy/rust-policy/client-closed-operation-rejection
policy/rust-policy/client-drop-cleanup
policy/rust-policy/client-http-connect-timeout
policy/rust-policy/client-owned-lifecycle
policy/rust-policy/client-preflight-validation
policy/rust-policy/client-public-surface-encapsulation
policy/rust-policy/client-query-execution-timeout
policy/rust-policy/client-reserved-environment
policy/rust-policy/client-secret-redaction
policy/rust-policy/client-session-startup-timeout
policy/rust-policy/client-shared-handle-safety
```

The remaining 1,759 currently Feature 2-owned rows receive ownership-only corrections.
Generated schema declarations route to Feature 4; module-global generated helpers
route to Feature 6; connection selection, CLI, environment, transport, and transport
error behaviours route to Feature 3; general integration coverage routes to Feature 8;
standalone-client behaviour routes to Feature 7; and examples or release-facing
documentation route to Feature 9. Each correction preserves the row's current status,
fingerprint, authority anchors, and implementation evidence.

### Reviewed Idiomatic Equivalences

| Definitive_Go_SDK surface | Rust mapping | Classification rationale |
|---|---|---|
| `Connect(ctx, opts...) -> *Client` | Async connection returning an owned Client from a validated Client_Config | Preserves ownership and connection behaviour without Go functional options |
| `ClientOpt` and `With*` functions | Typed Client_Config builder methods | Rust builders make option types, defaults, and validation explicit |
| `WithSkipWorkspaceModules` | Default `load_workspace_modules = false` plus an optional beta migration alias | Preserves the deprecated opt-out behaviour without stabilising a redundant switch |
| `Client.GraphQLClient()` | Client raw execution plus the stable Engine_Connection abstraction | Preserves supported advanced execution without exposing a concrete Reqwest client |
| `Client.QueryBuilder()` | Stable access to Rust query composition | Preserves compositional queries without exposing mutable generated fields |
| `Client.Close()` | Async, single-flight `Client::close` shared by every Client_Handle | Preserves deterministic shutdown while making Rust concurrency semantics explicit |

## Client Configuration Policy

| Behavioural input | Rust target and default | Invalid-input result | Side-effect boundary |
|---|---|---|---|
| Working directory | Optional native local path; absent by default | `InvalidWorkdir` for an empty, missing, or non-directory path | Forwarded as `--workdir` only to a newly started CLI |
| Workspace reference | Optional non-empty local path or remote workspace reference; absent by default | `InvalidWorkspace` for an empty reference | Forwarded unchanged as `--workspace` only to a newly started CLI |
| Diagnostic sink | Optional `Send + Sync` sink; absent means discard progress | No invalid state remains after construction; runtime write failures are non-fatal | Receives CLI progress and redacted lifecycle diagnostics only |
| Load workspace modules | Boolean opt-in; `false` by default | `ExistingSessionConflict` or `ExplicitConnectionConflict` where the option cannot apply | Forwarded as `--load-workspace-modules` only to a newly started CLI |
| Deprecated skip workspace modules | Expressed idiomatically by the default `false` load setting; no separate stable switch is required | `OptionConflict` if a retained compatibility alias conflicts with opt-in | Produces no CLI flag when disabled |
| Explicit connection | Optional consumed Engine_Connection; absent by default | `ExplicitConnectionConflict` for any configured CLI-only input | Bypasses CLI discovery, download, process start, and Existing_Session selection |
| Engine schema version override | Optional syntactically valid engine version; target version by default | `InvalidVersion` for malformed input; Feature 3 returns `UnsupportedVersion` when the engine rejects it | Forwarded as `--version` only to a newly started CLI; never selects the CLI binary |
| Verbosity | `u8` level; zero by default | `VerbosityOutOfRange` when an external integer cannot convert to `u8` | Forwarded as repeated `-v` only to a newly started CLI |
| Runner host | Optional absolute runner URI with a non-empty scheme; absent by default | `InvalidRunnerHost` for malformed input | Forwarded through `_EXPERIMENTAL_DAGGER_RUNNER_HOST` only to a newly started CLI |
| Additional environment | Ordered native key/value entries; empty by default | `InvalidEnvironmentKey`, `DuplicateEnvironmentKey`, `ReservedEnvironmentKey`, or `InvalidEnvironmentValue` | Forwarded only to a newly started CLI; values are never rendered in errors or diagnostics |
| Session startup timeout | Positive `Duration`; 300 seconds by default | `InvalidTimeout` for zero or unrepresentable input | Bounds source establishment; timeout cancels and reaps a started child |
| HTTP connect timeout | Positive `Duration`; 10 seconds by default | `InvalidTimeout` for zero or unrepresentable input | Bounds only the connection phase of each SDK-owned HTTP request |
| GraphQL execution timeout | Optional positive `Duration`; no timeout by default | `InvalidTimeout` for zero or unrepresentable input | Bounds one complete raw or generated GraphQL request without closing the Client |
| Legacy `config_path` / `--project` | Removed from the stable 1.0 configuration; use workspace reference | `LegacyOptionRemoved` in migration tooling | Never emits the obsolete `--project` flag |
| Legacy millisecond timeout fields | Replaced by the corresponding `Duration` inputs | `LegacyOptionRemoved` in migration tooling | No stable public field retains unit-encoded `*_ms` naming |

### Explicit Connection Compatibility

| Configuration with Explicit_Connection | Target policy | Error if present | Side-effect impact |
|---|---|---|---|
| Working directory | Mutually exclusive | `ExplicitConnectionConflict` | No filesystem or CLI work |
| Workspace reference | Mutually exclusive | `ExplicitConnectionConflict` | No workspace selection |
| Diagnostic sink | Mutually exclusive because no SDK-owned CLI emits progress | `ExplicitConnectionConflict` | No diagnostic sink writes |
| Load workspace modules | Mutually exclusive | `ExplicitConnectionConflict` | No module-loading flag |
| Engine schema version override | Mutually exclusive | `ExplicitConnectionConflict` | No CLI session version flag |
| Verbosity above zero | Mutually exclusive | `ExplicitConnectionConflict` | No CLI verbosity flag |
| Runner host | Mutually exclusive | `ExplicitConnectionConflict` | No runner environment mutation |
| Additional environment | Mutually exclusive | `ExplicitConnectionConflict` | No child environment mutation |
| Session startup timeout | Mutually exclusive with an already-created connection | `ExplicitConnectionConflict` | No connection establishment |
| HTTP connect timeout | Mutually exclusive with caller-owned transport construction | `ExplicitConnectionConflict` | No SDK transport construction |
| GraphQL execution timeout | Compatible | None | Wraps every injected request with the caller-selected bound |

### Reserved Environment Keys

Key comparison is ASCII case-insensitive so configuration has the same result on Unix
and Windows.

| Key | Owning invariant | Required caller action |
|---|---|---|
| `DAGGER_SESSION_PORT` | Existing_Session endpoint selection | Configure an Explicit_Connection or process environment instead |
| `DAGGER_SESSION_TOKEN` | Existing_Session authentication secret | Configure an Explicit_Connection or process environment instead |
| `_EXPERIMENTAL_DAGGER_CLI_BIN` | Local CLI source selection | Configure the process environment and let Feature 3 select the source |
| `_EXPERIMENTAL_DAGGER_RUNNER_HOST` | Runner-host selection | Use the runner-host Client_Config input |
| `TRACEPARENT` | W3C trace-context propagation | Supply tracing context through Feature 3's observability integration |
| `TRACESTATE` | W3C trace-context propagation | Supply tracing context through Feature 3's observability integration |
| `BAGGAGE` | OpenTelemetry baggage propagation | Supply tracing context through Feature 3's observability integration |

## Session Resource Policy

| Connection source | Ownership after connect | Explicit close | Final-handle drop |
|---|---|---|---|
| Newly started CLI | Client owns transport, child, stdin, and I/O tasks | Single-flight graceful shutdown followed by child and I/O-task reap | Non-blocking best-effort shutdown with a kill-on-drop backstop |
| Existing_Session | Client owns only its transport handle | Close the transport without terminating the external engine | Release the transport handle only |
| Explicit_Connection | Ownership is transferred to the Client | Invoke Engine_Connection close exactly once | Invoke non-blocking best-effort close exactly once |
| Client clone | Shares one Shared_Session | Closing any clone closes the shared lifecycle | Dropping a non-final clone does not close the session |
| Generated_Handle | Retains one Shared_Session lease | Observes a close initiated by any Client clone | Keeps the session alive until the handle is dropped |

## Raw GraphQL Contract Policy

### Raw_Request

| Field | Target policy | Error if invalid | Request effect |
|---|---|---|---|
| `query` | Required GraphQL document forwarded without semantic rewriting | `RequestEncoding` for a value that cannot be encoded; GraphQL validation remains an engine response | Becomes the request `query` member |
| `variables` | Optional JSON value; absence is represented distinctly from an empty object | `RequestEncoding` when serialization fails | Becomes the optional request `variables` member |
| `operation_name` | Optional operation name; absence is valid for single-operation documents | `RequestEncoding` when serialization fails | Becomes the optional request `operationName` member |

### Raw_Response

| Field | Target policy | Error if invalid | Response effect |
|---|---|---|---|
| `data` | Preserve absent, null, partial, and complete JSON data distinctly | `ResponseDecoding` for malformed JSON | Returned even when GraphQL errors are also present |
| `errors` | Preserve the ordered GraphQL error list with message, locations, path, and extensions | `ResponseDecoding` for malformed error shape | Available to callers without discarding partial data |
| `extensions` | Preserve the optional JSON object without SDK-specific filtering | `ResponseDecoding` for malformed JSON | Available for engine and protocol metadata |

## Requirements

### Requirement 1: Exact Completeness Scope

**User Story:** As a Rust SDK maintainer, I want Feature 2 tied to exact ledger
capabilities, so that implementation raises the honest completeness count without
claiming generated, transport, or release work it does not own.

#### Acceptance Criteria

1. WHEN Feature 2 implementation begins, THE Completeness_Ledger SHALL replace the
   coarse `baseline/go-client` ownership rule with exact feature routing.
2. WHEN Feature 2 routing is resolved, THE Feature 2 existing-capability set SHALL
   equal the 23 Capability_IDs listed in this document.
3. WHEN Feature 2 routing is resolved, THE contract tooling SHALL validate the
   existing-capability scope digest recorded in this document.
4. WHEN the omitted Rust lifecycle policies are inventoried, THE Canonical_Inventory
   SHALL assign the 14 Policy_Capability IDs listed in this document.
5. WHEN a non-Feature 2 row receives corrected ownership, THE Completeness_Ledger
   SHALL preserve its prior status.
6. WHEN a non-Feature 2 row receives corrected ownership, THE Completeness_Ledger
   SHALL preserve its Capability_Fingerprint.
7. WHEN a non-Feature 2 row receives corrected ownership, THE Completeness_Ledger
   SHALL preserve its existing evidence references.
8. WHEN a Feature 2 status becomes complete, THE Completeness_Ledger SHALL record
   target-scoped implementation evidence in the same change.
9. WHEN a Feature 2 status becomes complete, THE Completeness_Ledger SHALL record
   target-scoped Verification_Evidence in the same change.
10. IF a Feature 2 behaviour still depends on unverified sibling-feature semantics,
    THEN THE Completeness_Ledger SHALL retain a Blocking_Status with the exact residual
    gap.
11. WHEN all Feature 2 requirements and cross-feature dependencies are verified, THE
    Feature 2 capability set SHALL contain no Blocking_Status.

### Requirement 2: Owned, Shareable Client

**User Story:** As an async Rust application author, I want an owned Dagger client, so
that I can retain and share it using normal Rust application patterns.

#### Acceptance Criteria

1. WHEN connection establishment succeeds, THE Rust SDK SHALL return a Client that is
   not scoped to a callback.
2. THE Client SHALL implement `Clone` through one shared lifecycle rather than by
   creating another engine session.
3. THE Client SHALL implement `Send` without an unsafe implementation.
4. THE Client SHALL implement `Sync` without an unsafe implementation.
5. THE Generated_Handle SHALL implement `Send` without an unsafe implementation.
6. THE Generated_Handle SHALL implement `Sync` without an unsafe implementation.
7. WHEN a Client creates a Generated_Handle, THE Generated_Handle SHALL retain a
   Shared_Session lease.
8. WHEN the root Client value is dropped while a Generated_Handle remains, THE
   Generated_Handle SHALL remain usable while Client_State is `Open`.
9. WHEN one Client clone changes Client_State, THE remaining Client_Handles SHALL
   observe the same state.
10. THE public Client SHALL hide concrete HTTP client types.
11. THE public Client SHALL hide child-process handles.
12. THE public Client SHALL hide session credentials.
13. WHERE a closure-scoped convenience API is retained, THE convenience API SHALL
    delegate connection establishment to the owned Client API.
14. WHERE a closure-scoped convenience API is retained, THE convenience API SHALL
    close its Client after a successful callback.
15. WHERE a closure-scoped convenience API is retained, THE convenience API SHALL
    attempt Client close before returning a callback error.

### Requirement 3: Deterministic, Idempotent Close

**User Story:** As a Client owner, I want deterministic shutdown with one result, so
that cleanup is safe under cloning, retries, and concurrent task teardown.

#### Acceptance Criteria

1. WHEN close is requested while Client_State is `Open`, THE Shared_Session SHALL
   transition atomically to `Closing`.
2. WHEN close owns the `Open` to `Closing` transition, THE Shared_Session SHALL start
   exactly one Session_Resource close attempt.
3. WHEN close is requested while Client_State is `Closing`, THE caller SHALL await the
   in-progress close attempt.
4. WHEN close is requested while Client_State is `Closed`, THE caller SHALL receive
   the Terminal_Close_Result.
5. WHEN Session_Resource close succeeds, THE Shared_Session SHALL record a successful
   Terminal_Close_Result.
6. WHEN Session_Resource close fails, THE Shared_Session SHALL record a typed terminal
   close failure.
7. WHEN the close attempt terminates, THE Shared_Session SHALL transition to `Closed`.
8. WHEN close returns success for a newly started CLI session, THE Client SHALL have
   reaped the child process.
9. WHEN close returns success for a newly started CLI session, THE Client SHALL have
   joined its owned stdout and stderr tasks.
10. WHEN any Client clone closes the Shared_Session, THE remaining Client_Handles SHALL
    observe Client_State `Closed`.
11. WHEN an operation begins after Client_State leaves `Open`, THE Client SHALL return
    a typed `ClientClosed` error without reaching the transport.
12. WHEN close terminates a request already in flight, THE request SHALL return a typed
    lifecycle or cancellation error.
13. WHEN an Existing_Session Client closes, THE Client SHALL leave the externally
    owned engine process running.
14. WHEN an Explicit_Connection Client closes, THE Client SHALL invoke the transferred
    Engine_Connection's close operation exactly once.

### Requirement 4: Cancellation-Safe and Drop-Safe Cleanup

**User Story:** As a long-running Rust service operator, I want abandoned clients and
cancelled connections to clean up safely, so that SDK use does not leak processes or
background tasks.

#### Acceptance Criteria

1. WHEN the final Client_Handle is dropped while Client_State is `Open`, THE
   Shared_Session SHALL initiate non-blocking best-effort cleanup.
2. WHEN a non-final Client_Handle is dropped, THE Shared_Session SHALL remain `Open`.
3. WHEN final-handle cleanup runs inside a compatible async runtime, THE
   Shared_Session SHALL schedule graceful Session_Resource close.
4. WHEN final-handle cleanup runs without a compatible async runtime, THE
   Session_Resource SHALL retain a non-blocking kill-on-drop backstop.
5. WHEN a connection future is cancelled after starting a child, THE connection guard
   SHALL terminate the child.
6. WHEN a connection future is cancelled after starting a child, THE connection guard
   SHALL arrange for child reaping.
7. WHEN connection establishment fails after starting a child, THE connection guard
   SHALL arrange for child reaping before returning the failure.
8. WHEN connection establishment fails after starting an I/O task, THE connection
   guard SHALL arrange for that task to terminate.
9. WHEN implicit cleanup reports a failure, THE Client SHALL avoid panicking.
10. WHEN implicit cleanup reports a failure, THE Client SHALL emit only redacted
    diagnostics.
11. THE Client destructor SHALL avoid waiting indefinitely for asynchronous work.

### Requirement 5: Typed, Validated Client Configuration

**User Story:** As a Dagger user, I want a complete typed configuration builder, so
that invalid combinations fail predictably before the SDK starts external work.

#### Acceptance Criteria

1. THE Client_Config builder SHALL expose an idiomatic equivalent for every row in the
   Client Configuration Policy.
2. THE Client_Config builder SHALL represent all public timeout values as `Duration`.
3. WHEN Client_Config is built, THE builder SHALL return a typed result rather than
   panic.
4. WHEN Client_Config is built, THE builder SHALL perform no External_Work.
5. WHEN connect receives Client_Config, THE Client SHALL complete preflight validation
   before External_Work.
6. IF a working directory is empty, missing, or not a directory, THEN THE Client SHALL
   return `InvalidWorkdir`.
7. IF a workspace reference is explicitly empty, THEN THE Client SHALL return
   `InvalidWorkspace`.
8. IF an engine schema version override is syntactically malformed, THEN THE Client SHALL
    return `InvalidVersion`.
9. IF a runner host is not an absolute URI with a scheme, THEN THE Client SHALL return
   `InvalidRunnerHost`.
10. IF a timeout is zero, THEN THE Client SHALL return `InvalidTimeout`.
11. IF a configured integer verbosity cannot convert to `u8`, THEN THE Client SHALL
    return `VerbosityOutOfRange`.
12. WHEN no option is supplied, THE Client_Config SHALL use every default recorded in
    the Client Configuration Policy.
13. WHEN preflight validation fails, THE Client SHALL avoid CLI discovery.
14. WHEN preflight validation fails, THE Client SHALL avoid network access.
15. WHEN preflight validation fails, THE Client SHALL avoid process creation.
16. WHEN the stable 1.0 configuration is exposed, THE Rust SDK SHALL omit the legacy
    `config_path` option.
17. WHEN the stable 1.0 configuration is exposed, THE Rust SDK SHALL omit unit-encoded
    `timeout_ms` and `execute_timeout_ms` fields.
18. WHEN the beta configuration is replaced, THE release migration input SHALL record
    every removed or renamed public field for Feature 9.

### Requirement 6: Deterministic Configuration Effects

**User Story:** As a user connecting in different environments, I want each option to
have one explicit boundary, so that configuration is never silently ignored or applied
to the wrong session.

#### Acceptance Criteria

1. WHEN a newly started CLI session is selected, THE session launch request SHALL
   preserve every applicable Client Configuration Policy value.
2. WHEN a working directory is configured for a new CLI session, THE session launch
   request SHALL contain one `--workdir` value.
3. WHEN a workspace reference is configured for a new CLI session, THE session launch
   request SHALL contain one `--workspace` value.
4. WHEN workspace-module loading is enabled for a new CLI session, THE session launch
   request SHALL contain `--load-workspace-modules`.
5. WHEN workspace-module loading is disabled, THE session launch request SHALL omit a
   workspace-module loading flag.
6. WHEN an engine schema version override is configured for a new CLI session, THE session
   launch request SHALL contain one `--version` value.
7. WHEN verbosity is greater than zero for a new CLI session, THE session launch
   request SHALL encode the selected level without truncation.
8. WHEN a runner host is configured for a new CLI session, THE child environment SHALL
   contain one SDK-managed runner-host value.
9. WHEN additional environment entries are configured for a new CLI session, THE
   child environment SHALL preserve their insertion order.
10. IF two additional environment keys compare equal ignoring ASCII case, THEN THE
    Client SHALL return `DuplicateEnvironmentKey`.
11. IF an additional environment key is empty or contains `=` or NUL, THEN THE Client
    SHALL return `InvalidEnvironmentKey`.
12. IF an additional environment value contains NUL, THEN THE Client SHALL return
    `InvalidEnvironmentValue`.
13. IF an additional environment key matches the Reserved Environment Keys table,
    THEN THE Client SHALL return `ReservedEnvironmentKey`.
14. WHEN a Diagnostic_Sink is absent, THE Client SHALL discard ordinary CLI progress.
15. WHEN a Diagnostic_Sink is present, THE Client SHALL forward CLI progress in source
    order.
16. WHEN a Diagnostic_Sink write fails, THE Client SHALL continue the connection or
    shutdown operation without panicking.
17. WHEN an Existing_Session is selected, THE Client SHALL reject configured
    workspace state through a typed invariant error.
18. WHEN a CLI-only option cannot affect the selected source, THE Client SHALL return
    a typed option conflict rather than ignore the option.
19. WHEN the Client_Config is reused with the same process inputs, THE launch request
    SHALL be deterministic.

### Requirement 7: Explicit Connection Injection

**User Story:** As a test or infrastructure author, I want to inject an engine
connection, so that I can control transport without triggering SDK provisioning.

#### Acceptance Criteria

1. THE Rust SDK SHALL expose a stable Engine_Connection abstraction for explicit
   injection.
2. THE Engine_Connection abstraction SHALL require `Send`.
3. THE Engine_Connection abstraction SHALL require `Sync`.
4. THE Engine_Connection abstraction SHALL support Raw_Request execution.
5. THE Engine_Connection abstraction SHALL support asynchronous close.
6. WHEN an Explicit_Connection is configured, THE Client SHALL take ownership of that
   connection.
7. WHEN an Explicit_Connection is configured, THE Client SHALL bypass Existing_Session
   selection.
8. WHEN an Explicit_Connection is configured, THE Client SHALL bypass local CLI
   discovery.
9. WHEN an Explicit_Connection is configured, THE Client SHALL bypass CLI download.
10. WHEN an Explicit_Connection is configured, THE Client SHALL bypass process
    creation.
11. IF an Explicit_Connection is combined with a mutually exclusive input from the
    compatibility table, THEN THE Client SHALL return `ExplicitConnectionConflict`.
12. WHEN GraphQL_Execution_Timeout is configured with an Explicit_Connection, THE
    Client SHALL apply the timeout to each injected request.
13. WHEN an injected request fails, THE Client SHALL preserve the
    Engine_Connection-provided error as the typed source.

### Requirement 8: Raw GraphQL and Query Construction

**User Story:** As an advanced SDK user, I want supported raw and compositional query
paths, so that I can use engine capabilities without bypassing the Client lifecycle.

#### Acceptance Criteria

1. THE Client SHALL expose a stable Raw_Request execution operation equivalent to the
   Definitive_Go_SDK `Do` capability.
2. THE Raw_Request SHALL represent every field in the Raw GraphQL Contract Policy.
3. THE Raw_Response SHALL represent every field in the Raw GraphQL Contract Policy.
4. WHEN a Raw_Response contains both data and errors, THE Client SHALL preserve the
   data.
5. WHEN a Raw_Response contains both data and errors, THE Client SHALL preserve the
   errors.
6. WHEN a Raw_Response contains extensions, THE Client SHALL preserve the extensions.
7. WHEN request serialization fails, THE Client SHALL return a typed
   `RequestEncoding` error.
8. WHEN response deserialization fails, THE Client SHALL return a typed
   `ResponseDecoding` error.
9. WHEN the engine returns GraphQL errors, THE Client SHALL preserve their order.
10. WHEN the engine returns GraphQL errors, THE Client SHALL preserve each error path.
11. WHEN the engine returns GraphQL errors, THE Client SHALL preserve each error's
    extensions.
12. THE Client SHALL expose stable access to the supported query-construction surface.
13. WHEN a generated request executes, THE request SHALL use the same Shared_Session as
    raw execution.
14. WHEN a compositional request executes, THE request SHALL use the same
    Shared_Session as raw execution.
15. WHEN raw and generated requests execute concurrently, THE Client SHALL preserve
    thread safety without serializing unrelated request construction.
16. WHEN Client_State is not `Open`, THE raw execution operation SHALL return
    `ClientClosed` without invoking Engine_Connection.

### Requirement 9: Distinct Timeout and Cancellation Semantics

**User Story:** As an operator, I want time bounds to describe distinct phases, so that
I can tune startup and queries without corrupting a reusable Client.

#### Acceptance Criteria

1. WHEN no Session_Startup_Timeout is configured, THE Client_Config SHALL use 300
   seconds.
2. WHEN no HTTP_Connect_Timeout is configured, THE Client_Config SHALL use 10 seconds.
3. WHEN no GraphQL_Execution_Timeout is configured, THE Client_Config SHALL leave
   complete request execution unbounded by SDK policy.
4. WHEN Session_Startup_Timeout elapses, THE Client SHALL return a typed startup
   timeout error.
5. WHEN Session_Startup_Timeout elapses after child creation, THE connection guard
   SHALL terminate the child.
6. WHEN Session_Startup_Timeout elapses after child creation, THE connection guard
   SHALL arrange for child reaping.
7. WHEN HTTP_Connect_Timeout elapses, THE request SHALL return a typed transport-connect
   timeout error.
8. WHEN HTTP_Connect_Timeout elapses, THE Shared_Session SHALL remain usable for later
   requests.
9. WHEN GraphQL_Execution_Timeout elapses, THE request SHALL return a typed execution
   timeout error.
10. WHEN GraphQL_Execution_Timeout elapses, THE Shared_Session SHALL remain usable for
    later requests.
11. WHEN a caller cancels connection establishment, THE connection guard SHALL perform
    the cancellation cleanup required by Requirement 4.
12. WHEN a caller cancels one GraphQL request, THE Shared_Session SHALL remain usable
    for unrelated requests.

### Requirement 10: Stable, Documented, Secret-Safe Public Contract

**User Story:** As a Rust SDK consumer, I want a small, documented, typed public
surface, so that the 1.0 client remains understandable and safe to upgrade.

#### Acceptance Criteria

1. THE public Client API SHALL use typed error enums for configuration, connection,
   lifecycle, raw request, and close failures.
2. THE public Client API SHALL avoid exposing `eyre::Error` in public signatures.
3. THE public Client API SHALL avoid panic as an invalid-input path.
4. THE public Client API SHALL avoid `unwrap` in connection, request, shutdown, and
   background-task library paths.
5. WHEN a Client_Config is formatted with `Debug`, THE output SHALL omit additional
   environment values.
6. WHEN a connection value is formatted with `Debug`, THE output SHALL omit the
   session token.
7. WHEN an error is rendered, THE output SHALL omit the session token.
8. WHEN an error is rendered, THE output SHALL omit authentication headers.
9. WHEN a Diagnostic_Sink receives progress, THE Client SHALL omit session
   credentials.
10. THE stable public surface SHALL expose only intentional re-exports rather than the
    current concrete `core` implementation modules.
11. THE stable public surface SHALL keep lifecycle synchronization fields private.
12. THE stable public surface SHALL keep mutable query-construction fields private.
13. WHEN Rust SDK 1.0 is released, THE Client, Client_Config, Engine_Connection,
    Raw_Request, Raw_Response, and their public errors SHALL follow SemVer.
14. WHEN advanced testing configuration is public, THE API documentation SHALL label
    its intended use without weakening SemVer guarantees.
15. THE Client lifecycle module SHALL document its ownership and shutdown invariants.
16. THE configuration module SHALL document defaults, conflicts, and side-effect
    boundaries.
17. THE raw GraphQL module SHALL document partial-data and GraphQL-error behaviour.
18. THE Engine_Connection abstraction SHALL document ownership transfer and close
    semantics.

## Iteration and Feedback Notes

- The F1 `go-client` ownership rule is intentionally treated as an initial routing
  approximation, not as authority that all 1,782 declarations belong in this feature.
- The three-timeout model corrects a current documentation/implementation mismatch:
  the existing 10-second `timeout_ms` configures HTTP connection establishment, not
  CLI session startup.
- The design must derive executable lifecycle, configuration, raw-response, and
  concurrency properties from these requirements before implementation tasks are
  authored.
