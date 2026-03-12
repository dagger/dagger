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

## Persistence Redesign (Current Iteration)

* **THIS IS A NEW ITERATION OF THE PERSISTENCE DESIGN. PREVIOUS PERSISTENCE DESIGNS IN THIS FILE ARE OUTDATED AND SHOULD BE TREATED AS HISTORICAL CONTEXT ONLY.**
  * In particular, older designs that avoided engine-local integer IDs in persisted state are no longer the target.
  * The new goal is to persist a simple, direct reflection of the in-memory dagql cache / e-graph state so startup can reconstruct that state with minimal translation and minimal policy-specific cleverness.

* New direction:
  * persist engine-local numeric IDs directly for results / terms / eq classes
  * make the DB schema mirror the in-memory structures as directly as possible
  * prioritize simple export/import of current in-memory state over hypothetical future cross-engine import portability
  * keep the lazy envelope/self rehydration wrinkle only where truly required by live server/object decoding

* Design intent:
  * when the engine shuts down cleanly, persistence should flush enough state that startup can recreate the same in-memory cache / e-graph structure and metadata that existed before shutdown
  * avoid convoluted alternate identities / translation layers unless they are strictly required by the current in-memory model

### Clarification

* Persist the full current in-memory completed cache / e-graph state, not just a retained subset.
  * During shutdown, clients disconnect and sessions close first.
  * That session-close path may prune non-retained results before the final flush.
  * That is fine and desirable: the DB should mirror whatever state is still present after those in-memory retention rules run.
* There is only one base dagql cache in the engine.
  * Session caches are wrappers around that one base cache; they are not separate persistence graphs.
  * Do not invent "different cache" identity checks inside persistence/export logic unless we explicitly redesign around multiple base caches.
* Pointer equality checks are an extreme code smell and very rarely the right answer.
  * In particular, using pointer equality as a proxy for semantic identity in cache/persistence code is almost always broken and should be treated with suspicion by default.
* `IsPersistable` still matters for in-memory retention across session close.
  * It no longer needs to directly gate whether some surviving state is or is not written to the DB.
  * The DB mirrors the surviving state; it does not make an independent policy decision.
* Persist only completed state.
  * Do not attempt to mirror `ongoingCalls`, `ongoingArbitraryCalls`, or other in-flight state.
* Hard-cut schema handling:
  * if the stored schema version does not match the new schema version, wipe the DB and cold-start
  * do not write migration or backward-compatibility code for the old persistence schema

### Authoritative In-Memory State To Mirror

* Persist only authoritative in-memory state, not reverse/derived indexes.
* Target authoritative structures:
  * `resultsByID`
  * `egraphTerms`
  * term input eq-class lists + per-slot provenance
  * eq-class digest membership + digest labels
  * `resultOutputEqClasses`
  * explicit `sharedResult.deps`
  * persisted snapshot links
  * cache usage / prune metadata that already lives on `sharedResult`
* Do not persist derived indexes:
  * `egraphDigestToClass`
  * `eqClassToDigests` can be rebuilt from persisted eq-class digest rows
  * `inputEqClassToTerms`
  * `outputEqClassToTerms`
  * `egraphResultsByDigest`
  * `egraphTermsByTermDigest`
  * union-find parent/rank arrays

### SharedResult Cleanup

* Remove these persistence-era identity fields from in-memory `sharedResult`:
  * `persistedResultKey`
  * `idDigest`
  * `originalRequestID`
  * `outputDigest`
  * `outputExtraDigests`
* Also remove root-export bookkeeping from `sharedResult`:
  * `pendingPersistExport`
  * `pendingPersistWatchResults`
* Keep for now:
  * `outputEffectIDs`
  * `persistedEnvelope` / lazy decode wrinkle
  * `depOfPersistedResult` or whatever we rename that retention bit to during cleanup
* Important wrinkle:
  * removing `originalRequestID` does NOT remove the need for one canonical caller-facing ID per result for persistence / lazy rehydration
  * simplest likely replacement is a separate cache-owned `resultCanonicalIDs map[sharedResultID]*call.ID` (or equivalent)
  * strongly consider mirroring that directly in the DB as a `canonical_id` column on `results`
  * current implementation choice: `canonical_id` is the first materializing request ID for a shared result, i.e. the direct replacement for the old `originalRequestID` semantics

### Schema Target

* Keep `meta` for schema version / clean shutdown marker.
* Replace current persistence tables with a schema that mirrors in-memory state directly:
  * `results`
    * `id INTEGER PRIMARY KEY`
    * `canonical_id TEXT NOT NULL`
    * `self_payload BLOB NOT NULL`
    * `output_effect_ids_json TEXT NOT NULL DEFAULT '[]'`
    * `safe_to_persist_cache INTEGER NOT NULL`
    * `dep_of_persisted_result INTEGER NOT NULL`
    * `expires_at_unix INTEGER NOT NULL`
    * `created_at_unix_nano INTEGER NOT NULL`
    * `last_used_at_unix_nano INTEGER NOT NULL`
    * `size_estimate_bytes INTEGER NOT NULL`
    * `usage_identity TEXT NOT NULL`
    * `record_type TEXT NOT NULL`
    * `description TEXT NOT NULL`
  * `terms`
    * `id INTEGER PRIMARY KEY`
    * `self_digest TEXT NOT NULL`
    * `term_digest TEXT NOT NULL`
    * `output_eq_class_id INTEGER NOT NULL`
  * `term_inputs`
    * `term_id INTEGER NOT NULL`
    * `position INTEGER NOT NULL`
    * `input_eq_class_id INTEGER NOT NULL`
    * `provenance_kind TEXT NOT NULL`
    * primary key `(term_id, position)`
  * `eq_classes`
    * `id INTEGER PRIMARY KEY`
    * no extra metadata required initially, but useful as the authoritative ID anchor and future extension point
  * `eq_class_digests`
    * `eq_class_id INTEGER NOT NULL`
    * `digest TEXT NOT NULL`
    * `label TEXT NOT NULL DEFAULT ''`
    * primary key `(eq_class_id, digest, label)`
  * `result_output_eq_classes`
    * `result_id INTEGER NOT NULL`
    * `eq_class_id INTEGER NOT NULL`
    * primary key `(result_id, eq_class_id)`
  * `result_deps`
    * `parent_result_id INTEGER NOT NULL`
    * `dep_result_id INTEGER NOT NULL`
    * primary key `(parent_result_id, dep_result_id)`
  * `result_snapshot_links`
    * `result_id INTEGER NOT NULL`
    * `ref_key TEXT NOT NULL`
    * `role TEXT NOT NULL`
    * `slot TEXT NOT NULL DEFAULT ''`
    * primary key `(result_id, ref_key, role, slot)`
* Remove obsolete tables:
  * `roots`
  * `root_members`
  * old `result_terms` subordinate-row model
* Note on `eq_classes`:
  * yes, keep an explicit table even if it initially stores only the ID
  * that gives us an authoritative source of eq-class IDs, a clean FK anchor, and a simple way to restore `nextEgraphClassID = max(id)+1`

### Export / Worker Rewrite

* Replace the current root-scoped upsert/tombstone model entirely.
* Preferred first cut:
  * worker payload is a full-state persistence snapshot, not a per-root closure batch
  * mutations still enqueue persistence work, but the queue may coalesce to "latest full snapshot wins"
  * shutdown does a final synchronous flush of the latest full snapshot
* New snapshot builder should capture the authoritative in-memory state listed above:
  * all results
  * all terms
  * all term inputs + provenance
  * all eq classes
  * all eq-class digests/labels
  * all result-output-eq-class rows
  * all result deps
  * all result snapshot links
  * all canonical result IDs
* Worker apply path should run one transaction that:
  * clears mirror tables
  * rewrites them from the snapshot
  * updates clean-shutdown metadata as today
* Hard cut goals for code deletion:
  * delete `emitPersistUpsertForRootLocked`
  * delete `emitPersistTombstonesForRootLocked`
  * delete `snapshotPersistUpsertForRootLocked`
  * delete root-member tombstone / closure-member bookkeeping
  * delete `resultsByPersistKey` and old persist-result-key helpers if no longer needed

### Import Rewrite

* Startup import should read the mirror tables and reconstruct the same in-memory state.
* Recreate authoritative state first:
  * allocate results by persisted integer ID
  * restore canonical IDs
  * restore envelopes / eager-decoded payloads where possible
  * restore terms
  * restore term input eq IDs + provenance
  * restore result-output-eq-class rows
  * restore explicit deps
  * restore snapshot links
  * restore eq-class digest membership / labels
* Then rebuild derived indexes:
  * `egraphDigestToClass`
  * `eqClassToDigests`
  * `eqClassExtraDigests`
  * `inputEqClassToTerms`
  * `outputEqClassToTerms`
  * `egraphTermsByTermDigest`
  * `egraphResultsByDigest`
* Reinitialize union-find as trivial singleton roots for each persisted eq class ID.
  * no need to persist parent/rank state
* Restore next-ID counters as:
  * `nextSharedResultID = max(result.id)+1`
  * `nextEgraphTermID = max(term.id)+1`
  * `nextEgraphClassID = max(eq_class.id)+1`

### Debug / Introspection / Tests

* Update debug snapshot output to mirror the new authoritative structures and remove references to obsolete persisted keys / root closure concepts.
* Update persistence tests to assert:
  * restart recreates the same in-memory state shape
  * integer IDs are restored and next-ID counters advance from max+1
  * session-close pruning before shutdown results in the DB mirroring only the surviving in-memory state
  * eq-class digest labels and result-output-eq-class mappings survive restart
  * lazy envelope decode still works after import

### Suggested Implementation Order

1. Introduce the new schema side-by-side in code and bump schema version.
2. Introduce/settle the in-memory replacement for `originalRequestID` / persisted result identity (likely `resultCanonicalIDs`).
3. Rewrite import to target the new schema and reconstruct authoritative state + derived indexes.
4. Rewrite snapshot/export worker to emit a full-state mirror snapshot and apply via replace-all transaction.
5. Delete roots/root_members/tombstones/root-closure worker code.
6. Remove old persistence-era fields from `sharedResult` and associated helper/index code.
7. Update debug/test surfaces and verify restart reproduces pre-shutdown in-memory state.

# Pruning support

## Plan

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

## NOTES

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

## Implementation

### Scope and constraints

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

### Phase 1: Dagql prune metadata and usage accounting

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

### Phase 2: Dagql-native prune policy model + option resolution

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

### Phase 3: Dagql prune execution engine

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
* [x] Add explicit support for resolver-produced additional output results so detached results do not leak past resolver completion.
  * [x] Introduce a dagql interface for values that return additional resolver outputs that must be attached to cache once the main result is attached.
    * [x] Proposed shape: `AdditionalOutputResults() []AnyResult` (name can adjust, behavior should not).
    * [x] Additional outputs must be pure handles only; this method must never evaluate or materialize lazy values.
  * [x] Wire `GetOrInitCall` completion path to attach additional outputs immediately after main result indexing/attachment (same request lifecycle, not deferred/post-call).
    * [x] Do this after `initCompletedResult` has attached/indexed the parent result.
    * [x] For each additional output:
      * [x] Attach/reacquire via normal call-cache path using its existing ID (no synthetic IDs), so detached outputs become attached and cached outputs are ref-acquired consistently.
      * [x] Preserve laziness; attachment should not force output evaluation.
  * [x] Record explicit dependency edges from parent result -> attached additional outputs.
    * [x] This is required so persisted parent liveness retains output snapshots transitively.
    * [x] Do not rely only on term-input dependency indexing (that models child -> parent but not parent -> produced outputs).
  * [x] Ensure this flow works for `Container.withExec` outputs specifically:
    * [x] rootfs output directory result
    * [x] writable mount output directory/file results
    * [x] meta output directory result
  * [x] Keep hard-cutover behavior:
    * [x] no use of `PostCall` for this lifecycle step
    * [x] no fallback to container-intrinsic size accounting
    * [x] size accounting remains on snapshot-bearing types (Directory/File/CacheVolume)
  * [x] Add focused dagql tests for the new attachment semantics:
    * [x] detached additional outputs become attached after parent attach
    * [x] attached additional outputs remain lazy until evaluated
    * [x] parent result retains attached output deps in persisted-liveness traversal
    * [x] accounting/prune sees output-backed size through attached outputs (without container size hooks)
  * [x] Add integration assertions around `core/integration/localcache_test.go` scenarios that previously under-counted bytes.
* [x] REVIEW CHECKPOINT 3: review prune semantics and invariants before server API switch.

### Phase 4: Server integration and entrypoint cutover

* [x] Switch `EngineLocalCacheEntries` from buildkit `DiskUsage` to dagql usage snapshot.
* [x] Switch `PruneEngineLocalCacheEntries` from `baseWorker.Prune` to dagql prune execution.
* [x] Keep existing `gcmu` serialization around explicit prune.
* [x] Switch `gc()` to run dagql prune using default policy list.
* [x] Remove buildkit-specific side effects from prune flow where no longer applicable:
  * [x] `imageutil.CancelCacheLeases()` path
  * [x] `SolverCache.ReleaseUnreferenced(...)` post-prune path
* [x] Keep return type `EngineCacheEntrySet` populated from dagql prune results.
* [x] REVIEW CHECKPOINT 4: verify server-level behavior (automatic gc + manual prune) before API cleanup.

### Phase 5: API cleanup and compatibility polish

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
* [x] REVIEW CHECKPOINT 5: ensure interface coherence and no lingering buildkit-prune assumptions.

### Phase 6: Follow-up tasks (non-blocking for first cut)

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

## Goals

* Hard-cut the copied BuildKit cache-manager behavior out of `engine/snapshots`.
* Keep only minimal snapshot lifecycle functionality needed by current engine/core callsites.
* Move lifecycle/dependency/persistence/pruning responsibility out of snapshot manager and into dagql/core (where we already model object dependencies and retention).
* End state should look like a purpose-built Dagger snapshot primitive, not a partially-adapted BuildKit cache system.

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

## Open questions

* [ ] Best ownership model for mapping deduped snapshot bytes onto per-entry display fields when many entries share one snapshot.
  * Keep deterministic and simple in first pass; revisit UX refinements later.
* [ ] Whether to split usage view into:
  * [ ] per-entry logical view
  * [ ] global deduped physical view
  * (not required for first pass, but may help explain accounting to users)
* [ ] Consider measuring all snapshot sizes (including non-prunable) to support accurate engine total disk-usage visibility.
  * Verify first whether this is actually needed and desired as a product/system goal before implementing.

# Persistence Epic

## Persistence Notes (applies across all passes, updated as needed)

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

## Design (first pass)

### Core model

* In-memory dagql cache remains the single source of truth for all cache logic.
* Persistence is asynchronous and event-driven; cache mutations never synchronously write to disk.
* Persistence is represented as export/import of a portable DAG/e-graph shape (not engine-process-local numeric IDs).
* Persisted state is rebuilt into in-memory structures on engine startup before serving requests.

### Runtime architecture

* `PersistenceEmitter` (inside dagql cache mutation paths):
  * observes cache mutations that affect persistable state.
  * emits normalized persistence events after in-memory mutation is committed.
* `PersistenceQueue`:
  * unbounded queue of persistence events (first cut).
  * no drop policy in first cut.
  * feeds a separate persistence worker goroutine.
* `PersistenceWorker` (initially a goroutine, future remote service candidate):
  * drains queue.
  * first cut: no explicit coalescing/compaction optimization.
  * writes durable records to persistent storage.
* `PersistenceStore`:
  * direct insert/upsert/delete/read primitives for persisted cache records.
  * no append-only event log in first cut.
  * initial backend target: SQLite (WAL mode).
  * storage engine can change later without changing in-memory cache behavior.

### Decisions from review

* Graceful shutdown semantics are strict:
  * on SIGTERM/SIGINT (or any graceful shutdown), queue must be fully drained and worker writes fully synced before process exit.
* Ungraceful shutdown handling:
  * startup detects ungraceful previous shutdown.
  * if detected, persistence store is treated as untrusted and deleted/reset; rebuild from empty state.
* Startup import behavior:
  * only full rebuild is supported; no partial/degraded import mode.
* Tombstones:
  * prune/removal paths emit tombstones to persistence store.
* Last-used persistence:
  * do not persist last-used at all in first cut.
* Graceful shutdown wait policy:
  * wait indefinitely for queue drain + worker sync completion.
* Queue/write optimization:
  * skip coalescing/compaction optimizations for now; rely on direct SQLite upsert/delete behavior.
* Ungraceful-shutdown marker choice (tentative implementation direction):
  * store marker in SQLite metadata table (single source of truth, no extra sidecar file).
* Startup import robustness (tentative implementation direction):
  * load via one consistent SQLite read transaction/snapshot and rebuild in-memory from that.
* Persisted scope:
  * persist roots produced by persistable fields.
  * also persist all direct/transitive dependencies required to materialize those roots.
  * function-call result persistence must include anything referenced by the function-call result.
* `sharedResult` persistence must include `self` payload/state (not just graph metadata).
* Persistable type requirement:
  * each persistable type (directory/file/cachevolume/container/function-returnable objects, etc.) must implement serialize/deserialize support.
  * serialization must include enough snapshot metadata to restore mutable/immutable refs (snapshotter/content-store/lease integration shape).
* E-graph rebuild strategy:
  * avoid depending on engine-local numeric IDs as durable keys.
* Unsafe dependency policy:
  * if a persisted closure includes unsafe deps, persist anyway for now and emit warnings.
* Import failure policy:
  * if import is malformed/corrupt, wipe persistence store and continue startup from empty state.
* `self` persistence interface:
  * use one shared interface for all persistable `self` payloads.
* `self` payload storage format:
  * use opaque payload bytes + type discriminator in first cut.
* Worker write strategy:
  * allow batched writes with sane size/time defaults (tunable), while preserving graceful-shutdown full-drain guarantee.
* Ungraceful marker implementation:
  * strict/simple SQLite metadata row toggle.

## Persistence Cleanup Feedback (feedback from reviewing first implementation)

* Reassess the DB model entirely.
  * It is not necessarily true that the goal should be a byte-for-byte / field-for-field persisted form of the exact in-memory e-graph.
  * There is a legitimate longer-term goal of having a persisted/exported cache format that is engine-independent enough that one engine could export and another engine could import into its own cache.
  * That means process-local/runtime-specific identifiers like incrementing in-memory int IDs are probably not the right long-term persisted identity surface.
  * However, that does **not** excuse the current weirdness; if anything it raises the bar for the persisted model being coherent and self-sufficient.
  * Right now the design feels stuck in an awkward middle ground:
    * we are not persisting the in-memory e-graph directly
    * but we are also not persisting one especially clean engine-independent model either
  * Current weirdness to revisit as part of that DB-model reassessment:
    * we persist a mix of symbolic fragments (`terms`, `term_results`, `eq_facts`), materialized rows (`results`), explicit edges (`deps`), and snapshot links, then replay merge/index logic on import
    * `terms.input_digests_json` stores representative digests for input eq classes rather than persisting a true eq-class / union-find state
    * `eq_facts` is owner-scoped and closure-filtered, which is a strange middle ground instead of a clean statement of equivalence state
    * `results` is overloaded with durable request identity, output identity, payload serialization, retention flags, TTL, and prune metadata all in one table
    * import/export is asymmetrical: export writes a self payload, but import only partially reconstructs state and still leaves object payloads in lazy `persistedEnvelope` form
    * `snapshot_refs` currently looks under-modeled / placeholder-ish, with the key being the only obviously meaningful part
    * export writes explicit `deps`, but import still recomputes more dependency edges from term inputs, which suggests the persisted representation is not actually self-sufficient
    * `buildPersistUpsertBatchForRootLocked(...)` has become the real persistence brain, which concentrates too much semantics in one confusing place
  * Cleanup target: decide what the durable model actually is supposed to mean first, then make both export and import faithfully implement *that* model rather than accreting more special-case reconstruction logic.

* Runtime cache hits should not lazily load persisted payloads.
  * `ensurePersistedHitValueLoaded(...)` being called from the steady-state cache-hit path is the wrong model.
  * Import happens at engine startup; by the time normal runtime cache lookup is serving hits, persisted state should already be fully imported/reconstructed.
  * Cleanup target: remove the runtime hit-time "load persisted value now" behavior and make startup import own the whole rehydration boundary explicitly.
  * Important caveat for staging: making import fully eager is trickier than it sounds because many persisted decoders currently depend on having a dagql server available.
  * Module-object-shaped persisted values are especially awkward because decoding them may require module-specific schema already being installed on the server, which creates a real bootstrap/catch-22 problem.
  * So "full eager import at startup" is still the goal, but it likely requires an explicit import-time bootstrap/server/schema plan rather than just moving the current lazy decode call to an earlier place.
* The cache completion + persistence setup flow in `dagql/cache.go` around the indexing/export path is far too convoluted.
  * Persistability checks and persisted-dependency propagation are currently spread across multiple places, making the actual behavior hard to understand and likely hiding incorrect or inefficient work.
  * In particular, `markResultAsDepOfPersistedLocked(...)` and related persistence-side effects currently appear from too many call sites, which makes it unclear what the single source of truth is for "this result is now part of persisted closure".
  * Cleanup target: collapse this into one explicit, easy-to-follow flow for:
    * indexing the completed result into the e-graph
    * deciding whether the result is persistable
    * computing/marking the persisted closure exactly once
    * emitting persistence/export work from one clearly-owned place
  * Strong suspicion: the current shape is at best inefficient and at worst semantically wrong/confused, so this area needs a serious redesign rather than incremental cleanup.
* Naming cleanup is needed around the many different things currently called `ID`.
  * Example: `sharedResultID` is a totally different kind of identity than `call.ID`, but the current naming makes that much harder to see than it should be.
  * Lower priority than the semantic cleanup above, but still worth fixing because it actively obscures reasoning about the system.
* `indexWaitResultInEgraphLocked(...)` should return an error instead of silently swallowing failure cases.
  * In particular, places like the `derivePersistResultKey(...)` call during persist-key indexing should not just ignore errors and move on.
  * Cleanup target: make indexing/reporting failures explicit and return them to the caller so persistence/index integrity issues fail loudly instead of disappearing into partial state.
* `markResultAsDepOfPersistedLocked(...)` being called at the end of `indexWaitResultInEgraphLocked(...)` is not self-explanatory and needs justification or removal.
  * Right now this looks like persistence-side closure propagation is being opportunistically retriggered from deep inside indexing, which makes the control flow hard to follow.
  * Cleanup target: either eliminate this call as part of the broader persistence-flow simplification, or document the exact invariant it is repairing and why that repair belongs here rather than in one explicit persistence owner.
* `emitPersistUpsertForRootLocked(...)` firing from the cache-hit upgrade path also needs reassessment.
  * The current behavior of "mark this hit as dep-of-persisted and immediately emit a persist upsert for the root from deep inside lookup/indexing-related flow" feels suspicious and needs a harder look.
  * Cleanup target: revisit whether persist-upsert emission has a single coherent owner, or whether we are currently smearing persistence-side writes across too many opportunistic paths.
* In `sharedResult`, the `deps`, `heldDependencyResults`, and `persistedSnapshotLinks` fields feel overlap-y and need a dedicated ownership/lifecycle pass.
  * It is not currently clear which of these are truly fundamental versus artifacts of layering multiple experiments and hacks on top of each other.
  * Cleanup target: decide what concepts actually need to exist on `sharedResult`, what should be derived, and what should be owned elsewhere.
* Lazy persisted-envelope decode on first cache hit should go away along with the broader hit-time import behavior.
  * The whole import/reconstruction boundary should happen up front at engine startup rather than partially/lazily on the first runtime hit.
  * Cleanup target: remove the `persistedEnvelope` lazy-hit path as part of the same import-up-front redesign.
* `persistedClosureGraphLocked(...)` and related closure logic currently choosing only the first live result for an output eq class has a slightly suspicious smell and should be reassessed.
  * It may be okay, but it is worth double-checking whether evolving/pruned graphs can make that choice inconsistent or brittle over time.
  * Cleanup target: verify whether "first live result for output eq class" is actually a sound closure rule or just a convenient heuristic that happened to work so far.
* `attachDependencyResults(...)` / `loadDependencyResults(...)` currently look especially ugly and overloaded.
  * That path is not just recording symbolic dependency metadata; it is also doing live dependency materialization (`lookupCacheForID(...)` and even `srv.LoadType(...)`), validating cache-backed attachment, mutating explicit dependency edges, propagating persisted-liveness state, and taking over ref ownership via `heldDependencyResults`.
  * Cleanup target: split apart the symbolic dependency model from live ref-management and persistence-side bookkeeping so result completion is not secretly doing a pile of dynamic graph work through one hard-to-follow helper.

## Persistence Cleanup Implementation

### Phase 1: Remove persisted-object-resolver context plumbing

#### Goal

* Remove `ContextWithPersistedObjectResolver(...)` / `currentPersistedObjectResolver(...)` entirely.
* Keep current lazy persisted decode behavior for now; this phase is only about making the resolver/decode state explicit instead of smuggling it through `context.Context`.

#### Non-goals

* Do **not** solve the full eager-import-at-startup problem in this phase.
* Do **not** redesign the DB model in this phase.
* Do **not** change when lazy persisted payload decode happens yet.
* Do **not** try to solve the module-object/schema bootstrap problem yet beyond keeping the new API shape compatible with solving it later.

#### Implementation plan

* Change the persisted-result decode API so persisted object resolution is passed explicitly instead of being recovered from `context.Context`.
  * The cleanest first pass is likely to thread `PersistedObjectResolver` as an explicit argument through `PersistedSelfCodec.DecodeResult(...)` and the lower-level decode helpers, since object decoders already accept an explicit resolver today.
* Update all decode call sites to pass the resolver explicitly.
  * This includes the lazy persisted-hit path and any import-side eager decode helpers that currently rely on the context hack.
* Delete the context helper functions and all context writes/reads for persisted object resolution.
  * After this phase there should be no `ContextWithPersistedObjectResolver(...)` or `currentPersistedObjectResolver(...)` left.
* Keep `CurrentDagqlServer(ctx)` usage as-is for now.
  * This phase is specifically about removing the resolver-specific context hack, not about removing all ambient dagql-server context usage in one shot.

#### Desired end state

* Persisted decode paths are still lazy for now, but the data/control dependencies are explicit in function signatures.
* The next phase can then tackle eager startup import/bootstrap separately without also carrying the resolver-context cleanup at the same time.

#### Validation

* `go test ./dagql -run 'TestPersistedSelfCodec|TestCachePersistenceImport' -count=1`
* `go test ./dagql -run '^$' -count=1`
* Grep check that no persisted-object-resolver context helper usages remain.

### One-go hard cut: exact structural input refs + explicit exportability

#### Goal

* Replace the current lossy `SelfDigestAndInputs() -> []digest.Digest -> try to recover proof inputs later` flow with an explicit structural input model that never throws away the distinction between:
  * real call IDs
  * digest-only inputs
* Remove all fallback behavior when deciding whether a proof input is represented as a result ref or a digest ref.
* Make persistence observe runtime state transitions instead of causing them:
  * persistable values may remain lazy in memory
  * export must not force materialization
  * export may only serialize values that are already explicitly exportable
  * lazy but explicit/symbolic state is okay to export
  * opaque lazy state is not exportable and must trigger deferred retry, not forced `Sync`

#### Non-goals

* Do **not** reintroduce `Sync`/`PreparePersistedObject` forcing.
* Do **not** add compatibility shims for the old DB model.
* Do **not** keep the old proof-capture path around in parallel.
* Do **not** silently downgrade unresolved result-backed proof inputs to digest refs.
* Do **not** solve full eager payload import/bootstrap in the same pass.

#### Core design decisions

* `SelfDigestAndInputs()` should stop returning only raw digests.
* Replace it with an API that returns:
  * the same `selfDigest`
  * an ordered slice of input refs
* Each input ref must preserve what kind of structural input it is:
  * receiver call ID
  * ID literal argument
  * module call ID
  * digest-only literal (`LiteralDigestedString`)
* Downstream code is then responsible for explicitly deciding how each kind is persisted:
  * receiver / ID literal / module input => result-backed structural input, must resolve to a real cached result or error
  * digest-only literal => digest-backed structural input
* There is no fallback path from result-backed input to digest-backed input.

#### New call-layer shape

* Add a new ordered input-ref representation in `dagql/call`, something like:
  * `type StructuralInputRef struct { ID *ID; Digest digest.Digest }`
  * exactly one of `ID` or `Digest` must be set
* Add a new helper on `call.ID` that returns:
  * `selfDigest`
  * `[]StructuralInputRef`
* The order must exactly match the current `SelfDigestAndInputs()` input order so term digest semantics do not change.
* Explicit required behavior for that helper:
  * receiver => `ID`
  * `LiteralID` => `ID`
  * module input => `ID`
  * `LiteralDigestedString` => `Digest`
* Keep `SelfDigestAndInputs()` only if truly needed elsewhere, but it should become a simple projection from the richer API rather than the source of truth.

#### New proof-capture rules

* Replace `exactProofInputsForDigestsLocked(...)` with a helper that consumes the new ordered structural input refs directly.
* For each structural input ref:
  * if it is `ID`, resolve it to the actual cached `sharedResult`
    * if resolution fails, return an error immediately
    * do **not** try another lookup surface and do **not** fall back to digest
  * if it is `Digest`, emit a digest proof input directly
* The result of that helper is the exact proof witness list stored on `egraphResultTermAssoc`.
* `egraphResultTermAssoc` remains the per-result/per-term metadata carrier because the proof belongs to the association, not to the term alone and not to the result alone.

#### New e-graph association semantics

* Keep `egraphResultsByTermID` as the reverse lookup index for runtime hits.
* Keep `egraphTermIDsByResult`, but as the richer association map carrying exact proof inputs.
* Stop using `sharedResult.deps` for structural term proof edges.
  * `deps` is only for explicit non-structural dependencies (attached dependency results, etc.).
* `persistedClosureGraphLocked(...)` and any active-closure walk that needs structural proof must read it from `egraphResultTermAssoc.proofInputs`, not by heuristically picking “first live result for output eq class”.

#### Exportability model

* Export must be side-effect free.
* `EncodePersistedObject(...)` must remain pure.
* Exportability is a property of the current object state, not of the field spec alone.
* Valid export cases:
  * concrete realized state (snapshot-backed, etc.)
  * explicit symbolic lazy state that already has a real serializable form
* Invalid export case:
  * opaque lazy state represented only by runtime `LazyInit` behavior with no explicit serializable form
* Invalid exportability must return `ErrPersistStateNotReady`.
* `ErrPersistStateNotReady` is not a cache/persistence failure; it means “this persistable root is not exportable yet in its current runtime state”.

#### Deferred export model

* Keep the cache-level deferred export approach.
* When a persistable root completes and export hits `ErrPersistStateNotReady`:
  * mark that root as pending persistence
  * compute which closure members are currently not exportable
  * register retry callbacks on those closure members' natural lazy-realization path
* The callback must only:
  * retry persistence for the original root
  * never trigger materialization itself
* De-duplicate retries:
  * one root should not register multiple watchers for the same member
  * repeated natural realizations should not spam repeated exports once persistence succeeds
* Important detail for shared lazy gates like `WithExec`:
  * the root itself may not be the thing that gets evaluated directly
  * so we must continue registering on all non-exportable closure members, not just the root object

#### DB model hard cut

* Keep the new durable model we already cut to:
  * `results`
  * `roots`
  * `root_members`
  * `result_terms`
  * `result_deps`
  * `result_snapshot_links`
  * plus `meta`
* `result_terms` continues to be the durable statement of structural proof for one result/term association.
* `input_refs_json` remains acceptable for this pass if it continues to faithfully encode ordered tagged refs.
  * If we later want a normalized `result_term_inputs` table, that is a separate cleanup, not part of this hard cut.
* No reintroduction of:
  * `eq_facts`
  * `terms`
  * `term_results`
  * `snapshot_refs`

#### Export implementation details

* `buildPersistUpsertBatchForRootLocked(...)` should:
  * walk the persisted closure
  * write one `results` row per closure result
  * write one `root_members` row per closure membership
  * write one `result_terms` row per exact result-term association
  * serialize exact proof inputs from `egraphResultTermAssoc.proofInputs`
  * write explicit non-structural `result_deps`
  * write `result_snapshot_links`
* It should not synthesize any proof from digests at export time.
* It should not “best effort” persist around a bad proof input.
  * unresolved result-backed proof input => error
  * opaque lazy non-exportable member => `ErrPersistStateNotReady`

#### Import implementation details

* Import should:
  * load all durable results first
  * seed digest equivalence from each result's own durable digests
  * load `root_members`
  * load explicit `result_deps`
  * load `result_snapshot_links`
  * load `result_terms`
* For each `result_terms` row:
  * decode ordered tagged refs
  * for `result_ref`:
    * resolve imported result by durable key
    * use its output digest / eq class
    * recreate `egraphProofInput{kind: result, resultID: ...}`
  * for `digest_ref`:
    * ensure eq class for that digest
    * recreate `egraphProofInput{kind: digest, digest: ...}`
* Then:
  * rebuild the imported term
  * rebuild the result-term association with the exact proof inputs
  * rebuild the `egraphResultsByTermID` reverse index
* Import should not heuristically infer structural proof from the live graph.

#### The specific receiver-input bug we are fixing

* Today the receiver-side structural input for things like `Container.withMountedDirectory` is flattened to a raw digest too early.
* Later proof capture tries to reverse-map that digest to a result and may fail.
* The current fallback then silently emits a `digest_ref`, which weakens the persisted proof and causes restart misses.
* After this hard cut:
  * receiver inputs are captured as actual `ID` refs up front
  * proof capture resolves them directly to actual `sharedResult`s
  * unresolved receiver result => immediate error
  * therefore the wrong `digest_ref` can no longer be emitted for that class of bug

#### Required validations

* Focused unit/compile checks while iterating:
  * `go test ./dagql -run '^$' -count=1`
  * `go test ./core -run '^$' -count=1`
  * `go test ./cmd/engine -run '^$' -count=1`
* Focused persistence tests:
  * `go test ./dagql -run 'TestPersistedSelfCodec|TestCachePersistenceImport|TestCachePersistenceWorker|TestCachePersistenceCleanShutdownToggleOnClose|TestCachePersistenceWorkerIdempotentUpsertAndTombstone|TestCachePersistenceWorkerRootDeleteKeepsSharedClosureMembers|TestCachePersistenceWorkerUsesTermGraphWhenDepsAreMissing' -count=1`
* Focused integration repro:
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestEngine/TestDiskPersistenceAcrossRestart/container withExec output on host mount survives restart'`
* Trace expectations for the integration repro:
  * no `persist_batch_build_failed` for lazy directory/file exportability on the path under test
  * no `Host` decode failures
  * `Host.directory` may still be `DONE` on the second run
  * `Container.withMountedDirectory` should become `CACHED` on the second run
  * `Container.withExec` should become `CACHED` on the second run
* If any of the following happen, stop and escalate:
  * a structural input kind is ambiguous (unclear whether it should be `ID` or `Digest`)
  * a receiver / module / literal ID input cannot be resolved to a cached result and it is not obvious whether that is a producer bug or a persistence-timing bug
  * making `SelfDigestAndInputs()` a projection from the richer API would break some call-site semantics in a non-obvious way
  * any producer appears to require a broader shared-lazy-gate abstraction rather than the per-member deferred-export model we already agreed on

### One-go hard cut: on-demand `SelectNth` promotion into standalone cached results

#### Goal

* Keep the current no-fallback proof model intact:
  * structural `ID` inputs must resolve to real cached `sharedResult`s
  * digest-only literals remain `digest_ref`
* Fix the `function cache control survives restart` failure by making `SelectNth`-derived IDs real cache-backed results when they are actually loaded.
* Do this as a runtime identity/lifecycle fix, not as another persistence-model special case.

#### Problem statement

* Today array elements already have real IDs:
  * `DynamicArrayOutput.NthValue(...)` returns `newDetachedResult(enumID.SelectNth(i), t)`
  * module list conversion assigns `itemID := curID.SelectNth(i + 1)` before converting each element
* But `LoadType(...)` currently treats `id.Nth() != 0` as a pure projection:
  * it loads the parent enumerable
  * calls `NthValue(nth)`
  * dereferences the result
  * returns it directly
* That means the nth-element ID may never become a standalone cached `sharedResult`.
* Later proof capture sees a real `StructuralInputRef{ID: ...}` for that nth-element receiver and correctly expects `resolveSharedResultForInputIDLocked(...)` to find a cached result.
* In the failing `InputTypeDef.fields` case, it does not, and indexing errors before term association.

#### Why this is the right fix class

* The right invariant is:
  * if something has a real structural `ID` and can later act as a receiver, it should be resolvable to a real cached result
* That lets us keep the current clean proof rules:
  * `ID` input => `result_ref`
  * digest-only literal => `digest_ref`
  * no fallback
  * no third proof kind
* This is more cohesive than inventing a special persisted `id_ref` just because nth-element receivers are currently projections instead of cache entries.

#### Non-goals

* Do **not** add a third structural proof kind for this pass.
* Do **not** change persistence schema or `result_terms` encoding for this pass.
* Do **not** add a `currentTypeDefs`-specific hack.
* Do **not** eagerly fan out every array result into cached nth children by default.
  * We want on-demand promotion, not unconditional eager explosion.

#### Core implementation shape

* Hard-cut server-side nth materialization so `SelectNth` IDs are always loaded through a real `GetOrInitCall(...)` path with a real initializer.
* This applies to every place the server materializes nth elements, not just `LoadType(...)`.
* The first time an nth-element ID is materialized:
  * it becomes a real cache entry
  * it gets indexed into the e-graph like any other completed call result
* Every later load of that nth-element ID becomes a normal cache hit.
* Downstream proof capture then works unchanged because `resolveSharedResultForInputIDLocked(...)` can now find that nth receiver result.

#### Exact file/line plan

* `dagql/server.go`
  * At `LoadType(...)` around the current `callCtx := srvToContext(idToContext(ctx, id), s)` setup and the cache-probe block, keep the telemetry option construction intact.
  * Add a dedicated nth-promotion path for `id.Receiver() != nil && id.Nth() != 0` before the existing hit-only `cache miss` probe.
  * That path should call the shared nth-promotion logic instead of the current "probe then project" behavior.
  * The nth-promotion logic should:
    * call `GetOrInitCall(...)` for the nth ID
    * materialize the parent enumerable
    * select `NthValue(nth)`
    * call `DerefValue()`
    * preserve the current null/error behavior
    * return the dereferenced nth result
  * After adding that nth path, keep the existing hit-only `cache miss` probe for non-nth IDs only.
  * Leave the non-nth receiver recursion and normal object-call path unchanged.
  * Also update every other server-side nth materialization site to use the same nth-promotion logic rather than raw `NthValue(...)` projection:
    * enumerable walk in `Resolve(...)`
    * nth-selection / array enumeration in `Select(...)`
  * This is required because nth-element receivers can be materialized by normal GraphQL traversal without ever going through `LoadType(...)`, and those sites must also promote nth IDs into real cached results.

* `dagql/cache.go`
  * Do not add a new code path here.
  * Rely on the existing `initCompletedResult(...)` behavior where, if the initializer result is already cache-backed, it aliases the existing `sharedResult`; otherwise it materializes a new one.
  * This is important because the nth-promotion path should reuse normal cache completion/indexing semantics instead of inventing a second result-installation mechanism.

* `dagql/cache_egraph.go`
  * Do not change `exactProofInputsForRefsLocked(...)`.
  * Do not change `resolveSharedResultForInputIDLocked(...)`.
  * The point of the runtime fix is that these functions should start succeeding for nth-element receiver IDs without any fallback or new proof kind.

* `dagql/builtins.go`
  * No code change planned.
  * Keep relying on `DynamicArrayOutput.NthValue(...)` manufacturing `enumID.SelectNth(i)` detached results.
  * Keep relying on `DynamicResultArrayOutput.NthValue(...)` returning the already-materialized per-element `AnyResult`.

* `core/modtypes.go`
  * No code change planned.
  * Keep relying on module list conversion assigning `itemID := curID.SelectNth(i + 1)` before converting each element.
  * That is already the correct identity model; the missing piece is promotion into the cache when that nth ID is actually loaded later.

#### Why on-demand instead of eager fan-out

* Eagerly caching every nth child when an array result is created would likely fix this class of bug too.
* But it has real downsides:
  * cache/index explosion for large arrays
  * unnecessary persistence closure growth for array elements nobody ever uses
  * noisier release/dependency ownership between parent arrays and all children
* On-demand promotion keeps the same clean invariant without paying those costs:
  * the nth child becomes real when the system actually materializes it as a value
  * not before

#### Detailed behavior after the change

* First materialization of an nth ID:
  * one of the server nth-materialization sites enters the shared nth-promotion path
  * `GetOrInitCall(...)` misses for the nth ID
  * initializer loads the parent enumerable, selects nth, dereferences it, returns it
  * normal cache completion indexes that nth ID as a real result
* Later field selection using the nth ID as receiver:
  * proof capture sees `StructuralInputRef{ID: nthID}`
  * `resolveSharedResultForInputIDLocked(...)` can now resolve it
  * proof input is recorded as `result_ref`
  * export/import path remains unchanged and now works correctly

#### Focused test plan

* Add a new targeted test in `dagql/dagql_test.go` near `TestSelectArray`.
* The test should use a deterministic array-of-objects field, not a random one.
* The test should:
  * build the parent list ID
  * build `nthID := listID.SelectNth(1)`
  * call `srv.LoadType(ctx, nthID)` once and assert success
  * call `srv.LoadType(ctx, nthID)` again and assert it is now a cache hit
  * build a child field ID off that nth receiver
  * call `srv.LoadType(ctx, childID)` and assert success
  * call `srv.LoadType(ctx, childID)` again and assert it hits cache
* This proves both:
  * nth-element IDs are promoted into real cached results
  * downstream proof capture on fields selected from nth receivers now succeeds

#### Validation

* Focused compile/unit checks while iterating:
  * `go test ./dagql -run '^$' -count=1`
  * focused new dagql nth-promotion test
* Targeted integration repro:
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestEngine/TestDiskPersistenceAcrossRestart/function cache control survives restart' > /tmp/cache-debug-function-cache-control.log 2>&1`
  * grep for `panic:|fatal error:|SIGSEGV|--- FAIL:|^FAIL\\s`
* Full restart suite after the targeted repro goes green:
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestEngine/TestDiskPersistenceAcrossRestart' > /tmp/cache-debug-disk-persistence-full.log 2>&1`

#### Expected outcome

* `InputTypeDef.fields` receiver IDs coming from `currentTypeDefs` array elements will resolve to cached nth-element results instead of erroring.
* We keep the current clean proof model:
  * real `ID` => real cached result => `result_ref`
  * digest-only literal => `digest_ref`
* No fallback behavior returns.
* No DB/persistence special case is needed for this fix.

### Handoff Packet

#### Current status

* The persistence rewrite and restart-debug sequence has made real progress in layers:
  * `7bd18dbdd`
    * hard-cut the DB model to `results`, `roots`, `root_members`, `result_terms`, `result_deps`, `result_snapshot_links`
    * removed `eq_facts`, `terms`, `term_results`, `snapshot_refs`
  * `44eff441f`
    * hard-cut structural proof capture over to `StructuralInputRef`
    * removed proof fallback from `ID` to digest
    * fixed the host-mount restart miss by capturing exact `result_ref` proof instead of guessed `digest_ref`
  * `2408374ce`
    * promoted derived receiver IDs into cache-backed results
    * fixed the class of restart failures where nth-derived or dereferenced object receivers had real IDs but no standalone cached `sharedResult`
* After `2408374ce`, the old `function cache control survives restart` failure mode was gone:
  * no more `resolve structural input ... no cached shared result found`
  * no more proof-capture/indexing failure during `loading type definitions`
* Then a new failure surfaced:
  * persisted-hit decode of `Container.withMountedFile` failed because embedded persisted object refs inside container payloads were written using the wrong ID surface
  * the payload pointed at a request/alias object ID while restart lookup expected the canonical persisted result identity
* The current uncommitted work fixes that write-side bug by canonicalizing embedded persisted object references before encoding them:
  * `dagql/cache_persistence_resolver.go`
    * adds a `CanonicalPersistedCallID(...)` helper on cache/session cache
  * `core/persisted_object.go`
    * adds `encodePersistedObjectRefID(...)`
  * `core/container.go`
  * `core/directory.go`
  * `core/file.go`
  * `core/module.go`
  * `core/modulesource.go`
  * `core/object.go`
* With those uncommitted changes applied:
  * `go test ./dagql -run '^$' -count=1`
  * `go test ./core -run '^$' -count=1`
  * `go test ./cmd/engine -run '^$' -count=1`
  * all pass
* The latest targeted repro log is:
  * `/tmp/cache-debug-function-cache-control-rerun3.log`
* The old failures are gone in that log:
  * no `resolve structural input ... no cached shared result found`
  * no `decode persisted hit payload ... missing persisted result key`
* The current blocker is now purely semantic:
  * the test runs successfully on both sides of the restart
  * module loading succeeds
  * `test-always-cache` succeeds on both runs
  * but the returned string differs across restart, so the function result is still being recomputed instead of reused

#### Current blocker in plain English

* We are past:
  * structural proof capture problems
  * nth/deref receiver promotion problems
  * persisted object embedded-reference decode problems
* We are now at the actual thing the test cares about:
  * `Test.testAlwaysCache` returns random text
  * after restart, the second run still computes a new random value instead of reusing the first-run result
* So the next debugging question is:
  * why is the actual function-cache result still missing across restart even though the surrounding module/runtime/container state is now successfully reconstructed?

#### Current repro command

* Use the exact integration command format from `debugging.md`:
  * `dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestEngine/TestDiskPersistenceAcrossRestart/function cache control survives restart' > /tmp/cache-debug-function-cache-control-rerunN.log 2>&1`
* Useful follow-up greps:
  * `rg -n "panic:|fatal error:|SIGSEGV|--- FAIL:|^FAIL\\s" /tmp/cache-debug-function-cache-control-rerunN.log`
  * `rg -n "resolve structural input|missing persisted result key|decode persisted hit payload|test-always-cache|Test\\.testAlwaysCache|loading type definitions|load module: \\." /tmp/cache-debug-function-cache-control-rerunN.log`
* Current important logs to preserve as historical checkpoints:
  * `/tmp/cache-debug-disk-persistence-IbLDTa.log`
    * broad suite run showing the earlier failing subtests
  * `/tmp/cache-debug-function-cache-control-rerun.log`
    * first focused rerun while still failing on structural input resolution
  * `/tmp/cache-debug-function-cache-control-rerun2.log`
    * rerun showing the persisted-hit decode failure for embedded object refs
  * `/tmp/cache-debug-function-cache-control-rerun3.log`
    * latest rerun showing those old failures gone and the remaining semantic function-cache miss

#### Effective subagent repro prompt

* This was effective when the goal was just to rerun the test and report what changed:

```text
Please rerun the exact targeted repro for the function-cache-control restart failure in /home/sipsma/repo/github.com/sipsma/dagger.

Before running, quickly re-read:
- AGENTS.md
- skills/cache-expert/references/debugging.md

Then run exactly this, capturing the full output to a /tmp log:

dagger --progress=plain call engine-dev test --pkg ./core/integration --run='TestEngine/TestDiskPersistenceAcrossRestart/function cache control survives restart' > /tmp/cache-debug-function-cache-control-rerun.log 2>&1

After it finishes:
1. report whether it passed or failed
2. if failed, grep/summarize the first concrete failure and the first relevant dagql_egraph_trace divergence
3. if passed, say that clearly and mention the key signs in the log that the previous failure is gone
4. include the log path
5. do not change code

Focus especially on whether the previous known error is still present, and whether the latest code moved the failure forward.
```

#### Effective subagent analysis prompt

* This was effective when the goal was deep log/code analysis without another blind rerun:

```text
Please do a deep-dive analysis of the current failure in /home/sipsma/repo/github.com/sipsma/dagger.

First, refresh yourself on the current repo guidance and debugging workflow:
- read AGENTS.md
- read skills/cache-expert/references/debugging.md

Then familiarize yourself with the relevant dagql code and especially the available debug trace surfaces. Read enough of these files to understand what the trace events mean and where they come from:
- dagql/cache.go
- dagql/cache_egraph.go
- dagql/cache_debug.go
- dagql/server.go
- dagql/objects.go
- dagql/cache_persistence_import.go
- dagql/cache_persistence_self.go
- core/persisted_object.go
- any other directly-adjacent persistence/decode file needed for the exact failure

Important context:
- do not rerun unless absolutely necessary
- start from the existing log and current code
- current checkpoint commit and current uncommitted changes matter; inspect the live tree, not an older assumption

Please answer these questions very concretely:
1. What is the first real divergence now?
2. What exact code path produces the current error or miss?
3. What do the logs prove already about what succeeded before that point?
4. What is your most specific theory on why the current failure is happening?
5. Is the current observability sufficient to act, or do we need additional logs? If we need more logs, specify exactly where and what fields.
6. If you can see the likely fix direction already, explain it, but do not edit code.

Be explicit about the first bad event and the exact surrounding events that matter.
```

#### What the next agent should verify first

* Before doing any more code changes:
  * reread `AGENTS.md`
  * reread `skills/cache-expert/references/debugging.md`
  * inspect current uncommitted changes in:
    * `dagql/cache_persistence_resolver.go`
    * `core/persisted_object.go`
    * `core/container.go`
    * `core/directory.go`
    * `core/file.go`
    * `core/module.go`
    * `core/modulesource.go`
    * `core/object.go`
  * confirm the latest targeted repro state from `/tmp/cache-debug-function-cache-control-rerun3.log`

#### What the next agent should focus on

* Do **not** re-debug the old structural-input failure unless current evidence says it came back.
* Do **not** re-debug the old embedded persisted object reference decode failure unless current evidence says it came back.
* Focus on the remaining semantic miss:
  * why `Test.testAlwaysCache` still produces a new random value after restart
  * even though module load, type-def load, and test execution all now succeed
* The likely debugging seam is the actual persisted function result/hit path rather than object decode or receiver identity.

## E-graph Observability

### Goal

* Make e-graph/cache behavior permanently debugable without having to add and later remove one-off targeted logs.
* Preserve enough structured mutation history that, with enough patience, we can reconstruct how the e-graph evolved over a run.
* Preserve a deterministic point-in-time snapshot facility so we can answer "what exists right now?" separately from "how did we get here?".

### High-level design

* Use two complementary mechanisms:
  * structured mutation trace events written into the normal engine log stream
  * deterministic e-graph snapshot dump exposed from a debug endpoint
* Mutation trace answers:
  * how did the graph evolve?
  * what path created/merged/removed this result/term/eq-class edge?
* Snapshot dump answers:
  * what is the exact current in-memory graph state right now?
* Both are needed; either one alone leaves big blind spots.

### Logging direction

* Do **not** create a separate side log file for the first cut.
  * With ephemeral engines, operational friction for fetching a separate file is likely not worth it.
* Instead, emit structured e-graph trace events through the same engine logs we already capture.
* Gate them behind a simple compile-time boolean constant for now.
  * Runtime configurability can come later.
* Keep the events structured and machine-parseable, not ad hoc prose logging.
  * Stable event names + stable field names are more important than pretty human wording.

### Snapshot direction

* Add a dedicated debug endpoint under the existing engine debug HTTP surface in `cmd/engine/debug.go`.
* Proposed endpoint shape:
  * `/debug/dagql/egraph`
* Return deterministic JSON for the full current e-graph/cache state.
* This should be point-in-time / pull-based rather than continuously logged.

### Mutation trace event schema

* Every event should include:
  * `trace_format_version`
  * `boot_id` / engine-start ID
  * monotonic `seq`
  * timestamp
  * event type
  * phase/source (`runtime`, `import`, `persist-export`, `persist-apply`, `prune`, etc.)
  * correlation IDs when relevant:
    * `batch_id`
    * `import_run_id`
    * `client_id`
    * `session_id`
    * request/call digest
  * local in-memory IDs when useful:
    * `shared_result_id`
    * `term_id`
    * `eq_class_id`
  * durable identities when useful:
    * `result_key`
    * `id_digest`
    * `output_digest`
    * `output_extra_digests`
  * lightweight metadata:
    * `record_type`
    * `description`
    * object/graphql type when available
  * exact proof inputs when relevant:
    * `result_ref`
    * `digest_ref`
  * short reason/source field saying what code path emitted the event

### Initial event set

* `result_created`
* `result_removed`
* `result_digest_seeded`
* `eq_class_created`
* `eq_class_merged`
* `term_created`
* `term_removed`
* `result_term_assoc_added`
* `result_term_assoc_removed`
* `result_term_assoc_updated`
* `explicit_dep_added`
* `explicit_dep_removed`
* `ref_acquired`
* `ref_released`
* `term_inputs_repaired`
* `term_digest_recomputed`
* `term_rehomed_under_eq_classes`
* `term_outputs_merged`
* `persist_root_marked`
* `persist_root_deferred`
* `persist_member_not_ready`
* `persist_retry_registered`
* `persist_root_retry_triggered`
* `persist_retry_succeeded`
* `persist_batch_build_failed`
* `persist_batch_built`
* `persist_batch_applied`
* `persisted_payload_imported_lazy`
* `persisted_payload_imported_eager`
* `persisted_payload_decoded`
* `persisted_payload_decode_failed`
* `persist_root_added`
* `persist_root_member_added`
* `persist_root_deleted`
* `persist_root_member_removed`
* `persist_orphan_gc_deleted`
* `persist_store_wiped_schema_mismatch`
* `persist_store_wiped_unclean_shutdown`
* `persist_store_wiped_import_failure`
* `import_result_loaded`
* `import_root_member_loaded`
* `import_result_term_loaded`
* `import_result_dep_loaded`
* `import_result_snapshot_link_loaded`
* `lazy_realized`
* `lookup_attempt`
* `lookup_hit`
* `lookup_miss_reason`
* explicit no-op / skip decisions at important persistence/debug boundaries

### Where to emit mutation logs

* `dagql/cache_egraph.go`
  * digest -> eq-class creation
  * eq-class merges
  * term creation/removal
  * result-term association changes
  * result removal from the e-graph
  * persisted-closure propagation decisions
* `dagql/cache.go`
  * result creation/finalization in `initCompletedResult`
  * explicit dependency attachment/removal
  * deferred persist registration / retry trigger paths
  * release paths that affect structural ownership
* `dagql/cache_persistence_worker.go`
  * persistence batch build/apply
  * root delete/orphan GC
* `dagql/cache_persistence_import.go`
  * startup import reconstruction

### Snapshot dump contents

* All results:
  * local result ID
  * durable result key
  * request/output digests
  * extra digests/effects
  * `ref_count`
  * `has_value`
  * payload state (`materialized`, `imported_lazy_envelope`, `nil`)
  * retained/deferred-persist state
  * current deferred-persist watch state
  * explicit deps
  * `held_dependency_results` count
  * snapshot links
  * basic metadata (`record_type`, `description`)
* All terms:
  * local term ID
  * self digest
  * canonical input eq-class IDs
  * term digest
  * output eq-class ID
* All result-term associations:
  * result ID
  * term ID
  * exact proof inputs
* Digest -> eq-class mapping
* Canonical eq-class membership view
* Any pending deferred-persist state
  * including which roots are pending and which members they are currently watching
* Snapshot metadata:
  * `trace_format_version`
  * `boot_id`
  * `captured_at_seq`
  * `captured_at_time`

### Important design principles

* Log mutations, not reads/lookups by default.
  * We want causal history without overwhelming noise.
  * Exception: a small `lookup_attempt` / `lookup_hit` / `lookup_miss_reason`
    family is worth including because it directly identifies first divergence
    boundaries during cache-debug work.
* Include both local IDs and durable keys/digests.
  * Local IDs are needed to reconstruct one process's in-memory graph.
  * Durable keys/digests are needed to correlate across export/import/restart boundaries.
* Keep the trace facility generic and permanent.
  * It should be useful for this bug and the next ten, not just the current host-mount restart miss.

### Initial implementation proposal

#### Phase 1

* Add a tiny central e-graph trace helper in dagql cache code:
  * no-op when the debug const is false
  * emits one structured log event per mutation when enabled
  * owns the monotonic sequence counter
* Add the `/debug/dagql/egraph` endpoint in `cmd/engine/debug.go`.
* Instrument the full mutation surface needed to reconstruct the e-graph with enough patience from one log:
  * result create/remove
  * digest -> eq-class creation
  * eq-class merges
  * term create/remove
  * result-term assoc add/remove
  * result-term assoc metadata updates
  * term repair / rehome / digest recompute transitions
  * output-eq merges caused by congruent term repair
  * explicit dep add/remove
  * retained/persisted-closure propagation changes
  * deferred-persist not-ready / registration / retry / success / failure transitions
  * exact deferred-watch registration / firing / clearing details
  * persistence batch build/apply and build-failure transitions
  * persisted-root and root-member add/remove transitions
  * root delete/orphan-GC transitions
  * persistence-store wipe/reset transitions at startup
  * import-time result/root/member/term/dep/snapshot-link reconstruction events
  * imported payload lazy/eager materialization state transitions
  * successful natural lazy-realization completion events
  * lookup attempt/hit/miss-reason events at the cache lookup boundary
  * important skip/no-op decisions at persistence/debugging boundaries
  * refcount/lifecycle transitions that affect retention/prune semantics
* Make the dump deterministic enough that two snapshots can be diffed meaningfully.

### Expected payoff

* New cache/e-graph bugs should become debuggable from one captured engine log plus an optional debug dump, instead of requiring temporary ad hoc logging patches every time.
