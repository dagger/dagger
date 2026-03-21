# WHITEBOARD

## Agreement

## TODO
* Make sure that storing ID of objects on so many objects like Directory/File/Container doesn't actually result in O(n^2) space
   * Requires that pointers in call.ID are shared (or something fancier)
   * Probably just make sure shallow clone is used less, only when utterly needed for id op. make stuff more immutable
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
* Persistence follow-up: understand what the full-state mirror should do with `Service` results.
  * A shutdown flush in the disk-persistence coverage surfaced `persist result ... encode persisted object payload: type "Service" does not implement persisted object encoding`.
  * We need to decide whether `Service` should get a real persisted representation or be explicitly excluded from mirrored persistence, and then make that behavior intentional.
* Provisional Phase 6: make `Secret`, `Socket`, and `Service` persistence coherent.
  * We are going to need this soon for things like fully general persisted `GitRepository` / `GitRef` support, where auth sockets, secrets, and service-backed remotes show up in the object graph.
  * No implementation plan yet; just capturing that this is a real upcoming persistence phase, not incidental cleanup.
* !!! Check if we are losing a lot of parallelism in places, especially seems potentially apparent in the dockerBuild of e.g. TestLoadHostContainerd, which looks hella linear and maybe slower than it used to be
   * Probably time now or in near future to do eager parallel loading of IDs in DAG during ID load and such

## Notes
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.
  * Design implication: once a lookup lands on an output eq class, any materialized result in that eq class is a valid cache hit, even if it was originally materialized under a different but equivalent term.

* Cache/e-graph design decision: once a structural lookup has identified a term / output eq class, it is acceptable to return any materialized result in that output eq class, even if that result was originally materialized under a different but equivalent term.
  * In other words, output equivalence is authoritative; we do not require cache hits to stay confined to "results originally attached to this exact term".

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

# GC + Ref Counting + Dependency Tracking Refactor

## Current State Summary

There are currently two distinct lifetime systems that interact but are not the same thing:

* The DAGQL result cache in `dagql/`, built around `sharedResult`, e-graph identity, session tracking, `refCount`, `deps`, `heldDependencyResults`, and `depOfPersistedResult`.
* The snapshot/content lifetime system in `engine/snapshots/`, built around mutable/immutable refs, in-memory snapshot metadata, and containerd leases.

The most important current-state fact is that `engine.localCache` is DAGQL-cache-only today, not "all engine cache". The GraphQL API in `core/schema/engine.go` reads and prunes `srv.baseDagqlCache` via `engine/server/gc.go`, while the custom worker's BuildKit-style `DiskUsage` and `Prune` methods in `engine/buildkit/worker_source_metadata.go` are stubs.

`sharedResult` currently has four separate concepts that are easy to conflate:

* `refCount`: live runtime handles to the result
* `deps`: exact child-result dependency IDs
* `heldDependencyResults`: actual child `AnyResult`s whose refs are being actively held
* `depOfPersistedResult`: "must stay live even when `refCount` reaches zero"

Those are related, but they are not the same thing.

`refCount` is incremented in a few main places:

* normal cache hits in `lookupCacheForRequest`
* completed miss paths when a materialized `sharedResult` is returned
* do-not-cache calls that return an already-attached result
* `AddExplicitDependency`, which increments the child result's `refCount`
* `initCompletedResult` when it discovers exact `ResultCallRef` dependencies in the completed result's call frame

`refCount` is decremented through `AnyResult.Release`, which lands in `sharedResult.release`. In practice, most user-visible releases happen through `SessionCache.ReleaseAndClose`, because the session cache tracks returned results and releases them on close.

Dependencies between results are currently created in three main ways:

* explicitly via `AddExplicitDependency`
* automatically via `HasOwnedResults` and `attachOwnedResults`
* automatically from the completed result's call frame in `initCompletedResult` by walking receiver/module/arg/implicit-input `ResultCallRef`s

`deps` is the authoritative graph of exact materialized child results for:

* active-dependency closure
* release propagation
* persistence-retention closure
* explicit dependency bookkeeping

`heldDependencyResults` is the runtime ownership mechanism used while the parent still has active refs. `depOfPersistedResult` is the separate "stay live even after runtime refs are gone" bit.

When a `sharedResult` refcount drains to zero:

* `heldDependencyResults` are detached and released
* if `depOfPersistedResult` is false, the result is removed from the e-graph and `OnRelease` is run
* if `depOfPersistedResult` is true, the result remains in the e-graph with `refCount == 0` and `OnRelease` is not run

Persistable calls are marked in schema/field specs and then, on completion, `initCompletedResult` calls `markResultAsDepOfPersistedLocked`. That walk marks the root and its transitive reachable dependencies through both:

* `deps`
* result-call references in the stored call frame

That persistence walk is intentionally broader than active runtime ownership.

At the core object layer, the main direct snapshot owners are:

* `Directory` and `File`, which own immutable snapshots directly
* `Container`, which directly owns only `MetaSnapshot`; rootfs and mount snapshots are child object results
* `CacheVolume`, which owns a mutable snapshot
* some git-backed objects, which can own immutable snapshots

Snapshot refs are created primarily through `query.BuildkitCache().New(...)`, `MutableRef.Commit(...)`, `GetByBlob`, `Merge`, `Diff`, and persisted-object reload via `GetBySnapshotID` / `GetMutableBySnapshotID`.

Releasing snapshot refs does not necessarily remove snapshots:

* `ImmutableRef.Release` mostly drops the live handle and any temporary view lease; it does not delete the main committed lease or the metadata record
* `MutableRef.Release` removes non-retained mutable refs immediately, but retained mutable refs only update last-used and stay rooted

From the code as it exists today, the snapshot manager overwhelmingly does not call `Snapshotter.Remove` directly when dropping refs. Instead it deletes lease roots and metadata, leaving physical storage reclamation to containerd GC.

Snapshot-manager metadata in `engine/snapshots/` is in-memory only today. After restart, persisted snapshot links are restored by `GetBySnapshotID` / `GetMutableBySnapshotID`, which rehydrate minimal metadata from an existing snapshot ID and mark it retained.

There are a few important current-state wrinkles:

* `PreparePersistedObject` exists on several core objects, but I could not find a call site for it.
* `Container` itself does not report cache usage size directly; size accounting currently comes from child `Directory` / `File` / `CacheVolume` results that implement `CacheUsageSize` and `CacheUsageIdentity`.
* The engine's `engine/snapshots` fork diverges from `internal/buildkit/cache` in lifecycle-relevant ways. The biggest one is that `parentRefs.release` is a no-op in the engine fork, while the in-tree BuildKit version actually releases parent refs.

Pruning also currently has two systems in the tree, but only one is active for `engine.localCache`:

* DAGQL prune, active today for `engine.localCache`
* snapshot-manager / BuildKit prune, implemented in-tree but not wired through the custom worker's public prune/diskusage methods

The live engine path is:

* `engine.localCache.entrySet` -> `srv.baseDagqlCache.UsageEntriesAll(ctx)`
* `engine.localCache.prune` -> `srv.baseDagqlCache.Prune(ctx, prunePolicies)`
* automatic server GC also prunes `srv.baseDagqlCache`

DAGQL prune reasons over `CacheUsageEntry` data built from:

* `sizeEstimateBytes`
* `usageIdentity` for deduping shared physical storage
* `createdAtUnixNano`
* `lastUsedAtUnixNano`
* `recordType`
* `description`
* `ActivelyUsed`, which is literally `refCount > 0`

Candidate filtering today skips a result if:

* `refCount > 0`
* `depOfPersistedResult == true`
* it is in the active dependency closure of an active result
* it violates keepDuration
* it does not match filters

Important nuance: active-dependency closure walks `deps`, not result-call provenance refs.

When DAGQL prune chooses a result, it does not go through normal `sharedResult.release`. Instead it directly removes the result from the e-graph and result indexes, deletes dependency edges in surviving results that pointed at it, and then runs only the result's `OnRelease` callback after unlocking.

That means DAGQL prune can indirectly release snapshot refs through core object `OnRelease`, but it currently bypasses the normal `heldDependencyResults` release path.

The overall current-state tension is that there are really three different liveness notions in play:

* DAGQL runtime `refCount`
* DAGQL `depOfPersistedResult`
* snapshot-manager `CachePolicyRetain` plus containerd lease roots

Those systems interact, but they are not normalized into one coherent model.

## Brainstorming

These are brainstorming notes and opinions, not a settled design:

* `OnRelease` currently feels confusing enough that it may need simplification even if it is not outright buggy. There are codepaths where "run `OnRelease`" and "run normal release flow" feel too easy to mentally conflate, and that is a bad sign by itself.
* The session cache storing a plain slice of results feels like ancient-history scaffolding at this point. Even if the behavior is technically correct, the data structure itself feels too implicit and too hard to reason about, especially around duplicates and ownership semantics.
* The current ref-counting model is attractive mechanically because increments/decrements are cheap, but it feels treacherous because we are maintaining both:
  * counts
  * pointer/edge-like side structures (`deps`, held dependency results, session-held slices, etc.)
  It would be cleaner if there were one central index of "who points at what", and counts were derived directly from that one model.
* A centralized edge index may make more sense than today's mixture of systems. Shared results really do form a DAG, and pruning / liveness / transitive dependency questions all feel like they want to be answered from one coherent graph-like structure instead of a pile of partially overlapping mechanisms.
* It still makes conceptual sense that baseline dependencies come from the result call:
  * receiver
  * module
  * args
  * implicit inputs
  If those point at other shared results, then yes, that is a dependency. That general direction still feels right.
* It also still makes sense that we need a way to draw explicit edges that are not derivable from the result call alone. We have real use cases for that today, and things like `HasOwnedResults` / attach-owned-result behavior still feel conceptually valid.
* The cleanup target feels less like "throw away result-call-derived dependencies" and more like "unify how all dependency knowledge is tracked, queried, and used for pruning / release / persistence".
* There should be a separation between:
  * ownership / liveness edges
  * identity / equivalence / e-graph knowledge
  However, there is still an important intersection: cache-hit knowledge learned through term/equivalence machinery must be retained while it remains relevant to some materialized shared result, and should be released once it is no longer relevant to any remaining shared result. So the e-graph should stay separate from liveness, but the two still need controlled points of contact.
* It likely makes sense to remove `SessionCache` entirely and make the base cache session-aware. Session ownership should be part of the one ownership model, not an outside slice of handles that happens to release things later.
* The current direction is increasingly edge-centric:
  * one conceptual root
  * edges from that root to shared results for session ownership and persisted retention
  * ordinary sharedResult -> sharedResult dependency edges below that
  This is appealing because session close and TTL expiry both become "remove some root edges, then cascade".
* Telemetry is adjacent to this redesign, but it should not live in the cache anymore. If we need per-session seen-key dedupe, that should live on session/server state and be exposed to dagql through a small explicit interface rather than through a session-cache wrapper.
* This whole area probably wants hard-cut thinking, not incremental Stockholm-syndrome patching. We should be fully willing to replace old modeling assumptions if a cleaner system emerges.
* From a pruning perspective, the real job of prune is to manage persistable results and whatever they keep alive, directly or transitively. Non-persistable results are ideally released naturally once nothing points at them. Persistable results are the ones that can accumulate and therefore require policy.
* That means pruning wants to reason about a "currently removable closure" or "prunable frontier", not just "things with no incoming edges right now". If pruning one persistable root would make some of its children newly unreachable, those children should become prunable as part of the same conceptual analysis.
* A promising conceptual model is:
  * one conceptual root
  * session ownership is represented as root edges indexed by session
  * persisted retention is represented as root edges indexed as persisted
  * pruning means selecting persisted root edges to cut and then sweeping anything no longer reachable from the root
* For now, the only special root-owned edge indices that feel necessary are:
  * session edges
  * persisted edges
  There is still only one logical edge concept overall. More special-purpose indices can be added later if a real need appears, but they should not be invented speculatively.
* This is clearly a solved-problem family in the abstract sense. There are known graph / GC / reachability / mark-sweep / ownership-index / transitive-reduction / liveness-analysis concepts that should probably inform the redesign, even if we do not copy any one system literally.
* For now, it is preferable to assume the ownership graph is acyclic. I cannot currently think of a legitimate engine use case for circular ownership dependencies, and designing around hypothetical cycles too early risks overengineering.
* The result-call-derived dependency model still feels conceptually correct as the baseline; the redesign pressure is much more about:
  * how ownership knowledge is represented
  * how reachability / prunability are computed
  * how that state is exported/imported
  than about replacing result-call-derived dependency discovery entirely.
* It is too early to optimize around derived counters or fast-path refcount implementations. A centralized ownership/indexing model matters more than whether the final implementation keeps a cached incoming-edge count or recomputes more often.
* It is still okay, and probably desirable, for TTL metadata to exist on `sharedResult` because that is where callers conceptually specify it today. But that does not mean sharedResult-local TTL metadata should be the authoritative lifetime mechanism. The actual retention / expiry semantics may still need to live on the root edge that is keeping the result alive.
* There is no appetite for a big type-system explosion around edges. A simple model is preferred:
  * one logical ownership edge concept
  * one generic dependency adjacency structure for `sharedResult -> sharedResult`
  * separate indices/tables for root-owned edges such as session ownership and persisted retention
  * no elaborate polymorphic edge hierarchy unless a real need appears
* The distinction between ownership/liveness and e-graph identity still stands, but the systems are not independent:
  * ownership decides what materialized shared results stay alive
  * shared results justify retained cache/e-graph knowledge
  * when the last relevant shared result goes away, the associated cache knowledge can go away too
* The snapshot-manager side is still a mess, but it probably makes more sense to first clarify the higher-level ownership / liveness / pruning model, then fold snapshots into that clarified model afterward instead of starting from the low-level snapshot mechanics.

## Design

First concrete design pass. This is still design, not implementation, but it should be treated as more concrete than the brainstorming section above.

### Core Model

Use one conceptual root plus ownership edges.

The ownership graph has:

* one conceptual root
* root-owned session edges from the root to `sharedResult`
* root-owned persisted edges from the root to `sharedResult`
* ordinary dependency edges from child `sharedResult` to parent `sharedResult`

Terminology:

* a child depends on a parent
* dependency edges therefore point from child to parent
* example: a base container image is a parent; a `withExec` result built on top of that image is a child
* removing a live child may make one of its parents collectible

E-graph / term / digest knowledge remains separate from ownership/liveness. It should be associated with live materialized shared results, but it should not itself decide what is alive.

The ownership graph is assumed to be acyclic for now.

There is no `SessionCache` in the target architecture.

Instead:

* the base DAGQL cache is the only cache type
* session ownership is represented directly in the base cache's ownership graph
* session lifecycle coordination lives in engine/session state (`daggerSession`)
* the base cache does not infer session ownership from context
* ownership-capable cache APIs take explicit `sessionID string` arguments
* callers must pass a real session ID when they are creating session ownership
* `sessionID == ""` is only for explicit non-owning internal paths
* the base cache exposes `ReleaseSession(ctx, sessionID)` to remove all session-owned state
* `dagql.Server` receives the base cache directly, along with whatever explicit session/telemetry state it needs for caller-facing cache operations

### Node Metadata

Each `sharedResult` remains the node identity.

Each node should have metadata roughly like:

* `resultID`
* `self`
* `resultCall`
* cache/persistence metadata already conceptually living on sharedResult
* finalizer
* size / usage identity / record type / timestamps
* TTL metadata may still be stored on `sharedResult` for provenance / API clarity, even if authoritative retention semantics live on persisted edges
* associations to e-graph / term / digest knowledge that are justified by this live materialized result

Session-specific telemetry dedupe metadata is not node metadata and does not live in the cache. It lives in engine/session state and is exposed to dagql through an explicit interface.

### Source Of Truth

Source of truth:

* `sharedResultsByID: map[resultID]*sharedResult`
* `parentResultsByChild: map[resultID]set[resultID]`
* `sessionEdgesBySession: map[sessionID]set[resultID]`
* `persistedEdgesByResult: map[resultID]PersistedEdge`
* `arbitraryResultsBySession: map[sessionID]set[arbitraryCallKey]`

Derived / cached state:

* `incomingOwnershipCount: map[resultID]uint32`
* any `childResultsByParent` reverse index if we decide to keep one for convenience or debugging
* any reverse root-edge summaries that make pruning or debugging cheaper
* any sorted candidate views / heaps / scores used by prune
* any temporary simulation state

The important rule is that edge membership is source-of-truth. Counts are derived cached summaries.

### Edge Representation

There is one logical ownership-edge concept. Do not build a large edge type system.

Concrete representation can stay boring:

* `parentResultsByChild` for dependency edges (`child -> parent`)
* `sessionEdgesBySession` for root-owned session edges
* `persistedEdgesByResult` for root-owned persisted edges
* a separate arbitrary-results-by-session index for the non-graph arbitrary cache path

Session edges are currently modeled as a set, not a multiset:

* if a session "opens" the same result multiple times, that does not currently change liveness semantics
* the result stays owned by that session until the session closes
* if we ever introduce true mid-session per-handle release semantics, revisit multiplicity then

Persisted ownership is intentionally result-centric in this design:

* there is at most one persisted ownership edge per `sharedResult`
* if the same `sharedResult` is observed again in a persistable result path, update the existing persisted edge instead of creating another one
* if multiple distinct cache identities / terms / digests point at the same `sharedResult`, that multiplicity belongs in the e-graph association layer, not in the ownership graph

### Persisted Edge Metadata

A persisted edge should contain metadata needed for pruning / retention policy, such as:

* `resultID`
* `createdAt`
* `expiresAt` / TTL-derived metadata, if any

TTL should participate authoritatively in persisted-edge retention semantics, not directly in node liveness.

Effective TTL merge semantics for repeated persistable observations of the same `sharedResult`:

* `expiresAt == 0` means "this observation has no TTL", not "this result is globally non-expiring"
* if one observation has a TTL and another does not, keep the TTL
* if both old and new have TTLs, keep the earlier `expiresAt`
* `expiresAt` only remains `0` if all observations are `0`

### Collectibility

A `sharedResult` is collectible iff its incoming ownership count is zero.

That means there is no remaining:

* session edge to it
* persisted edge to it
* dependency edge to it from any still-live `sharedResult`

E-graph associations do not count toward liveness.

### Edge Mutation Rules

Dependency edges:

* created when a `sharedResult` is materialized
* may also be explicitly added later for real use cases
* otherwise should be treated as effectively immutable for the life of the child node

Session edges:

* created only when an ownership-capable cache API is called with a non-empty explicit `sessionID`
* removed when the session closes

That means session ownership is not attached later by a wrapper, and it is not inferred implicitly from ambient context.

### Ownership-Capable Cache Entrypoints

Only a small set of cache entrypoints may create session ownership:

* `GetOrInitCall(ctx, sessionID, ...)`
* `LoadResultByResultID(ctx, sessionID, ...)`
* `AttachResult(ctx, sessionID, ...)`
* `GetOrInitArbitrary(ctx, sessionID, ...)`

Rules:

* `sessionID != ""` means the call may create session ownership
* `sessionID == ""` means the call is explicitly non-owning
* if there is no legitimate production use case for an empty session ID on a given entrypoint, empty session ID should be an error rather than a silent fallback

Pure internal cache lookup helpers should not create session ownership and should not be exported:

* `lookupCacheForDigests`
* `lookupCallRequest`

Those internal helpers exist specifically for recipe replay / structural lookup / other non-owning internal paths.

Persisted edges:

* created or updated when a persistable result has been materialized, returned, and is in the result-handling / indexing path
* keyed by target `resultID`
* if the same `sharedResult` is seen again in a persistable result path, update only the persisted edge metadata that has explicit design meaning
* do not model separate persisted ownership edges for separate cache identities that happen to converge on the same `sharedResult`

### Finalizer Semantics

Replace the current overloaded `OnRelease` mental model with the idea of a finalizer.

A finalizer:

* is specific to the node/type
* is not ownership bookkeeping
* runs exactly once when the node is actually collected
* may release external resources such as snapshots, services, temp files, etc.
* may be expensive in wall-clock time, so it must run outside coarse graph locks once the node has been detached from authoritative indices

Ownership edge removal / count updates / graph detachment are not part of the finalizer.

### Synchronous Collection Cascade

Collection should be immediate and synchronous as part of edge removal, not delegated to a background cleaner.

The "queue" here is only a local in-memory work queue used by the current operation, not an async system.

High-level process for removing ownership edges:

1. Remove one or more ownership edges under the ownership-graph lock.
2. For each target whose incoming ownership count becomes zero, push its `resultID` into a local work queue.
3. While the queue is not empty:
   1. Pop one zero-count node.
   2. Detach it from ownership indices / maps so the graph no longer considers it live.
   3. Remove its outgoing dependency edges to its parents.
   4. Decrement incoming counts of each parent.
   5. If any parent reaches zero, push it onto the same local queue.
   6. Remove any e-graph / digest / term associations that are no longer justified by live materialized results.
   7. Record the node's finalizer for later execution.
4. Release the graph lock.
5. Run finalizers outside the lock.

Important implementation note:

* expensive work should happen after graph detachment and outside the lock where possible
* once the node is detached from the authoritative indices, expensive finalizer work can run without blocking unrelated graph mutations

### Session Close

Session close should be modeled as:

1. Engine/session state marks the session as closing and blocks new DAGQL work.
2. Engine/session state waits for in-flight DAGQL work for that session to drain.
3. Engine/session teardown calls `baseCache.ReleaseSession(ctx, sessionID)`.
4. The base cache removes all session edges for that session under the graph lock.
5. The base cache also releases all arbitrary cached values owned by that session.
6. The base cache seeds the local zero-count work queue.
7. The base cache runs the exact graph-detachment portion of the synchronous collection cascade under the lock.
8. The base cache releases the lock.
9. The base cache runs collected finalizers outside the lock.

No separate session-cache wrapper should exist in this design.

### Persisted Edge Add

High-level process:

1. Resolve the target `sharedResult`.
2. Use the target `resultID` as the persisted-edge key.
3. Under the graph lock:
   1. If the result is absent from `persistedEdgesByResult`, create the persisted edge and increment incoming ownership count on the target.
   2. If the result is already present, merge persisted-edge metadata using the explicit TTL rules above.

### Prune Model

Prune is conceptually two separate problems:

1. Choose which persisted edges to cut.
2. After cutting them, run exact cascading collection of newly unreachable nodes.

Problem (2) is exact.
Problem (1) is heuristic.

Prune chooses among persisted edges because those are the units we are allowed to cut.

### Prune Policy Translation

Existing prune policies (`maxUsedSpace`, `reservedSpace`, `minFreeSpace`, `targetSpace`, etc.) should be translated into:

* whether pruning should start
* how many bytes we need to reclaim
* when pruning can stop

The ownership/cascade model should remain the same regardless of policy flavor.

### Prune Heuristic: First Pass

First-pass high-level algorithm:

1. Compute current used bytes and target reclaim bytes from the current prune policy.
2. Build candidate set from persisted edges only.
3. Expired persisted edges are always highest priority.
4. Sort remaining candidates by a cheap heuristic score biased toward:
   * oldest `sharedResult.lastUsedAt`
   * maybe largest estimated reclaim as a secondary factor if cheap enough
5. Iterate candidates greedily.
6. For each candidate edge:
   1. cheaply skip obviously-zero-reclaim cases, such as a target that is still directly owned by a session edge
   2. run an exact scratch simulation of "remove this persisted edge and cascade"
   3. compute marginal reclaimed bytes
   4. if marginal reclaim is zero, skip it
7. Pick the best current candidate.
8. Apply that cut for real.
9. Repeat until:
   * reclaim target is satisfied, or
   * no candidate can reclaim anything useful

Special-case broad prune modes like "prune everything" so we do not waste time running unnecessary repeated simulations.

### Simulation

Exact scratch simulation should be local and bounded to the affected subgraph:

* do not mutate the real graph
* do not run finalizers
* do not touch disk
* only simulate ownership-count decrements and cascade reachability
* sum reclaimable bytes of simulated-collected nodes

### E-Graph Interaction

Ownership graph decides liveness.

E-graph / term / digest knowledge should:

* remain separate from ownership/liveness
* be associated to live materialized shared results in controlled ways
* be removed when no remaining live materialized result justifies it

Do not use terms / eq classes / digest sets to answer "what is alive?"

### TTL Clarification

TTL has two related but distinct roles:

* `sharedResult` TTL metadata governs cache-hit eligibility
* persisted-edge TTL metadata governs retained-root expiry for pruning / persistence

That means:

* session-scoped / non-persisted results may still have a hit-expiry deadline on the node even though they have no persisted edge
* persisted results should update both node hit-expiry metadata and persisted-edge retention metadata using the same "earliest non-zero expiry wins" semantics

### Active vs Retained Clarification

`ActivelyUsed` is only important for the public engine-cache entry API surface. It should not drive prune design.

That means:

* `ActivelyUsed` can simply mean "has a direct session edge"
* a persisted-only retained result is not "actively used"
* prune eligibility should be blocked by session reachability in the ownership graph, not by the `ActivelyUsed` field

### Persistence Boundary Clarification

Persistence export / import should operate on the persisted-root-reachable subgraph, not on the whole in-memory graph.

That means:

* session edges are never persisted
* conceptually, only results reachable from persisted edges are part of persisted state
* however, export code should not try to re-derive or enforce that by filtering the live graph
* clean shutdown already closes sessions first and runs ordinary release/cascade before persistence export
* so by the time export runs, anything that should have disappeared because of session closure should already be gone
* export can therefore serialize the remaining graph state naively, as long as the in-memory ownership model is correct
* dependency edges and e-graph knowledge should simply mirror that remaining persisted state

## Implementation Plan

This is the pre-implementation map from the current code to the desired design.

The point of this section is to identify, before coding, exactly where the current implementation encodes the old model and what each rewrite chunk needs to do.

### First-Pass Scope

Phase 1 should include:

* the `sharedResult` ownership/liveness refactor
* deleting `SessionCache` entirely
* moving session ownership into the base cache ownership graph
* persisted ownership moving out of `depOfPersistedResult` booleans and into explicit persisted edges
* prune moving to "cut persisted edges, then exact cascade"
* persistence export/import moving to explicit persisted-edge state
* debug / test updates needed to make the new model observable
* deleting stale/orphan lifecycle helpers that no longer fit the new model

Phase 1 should explicitly not try to solve everything:

* arbitrary cached values can remain a separate path for now
* snapshot-manager redesign remains a later phase

### Migration Order

Preferred order:

1. Update the design/debug surface so the new ownership graph can be inspected while being built.
2. Delete `SessionCache` by moving its real responsibilities either into the base cache or into engine/session state.
3. Rewrite in-memory ownership state in `dagql/cache.go`.
4. Rewrite prune to consume the new ownership graph.
5. Rewrite persistence export/import and schema to persist the new ownership state.
6. Rewrite tests and debug output around the new model.

### File Map

### `dagql/cache.go`

This file is the center of the rewrite.

Current responsibilities encoded here that must change:

* `sharedResult` currently mixes:
  * node metadata
  * release callback
  * `refCount`
  * `depOfPersistedResult`
  * `deps`
  * `heldDependencyResults`
* `AddExplicitDependency` currently mutates `deps` and manually increments child `refCount`
* `sharedResult.release` currently:
  * decrements `refCount`
  * conditionally removes from e-graph
  * releases `heldDependencyResults`
  * runs `onRelease`
* `initCompletedResult` currently:
  * mirrors result-call refs into `deps`
  * increments child `refCount`
  * marks persisted closure via `markResultAsDepOfPersistedLocked`
* TTL merge currently lives on `sharedResult.expiresAtUnix` and keeps the earlier expiry
* the base cache currently has no explicit session-ownership boundary in its API surface and relies on `SessionCache` for that

Planned rewrite chunks:

* Redefine `sharedResult` to be mostly node metadata:
  * payload / resultCall / object typing / safe-to-persist / description / size / timestamps
  * finalizer instead of `onRelease`
  * node hit-expiry metadata for cache-hit eligibility
  * no `depOfPersistedResult`
  * no `deps`
  * no `heldDependencyResults`
  * no old `refCount` semantics as source-of-truth
* Add ownership graph state to `cache`:
  * `parentResultsByChild`
  * `sessionEdgesBySession`
  * `persistedEdgesByResult`
  * arbitrary-results-by-session tracking
  * cached incoming ownership counts
* Rewrite dependency creation:
  * result-call-derived parents added during `initCompletedResult`
  * `HasOwnedResults` / `AttachOwnedResults` adds explicit child->parent dependency edges
  * `AddExplicitDependency` becomes "add dependency edge" instead of "hold child ref"
* Make ownership-capable cache entrypoints take explicit `sessionID string` arguments:
  * `GetOrInitCall`
  * `LoadResultByResultID`
  * `AttachResult`
  * `GetOrInitArbitrary`
* Do not infer session ownership from `context.Context`
* Lowercase pure internal lookup helpers and keep them non-owning:
  * `lookupCacheForDigests`
  * `lookupCallRequest`
* Enforce explicit ownership boundaries:
  * non-empty `sessionID` creates session ownership where the design says it should
  * `sessionID == ""` is only for explicit non-owning internal paths
  * if an entrypoint has no legitimate production non-owning mode, empty session ID should be an error
* Add `ReleaseSession(ctx, sessionID)` to the base cache:
  * remove all session edges
  * release arbitrary values owned by that session
  * run the exact synchronous collection cascade
* Rewrite collection:
  * remove edge(s)
  * seed zero-count local queue
  * detach nodes and child->parent edges under the graph lock (see design above for the synchronous collection cascade)
  * record finalizers
  * run finalizers after unlock
* Rewrite `Result.Release` semantics:
  * detached results still directly run their finalizer if any
  * cache-backed result release must stop being a public/user-facing concept
  * eliminate public cache-backed `Result.Release()` usage entirely if possible
* Rewrite TTL handling:
  * keep node-level hit eligibility on `sharedResult`
  * move retained-root expiry semantics to `persistedEdgesByResult`
  * keep the "earliest non-zero expiry wins" semantics and apply them consistently to persisted-edge retention (see TTL design above)
* Rewrite usage accounting helpers:
  * keep `UsageEntriesAll` for the production engine cache API
  * delete test-only `UsageEntries` / `measurePruneCandidateSizesLocked`
  * `ActivelyUsed` should be a simple direct-session-edge flag for the public API only, not a prune input

### `dagql/session_cache.go`

This file should be deleted entirely.

Its responsibilities split in two directions:

* move real cache/lifetime behavior into `dagql/cache.go`
* move session lifecycle coordination and telemetry seen-key storage into `engine/server/session.go`

Planned rewrite chunks:

* delete `SessionCache`
* delete `NewSessionCache`
* delete `ReleaseAndClose`
* move cache-call telemetry option types and helpers out of this file into a neutral dagql file if they still make sense
* keep `WithRepeatedTelemetry` / `WithNonInternalTelemetry` only if they still serve a real purpose after the move, but do not keep them tied to a cache wrapper concept

### `dagql/cache_egraph.go`

This file currently mixes cache identity with old persisted-liveness marking.

Current responsibilities encoded here that must change:

* `lookupCacheForRequest` upgrades hits with `markResultAsDepOfPersistedLocked`
* persisted retention is currently expressed as a transitive boolean closure

Planned rewrite chunks:

* Remove `markResultAsDepOfPersistedLocked`
* On persistable hit/materialization, upsert a persisted edge in `persistedEdgesByResult`
* Keep identity/equivalence responsibilities here:
  * term lookup
  * digest association
  * eq-class maintenance
* Keep pure lookup helpers non-owning and internal-only:
  * `lookupCacheForDigests`
  * `lookupCallRequest`
* Keep e-graph cleanup tied to node removal, but not to liveness decisions

### `dagql/cache_prune.go`

This file currently prunes individual results out of the old `refCount` / `depOfPersistedResult` world.

Current responsibilities encoded here that must change:

* candidate selection currently scans `resultsByID`
* it skips `refCount > 0`
* it skips `depOfPersistedResult`
* `pruneResultLocked` directly removes one result and strips its ID out of other `deps`

Planned rewrite chunks:

* Candidate set should come from `persistedEdgesByResult`, not arbitrary results
* "active" blocking should come from session reachability closure, not from `ActivelyUsed` (see design above)
* prune should:
  * choose a persisted edge to cut
  * run exact scratch simulation (see prune design above)
  * apply the real edge cut
  * invoke the same synchronous cascade as ordinary edge removal (see design above)
* delete `pruneResultLocked` and replace it with persisted-edge-cut plus exact cascade
* keep policy translation logic, but change how targets are selected and reclaimed

### `dagql/cache_persistence_worker.go`

This file currently snapshots the whole in-memory graph and mirrors `depOfPersistedResult` state directly.

Current responsibilities encoded here that must change:

* `snapshotPersistState` currently iterates all `resultsByID`
* it mirrors raw `resultDeps`
* it mirrors `DepOfPersistedResult`
* it exports all reachable e-graph state, not just persisted-root-relevant state

Planned rewrite chunks:

* Add explicit persisted-edge rows to the persisted mirror state
* Stop using `DepOfPersistedResult` as the persisted-liveness encoding
* Because persistence runs after session closure/cascade at shutdown, do not add extra export-time filtering logic for session-owned state
* Keep dependency-edge export for the remaining graph state that survives to persistence
* Keep snapshot-link export for those results

### `dagql/cache_persistence_import.go`

This file currently reconstructs the old model directly from mirrored booleans and deps.

Current responsibilities encoded here that must change:

* import reconstructs `deps`
* import restores `depOfPersistedResult`
* import does not reconstruct explicit persisted edges because they do not exist yet

Planned rewrite chunks:

* Import explicit persisted-edge rows into `persistedEdgesByResult`
* Import dependency edges into `parentResultsByChild`
* Rebuild cached incoming ownership counts from:
  * persisted edges
  * dependency edges
* session edges start empty on import
* arbitrary-results-by-session state starts empty on import
* Reconstruct the exact e-graph knowledge that was exported; import does not need extra persisted-awareness beyond the imported rows themselves

### `dagql/cache_persistence_contracts.go` and `persistdb`

These files define the persisted mirror shape, so they must hard-cut with the new model.

Planned rewrite chunks:

* add persisted-edge rows/contracts
* remove `DepOfPersistedResult` as the authoritative persisted-liveness encoding
* bump persistence schema version
* update mirror import/export tests accordingly

### `dagql/cache_persistence_self.go`

This file is mostly payload codec logic and should stay conceptually similar.

Main thing to verify during implementation:

* persisted object encoding still works correctly under the new explicit persisted-edge model

### `dagql/types.go` and core object finalizers

The current `OnRelease` terminology is part of the confusion and should be cleaned up as part of the hard cut.

Files directly involved:

* `dagql/types.go`
* `core/directory.go`
* `core/file.go`
* `core/container.go`
* `core/cache.go`
* `core/git.go`

Planned rewrite chunks:

* rename the internal lifecycle concept from `OnRelease` to a finalizer-oriented name
* keep the behavior the same in spirit:
  * directory/file/container/cache/git objects release underlying snapshots/resources
* ensure finalizers are only run after graph detachment, outside coarse locks
* delete `PreparePersistedObject`-style methods unless implementation immediately proves a real need during the cutover

### `dagql/server.go`

This file currently bakes `SessionCache` into the server type.

Current responsibilities encoded here that must change:

* `Server.Cache` is currently `*SessionCache`
* `NewServer` and `WithCache` currently take `*SessionCache`
* `recipeLoadState.cache` currently depends on the session-cache wrapper, including `lookupCallRequest`

Planned rewrite chunks:

* change `Server.Cache` to the base cache type
* change `NewServer` and `WithCache` accordingly
* ensure caller-facing server paths have an explicit session ID available for ownership-capable cache calls
* keep `lookupCallRequest` on the base cache in simplified form if still needed for recipe loading
* lowercase `LookupCacheForDigests` to `lookupCacheForDigests` and keep it internal-only
* add a small explicit interface on `Server` for telemetry seen-key access
  * implemented by engine/session-side state
  * not by the cache

### `engine/server/session.go`

This file should become the owner of DAGQL session lifecycle state once `SessionCache` is deleted.

Current responsibilities encoded here that must change:

* `daggerSession` currently stores `dagqlCache *dagql.SessionCache`
* session init constructs `dagql.NewSessionCache(...)`
* session teardown calls `sess.dagqlCache.ReleaseAndClose(ctx)`

Planned rewrite chunks:

* remove `dagqlCache *dagql.SessionCache`
* keep `srv.baseDagqlCache` as the only cache instance
* add explicit DAGQL session lifecycle state to `daggerSession`:
  * open/closed marker for DAGQL work
  * in-flight request counting
  * wait primitive for draining in-flight DAGQL requests
  * telemetry seen-key storage
* on session close:
  * mark session closing
  * reject new DAGQL work
  * wait for in-flight DAGQL requests to drain
  * call `srv.baseDagqlCache.ReleaseSession(ctx, sess.sessionID)`
* `dagql.NewServer(...)` should receive the base cache directly plus the explicit telemetry/session interface it needs
* engine/session code should pass explicit session IDs into ownership-capable cache APIs instead of expecting the cache to discover them from context

### `core/query.go`

This file currently leaks `SessionCache` as a type into the core/query interface surface.

Planned rewrite chunks:

* change `Cache(context.Context)` from `*dagql.SessionCache` to the base cache type/interface
* change `CurrentDagqlCache(...)` accordingly or delete/rename it if the old name becomes misleading
* update all server implementations and callers to stop depending on `SessionCache` as a type
* update callers to pass explicit session IDs when invoking ownership-capable cache entrypoints

### `dagql/cache_persistence_resolver.go`

This file currently carries `SessionCache` forwarding adapters.

Planned rewrite chunks:

* delete the `SessionCache` forwarding methods entirely
* keep only the base-cache implementations

### Telemetry Hooks

Telemetry dedupe should no longer live in the cache layer.

Planned rewrite chunks:

* move session-level seen-key storage to `daggerSession`
* keep dagql telemetry callback construction in `dagql/objects.go`
* expose seen-key access to dagql through a small explicit interface passed into `NewServer`
* do not let cache internals own or reason about telemetry dedupe

### Arbitrary Cached Values

Arbitrary cached values stay separate from the ownership graph for now.

Planned rewrite chunks:

* keep arbitrary cached values stored in the base cache
* add base-cache tracking of arbitrary values by session
* make `ReleaseSession` release those arbitrary values in addition to removing session edges from the ownership graph

Files to delete `PreparePersistedObject` from:

* `core/directory.go`
* `core/file.go`
* `core/container.go`
* `core/cache.go`
* `core/git.go`

### `core/*` `AttachOwnedResults`

These paths are where child->parent dependency edges are discovered outside pure result-call provenance.

Files directly involved:

* `core/directory.go`
* `core/file.go`
* `core/container.go`
* `core/git.go`
* `core/module.go`
* `core/modulesource.go`
* `core/object.go`

Planned rewrite chunks:

* audit every `AttachOwnedResults` implementation under the new child->parent ownership graph
* confirm each one is truly adding parent dependencies, not trying to simulate ownership via held refs
* update any tests that currently rely on the old attach/release/refcount behavior

### `engine/server/gc.go`

This file should stay relatively simple, but its comments and expectations need to match the new world.

Planned rewrite chunks:

* keep prune policy parsing / override resolution
* update local-cache comments / semantics so "actively used" means "has a direct session edge"
* keep translating prune reports from dagql usage entries

### `dagql/cache_debug.go`

This file becomes more important during the refactor, not less.

Planned rewrite chunks:

* expose the new ownership graph state:
  * session edges
  * persisted edges
  * child->parent dependency edges
  * incoming ownership counts
* remove or de-emphasize old `depOfPersistedResult` / `refCount` debug fields
* make sure debug dumps are good enough to explain prune and liveness decisions empirically

### Test Rewrite Map

Tests that will likely need direct attention:

* `dagql/session_cache_test.go`
  * delete this file and replace its meaningful coverage with base-cache / engine-session tests
* `dagql/cache_persistence_worker_test.go`
* `dagql/cache_persistence_import_test.go`
  * rewrite around explicit persisted edges and the post-session-close export model
* `dagql/cache_test.go`
  * rewrite tests that currently exercise `UsageEntries()` or direct cache-backed `Result.Release()`
  * add tests for `ReleaseSession`
  * add tests for explicit `sessionID` behavior on ownership-capable cache entrypoints
  * add tests that empty `sessionID` errors where no legitimate production non-owning mode exists
* prune-focused tests in `dagql` and `core/integration`
  * assert session-active vs persisted-retained behavior clearly
* cache debug / introspection tests, if any, that currently assume `depOfPersistedResult`

### Main Gotchas Already Identified

These are the places most likely to bite during implementation:

* current prune logic treats `refCount` as both "active" and "retained", which is exactly the conflation we are trying to remove
* current persistence worker mirrors `depOfPersistedResult` booleans instead of explicit persisted edges
* `PreparePersistedObject` exists today but appears to be dead/orphaned code and should be deleted
* current direct `Result.Release()` behavior is still embedded in some internal code paths and must be audited during the cutover
* `SessionCache` is currently doing several unrelated jobs at once (lifetime tracking, session close gating, telemetry dedupe, forwarding), and all of those have to be split cleanly rather than recreated under a different name

### Explicit Deletions

This list is intentionally incomplete.

This is a hard cutover. That means:

* anything old, unused, outdated, superseded, or no longer coherent with the new model should be deleted as we encounter it
* do not preserve dead code, transitional helpers, compatibility shims, or stale concepts just because they already exist
* if the refactor creates an opportunity to remove obsolete code cleanly, that is part of the work, not optional cleanup for later

Production/runtime code to delete as part of the cutover unless implementation immediately proves otherwise:

* test-only `cache.UsageEntries()`
* test-only `cache.measurePruneCandidateSizesLocked()`
* orphan `PreparePersistedObject` methods
* public cache-backed `Result.Release()` semantics
* `SessionCache`
* `NewSessionCache`
* `ReleaseAndClose`
