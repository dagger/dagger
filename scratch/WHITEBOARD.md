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
* !!! Check if we are losing a lot of parallelism in places, especially seems potentially apparent in the dockerBuild of e.g. TestLoadHostContainerd, which looks hella linear and maybe slower than it used to be
   * Probably time now or in near future to do eager parallel loading of IDs in DAG during ID load and such

## Notes
* Big downside to storing ID on Directory/File/Container and re-exec'ing to dagop-flavor is that IDs are really per-caller now, but results are shared. So if we store the ID, what which one is it? Too confusing to think through.
   * Eh, technically the e-graph equivalence should take care of this, might not matter
   * HOWEVER it would still mean that if you e.g. select a directory on a container, then the "presentation ID" to the caller should be something it knows. But I guess if the presentation ID is just "...Container.directory", then who cares?
   * Crucial part to is that Container has a cache dep on its referenced Directories, cannot prune directory unless Container is pruned
   * It will be a little weird that Container has a ObjectResult[Directory] and an associated ID that's not necessarily relevant in particular anymore (at least, recipe not necessarily relevant in its entirety, just the digests used for equivalence checking). But I'm okay with weird for now, not a big deal at all for this iteration.
   * So to summarize all of the above: Container storing ObjectResult for Directory (and similar patterns elsewhere) should be a-okay if our understanding is correct

* Approach to replace the convoluted "dag-op self reinvoke in different flavor" approach:
   * API fields can be marked as "persistable"
   * They are checked in memory same as any others
     * If we end up paging out to disk, we'd need an fallback check of on disk, which might be a lil tricky but feasible. 
       Not needed for now though
   * On completion, everything is stored in memory still, but we put it in a "persist queue"
     * In "page to disk" future, stays in-mem until persisted
   * Part of what is persisted, is the ID! As explicated in later point ("For persistence, ..." below)
   * On session close, does not leave memory
   * Then, in terms of lazy evaluation, there is no extra step that has to go through the cache. It's just a lazily evaluated/single-flighted callback. Emphasis on callback I guess, that needs to be set on the object as a field
   * On engine restart, we load the whole ID. 
      * Anything in the ID that relied on per-client state must be guaranteed cached too or else we are screwed of course, but that's fine and a given in reality.
     * This also relies on the fact that anything even slightly expensive stays in the persisted state so we don't recompute it when loading. We can just fly through the id loading and be done with it.

* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* Need to be really careful about .Clone() now
   * Should the result be cloned? I'm going with no because usually Clone is used to make a new thing
   * Honestly clone should be re-assessed fundamentally, but just be careful for now
   * I guess there are cases where perhaps you *DO* want to clone the result because it doesn't change, jsut metadata. I.e. selecting a subdir of a Directory, you want same result but new metadata field 

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

* We should update the worker snapshot manager to be named that, not cache manager. 

* New rule of thumb for core: if it returns a LazyInitFunc, you should almost certainly not be calling it directly unless you are a dedicated core/schema api wrapping it. If you are not, you should be making a dagql.Select and/or id load to call this. That way, we keep all the ref+cache management in control of the dagql server!

* The current work in moving in the direction of not having the buildkit cache.Manager either (not to be confused with the solver.CacheManager...)
  * Idea in long run is to just store all the metadata in the objects themselves rather than storing the bkcache.ImmutableRef.
  * We are setting up bookkeeping and ref-counting (i.e. parent/child pointers) to aim for this future.


# Persistence Epic

## Persistence Notes

* Simulated persistence behavior can be misleading for fields whose IDs are still per-session/per-client scoped.
  * We should eventually avoid persisting results that can never be reused cross-session.
  * Not a blocker for this experiment.
* `container.from` needs follow-up on scope behavior.
  * End goal is that the digest-addressed invocation is persistable/reusable cross-session.
  * Tag-addressed flows can remain session-scoped where appropriate.
* Similar follow-up for `http` and any other per-client-scoped APIs that were marked persistable.
  * They may get pinned in-memory but provide little/no cross-session reuse until ID scoping is adjusted.
* TODO: function calls are now marked persistable unconditionally.
  * Optimization follow-up: skip persistable marking for per-session and never-cache function policies.
* There appears to be a race risk around ref-count reaching zero vs concurrent cache hits/acquires.
  * Need to verify exact behavior in current egraph lifecycle and ensure retain-on-zero does not make it worse.
* We need to integrate this with `safeToPersistCache`, but not yet.
  * Follow-up likely requires flipping default `safeToPersistCache` from false to true, then explicitly opting out where needed.
* Root cause discovered in `TestContainer/TestFileCaching/use_file_directly` during persistence experiment:
  * Retaining a persistable vertex (e.g. `Container.withExec`) without retaining its dependency chain allows e-graph equivalence evidence to be pruned.
  * In practice, dependent non-persistable terms (`Host.file`/`Directory.file`/`Container.withFile`) can be dropped on session close, so the retained term no longer has the bridging structure needed to match equivalent future inputs.
  * Short-term experiment strategy: retain full dependent results transitively whenever a result is retained/persisted.
  * Longer-term optimization option: retain only the necessary e-graph proof structure transitively (instead of full result payload retention), but that is explicitly deferred for now.

## Persistence Tasks

### Base implementation for memory-only first pass

* [x] Add a sticky retain flag on cached call results in dagql cache state (`sharedResult`) for simulation-only persistence.
* [x] When a persistable field call completes (initializer path), set the retain flag so zero-ref release does not evict it from egraph memory.
* [x] On cache hit from a persistable field, upgrade the hit result to retained even if original producer field was non-persistable.
  * Add explicit code comments explaining this ambiguous behavior choice so we revisit it intentionally later.
* [x] Update release lifecycle: when refcount reaches zero and retain flag is set, skip egraph removal and skip `onRelease`.
* [x] Add lightweight observability for the experiment (at least counters/logging for retained entries and session-close deltas).
* [x] Add focused tests for simulation semantics:
  * persistable result survives session close and is hit by a new session
  * non-persistable result still drops on zero ref
  * persistable-hit upgrade path retains entries created by non-persistable producers
* [x] Implement transitive retention of full dependent results for retained/persisted entries (simple/heavier experiment path).
  * [x] Rename `retainInMemory` to `depOfPersistedResult` to make intent explicit: this is about dependency liveness for persisted roots, not a generic retention feature.
  * [x] Add per-result dependency tracking on cached results (`sharedResult` -> dependent `sharedResult`s).
  * [x] Populate dependency edges during e-graph indexing by mapping each term input eq-class to a live producer result when available.
  * [x] Add transitive retention propagation helper (DFS/BFS under `egraphMu`) that marks all reachable dependencies as `depOfPersistedResult`.
  * [x] Apply transitive retention when:
    * a persistable field call completes
    * a persistable field cache-hit upgrades an existing non-persistable-produced result
  * [x] Keep retention sticky for now (no unretain/pruning pass in this experiment).
  * [x] Validate with:
    * `TestContainer/TestFileCaching/use_file_directly` (expected to stop re-executing on run #2)
    * focused dagql persistence tests
    * session-close retained-count logs still behaving as expected
* [ ] Deferred optimization follow-up: evaluate retaining only e-graph proof structure transitively (instead of full result payloads).

### TTL support

#### Plan

* Goal: re-add TTL enforcement in dagql cache/e-graph with no DB dependency.
  * TTL should gate cache-hit eligibility, not directly own object lifetime mechanics.
  * Keep this as an in-memory-only implementation for now.

* Core design: store TTL directly on `sharedResult`.
  * Current lookup paths:
    * term digest -> term set -> term results
    * output digest fallback -> result set -> associated term
  * Lookup already resolves to `sharedResult` objects; expiration checks should happen there.

* Proposed data model updates:
  * `ongoingCall` gets `ttlSeconds int64` copied from `CacheKey.TTL`.
  * Keep `egraphResultsByTermID` as:
    * `map[egraphTermID]map[sharedResultID]struct{}`
  * Add expiry on `sharedResult`:
    * `expiresAtUnix int64`
  * Expiry semantics:
    * `0` => never expires
    * `>0` => unix expiration timestamp

* Index/write behavior:
  * On call completion, set `sharedResult.expiresAtUnix` from `ongoingCall.ttlSeconds`:
    * `expiresAtUnix = now + ttlSeconds` when TTL is set
    * `expiresAtUnix = 0` when TTL is not set
  * If a result already exists and a new call with TTL aliases to that same result:
    * use conservative merge policy for now: `min(non-zero expiries)`; `0` only if all are `0`
    * add comments noting this is a policy choice and may need refinement.

* Lookup/read behavior:
  * In both canonical term lookup and output-digest fallback:
    * for each candidate `sharedResult`, check:
      * `expiresAtUnix == 0 || now < expiresAtUnix`
    * skip expired results and continue scanning for another candidate.
  * If all candidates are expired, treat as cache miss and run initializer.

* Session-scope behavior for unsafe TTL returns:
  * Previously this piggybacked on `persistToDB != nil`; now that DB path is removed, restore behavior explicitly with TTL.
  * In `wait` and `initCompletedResult`, use:
    * `if oc.ttlSeconds > 0 && !oc.res.safeToPersistCache { append sessionID implicit arg }`
  * This preserves per-session scoping for unsafe values when TTL is active.

* Expected properties:
  * TTL works in-memory without storage keys or DB.
  * Expired entries naturally stop hitting while leaving lifetime/refcount behavior unchanged.
  * Fits current e-graph/sharedResultID architecture cleanly.

* Tradeoffs / follow-ups:
  * No cross-process persistence yet (intentional for this step).
  * Expired results may linger in maps until normal cleanup or opportunistic prune.
  * One `sharedResult` can be associated with multiple terms/calls that had different TTL intent.
    * With `sharedResult.expiresAtUnix`, we lose per-association TTL precision.
    * Conservative `min` policy avoids over-retention but may expire reusable results earlier than ideal.
    * Future option: retain at least e-graph proof structure transitively while splitting TTL metadata by association if needed.
  * `GCLoop` is still DB-oriented and should be no-op-safe when `c.db == nil` (cleanup task).

#### Implementation

* [x] Plumb TTL into ongoing call state.
  * [x] Add `ttlSeconds` to `ongoingCall`.
  * [x] Set it from `CacheKey.TTL` in `GetOrInitCall`.
* [x] Store expiry on `sharedResult`.
  * [x] Add `expiresAtUnix int64` field.
  * [x] On completed call, compute candidate expiry from `ttlSeconds`.
  * [x] Merge expiry onto shared result with conservative policy:
    * [x] `0` only if all writers are `0`.
    * [x] otherwise earliest non-zero (`min`) wins.
  * [x] Add code comments explaining policy and why it is intentionally conservative.
* [x] Enforce TTL in lookup.
  * [x] Update deterministic result pickers to skip expired results.
  * [x] Apply this for both term-based lookup and output-digest fallback.
  * [x] If all candidates are expired, treat as cache miss.
* [x] Restore session scoping behavior for unsafe TTL values.
  * [x] Replace existing DB-gated checks with `ttlSeconds > 0 && !safeToPersistCache`.
  * [x] Apply in both return-ID shaping (`wait`) and index-ID shaping (`initCompletedResult`).
* [x] Keep e-graph term->result association maps as sets (no map value TTL).
* [ ] Validation.
  * [x] Run focused tests around persistable retention.
  * [x] Run focused tests around output-digest fallback.
  * [ ] Reconcile `TestCacheTTLNonPersistableEquivalentIDsCanCrossRecipeLookup` with new unsafe TTL session-scoping behavior.

### Pruning support

#### Plan

* Goal: implement cache pruning against dagql cache state (not buildkit), while preserving roughly the existing policy interface shape.
* Keep current top-level behavior shape:
  * automatic background gc entrypoint
  * explicit `engine.localCache.prune(...)` entrypoint
  * `useDefaultPolicy` + per-invocation space overrides
* Define dagql-native prune policy model equivalent to current worker policy fields:
  * `all`, `filters`, `keepDuration`, `reservedSpace`, `maxUsedSpace`, `minFreeSpace`, `targetSpace`
* Build dagql-native usage accounting needed for policy evaluation:
  * in-use vs releasable
  * size accounting
  * created/last-used timestamps
  * enough metadata for future filter support
* Implement policy application as ordered passes (like today’s list semantics), with deterministic candidate selection.
* Integrate prune execution under existing server-level serialization (`gcmu`) so automatic and manual prune do not race.
* Keep API return shape (`EngineCacheEntrySet`) but source entries from dagql cache state.
* Follow-up after first cut: remove or isolate remaining buildkit-coupled pruning hooks once dagql prune is authoritative.

#### NOTES

* Main prune-related entrypoints in current code:
  * `Server.gc()` in `engine/server/gc.go`
  * `Server.PruneEngineLocalCacheEntries(...)` in `engine/server/gc.go`
* Main callers:
  * `gc()` is scheduled through `srv.throttledGC`:
    * initialized in `engine/server/server.go` as `throttle.After(time.Minute, srv.gc)`
    * triggered once shortly after startup (`time.AfterFunc(time.Second, srv.throttledGC)`)
    * triggered after session removal in `removeDaggerSession` (`time.AfterFunc(time.Second, srv.throttledGC)`)
  * `PruneEngineLocalCacheEntries(...)` is called from GraphQL `engine.localCache.prune(...)` in `core/schema/engine.go`.
* Current implementation is buildkit-worker based end-to-end:
  * list entries: `srv.baseWorker.DiskUsage(...)`
  * prune: `srv.baseWorker.Prune(..., pruneOpts...)`
  * policy types: `bkclient.PruneInfo`
* Current `engine.localCache` API behavior:
  * `localCache` returns only one policy view (`EngineCache`) derived from `EngineLocalCachePolicy()` (the default/last policy).
  * It does not expose the full policy list, filters, or keepDuration.
  * `entrySet` has no filtering support yet.
  * `prune` returns `Void`, not the pruned set (even though server prune computes a set).
* `PruneEngineLocalCacheEntries` behavior details:
  * serialized by `srv.gcmu`.
  * when no active dagger sessions, calls `imageutil.CancelCacheLeases()`.
  * resolves prune options via `resolveEngineLocalCachePruneOptions(...)`.
  * executes worker prune and accumulates `UsageInfo` responses.
  * if anything pruned, attempts `SolverCache.ReleaseUnreferenced(...)` (buildkit solver metadata cleanup path).
  * returns `EngineCacheEntrySet` built from prune response items.
* `gc()` behavior details:
  * serialized by `srv.gcmu`.
  * runs worker prune with `srv.baseWorker.GCPolicy()` (full policy list).
  * sums pruned bytes and logs.
  * if anything pruned, schedules `srv.throttledReleaseUnreferenced` (5-minute throttle), which calls `srv.SolverCache.ReleaseUnreferenced(...)`.
* Policy resolution behavior today (`resolveEngineLocalCachePruneOptions`):
  * default when `UseDefaultPolicy=false`: single policy `{All: true}` (prune all releasable entries).
  * when `UseDefaultPolicy=true`: copy worker default policy list.
  * per-call overrides (`maxUsedSpace`, `reservedSpace`, `minFreeSpace`, `targetSpace`) are parsed from string disk-space syntax and applied to every selected policy.
  * tests in `engine/server/gc_test.go` verify:
    * override behavior for both default and non-default paths
    * no mutation of the default policy slice when overrides are applied
    * invalid disk-space strings produce argument-specific errors
* Default policy construction path:
  * worker created with `GCPolicy: getGCPolicy(...)`.
  * fallback order inside `getGCPolicy(...)`:
    * explicit engine config policies
    * converted buildkit config policies
    * generated defaults (`defaultGCPolicy(...)`)
  * `getDefaultGCPolicy(...)` currently means “last policy in list.”
  * conversion includes `All`, `Filter`, `KeepDuration`, and space fields.
  * when `SweepSize` is set, `TargetSpace` is derived from `MaxUsedSpace - SweepSize(...)` (clamped so it never uses 0, since 0 means “ignore”).
* Buildkit-specific coupling still embedded in prune surface:
  * `core.Query.EngineLocalCachePolicy()` returns `*bkclient.PruneInfo` (buildkit type leaks into core API interface).
  * `EngineLocalCacheEntries`/`PruneEngineLocalCacheEntries` operate only on worker/buildkit cache records.
  * comments note buildkit prune currently does not populate `RecordType` for pruned items.
* Interaction with current dagql cache:
  * dagql cache has its own `GCLoop`, but this is separate from engine prune APIs.
  * after recent changes, `GCLoop` is effectively no-op-safe when db is nil; it is not the policy-based prune mechanism we need.
* Key migration implication:
  * current public-ish behavior shape is “policy-driven prune with optional one-off space overrides,” but concrete enforcement is entirely buildkit-worker.
  * for hard cutover, dagql cache needs first-class policy evaluation + reclaim semantics; current interface can be preserved while swapping the backend implementation.

#### Implementation

##### Scope and constraints

* Hard cutover target: pruning decisions and deletions are driven by dagql cache state, not buildkit worker disk-usage/prune APIs.
* Keep external behavior shape roughly stable:
  * `engine.localCache.entrySet`
  * `engine.localCache.prune(useDefaultPolicy, maxUsedSpace, reservedSpace, minFreeSpace, targetSpace)`
  * automatic background `gc()` path
* Determinism requirement:
  * candidate ordering must be deterministic for reproducibility and easier debugging.
* Safety requirement:
  * do not prune in-use results (active refs / active evaluation dependencies).
  * prune execution must stay serialized with existing server gc lock (`gcmu`).
* Cohesion requirement:
  * one policy model used by both automatic gc and explicit prune API.

##### Phase 0: CacheVolume snapshot ownership cutover (prereq for pruning)

* [x] Move cache volume from metadata-only to snapshot-owning model in core.
  * [x] Extend `core.CacheVolume` with an owned snapshot ref (similar lifecycle to `Directory`/`File` snapshot ownership, but no lazy init).
  * [x] Add explicit snapshot access/update methods on `CacheVolume` (single place to enforce clone/release/refcount behavior).
  * [x] Keep cache volume identity keyed by namespaced cache key (`cache.Sum()`-derived identity), but ensure source-influenced variants are represented explicitly (see below).
* [ ] Preserve current source semantics while removing BuildKit cache-mount indirection.
  * [ ] Maintain behavior parity (cache mount behavior/result expectations), not strict parity of BuildKit identity internals.
  * [ ] Today, BuildKit cache mount identity is effectively `cacheID + optional baseRefID`; use this as reference behavior, but allow a different dagql-native identity rule if it is more cohesive and still preserves externally visible behavior.
  * [ ] Make cache identity derivation explicit in one place so future semantic changes (especially source/base handling) are easy and low-risk.
  * [ ] Keep owner/source preprocessing behavior coherent with current API expectations (`Owner` applies to source-initialized cache root/entries).
* [x] Replace `MountType_CACHE` runtime creation path with explicit snapshot mounts.
  * [x] In container mount-data prep, stop emitting BuildKit cache mounts for `CacheSource`; instead mount concrete snapshots (directory-style input mount behavior).
  * [x] In `withExec` output handling, capture writable cache mount outputs and write them back to the owning `CacheVolume` snapshot.
  * [x] Apply the same cutover for service container start path (services also call `PrepareMounts`; not just `withExec`).
* [ ] Re-home cache sharing-mode behavior away from BuildKit mount manager.
  * [ ] `SHARED`: default shared snapshot lineage for same cache identity.
  * [ ] `LOCKED`: serialize writes per cache identity in engine (explicit lock keyed by cache identity).
  * [ ] `PRIVATE`: follow current BuildKit-ish behavior for now, but isolate that policy decision behind an explicit seam so we can switch to stricter private semantics later without broad rewrites.
* [ ] Hook CacheVolume into dagql persistence/pruning accounting.
  * [ ] Ensure cache volume snapshot refs are visible to usage accounting (size + last-used/created metadata source).
  * [ ] Ensure pruner can reclaim cache-volume-backed snapshots safely when no active dependers.
* [ ] Validation for Phase 0 (before pruning Phase 1):
  * [x] `TestWithMountedCache`
  * [x] `TestWithMountedCacheFromDirectory`
  * [x] `TestWithMountedCacheOwner`
  * [ ] service path with mounted cache (`core/integration/services_test.go` cases)
  * [ ] platform and multi-session cache-mount reuse cases
  * NOTE: `TestServices/TestServiceTunnelStartsOnceForDifferentClients` currently fails (`expected 1 /cache/svc.txt`, `actual 0 /cache/svc.txt`). Current model writes service cache-mount outputs back on service cleanup; this does not yet provide live shared visibility while service is still running.
* [ ] REVIEW CHECKPOINT 0: confirm cache volume lifecycle semantics (identity, source/base behavior, sharing mode, write-back timing) before implementing prune metadata/policies.

##### Phase 1: Dagql prune metadata and usage accounting

* [x] Add/validate metadata on cache entries needed for pruning decisions:
  * [x] created timestamp
  * [x] last-used timestamp (updated on cache hit and post-initialization return)
  * [x] current in-use status derivable from cache state/refcount
  * [x] disk-space estimate/bytes tracked per entry (or explicit temporary approximation with TODO if exact accounting not ready)
  * [x] stable type/category string for debugging and future filters
* [x] Implement a locked snapshot method in dagql cache to enumerate usage entries with:
  * [x] id
  * [x] description/type
  * [x] size bytes
  * [x] created/lastUsed timestamps
  * [x] in-use bool
* [x] Ensure usage accounting includes retained persisted results (so they are visible candidates if policy says to reclaim).
* [x] Add focused unit tests in `dagql/cache_test.go` for usage snapshot correctness.
* [ ] REVIEW CHECKPOINT 1: stop and validate metadata/usage model before policy engine implementation.

##### Phase 2: Dagql-native prune policy model + option resolution

* [x] Introduce dagql-native prune policy struct (server-local or core-owned) equivalent to current fields:
  * [x] all
  * [x] filters
  * [x] keepDuration
  * [x] reservedSpace
  * [x] maxUsedSpace
  * [x] minFreeSpace
  * [x] targetSpace
* [x] Port/replace `resolveEngineLocalCachePruneOptions` to produce dagql-native policies (same override semantics).
* [x] Keep existing disk-space parsing behavior and error messages for overrides.
* [x] Preserve “default policy list copy + do-not-mutate originals” semantics.
* [x] Keep support for policy generation from engine config (`GC.Policies`, fallback defaults, sweep size behavior).
* [x] Add/port tests currently in `engine/server/gc_test.go` to validate identical override behavior.
* [ ] REVIEW CHECKPOINT 2: confirm policy compatibility and override parity before wiring prune execution.

##### Phase 3: Dagql prune execution engine

* [x] Add a dagql cache API to apply ordered prune policies and return pruned-entry report.
* [x] Implement policy pass execution:
  * [x] Evaluate each policy in order against current in-memory usage snapshot.
  * [x] Apply eligibility gates:
    * [x] not actively used
    * [x] keepDuration cutoff
    * [x] `all`/filters behavior (initially minimal but explicit)
  * [x] Compute reclaim target per policy using max/reserved/minFree/target semantics.
  * [x] Sort candidates deterministically (e.g. oldest last-used, then oldest created, then stable id tie-break).
  * [x] Prune until target satisfied or candidates exhausted.
* [x] Ensure pruning removes e-graph/result state coherently:
  * [x] remove indexes/associations
  * [x] release payload resources/onRelease hooks as needed
  * [x] maintain dependency and retained-state invariants
* [x] Add detailed debug logging hooks for prune decisions (candidate selected/skipped reason, reclaimed bytes).
* [x] Add focused unit tests:
  * [x] keepDuration behavior
  * [x] threshold behavior (`maxUsedSpace`/`targetSpace`)
  * [x] in-use entries never pruned
  * [x] deterministic selection order
* [ ] REVIEW CHECKPOINT 3: review prune semantics and invariants before server API switch.

##### Phase 4: Server integration and entrypoint cutover

* [x] Switch `EngineLocalCacheEntries` from buildkit `DiskUsage` to dagql usage snapshot.
* [x] Switch `PruneEngineLocalCacheEntries` from `baseWorker.Prune` to dagql prune execution.
* [x] Keep existing `gcmu` serialization around explicit prune.
* [x] Switch `gc()` to run dagql prune using default policy list.
* [x] Remove buildkit-specific side effects from prune flow where no longer applicable:
  * [x] `imageutil.CancelCacheLeases()` path
  * [x] `SolverCache.ReleaseUnreferenced(...)` post-prune path
* [x] Keep return type `EngineCacheEntrySet` populated from dagql prune results.
* [ ] REVIEW CHECKPOINT 4: verify server-level behavior (automatic gc + manual prune) before API cleanup.

##### Phase 5: API cleanup and compatibility polish

* [x] Remove buildkit type leakage from query interface where possible:
  * [x] replace `EngineLocalCachePolicy() *bkclient.PruneInfo` with dagql-native/core policy type
  * [x] update schema resolver mapping accordingly
* [x] Decide whether to expose full policy list in schema or keep current “default policy only” surface for now.
  * [x] keep current “default policy only” surface for now (full list exposure deferred)
* [x] Ensure docs/comments reflect dagql pruning, not buildkit pruning.
* [x] Update/fix `core/integration/localcache_test.go` to pass while preserving original behavior intent:
  * [x] do not weaken or “cheat” assertions
  * [x] keep testing the same underlying pruning/local-cache semantics the tests originally covered
  * [x] only adjust test mechanics where needed for dagql-prune cutover
  * [x] end state for this phase: `core/integration/localcache_test.go` passing
* [x] Add/adjust tests for `core/schema/engine.go` paths (`localCache`, `entrySet`, `prune`).
* [ ] REVIEW CHECKPOINT 5: ensure interface coherence and no lingering buildkit-prune assumptions.

##### Phase 6: Follow-up tasks (non-blocking for first cut)

* [ ] Implement richer filter semantics (if needed) against dagql metadata categories.
* [ ] Improve size accounting precision if first-cut uses approximations.
* [ ] Add metrics for prune runs:
  * [ ] candidates
  * [ ] pruned count/bytes
  * [ ] skip reasons
* [ ] Prepare seam for future async on-disk persistence queue:
  * [ ] prune should operate on in-memory graph of truth
  * [ ] disk updates are best-effort reflection, never source of truth for prune eligibility

# Snapshot Manager Cleanup

## Execution status

* [x] Execute all phases in one continuous pass (0 -> 5), with incremental commits after each phase.
  * This is intentionally long-running so compactions don't reset scope mid-way.

## Goals

* Hard-cut the copied BuildKit cache-manager behavior out of `engine/snapshots`.
* Keep only minimal snapshot lifecycle functionality needed by current engine/core callsites.
* Move lifecycle/dependency/persistence/pruning responsibility out of snapshot manager and into dagql/core (where we already model object dependencies and retention).
* End state should look like a purpose-built Dagger snapshot primitive, not a partially-adapted BuildKit cache system.

## Design constraints (explicit)

* No persistence in snapshot manager:
  * no BoltDB metadata
  * no on-disk cache record/index bookkeeping
  * no migration paths for old metadata schema
* No pruning in snapshot manager:
  * no `DiskUsage` implementation
  * no `Prune` implementation
  * no external ref checker hooks
* No progress-controller plumbing:
  * remove `progress.Controller` from snapshot-manager API surface and internals
* No lazy blob/remote snapshot machinery:
  * remove `DescHandler`/`NeedsRemoteProviderError`-driven lazy/unlazy flow
  * remove stargz-specific behavior and remote snapshot label handling
* No internal parent/dependency retention graph in snapshot manager:
  * no parent ref graph ownership for lifecycle/refcount retention
  * no recursive release behavior based on internal ref DAG
* No `equalMutable` / `equalImmutable` dual representation:
  * mutable and immutable refs are distinct and permanent states
  * no metadata fields or conversion shortcuts based on “equal” sibling records

## Current package observations (from code pass)

* Persistence + metadata are deeply embedded today:
  * `engine/snapshots/metadata.go`
  * `engine/snapshots/metadata/metadata.go`
  * `engine/snapshots/migrate_v2.go`
  * manager init/get/search paths are metadata-index-driven (`chainid`, `blobchainid`, etc.)
* Prune and disk-usage are still implemented in `manager.go` (buildkit-shaped policy model).
* Lazy remote/blob stack is broad:
  * `blobs.go`, `remote.go`, `opts.go`, stargz branches in `refs.go`/`manager.go`
* Parent/lifecycle graph is currently encoded in snapshot refs:
  * `parentRefs` + `diffParents` + recursive parent release
  * metadata parent pointers
* Equal mutable/immutable compatibility paths still exist:
  * `equalMutable`, `equalImmutable`, special remove/release branches

## Plan

### Phase 0: Lock minimal required surface

* [x] Inventory current non-test callsites of `engine/snapshots` API and classify by operation:
  * [x] `Get`
    * Used by schema/directory loading by ID and generic ref handoff paths.
  * [x] `GetMutable`
    * Used by filesync and cache-volume/mount mutation paths.
  * [x] `New`
    * Used broadly for creating mutable snapshots in directory/file/container/service/http/git flows.
  * [x] `Commit`
    * Used broadly after writes/mutations (directory/file/filesync/service/etc).
  * [x] `Mount`
    * Used by filesync/contenthash/git/http/util execution helpers.
  * [x] `Merge`
    * Still used by directory/changeset flows.
  * [x] `Diff`
    * Still used by directory/changeset flows.
  * [x] `GetByBlob`
    * Used by image pull path in `core/containersource/pull.go`.
  * [x] `GetRemotes` / container image export needs
    * Used by exporter (`engine/buildkit/exporter/containerimage`, `engine/buildkit/exporter/oci`).
* [x] Freeze a minimal interface target (`SnapshotManager` + refs) based only on actual callsites.
  * Keep now: `New`, `Get`, `GetMutable`, `GetByBlob`, `Merge`, `Diff`, ref mount/commit/release/clone/finalize/extract, metadata read/write needed by `core/cacheref`, `engine/filesync`, `engine/contenthash`, and `GetRemotes`.
  * Drop now: persistence DB requirements, prune controller contract, progress-controller contract, lazy/stargz-specific public option types.
* [x] Add TODO comments at temporary compatibility seams that survive this phase.
  * Merge/Diff kept for now due active callsites; revisit once higher-level APIs absorb them.
  * GetRemotes kept temporarily in snapshots; bias remains to move export-specific remotes construction toward exporter package as cleanup progresses.
* [x] Checkpoint: confirm we are not preserving API methods solely for legacy internal behavior.
  * Any method kept in this pass has a current non-test caller in `core`, `engine/filesync`, `engine/contenthash`, or exporter code.

### Phase 1: Remove persistence and metadata DB completely

* [x] Delete metadata store dependency from `SnapshotManagerOpt` and manager state:
  * [x] remove `MetadataStore *metadata.Store`
  * [x] remove `root` metadata-db dependent logic (server no longer initializes/closes snapshots metadata DB)
* [x] Delete metadata persistence implementation files:
  * [ ] `engine/snapshots/metadata.go`
  * [x] `engine/snapshots/metadata/metadata.go`
  * [x] `engine/snapshots/migrate_v2.go`
  * Note: `engine/snapshots/metadata.go` is retained but rewritten as in-memory metadata/index logic (no BoltDB, no on-disk persistence).
* [x] Replace record metadata with in-memory fields directly on cache records/refs for values still needed at runtime (description, createdAt, recordType, etc., only if still used by remaining APIs).
* [x] Remove metadata index search paths (`chainIndex`, `blobchainIndex`) and any retrieval that depends on DB-backed search.
  * Index lookup is now entirely in-memory (`metadataStore.index`), including blob/chain and external cache-key indexes.
* [ ] Remove `RefMetadata` API methods that only existed for persisted metadata semantics.
  * Deferred: kept current methods for active core/filesync/contenthash callsites; trim after downstream callsite cleanup.
* [x] Checkpoint: snapshot manager can start, create refs, mount, and commit without any BoltDB or migration path.

### Phase 2: Remove pruning and progress plumbing

* [x] Delete prune/disk usage controller behavior from snapshot manager:
  * [x] remove `Controller` interface members from public `SnapshotManager` contract
  * [x] remove `DiskUsage` + `Prune` implementation and helpers from `manager.go`
  * [x] remove prune-related locks/fields (`muPrune`, `PruneRefChecker`, `GarbageCollect`) from manager state/opts
* [x] Remove progress controller usage from snapshot APIs:
  * [x] remove `pg progress.Controller` parameters from `Get`/`Merge`/`Diff` internals
  * [x] remove `progress` field from immutable refs
  * [x] remove progress-related wrappers in remote pull/unlazy flow
* [x] Update callsites to the simplified signatures.
* [x] Checkpoint: `engine/server/gc.go` and all prune-facing behavior are fully decoupled from snapshot manager.

### Phase 3: Remove lazy/stargz/remote-unlazy machinery

* [ ] Delete lazy descriptor handler model from public options:
  * [ ] remove/replace `DescHandler`, `DescHandlers`
  * [x] remove `NeedsRemoteProviderError`
  * [x] remove `Unlazy` marker option type
* [ ] Delete lazy blob/remote code paths:
  * [ ] `engine/snapshots/blobs.go`
  * [ ] `engine/snapshots/blobs_linux.go`
  * [ ] `engine/snapshots/blobs_nolinux.go`
  * [ ] `engine/snapshots/remote.go` (or replace with minimal non-lazy remote export utility if still required)
* [x] Remove stargz-specific branches in refs/manager code (`Snapshotter.Name() == "stargz"` branches and related remote-label prep).
  * `manager.New`, immutable mount/extract, and mutable mount no longer have stargz-special handling.
* [~] Simplify `GetRemotes` behavior to only operate on already-materialized snapshots/blobs (or move this responsibility upward if no longer needed).
  * In progress: lazy-provider error gates were removed and lazy detection now short-circuits false.
* [ ] Checkpoint: no `unlazy`, no lease-driven lazy pull-on-read, no stargz-specific logic remains.

### Phase 4: Remove internal parent-ref lifecycle graph and equal-ref compatibility

* [~] Delete parent graph ownership from refs:
  * [~] remove `parentRefs`, `diffParents`, recursive parent `release`/`clone`
    * Recursive parent release/clone behavior is removed; `parentRefs.release` is now a no-op and `clone` is shallow.
  * [ ] remove metadata parent encoding (`parent`, `mergeParents`, diff parent keys)
* [~] Delete dual-representation compatibility fields:
  * [ ] remove `equalMutable`, `equalImmutable`
  * [~] remove all paths that special-case them during remove/release/get
    * Materialization no longer creates mutable<->immutable sibling links.
    * manager get/getMutable and ref release paths no longer special-case equal mutable/immutable pairing.
* [x] Enforce simple lifecycle:
  * [x] mutable ref owns one snapshot id
  * [x] commit returns immutable ref for that committed snapshot
  * [x] no new mutable<->immutable “same data sibling” tracking in commit path
* [ ] Adjust `Merge`/`Diff` implementation strategy to avoid internal parent ownership assumptions:
  * [ ] either materialize explicit snapshots immediately
  * [ ] or push these operations upward if unnecessary in new model
* [~] Checkpoint: ref lifecycle is becoming linear/explicit; remaining equal-field struct cleanup + merge/diff parent metadata cleanup still pending.

### Phase 5: Final package trim and naming cleanup

* [~] Remove now-unused files and options after previous cutovers.
  * [x] removed snapshots `Root` option/field (leftover from prune path)
  * [x] removed unused `DescHandlerKey`
  * [x] removed `engine/snapshots/remotecache/*` package subtree
* [x] Remove any remaining imports of BuildKit cache-manager-era helpers that are no longer relevant.
  * direct import grep confirms no non-internal package imports `internal/buildkit/cache`.
* [~] Ensure package comments and type names describe snapshots only (not cache manager semantics).
  * in progress: major API surface now snapshot-focused, but some compatibility names/comments still remain (`cacheRecord`, etc.).
* [x] Re-run direct-import audit:
  * [x] verify no reintroduced dependency on `internal/buildkit/cache`
  * [x] verify only required `internal/buildkit/solver/*` subpackages remain (current external usage is in `pb`, `result`, and selected provenance/errdefs paths)
* [~] Checkpoint: `engine/snapshots` is significantly smaller and more cohesive; final naming/field cleanup remains.

## Validation plan

* [ ] Compile gates after each phase:
  * [ ] `go test ./engine/snapshots/... -run TestDoesNotExist -count=1`
  * [ ] `go test ./engine/buildkit/... -run TestDoesNotExist -count=1`
  * [ ] `go test ./core/... -run TestDoesNotExist -count=1`
  * [ ] `go test ./engine/server/... -run TestDoesNotExist -count=1`
* [ ] Integration smoke after major phases:
  * [ ] `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestDirectory|TestContainer' --count=1`
* [ ] Explicitly watch for regressions in:
  * [ ] cache volume mount behavior through `withExec`
  * [ ] image export paths that still depend on snapshot remotes
  * [ ] service startup paths using snapshot refs

## Open questions to resolve during implementation

* [ ] Do we still need `Merge`/`Diff` in snapshot manager at all for current callsites, or can those be removed in favor of higher-level APIs?
* [ ] Which metadata fields must remain in-memory on refs for current UX/debug behavior (description/record type/etc.) versus can be deleted immediately?
* [ ] For image export, should remote-descriptor construction stay in `engine/snapshots` or move entirely to exporter-side code now that lazy handling is gone?

## Answered decisions

* `Merge` / `Diff`: keep them for now; still expected to be used.
* Metadata fields in memory:
  * Keep straightforward fields where easy.
  * If a field is difficult to preserve, verify whether it has active callsites; remove if unused.
  * If used but hard to preserve in the current pass, leave a TODO and keep moving.
* Image export remote descriptor construction:
  * Bias toward moving to exporter-side construction if practical during cleanup.
  * If unexpected complexity shows up, it is acceptable to leave it in `engine/snapshots` for now.

# Size Accounting

## Goals

* Replace placeholder `estimateSharedResultSizeBytes` with real snapshot-backed size accounting.
* Compute size only when it is useful for pruning decisions (avoid eager/always-on cost).
* Include cache-volume snapshots (mutable refs) in accounting.
* Prevent overestimation by deduplicating shared snapshots by snapshot record identity as part of first pass.
* Keep implementation cohesive with current dagql e-graph retention/liveness model.

## Design constraints

* No fallback to synthetic constant estimates once real sizing is available.
* Do not force expensive lazy evaluation purely for metrics.
  * If a result does not currently expose a concrete snapshot without additional compute, leave size unknown until it does.
* Dedupe by underlying snapshot record identity is mandatory in initial implementation.
  * This is required to avoid counting same bytes multiple times when multiple retained results reference the same snapshot.
* Mutable cache volume sizes must be represented and refreshed when they can change.
* First cut should prioritize correctness and determinism over micro-optimizations.

## Notes

* Current `dagql` usage entries are fed by `sharedResult.sizeEstimateBytes`, which is currently set from a placeholder.
* Real size implementation already exists in snapshot manager (`cacheRecord.size`) and persists in-memory metadata size.
* Persisted/retained graph behavior currently uses `depOfPersistedResult` + transitive `deps`.
* Prune-relevant candidates are retained results that are not actively used and not required by active retained roots.
* Cache-volume snapshots are created eagerly in `query.cacheVolume` and mounted into `withExec` as mutable refs; they must be included in size accounting.

## Plan

### Phase 0: Plumbing and interfaces

* [x] Add a small snapshot size access seam in `engine/snapshots` so callers can request real size from refs.
  * [x] Expose a method callable on immutable and mutable refs that returns the underlying `cacheRecord.size(ctx)`.
  * [x] Preserve existing internal metadata update behavior (`queueSize` / commit).
* [x] Add a dagql-side size provider hook for cached payloads.
  * [x] Introduce an internal interface implemented by cacheable core objects that can report snapshot-backed usage size.
  * [x] Implement it for `Directory`, `File`, and `CacheVolume`.
  * [x] `Container` remains aggregate-only (it should not pretend to own a single snapshot size).
* [x] Replace placeholder callsite in `dagql/cache.go` with provider-based sizing path.
  * [x] Keep unknown as explicit state until first real measurement.

### Phase 1: Prune-candidate gated measurement + mandatory dedupe

* [ ] Define prune-relevant candidate set in dagql cache state.
  * [ ] Candidate must be retained (`depOfPersistedResult` true).
  * [ ] Candidate must not be actively used (`refCount == 0`).
  * [ ] Candidate must not be transitively depended on by any actively used retained result.
* [ ] Add transitive active-dependency closure helper under `egraphMu` to compute exclusion set.
* [ ] Compute real size only for candidates with unknown/stale size.
* [ ] Implement dedupe by snapshot record identity as part of first pass.
  * [ ] For each usage-accounting pass, aggregate bytes by snapshot record ID instead of summing per-result blindly.
  * [ ] If multiple results point at same snapshot record, count it once.
  * [ ] Ensure deterministic tie-break/ownership when mapping deduped bytes back to entries.
* [ ] Ensure `UsageEntries` and prune accounting use the deduped numbers.

### Phase 2: Mutable cache-volume correctness

* [ ] Ensure mutable cache-volume refs refresh/invalidate size when writes can change content.
  * [ ] Invalidate cached size on write paths where mutable content changes are committed/applied.
  * [ ] Recompute lazily on next prune-candidate measurement.
* [ ] Confirm cache-volume entries participate in dedupe with other snapshot-backed entries.
* [ ] Confirm repeated runs do not leak stale size metadata for frequently-mutated cache volumes.

### Phase 3: Integration and polish

* [ ] Remove `estimateSharedResultSizeBytes` placeholder function and dead comments.
* [ ] Add explicit comments near size-gating logic describing why we only size prune-relevant candidates.
* [ ] Add explicit comments near dedupe logic documenting snapshot-record-level accounting choice.
* [ ] Keep behavior deterministic across runs (entry ordering + dedupe ownership stable).

## Validation plan

* [ ] Unit tests in `dagql/cache_test.go`:
  * [ ] retained entry size is populated from real sizing path (not constant placeholder).
  * [ ] non-candidate retained entries do not trigger size calculation.
  * [ ] dedupe test: two retained results sharing one snapshot record report one logical byte budget in prune accounting.
  * [ ] mutable cache-volume mutation test: size refreshes after content change.
* [ ] Integration smoke:
  * [ ] `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestEngine/TestLocalCache|TestContainer/TestWithMountedCache|TestContainer/TestLoadSaveNone' --count=1`
* [ ] Re-run key cache persistence tests to ensure no regressions from size-path changes.

## Open questions

* [ ] Best ownership model for mapping deduped snapshot bytes onto per-entry display fields when many entries share one snapshot.
  * Keep deterministic and simple in first pass; revisit UX refinements later.
* [ ] Whether to split usage view into:
  * [ ] per-entry logical view
  * [ ] global deduped physical view
  * (not required for first pass, but may help explain accounting to users)
