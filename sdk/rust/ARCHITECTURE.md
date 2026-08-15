# Rust SDK architecture

The Rust SDK exposes one owned `Client` while keeping source selection, CLI
provisioning, process control, HTTP, propagation, compatibility, and shutdown private.
Generated bindings and raw GraphQL share the same session lease, so no generated
handle can outlive or bypass the client's lifecycle state.

## Workspace ownership

- `crates/dagger-sdk` owns the public client, generated bindings, module runtime
  bridges, implicit connector, raw GraphQL values, diagnostics, errors, and session
  lifecycle. It is one of exactly two publishable crates.
- `crates/dagger-sdk-macros` is the exact-version procedural-macro companion re-exported
  by `dagger-sdk`. It owns authoring syntax and typed crate-local bridge emission only;
  it has no runtime dependency on `dagger-sdk` and performs no schema, I/O, engine, or
  dispatch work.
- `crates/dagger-codegen` is a workspace-private compiler that converts the pinned
  engine schema into Rust source. Generated output is reviewed through fixtures and is
  never edited by hand.
- `crates/dagger-bootstrap` supports code-generation bootstrapping and is not a
  publishable application dependency.
- `crates/dagger-sdk-engine` owns the private, data-only operation compiler, Cargo
  adoption, generated ownership, descriptor, runtime, and protocol contracts used by
  the engine adapter. It is deliberately absent from the public SDK dependency graph.
- `crates/dagger-sdk-completeness` is workspace-private and derives the source
  inventory, ledger, evidence, and reports used to measure the Rust SDK against the
  pinned Go SDK, engine schema, common SDK harness, and Rust policy.

The public package graph is deliberately closed and acyclic: an application depends on
`dagger-sdk`, which pins `dagger-sdk-macros` to the same exact version. Code generation,
engine integration, bootstrap, and completeness crates cannot enter that graph.

## Standalone-client generation

Standalone-client work is split across four fail-closed boundaries. `dagger-codegen`
purely compiles complete Core plus at most one selected module into a typed Rust plan;
it reuses the checked public Core catalog rather than regenerating Core. The private
engine crate discovers or adopts one Cargo project, reconciles semantic amendments,
and publishes only manifest-authorized generated files. The Go adapter translates
transient engine objects into those data-only operations. Generated applications then
use only the public `dagger-sdk` lifecycle and transport.

One client never absorbs transitive module dependencies. Each dependency is bound by
an independent client, giving it a separate pin, namespace, schema, project, manifest,
and regeneration boundary. Publication preserves unknown files and authored bytes;
the ownership manifest is committed last so failure cannot expose a partial next
generation. See [CLIENT_GENERATION.md](CLIENT_GENERATION.md) for the consumer and
maintainer contract.

## Module authoring and dispatch

Module authoring has one source of intent and two checked interpretations. Thin
procedural attributes preserve ordinary Rust type checking and emit crate-local typed
bridges. The private source compiler reads the same grammar, resolves the complete
Cargo module/type closure, and produces one canonical descriptor. Registration,
introspection, codecs, dispatch, and generated entrypoint assets are all derived from
that descriptor; authors do not maintain a second schema or dispatcher.

Calls enter one generated adapter which snapshots the active `FunctionCall` into a
typed envelope. Registration is selected only by an empty parent name; an empty
function name remains the constructor contract. Invocation validates parent state and
all named arguments before user code runs, re-enters handles through the active shared
session, and elects one value, structured application error, cancellation, panic-safe
failure, or publication failure. State, context, cancellation, telemetry, and result
sinks remain call-local.

Generated module assets carry target, descriptor, input-domain, and ownership digests.
Checked mode compares them directly. A scoped refresh is authorized only by a changed
authoring, visible-schema, target, or generator identity, and manifest-authorized
publication never adopts unknown user content.

## Connection pipeline

`ClientConfig` construction is pure. `connect_with` consumes it, performs preflight,
and selects exactly one source in this order:

1. a caller-supplied `EngineConnection`;
2. an existing session identified by `DAGGER_SESSION_PORT` and
   `DAGGER_SESSION_TOKEN`;
3. the configured `_EXPERIMENTAL_DAGGER_CLI_BIN`; or
4. the exact CLI release compiled from the completeness target.

A present source is authoritative. Invalid existing-session values do not fall through
to a CLI, and an invalid explicit CLI does not trigger a download. The sole compatibility
edge is a checksum-manifest HTTP 403 or 404, which may resolve the canonical `dagger`
executable from the process snapshot captured during preflight.

Caller-supplied connections transfer directly into the shared lifecycle and bypass the
implicit connector. Every other source remains inside an armed pending owner until the
session protocol, transport construction, and exact-target compatibility probe succeed.
An error or cancellation before that transfer uses the same cleanup path.

## Provisioning and cache publication

The compiled release descriptor fixes the version, platform archive, expected member,
and official HTTPS release origin. Provisioning first parses a bounded checksum
manifest, then streams the bounded archive into private temporary state while hashing
every byte. Extraction accepts exactly one regular `dagger` or `dagger.exe` member and
never joins an archive-controlled path to the cache root.

Cache hits are validated without following symlinks and need no network access. Cache
misses download outside the cross-process lock, then lock, revalidate, flush, set native
permissions, and atomically publish. The executable and lock lease remain owned through
spawn so retention cannot replace the file between validation and execution. Retention
runs under the same lock and may remove only older SDK-managed entries; failure is a
non-fatal redacted diagnostic.

## Session control and diagnostics

The SDK starts a CLI with stable Rust SDK labels and a complete projection of validated
configuration. Native executable-busy startup may retry at most ten attempts with
bounded cancellable backoff; every other spawn error is terminal.

The first stdout line is bounded control data containing the session port and token. It
is parsed once and is structurally unable to enter the diagnostic sink. Stderr remains
sealed until the token is registered with the streaming redactor. Subsequent stdout and
stderr are drained independently, delivered in one sink order, and retained only as
bounded redacted tails. A sink error or panic disables that sink without interrupting
protocol work or child reaping.

## Transport, propagation, and compatibility

Implicit transport constructs its endpoint internally from a validated non-zero port.
It dials `127.0.0.1`, sends the session token as the Basic-auth username with an empty
password, ignores proxies, rejects redirects, and transmits each GraphQL operation at
most once. Request encoding, HTTP status, response decoding, GraphQL, and engine-domain
failures retain separate typed boundaries.

Each client owns stateless W3C trace-context and baggage propagators. A valid context
from the active `tracing` span takes precedence over the environment captured at
preflight. Context is injected into a new CLI and into each HTTP request without
reading or changing OpenTelemetry's process-global propagator.

Before an implicit connection becomes public, a constant raw `Query.version` probe
requires the compiled semantic version and clean revision prefix. The compatibility
bypass applies only when provenance is unprovable; known version and revision
mismatches always fail. A caller-supplied `EngineConnection` remains a complete
abstraction and therefore does not receive this probe.

## Shared shutdown

Every `Client`, `QueryBuilder`, and generated handle owns an application lease on one
`SharedSession`. Close has one `Open -> Closing` election and one immutable terminal
result shared by all callers. Requests admitted after close fail before transport;
already-admitted requests either finish or report interruption.

For an SDK-owned CLI, explicit close first closes stdin, waits a bounded interval for
graceful exit, then kills and reaps if necessary before joining stream tasks. Failures
are aggregated in deterministic order with bounded redacted diagnostic tails. Dropping
the final application lease starts non-blocking cleanup, while the connection's
idempotent abort operation remains the no-runtime backstop.

## Stable boundary and verification

`lib.rs` is the intentional public namespace. Provisioners, cache state, process
guards, credentials, Reqwest values, propagators, compatibility probes, clocks, and
fixture controls are private. Public errors expose safe semantic coordinates and typed
inspection methods; ordinary `Display` and `Debug` do not interpolate URLs, paths,
tokens, response bodies, command output, or opaque callback text.

The stable surface is fenced by a normalized API manifest, compile-pass and
compile-fail fixtures, denied rustdoc warnings, source-policy tests, deterministic
properties, portable process/HTTP/archive/cache fixtures, and an isolated exact-target
default-connector run. The completeness crate admits status changes only from
machine-readable evidence with exact target and capability scope.

## Built-in engine boundary

The built-in Rust SDK is packaged as an acyclic OCI payload: the private operation
binary and its canonical descriptor are built before the engine embeds their content
digests. The Go layer under `core/sdk` is therefore an ABI adapter only. It translates
engine calls into closed Rust operations and applies validated changesets; Cargo,
schema interpretation, generated ownership, diagnostics, runtime provenance, and
security policy remain Rust-owned.

Operation manifests—not filenames or Go symbol names—own generated files. Unknown
content is preserved or rejected, never silently adopted. Runtime provenance is
two-phase so pre-build input validation cannot fabricate the post-strip binary digest,
and the final container is rebuilt from a clean digest-pinned base without Cargo
caches, source, credentials, or builder state.

The private entrypoint proves registration and invocation hooks against the nested
engine session. Those hooks cannot stand in for arbitrary module dispatch or complete
standalone-client content, which remain separately scoped work. See
[ENGINE_INTEGRATION.md](ENGINE_INTEGRATION.md) for the reproducible build audit,
focused case workflow, and exact-target evidence rules.

## Implementation closure and SDK sign-off

Local Feature closure is Rust-first and engine-free. A closed typed planner admits only
scoped Cargo actions, direct Rust-owned Go ABI tests, generated drift/ownership checks,
package policy, security, and clean-output inspection. It records elapsed time and the
checked-asset decision, rejects Dagger, engines, network graphs, unrelated SDKs, and
distribution builds, and admits closure only when every required gate passed against
one implementation identity.

SDK sign-off is a later exact-target observation. It consumes that closure rather than
replaying it, binds one artifact, runtime, generated-assets set, engine service, and
installed Rust baseline to a closed isolated case inventory, and emits one atomic
digest-bound verdict. A failed, skipped, stale, missing, duplicated, or overbroad case
admits no partial sign-off evidence. The maintainer procedure is documented in
[MODULE_AUTHORING.md](MODULE_AUTHORING.md); the standalone-client procedure and its
distinct five-case inventory are in [CLIENT_GENERATION.md](CLIENT_GENERATION.md).
