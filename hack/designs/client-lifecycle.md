# Engine client lifecycle and reclamation

## Status

Implemented through quiescent execution-runtime reclamation. Lightweight record
collection remains intentionally disabled. The safety and liveness contracts
are executable in TLA+; the Go tests cover the concrete synchronization
boundaries.

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
   force-flush and provider shutdown barrier. Metrics were deferred until typed
   leases could make their shorter runtime ownership safe in step 9.
4. `ClientScope` and typed lifecycle leases exist. Accepted requests carry an
   immutable per-scope metadata snapshot, and services, lazy evaluation, and
   shared DagQL work explicitly clone one typed lease when detaching. Service
   and shared-work leases release only after their observed terminal cleanup
   transitions. Agent lease kinds remain reserved for a future engine-owned
   detached agent runtime; current agent composition executes through request
   and shared-work scopes.
5. Engine-owned container proxies and in-engine Dang proxies explicitly register
   one unique nested transport from the creating context's held `ClientScope`
   before serving. Registration validates the exact session authority, parent
   identity, and immutable ancestry while atomically cloning the parent's
   `child` lease and publishing the child's `transport` lease. The opaque handle
   closes idempotently from proxy exit or `/shutdown`; close before or during the
   first request leaves a permanent closed record, and closed or duplicate IDs
   cannot reconnect. A held scope may still delegate while its parent record is
   closing; the child's later serialized quiescent transition releases the
   delegated parent lease.
6. Registration and every initialization request reconcile deeply cloned client
   metadata under the session lifecycle serialization point. Missing fields may
   be completed until the first runtime initialization seals one immutable
   snapshot; identical replay remains valid, while conflicts and post-seal
   completion are rejected. Scope acquisition and metadata lookup require and
   clone that snapshot. First initialization publishes a provisional request
   lease before leaving the admission serialization point, runs slow DagQL/schema
   construction without holding the scope lock, then re-enters admission to
   validate transport closure and publish the executable request.

7. Client identity and routing records are stored separately from execution
   runtimes. Metadata, ancestry, telemetry, attachable, and executable lookups
   are purpose-specific; executable runtime access requires a held `ClientScope`.
   Records remain session-long while the runtime map is now only the publication
   set for non-quiescent executable state.
8. Workspace host access uses a session-owned engine gateway plus immutable
   owner metadata from the record graph, so it no longer rediscovers an owner
   runtime. Workspace served-schema operations are different: they explicitly
   select the owner only when it is the currently scoped runtime or a validated
   live ancestor retained by that scope's child-lease chain. Metadata alone
   cannot select an arbitrary runtime. Every detached DagQL cache initializer
   now retains one explicit shared-work scope and drops its detached context
   when the callback finishes. Module/schema memoization is scoped to its owning
   query capability instead of a process-global cache.
9. Each live client runtime owns one meter provider and one periodic reader. Its
   exporter is bound to the client's immutable record and routes the whole
   collection to that record and its validated ancestors exactly once, without
   requiring origin attributes on every engine measurement or one reader per
   ancestor. The serialized zero-lease transition keeps the closed runtime
   published while metrics force-flush and shut down, then unpublishes it.
   Authoritative session teardown drains metrics for every remaining runtime
   before the final session trace/log barrier.

10. Quiescent runtime reclamation is enabled. Transport close serializes the
    monotonic acceptance stop and transport-lease release; every admitted
    request, including bootstrap requests, already owns its request lease before
    that lock is released. When a live session observes an empty typed lease set,
    one serialized transition marks the runtime reclaiming; its metric provider
    drains outside the lock before the runtime is atomically unpublished,
    execution-only fields are dropped, and the parent's `child` lease is released.
    Authoritative teardown first marks the session removed, disabling new
    reclamation starts while it drains the same idempotent edges and remaining
    metric providers.

The durable-capability audit remains explicit rather than optimistic. Workspace
host access, telemetry, metadata, ancestry, secrets/sockets, exited-service
records, and completed cache values use session records or self-contained data;
running services and callbacks retain executable leases through cleanup.

### Remaining optional seam

Record collection was not implemented. Records, sealed metadata, immutable
ancestry, telemetry routes, and closed-ID tombstones remain session-long, so a
closed client ID is never reused or resurrected. Descriptor collection remains
an optional, separately justified optimization and must not be inferred from
runtime reclamation.

## Problem

A long-running session creates a fresh engine client for every nested SDK
invocation. Those IDs are unique, and before change 10 each execution runtime was
retained in the session until the main session ended.

At the time of the proposal, that retention was expensive. Every client owned a
DagQL server and schema, an engine utility client, module and workspace state,
three OpenTelemetry providers, one large trace queue, one log queue, periodic
metric readers, and a long-lived clientdb reference with three stream goroutines.
The completed telemetry seams removed per-client trace/log providers, queues,
and DB keepalives. Metric providers and readers remain only on live runtimes and
are reclaimed with them. Change 10 also unpublishes and clears the remaining
DagQL/schema execution state when a closed client has no typed owner.

At the time of the proposal, calling `/shutdown` from SDK runtimes did not solve
this. Containerized SDKs did not consistently call it, and the in-engine Dang
runtime had no independent session transport. Explicit proxy-owned registration
now closes reachability correctly, but proxy exit is still not proof that the
client runtime is unused. The engine deliberately lets work outlive the request
that created it:

| Consumer | How it outlives a request | What it later needs |
|---|---|---|
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
3. **Background work**: services or shared evaluations are running.
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
| `agent` | Reserved for a future engine-owned detached agent loop | The loop's terminal transition |
| `agent-tombstone` | Reserved for a future runtime-backed dormant agent snapshot | Relaunch or session teardown |
| `service` | Running service captures the client scope | Service exit/stop and tracked-resource cleanup are observed |
| `shared-work` | Detached lazy/shared callback starts | Callback and its release path complete |
| `child` | Nested runtime is created | Child runtime becomes quiescent |

The runtime becomes quiescent only when reachability is closed and every lease is
gone. The transition removes the runtime from executable lookup and releases its
heavy fields. The lightweight record remains available for identity, ancestry,
and diagnostics.

Leases become a reclamation proof only after the cold-capability audit. A cached
lazy value or workspace may be idle now but initiate work later; implemented
paths are independent of a live runtime or retain a documented typed lease. The
system never infers safety from an empty goroutine/request count alone.

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
one per waiter. Detaching replaces only the held execution scope; it preserves
the context's effective client metadata because host/resource routing may have
been rebound independently, for example to a Workspace owner.

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
| Workspace served schema | Current held scope, restricted to its scoped runtime or a retained ancestor owner |

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

### Lifecycle-owned telemetry

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

Metrics instead use one meter provider and periodic reader per live client
runtime. Each exporter is bound to an immutable client record, so ordinary OTel
measurement attributes remain untouched and the whole aggregated collection can
be routed through the record graph. There is one reader per client, not one per
ancestor; reclaimed clients retain neither provider nor reader.

Client metric shutdown happens only after close and the typed lease count reaches
zero. Reclamation keeps the runtime published but non-accepting during that drain,
so concurrent session flushes cannot miss it, then unpublishes the runtime and
releases its parent edge. Session teardown prevents new root work, drains
background leases and cache cleanup, shuts down any remaining client metrics,
and finally force-flushes and shuts down the session trace/log providers. A
detached agent cannot accidentally inherit a provider that another goroutine
shuts down.

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
AND runtime is not already reclaiming
```

The operation atomically marks one reclamation owner, keeps the non-accepting
runtime published while its metric provider drains outside the lock, then
atomically unpublishes it before clearing heavy state and releasing its parent
edge. Keeping it published makes concurrent session flushes wait for that drain
instead of missing the provider. Lease acquisition first checks `accepting` under
the same serialization point; an already-held lease may be cloned for
child/background work while closing, but an unrelated request cannot acquire a
new root lease. Session teardown wins by changing session state first, then
draining through the same release paths. This prevents resurrection, duplicate
reclamation, and a last-release/new-acquire race.

### Cache release handoff

`dagql.Cache.ReleaseSession` intentionally remains a non-blocking tombstone
operation when cache work is active. In that case #13940 assigns cleanup to the
last active operation and returns before cleanup hooks run. Session teardown
therefore follows it with `WaitSessionRelease`, which is the completion barrier:
after it returns, the last operation and every per-session cleanup hook have
finished. Only then may the session stamp telemetry completeness and shut down
its providers.

Teardown also waits for `waitForClientScopeDrain`. This second barrier covers
detached shared and arbitrary callbacks whose client-runtime lease can outlive
their last request waiter even when they no longer hold a cache operation. The
two waits jointly establish the model's `cachePhase = "done"` and
`NoActiveProducers` preconditions.

This separation preserves both useful contracts: ordinary release does not
deadlock behind detached cache work, while authoritative teardown has a precise
point after which no cache cleanup can emit more telemetry.

## Executable model

Two TLA+ modules divide ownership at the same boundary as the Go code:

| Model | Owns | Project check |
|---|---|---|
| `dagql/tla/CacheLifecycle.tla` | Cache operation admission, release handoff, cleanup completion, and the release waiter | `dagger check tla-check:cache-lifecycle` |
| `engine/server/tla/ClientLifecycle.tla` | Requests, typed client leases, nested-client ownership, runtime reclamation, session teardown, and final telemetry shutdown | `dagger check tla-check:client-lifecycle` |

The client model treats cache cleanup as the abstract phases `live`, `deferred`,
`cleaning`, and `done`; the cache model proves the detailed implementation of
that abstraction. This avoids duplicating the large cache state space while
still checking the cross-subsystem ordering rule.

The modeled actions correspond to concrete serialization points:

| Modeled transition | Go boundary |
|---|---|
| Admit or finish a request | `acquireRootClientScope` and request cleanup |
| Start or finish detached work | `DetachClientScope` and `ClientLifecycleLease.Release` |
| Register or close a child transport | `RegisterNestedClientTransport` and `NestedClientTransport.Close` |
| Begin metric drain for a runtime | `maybeBeginClientRuntimeReclamationLocked` |
| Finish metric drain and reclaim | `shutdownMetrics` and `finishClientRuntimeReclamation` |
| Begin authoritative teardown | `markSessionRemoved` and `beginClientScopeTeardown` |
| Complete cache cleanup | `ReleaseSession` followed by `WaitSessionRelease` |
| Drain accepted producers | `waitForClientScopeDrain` |
| Stop telemetry | `shutdownTelemetry` |

Changes to these transitions should update the appropriate TLA+ action and
configuration first, run TLC, then update the Go implementation and focused
race tests. TLC verifies all interleavings in the bounded model; Go tests remain
responsible for proving that the real locks, atomics, contexts, and callbacks
implement those modeled atomic steps.

## Rejected shortcuts

| Shortcut | Why it is insufficient |
|---|---|
| Have every SDK call `/shutdown` | Improves reachability signaling only; detached and cold capabilities remain |
| Retire on `activeCount == 0` | Counts HTTP handlers, not engine work started by them |
| Retire when no descendants remain | Parent pointers are only one retention path; agents, services, cache, and workspaces are invisible |
| Delay retirement for a grace period | Changes race probability, not the safety condition |
| Fix the noop-span panic and retry retirement | Prevents one symptom while leaving use-after-retirement failures elsewhere |
| Hold the scope lock through runtime initialization | Initialization performs DagQL/schema work that clones lifecycle scopes; holding the same lock creates a lock-order cycle on cold concurrent schema builds |
| Keep current clients but shrink queues | Reduces slope but still makes providers, readers, DB streams, and schemas proportional to historical calls |
| Reference-count `daggerClient` directly | Makes every accidental pointer an ownership edge and cannot explain cold capabilities; split identity from execution first |

## Enabling changes

Keep the implementation seams separately reviewable. Checked seams are complete;
record collection remains the only optional unchecked optimization.

1. [x] **Add lifecycle observability without changing behavior.** Debug snapshots
   report records and runtimes by state, typed leases by kind and owner ID,
   provider/processor counts, clientdb handles, closed/quiescent timestamps, and
   why a closed runtime remains retained. Queue capacity is reported explicitly;
   OTel does not expose measured occupancy.
2. [x] **Replace client parent pointers with immutable parent IDs.** Route
   traversal is centralized in the session, registration rejects invalid ancestry,
   and telemetry no longer retains ancestor runtimes through parent pointers.
3. [x] **Move traces and logs to session-owned providers.** Explicit origin IDs
   route records to immutable ancestry, per-client trace/log queues and DB
   keepalives are gone, and final provider shutdown follows producer cleanup.
   Metrics were explicitly deferred until typed leases could prove their
   runtime-scoped shutdown boundary in step 9.
4. [x] **Introduce `ClientScope` and typed leases.** Request entry, service
   runtimes, DagQL shared/lazy work, and explicit detachment now carry auditable
   lifecycle ownership.
5. [x] **Register nested transports through strict scope delegation.** Container
   and Dang proxies register before serving, own one opaque idempotent handle,
   reject stale/duplicate/reused identities, and delegate one parent `child`
   lease that only the child's quiescent transition (or authoritative teardown)
   releases.
6. [x] **Seal client metadata after monotonic bootstrap.** Registration and
   initialization requests reconcile deeply cloned declarations under lifecycle
   serialization, reject conflicts and post-seal completion, and seal the
   snapshot before executable scope or query runtime publication.
7. [x] **Split lookup and storage.** Separate session-long `clientRecord` and
   reclaimable `clientRuntime` publication maps back metadata, telemetry-route,
   attachable, and executable-scope lookups. Record-only paths do not require a
   runtime, while executable lookup requires both a published runtime and a held
   scope.
8. [x] **Decouple cold capabilities.** Workspace host access uses immutable
   owner metadata and a session-owned gateway rather than owner runtime lookup.
   Workspace served-schema lookup may select that owner only when the current
   held scope's validated ancestry retains it. Detached lazy, shared-call, and
   arbitrary-cache callbacks hold one explicit shared-work lease and release
   their contexts at terminal transitions. Module/schema memoization is
   capability-owned instead of process-global.
9. [x] **Bind metrics to live runtimes.** One meter provider and periodic reader
   aggregate each live client's ordinary measurements. A record-bound exporter
   routes the collection to the origin and validated ancestor IDs exactly once.
   The zero-lease transition drains metrics before unpublishing the runtime;
   session teardown drains every remaining provider before trace/log shutdown.
10. [x] **Enable quiescent runtime reclamation.** Proxy close monotonically marks
    `accepting=false` and releases `transport`; admitted requests already own
    their typed lease, and one serialized zero-lease transition atomically
    unpublishes the runtime, clears execution-only fields, and releases its
    parent's `child` lease. Teardown remains authoritative and idempotent.
11. [ ] **Optionally collect records.** Only after all durable references are
    enumerable, add descriptor refcounts/epochs. This is not required to solve the
    memory leak and must not be coupled to runtime reclamation.

The order mattered. Telemetry and lease seams bounded retention, metadata sealing
made identity snapshots authoritative, and the cold-capability audit removed
accidental runtime dependencies before zero typed leases became the reclamation
proof. Runtime reclamation therefore does not require or imply record collection.

## Verification

### Deterministic lifecycle tests

- Closing a proxy with an idle client reaches `quiescent` and releases its
  runtime exactly once; closing concurrently with the first request cannot publish
  a runtime after the transport handle has closed.
- Metadata may be monotonically completed during bootstrap, is sealed before the
  first executable query, and rejects conflicting updates afterward.
- Closing during an active request reaches `closing`; the last request release
  performs reclamation.
- Closing while a shared or interactive service is running does not invalidate
  its context. The same matrix applies to service crash and
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
- Aggregated metrics from each record appear in that record and each validated
  ancestor exactly once, without a caller-supplied routing attribute.
- Trace/log provider and queue count is proportional to sessions; metric
  provider and reader count is proportional to live runtimes, not historical
  clients or ancestry depth.
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
- trace/log provider and queue counts remain constant for the session, while
  metric provider/reader counts return to the live-runtime baseline;
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
