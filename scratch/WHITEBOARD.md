# WHITEBOARD

## TODO
* Remove `Module.persistedResultID` and stop mutating typed `*Module` self values with attached result IDs
  * The remaining read is only for the `IncludeSelfInDeps` skip during `Module.EncodePersistedObject`
  * Follow up by hard-cutting the persisted-object encoding contract so object encoders receive current result identity directly instead of reading mutable IDs off typed self structs
* Assess changeset merge decision to always use git path (removed `conflicts.IsEmpty()` no-git fast path), with specific focus on performance impact
   * Compare runtime/cost of old no-git path vs current always-git path in no-conflict workloads
   * Confirm whether correctness/cohesion benefits outweigh any measured regression and document outcome
* Sweep remaining raw `srv.Select(..., &ptr, ...)` / `.Self()` ownership boundaries after the current result-detach fixes
   * Confirm there are no more places where a releasable `*Directory` / `*File` / `*Container` pointer is being re-exposed after loading from dagql
   * In particular, keep checking for any helper that unwraps a selected result to a raw pointer and then returns or re-wraps that same pointer
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

## Notes
* **THE DAGQL CACHE IS A SINGLETON CREATED ONCE AT ENGINE START AND IT LIVES FOR THE ENTIRE LIFETIME OF THE ENGINE.**
  * There is not a second DAGQL cache.
  * There is not a per-session DAGQL cache.
  * Result-call planning/runtime code should not be written as if cache identity were ambiguous.
  * If a code path needs the DAGQL cache, it should explicitly use or fetch the singleton cache rather than storing mutable cache backpointers on frame/helper structs.
* **CONTAINERD IS TRUSTED. IT IS IN OUR FULL CONTROL. IN THIS USE CASE IT IS NOT SHARED WITH OR DRIVEN BY ANY OTHER SYSTEM.**
  * We should be entirely willing to use containerd directly where it is the right substrate.
  * We should not duplicate state in dagql that containerd already stores correctly and authoritatively for us.
  * Dagql persistence should store only the Dagger-specific state that containerd does not already represent.
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.

* **CRITICAL OWNERSHIP RULE: NEVER RE-EXPOSE A RAW DAGQL-LOADED POINTER FOR A RELEASEABLE OBJECT.**
  * This is a crucial design constraint for the hard-cutover cache/snapshot model.
  * If a helper loads or selects a `Directory`, `File`, or `Container` from dagql, then returning the raw `*Directory` / `*File` / `*Container` pointer is dangerous unless that object is explicitly detached first.
  * The reason is concrete and non-negotiable: these objects own snapshot refs via `OnRelease`, so pointer aliasing can poison some other owner when one result is released.
  * Avoiding this entire class of bugs is crucial.
  * The preferred fix is to preserve `dagql.ObjectResult[*T]` all the way through whenever possible.
  * If a raw `*T` really must be returned, it must be a detached object with reopened/refreshed ownership of any releaseable snapshot state.
  * Internal `Value` shells embedded in larger objects are not public results and must not be handed back out directly as if they were.

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

# Lazy And Snapshot Cleanup
