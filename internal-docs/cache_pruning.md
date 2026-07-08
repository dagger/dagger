# DagQL Cache Pruning And Retention

This document describes the current pruning and retention model for the `dagql`
cache.

The source of truth is the code, mainly:

- `dagql/cache.go`
- `dagql/cache_prune.go`
- `engine/server/gc.go`
- `engine/server/server.go`
- `core/schema/coremod.go`
- `engine/snapshots/persistent_metadata.go`

This doc is about:

- what keeps results alive
- what makes a result prunable
- how the prune algorithm works today
- how size accounting works
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

## Persistable Results

User-visible persistable behavior is driven by `Field.IsPersistable()`.

At execution time this becomes `CallRequest.IsPersistable`.

When a persistable result is completed, `initCompletedResult` calls
`upsertPersistedEdgeLocked`.

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

## Policies

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

## Where Policies Come From

The engine server builds dagql prune policies in `engine/server/gc.go`.

That layer:

- resolves configured/default engine GC policy
- translates/overlays CLI or API prune options
- sets `CurrentFreeSpace` from actual disk stats
- calls `engineCache.Prune`

So `dagql` owns the prune implementation, while `engine/server` owns policy
construction and triggering.

## High-Level Prune Algorithm

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

### 2. Apply actual cuts later

Only once the plan is chosen does the cache reacquire the live lock and attempt
to remove persisted edges from the real cache.

That means the slow part is simulation, not holding the live graph lock.

## The Snapshot Used For Prune

The prune snapshot is a simplified view of the live cache:

- one `pruneSnapshotResult` per live result
- incoming ownership count
- exact deps
- usage identities
- cache usage entry metadata
- whether a persisted edge exists
- whether it is unpruneable
- persisted expiry

There is also `pruneUsageIdentityState` tracking shared-storage identities.

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
- they are recently used and not expired, according to `KeepDuration`
- they do not match policy filters

So pruning is not scanning "all results." It is scanning the persisted-root set
and applying a few simple eligibility rules.

## Candidate Ordering

The current candidate ordering is heuristic and intentionally simple.

Candidates are sorted roughly by:

1. expired before non-expired
2. least recently used first
3. oldest creation time first
4. larger reported size first
5. stable ID tie-break

This is not sophisticated. It is a basic heuristic.

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
- alive member count per usage identity
- size per usage identity
- which results have already been collected in the simulation

Applying a candidate means:

1. decrement that result's incoming count by one, representing cutting the
   persisted edge
2. if that reaches zero, enqueue the result for collection
3. when a result is collected:
   - mark it collected
   - decrement alive counts for its usage identities
   - only reclaim bytes when an identity's alive count reaches zero
   - decrement incoming counts of its exact deps
   - recursively collect newly unowned deps

This is why the simulation is "edge cut" based rather than "delete this result"
based.

## Shared Snapshot / Shared Storage Accounting

Multiple results can represent the same underlying physical storage.

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

Prune needs approximate reclaim sizes, so it measures usage before planning.

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

## Policy Targets

`pruneTargetBytes` computes the reclaim target from policy thresholds.

The current logic is still policy-shaped rather than deeply semantic:

- `MaxUsedSpace`
- `ReservedSpace`
- `MinFreeSpace`
- `TargetSpace`

If thresholds are not triggered but the policy is effectively "prune matching
things anyway" (`All` or filters), the target becomes effectively unlimited.

That is how explicit user prune requests can still remove matching entries even
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
removed entries, but the low-level cleanup semantics are intentionally delegated
to containerd rather than reimplemented in dagql.

That is enough to understand the current prune story at a high level. The
lease/snapshot side can be documented in finer detail separately.

## Eq-Class Compaction After Prune

Pruning can leave the union-find class ID space sparse.

So after prune removes anything, the cache may compact eq-classes.

This:

- rebuilds the live eq-class ID space
- rewrites term input/output eq-class IDs
- rebuilds eq-class/digest mappings
- rebuilds output-eq-class membership
- recomputes term digests

This is maintenance work to keep the e-graph structure tidy after repeated
merge-and-prune cycles.

## Usage Reporting

The same size-accounting machinery also feeds usage reporting.

`UsageEntriesAll`:

- snapshots current session roots
- measures result sizes
- builds sorted `CacheUsageEntry` values

The engine exposes that through `EngineLocalCacheEntries`.

So the prune-size view and the user-visible cache-entry view come from the same
accounting path.

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

## Limitations Of The Current Algorithm

The current algorithm is intentionally basic.

Important limitations:

- candidate ordering is crude
- the planner is greedy, not optimal
- it does not reason about richer value/cost tradeoffs
- it relies on approximate/current size measurements
- it accepts drift between snapshot time and apply time

This is not trying to be the final word in pruning quality.

It is a straightforward best-effort heuristic that works with the current cache
ownership model.

## Short Summary

The current dagql prune model treats persisted edges as prunable retention roots,
protects live session closure from prune, takes a quick snapshot of the retained
graph, runs a simple greedy edge-cut simulation outside the lock, then cuts real
persisted edges and lets normal ownership cascade and containerd lease cleanup
do the rest.
