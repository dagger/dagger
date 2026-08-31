# DagQL Cache Pruning And Retention

This document describes the current pruning and retention model for the `dagql`
cache.

The source of truth is the code, mainly:

- `dagql/cache.go`
- `dagql/cache_prune.go`
- `dagql/cache_egraph.go`
- `engine/server/gc.go`
- `engine/server/server.go`
- `engine/server/session.go`
- `engine/config/config.go`
- `cmd/engine/metrics.go`
- `core/schema/coremod.go`
- `engine/snapshots/persistent_metadata.go`

This doc is about:

- what keeps results alive
- what makes a result prunable
- how the prune algorithm works today
- how disk-size and structural-memory accounting differ
- how pruning hands actual snapshot cleanup off to containerd

## The Core Mental Model

The live cache is a DAG of materialized results.

Each `sharedResult` may depend on other `sharedResult`s through exact dependency
edges in `sharedResult.deps`.

Conceptually, retention works like graph reachability:

- if a result is reachable from one of the current retention roots, it stays
  alive
- if it is no longer reachable from any retention root, it is collected

The implementation does not literally maintain one explicit synthetic root node.
Instead, it maintains explicit classes of ownership edges, and
`incomingOwnershipCount` is the compact runtime summary of whether a result is
still retained.

Still, the "can I reach this from a root?" mental model is the right one.

## Important Separation: Equivalence Is Not Retention

The cache's e-graph tells us about equivalence and lookup reuse.
It does **not** by itself retain results.

Retention comes from explicit ownership edges:

- session ownership
- persisted edges
- exact dependency edges between results

This distinction matters a lot.

Examples of things that are **not** retention edges:

- term membership
- output eq-class membership
- digest equivalence
- result/digest indexes

A result may be equivalent to another result and still be collectible if nothing
owns it.

## The Runtime Truth: `incomingOwnershipCount`

`sharedResult.incomingOwnershipCount` is the authoritative liveness count.

It is incremented when the cache adds a real ownership edge and decremented when
that edge goes away.

When the count reaches zero, the result becomes collectible.

Collection then:

- removes the result from the e-graph and indexes
- runs any `OnRelease` hooks
- decrements ownership on its exact dependency results
- cascades transitively

So the runtime system is not a tracing GC. It is explicit ownership accounting
with cascade cleanup.

## Retention Root Classes

There are three important root classes today.

## 1. Session Ownership

When a session obtains cache-backed results, the session gets ownership edges to
those results.

Those edges live for the duration of the session.

When the session ends:

- `ReleaseSession` drops those session ownership edges
- any results that are no longer otherwise retained become collectible

This is the most ordinary retention class: "the client is using this result, so
keep it alive."

## 2. Persisted Edges

When a field is marked `IsPersistable`, completed results of that field get a
persisted edge.

That edge does not disappear at session end.

Instead, it remains until later prune work explicitly removes it.

This is how results survive beyond the session that created them and become
eligible for shutdown persistence and later restart reuse.

Persisted edges can also carry expiration metadata and can be marked
unpruneable.

## 3. Unpruneable Engine-Lifetime Retention

There are some special cases where the engine intentionally keeps results for
its own lifetime.

The main current example is core typedef retention.

`core/schema/coremod.go` builds the static core typedef graph and then calls
`cache.MakeResultUnpruneable(...)` on each typedef result. That effectively
installs persisted edges that are never eligible for prune.

This is not a separate retention mechanism in the cache internals. It is the
same persisted-edge machinery with the `unpruneable` bit set.

## Exact Dependency Edges

Dependency edges are how retention propagates transitively.

If result A depends on result B, then A holds an ownership edge to B.

That means:

- if A is retained by a session, persisted edge, or unpruneable edge
- then B stays alive too

This is why a persistable result's transitive dependency closure is retained
even though only the top-level result was directly marked persistable.

The dependency edges that matter here are the exact ones in `sharedResult.deps`,
not symbolic graph relationships.

## Where Dependency Edges Come From

The cache adds exact dependency edges from a few important sources:

- explicit `AddExplicitDependency` calls
- dependency attachment during publication
- exact `ResultCallRef` dependencies extracted from the authoritative
  `ResultCall`
- import-time reconstruction from persisted `result_deps`

The important thing is not how they were discovered. The important thing is that
once they exist, they participate in real retention and prune simulation.

## Session Release Is The First Pruning Pass

A big part of the retention story is session teardown.

On session removal, the engine:

- stops services
- drains in-flight dagql work for the session
- then calls `engineCache.ReleaseSession`

That drops the session root set and immediately runs the same ownership cascade
logic the cache uses everywhere else.

So even before explicit disk pruning policies run, ordinary session release is
already constantly pruning the cache back to the non-session-retained graph.

Session completion also clears the metadata monitor's blocked state and
schedules the shared local-cache GC path after one second. Repeated
session-triggered runs are throttled to once per minute. This later pass handles
persisted roots that remain after the immediate ownership cascade.

## Persistable Results

User-visible persistable behavior is driven by `Field.IsPersistable()`.

At execution time this becomes `CallRequest.IsPersistable`.

When a persistable result completes dependency attachment successfully, the
final publication handoff calls `upsertPersistedEdgeLocked` before dropping its
temporary ownership.

For a persistable cache hit, the upsert instead happens after its attachment
barrier and persisted-payload load both succeed.

That:

- creates or updates a persisted edge
- increments ownership if the edge is new
- tracks expiry / unpruneable state

This is why persistable results stay alive after session close.

## Unpruneable Results

`MakeResultUnpruneable` is a special case of persisted retention.

It installs a persisted edge with:

- `unpruneable = true`
- expiry cleared

Prune candidate selection skips those results entirely.

This is what the core typedef retention path uses today.

## TTL And Expiry

Persisted edges may have an `expiresAtUnix`.

That expiration does **not** by itself immediately delete the result.
Instead, it affects candidate ordering and eligibility during prune.

Expired persisted edges are preferred prune candidates.

## What Prune Actually Cuts

The prune operation does **not** directly remove arbitrary results.

The thing it cuts is the **persisted edge**.

That is an important design point.

Why?

Because persisted edges are the durable roots for cache retention beyond live
sessions. If prune wants to stop keeping something, it removes that root edge.
The normal ownership cascade then collects anything that is no longer reachable.

So the prune algorithm is really:

- choose persisted roots to cut
- cut them
- let exact dependency/liveness rules do the rest

## Disk Policies

The current prune policy type is `dagql.CachePrunePolicy`.

It includes:

- `All`
- `Filters`
- `KeepDuration`
- `ReservedSpace`
- `MaxUsedSpace`
- `MinFreeSpace`
- `TargetSpace`
- `CurrentFreeSpace`

This policy shape is still buildkit-influenced.

That is intentional for now:

- it was already a workable policy shape
- it avoided extra redesign work during the cutover
- it preserved compatibility with existing engine GC configuration expectations

So the current pruning system is Dagger-owned in implementation, but still uses
policy concepts inspired by BuildKit.

## Where Disk Policies Come From

The engine server builds dagql prune policies in `engine/server/gc.go`.

That layer:

- resolves configured/default engine GC policy
- translates/overlays CLI or API prune options
- sets `CurrentFreeSpace` from actual disk stats
- calls `engineCache.Prune`

So `dagql` owns the prune implementation, while `engine/server` owns policy
construction and triggering.

The structural-memory pass does not create another policy language. It always
uses `KeepDuration=0`, no filters, and the current deterministic candidate
order. Its only controls are an absolute maximum estimate and a lower target
estimate.

## Local-Cache GC Scheduling

When GC is enabled and the engine cache exists, the server's five-second local
cache pressure monitor checks two independent signals:

- disk pressure from configured worker policies, when such policies exist and
  disk stats are available
- the O(1) DAGQL structural estimate, even when there are no worker disk
  policies or disk stats fail

Either signal enters the existing 30-second-throttled shared GC path. Inside
that path, disk-stat failure skips only disk-policy pruning; it does not suppress
structural pruning. Startup, session completion, and graceful shutdown also use
the shared path.

The server has one minimal monitor-only blocked boolean. If a monitor-triggered
structural pass remains over maximum and removes no persisted roots, later
monitor ticks suppress only the structural trigger until a session/lifecycle
entry or another GC call updates the state. The bit never suppresses disk
pressure or disk policies. Session completion and other explicit lifecycle
entries bypass or clear it, and any successful removal or return below maximum
clears it.

`gc.enabled=false` disables automatic disk and structural pruning. Legacy
BuildKit GC disablement has the same effect on automatic scheduling. An explicit
manual structural request remains allowed.

## Manual Pruning Modes

`Engine.localCache.prune` selects disk and structural stages independently while
holding the existing server `gcmu` for the complete request:

- no options preserves the legacy all-eligible disk prune
- disk options run disk pruning
- structural `maxEstimatedBytes` or `targetEstimatedBytes` options run
  structural pruning without an implicit disk prune
- combined disk and structural controls run disk first and structural second,
  matching automatic GC ordering
- `useDefaultPolicy=true` runs both configured stages when automatic GC is
  enabled

When automatic GC is disabled, `useDefaultPolicy` does not enable the disabled
structural policy. Explicit structural options still run because they are a
direct operator action rather than automatic scheduling. No default disk
policies are active in this state, so the existing manual disk path falls back
to its all-releasable policy when `useDefaultPolicy=true`.

Structural-only means that the disk-policy stage is skipped, not that the
request cannot reclaim disk. Cutting persisted roots can release owned cache
entries, and a successful structural removal invokes snapshot GC. Disk-policy
retention rules and filters do not apply to the structural pass.

Manual structural options are optional absolute byte integers. Each omitted
member of the pair inherits the server's already-resolved configured/default
value. An explicitly supplied value must be positive, and the resulting target
must be lower than the resulting maximum. The server validates the complete
request before either stage mutates cache state.

## High-Level Disk Prune Algorithm

At a high level, the prune implementation in `dagql/cache_prune.go` does this:

1. snapshot current active session roots
2. measure result sizes
3. take a quick snapshot of the retained graph under lock
4. release the lock
5. compute active closure from session roots
6. collect prune candidates from persisted edges
7. sort them heuristically
8. run a greedy simulation of cutting candidates
9. reacquire the live lock only when actually cutting persisted edges
10. compact eq-classes if needed
11. trigger snapshot metadata GC if something was actually reclaimed

This is absolutely a best-effort pruning pass, not an optimal solver.

## Structural Estimate And Memory Pruning

The cache exposes an O(1) estimate from cardinalities already protected by
`egraphMu`:

```text
estimated bytes = 3072*R + 512*T + 768*C
```

Where:

- `R` is `len(resultsByID)`, the number of live `sharedResult`s
- `T` is `len(egraphTerms)`, the number of live symbolic operation terms
- `C` is `len(egraphParents)-1`, the allocated union-find class-slot
  high-water

The weights are a calibrated bundle for ordinary result-owned calls, payloads,
dependencies, indexes, maps, terms, and classes. They are not claims about the
isolated size of any one Go object. The estimate intentionally ignores exact
map capacity, dependency fan-out, call-string length, payload size, imported
envelope size, and allocator fragmentation. It bounds the reproduced
population-growth problem; it is not a process RSS measurement.

The class-slot coefficient is 768 rather than the initial 1,024 hypothesis
because 1,024 produced a 2.199 churn-compaction estimate-to-heap ratio, outside
the calibration gate's `[0.5, 2]` upper bound. Recalibration at 768 produced a
1.649 ratio while the common one-million-result shape still estimates
4,352,000,000 bytes and crosses the 4 GiB trigger.

The built-in maximum of 4 GiB and target of 3 GiB are Erik-approved provisional
implementation defaults. They remain subject to final tuning and canary
evidence before shipping; that tuning does not block the implementation.
Configuration is under
`gc.dagqlCache.maxEstimatedBytes` and
`gc.dagqlCache.targetEstimatedBytes`. Both are absolute `int64` byte values;
zero resolves to the built-in default, and the resolved target must be positive
and lower than the maximum.

When the estimate exceeds the maximum, `PruneMetadataEstimate`:

1. force-compacts equivalence classes under `egraphMu`
2. returns without cuts if compaction alone restores the maximum
3. snapshots active session roots and the retained graph in structural mode
4. computes active closure and candidates using `KeepDuration=0`, no filters,
   and the current deterministic order
5. gives each simulated collected result the same coarse structural credit
6. reuses the existing greedy ownership simulation until the target is reached
   or candidates are exhausted
7. applies persisted-edge cuts through the shared live collector
8. force-compacts classes again after a non-empty plan and reports the final
   structural estimate

Structural snapshot mode skips physical size measurement, usage-identity
details, call labels, cloned call frames, digest derivation, and per-root report
entries. Structural output is one aggregate INFO record, emitted only after the
maximum is actually exceeded, plus aggregate trace events when e-graph tracing
is enabled. A manual request retains the existing detailed response for its disk
stage; its structural stage remains aggregate-only.

## Stop-The-World Avoidance

An important design goal is: prune should not become a stop-the-world GC.

The implementation addresses that in two ways:

### 1. Snapshot first, simulate later

The cache briefly takes a snapshot of the information it needs:

- current retained results
- incoming counts
- exact deps
- persisted-edge metadata
- measured sizes
- active session roots

Then it releases the lock and does the expensive reasoning outside the lock.
Structural planning carries the request context through active-root snapshotting,
graph snapshotting, active-closure traversal, candidate ordering, and ownership
simulation. It checks cancellation between phases and at bounded intervals
inside those O(N) loops, and discards partial planning state without cutting
persisted roots when canceled.

### 2. Apply actual cuts later

Only once the plan is chosen does the cache reacquire the live lock and attempt
to remove persisted edges from the real cache.

That means the slow part is simulation, not holding the live graph lock.

## The Snapshot Used For Prune

The prune snapshot is a simplified view of the live cache:

- one `pruneSnapshotResult` per live result
- incoming ownership count
- exact deps
- whether a persisted edge exists
- whether it is unpruneable
- persisted expiry

Disk mode additionally includes usage identities, physical size/detail fields,
and `pruneUsageIdentityState` for shared-storage identities. Structural mode
omits those physical fields and stores only the coarse per-result credit needed
by the shared planner.

This snapshot is enough to simulate edge cuts without touching live cache state.

## Active Closure

Before choosing prune candidates, the cache computes the active closure from
session roots.

This means:

- start from every result actively held by some session
- walk exact dependency edges
- mark the whole reachable set as active

Anything in that active closure is not a prune candidate, even if it has a
persisted edge.

This is an important subtlety:

- a result can be persistable
- and also currently active through a session
- prune will not cut it while it is still in that active closure

## Candidate Collection

Only results with persisted edges are considered.

Candidate collection skips results if:

- they have no persisted edge
- the persisted edge is unpruneable
- they are in the active closure
- they are recently used and not expired, according to `KeepDuration` in disk
  mode
- they do not match policy filters in disk mode

So pruning is not scanning "all results." It is scanning the persisted-root set
and applying a few simple eligibility rules.

Structural mode supplies no filters and `KeepDuration=0`, so active and
unpruneable protection are its only eligibility exclusions. Both modes recheck
the live persisted edge's unpruneable bit while holding `egraphMu` immediately
before a cut. A concurrent `MakeResultUnpruneable` upgrade therefore wins even
after snapshot planning.

## Candidate Ordering

The current candidate ordering is heuristic and intentionally simple.

Candidates are sorted roughly by:

1. expired before non-expired
2. least recently used first
3. oldest creation time first
4. larger reported size first
5. stable ID tie-break

This is not sophisticated. It is a basic heuristic.

All structural candidates have equal coarse direct credit, so step 4 does not
reorder them in structural mode. When the time fields also tie, the stable
result ID is the final deterministic refinement.

There is a lot of room to improve this later.

## Greedy Simulation

The current reclaim planner is greedy.

It does **not** try to solve a globally optimal selection problem.

Given the current candidate order, it simulates cutting persisted edges one by
one until the target reclaim threshold is reached.

That is intentionally cheap and simple compared to trying to solve a more
optimal subset selection problem.

This is very much a "good enough for now" pruning strategy.

## What The Simulation Actually Simulates

The simulation state tracks:

- remaining incoming ownership count per result
- which results have already been collected in the simulation

Disk mode also tracks alive member count and measured size per physical usage
identity. Structural mode instead credits the same coarse byte value for each
result the simulation actually collects.

Applying a candidate means:

1. decrement that result's incoming count by one, representing cutting the
   persisted edge
2. if that reaches zero, enqueue the result for collection
3. when a result is collected:
   - mark it collected
   - in disk mode, decrement alive counts for its usage identities and reclaim
     bytes only when an identity's alive count reaches zero
   - in structural mode, add one coarse per-collected-result credit
   - decrement incoming counts of its exact deps
   - recursively collect newly unowned deps

This is why the simulation is "edge cut" based rather than "delete this result"
based.

## Shared Snapshot / Shared Storage Accounting

In disk mode, multiple results can represent the same underlying physical
storage.

This is handled through cache-usage identities.

The relevant interfaces are:

- `hasCacheUsageIdentity`
- `cacheUsageSizer`
- `cacheUsageMayChange`

The basic idea is:

- a result can expose one or more stable usage identities
- identical usage identities mean "this is the same physical storage for pruning
  size purposes"
- the cache chooses one owner result for each identity, currently the lowest
  `sharedResultID`
- only that owner result publishes the measured size
- reclaim bytes are only counted when the last alive member for an identity is
  collected

This is how pruning avoids double-counting shared snapshots or other shared
storage.

## Size Measurement

Disk prune needs approximate physical reclaim sizes, so it measures usage before
planning.

The flow is:

1. collect measurement inputs under read lock
2. release the lock
3. measure by usage identity outside the lock
4. publish the measurements back under lock

Important details:

- only materialized results with typed `self` values participate
- non-changing identities reuse existing measured size when possible
- changing identities (like mutable cache volume snapshots) are remeasured

This measurement phase is separate from candidate simulation, but the simulation
depends on its output.

Structural pruning never calls this physical measurement path. Its trigger,
target, and planning credit come only from `R`, `T`, and `C`.

## Policy Targets

`pruneTargetBytes` computes the reclaim target from policy thresholds.

The current logic is still policy-shaped rather than deeply semantic:

- `MaxUsedSpace`
- `ReservedSpace`
- `MinFreeSpace`
- `TargetSpace`

If thresholds are not triggered but the policy is effectively "prune matching
things anyway" (`All` or filters), the target becomes effectively unlimited.

That is how explicit disk-prune requests can still remove matching entries even
without disk pressure.

## Applying The Plan To Live State

Once the plan is built, the cache applies it against live state by calling
`removePersistedEdge` for each planned candidate.

This is where real-time drift matters.

Between snapshot time and apply time:

- some edges may already be gone
- some results may no longer be collectible
- ownership may have changed

The implementation accepts that.

If `removePersistedEdge` says the edge is already gone, prune just skips it.
This is fine. Pruning is best effort.

The same live path rechecks `unpruneable` under `egraphMu` in both disk and
structural modes before deleting the persisted edge.

The live apply path relies on the same ownership cascade used everywhere else:

- delete persisted edge
- decrement incoming ownership
- collect newly unowned results
- run `OnRelease`

## Containerd Leases And Actual Snapshot Cleanup

At a high level, dagql retention and pruning are expressed through snapshot
owner leases.

When a retained result owns snapshots, the cache ensures the snapshot manager
attaches a lease for that result's owner slots.

When a result is finally collected, its `OnRelease` cleanup removes those owner
leases.

Actual physical snapshot reclamation is then largely delegated to containerd:

- dagql removes the logical owner lease
- containerd metadata / GC handles actual resource cleanup

The prune path itself triggers snapshot metadata GC after it has actually
removed persisted roots. Structural reports deliberately have no per-root
entries, so this decision uses the aggregate removed-root count. The low-level
cleanup semantics are intentionally delegated to containerd rather than
reimplemented in dagql.

That is enough to understand the current prune story at a high level. The
lease/snapshot side can be documented in finer detail separately.

## Eq-Class Compaction After Prune

Pruning can leave the union-find class ID space sparse. Disk prune keeps the
existing heuristic compaction after removals; it declines when allocated slots
are less than twice the live roots.

Structural pruning charges the allocated class-slot high-water, so it uses
forced compaction before planning and after a non-empty cut plan. Forced mode
rebuilds whenever allocated slots exceed the compacted live slot count, even
when the normal two-times guard would decline. A pre-plan compaction can resolve
pressure without evicting a root.

This:

- rebuilds the live eq-class ID space
- rewrites term input/output eq-class IDs
- rebuilds eq-class/digest mappings
- rebuilds output-eq-class membership
- recomputes term digests

This keeps the e-graph structure tidy after repeated merge-and-prune cycles and
makes cuts lower the structural class term. It does not broadly rebuild all
top-level cache maps.

## Usage And Metrics Reporting

The physical size-accounting machinery also feeds usage reporting.

`UsageEntriesAll`:

- snapshots current session roots
- measures result sizes
- builds sorted `CacheUsageEntry` values

The engine exposes that through `EngineLocalCacheEntries`.

So the disk-prune-size view and the user-visible cache-entry view come from the
same accounting path.

Structural pruning has one separate Prometheus gauge:
`dagger_dagql_cache_metadata_estimated_bytes`. It reads the same O(1) estimate
used by the trigger. There are no per-type, per-field, or per-root structural
metrics.

A structural pass returns and logs only aggregate counts, estimates, compaction
slot counts, outcome, and duration. A below-threshold call emits no INFO
start/finish record. Manual pruning remains compatible with its existing
detailed response for disk-stage removals.

## Persistence And Restart

Structural pruning uses graph state already stored by persistence schema 17.
It does not change `cachePersistenceSchemaVersion` or add persisted fields.

A schema-17 cache imports normally. The startup GC scheduled after one second
can then trim cold roots if the imported estimate exceeds the maximum. The
oversized graph is necessarily loaded before that pass, so an upgrade can have
a temporary import-memory peak. This is a known limitation, not a schema hard
cut or permission to wipe worker state.

## Special Case: Core Typedef Retention

The static core schema typedef graph is intentionally retained for the life of
the engine.

`core/schema/coremod.go` does this by calling `MakeResultUnpruneable` on the
typedef results when building the core schema view state.

That means:

- these typedef results are retained even after sessions end
- prune skips them entirely

This is one of the clearest examples of "engine-owned lifetime" rather than
session-owned or merely persistable lifetime.

## Calibration And Scale Evidence

The committed benchmarks in `dagql/cache_metadata_prune_benchmark_test.go` were
run in isolated processes on 2026-08-05 with Go 1.26.5, Linux amd64, and 16
logical CPUs. `HeapAlloc` endpoints use `runtime.GC` plus
`debug.FreeOSMemory`; peak `HeapInuse` is sampled every 5 ms. RSS comes from
`/usr/bin/time -v` and therefore includes fixture setup as well as the timed
operation.

At 200,000 results, all named metadata-dominated fixtures put both population
and prune estimate-to-post-GC-heap ratios inside the required `[0.5, 2]` band:

| Fixture | Population ratio | Prune ratio | Pass time | Sampled scratch | Automatic log |
| --- | ---: | ---: | ---: | ---: | ---: |
| Minimal persisted scalar | 1.437 | 1.468 | 3.333 s | 161,308,672 B | 878 B |
| No-match `Directory.glob` shape | 1.213 | 1.235 | 4.321 s | 159,817,728 B | 878 B |
| Rich eight-argument call | 0.811 | 0.821 | 3.333 s | 160,022,528 B | 878 B |
| Shared dependency | 1.232 | 1.255 | 3.507 s | 163,840,000 B | 878 B |

The 200,000-result churn benchmark retained a 2,000-result unpruneable floor.
Its baseline-to-peak, peak-to-session-release, and pre-to-post-compaction ratios
were 1.463, 1.561, and 1.649. The transitions were well above the 0-byte no-op
control delta. Session release took 1.395 s. Forced compaction took 11.335 ms,
reduced class slots from 202,000 to 2,000, and reduced post-GC `HeapAlloc` from
143,694,784 B to 50,541,296 B. The final floor estimate was 8,704,000 B.

A separate sparse forced-compaction measurement allocated exactly 1,000,000
class slots, released all but the same 2,000-result unpruneable floor, and then
timed the `egraphMu` write-lock hold. Compaction reduced the slots from
1,000,000 to 2,000 in 31.709 ms; the fresh benchmark process reached 4,341,664
KiB maximum RSS.

At one million results, each fixture had `R=T=C=1,000,000` before pruning and a
4,352,000,000-byte estimate. Every fixture ended with zero results, terms, and
class slots. The no-match and shared fixtures cut 999,999 persisted roots plus
their common dependency; the other fixtures cut one million roots.

| Fixture | Pass | Ratios (population/prune) | Setup `HeapInuse` | Sampled scratch | Post-prune retained `HeapAlloc` delta | B/op; allocs/op | Max RSS | Aggregate log |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Minimal scalar | 14.276 s | 1.330 / 1.373 | 3,904,937,984 B | 874,266,624 B | 100,561,664 B | 946,794,944; 4,024,650 | 4,792,036 KiB | 895 B |
| No-match glob | 27.318 s | 1.132 / 1.162 | 4,705,189,888 B | 863,895,552 B | 100,569,480 B | 954,501,544; 5,024,617 | 5,583,412 KiB | 892 B |
| Rich call | 22.666 s | 0.776 / 0.790 | 6,930,653,184 B | 888,102,912 B | 100,284,368 B | 946,499,064; 4,024,612 | 7,846,152 KiB | 894 B |
| Shared dependency | 25.996 s | 1.149 / 1.180 | 4,741,349,376 B | 854,089,728 B | 100,719,232 B | 954,723,512; 5,024,641 | 5,612,572 KiB | 892 B |

The one-million-result estimate read took 6.524 ns/op with zero allocations.
Structural snapshot construction held the graph read lock for 1.004 s and
allocated 277,832,800 B in 1,004,099 allocations. The matching existing disk
snapshot baseline took 2.025 s and allocated 357,825,304 B in 4,003,851
allocations. Structural mode exceeds the 500 ms review threshold but remains
well below the current disk-mode baseline. All full passes completed inside the
30-second pressure throttle, and all sampled scratch peaks fit inside the
1,073,741,824-byte maximum-to-target gap. The no-match fixture's 27.318-second
pass is close to that gate and should be watched when these structures change.

The unchanged schema-17 import benchmark preserved all `R`, `T`, and `C` counts.
At 200,000 results it imported an 85,123,128-byte database in 3.543 s, with
826,048,512 B sampled peak additional `HeapInuse`. At one million results it
imported a 426,000,440-byte database in 25.982 s, with 4,432,961,536 B sampled
peak additional `HeapInuse` and 7,343,756 KiB maximum process RSS. This confirms
the known temporary import peak before startup pruning; it is not included in
the pruning scratch-gap gate.

## Limitations Of The Current Algorithm

The current algorithm is intentionally basic.

Important limitations:

- candidate ordering is crude
- the planner is greedy, not optimal
- it does not reason about richer value/cost tradeoffs
- disk mode relies on approximate/current physical size measurements
- structural mode does not model rare large calls, payloads, imported
  envelopes, map capacity, or allocator fragmentation
- the full O(N) snapshot and simulation allocate substantial temporary memory
- it accepts drift between snapshot time and apply time

This is not trying to be the final word in pruning quality.

It is a straightforward best-effort heuristic that works with the current cache
ownership model.

## Short Summary

The current dagql prune model treats persisted edges as prunable retention roots
and protects live session closure and unpruneable roots. Disk mode uses measured
physical size and worker policies. Structural mode triggers automatically or
manually from an O(1) `R/T/C` estimate, force-compacts class slots, and uses
equal coarse credit without physical details. Both modes reuse the same graph
snapshot, greedy ownership simulation, live unpruneable recheck, persisted-edge
cuts, normal ownership cascade, and containerd lease cleanup.
