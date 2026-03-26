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
* Moved the Prune Refactor notes to `scratch/prunerefactor.md` so this file can stay focused on typedef performance.

# Telemetry

## Design

### Core model

We are doing a hard cut away from the old buildkit-shaped effect model for lazy evaluation telemetry.

Old effect IDs existed to bridge two separate systems with separate identity:
- dagql call spans
- buildkit vertex/custom-op spans

That split no longer exists for lazy evaluation in the new cache model. A lazy callback is not a separate buildkit-owned unit of work. It is deferred continuation of the same conceptual dagql operation/result.

So the new telemetry model should be:
- the original call span is the main user-visible span for the operation
- if the call returns a result that is still lazy, the original span is marked as pending
- later, when the lazy callback actually runs, we emit a new hidden `telemetry.Resume(...)` span causally linked back to the original span
- the resume span is only for duration/status/activity of the resumed lazy phase
- logs from the lazy callback should go to the original span, not the resume span
- nested spans started by the lazy callback should also continue from the original span lineage rather than becoming children of the hidden resume span
- to make that work, the lazy callback must run under a special callback context that exposes the original span context while still preserving a real tracer provider and a real recording span for direct span mutations/events

This means:
- no effect IDs
- no `effects.completed`
- no recursive effect aggregation
- no buildkit compatibility logic
- pending becomes an explicit first-class server-driven state rather than being inferred from effect attrs

### Session scoping

The mapping from result to original span context must be session state, not cache-global state.

Reason:
- `sharedResult` is engine-global and can live much longer than any one session
- span contexts are useful only relative to the current session/trace
- storing span context on `sharedResult` would incorrectly associate later sessions with earlier sessions' spans

So we need session-local state in the cache of roughly:
- `sessionID -> sharedResultID -> trace.SpanContext`

Important properties:
- first-writer-wins within a session
- mapping is cleared when the session is released
- no persistence
- no import/export
- no cross-session fanout for the first pass; current session only

We explicitly are *not* trying to solve:
- multi-session resume fanout
- using one result realization to send resume telemetry into every session that has ever seen the result

That can be revisited later if needed. For now we keep the design simple and correct for the current session.

### Explicit capture points

Do not bury result->span-context capture inside generic session-result tracking.

That would be too implicit and would end up capturing many intermediate/internal contexts that are not actually the intended user-visible "original span" for a lazy result.

Capture must happen only in explicit places where we trust the current context to be the right original span for the current session.

Current intended capture sites:
1. `Cache.GetOrInitCall` cache-hit return path
2. `Cache.wait` completion return path for newly completed calls
3. `Cache.LoadResultByResultID` success path

Capture rules:
1. no-op if `sessionID` is empty
2. no-op if result is not cache-backed
3. no-op if current `trace.SpanContextFromContext(ctx)` is invalid
4. set mapping only if no mapping already exists for `(sessionID, sharedResultID)`

This should be an explicit helper in the cache used only at those sites.

We are intentionally *not* capturing this in generic `trackSessionResult`.

### Pending model

Pending should become an explicit attr emitted by the server.

Today pending is client-derived from effect attrs. We are replacing that.

Add a local repo-owned const for a new attr, something like:
- `PendingAttr = "dagger.io/dag.pending"`

Important:
- do not modify the upstream `github.com/dagger/otel-go` package right now
- the server and client in this repo can just share a local const
- if duplicated in more than one package for now, add a TODO noting that it should eventually live in shared telemetry attrs

Pending semantics:
- original call span sets `PendingAttr=true` if it returns a result whose lazy callback is still unrealized
- once a causal linked resume span appears for that original span, the client should stop considering it pending
- while the linked resume span is running, the original span should appear active through existing causal propagation
- if the linked resume span fails, the original span should appear failed through existing causal propagation

Meaning of pending in this model:
- "this operation produced deferred work that has not yet resumed in this session"

If the lazy callback never resumes in the session, the original span stays pending. That is acceptable and consistent with this design.

### Resume-span model

When the cache actually executes a lazy callback, it should:
1. find the current session ID from client metadata in the evaluation ctx
2. look up the original span context for `(sessionID, sharedResultID)`
3. if no original span context exists for this session/result, just evaluate normally with no resume behavior
4. if one exists, start a hidden resume span using `telemetry.Resume(trace.ContextWithSpanContext(ctx, originalSpanCtx))`
5. use that hidden resume span only for:
   - duration of resumed lazy work
   - final status of resumed lazy work
   - causal/activity propagation back to the original span
6. run the lazy callback itself with a special callback context that presents the original span context while still using the real tracer provider and delegating direct span mutations to the hidden resume span

This is the crucial split:
- resume span: status/duration/activity only
- callback context: original span identity for logs/nested spans, resume-span recording target for direct span mutations/events

That is what gives the intended UI behavior:
- logs continue on the original row
- resumed work can still have timing/status of its own
- the original span can become active/failed again through causal linking

The resume span should usually be hidden by default:
- mark it internal
- keep it as noise-free as possible

The reason we do not need an extra `LazyResumeAttr` is that causal linking itself is the signal:
- original span says it is pending
- the first causal linked continuation means the pending work has resumed

### Lazy callback execution context

When lazy evaluation runs, we must not simply run the callback under the resume-span context.

If we do that:
- logs go to the resume span instead of the original span
- nested spans become children/descendants of the resume span instead of visually continuing the original operation

We also must not simply do `trace.ContextWithSpanContext(ctx, originalSpanCtx)` and call it done.

That is sufficient for log correlation, but it is *not* sufficient for nested spans:
- OpenTelemetry's `ContextWithSpanContext` installs a non-recording span wrapper
- that wrapper carries the right `SpanContext`
- but its `TracerProvider()` is effectively noop
- so nested `Tracer(ctx).Start(...)` calls inside the lazy callback would not record correctly if we used it directly

So the callback context must be constructed intentionally:
- the hidden resume span exists in parallel
- the current span in the callback context must report `SpanContext() == originalSpanCtx`
- the current span in the callback context must report a real tracer provider, not the noop provider from plain `ContextWithSpanContext`
- direct span mutation methods on the callback context's current span should delegate to the hidden resume span

Concretely, the callback context should install a tiny custom span wrapper with behavior like:
- `SpanContext()` returns the stored original span context
- `TracerProvider()` returns the real tracer provider from the current evaluation/resume context
- `AddEvent`, `SetAttributes`, `SetStatus`, `RecordError`, `SetName`, `AddLink`, `End` delegate to the hidden resume span

This gives the desired split cleanly:
- log SDK pulls trace/span IDs from `trace.SpanContextFromContext(ctx)` and therefore writes logs onto the original span
- `core.Tracer(ctx).Start(...)` and similar nested span creation still work because they get a real tracer provider from the callback context's current span
- direct `trace.SpanFromContext(ctx).AddEvent(...)` / `SetAttributes(...)` style calls made inside the callback land on the hidden resume span rather than disappearing

That way:
- `telemetry.SpanStdio(...)` inside the callback writes to the original span
- direct `telemetry.NewWriter(...)` style logging writes to the original span
- any nested `Tracer(...).Start(...)` calls made during lazy realization continue from the original span lineage using the real tracer provider
- direct span events/attribute/status mutations made against `trace.SpanFromContext(ctx)` are recorded on the hidden resume span

### Client-side simplification

The client should be simplified substantially as part of this cut.

Delete effect-driven lazy state logic:
- `EffectID`
- `EffectIDs`
- `EffectsCompleted`
- effect-spans indexes
- completed-effects bookkeeping
- failed-effects bookkeeping
- effect-based pending logic
- effect-based cached inference
- effect-debug rendering

Keep and reuse the existing causal-link model:
- links already default to causal relationships
- causal links already feed activity propagation
- causal links already feed failure propagation
- causal links already affect display hierarchy

New client pending logic should be:
1. if final snapshot says pending, trust that
2. if span is running or has running linked continuations, it is not pending
3. if span has raw `Pending=true` and no causal linked continuation has appeared yet, it is pending
4. otherwise it is not pending

New cached logic should be much simpler:
- trust explicit server `CachedAttr`
- stop trying to infer cached-ness from effect attrs

We should strongly consider renaming client-side `effectsViaLinks` to something like `continuationsViaLinks` or similar, because after this cut those linked spans are no longer conceptually "effects" in the old buildkit sense.

### Server-side changes

In `core/telemetry.go`:
- delete `collectEffects`
- stop emitting effect attrs entirely
- add explicit pending emission on original call span
- pending should be set when the returned result is still lazy/incomplete at the time the original call span finishes

In dagql:
- add a helper for "does this result still have pending lazy realization right now?"
- do not route this through `ResultCall.AllEffectIDs` or any similar recursive metadata path
- inspect actual cache-backed lazy state directly

In `Cache.evaluateOne`:
- this is the central place to perform resume logic
- it already owns lazy singleflight evaluation
- it should also own hidden resume-span creation and lookup of the current session's original span context

In session-state bookkeeping:
- add session-local map for original span contexts
- clear it on session release with the rest of session-root cache state

### Persistence changes

Because effect IDs are gone from the lazy model:
- remove `outputEffectIDs`
- stop persisting/importing any effect-related result metadata
- remove effect-related fields from debug snapshots and persistence contracts

This should be a clean schema/model cut, not dead compatibility baggage left around.

### Tests

Add focused tests for:
1. original call span gets `PendingAttr=true` when returning still-lazy result
2. current session captures original span context in explicit capture sites
3. first-writer-wins within a session
4. `ReleaseSession` clears the session-local span-context mapping
5. lazy evaluation with stored original span context creates hidden resume span
6. lazy callback logs attach to original span, not the resume span
7. nested spans started inside the lazy callback still record correctly and continue from the original span lineage
8. direct `trace.SpanFromContext(ctx)` mutations inside the lazy callback are recorded on the hidden resume span
9. causal linked resume span clears pending in the client
10. causal linked resume span failure propagates failure back to original span
11. load-by-result-ID path captures original span context for the current session

Prefer focused unit/targeted tests before broader integration coverage.

### Scope limit / explicit non-goal for v1

This first pass only resumes telemetry for the current session when the current session has explicitly captured an original span context for the result.

We are not trying to solve:
- fanout to every session that knows a result
- retroactively resuming spans in other sessions
- cross-session log routing

If some result reaches a session only through a path that is not one of the explicit capture sites, then that result simply will not get resume-style telemetry continuity in that session for v1.

That is an intentional scope boundary, not something to paper over implicitly.
