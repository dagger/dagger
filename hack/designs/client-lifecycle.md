# Engine client lifecycle and reclamation

## Status

Implementation in progress. The correctness foundations through separate client
record/runtime storage are complete; cold-capability decoupling and reclamation
are still deliberately disabled.

### Implementation progress

Completed, in dependency order:

1. Lifecycle debug snapshots report records, retained runtimes, closed state,
   active requests, typed lease owners, provider topology, and clientdb handle
   cardinality without treating zero leases as proof of reclaimability.
2. Client ancestry is stored as immutable root-to-parent ID routes. Registration
   rejects missing roots, cycles, ancestry mismatch, and ID reuse; telemetry and
   other consumers resolve ancestors from the session record graph instead of
   retaining runtime pointers.
3. Trace and log providers, processors, queues, routing, and final shutdown are
   session-owned. Emissions carry an immutable origin client ID, nested telemetry
   fans out through the validated ancestry route, clientdb operations release
   their handles, and session teardown stops producers before the final
   force-flush and provider shutdown barrier. Metrics intentionally remain per
   runtime for now.
4. `ClientScope` and typed lifecycle leases exist. Accepted requests carry an
   immutable per-scope metadata snapshot, and agents, services, lazy evaluation,
   and shared DagQL work explicitly clone one typed lease when detaching. Agent
   tombstones retain their durable lease because their snapshots are not yet
   self-contained, replacing it on relaunch and releasing it at session teardown;
   service and shared-work leases release at observed terminal transitions.
5. Engine-owned container proxies and in-engine Dang proxies explicitly register
   one unique nested transport from the creating context's held `ClientScope`
   before serving. Registration validates the exact session authority, parent
   identity, and immutable ancestry while atomically cloning the parent's
   `child` lease and publishing the child's `transport` lease. The opaque handle
   closes idempotently from proxy exit or `/shutdown`; close before or during the
   first request leaves a permanent closed tombstone, and closed or duplicate IDs
   cannot reconnect. A held scope may still delegate while its parent record is
   closing.
6. Registration and every initialization request reconcile deeply cloned client
   metadata under the session lifecycle serialization point. Missing fields may
   be completed until the first runtime initialization seals one immutable
   snapshot; identical replay remains valid, while conflicts and post-seal
   completion are rejected. Scope acquisition and metadata lookup require and
   clone that snapshot, and sealing/initialization serialize with transport close
   and authoritative session teardown.

7. Client identity and routing records are stored separately from retained
   execution runtimes. Metadata, ancestry, telemetry, attachable, and
   executable lookups are purpose-specific; executable runtime access requires
   a held `ClientScope`. Both maps remain session-long, and zero leases never
   trigger reclamation.

Intentional interim retention remains visible: child quiescence is not yet
implemented, so each parent's `child` lease is retained after transport close and
released safely during authoritative session teardown. Records and execution
runtimes are now separate, but both remain session-long while cold capabilities
and per-runtime metrics still depend on retained execution state. No quiescence
inference or reclamation is enabled yet.

### Next implementation seam

Decouple cold capabilities from retained execution runtimes without enabling
reclamation yet:

1. Make workspace host access session-owned and capture only immutable owner
   metadata plus the minimal host gateway capability instead of rediscovering a
   retained runtime by client ID.
2. Audit cached lazy/shared callbacks and require each path either to retain a
   documented durable scope lease or to capture a self-contained capability
   that no longer points back to runtime query/schema/engine state.
3. Audit module/schema objects and agent tombstones for retained runtime
   pointers. Preserve every current typed lease until the corresponding cold
   value is demonstrably self-contained.
4. Keep records and runtimes session-long, metrics per runtime, and child leases
   retained until teardown throughout this seam.

Stop after that seam. Do not infer child quiescence, migrate metrics, or reclaim
runtimes yet. Once cold capabilities no longer depend accidentally on runtime
identity, continue with session-owned metrics and finally quiescent runtime
reclamation.

## Problem

A long-running session creates a fresh engine client for every nested SDK
invocation. Those IDs are unique, but `daggerClient` instances are retained in
`daggerSession.clients` until the main session ends.

At the time of the proposal, that retention was expensive. Every client owned a
DagQL server and schema, an engine utility client, module and workspace state,
three OpenTelemetry providers, one large trace queue, one log queue, periodic
metric readers, and a long-lived clientdb reference with three stream goroutines.
The completed session-telemetry seam has removed the per-client trace/log queues,
providers, and DB keepalive, but runtimes and per-client metrics are still retained
for the session. A session evaluating mostly Dang modules therefore still makes
Dang dominate the remaining profile, though the lifecycle problem applies to
every SDK.

At the time of the proposal, calling `/shutdown` from SDK runtimes did not solve
this. Containerized SDKs did not consistently call it, and the in-engine Dang
runtime had no independent session transport. Explicit proxy-owned registration
now closes reachability correctly, but proxy exit is still not proof that the
client runtime is unused. The engine deliberately lets work outlive the request
that created it:

| Consumer | How it outlives a request | What it later needs |
|---|---|---|
| Agent runtime | `AgentRuntime.start` detaches the loop context | Query, DagQL server, client metadata, telemetry |
| Service runtime | Service and tunnel starts detach their contexts | Engine utility client, attachables, telemetry |
| Lazy/shared DagQL work | Cache evaluation detaches shared callbacks | Original call, query, client identity, telemetry |
| Workspace | Stores its creator's `ClientID` | Creator metadata and engine utility client |
| Secret/socket | Stores a source or binding client ID | Session resource binding and possibly attachables |
| Nested telemetry | Fans out through the client's ancestor chain | Routing identity and clientdb destinations |

The reverted retirement change waited for HTTP requests and descendant clients,
but none of the other consumers above. It could shut down a tracer provider
while an agent still used it, producing noop spans; it could also remove the
client that `SpecificClientMetadata`, workspace access, or an agent's next tool
call expected to find. The observed `hash of unhashable type noop.Span` panic was
a latent `map[trace.Span]...` bug exposed by that invalid lifecycle transition,
not evidence that retirement was otherwise safe.

The core problem is that one object represents five different lifetimes:

1. **Reachability**: a proxy or transport can submit new requests.
2. **Requests**: accepted requests are still running.
3. **Background work**: agents, services, or shared evaluations are running.
4. **Durable capabilities**: cached values may refer to the client later.
5. **Telemetry delivery**: buffered records and subscriptions still need routing.

`activeCount` observes only the second lifetime. `/shutdown` observes only the
first. Neither is a safe reclamation condition.

## Invariants

Any implementation must preserve all of these:

1. Closing a proxy prevents new requests through that proxy but does not cancel
   work already accepted by the engine.
2. A client runtime is not reclaimed while a request, agent, service, lazy
   callback, or descendant runtime can execute with its identity.
3. A durable value either remains usable for its documented session lifetime or
   fails because its external attachable disappeared, never because unrelated
   client cleanup removed hidden state.
4. Telemetry emitted by detached work remains recordable and follows the same
   nested-to-ancestor visibility rules as synchronous work.
5. Client IDs are never reused or resurrected. Once reachability closes, the ID
   cannot accept a new transport even if its descriptor remains.
6. Session teardown is authoritative: it prevents new work, cancels and waits for
   all background work, releases resources that may emit cleanup telemetry, then
   flushes and shuts down telemetry as the final barrier, closes session resources,
   and only then removes descriptors.
7. Reclamation is idempotent and race-safe. A concurrent close, last request,
   last lease, and session teardown may run in any order.
8. Resource cost after an idle period is bounded by live work and durable data,
   not by the number of SDK invocations that have occurred.

## Proposed model

Split today's `daggerClient` into a lightweight session record and a reclaimable
execution runtime.

```go
type clientRecord struct {
    id        string
    parentIDs []string
    metadata  clientMetadataSnapshot

    // Monotonic state: open -> closed. Never reopened.
    accepting bool

    // Nil once quiescent. Protected by the session lifecycle lock.
    runtime *clientRuntime
}

type clientRuntime struct {
    dag          *dagql.Server
    query        *core.Query
    engine       *engineutil.Client
    schema       *core.SchemaBuilder
    // workspace/module initialization state, etc.

    leases clientLeaseSet
}
```

The record is an identity and routing descriptor. It has no goroutines, queues,
database handles, or provider shutdown semantics. Records may initially live for
the whole session; that is cheap and makes correctness simple. Reclaiming unused
records can be a later optimization.

Metadata has a short, explicit bootstrap phase rather than a general mutable
lifetime. Registering a transport creates the record, and the attachables, `/init`,
and first request may monotonically fill fields that arrive on those adjacent
initialization paths. The record seals its final immutable metadata snapshot before
the first executable query can launch background work. Later identical metadata is
accepted, while a conflicting lifecycle-relevant update is rejected. A
`ClientScope` therefore never depends on metadata that might arrive after the work
starts.

The runtime is the capability to execute as that client. Nested reachability is
registered explicitly when the engine-owned container or Dang proxy is created,
not inferred from the first HTTP request. Registration returns one opaque transport
handle whose idempotent close marks the record closed and releases the transport
lease. Register-before-serve closes the race where proxy cleanup observes no
published client and an in-flight first request publishes one afterward. A unique
nested client ID has one owner transport; `/shutdown` is only an additional,
idempotent close signal.

The runtime has explicit leases:

| Lease kind | Acquired | Released |
|---|---|---|
| `transport` | Proxy/runtime becomes reachable | Proxy/runtime permanently exits |
| `request` | Request is authenticated and accepted | Handler completes |
| `agent` | An agent loop launches with the client scope | That loop terminates; a relaunch acquires its current scope |
| `service` | Running service captures the client scope | Service exit/stop is observed |
| `shared-work` | Detached lazy/shared callback starts | Callback and its release path complete |
| `child` | Nested runtime is created | Child runtime becomes quiescent |

The runtime becomes quiescent only when reachability is closed and every lease is
gone. The transition removes the runtime from executable lookup and releases its
heavy fields. The lightweight record remains available for identity, ancestry,
and diagnostics.

This intentionally does **not** claim that leases alone immediately make every
runtime reclaimable. A cached lazy value or workspace may be a cold capability:
it is not executing now, but it can initiate client-scoped work later. Those
paths must first be made independent of a live `daggerClient`, or must hold a
documented durable lease. The system must never infer safety from an empty
goroutine/request count.

Agent tombstones are another cold-capability case: their last LLM snapshot stays
addressable and may be resumed. A running, idle, or paused loop retains the scope
that launched that loop. Once a failed or stopped tombstone's snapshot no longer
depends on the original live runtime, it releases that scope; a later resume or
send acquires the new calling client's scope, and that client owns the relaunched
loop and its telemetry. Until the snapshot is self-contained, the tombstone must
hold a visible durable lease on the original runtime. The same rule applies to
exited-service diagnostics if they retain more than copied IDs and span contexts.

### Explicit client scope

Introduce a `ClientScope` value in context. It contains the session and client
IDs, an immutable metadata snapshot, and an opaque runtime lease handle. Code
that detaches client-bearing work must use an explicit operation:

```go
workCtx, release, err := query.DetachClientScope(ctx, "agent")
if err != nil { ... }
defer release()
```

`context.WithoutCancel` remains valid for cleanup-only contexts, but it is no
longer the lifecycle primitive for executable work. Agent and service registries
store the acquired scope and release it from their actual terminal transition.
DagQL shared-work coordination acquires one lease for the shared callback, not
one per waiter.

Nested client creation is strict capability delegation. The engine-owned proxy
must register its transport with the creating `ClientScope`, atomically cloning a
`child` lease before it becomes reachable. A scope already held by accepted work
may create a child while its record is closing, but a bare, stale, missing, or
mismatched caller ID cannot. A non-empty parent never silently degrades into a
root: intentional session/system roots use a separate explicit path. Child
quiescence releases the parent lease. This extra plumbing is deliberate; it makes
ancestry authorization, telemetry routing, and parent retention the same audited
operation.

This gives us one auditable boundary. A test hook can reject starting engine
work from a closed or unleased scope, and counters can attribute retained
runtimes to lease kinds.

### Separate execution from durable identity

The current APIs turn a client ID back into the entire `daggerClient`:
`SpecificClientMetadata`, `workspaceOwnerAccess`, telemetry handlers, host
attachable lookup, and cloud scale-out all use `clientFromIDs`. Replace that
single lookup with purpose-specific APIs:

| Need | New source |
|---|---|
| Metadata/ancestry | `clientRecord` |
| External attachable | Session attachable registry, keyed by source client ID |
| Telemetry route | Record's immutable parent-ID route |
| Execute as owner | A live `ClientScope`, or a self-contained capability |
| Workspace host access | Session-owned host gateway plus workspace owner metadata |

In particular, move `engineutil.Client` toward a session-owned gateway whose
methods take client metadata/scope. Most of its methods already derive routing
from `ClientMetadataFromContext`; per-client wrapper allocation should not be the
thing keeping an identity alive. Workspaces should capture the minimal host
access capability they need, rather than rediscovering a full client by ID.

Secrets and sockets already distinguish cached handles from concrete
session-local bindings. Their resolution should continue through the session
resource and attachable registries. A dead external attachable may produce the
existing clear "no active session attachables" error; retaining a heavyweight
client object cannot make that external process live again.

### Session-owned telemetry

Telemetry should be fixed before client reclamation. It is the dominant retained
cost, and its lifetime is naturally the session's, not an individual proxy's.

Create one trace provider and one logger provider per session. On span/log emit,
a processor stamps the origin client ID from `ClientScope`; a routing exporter
uses the session's lightweight record graph to write to the origin and ancestor
client DBs. Parent relationships become IDs rather than pointers to full client
objects. Incoming OTLP already has session/client headers and uses the same
router.

Clientdb references should be opened for an export/subscription and closed when
that operation ends. Remove the per-client keepalive DB. The DB manager may keep
an internal bounded idle cache if reopen cost is measurable, but that cache must
have an explicit size/time bound rather than one entry per historical client.

Metrics need a separate migration because an OTel meter provider aggregates
measurements before export. Use one session meter provider and require engine
measurements to carry origin client ID as an attribute; route or filter the
result at export. Until that conversion is complete, per-client meter providers
may remain behind runtime leases. They are not a reason to retain trace/log
queues or clientdb streams.

Provider shutdown then happens only during session teardown. Teardown first
prevents new root work, cancels and drains background leases, and releases cache
and session resources that may emit cleanup telemetry. A final force-flush is the
barrier after those producers have stopped; only then are the session providers
shut down, DB references closed, and descriptors removed. A detached agent cannot
accidentally inherit a provider that another goroutine shuts down.

### State machine

The lifecycle is monotonic:

| State | Accept new transport work | Existing work executes | Heavy runtime present |
|---|---:|---:|---:|
| `open` | yes | yes | yes |
| `closing` | no | yes | yes |
| `quiescent` | no | no | no |
| `removed` | no | no | no; record removed at session end |

Closing is triggered by the owner proxy/runtime, not by an SDK convention. SDK
`/shutdown` may be an additional signal, but correctness does not depend on every
language runtime implementing it. A unique ID cannot transition from `closing`
or `quiescent` back to `open`.

All state changes run through one session lifecycle operation. In pseudocode,
the only reclamation predicate is:

```text
session is live
AND record.accepting == false
AND runtime.totalLeases == 0
AND runtime is still the record's published runtime
```

The operation atomically unpublishes the runtime before running slow cleanup.
Lease acquisition first checks `accepting` under the same serialization point;
an already-held lease may be cloned for child/background work while closing,
but an unrelated request cannot acquire a new root lease. Session teardown wins
by changing session state first, then draining through the same release paths.
This prevents both resurrection and a last-release/new-acquire race.

## Rejected shortcuts

| Shortcut | Why it is insufficient |
|---|---|
| Have every SDK call `/shutdown` | Improves reachability signaling only; detached and cold capabilities remain |
| Retire on `activeCount == 0` | Counts HTTP handlers, not engine work started by them |
| Retire when no descendants remain | Parent pointers are only one retention path; agents, services, cache, and workspaces are invisible |
| Delay retirement for a grace period | Changes race probability, not the safety condition |
| Fix the noop-span panic and retry retirement | Prevents one symptom while leaving use-after-retirement failures elsewhere |
| Keep current clients but shrink queues | Reduces slope but still makes providers, readers, DB streams, and schemas proportional to historical calls |
| Reference-count `daggerClient` directly | Makes every accidental pointer an ownership edge and cannot explain cold capabilities; split identity from execution first |

## Enabling changes

Keep the remaining work separately reviewable. The checked seams are implemented;
the unchecked seams preserve the intended order from correctness foundations to
reclamation.

1. [x] **Add lifecycle observability without changing behavior.** Debug snapshots
   report records and runtimes by state, typed leases by kind and owner ID,
   provider/processor counts, clientdb handles, and why closed runtimes remain
   retained. Queue capacity is reported explicitly; OTel does not expose measured
   occupancy.
2. [x] **Replace client parent pointers with immutable parent IDs.** Route
   traversal is centralized in the session, registration rejects invalid ancestry,
   and telemetry no longer retains ancestor runtimes through parent pointers.
3. [x] **Move traces and logs to session-owned providers.** Explicit origin IDs
   route records to immutable ancestry, per-client trace/log queues and DB
   keepalives are gone, and final provider shutdown follows producer cleanup.
   Per-client metrics remain as an explicitly deferred migration.
4. [x] **Introduce `ClientScope` and typed leases.** Request entry, agent and
   service runtimes, DagQL shared/lazy work, and explicit detachment now carry
   auditable lifecycle ownership.
5. [x] **Register nested transports through strict scope delegation.** Container
   and Dang proxies register before serving, own one opaque idempotent handle,
   reject stale/duplicate/reused identities, and retain the parent `child` lease
   until session teardown while child quiescence is unavailable.
6. [x] **Seal client metadata after monotonic bootstrap.** Registration and
   initialization requests reconcile deeply cloned declarations under lifecycle
   serialization, reject conflicts and post-seal completion, and seal the
   snapshot before executable scope or query runtime publication.
7. [x] **Split lookup and storage.** Separate session-long `clientRecord` and
   `clientRuntime` maps now back metadata, telemetry-route, attachable, and
   executable-scope lookups. Record-only paths do not require a runtime, while
   executable lookup requires both a retained runtime and a held scope.
8. [ ] **Decouple cold capabilities.** Make workspace host access session-owned;
   verify cached lazy callbacks either acquire a durable scope lease or capture a
   self-contained capability. Audit module/schema objects and agent tombstones for
   pointers back to the client runtime.
9. [ ] **Move metrics to session ownership.** Preserve origin attribution through
   aggregation and routing so per-runtime meter providers and periodic readers no
   longer block runtime reclamation.
10. [ ] **Enable quiescent runtime reclamation.** Proxy close marks
    `accepting=false` and releases `transport`; one serialized transition drops the
    runtime only after every executable and durable lease, including children, is
    demonstrably gone.
11. [ ] **Optionally collect records.** Only after all durable references are
    enumerable, add descriptor refcounts/epochs. This is not required to solve the
    memory leak and must not be coupled to runtime reclamation.

The order matters. The completed telemetry and lease seams bound known retention
and make ownership visible without guessing. Metadata must be sealed before a
record/runtime split can safely make identity snapshots authoritative; cold
capabilities and metrics must then be independent before zero leases can become a
valid reclamation proof.

## Verification

### Deterministic lifecycle tests

- Closing a proxy with an idle client reaches `quiescent` and releases its
  runtime exactly once; closing concurrently with the first request cannot publish
  a runtime after the transport handle has closed.
- Metadata may be monotonically completed during bootstrap, is sealed before the
  first executable query, and rejects conflicting updates afterward.
- Closing during an active request reaches `closing`; the last request release
  performs reclamation.
- Closing while an agent is idle, running, paused, failed, or being resumed does
  not invalidate its context. A running or paused loop keeps its launching scope;
  a self-contained failed/stopped tombstone releases that scope, and a relaunch is
  owned by the new calling client.
- The same matrix applies to shared and interactive services, including crash and
  concurrent stop.
- Lazy evaluation triggered after its creating request either succeeds through a
  documented capability or holds a visible durable lease.
- Workspace reload/export still works after its creator proxy closes.
- Secret/socket handles resolve through session bindings; missing external
  attachables fail for that reason, not "client not found."
- A parent cannot quiesce before a live child; child release wakes parent
  reclamation. Nested transport registration without the creating scope, with a
  stale or mismatched scope, or with a missing parent fails instead of creating an
  implicit root.
- Concurrent proxy close, request completion, lease release, and session teardown
  pass under `-race` and every cleanup hook runs once.
- A closed client ID cannot reconnect or be initialized again in the same
  session, even while its record remains.

### Telemetry tests

- Synchronous and detached spans/logs from nested clients appear in the origin
  and every ancestor DB exactly once.
- Provider count is proportional to sessions, not clients.
- Clientdb stream goroutines return to the bounded idle baseline.
- Closing any nested proxy cannot turn a live agent's tracer into a noop tracer.
- Session flush includes records concurrently emitted by closing client scopes
  and establishes a clear flush barrier before provider shutdown.

### Stress acceptance test

Run one long-lived `dagger agent` session and repeatedly evaluate a mix of Dang,
Go, Python, and TypeScript modules, including agents, services, workspaces, lazy
objects, secrets, and sockets. After each workload wave, stop all deliberately
long-lived work and wait for two GC/telemetry intervals.

The acceptance criteria are:

- live client runtimes return to the known baseline;
- trace/log provider and queue counts remain constant for the session;
- clientdb goroutines and open streams remain within the configured idle bound;
- heap after forced GC plateaus across waves rather than tracking total
  invocations;
- all functional and telemetry assertions above continue to pass.

Capture heap, alloc, goroutine, and the lifecycle debug dump at every plateau.
Compare retained objects by client ID so a failure identifies the exact lease or
durable capability preventing reclamation.

## Non-goals

- Reusing client IDs. They remain unique per initialization.
- Making external attachables survive their owning process.
- Collecting every lightweight client record in the first implementation.
- Treating the `map[trace.Span]...` panic as a lifecycle mechanism. That map
  should be keyed by comparable span identity independently, but fixing it does
  not make premature provider shutdown safe.
