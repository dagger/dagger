# Requirements Document: Rust SDK Transport, Observability, and Reliability

## Introduction

This specification defines Feature 3 of the approved
`rust-sdk-complete-implementation` umbrella: the concrete connection machinery beneath
the stable client contract delivered by Feature 2. It completes deterministic
connection-source selection, existing-session transport, local and downloaded CLI
selection, verified provisioning, CLI process ownership, authenticated GraphQL over
loopback HTTP, W3C trace propagation, diagnostic routing, target compatibility, and a
typed failure taxonomy.

The behavioural authority is `github.com/dagger/dagger-go-sdk` commit
`1309520660f6a5b35ef97b4fbe151e32a06a8dc5`, mirrored under `sdk/go/**` at Dagger
Target_Revision `25300124ca110612edc09c43f89cb5fad6028170`. Go defines the observable
source order, CLI protocol, download fallback boundary, authentication, tracing, and
engine-error semantics. Rust ownership, cancellation, bounded resources, typed errors,
race-free cache publication, panic-free parsing, and private dependency-injection seams
define how those behaviours are expressed. Where the definitive Go implementation has
an accidental weakness, this specification preserves the behaviour while strengthening
the mechanism rather than copying the weakness.

Feature 3 consumes Feature 2's validated Connection_Plan, owned Shared_Session,
Diagnostic_Sink, Raw_Request, and Raw_Response. It supplies the concrete connector that
turns an implicit plan into one transferred Session_Resource. Feature 4 owns complete
schema-derived bindings, although Feature 3 maps engine-authored execution extensions
already represented by Feature 2's lossless raw response. Feature 8 owns the closing
live platform, conformance, and security matrices; Feature 9 owns migration and release
publication. Feature 3 implements Linux, macOS, and Windows archive logic and proves it
with deterministic fixtures, but does not claim Feature 8's multi-platform live gate.

The current `sdk-sdk` harness remains authoritative for the common checks it actually
defines. Its pinned checks do not exercise client-side source selection, CLI download,
session control lines, HTTP authentication, trace propagation, or transport errors.
Feature 3 therefore requires dedicated deterministic conformance fixtures and
target-scoped live engine evidence. An unrelated green harness check is not
Verification_Evidence for a Feature 3 status change.

## Glossary

- **Archive_Descriptor:** The platform-specific release archive name, format, and
  expected executable member for one CLI_Target.
- **Blocking_Status:** `Missing` or `Partial` under the Feature 1 status policy.
- **Cache_Lock:** The cross-process exclusion mechanism protecting cache validation,
  first publication, and managed retention.
- **CLI_Source:** Exactly one of Explicit_Local_CLI, Verified_Download, or
  Compatibility_PATH_Fallback.
- **CLI_Target:** The compiled Dagger CLI version plus normalized operating system and
  architecture used to select one release artifact.
- **Compatibility_PATH_Fallback:** A `dagger` executable resolved from `PATH` only
  after checksum metadata proves that the compiled release is unavailable.
- **Complete_Status:** `Implemented`, `Idiomatic_Equivalent`, or a justified
  `Inapplicable` classification under the Feature 1 status policy.
- **Connection_Plan:** Feature 2's deterministic, validated decision to use an
  Explicit_Connection, Existing_Session, or New_CLI.
- **Control_Line:** The first newline-terminated CLI stdout record containing a JSON
  port and session token. It is protocol data, never a diagnostic payload.
- **Definitive_Go_SDK:** `github.com/dagger/dagger-go-sdk` at commit
  `1309520660f6a5b35ef97b4fbe151e32a06a8dc5`.
- **Diagnostic_Snapshot:** A bounded, redacted tail retained to explain a startup or
  shutdown failure without retaining an unbounded CLI stream.
- **Engine_Domain_Error:** A GraphQL error whose extensions identify a typed
  engine-authored failure such as `EXEC_ERROR`.
- **Exact_Target:** The single Target_Descriptor declared by
  `sdk/rust/completeness/compatibility.json` for Rust SDK `1.0.0-beta.10`.
- **Existing_Session:** A Dagger session selected when `DAGGER_SESSION_PORT` is present
  and authenticated by `DAGGER_SESSION_TOKEN`.
- **Explicit_Connection:** Feature 2's caller-supplied Engine_Connection, selected
  before all process and environment sources.
- **Explicit_Local_CLI:** The executable selected when
  `_EXPERIMENTAL_DAGGER_CLI_BIN` is present.
- **Managed_Cache_Entry:** A regular, non-symlink CLI file whose name and metadata are
  owned by this SDK's provisioner.
- **New_CLI:** A Connection_Plan requiring CLI selection, process startup, and a new
  SDK-owned session.
- **Provisioning_Test_Seam:** A private, injected clock, filesystem, platform, process,
  or HTTP adapter used to prove behaviour without public mutable globals.
- **Release_Unavailable:** The typed condition produced only when the checksum
  manifest request returns HTTP 403 or 404.
- **Session_Resource:** Feature 2's closable transport plus any owned child process and
  stream tasks transferred after complete connection establishment.
- **Target_Revision:** Dagger commit
  `25300124ca110612edc09c43f89cb5fad6028170`.
- **Verified_Download:** A CLI executable extracted from the compiled release archive,
  verified against its SHA-256 manifest entry, and atomically published into the SDK
  cache.
- **W3C_Propagation_State:** W3C `traceparent`, `tracestate`, and `baggage` values
  derived from the active OpenTelemetry context or, when no valid active span exists,
  inherited from the corresponding process environment.

## Target State

An ordinary connection follows one inspectable source order: Explicit_Connection,
Existing_Session, Explicit_Local_CLI, then Verified_Download. A source is selected by
presence, not by success. Malformed Existing_Session input, a broken explicitly
configured CLI, a provisioning integrity failure, or a failed child process returns a
typed error without silently changing sources. The sole exception is the definitive
Go compatibility fallback: a 403 or 404 for the release checksum manifest may select a
`dagger` executable from `PATH`, with an explicit compatibility warning.

Verified provisioning is streaming, bounded, checksum-gated, and cancellation-safe.
The SDK supports the Dagger release layouts for Linux and macOS tarballs and Windows
ZIP archives on `amd64` and `arm64`. Concurrent processes cannot observe a partial
binary, corrupt one another's first download, or race managed retention. Production
URLs are HTTPS and fixed to `dl.dagger.io`; tests replace private adapters rather than
mutating public global URL variables.

A newly started CLI receives Feature 2's canonical arguments plus Rust SDK identity
labels and W3C propagation environment. The first stdout record is parsed exactly once
as bounded session control data. Remaining stdout and stderr are routed separately to
the Diagnostic_Sink after redaction. Startup cancellation, timeout, malformed control
data, early child exit, stream failure, close, and drop all converge on Feature 2's
single resource owner, so every child is terminated and reaped and every background
failure remains observable.

Every implicit HTTP transport connects only to `127.0.0.1:<port>/query`, authenticates
with the session token as the Basic username and an empty password, and injects W3C
headers into each request. GraphQL data and errors remain lossless. Known engine-domain
extensions gain typed Rust access without discarding the Raw_Response. Transport,
protocol, GraphQL, engine-domain, compatibility, timeout, and shutdown failures remain
distinguishable without rendering credentials.

Before an implicit connection is returned, the SDK obtains the engine's public
`Query.version` and validates it against the Exact_Target. A known outside target or an
identity that cannot establish the declared exact compatibility claim returns a typed
compatibility failure after cleaning up any SDK-owned Session_Resource. An
Explicit_Connection remains caller-owned transport policy: it bypasses this implicit
source handshake as it already bypasses SDK provisioning.

Feature 3 does not widen the public compatibility range, add arbitrary query retries,
make private provisioning controls public, expose a concrete HTTP client, or duplicate
Feature 2's ownership state machine. It replaces the beta downloader and CLI session
path rather than routing the stable facade through parallel legacy machinery.

## Evidence From Current Code

Repository citations for definitive behaviour use Target_Revision unless an external
revision is stated. Citations to the Feature 2 foundation use merge commit
`268b1c41b20279cbf4c36981b7d62e0a64952d23`.

- **Source order and existing-session invariants:**
  `sdk/go/engineconn/engineconn.go:39-95` selects explicit connection, session
  environment, explicit local CLI, and downloaded CLI in that order and returns an
  error from a selected source rather than continuing. `sdk/go/engineconn/env.go:10-33`
  selects an Existing_Session by port presence and requires a parseable port plus a
  non-empty token. `sdk/go/engineconn/session_test.go:34-54` verifies workspace-state
  rejection for an Existing_Session.
- **Authenticated, traced HTTP:** `sdk/go/engineconn/engineconn.go:108-134` prefers a
  valid active span, otherwise extracts process propagation, dials loopback, applies
  Basic authentication, and injects propagation headers for each request.
  `sdk/go/engineconn/otel.go:14-55` defines composite W3C baggage and trace-context
  propagation and uppercase environment-carrier keys.
- **CLI projection and ownership:** `sdk/go/engineconn/session.go:20-45` closes a
  CLI-owned child and waits for its I/O work. `sdk/go/engineconn/session.go:71-120`
  builds session arguments, Go SDK labels, runner environment, extra environment, and
  trace propagation. `sdk/go/engineconn/session.go:180-195` uses child stdin as the
  portable graceful-close signal and bounds process waiting.
- **CLI startup protocol:** `sdk/go/engineconn/session.go:124-218` bounds the narrow
  text-file-busy spawn retry to ten attempts. `sdk/go/engineconn/session.go:233-278`
  reads one JSON Control_Line under a 300-second bound and transfers the resulting
  child, transport, stderr buffer, and I/O owner together.
- **Local and downloaded selection:** `sdk/go/engineconn/cli.go:48-89` makes a present
  `_EXPERIMENTAL_DAGGER_CLI_BIN` authoritative, selects the compiled CLI version for
  download, and preserves both download and PATH-start causes when compatibility
  fallback also fails. `sdk/go/engineconn/version.gen.go:1-5` pins that CLI version to
  `1.0.0-beta.10`.
- **Narrow fallback boundary:** `sdk/go/engineconn/cli.go:91-109,208-266` permits PATH
  fallback only for checksum-manifest HTTP 403 or 404. The definitive tests at
  `sdk/go/engineconn/cli_test.go:19-73` prove the warning, no fallback for other
  failures, the checksum-unavailable classification, and no fallback for a missing
  archive.
- **Verified provisioning:** `sdk/go/engineconn/cli.go:111-184` creates a private
  cache, verifies before publication, makes the result executable, renames it, and
  cleans old managed CLIs. `sdk/go/engineconn/cli.go:268-373` streams a SHA-256 of the
  whole archive and bounds extracted tar or ZIP output to one GiB.
  `sdk/go/engineconn/cli.go:375-432` defines release archive names and URLs for the
  current operating system and architecture.
- **Concurrency evidence boundary:** `sdk/go/provision_test.go:28-145` proves initial
  provisioning followed by concurrent cache reuse and managed retention. Its explicit
  serial first run at lines 120-121 means it does not prove concurrent first-download
  publication; race-safe first publication is therefore a Rust policy obligation, not
  a claimed Go guarantee.
- **GraphQL engine errors:** `sdk/go/dagger.gen.go:43-108` preserves arbitrary GraphQL
  extensions and recognizes `_type = EXEC_ERROR`. `sdk/go/dagger.gen.go:110-135`
  exposes command, exit code, stdout, stderr, message, unwrap, and original extensions.
  `sdk/go/client_test.go:230-260` verifies the typed execution fields.
- **Engine target identity:** `core/schema/query.go:100-125` defines public
  `Query.version` as the current full engine version; its Go binding is
  `sdk/go/dagger.gen.go:12894-12901`. Feature 1's exact compatibility claim and typed
  outside-range capability are recorded in
  `sdk/rust/completeness/compatibility.json:1-15`.
- **Current stable Rust seam:** at the Feature 2 merge commit,
  `sdk/rust/crates/dagger-sdk/src/preflight.rs:138-236` produces validated Existing or
  New_CLI requests and preserves every CLI-only option. Explicit injection is selected
  before process observations at `sdk/rust/crates/dagger-sdk/src/preflight.rs:302-330`.
- **Current concrete gap:**
  `sdk/rust/crates/dagger-sdk/src/connector.rs:69-96` returns `Unavailable` for every
  New_CLI plan. Existing-session HTTP at `sdk/rust/crates/dagger-sdk/src/connector.rs:99-177`
  validates port and token and applies Basic authentication, but does not inject W3C
  propagation and turns non-success HTTP status into a transport failure before
  decoding a structured GraphQL body.
- **Reusable Rust ownership:**
  `sdk/rust/crates/dagger-sdk/src/connector.rs:179-390` already arms pending child and
  stream resources, reaps them on cancellation, and transfers them into one
  CLI-owned connection. Feature 3 must use this boundary rather than create a second
  resource owner.
- **Reusable Rust diagnostics:**
  `sdk/rust/crates/dagger-sdk/src/diagnostic.rs:13-113` distinguishes stdout, stderr,
  lifecycle, and unobservable Control_Line input. Its dispatcher at lines 115-156
  serializes callbacks and contains sink failures, but the CLI stream reader is not yet
  wired to it.
- **Current beta provisioning hazards:**
  `sdk/rust/crates/dagger-sdk/src/core/downloader.rs:1-43` is Unix-specific and can
  panic while detecting a platform. It writes the final cache path non-atomically at
  lines 132-172, buffers whole downloads at lines 209-225, and invokes `todo!()` for
  ZIP at lines 218-220. The compiled beta CLI constant remains stale at
  `sdk/rust/crates/dagger-sdk/src/core/version.rs:1`.
- **Current beta session hazards:**
  `sdk/rust/crates/dagger-sdk/src/core/cli_session.rs:91-120` has no implemented retry
  or trace propagation. Its stdout task at lines 123-175 treats any parseable line as
  control data and uses `unwrap`; stream failures after startup at lines 144-202 are
  discarded.
- **Current error boundary:**
  `sdk/rust/crates/dagger-sdk/src/errors.rs:373-555` distinguishes configuration,
  startup timeout, connection, request, GraphQL, and decoding failures while retaining
  Raw_Response for GraphQL errors. It does not yet distinguish provisioning phases,
  HTTP status, background session failure, unsupported target, or typed engine-domain
  extensions.
- **Harness limitation:** `sdk/rust/completeness/harness-mappings.json` maps the pinned
  `sdk-sdk` checks only to their own common capability IDs. No mapped check proves a
  Feature 3 transport capability.
- **Rust policy:** `sdk/rust/AGENTS.md` requires typed public errors, panic-free library
  paths, no unsafe code, secret-safe diagnostics, documented public contracts,
  bounded resource behaviour, and WHY comments for lifecycle and concurrency
  invariants.

## Completeness Contract Policy

### Existing Capability_IDs Whose Status Feature 3 Intends to Change

The following 32 IDs are the exact current-ledger status scope. It contains all 21
currently Feature 3-owned rows and the 11 Feature 2-owned rows whose recorded residual
gap is Feature 3 live behaviour. The scope digest is
`sha256:11568be7e981928bba0883527a4a5dd83401c7a226e341321ab9f94a9becb4c7`,
computed over the compact JSON encoding of this lexicographically sorted list.

```text
behavior/go-client/source%2Fgo-client%2Fgo-const%2Fengineconn%2F%2543%254%43%2549%2556ersion
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2543onnect
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43oad%2557orkspace%254%44odules
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%254%43og%254%46utput
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2545nvironment%2556ariable
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2552unner%2548ost
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2553kip%2557orkspace%254%44odules
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556erbosity
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2556ersion%254%46verride
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkdir
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fdagger%2F%2557ith%2557orkspace
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2546rom%254%43ocal%2543%254%43%2549
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2546rom%2544ownloaded%2543%254%43%2549
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2546rom%2553ession%2545nv
behavior/go-client/source%2Fgo-client%2Fgo-function%2Fengineconn%2F%2547et
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fdagger%2F%2543lient%2F%2543lose
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fengineconn%2F%2543%254%43%2549%2544ownloader%2F%2544ownload
behavior/go-client/source%2Fgo-client%2Fgo-method%2Fengineconn%2F%2552ound%2554ripper%2546unc%2F%2552ound%2554rip
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%254%44issing%2541rchive%2544oes%254%45ot%2546allback
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%254%45o%2546allback%2554o%254%43ocal%2543%254%43%2549%2546or%254%46ther%2545rrors
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2543%254%43%2549%2553ession%2541rgs%2549nclude%254%43oad%2557orkspace%254%44odules
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2543%254%43%2549%2553ession%2541rgs%2549nclude%2557orkspace
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2543hecksum%254%44ap%254%44arks%2555navailable
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2546allback%2554o%254%43ocal%2543%254%43%2549
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2547et%2552ejects%2557orkspace%254%44odule%254%43oading%2546or%2545xisting%2553ession
behavior/go-client/source%2Fgo-client%2Fgo-test%2Fengineconn%2F%2554est%2547et%2552ejects%2557orkspace%2546or%2545xisting%2553ession
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2543%254%43%2549%2544ownloader
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2543onnect%2550arams
behavior/go-client/source%2Fgo-client%2Fgo-type%2Fengineconn%2F%2552ound%2554ripper%2546unc
behavior/go-client/source%2Fgo-client%2Fgo-var%2Fengineconn%2F%254%46verride%2543%254%43%2549%2541rchive%2555%2552%254%43
behavior/go-client/source%2Fgo-client%2Fgo-var%2Fengineconn%2F%254%46verride%2543hecksums%2555%2552%254%43
behavior/go-engine-sdk/typed-outside-target-response
```

The two Feature 2 test rows whose remaining gap is Feature 8 live verification are not
in this scope. Feature 3 must not relabel them complete merely because deterministic
CLI projection or a single target run succeeds.

### Omitted Policy_Capabilities to Add and Complete

F1 did not inventory transport obligations that have no one-to-one Go declaration.
The following 26 stable IDs are added under the `rust-policy` authority before Feature
3 status changes are accepted:

```text
policy/rust-policy/transport-background-failure-observation
policy/rust-policy/transport-cache-atomic-publication
policy/rust-policy/transport-cache-permission-safety
policy/rust-policy/transport-cache-retention
policy/rust-policy/transport-cli-archive-bounds
policy/rust-policy/transport-cli-trace-propagation
policy/rust-policy/transport-cli-version-selection
policy/rust-policy/transport-control-line-isolation
policy/rust-policy/transport-diagnostic-bounds
policy/rust-policy/transport-diagnostic-failure-containment
policy/rust-policy/transport-download-fallback-boundary
policy/rust-policy/transport-engine-error-extensions
policy/rust-policy/transport-error-taxonomy
policy/rust-policy/transport-existing-session-validation
policy/rust-policy/transport-http-trace-propagation
policy/rust-policy/transport-local-cli-no-fallback
policy/rust-policy/transport-loopback-authentication
policy/rust-policy/transport-no-query-retry
policy/rust-policy/transport-platform-archive-selection
policy/rust-policy/transport-session-labels
policy/rust-policy/transport-session-protocol
policy/rust-policy/transport-shutdown-bound
policy/rust-policy/transport-source-precedence
policy/rust-policy/transport-startup-retry-boundary
policy/rust-policy/transport-unsupported-target-response
policy/rust-policy/transport-verified-cli-download
```

### Rust Transport Policy Anchors

The completeness extractor selects these exact, stable policy statements. Their IDs
are semantic coordinates; source line numbers are evidence locators only.

- `transport-background-failure-observation`: Every owned process and stream task failure is retained for typed startup or shutdown inspection.
- `transport-cache-atomic-publication`: Concurrent provisioners expose either no cache entry or one complete verified executable, never partial bytes.
- `transport-cache-permission-safety`: The provisioner rejects symlink or non-regular cache entries and applies private platform-appropriate cache permissions.
- `transport-cache-retention`: Managed retention runs under the Cache_Lock, preserves the selected executable, and treats cleanup failure as a redacted non-fatal diagnostic.
- `transport-cli-archive-bounds`: Checksum manifests, archive input, extracted executable output, and session control input have fixed documented size bounds.
- `transport-cli-trace-propagation`: A new CLI receives W3C trace context and baggage derived from the active context or environment fallback.
- `transport-cli-version-selection`: The stable connector selects the CLI version declared by the Exact_Target rather than a stale beta constant.
- `transport-control-line-isolation`: Session control bytes are parsed once and can never enter diagnostics, traces, Debug output, or rendered errors.
- `transport-diagnostic-bounds`: Startup and shutdown diagnostics retain only a fixed redacted tail while live sink delivery remains streaming.
- `transport-diagnostic-failure-containment`: A Diagnostic_Sink error or panic disables that sink without failing or panicking the transport operation.
- `transport-download-fallback-boundary`: PATH fallback is permitted only for a typed Release_Unavailable checksum-manifest response.
- `transport-engine-error-extensions`: Known engine error extensions gain typed access without discarding the complete Raw_Response or unknown extension members.
- `transport-error-taxonomy`: Configuration, discovery, provisioning, process, protocol, HTTP, GraphQL, engine-domain, compatibility, timeout, background, and shutdown failures remain distinguishable.
- `transport-existing-session-validation`: A present session port selects Existing_Session and malformed port or token input fails without considering any CLI source.
- `transport-http-trace-propagation`: Every implicit GraphQL HTTP request injects W3C trace context and baggage with active context precedence.
- `transport-local-cli-no-fallback`: A present explicit local CLI input is authoritative and any resolution or startup failure is terminal for source selection.
- `transport-loopback-authentication`: Implicit GraphQL HTTP dials loopback and authenticates with the session token as Basic username and an empty password.
- `transport-no-query-retry`: The transport never automatically repeats a GraphQL operation after request transmission may have begun.
- `transport-platform-archive-selection`: Linux and macOS select tar.gz dagger members while Windows selects ZIP dagger.exe members for amd64 and arm64.
- `transport-session-labels`: Every new CLI session receives stable Rust SDK name and package-version labels.
- `transport-session-protocol`: One bounded first stdout line must contain a valid port and non-empty token before resources transfer to the Client.
- `transport-shutdown-bound`: Graceful CLI shutdown has a fixed bound after which the SDK kills and reaps the owned child.
- `transport-source-precedence`: End-to-end source order is Explicit_Connection, Existing_Session, Explicit_Local_CLI, then Verified_Download.
- `transport-startup-retry-boundary`: Process startup retries only a recognized executable-busy condition for at most ten attempts with bounded backoff.
- `transport-unsupported-target-response`: An implicit engine outside or unprovable against the Exact_Target fails with a typed compatibility response.
- `transport-verified-cli-download`: A downloaded executable is streamed, SHA-256 verified, cancellation-safe, and atomically published before execution.

### Reviewed Idiomatic Equivalences

| Definitive_Go_SDK surface | Rust mapping | Classification rationale |
|---|---|---|
| `engineconn.Get` | Feature 2 Connection_Plan plus one private concrete connector | Preserves exact source order while keeping validation separate from side effects |
| `FromSessionEnv` | Private Existing_Session adapter with typed native-environment parsing | Preserves presence semantics without exposing credentials or Go's tuple result |
| `FromLocalCLI` | Private explicit-local discovery branch | Preserves authority and no-fallback behaviour without public experimental functions |
| `FromDownloadedCLI` and `CLIDownloader` | Private async provisioner returning a verified native path | Preserves provisioning behaviour with Rust cancellation and cache ownership |
| `ConnectParams` | Private Control_Line wire type followed by validated session parameters | Keeps the session token out of public API and Debug output |
| `RoundTripperFunc` | Private Reqwest-backed Engine_Connection implementation | The Go function adapter is unnecessary; the authenticated traced round trip remains |
| `OverrideCLIArchiveURL` and `OverrideChecksumsURL` | Provisioning_Test_Seam dependency injection | Avoids public mutable globals and test races while preserving fixture control |
| `ExecError` | Typed Rust engine-domain error retaining its complete Raw_Response | Preserves fields and extensions without losing partial data or sibling errors |
| Go stdout/stderr writers | Feature 2 Diagnostic_Sink with typed stream origin | Preserves progress while containing callback failures and isolating Control_Line data |

## Connection Source Policy

| Priority | Selection condition | Success result | Selected-source failure |
|---:|---|---|---|
| 1 | Client_Config owns an Explicit_Connection | Transfer it directly through Feature 2 | Return its typed setup failure; inspect no process input |
| 2 | `DAGGER_SESSION_PORT` is present | Existing_Session over loopback HTTP | Return typed environment, protocol, transport, or compatibility failure; inspect no CLI source |
| 3 | `_EXPERIMENTAL_DAGGER_CLI_BIN` is present | Resolve and start Explicit_Local_CLI | Return typed discovery, spawn, protocol, transport, or compatibility failure; do not download |
| 4 | No higher source is selected | Provision and start the compiled CLI_Target | Follow only the Release_Unavailable exception below |

`DAGGER_SESSION_TOKEN` without `DAGGER_SESSION_PORT` does not select an
Existing_Session. An empty explicit-local value is still present and therefore fails
that selected source. Source selection is performed once from one process-input
snapshot; a failure cannot observe a later environment mutation and reconsider the
decision.

## CLI Provisioning Policy

| Concern | Target policy | Failure result | Side-effect boundary |
|---|---|---|---|
| Compiled version | `1.0.0-beta.10`, derived from the Exact_Target | Typed internal target mismatch if generated constants drift | Validated before discovery or network |
| Supported targets | `linux`/`darwin`/`windows` crossed with `amd64`/`arm64` | Typed unsupported platform | Fails before network or cache mutation |
| Archive name | `dagger_v<version>_<os>_<arch>.tar.gz`; Windows uses `.zip` | Typed archive descriptor error | Pure deterministic construction |
| Release base | HTTPS `dl.dagger.io/dagger/releases/<version-without-v>/` | Typed provisioning error | Production adapter only |
| Checksum manifest | HTTP 200, bounded UTF-8 text, exactly two fields per line, one exact archive entry | Typed manifest status, size, syntax, or missing-entry error | Read before archive download |
| Unavailable release | Manifest HTTP 403 or 404 only | Typed Release_Unavailable | May enter Compatibility_PATH_Fallback |
| Archive response | HTTP 200 and bounded streaming body | Typed archive status, size, I/O, or cancellation error | Never permits PATH fallback |
| Integrity | SHA-256 of every compressed archive byte equals the exact manifest digest | Typed checksum mismatch | No final executable is visible |
| Executable extraction | Exact basename `dagger` or `dagger.exe`; regular file; at most one GiB output | Typed archive format, ambiguity, missing member, or size error | Extracts to a private temporary path |
| Cache hit | Existing Managed_Cache_Entry only | Typed unsafe cache-entry error | No network on accepted hit |
| Publication | Flush and close verified temporary file, set permissions, then atomically publish under Cache_Lock | Typed cache publication error | Final path changes once |
| Retention | Remove only recognized obsolete managed CLI entries under Cache_Lock; keep selected entry | Non-fatal redacted diagnostic | Runs only after successful selection |

Checksum manifests are bounded to 8 MiB. Compressed archive input and extracted
executable output are each bounded to one GiB. A tar or ZIP containing multiple
matching executable members is rejected as ambiguous. Archive member paths are never
joined to the cache directory, so traversal entries cannot escape the private
temporary file.

On Unix, the cache directory and published executable use owner-only `0700`
permissions. On Windows, the executable uses the `.exe` suffix and the SDK relies on
the private user cache directory's platform ACL rather than emulating Unix modes.
Accepted cache entries must be regular files and must not be symbolic links.

## Download Fallback and Retry Policy

| Condition | PATH fallback | Retry | Required observation |
|---|---:|---:|---|
| Checksum manifest HTTP 403 or 404 | Yes, resolve `dagger`/`dagger.exe` from PATH | No download retry | Compatibility warning names compiled version and selected path but no credentials |
| PATH lookup or PATH CLI startup fails after Release_Unavailable | No further source | No | Typed compound error retains both causes |
| Manifest network, timeout, size, syntax, or missing-entry failure | No | No | Typed provisioning phase |
| Archive HTTP status, network, timeout, format, ambiguity, or size failure | No | No | Typed provisioning phase |
| SHA-256 mismatch | No | No | Expected and actual digest may be inspected; no body bytes rendered |
| Cache validation or publication failure | No | No | Typed cache phase |
| Explicit_Local_CLI lookup or startup failure | No | No | Typed local CLI phase |
| Recognized executable-busy spawn failure | Not applicable | At most 10 attempts, 100 ms bounded delay between attempts | Each failed attempt releases its pipes and pending resources |
| Any GraphQL request failure | No | No automatic request replay | Original operation has at-most-once SDK transmission |

The ten-attempt spawn bound includes the initial attempt. Cancellation or
Session_Startup_Timeout interrupts backoff immediately. Matching is based on an
operating-system error identity where the platform exposes one, not localized rendered
error text. No other discovery, download, startup, HTTP, GraphQL, or shutdown failure
is retried by Feature 3.

## Session Protocol and Observability Policy

| Channel or event | Handling | Bound | Credential policy |
|---|---|---:|---|
| CLI first stdout line | Parse once as Control_Line | 64 KiB including delimiter | Never emitted, retained, traced, or rendered |
| CLI stdout after Control_Line | Stream as `DiagnosticStream::Stdout` | No retained copy | Redact known token and configured environment values |
| CLI stderr | Stream as `DiagnosticStream::Stderr` and retain failure tail | 1 MiB retained tail | Redact known token and configured environment values |
| SDK lifecycle | Emit structured `DiagnosticStream::Lifecycle` events | No unbounded payload | Use source/phase identifiers, never raw environment or headers |
| Sink error or panic | Disable sink and continue | First failure is terminal for that sink | Do not format caller failure or panic payload |
| Stream read failure | Retain typed background result | One terminal result per task | Render only phase and redacted bounded tail |
| Child early exit | Fail startup or later shutdown observably | One terminal child result | Include status and redacted bounded tail |

Control_Line JSON requires an integer TCP port in `1..=65535` and a non-empty string
`session_token`. Additional JSON members are ignored for forward compatibility. A
missing delimiter, invalid UTF-8, over-limit line, malformed JSON, invalid port, empty
token, or EOF before a valid first line is a typed session-protocol failure.

The child inherits the host environment plus Feature 2's validated managed additions.
It receives `--label dagger.io/sdk.name:rust` and
`--label dagger.io/sdk.version:<CARGO_PKG_VERSION>` exactly once each. W3C propagation
keys are SDK-managed and cannot be overridden by additional environment configuration.

## HTTP and Trace Policy

| Concern | Target policy | Failure category |
|---|---|---|
| Endpoint | `http://127.0.0.1:<validated-port>/query` only | Session protocol before transport |
| Authentication | HTTP Basic username = session token, password = empty | HTTP status or transport; header never rendered |
| Content type | `application/json` | Protocol construction |
| Active propagation | Valid active OpenTelemetry span plus baggage | No failure; absent values omitted |
| Environment fallback | Uppercase `TRACEPARENT`, `TRACESTATE`, and `BAGGAGE` only when no valid active span exists | Malformed values are ignored by the propagator |
| CLI propagation | Inject W3C_Propagation_State into child environment | Process startup |
| HTTP propagation | Inject current W3C_Propagation_State into every request header | Request construction |
| Redirect | Do not follow a redirect away from the validated loopback authority | Typed HTTP protocol failure |
| Retry | Never automatically replay GraphQL requests | Original typed request failure |

The transport does not honor proxy environment for this loopback endpoint and does not
accept a CLI-authored host. These restrictions prevent a session token from being sent
to a proxy, redirect, or non-loopback authority. Existing_Session close releases only
SDK HTTP state; it never signals the externally owned engine.

## Error Taxonomy Policy

| Layer | Required inspectable category | Preserved detail | Ordinary rendering |
|---|---|---|---|
| Existing session | Environment port, missing token, invalid port | Variable identity, not value | Redacted invariant description |
| CLI discovery | Explicit local lookup, PATH compatibility lookup, unsupported platform | Source and native path role | No environment values |
| Provisioning | Manifest, archive, checksum, extraction, cache, cancellation | Phase, status, safe path role, digests where relevant | No response bodies or credentials |
| Process | Pipe, spawn, executable busy exhausted, early exit | Phase, OS source, exit status | Optional redacted bounded stderr tail |
| Session protocol | Control line size, encoding, JSON, port, token shape, EOF | Protocol phase only | Never Control_Line bytes |
| HTTP | Connect timeout, transport I/O, redirect, non-success status | Status and retained source | Never Authorization header or token |
| GraphQL protocol | Ordered errors plus partial data and extensions | Complete Raw_Response | Stable summary |
| Engine domain | `EXEC_ERROR` plus unknown future types | Typed known fields and complete Raw_Response | Engine message; stdout/stderr inspectable but not appended automatically |
| Compatibility | Unsupported or unverified Exact_Target | Expected target and safe observed identity | No credentials or transport internals |
| Background/shutdown | Stream, child wait, forced kill, task join | Phase and terminal source | Redacted bounded diagnostic tail |

For `_type = EXEC_ERROR`, typed access includes message, command arguments, exit code,
stdout, stderr, and the complete extensions object. Missing or wrongly typed known
members never panic; the error remains a generic GraphQL error with its original
extensions. Typed mapping never discards partial data, sibling errors, response-level
extensions, or unknown members.

## Requirements

### Requirement 1: Exact Completeness Scope

**User Story:** As a Rust SDK maintainer, I want Feature 3 tied to exact ledger
capabilities, so that transport work raises the honest completeness count without
claiming Feature 8's platform and release gates.

#### Acceptance Criteria

1. WHEN Feature 3 implementation begins, THE contract tooling SHALL validate the 32
   existing Capability_IDs listed in this document as the exact status-change scope.
2. WHEN Feature 3 implementation begins, THE contract tooling SHALL validate the
   existing-capability scope digest recorded in this document.
3. WHEN omitted transport policies are inventoried, THE Canonical_Inventory SHALL
   contain the 26 Policy_Capability IDs listed in this document.
4. WHEN a Feature 3 status becomes complete, THE Completeness_Ledger SHALL record
   target-scoped implementation evidence in the same change.
5. WHEN a Feature 3 status becomes complete, THE Completeness_Ledger SHALL record
   status-appropriate Verification_Evidence in the same change.
6. WHEN a Go test-control global is expressed through a private Rust seam, THE
   Completeness_Ledger SHALL classify the capability as an Idiomatic_Equivalent with
   decision evidence.
7. WHEN an `sdk-sdk` Harness_Check does not exercise a Feature 3 assertion, THE
   Completeness_Ledger SHALL exclude that check from Feature 3 Verification_Evidence.
8. WHEN a Feature 2 row's exact residual Feature 3 gap is verified, THE
   Completeness_Ledger SHALL update that row in the same evidence-bearing change.
9. WHEN a Feature 2 row retains a Feature 8 live-verification gap, THE
   Completeness_Ledger SHALL preserve its Blocking_Status.
10. IF a Feature 3 behaviour still lacks target-scoped verification, THEN THE
    Completeness_Ledger SHALL retain its exact residual Blocking_Status.
11. WHEN all Feature 3 requirements are verified, THE 21 Feature 3-owned existing rows
    SHALL contain no Blocking_Status.
12. WHEN all Feature 3 requirements are verified, THE 26 new transport policy rows
    SHALL contain no Blocking_Status.

### Requirement 2: Deterministic End-to-End Source Selection

**User Story:** As a Dagger user, I want one authoritative connection source, so that a
broken explicit environment never causes surprising network access or a different
engine selection.

#### Acceptance Criteria

1. WHEN Client_Config contains an Explicit_Connection, THE connection pipeline SHALL select it
   before reading process connection inputs.
2. WHEN an Explicit_Connection is selected, THE connection pipeline SHALL avoid Existing_Session
   selection.
3. WHEN an Explicit_Connection is selected, THE connection pipeline SHALL avoid local CLI
   discovery.
4. WHEN an Explicit_Connection is selected, THE connection pipeline SHALL avoid CLI download.
5. WHEN no Explicit_Connection exists and `DAGGER_SESSION_PORT` is present, THE
   connection pipeline SHALL select Existing_Session.
6. WHEN Existing_Session is selected, THE connection pipeline SHALL avoid explicit-local CLI
   discovery.
7. WHEN Existing_Session is selected, THE connection pipeline SHALL avoid CLI download.
8. WHEN no higher-priority source exists and `_EXPERIMENTAL_DAGGER_CLI_BIN` is present,
   THE connection pipeline SHALL select Explicit_Local_CLI.
9. WHEN Explicit_Local_CLI is selected, THE connection pipeline SHALL avoid CLI download.
10. WHEN no higher-priority source exists, THE connection pipeline SHALL select
    Verified_Download.
11. WHEN one source is selected, THE connection pipeline SHALL retain that source decision for
    the entire connection attempt.
12. WHEN a selected source fails outside the documented Release_Unavailable exception,
    THE connection pipeline SHALL return its typed failure without selecting another source.
13. WHEN process inputs change after the selection snapshot, THE connection pipeline SHALL ignore
    those changes for the active attempt.

### Requirement 3: Existing Session and Explicit Local CLI

**User Story:** As an operator supplying an existing engine or local CLI, I want
malformed inputs to fail locally and explicitly, so that the SDK never hides a
deployment error by provisioning something else.

#### Acceptance Criteria

1. WHEN `DAGGER_SESSION_PORT` is absent, THE connection pipeline SHALL ignore
   `DAGGER_SESSION_TOKEN` for source selection.
2. IF `DAGGER_SESSION_PORT` is not native text, THEN THE connector SHALL return a typed
   existing-session environment error.
3. IF `DAGGER_SESSION_PORT` is not an integer in `1..=65535`, THEN THE connector SHALL
   return a typed invalid-port error.
4. IF `DAGGER_SESSION_TOKEN` is absent after Existing_Session selection, THEN THE
   connector SHALL return a typed missing-token error.
5. IF `DAGGER_SESSION_TOKEN` is empty after Existing_Session selection, THEN THE
   connector SHALL return a typed missing-token error.
6. WHEN Existing_Session input is malformed, THE connector SHALL avoid rendering its
   port or token values.
7. WHEN Existing_Session input is valid, THE connector SHALL create an externally
   owned loopback transport.
8. WHEN an Existing_Session Client closes, THE connector SHALL leave the external
   engine process running.
9. WHEN `_EXPERIMENTAL_DAGGER_CLI_BIN` is present, THE connector SHALL treat an empty
   value as a selected-source error.
10. WHEN an explicit-local path begins with a home-directory marker, THE connector
    SHALL expand it through the native home-directory policy.
11. WHEN an explicit-local value is resolved, THE connector SHALL apply native
    executable lookup semantics.
12. IF explicit-local resolution fails, THEN THE connector SHALL return a typed local
    CLI discovery error.
13. IF Explicit_Local_CLI startup fails, THEN THE connector SHALL return a typed local
    CLI process error.
14. WHEN Explicit_Local_CLI fails, THE connector SHALL avoid checksum-manifest access.
15. WHEN Existing_Session or Explicit_Local_CLI fails, THE connector SHALL avoid PATH
    compatibility fallback.

### Requirement 4: Platform-Correct Verified CLI Provisioning

**User Story:** As a Rust SDK user without a local CLI, I want the matching verified
release provisioned safely, so that the SDK executes the intended Dagger binary on
Linux, macOS, and Windows.

#### Acceptance Criteria

1. WHEN CLI provisioning begins, THE provisioner SHALL select version
   `1.0.0-beta.10` from the Exact_Target.
2. WHEN the compiled CLI version differs from the Exact_Target, THE provisioner SHALL
   return a typed internal target mismatch before network access.
3. WHEN the native platform is Linux x86-64, THE provisioner SHALL select
   `linux/amd64` tar.gz.
4. WHEN the native platform is Linux AArch64, THE provisioner SHALL select
   `linux/arm64` tar.gz.
5. WHEN the native platform is macOS x86-64, THE provisioner SHALL select
   `darwin/amd64` tar.gz.
6. WHEN the native platform is macOS AArch64, THE provisioner SHALL select
   `darwin/arm64` tar.gz.
7. WHEN the native platform is Windows x86-64, THE provisioner SHALL select
   `windows/amd64` ZIP.
8. WHEN the native platform is Windows AArch64, THE provisioner SHALL select
   `windows/arm64` ZIP.
9. IF the native operating system or architecture is unsupported, THEN THE provisioner
   SHALL return a typed unsupported-platform error before network access.
10. WHEN a release archive name is constructed, THE provisioner SHALL follow the CLI
    Provisioning Policy exactly.
11. WHEN a production checksum URL is constructed, THE provisioner SHALL use the fixed
    HTTPS Dagger release origin.
12. WHEN a production archive URL is constructed, THE provisioner SHALL use the fixed
    HTTPS Dagger release origin.
13. WHEN deterministic download tests run, THE provisioner SHALL replace private HTTP
    and platform adapters without mutating public global state.
14. THE stable public API SHALL omit mutable archive-URL override globals.
15. THE stable public API SHALL omit mutable checksum-URL override globals.

### Requirement 5: Bounded Download, Integrity, and Archive Safety

**User Story:** As a security-conscious operator, I want every downloaded CLI verified
before execution, so that partial, corrupt, oversized, or malicious archives cannot
become cache executables.

#### Acceptance Criteria

1. WHEN a checksum manifest returns HTTP 200, THE provisioner SHALL enforce the 8 MiB
   manifest bound.
2. IF a checksum line does not contain exactly two whitespace-separated
   fields, THEN THE provisioner SHALL return a typed manifest syntax error.
3. IF the manifest lacks the exact Archive_Descriptor name, THEN THE provisioner SHALL
   return a typed missing-checksum error.
4. IF the manifest digest is not valid SHA-256 text, THEN THE provisioner SHALL return
   a typed checksum-format error.
5. WHEN an archive is downloaded, THE provisioner SHALL hash every compressed byte in
   stream order.
6. WHEN an archive is downloaded, THE provisioner SHALL enforce the one-GiB compressed
   input bound.
7. WHEN an executable member is extracted, THE provisioner SHALL enforce the one-GiB
   output bound.
8. WHEN a tar.gz is selected, THE provisioner SHALL accept only a regular member whose
   basename is `dagger`.
9. WHEN a ZIP is selected, THE provisioner SHALL accept only a regular member whose
   basename is `dagger.exe`.
10. IF an archive has no matching executable member, THEN THE provisioner SHALL return
    a typed missing-member error.
11. IF an archive has more than one matching executable member, THEN THE provisioner
    SHALL return a typed ambiguous-member error.
12. WHEN an archive contains traversal paths, THE provisioner SHALL avoid resolving
    those paths against the cache directory.
13. IF the computed archive digest differs from the manifest digest, THEN THE
    provisioner SHALL return a typed checksum-mismatch error.
14. WHEN checksum verification fails, THE provisioner SHALL leave no final cache
    executable.
15. WHEN archive parsing fails, THE provisioner SHALL leave no final cache executable.
16. WHEN provisioning is cancelled, THE provisioner SHALL remove its private temporary
    artifact.
17. WHEN provisioning fails, THE provisioner SHALL avoid rendering downloaded body
    bytes.
18. THE provisioning library path SHALL avoid panic for every manifest and archive
    input.

### Requirement 6: Race-Safe Cache Publication and Retention

**User Story:** As a service starting many Rust clients concurrently, I want CLI cache
publication to be atomic across tasks and processes, so that every client executes one
complete verified artifact.

#### Acceptance Criteria

1. WHEN the provisioner opens the cache, THE provisioner SHALL use the native user
   cache location under a `dagger` directory.
2. WHEN the provisioner opens or creates the cache directory on Unix, THE provisioner SHALL set
   owner-only `0700` permissions.
3. WHEN an expected final path is a symlink, THE provisioner SHALL return a typed unsafe
   cache-entry error.
4. WHEN an expected final path is not a regular file, THE provisioner SHALL return a
   typed unsafe cache-entry error.
5. WHEN an accepted Managed_Cache_Entry exists, THE provisioner SHALL avoid network
   access.
6. WHEN an accepted Managed_Cache_Entry exists, THE provisioner SHALL return the exact
   selected native path.
7. WHEN no accepted entry exists, THE provisioner SHALL acquire the Cache_Lock before
   publishing.
8. WHEN another provisioner publishes while the caller waits for the Cache_Lock, THE
   waiting provisioner SHALL revalidate and reuse the published entry.
9. WHEN a temporary executable is written, THE provisioner SHALL place it on the same
   filesystem as the final cache path.
10. WHEN a verified temporary executable is complete, THE provisioner SHALL flush and
    close it before publication.
11. WHEN a verified temporary executable is complete on Unix, THE provisioner SHALL
    set owner-only executable permissions before publication.
12. WHEN publication occurs, THE provisioner SHALL make the complete verified file
    visible atomically.
13. WHEN concurrent first downloads finish, THE cache SHALL contain one accepted
    selected executable.
14. WHEN managed retention runs, THE provisioner SHALL hold the Cache_Lock.
15. WHEN managed retention runs, THE provisioner SHALL preserve the selected
    executable.
16. WHEN managed retention runs, THE provisioner SHALL ignore unrelated cache files.
17. WHEN managed retention removes an obsolete entry, THE provisioner SHALL remove
    only a recognized Managed_Cache_Entry.
18. IF managed retention fails, THEN THE provisioner SHALL keep the successful selected
    executable usable.
19. IF managed retention fails, THEN THE provisioner SHALL emit a redacted non-fatal
    diagnostic.
20. WHEN a publication attempt fails, THE provisioner SHALL release the Cache_Lock.

### Requirement 7: Narrow Compatibility Fallback and Retry

**User Story:** As a user of a newly cut SDK or unavailable release, I want the one
documented compatibility escape hatch without silent fallback for integrity failures.

#### Acceptance Criteria

1. WHEN the checksum manifest returns HTTP 403, THE provisioner SHALL classify the
   failure as Release_Unavailable.
2. WHEN the checksum manifest returns HTTP 404, THE provisioner SHALL classify the
   failure as Release_Unavailable.
3. WHEN Release_Unavailable occurs, THE connector SHALL resolve `dagger` through native
   PATH lookup.
4. WHEN PATH fallback is selected, THE connector SHALL emit a compatibility warning.
5. WHEN PATH fallback is selected, THE warning SHALL identify the unavailable compiled
   CLI version.
6. WHEN PATH fallback is selected, THE warning SHALL state that version compatibility
   is not guaranteed.
7. IF PATH lookup fails after Release_Unavailable, THEN THE connector SHALL preserve
   both typed causes.
8. IF PATH CLI startup fails after Release_Unavailable, THEN THE connector SHALL
   preserve both typed causes.
9. WHEN a checksum-manifest failure is not HTTP 403 or 404, THE connector SHALL avoid
   PATH fallback.
10. WHEN an archive request returns HTTP 403 or 404, THE connector SHALL avoid PATH
    fallback.
11. WHEN checksum verification fails, THE connector SHALL avoid PATH fallback.
12. WHEN archive extraction fails, THE connector SHALL avoid PATH fallback.
13. WHEN cache publication fails, THE connector SHALL avoid PATH fallback.
14. WHEN child startup returns a recognized executable-busy error, THE connector SHALL
    attempt startup at most ten times total.
15. WHEN a retried startup attempt fails, THE connector SHALL release that attempt's
    pipes and pending resources before backoff.
16. WHEN startup backoff is required, THE connector SHALL bound each delay to 100
    milliseconds.
17. WHEN connection cancellation occurs during startup backoff, THE connector SHALL
    stop retrying promptly.
18. WHEN Session_Startup_Timeout occurs during startup backoff, THE connector SHALL stop
    retrying promptly.
19. WHEN a spawn failure is not the recognized executable-busy condition, THE connector
    SHALL avoid retry.
20. WHEN a GraphQL request fails, THE transport SHALL avoid automatic replay.

### Requirement 8: CLI Session Protocol and Resource Transfer

**User Story:** As a long-running application operator, I want CLI startup to have one
bounded protocol and one resource owner, so that malformed output or cancellation
cannot leak a child or task.

#### Acceptance Criteria

1. WHEN New_CLI is selected, THE connector SHALL launch the executable with Feature
   2's canonical session arguments.
2. WHEN New_CLI is selected, THE connector SHALL add the Rust SDK name label exactly
   once.
3. WHEN New_CLI is selected, THE connector SHALL add the Rust package-version label
   exactly once.
4. WHEN New_CLI is selected, THE connector SHALL preserve Feature 2's validated child
   environment additions.
5. WHEN New_CLI is selected, THE connector SHALL pipe child stdin, stdout, and stderr.
6. WHEN the child is spawned, THE PendingConnection guard SHALL own it before any
   fallible session parsing.
7. WHEN the child is spawned, THE PendingConnection guard SHALL own every stream task
   before connection transfer.
8. WHEN the first stdout line is read, THE connector SHALL enforce the 64 KiB
   Control_Line bound.
9. WHEN the first stdout line is valid, THE connector SHALL parse it exactly once as
   session parameters.
10. IF the Control_Line port is outside `1..=65535`, THEN THE connector SHALL return a
    typed session-protocol error.
11. IF the Control_Line token is empty, THEN THE connector SHALL return a typed
    session-protocol error.
12. IF EOF occurs before a valid Control_Line, THEN THE connector SHALL return a typed
    session-protocol error.
13. IF the child exits before a valid Control_Line, THEN THE connector SHALL return a
    typed early-exit error.
14. WHEN session establishment exceeds Session_Startup_Timeout, THE connector SHALL
    return Feature 2's typed startup-timeout error.
15. WHEN startup fails after child creation, THE PendingConnection guard SHALL arrange
    for child reaping.
16. WHEN startup fails after stream-task creation, THE PendingConnection guard SHALL
    arrange for task termination.
17. WHEN connection establishment is cancelled, THE PendingConnection guard SHALL
    begin child termination synchronously.
18. WHEN session parameters and HTTP transport are ready, THE connector SHALL transfer
    all owned resources into one CliSessionConnection.
19. WHEN resource transfer succeeds, THE connector SHALL disarm the
    PendingConnection guard exactly once.
20. THE stable connector SHALL avoid constructing the beta DaggerSessionProc owner.

### Requirement 9: Authenticated, Trace-Propagating HTTP

**User Story:** As a Dagger-in-Dagger and distributed-tracing user, I want authenticated
requests to preserve trace lineage without exposing the session token beyond the
loopback engine.

#### Acceptance Criteria

1. WHEN implicit session parameters are valid, THE transport SHALL target
   `127.0.0.1`.
2. WHEN implicit session parameters are valid, THE transport SHALL target the validated
   session port.
3. WHEN a GraphQL request is built, THE transport SHALL target the `/query` path.
4. WHEN a GraphQL request is built, THE transport SHALL set `application/json` content
   type.
5. WHEN a GraphQL request is built, THE transport SHALL set the session token as the
   Basic username.
6. WHEN a GraphQL request is built, THE transport SHALL set an empty Basic password.
7. WHEN an active OpenTelemetry span context is valid, THE propagator SHALL prefer it
   over process environment trace context.
8. WHEN no active OpenTelemetry span context is valid, THE propagator SHALL extract
   W3C trace state from uppercase process environment keys.
9. WHEN baggage is present in the selected context, THE propagator SHALL preserve it.
10. WHEN a new CLI is launched, THE connector SHALL inject selected W3C propagation
    values into its environment.
11. WHEN an implicit HTTP request is sent, THE transport SHALL inject current W3C
    propagation values into request headers.
12. WHEN concurrent requests use different active contexts, THE transport SHALL inject
    each request's own context.
13. WHEN a response redirects away from the validated loopback authority, THE
    transport SHALL return a typed HTTP protocol failure.
14. WHEN proxy environment is configured, THE loopback transport SHALL avoid routing
    through that proxy.
15. WHEN authentication fails, THE transport SHALL avoid rendering the Authorization
    header.
16. WHEN a request fails, THE transport SHALL avoid rendering the session token.
17. WHEN an Existing_Session Client closes, THE transport SHALL avoid sending a
    shutdown signal to the engine.

### Requirement 10: Diagnostic Isolation and Background Observability

**User Story:** As an operator diagnosing startup and shutdown, I want useful ordered
diagnostics without credential leakage or invisible background failures.

#### Acceptance Criteria

1. WHEN the Control_Line is consumed, THE connector SHALL represent it only as
   `DiagnosticInput::SessionControl`.
2. WHEN the Control_Line is consumed, THE Diagnostic_Sink SHALL receive none of its
   bytes.
3. WHEN the Control_Line is consumed, THE tracing subscriber SHALL receive none of its
   bytes.
4. WHEN stdout follows the Control_Line, THE connector SHALL stream it as
   `DiagnosticStream::Stdout`.
5. WHEN stderr is read, THE connector SHALL stream it as
   `DiagnosticStream::Stderr`.
6. WHEN stdout and stderr events reach the dispatcher, THE dispatcher SHALL serialize
   sink callback invocation.
7. WHEN known session tokens appear in a diagnostic payload, THE connector SHALL redact
   them before dispatch.
8. WHEN configured environment values appear in a diagnostic payload, THE connector
   SHALL redact them before dispatch.
9. WHEN stderr is retained for failure explanation, THE connector SHALL retain at most
   the final one MiB.
10. WHEN stderr exceeds the retained bound, THE connector SHALL continue live streaming
    without growing the Diagnostic_Snapshot.
11. WHEN the Diagnostic_Sink returns an error, THE dispatcher SHALL disable it without
    failing the transport operation.
12. WHEN the Diagnostic_Sink panics, THE dispatcher SHALL disable it without unwinding
    through the transport operation.
13. WHEN a Diagnostic_Sink failure is traced, THE dispatcher SHALL avoid formatting
    the caller-controlled failure.
14. WHEN a stdout stream task fails, THE Session_Resource SHALL retain a typed
    background result.
15. WHEN a stderr stream task fails, THE Session_Resource SHALL retain a typed
    background result.
16. WHEN the child exits unexpectedly after connection transfer, THE Session_Resource
    SHALL retain its terminal status.
17. WHEN Client close observes a retained background failure, THE Client SHALL return a
    typed shutdown failure.
18. WHEN startup fails, THE connector SHALL include only the redacted bounded
    Diagnostic_Snapshot in inspectable error detail.
19. WHEN ordinary transport events are traced, THE tracing fields SHALL omit raw
    environment values.
20. WHEN ordinary transport events are traced, THE tracing fields SHALL omit
    credentials.

### Requirement 11: Typed Transport, GraphQL, and Engine-Domain Failures

**User Story:** As a Rust application author, I want failures distinguished without
loss of engine information, so that I can respond programmatically and still inspect
partial GraphQL results.

#### Acceptance Criteria

1. THE stable error API SHALL distinguish every layer in the Error Taxonomy Policy.
2. WHEN an error wraps a safe underlying source, THE stable error API SHALL retain that
   source for inspection.
3. WHEN an HTTP connection exceeds HTTP_Connect_Timeout, THE Client SHALL return the
   typed transport-connect timeout defined by Feature 2.
4. WHEN a complete request exceeds GraphQL_Execution_Timeout, THE Client SHALL return
   the typed execution timeout defined by Feature 2.
5. WHEN an HTTP response has a non-success status, THE transport SHALL return a typed
   HTTP-status failure.
6. WHEN a non-success HTTP response contains a valid GraphQL body, THE transport SHALL
   preserve its GraphQL errors for inspection.
7. WHEN a success HTTP response contains GraphQL errors, THE Client SHALL preserve the
   complete Raw_Response.
8. WHEN a GraphQL error lacks `_type`, THE Client SHALL preserve it as a generic
   GraphQL error.
9. WHEN a GraphQL error has an unknown `_type`, THE Client SHALL preserve it as a
   generic GraphQL error.
10. WHEN a GraphQL error has `_type = EXEC_ERROR` with valid fields, THE Client SHALL
    expose a typed execution error.
11. WHEN a typed execution error is exposed, THE error SHALL retain the engine message.
12. WHEN a typed execution error is exposed, THE error SHALL retain the command
    arguments.
13. WHEN a typed execution error is exposed, THE error SHALL retain the exit code.
14. WHEN a typed execution error is exposed, THE error SHALL retain stdout.
15. WHEN a typed execution error is exposed, THE error SHALL retain stderr.
16. WHEN a typed execution error is exposed, THE error SHALL retain the complete
    extensions object.
17. WHEN a typed execution error is exposed, THE error SHALL retain the complete
    Raw_Response.
18. WHEN an `EXEC_ERROR` member has an invalid type, THE Client SHALL avoid panic.
19. WHEN an `EXEC_ERROR` member has an invalid type, THE Client SHALL preserve the
    original generic GraphQL error.
20. WHEN a typed execution error is formatted ordinarily, THE error SHALL avoid
    appending stdout or stderr automatically.
21. THE stable transport error API SHALL avoid exposing `eyre::Error`.
22. THE stable transport library path SHALL avoid `unwrap` for runtime input and
    background-task outcomes.

### Requirement 12: Exact Target Compatibility Response

**User Story:** As a Rust SDK consumer, I want an explicit response when an implicit
engine is outside the declared target, so that the exact compatibility claim is true at
runtime rather than only in release metadata.

#### Acceptance Criteria

1. WHEN an implicit HTTP transport becomes ready, THE connector SHALL query public
   `Query.version` before returning the Client.
2. WHEN the engine version response is received, THE connector SHALL parse its Dagger
   semantic version without treating build metadata as a wider compatibility range.
3. WHEN the observed engine semantic version equals `v1.0.0-beta.10`, THE connector
   SHALL continue exact-target validation.
4. WHEN the observed engine semantic version differs from `v1.0.0-beta.10`, THE
   connector SHALL return a typed unsupported-target error.
5. WHEN the observed engine includes VCS build provenance, THE connector SHALL require
   it to agree with Target_Revision.
6. WHEN observed identity cannot prove the Exact_Target, THE connector SHALL return a
   typed unverified-target error.
7. WHEN compatibility validation fails for a new CLI session, THE PendingConnection
   owner SHALL terminate and reap the child.
8. WHEN compatibility validation fails for Existing_Session, THE connector SHALL leave
   the external engine running.
9. WHEN compatibility validation fails, THE error SHALL expose the expected safe target
   identity.
10. WHEN compatibility validation fails, THE error SHALL expose the safely parsed
    observed identity when one exists.
11. WHEN compatibility validation fails, THE error SHALL avoid exposing authentication
    and trace headers.
12. WHEN Compatibility_PATH_Fallback selects an exact compatible engine, THE connector
    SHALL permit connection after the warning.
13. WHEN Compatibility_PATH_Fallback selects an outside engine, THE connector SHALL
    return the typed compatibility failure.
14. WHEN an Explicit_Connection is selected, THE connector SHALL bypass the implicit
    target handshake.
15. WHEN the compatibility claim changes in a later target transition, THE generated
    runtime target constants SHALL change through the target-transition workflow.

### Requirement 13: Bounded Shutdown and Reliability

**User Story:** As a service operator, I want close and failure paths to terminate in a
bounded, observable way, so that a wedged CLI cannot hang application shutdown.

#### Acceptance Criteria

1. WHEN a CLI-owned Client closes, THE Session_Resource SHALL close child stdin as the
   graceful shutdown signal.
2. WHEN child stdin closes, THE Session_Resource SHALL wait for the child under a
   300-second shutdown bound.
3. WHEN the child exits within the shutdown bound, THE Session_Resource SHALL reap it.
4. WHEN the child exceeds the shutdown bound, THE Session_Resource SHALL start forced
   termination.
5. WHEN forced termination starts, THE Session_Resource SHALL arrange for child
   reaping.
6. WHEN child reaping completes, THE Session_Resource SHALL join its owned stdout task.
7. WHEN child reaping completes, THE Session_Resource SHALL join its owned stderr task.
8. WHEN graceful shutdown succeeds without background failure, THE Client SHALL return
   successful close.
9. WHEN graceful shutdown observes an unexpected child status, THE Client SHALL return
   a typed child-shutdown error.
10. WHEN forced termination is required, THE Client SHALL return a typed shutdown
    timeout error.
11. WHEN a stream task fails during shutdown, THE Client SHALL return a typed background
    shutdown error.
12. WHEN more than one shutdown component fails, THE terminal close result SHALL retain
    every safe failure category.
13. WHEN close returns an error, THE error SHALL omit the session token.
14. WHEN close returns an error, THE error SHALL omit configured environment values.
15. WHEN close is called again, THE Client SHALL return Feature 2's same Terminal_Close_Result.
16. WHEN final-handle drop triggers the abort backstop, THE Session_Resource SHALL avoid
    blocking the destructor.
17. WHEN any failure path owns a child, THE stable library SHALL avoid leaving an
    unreaped zombie process.

### Requirement 14: Verification and Stable Documentation

**User Story:** As a maintainer reviewing a 1.0 transport, I want deterministic and live
evidence plus durable reasoning, so that future refactors cannot silently weaken source,
security, or lifecycle guarantees.

#### Acceptance Criteria

1. WHEN source selection is verified, THE test suite SHALL exercise every row of the
   Connection Source Policy.
2. WHEN fallback is verified, THE test suite SHALL exercise every row of the Download
   Fallback and Retry Policy.
3. WHEN platform selection is verified, THE test suite SHALL exercise every supported
   Archive_Descriptor without host-platform branching.
4. WHEN archive safety is verified, THE test suite SHALL generate malformed, oversized,
   ambiguous, and traversal fixtures.
5. WHEN cache publication is verified, THE test suite SHALL exercise concurrent first
   publication rather than only concurrent cache reuse.
6. WHEN cache publication is verified, THE test suite SHALL exercise independent
   provisioner instances sharing one cache.
7. WHEN Control_Line parsing is verified, THE test suite SHALL exercise arbitrary
   malformed and boundary-sized inputs without panic.
8. WHEN redaction is verified, THE test suite SHALL use high-entropy canary secrets and
   assert their absence from errors, Debug, diagnostics, and traces.
9. WHEN trace propagation is verified, THE test suite SHALL inspect exact child
   environment and HTTP header carriers.
10. WHEN request reliability is verified, THE test suite SHALL prove a failed operation
    is transmitted at most once.
11. WHEN background reliability is verified, THE test suite SHALL inject stdout,
    stderr, child, and join failures separately.
12. WHEN target compatibility is verified, THE test suite SHALL exercise exact,
    outside, malformed, and unprovable engine identities.
13. WHEN target-scoped verification is recorded, THE live test SHALL establish a Rust
    Client through the stable default connector against the Exact_Target.
14. WHEN target-scoped verification is recorded, THE live test SHALL execute at least
    one authenticated GraphQL request.
15. WHEN target-scoped verification is recorded, THE live test SHALL close the
    CLI-owned Client and prove child reaping.
16. WHEN a deterministic test does not execute an engine, THE evidence record SHALL not
    mislabel it as target-scoped live conformance.
17. THE concrete connector module SHALL document source precedence and resource
    transfer invariants.
18. THE provisioning module SHALL document integrity, cache-lock, and publication
    invariants.
19. THE session module SHALL document Control_Line isolation and shutdown invariants.
20. THE transport module SHALL document authentication, redirect, retry, and trace
    propagation boundaries.
21. THE error module SHALL document inspectable fields and redaction guarantees for
    every public transport error.
22. WHEN non-obvious concurrency or security logic is implemented, THE source SHALL
    include a WHY comment describing the invariant it preserves.
23. WHEN implementation comments are authored, THE source SHALL avoid references to
    spec feature numbers or task numbers.
24. WHEN implementation comments are authored, THE source SHALL avoid narrating
    obvious control flow.

## Iteration and Feedback Notes

- The definitive Go SDK informs this target but does not dictate Rust structure.
  Private dependency injection replaces mutable URL globals; typed variants replace
  stringly errors; one Feature 2 resource owner replaces parallel process wrappers.
- The Go provisioning test deliberately serializes first download. Feature 3 adds the
  missing concurrent first-publication guarantee because the umbrella explicitly
  requires race-safe retention.
- PATH fallback remains intentionally narrow. General retries or “try something else”
  behaviour would hide integrity and configuration failures and could repeat mutating
  GraphQL operations.
- The exact runtime target handshake is required by Feature 1's compatibility claim.
  It does not imply that every future Rust release must remain exact-target-only; a
  future target transition may widen the range only with its own boundary evidence.
- Linux, macOS, and Windows archive logic belongs here. Feature 8 still owns live
  platform-matrix evidence and must retain its blockers until those runs exist.
- The design must derive executable properties for source determinism, fallback
  monotonicity, archive bounds, atomic publication, protocol isolation, redaction,
  at-most-once requests, resource cleanup, error preservation, and target compatibility
  before implementation tasks are authored.
