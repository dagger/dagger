# Lazy Evaluation in DagQL Cache

This document describes the current lazy-evaluation model used by the dagql cache and the `core` object implementations built on top of it.

At a high level, laziness means a resolver can return a real dagql result immediately while deferring some materialization work until that result is actually needed later. The returned result is still a normal cache-backed result with normal identity, dependency edges, session-resource requirements, and persistence behavior. What is deferred is the work needed to fully materialize its internal state.

In practice, this is used heavily for `Directory`, `File`, and `Container` objects. The typical pattern is:

1. A schema resolver returns an object shell immediately.
2. The object carries a `Lazy` implementation that knows how to finish materializing it later.
3. Future code accesses a lazy-backed field through a `LazyAccessor`, which calls into `dagql.Cache.Evaluate`.
4. The cache guarantees that evaluation for a given attached result only runs once at a time, with correct call context and telemetry reconstruction.

## Why laziness exists here

There are three main reasons this exists:

- It lets dagql publish and cache a result before doing all the expensive work needed to fully materialize it.
- It lets later callers share that deferred work instead of each redoing it independently.
- It lets object implementations keep a small, stable shell alive in the cache while computing heavyweight state only on demand.

This is different from `DoNotCache`. A lazy result is still expected to be attached to the cache. In fact, the cache rejects newly created lazy results from `DoNotCache` calls, because lazy evaluation depends on the result having an attached `sharedResult`.

A `DoNotCache` field can still return an already attached lazy result. What is rejected is creating a brand new lazy result that never becomes cache-backed.

## DagQL-Side Contract

The dagql cache does not know anything about `Directory`, `File`, `Container`, or any other concrete type-specific lazy shape. Its contract is intentionally small:

- `dagql.HasLazyEvaluation`
- `dagql.LazyEvalFunc`
- `dagql.Cache.Evaluate`
- `dagql.HasLazyEvaluationParts` and `dagql.Cache.EvaluateParts` (per-part evaluation, below)

Any result can participate in lazy evaluation if its wrapped value implements:

```go
type HasLazyEvaluation interface {
 LazyEvalFunc() LazyEvalFunc
}
```

That means the mechanism is generic. In practice, the engine mostly uses it for object results, but the cache itself is not limited to objects.

## Cache Entry Points

The key dagql entry points are:

- `dagql.Cache.Evaluate`
  The public API for forcing one or more attached results to finish every piece of their lazy evaluation.
- `dagql.Cache.EvaluateParts`
  The public API for forcing only the named parts of one attached result. On a value that does not split its deferred work this degenerates to `Evaluate`.
- `dagql.Cache.evaluateGroup`
  The per-(result, group) implementation that handles singleflight, recursion detection, cancelation, call-context restoration, and telemetry resumption. For values that do not split their work there is exactly one group, so this is the familiar per-result loop.
- `dagql.Cache.registerLazyEvaluation`
  Stores the current lazy callback on the attached `sharedResult` when a result is first published or when a cache/persisted hit is re-wrapped.
- `dagql.HasPendingLazyEvaluation`
  Reports whether an attached result still has deferred work. Telemetry uses this to avoid treating a pending lazy hit as a fully satisfied cache hit.

## What `Cache.Evaluate` Guarantees

`Cache.Evaluate(ctx, results...)` does two different kinds of coordination:

First, if the caller passes multiple results, the cache evaluates those different results in parallel with an `errgroup`.

Second, for any single attached result, the cache uses per-`sharedResult` singleflight so that only one lazy callback is running for that result at a time. Other callers wait for the same work instead of duplicating it.

That gives two important properties:

- evaluating several unrelated lazy results can proceed concurrently
- evaluating the same lazy result from many callers collapses to one actual callback execution

The tests in `dagql/cache_test.go` cover both behaviors.

## `sharedResult` Lazy State

Attached results carry cache-owned lazy state in `dagql.sharedResult`, organized per evaluation group (`lazyGroupState`):

- `lazyWhole` is the implicit whole-result group, held inline: the lazy state of values that do not split their deferred work. Results that never split allocate nothing beyond it.
- `lazyPartGroups` holds the named groups of a parts value; nil until first use. Per value exactly one regime is in use: a parts value never uses `lazyWhole` and a non-parts value never uses named groups.
- `lazyEvalComplete` is the result-level completion latch: everything deferred anywhere on the result is settled. It is only the fast path; correctness derives from the per-group state.

Each group record carries the same four fields the per-result state used to:

- `eval` is the group's stored callback
- `complete` means the group's evaluation succeeded
- `attempt` is the group's currently running callback attempt, including its completion channel, cancel function, waiter count, outcome, and telemetry wait targets
- `syncPending` means a previous attempt's callback body succeeded but the attempt's cache-side bookkeeping (snapshot-lease sync, lease release) did not; the next attempt retries only that bookkeeping

This state is guarded by `lazyMu`. `syncResultSnapshotLeases` itself is additionally serialized per result (`leaseSyncMu`) because concurrent group attempts make concurrent lease syncs routine and the read-diff-write sync must not interleave.

Waiters retain the particular attempt record they joined. When its callback
finishes, the cache stores the outcome on that record and removes it as the
current attempt. Existing waiters can then consume the old outcome without
reading or changing a newer attempt's state.

## How Lazy Callbacks Get Registered

The cache registers lazy evaluation whenever it publishes or reconstructs an attached result that still has deferred work.

Important places where this happens:

- after a new completed call is initialized in `initCompletedResult`
- when an attached dependency result resolves to an existing cache hit
- when a cache hit is re-wrapped from an attached `sharedResult`
- when a persisted hit payload is decoded lazily and wrapped back into a typed result

This matters because the `sharedResult` is the stable cache-owned object, while typed wrappers may be re-created on hit paths. The cache re-derives the current `LazyEvalFunc` from the wrapped value and stores it on the `sharedResult` so later `Evaluate` calls have the right callback.

## Evaluate Flow

For a single result, `Cache.evaluateResolved` routes the demand: a value that does not implement `HasLazyEvaluationParts` goes straight to the whole-result group; a parts value has its demand resolved to named groups (`ResolveLazyEvalGroups`, run outside `lazyMu` because it may re-enter the cache to settle the value's own metadata part) and each group is evaluated in order. `Cache.evaluateGroup` then works roughly like this, per (result, group):

1. Validate that the cache and result are non-nil, and that the result is attached to a real `sharedResult`.
2. Detect recursive lazy evaluation using a stack of `(sharedResultID, group)` pairs stored in context, pushed at group entry. The resolution phase legitimately re-enters the same result for its metadata group before the requested group is entered; re-entering the same (result, group) is refused.
3. If the result's result-level latch or this group's completion is set, return.
4. If another goroutine is already evaluating this group, retain its attempt record and wait on its channel. This check comes before reading any object-side lazy state: callback bodies (Directory, File, Container) clear their object-side state while their attempt is still running cache-side bookkeeping, so object-side state is only trustworthy once no attempt is published.
5. With no attempt in flight, check pending bookkeeping first: the group's `syncPending` means a previous attempt's body already succeeded and consumed the group's deferred work, so the group's callback is not re-read and the next attempt retries only the bookkeeping. Otherwise re-read the group's current callback from the wrapped value (`LazyEvalFuncForGroup` for named groups, `LazyEvalFunc` for the whole group); if it is nil, mark the group complete and return.
6. Otherwise start an attempt in a background goroutine and wait for it: the callback body if one is armed, then the cache-side bookkeeping; or only the bookkeeping when the group's `syncPending` is set.

The result-level `lazyEvalComplete` latch is set by whole-group completion for non-parts values, and for parts values only by a whole-value pass that resolved the full group set after metadata settled and observed every returned group complete. A parts-scoped `EvaluateParts` never sets it.

Two details are especially important.

### Recursive Evaluation Detection

The cache threads a linked stack of `sharedResultID`s through context while evaluating lazy results. If a lazy callback tries to re-enter evaluation of itself, or any ancestor already on that stack, the cache returns:

`recursive lazy evaluation detected`

That prevents accidental infinite recursion when a lazy implementation evaluates the wrong result.

### Waiter and Cancelation Semantics

The actual callback runs under a context built from `context.WithoutCancel(stackCtx)`, then wrapped in `context.WithCancelCause`.

That means one impatient caller does not immediately tear down the shared lazy callback for everyone else. Instead:

- each waiter can independently cancel its own wait
- if the last waiter goes away, the cache invokes that attempt's cancel func with the waiter's cause
- the callback attempt remains current until the callback actually finishes, so a replacement callback never overlaps it
- if a healthy waiter receives a cancellation caused by the shared callback context, it transparently retries instead of returning another waiter's cancellation

This is a shared-work model, not a per-caller callback model.

## Call Context Restoration

Before starting the lazy callback, the cache restores the result's authoritative `ResultCall` into the callback context with:

`dagql.ContextWithCall(evalCtx, resultCall)`

This is crucial. Lazy evaluation often runs much later than the original field resolver, but many core helpers still need the current dagql call.

One concrete example is `DirectoryWithoutLazy.Evaluate`, which calls:

`dir.Without(ctx, lazy.Parent, dagql.CurrentCall(ctx), true, lazy.Paths...)`

That only works because `Cache.Evaluate` restored the original call frame first.

Without this, lazy implementations that depend on `dagql.CurrentCall(ctx)` would behave differently from eager execution and could break equivalence-teaching, provenance, or other call-sensitive behavior.

## Telemetry Resumption

The lazy model also restores telemetry lineage instead of treating lazy work as an unrelated background task.

When a result is first returned from `GetOrInitCall` or `wait`, the cache captures the session's current span context in `captureSessionLazySpanContext`.

Later, when some caller actually triggers `Cache.Evaluate`, the cache:

- creates a hidden resume span named either `resume lazy evaluation` or `resume <field>`
- parents that span under the current triggering span
- links it back to the original span context from the initial call

Then it wraps the callback context with `resumedCallbackSpan`, which deliberately reports the original span context to the lazy callback itself.

That gives the desired split:

- the trigger path records that lazy work resumed now
- logs and child spans emitted by the lazy callback still line up with the original call lineage

This is why the telemetry tests verify both:

- a hidden `resume ...` span linked to the original span
- child spans and logs from the lazy callback appearing under the original span context

## Success and Failure Semantics

After the lazy callback returns successfully, the cache:

1. syncs snapshot owner leases with `syncResultSnapshotLeases`
2. marks `lazyEvalComplete = true`
3. clears the stored `lazyEval`

If the callback genuinely fails, the cache does not mark the result complete.
Every caller waiting on that attempt receives the failure, and a future
`Evaluate` call can try again. If the callback instead returns cancellation
caused by its shared callback context, a still-healthy waiter retries
immediately. A waiter's own context cancellation still ends only that
waiter's call with its own cancellation cause.

So the rule is simple:

- success permanently materializes the attached result
- genuine failure is returned and leaves the result pending
- cancellation caused by abandoned shared work is retried for healthy callers

## Per-Part Evaluation (Stage 2)

A value may split its deferred work into independently evaluable pieces by implementing `dagql.HasLazyEvaluationParts`:

- A `PartKey` names one separately evaluable piece of the value (dagql treats keys as opaque; the value's package defines them).
- A `LazyGroupKey` names one evaluation group. Every part maps to exactly one group, a group's single body fills all its parts, and an attempt is per (result, group). There are no part-level attempts.
- `ResolveLazyEvalGroups(ctx, self, parts)` maps requested parts to groups in evaluation order (nil parts means everything). Resolution may evaluate the value's own metadata part via the cache — the only self-demand anywhere — so positional questions (which mounts exist) are answered before any snapshot group's body starts. It must never evaluate snapshot content.
- `LazyEvalFuncForGroup(group)` is the per-group consumption contract, exactly like `LazyEvalFunc` per group: nil once the group's work is consumed, and the cache independently guarantees a group's body never runs twice.

Rules the container implementation follows (and any future parts value should):

- Every part is written exactly once, by its own group's body, and never rewritten. A delegation body copies from the parent's already-latched part, so a part's value is independent of the order sibling groups run in.
- Group bodies never demand sibling groups of their own op. The only intra-result demand is the resolution-phase metadata settling, which completes before any requested group's body starts.
- A refined container op keeps `container.Lazy` non-nil until its last group is consumed, which keeps `LazyEvalFunc() != nil` meaning "something is still deferred" and persistence's ready-form selection (`Lazy == nil`) meaning "fully materialized". A partially evaluated result persists in the lazy form: the recipe, exactly like an unevaluated result; partial progress is re-derived on demand after restart.

For containers the parts are `metadata` (every plain field), `fs`, `execMeta`, and `mount:<target>` (target-keyed: targets are unique in a mount list and survive add/remove/replace). Refined ops fall into two templates: metadata-only mutations (metadata group applies the field edit; every snapshot part is its own delegation group) and snapshot writers (the exec's joint `execOutputs` group fills fs, execMeta, and every writable mount from one process run; `withRootfs`'s `write` group fills fs from its source). Unrefined ops map every part to the whole-result group and behave exactly as before, so chains refine incrementally: a refined child of an unrefined parent simply evaluates the parent fully when it delegates.

Failure and retry are per group: a group body failure leaves that group retryable and its siblings untouched; success is permanent per group. Two callers wanting different parts of one result do not serialize at either layer — their groups' attempts are independent, and object-side sibling groups hold different per-group mutexes.

## The Second Layer: `core.Lazy[T]`

The cache-level mechanism is only half the story. The object implementations use a second layer in `core/lazy_state.go`:

```go
type Lazy[T dagql.Typed] interface {
 Evaluate(context.Context, T) error
 AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error)
 EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error)
}
```

This is the object-side contract.

Every concrete lazy type in `core` embeds a `LazyState`.

`LazyState.Evaluate` gives per-instance idempotence for whole-op evaluation:

- if the instance already finished, it returns immediately
- otherwise it locks `LazyMu`, runs the callback once, and marks the instance complete on success

`LazyState.EvaluateGroup` is the per-group form used by refined container ops: each group has its own mutex held across only that group's body (`LazyMu` guards the latch map and is never held across a body, so sibling groups of one op run concurrently), and a second runner of the same group observes the group's done flag and returns. Per op exactly one of the two entry points is in use.

This is distinct from the cache-level singleflight.

### Why There Are Two Layers

There are two separate jobs here:

- dagql cache lazy state coordinates attached results across callers, restores call context, and restores telemetry
- `core.LazyState` makes each concrete lazy object implementation itself behave like a one-time materializer

The cache layer is the authoritative cross-caller coordination layer. The `core` layer keeps each lazy implementation internally disciplined and idempotent.

## `LazyAccessor`: The Actual Field Boundary

The most important practical API for authors is `LazyAccessor`.

Examples:

- `Directory.Dir`
- `Directory.Snapshot`
- `File.File`
- `File.Snapshot`
- `Container.FS`
- `Container.MetaSnapshot`

`LazyAccessor` exists to make it hard to accidentally read a lazy-populated field without first evaluating the owning result.

### `GetOrEval`

`GetOrEval(ctx, ownerResult)` is the normal access path.

It:

1. fetches the engine cache from context
2. calls `cache.Evaluate(ctx, ownerResult)`
3. returns the accessor's value after evaluation

If evaluation succeeds but the accessor still was not populated, it returns an error. In other words, `GetOrEval` treats "the lazy callback forgot to set the field" as a bug.

### `Peek`

`Peek()` returns the currently stored value without triggering lazy evaluation.

This is intentional and important. Many paths need to inspect already-known state without forcing full materialization, including:

- release paths
- cache usage accounting
- persisted snapshot link reporting
- persisted object encoding
- schema constructors that can pre-seed a cheap path or shell immediately

`Peek` is for "use what is already present." `GetOrEval` is for "I need the real materialized value."

### `SetValue`

`SetValue` is used by:

- schema constructors that pre-seed obvious cheap values
- lazy implementations after they finish materializing

The comment on `GetOrEval` is important: the caller must pass the dagql result wrapper for the same owning object as the accessor. That pairing is not validated automatically today.

## Attach-Time Dependencies vs Evaluate-Time Dependencies

Every concrete `core.Lazy` type also implements `AttachDependencies`.

This is not the same thing as calling `cache.Evaluate` inside the lazy callback.

They serve different purposes:

- `AttachDependencies` runs when the object is attached to the cache. It rewrites embedded result references to attached/cache-backed results and returns the exact dependency edges that should be recorded for ownership, pruning, and persistence closure.
- `cache.Evaluate` inside the lazy callback runs later when the implementation actually needs those dependencies materialized.

This distinction is central to the design. A dependency can be known structurally and retained correctly long before its expensive value is actually needed.

## Common Authoring Pattern in Schema Code

Most schema resolvers follow the same shape:

1. Normalize or load the inputs they care about.
2. Construct an object shell immediately.
3. Pre-seed any cheap fields that are already known.
4. Store a concrete `Lazy` implementation on the object.
5. Return a normal dagql object result immediately.

Two common examples:

### Example: `container.rootfs`

The schema returns a `Directory` shell immediately with:

- `Lazy: &core.ContainerRootFSLazy{...}`
- `Dir` pre-seeded to `"/"`

The expensive work of resolving the actual rootfs snapshot is deferred until somebody needs it.

### Example: `container.directory(path)`

The schema resolves env expansion and working-directory normalization immediately, then returns a `Directory` shell with:

- `Lazy: &core.ContainerDirectoryLazy{Parent: parent, Path: path}`
- `Dir` pre-seeded to the resolved path

Validation and snapshot reopening happen later during lazy evaluation.

### Example: `file.withName(name)`

The schema constructs a `File` shell immediately, stores `FileWithNameLazy`, and if the parent's current path is already known it pre-seeds the derived path in the accessor right away.

## Lazy Args Are the Actual Evaluation Recipe

This is a very important practical point: the fields stored on a concrete lazy struct are the arguments that define eventual evaluation.

They are not required to match the outer GraphQL arg struct one-for-one.

In many places, schema code has already normalized the inputs before storing them on the lazy struct. For example:

- IDs are often loaded into attached `dagql.ObjectResult[...]` values before being stored
- alternate arg shapes are collapsed into one normalized source
- paths may already be env-expanded or made absolute
- a resolver may pre-seed some cheap state separately from the lazy struct itself

So when reading a lazy type, treat its fields as the real execution recipe for deferred evaluation, not as a copy of some public API shape.

## Directory and File Pattern

`Directory` and `File` follow a very consistent shape:

- they expose lazy-populated accessors for path and snapshot
- their `AttachDependencyResults` implementation just delegates to the current lazy op
- their `LazyEvalFunc` wrapper calls `lazy.Evaluate(...)` and then clears `Lazy` on success

That last point is important: for `Directory` and `File`, the wrapper method itself handles the common "clear `Lazy` after successful materialization" behavior.

Representative lazy types include:

- `DirectoryWithDirectoryLazy`
- `DirectoryWithFileLazy`
- `DirectorySubdirectoryLazy`
- `DirectoryWithoutLazy`
- `FileSubfileLazy`
- `FileWithNameLazy`
- `FileWithReplacedLazy`

The common implementation shape is:

1. call `lazy.LazyState.Evaluate`
2. evaluate any parent/source results that must be materialized
3. use `GetOrEval` on accessors when actual values are needed
4. call the existing non-lazy helper or populate the target accessors directly

Two representative examples:

- `DirectoryWithDirectoryLazy.Evaluate` just delegates to `dir.WithDirectory(...)`
- `DirectorySubdirectoryLazy.Evaluate` materializes the parent, validates the subdirectory only when needed, then reopens the parent snapshot by ID and populates the new directory shell

That second case is a good example of why laziness exists: path validation and snapshot reopening are deferred until somebody actually needs the subdirectory value.

## Container Pattern

`Container` uses the same overall model, but with more variation.

There are two large families of container lazy ops:

- mutation lazies that produce a new container state from an existing parent container
- selector/view lazies that produce a `Directory` or `File` view from a container

### Mutation Lazies

Examples:

- `ContainerWithRootFSLazy`
- `ContainerWithDirectoryLazy`
- `ContainerWithFileLazy`
- `ContainerWithUnixSocketLazy`
- many config mutation lazies like env, labels, workdir, entrypoint, and so on

These usually follow this pattern:

1. materialize the parent container with `cache.Evaluate`
2. copy its current concrete state into the destination shell, often via `materializeContainerStateFromParent`
3. apply the existing eager helper (`WithDirectory`, `WithFile`, `WithUnixSocketFromParent`, etc.)
4. clear `container.Lazy`

The container lazy implementations typically clear `container.Lazy` themselves after success. Unlike `Directory` and `File`, container-wide lazy clearing is not centralized in `Container.LazyEvalFunc`.

### Selector/View Lazies

Examples:

- `ContainerRootFSLazy`
- `ContainerDirectoryLazy`
- `ContainerFileLazy`

These materialize detached `Directory` or `File` shells from container state. They often:

- evaluate the parent container
- inspect already-known mounted sources with `Peek`
- reopen snapshots by ID when needed
- clone detached directory/file shells instead of aliasing the parent's internal objects directly

This detached-clone behavior is important. It keeps a child result shell from sharing mutable accessor state with the parent container object.

## Why `materializeContainerStateFromParent` Exists

Container mutation lazies almost all need a concrete copy of the parent's current state before applying one more operation.

`materializeContainerStateFromParent` does that by:

- forcing evaluation of the parent
- cloning `FS`
- cloning `Mounts`
- cloning `MetaSnapshot`
- cloning container config and other slices/maps

This avoids reimplementing the same copy logic in every lazy type and keeps container lazy evaluation deterministic.

## Persistence of Lazy Objects

Lazy evaluation is designed to survive persistence.

For `Directory` and `File`, persisted object encoding chooses between:

- a snapshot form when a concrete snapshot is already available
- a lazy form when the object is still deferred

For `Container`, the persisted payload distinguishes:

- a ready form
- a lazy form

Nested directory/file values inside containers are also encoded explicitly.

Each lazy type that supports persistence implements `EncodePersisted`, and the corresponding object decoder reconstructs the right lazy type from an explicit persisted lazy kind.

So persistence does not serialize "a function pointer." It serializes an explicit, typed lazy recipe plus references to the attached dependency results it needs.

## Selector Lazies as Standalone Persisted Forms

Selector-style container lazies are supported as standalone top-level persisted forms.

The persisted directory/file payload carries a `lazyKind` alongside the lazy recipe JSON. This is necessary because some field names are ambiguous across parent types. For example, `file` can mean `Directory.file` or `Container.file`; decoding from the retained call field alone is not enough to know which lazy recipe to reconstruct.

The supported selector lazy kinds are:

- `container.rootfs`
- `container.directory`
- `container.file`

These recipes store the parent container result ID, plus the selected path for `container.directory` and `container.file`. They do not evaluate the container or materialize snapshots during shutdown persistence.

Directory and File decoders require `lazyKind` for lazy forms. This is a hard persistence schema cut: older persisted lazy payloads without `lazyKind` are not decoded.

## Performance and Correctness Notes

- Lazy evaluation defers validation, snapshot reopening, and other heavyweight work until the object is actually needed.
- `Peek` is intentionally used throughout lifecycle, accounting, and persistence code to avoid triggering expensive materialization from read-only bookkeeping paths.
- `AttachDependencies` should describe the real structural dependencies even if the lazy callback will not immediately materialize them.
- Lazy callbacks usually begin by evaluating the parent/source results they truly need right now.
- Detached results cannot participate in lazy evaluation.
- A newly created `DoNotCache` result cannot be lazy.
- Successful evaluation should leave the object in a plain materialized shape, with accessors populated and `Lazy` cleared.
- Failed evaluation leaves the result pending so a future caller can retry.

## Reading the Code

If you are trying to understand or modify this system, this is a good reading order:

1. `dagql/cache.go`
   - `registerLazyEvaluation`
   - `HasPendingLazyEvaluation`
   - `Cache.Evaluate`
   - `Cache.evaluateOne`
2. `core/lazy_state.go`
   - `Lazy`
   - `LazyState`
   - `LazyAccessor`
3. `core/directory.go`
   - `Directory.LazyEvalFunc`
   - `DirectorySubdirectoryLazy`
   - `DirectoryWithoutLazy`
4. `core/file.go`
   - `File.LazyEvalFunc`
   - `FileSubfileLazy`
5. `core/container.go`
   - `Container.LazyEvalFunc`
   - `materializeContainerStateFromParent`
   - `ContainerRootFSLazy`
   - `ContainerDirectoryLazy`
   - `ContainerWithDirectoryLazy`
   - `ContainerWithFileLazy`
   - `ContainerWithUnixSocketLazy`
6. `core/schema/container.go`, `core/schema/directory.go`, `core/schema/file.go`
   - the shell-construction patterns that install these lazy recipes in the first place

## Mental Model

The cleanest mental model is:

- dagql cache owns when lazy work runs, how many times it runs, what call/trace context it runs under, and how callers synchronize on it
- `core` owns what the deferred work actually is
- `LazyAccessor` is the safety boundary that makes consumers go through evaluation instead of casually reaching into half-materialized state

If you keep those three layers distinct, the implementation becomes much easier to reason about.
