# WHITEBOARD

## Agreement

## TODO
* Assess changeset merge decision to always use git path (removed `conflicts.IsEmpty()` no-git fast path), with specific focus on performance impact
   * Compare runtime/cost of old no-git path vs current always-git path in no-conflict workloads
   * Confirm whether correctness/cohesion benefits outweigh any measured regression and document outcome
* Remove internal `__immutableRef` schema API once and for all
   * Replace remaining stable-ID use cases with a cleaner non-internal API pattern in dagql/core
* Review the new HTTP implementation for clarity/cohesion
   * Current implementation is functional but confusing; do a low-priority cleanup pass
* Fix `query.__schemaJSONFile` implementation to avoid embedding megabytes of file contents in query args
   * Build/write via ref/snapshot path directly instead of passing huge inline string payloads through select args
* Clean up `cloneContainerForTerminal` usage
   * Find a cleaner container-child pattern for terminal/service callsites instead of special clone helper
* replacing CurrentOpOpts CauseCtx with trace.SpanContextFromContext seems sus, needs checking
* Reassess file mutator parent-passing + lazy-init shape (`WithName`/`WithTimestamps`/`Chown`/`WithReplaced`)
   * Current implementation passes parent object results through schema into core and appears correct in tests, but may not be the most cohesive long-term model.
   * Follow-up: revisit whether lazy-init/parent snapshot modeling can eliminate this explicit parent threading while preserving correctness for service-backed files.
* Assess whether we dropped any git lazyness (especially tree) and whether we should restore it
* Assess whether we really want persistent cache for every schema json file, that's probably a lot of files that are actually kinda sizable!
* Find a way to enable pruning of filesync mirror snapshots
   * Pretty sure filesync mirrors are currently not accounted for by dagql prune/usage accounting.

## Notes
* **THE DAGQL CACHE IS A SINGLETON CREATED ONCE AT ENGINE START AND IT LIVES FOR THE ENTIRE LIFETIME OF THE ENGINE.**
  * There is not a second DAGQL cache.
  * There is not a per-session DAGQL cache.
  * Result-call planning/runtime code should not be written as if cache identity were ambiguous.
  * If a code path needs the DAGQL cache, it should explicitly use or fetch the singleton cache rather than storing mutable cache backpointers on frame/helper structs.
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future
* Moved the GC + ref counting + dependency tracking refactor notes to `scratch/GC-refactor.md` so this file can stay focused on the next design/debugging pass.

* Moved the Laziness Refactor notes to `scratch/laziness.md` so this file can stay focused on prune.

# Prune Refactor

## Design

### Overall model

The prune refactor should treat pruning as a best-effort planning problem over a
snapshot of cache state rather than an in-place optimizer over the live e-graph.

The intended high-level flow is:

1. Snapshot the minimum relevant state needed for prune while holding locks for
   as little time as possible.
2. Outside of lock, decide what to prune by simulating over that snapshot.
3. Reacquire the lock and apply the selected persisted-edge removals best-effort.
   If the live graph has drifted, that is acceptable.

Prune is already heuristic and nondeterministic.

That means we explicitly accept:

* if snapshot state drifts before apply, that is fine
* if some planned removals are no longer possible, skip them and get them next prune
* if new cache state appears during prune, that is fine
* if we over-prune somewhat relative to the exact byte target, that is fine

The design goal is not exact optimality. The design goal is:

* no expensive work while holding the e-graph lock
* no giant global rescoring loops against live state
* one coherent planning pass over a snapshot
* one best-effort apply phase back to live state

### Existing problems to remove

The current prune implementation has several structural problems.

* `Prune` holds the full e-graph lock and then calls `measureAllResultSizesLocked`.
  That means we can run arbitrary `CacheUsageIdentity`, `CacheUsageMayChange`,
  and worst of all `CacheUsageSize` object code while the cache is globally locked.
  This is exactly the sort of lock-hostile design we want to eliminate.
* `simulatePersistedEdgeRemovalLocked` is an expensive per-candidate graph
  simulation, and the current algorithm runs it for every candidate in a giant
  best-candidate scoring loop.
* The current algorithm rebuilds almost all of its global state after every
  single selected removal:
  * size accounting
  * usage-identity ownership maps
  * active/session closure
  * candidate list
  * candidate sort order
  * marginal reclaim scores
* `usageEntriesLocked` calls `resultHasSessionEdge` for every result, and that
  helper grabs `sessionMu` and scans all sessions. That is both a performance
  problem and the wrong kind of cross-lock interaction inside prune.
* `sessionDependencyClosureLocked` grabs `sessionMu` while prune is already
  holding `egraphMu`. Other codepaths take those locks in the opposite order,
  which is both a contention problem and a lock-order smell.
* The current greedy selection is too expensive even if it were moved entirely
  outside of locks. The problem is not just lock placement; the algorithmic
  shape itself is wrong for prune.

### Snapshot contents

The prune planner should snapshot only the state needed to make pruning
decisions.

The intended snapshot contents are:

* the set of persisted retained-root edges that are eligible candidates
* enough per-result graph state to simulate ownership release:
  * result ID
  * current incoming ownership count
  * dependency edges
* enough usage-accounting state to estimate reclaimed bytes without rewalking
  all identities after every candidate:
  * usage identity per result
  * size bytes per usage identity
  * live-member count per usage identity
* enough policy metadata per candidate to sort deterministically:
  * expiry status / expiry time
  * most-recent-use time
  * created-at time
  * current attributed size
* the active/session-retained roots or active closure needed to exclude
  in-use results from prune candidacy

This snapshot should be copied out of live state quickly. Expensive work such as
size measurement should either already be cached or should run outside the main
lock against snapshot-owned worklists.

### Candidate definition

A prune candidate is not a leaf, and it is not any arbitrary graph vertex.

A prune candidate is:

* a result that currently has a persisted retained-root edge
* that passes the prune policy filters
* that is not blocked by active/session retention

Everything else in the graph still matters to the simulation, but only as
simulation state.

So:

* interior nodes with a persisted edge are valid candidates
* top-level results with a persisted edge are valid candidates
* a node with no persisted edge is never a direct prune action, even if it would
  be collectible after other removals

This keeps the action space honest: prune only plans removals it can actually
apply.

### Stage-2 planning algorithm

The preferred planning algorithm is a one-pass ordered simulation over the
snapshot.

The intended flow is:

1. Build the candidate list from persisted retained-root edges.
2. Sort candidates once by a simple heuristic priority.
3. Walk the candidates in that fixed order.
4. For each candidate, apply its persisted-edge removal to the snapshot counts.
5. If any result count hits zero, propagate collection through its deps.
6. As results become collected, decrement live counts for their usage
   identities and add bytes once an identity becomes fully dead.
7. Stop once estimated reclaimed bytes reaches the prune target.
8. Apply the selected candidate removals back to the live cache best-effort.

This is intentionally not exact greedy.

The point is:

* graph-aware
* overlap-aware
* target-aware
* much cheaper than rescoring every candidate on every round

### Heuristic order

The heuristic should stay simple and explainable. The graph-awareness comes from
the simulation, not from a fancy candidate score.

A good first-pass order is:

1. expired persisted edges first
2. older least-recently-used first
3. older created-at first
4. larger currently-attributed size first
5. lower result ID as deterministic tie-break

This means:

* obviously cold and expired data gets considered first
* among equally cold candidates, larger roots are considered earlier so we can
  reach the target with fewer removals
* the order is deterministic and easy to reason about

We should not attempt to make the heuristic itself encode exact transitive
reclaim. That is what the simulation is for.

### Why this is graph-aware

The planner tracks incoming ownership counts for all results in the snapshot.

When a candidate retained root is removed in the simulation:

* decrement that result's incoming count
* if it reaches zero, collect it
* for each collected result, decrement its deps
* if those deps reach zero, collect them too
* continue transitively until the queue drains

So reclaim is based on the actual DAG of ownership dependencies, not on a root's
local size.

### Why this is overlap-aware

The planner carries the simulated state forward across the full ordered pass.

That means:

* if two persisted roots share a large dependency, removing the first root will
  not reclaim that dependency while the second still retains it
* when the last retaining root is simulated away, the shared dependency can then
  collect
* shared physical bytes are handled the same way via usage identities: bytes are
  only counted reclaimed once the last live result for that identity is gone

This avoids the current per-candidate full scan over all usage identities.

Instead, the planner should maintain:

* `remainingIncomingCount[resultID]`
* `aliveCountByUsageIdentity[identity]`
* `sizeBytesByUsageIdentity[identity]`

As each result is collected in the simulation:

* decrement its identity's alive count
* if that alive count reaches zero, add that identity's bytes exactly once

### Why this is target-aware

The simulation maintains a running reclaimed-byte total.

The ordered pass stops once:

* simulated reclaimed bytes reaches or exceeds the prune target

This is sufficient for prune.

We do not need exact optimality. We only need:

* a plan that reasonably tends toward old/expired data
* overlap-aware reclaim accounting
* a stopping point once enough bytes are expected to be reclaimed

### Zero-immediate-reclaim candidates

A candidate must not be discarded merely because its immediate reclaim delta is
zero.

This is important for shared-base-image-style cases where:

* many tiny retained roots all keep a huge shared dependency alive
* removing any one of them alone does not reclaim the huge shared dependency
* but removing enough of them eventually does

So in the ordered simulation:

* when a candidate is considered while we are still under target, its retained
  edge removal is applied to the snapshot state even if that single action
  reclaims zero bytes immediately
* that candidate still becomes part of the prune plan
* later candidates can then benefit from the state change it introduced

This is another reason the one-pass ordered simulation is a better fit than the
current exact-marginal greedy scoring loop.

### Relationship to the old greedy algorithm

The old algorithm tries to choose the single best next candidate by exact
marginal reclaim.

That should not be the target anymore.

Even outside locks, that shape is still:

* too expensive
* too recomputation-heavy
* too complicated for a best-effort subsystem

If we ever want to preserve a more greedy flavor later, a lazy-greedy/CELF-style
variant over a snapshot would be much better than the current full rescoring
loop, but that is not the preferred first cut.

The preferred first cut is:

* snapshot once
* sort once
* simulate once
* apply best-effort batch

### Apply phase

Applying the plan back to the live graph should be best-effort and simple.

The intended behavior is:

* reacquire the lock only around actual live mutations
* for each selected candidate, remove the persisted retained-root edge if it is
  still present
* if it is already gone, skip it
* if removing one candidate changes later live-state conditions, that is okay
* do not attempt to re-run full planning inside apply

We explicitly accept drift between snapshot and live state.

### Secondary follow-up opportunities

Once the structural refactor above is in place, there are some possible
follow-ups if needed:

* add a more nuanced sort key if the simple expiry/LRU/size order proves too crude
* introduce a prune-cycle cap so one invocation does not apply an unbounded plan
* incrementally maintain some of the snapshot inputs, such as cached size or
  active-session closure summaries, if that proves worthwhile

But the first pass should focus on the structural hard cut:

* no expensive work under the e-graph lock
* no repeated whole-world rescoring
* no session-lock work from inside the prune critical section
* one-pass ordered simulation over a snapshot

## Implementation plan

### `dagql/cache.go`

#### Snapshotting session-owned roots

Specific change:

* stop using `sessionDependencyClosureLocked()` from inside prune
* replace it with a small helper that snapshots session-owned result IDs under
  `sessionMu` alone, for example:
  * `snapshotSessionResultIDs() map[sharedResultID]struct{}`
* that helper should:
  * hold only `sessionMu`
  * copy the union of all `sessionResultIDsBySession`
  * return the copied root set
* closure expansion over deps must then happen outside of `sessionMu` and
  outside of `egraphMu`, using the prune snapshot graph

Result:

* no `sessionMu` acquisition from inside prune's e-graph critical section
* no lock-order inversion between session tracking and prune

#### Active-state accounting for usage entries

Specific change:

* stop calling `resultHasSessionEdge(resultID)` once per result from
  `usageEntriesLocked()`
* remove the current per-result `sessionMu` scan from `usageEntriesLocked`
* replace it with an explicit active-root snapshot input:
  * `usageEntriesLocked(activeRoots map[sharedResultID]struct{}) []CacheUsageEntry`
  * or equivalently a small internal helper that takes a precomputed active set
* `UsageEntriesAll(ctx)` should:
  * snapshot active roots under `sessionMu`
  * run size measurement through the new unlocked measurement pipeline
  * reacquire `egraphMu`
  * build usage entries using the precomputed active-root set

Result:

* no O(results * sessions) active-state scan during usage-entry generation
* no `sessionMu` traffic from inside `usageEntriesLocked`

#### Size measurement pipeline

Specific change:

* split the current locked `measureAllResultSizesLocked(ctx)` into three phases:

1. locked collect:
   * under `egraphMu`, gather a measurement worklist for results whose
     `usageIdentity` / `sizeEstimateBytes` are missing or may change
   * copy out only the minimum stable information needed to measure outside lock
     for each candidate identity group:
     * owner result ID
     * owner shared-result pointer identity to revalidate on publish
     * current payload object pointer / typed self
2. unlocked measure:
   * outside `egraphMu`, call:
     * `CacheUsageIdentity()`
     * `CacheUsageMayChange()`
     * `CacheUsageSize()`
   * group by usage identity outside lock
   * compute the owner result ID for each identity group outside lock
3. locked publish:
   * reacquire `egraphMu`
   * for each measured identity group, publish:
     * `usageIdentity`
     * `sizeEstimateBytes`
   * only publish if the result still exists and still matches the expected
     shared-result / payload identity from the collect phase

* keep a small locked helper for the collect/publish pieces, but the expensive
  object-method calls must move out of lock entirely
* `measureAllResultSizesLocked` should disappear from prune and usage-entry
  callers
* replace it with an outer orchestration method such as:
  * `measureAllResultSizes(ctx)`

Result:

* no arbitrary object `CacheUsage*` work under `egraphMu`
* `Prune` and `UsageEntriesAll` can both reuse the same safe measurement path

#### Usage-identity cache state

Specific change:

* keep `usageIdentity` and `sizeEstimateBytes` on `sharedResult`
* continue using the smallest `sharedResultID` as the deterministic owner of a
  usage identity for reporting purposes
* do not change the external `CacheUsageEntry` shape in this pass
* treat `CachePruneReport.ReclaimedBytes` as the sum of simulated reclaim deltas
  for the actually-applied selected candidates

Result:

* no API churn in the first pass
* report semantics stay simple enough for GC logging and tests

#### Locked helpers kept as pure mutation-only helpers

Specific change:

* keep low-level mutation helpers like:
  * `removePersistedEdge`
  * `incrementIncomingOwnershipLocked`
  * `decrementIncomingOwnershipLocked`
  * `collectUnownedResultsLocked`
* do not teach these helpers anything about prune planning
* the new prune planner should hand them a finished best-effort removal list and
  let them continue doing the actual live-graph mutation work

Result:

* planning and mutation stay clearly separated
* no new prune-specific magic in the core ownership mutation helpers

### `dagql/cache_prune.go`

#### Replace the current `Prune` loop shape

Specific change:

* delete the current nested loop that repeatedly:
  * locks `egraphMu`
  * measures all sizes
  * rebuilds entries and identity maps
  * rebuilds active closure
  * rescans all candidates
  * rescored all candidates with exact marginal reclaim
  * unlocks
  * removes one candidate
  * repeats
* replace it with one plan/apply cycle per policy:

1. call the new unlocked size-measurement pipeline
2. snapshot session-owned roots under `sessionMu`
3. snapshot prune-relevant e-graph state under `egraphMu`
4. outside lock:
   * build active closure from the snapshot graph
   * build and sort candidates
   * run the one-pass ordered simulation
   * produce a prune plan
5. apply the prune plan back to live state best-effort
6. after apply, compact eq classes once if anything was actually removed

Result:

* one snapshot
* one ordered simulation
* one apply phase
* no repeated whole-world rebuilds

#### Add explicit prune snapshot structs

Specific change:

* add snapshot-only structs local to `cache_prune.go`, for example:
  * `type pruneSnapshot struct`
  * `type pruneSnapshotResult struct`
  * `type pruneUsageIdentityState struct`
  * `type prunePlanEntry struct`
* `pruneSnapshotResult` should contain:
  * `resultID`
  * `incomingCount`
  * `deps []sharedResultID`
  * `usageIdentity string`
  * `sizeBytes int64`
  * `hasPersistedEdge bool`
  * `expiresAtUnix int64`
  * `createdAtUnixNano int64`
  * `mostRecentUseTimeUnixNano int64`
* `pruneUsageIdentityState` should contain:
  * `identity string`
  * `sizeBytes int64`
  * `aliveMembers int`
* `prunePlanEntry` should contain:
  * the candidate result ID
  * the corresponding `CacheUsageEntry` metadata for reporting
  * the immediate reclaim delta produced at the moment that candidate was
    simulated

Result:

* prune planning can run entirely from immutable snapshot state
* no need to reach back into live `sharedResult` objects during simulation

#### Snapshot acquisition

Specific change:

* add a dedicated snapshot builder in `cache_prune.go` that runs under
  `egraphMu` and copies only prune-relevant state into `pruneSnapshot`
* the snapshot builder must not call any `CacheUsage*` object methods
* it should consume already-published `usageIdentity` / `sizeEstimateBytes`
  values produced by the earlier measurement pipeline
* while snapshotting:
  * normalize unknown `usageIdentity` to `dagql.result.<id>` only in the
    snapshot, not by calling back into object code
  * copy current persisted-edge expiry metadata
  * copy current created/last-used timestamps

Result:

* snapshot acquisition stays cheap and deterministic

#### Active closure computation

Specific change:

* remove the live-graph `sessionDependencyClosureLocked` use from prune
* replace it with an unlocked helper in `cache_prune.go` that computes active
  closure from:
  * the copied session-root set
  * the copied snapshot `deps`
* the closure builder should use plain DFS/BFS over the snapshot adjacency

Result:

* no session lock usage during prune planning
* no need to hold `egraphMu` while computing active closure

#### Candidate collection

Specific change:

* candidate collection should operate entirely on `pruneSnapshot`
* a candidate is:
  * a result with a persisted edge in the snapshot
  * not in active closure
  * matching the current prune policy
* keep the existing policy filtering semantics from:
  * `KeepDuration`
  * `All`
  * `Filters`
  * threshold target calculation
* reuse the existing `pruneTargetBytes`, `entryRecentlyUsed`,
  `persistedEdgeExpired`, and `cachePrunePolicyMatchesEntry` logic unless a
  specific simplification is required

Result:

* candidate definition stays honest and directly tied to removable retained
  roots

#### Candidate sort order

Specific change:

* replace the current exact-marginal scoring loop with a one-time sort
* sort candidates by:
  1. expired edge first
  2. older `MostRecentUseTimeUnixNano` first
  3. older `CreatedTimeUnixNano` first
  4. larger `SizeBytes` first
  5. lower result ID / entry ID tie-break
* use the candidate entry's already-snapshotted size as the heuristic input
* do not attempt to encode exact transitive reclaim into the heuristic itself

Result:

* the heuristic stays simple and explainable
* graph-awareness stays in the simulation layer

#### Ordered simulation

Specific change:

* delete `simulatePersistedEdgeRemovalLocked`
* replace it with an unlocked one-pass simulation over the sorted candidate list
* the simulation state should maintain:
  * `remainingIncomingCount[resultID]`
  * `collected[resultID]`
  * `aliveCountByUsageIdentity[identity]`
  * `sizeBytesByUsageIdentity[identity]`
* for each candidate in order:
  * decrement that candidate's `remainingIncomingCount`
  * if it hits zero, enqueue it
  * drain the queue:
    * mark each newly-collected result once
    * decrement the alive-member count for its usage identity
    * if an identity count reaches zero, add that identity's bytes to this
      candidate's immediate reclaim delta
    * decrement all deps of the collected result
    * enqueue any deps whose counts reach zero
  * append the candidate to the prune plan even if its immediate reclaim delta
    is zero
  * accumulate total reclaimed bytes
  * stop once cumulative reclaimed bytes reaches or exceeds target

This zero-delta behavior is intentional.

It is required for cases where:

* many tiny retained roots keep a huge shared dependency alive
* the early root removals reclaim little or nothing individually
* the big reclaim only appears after enough prerequisites have been removed

Result:

* graph-aware
* overlap-aware
* target-aware
* no per-candidate full rescoring pass

#### Apply phase

Specific change:

* the apply phase should take the ordered `prunePlanEntry` list and attempt to
  remove those persisted edges from live state in order
* for each planned entry:
  * call `removePersistedEdge(ctx, resultID)`
  * if the edge is already gone, skip it
  * if the result has drifted in live state, accept it and keep going
* only entries whose persisted edge was actually removed should be appended to
  the final `CachePruneReport`
* each report entry should use the candidate's immediate reclaim delta from the
  snapshot simulation as its `SizeBytes`
* `CachePruneReport.ReclaimedBytes` should be the sum of those applied entry
  deltas
* after apply, compact eq classes once if at least one persisted edge was
  actually removed

Result:

* best-effort drift-tolerant apply
* honest per-entry reclaim reporting for zero-delta prerequisite removals and
  later large unlocks

#### Obsolete current helpers

Specific change:

* remove `pruneCandidate` if it no longer matches the new snapshot/planner shape
* remove `simulatePersistedEdgeRemovalLocked`
* do not keep a second dead codepath around for the old exact-greedy planner

Result:

* hard cut to the new prune model

### `dagql/cache_test.go`

#### Replace greedy-specific assertions

Specific change:

* update tests that currently encode the old exact-greedy behavior
* specifically, `TestCachePrunePrefersHigherMarginalReclaim` should be replaced
  with a test that matches the new ordered-simulation semantics instead of
  asserting “pick the mathematically best single next candidate”

Result:

* tests stop pinning the implementation to the old rescoring algorithm

#### Add ordered-simulation coverage

Specific change:

* add a test where:
  * many tiny persisted roots share one large dependency
  * early candidate removals reclaim zero or very little
  * a later candidate unlocks the large shared reclaim
* assert that:
  * zero-delta prerequisite candidates are still selected into the plan
  * the large shared dependency bytes are counted exactly once when the last
    retaining root is removed

Result:

* direct coverage of the shared-base-image-style case discussed in design

#### Add candidate-definition coverage

Specific change:

* add a test that proves:
  * only results with persisted edges are selectable prune candidates
  * non-persisted interior nodes can still be reclaimed transitively
  * interior nodes with their own persisted edge are valid direct candidates

Result:

* candidate semantics stay explicit and regression-resistant

#### Add active-closure snapshot coverage

Specific change:

* add a test covering:
  * active/session-retained roots are excluded from candidate selection
  * their structural deps are also excluded via the snapshot-computed active
    closure
* keep the existing “exact dependency of active result is never pruned” and
  “term provenance only result is not protected” tests, but update them if
  needed to match the new snapshot planner instead of live-graph rescoring

Result:

* the new unlocked active-closure computation is covered directly

#### Add measurement / lock-behavior regression coverage

Specific change:

* add unit coverage for the new size-measurement split:
  * size callbacks are invoked outside the e-graph lock
  * unknown or mutable sizes are republished correctly
  * usage-identity dedupe still behaves the same after republish
* keep existing threshold/keep-duration tests and update only where report-entry
  ordering or zero-delta entries legitimately change

Result:

* lock-fix regressions become visible in tests instead of only in profiles
