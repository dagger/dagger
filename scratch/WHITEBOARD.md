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

# Lease Fixes

## Constraints
* Dagql result owner leases are now the only intended long-lived snapshot lifetime.
* The snapshots package should only provide short-lived protection for in-progress work.
* We do **not** want to call `Snapshotter.Remove` from dagql `OnRelease` / prune release callbacks.
  * Verified: `runOnReleaseFuncs` runs after `egraphMu.Unlock()`, so this is not under the e-graph lock.
  * However it is still a serial release loop on the prune/session-teardown path, so doing real snapshot deletion there would still make prune/session close heavy and serialized in exactly the way we do not want.
  * We want `OnRelease` to stay cheap and let the explicit containerd GC pass at the end of prune do the actual snapshot removals.
* User confirmed design constraint: no snapshot should outlive a dagql-owned object except as short-lived in-progress work. Any code path that needs a snapshot to outlive an operation without dagql ownership is wrong.

## Verified Current State
* Dagql result-owner lease leak is fixed.
* After full prune, dagql has `0` live `snapshot_links` and `0` live `dagql/result/...` owner leases.
* Remaining disk state is in containerd/snapshotter metadata:
  * snapshotter metadata DB has `1092` committed snapshots
  * containerd metadata DB has `1092` overlay snapshots
  * containerd metadata DB has `2814` opaque flat leases
  * `584` committed snapshots are held **only by handle leases**
  * one committed snapshot currently has `340` leases on it
* There are `0` `-view` snapshots left, so this is not a mount-view leak.
* Containerd flat snapshot leases still traverse the parent edge in GC, so one lease on a leaf snapshot is enough to retain the whole parent chain. No recursive parent snapshot leasing is needed.

## Cutover Plan
### 1. Add a dagql operation-lease hook
Files:
* `dagql/operation_lease.go` (new)

Purpose:
* Give dagql a generic way to ask for a short-lived lease around active evaluation without importing `core`.
* The hook should no-op if a lease is already present in the context, so nested evaluation does not create redundant leases.

Sketch:
```go
package dagql

import (
    "context"

    "github.com/containerd/containerd/v2/core/leases"
)

type OperationLeaseProvider interface {
    WithOperationLease(context.Context) (context.Context, func(context.Context) error, error)
}

type OperationLeaseProviderFunc func(context.Context) (context.Context, func(context.Context) error, error)

func (f OperationLeaseProviderFunc) WithOperationLease(ctx context.Context) (context.Context, func(context.Context) error, error) {
    return f(ctx)
}

type operationLeaseProviderKey struct{}

func ContextWithOperationLeaseProvider(ctx context.Context, p OperationLeaseProvider) context.Context {
    return context.WithValue(ctx, operationLeaseProviderKey{}, p)
}

func withOperationLease(ctx context.Context) (context.Context, func(context.Context) error, error) {
    if _, ok := leases.FromContext(ctx); ok {
        return ctx, func(context.Context) error { return nil }, nil
    }
    p, _ := ctx.Value(operationLeaseProviderKey{}).(OperationLeaseProvider)
    if p == nil {
        return ctx, func(context.Context) error { return nil }, nil
    }
    return p.WithOperationLease(ctx)
}
```

### 2. Install the operation-lease provider in request/session context setup
Files:
* `engine/server/session.go`

Sites:
* the client initialization path around `core.ContextWithQuery(...)`
* the HTTP request serving path around `core.ContextWithQuery(...)`

Purpose:
* All dagql field resolution and lazy callbacks for a request should be able to acquire a short-lived engine lease from context.
* The provider should reuse an existing lease from context if one is already installed.

Sketch:
```go
ctx = dagql.ContextWithOperationLeaseProvider(ctx, dagql.OperationLeaseProviderFunc(
    func(ctx context.Context) (context.Context, func(context.Context) error, error) {
        if _, ok := leases.FromContext(ctx); ok {
            return ctx, func(context.Context) error { return nil }, nil
        }
        return leaseutil.WithLease(ctx, srv.leaseManager, leaseutil.MakeTemporary)
    },
))

ctx = core.ContextWithQuery(ctx, client.dagqlRoot)
```

### 3. Wrap field resolver execution in a temporary operation lease
Files:
* `dagql/server.go`

Site:
* `resolvePath`, immediately before `self.Select(...)`

Purpose:
* Any snapshot/content work performed while resolving a field should be protected by a short-lived lease.
* Nested sub-selections should reuse the same lease through context.

Sketch:
```go
leaseCtx, release, err := withOperationLease(ctx)
if err != nil {
    return nil, err
}
defer release(context.WithoutCancel(leaseCtx))

val, err := self.Select(leaseCtx, s, sel.Selector)
if err != nil {
    return nil, err
}
```

### 4. Wrap lazy callbacks and owner-lease attach in the same temporary operation lease
Files:
* `dagql/cache.go`

Site:
* the goroutine in `evaluateOne` that currently runs `lazyEval(callbackCtx)` and then `syncResultSnapshotLeases(...)`

Purpose:
* Protect snapshots during lazy materialization.
* Keep the temporary lease alive across the exact critical window:
  * lazy callback starts
  * snapshot is created / reopened / imported
  * dagql owner lease is attached
* If a lease already exists in context, reuse it.

Sketch:
```go
go func() {
    callbackCtx := evalCtx
    ...
    leaseCtx, release, err := withOperationLease(callbackCtx)
    if err != nil {
        ...
    }
    callbackCtx = leaseCtx
    defer release(context.WithoutCancel(callbackCtx))

    err = lazyEval(callbackCtx)
    if err == nil {
        err = c.syncResultSnapshotLeases(callbackCtx, shared, "lazy_eval_complete")
    }
    ...
}()
```

### 5. Remove long-lived ref-owned handle leases from reopened refs
Files:
* `engine/snapshots/manager.go`

Sites:
* `get`
* `GetMutable`
* `GetMutableBySnapshotID`
* remove `newHandleLease`

Purpose:
* Reopening an immutable or mutable snapshot should not mint a persistent opaque lease anymore.
* Active-use protection comes from the dagql operation lease already present in context.

Changes:
* Delete `newHandleLease`.
* Stop creating a lease in `get`, `GetMutable`, and `GetMutableBySnapshotID`.
* Reopened refs just carry snapshot metadata and last-used tracking.

Sketch:
```go
ref := &immutableRef{
    cm:              cm,
    refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
    triggerLastUsed: triggerUpdate,
}
```

### 6. Remove long-lived committed-snapshot leases from creation paths
Files:
* `engine/snapshots/manager.go`
* `engine/snapshots/refs.go`

Sites:
* `New`
* `ApplySnapshotDiff`
* `Merge`
* `mutableRef.commit`

Purpose:
* Final immutable snapshots should not keep their own opaque flat lease forever.
* Long-lived ownership comes from dagql owner leases once the object is materialized and attached.
* In-progress safety comes from the operation lease already on the context.

Changes:
* Delete `LeaseManager.Create + AddResource` for the final immutable snapshot.
* The committed immutable ref returned from `Commit` / `Merge` / `ApplySnapshotDiff` should no longer carry a lease ID.
* For local failure cleanup while still inside the active operation, direct `Snapshotter.Remove` is still acceptable because that is not the dagql release path.

Representative shape:
```go
snapshotID := identity.NewID()

if err := cm.Snapshotter.Merge(ctx, snapshotID, diffs); err != nil {
    return nil, errors.Wrap(err, "failed to merge snapshots")
}

ref := &immutableRef{
    cm:              cm,
    refMetadata:     refMetadata{snapshotID: rec.md.getSnapshotID(), md: rec.md},
    triggerLastUsed: true,
}
```

### 7. Simplify `cacheRecord.remove` into metadata-only drop; no backend removal on release path
Files:
* `engine/snapshots/refs.go`
* callsites in `engine/snapshots/manager.go` and `engine/snapshots/refs.go`

Purpose:
* With ref-owned persistent leases removed, `cacheRecord.remove` no longer needs to decide whether to delete a snapshot lease.
* Actual backend snapshot deletion should remain deferred to containerd GC.
* This should be a hard-cut simplification: remove the `removeSnapshot bool` parameter entirely.

Changes:
* Replace `remove(ctx, removeSnapshot bool)` with `remove(ctx)`.
* It should:
  * delete the in-memory record
  * clear the snapshot-manager metadata store entry
  * not call `Snapshotter.Remove`
  * not call `LeaseManager.Delete`

Sketch:
```go
func (cr *cacheRecord) remove(ctx context.Context) (rerr error) {
    delete(cr.cm.records, cr.md.ID())
    cr.cm.metadataStore.clear(cr.md.ID())
    return nil
}
```

Callsite adjustments:
* mutable-abandon and stale-record paths now call `rec.remove(ctx)`
* no caller should expect `cacheRecord.remove` to do backend deletion

### 8. Remove `leaseID` from `immutableRef` and `mutableRef`
Files:
* `engine/snapshots/refs.go`
* tests that construct refs directly

Purpose:
* Refs should stop pretending to own long-lived snapshot leases.
* Keep validity tracking explicit rather than overloaded on a lease ID.

Changes:
* Remove `leaseID string` from both structs.
* Replace invalidity checks based on `leaseID == ""` with an explicit `released bool` if needed.
* Remove `leaseID` from trace fields.

Sketch:
```go
type immutableRef struct {
    cm *snapshotManager
    refMetadata
    released        bool
    mu              sync.Mutex
    mountCache      snapshot.Mountable
    triggerLastUsed bool
    sizeG           flightcontrol.Group[int64]
}
```

### 9. Make ref release cheap and local
Files:
* `engine/snapshots/refs.go`

Sites:
* `immutableRef.release`
* `mutableRef.Release`
* `mutableRef.commit`

Purpose:
* Releasing a ref should stop mutating containerd lease state except for short-lived local leases that still genuinely belong to that ref.
* In this cutover, normal immutable/mutable refs will not own any long-lived lease.

Changes:
* `immutableRef.release` should just update last-used if needed, clear local mount cache, mark released, and return.
* `mutableRef.Release` should clear local state, unlock the record, and drop snapshot-manager metadata via `rec.remove(ctx)`.
* `mutableRef.commit` should stop deleting any immutable lease or mutable lease because neither should exist as ref-owned long-lived state anymore.

Representative shape:
```go
func (sr *immutableRef) release(ctx context.Context) error {
    if sr.released {
        return nil
    }
    if sr.updateLastUsedNow() {
        if err := sr.md.updateLastUsed(); err != nil {
            return err
        }
    }
    sr.mountCache = nil
    sr.released = true
    return nil
}
```

### 10. Replace ref-lease content attachment with context-lease protection
Files:
* `engine/snapshots/pull.go`

Sites:
* `importImageLayer`
* `ImportImage`
* replace `linkContentToRefLease`

Purpose:
* Today top-level image content is attached to the immutable ref’s opaque lease.
* After removing ref-owned leases, the temporary operation lease should be the short-lived owner until dagql owner leases are attached.
* Dagql owner-lease attachment already backfills known content digests from `recordSnapshotContent`.

Changes:
* Delete `linkContentToRefLease`.
* Add a helper that attaches content to the current lease from context if one exists, otherwise no-op.
* Keep `recordSnapshotContent` exactly because it is how owner leases pick up content later.

Sketch:
```go
func (cm *snapshotManager) linkContentToContextLease(ctx context.Context, desc ocispecs.Descriptor) error {
    if desc.Digest == "" {
        return nil
    }
    l, ok := leases.FromContext(ctx)
    if !ok {
        return nil
    }
    return cm.LeaseManager.AddResource(ctx, l, leases.Resource{
        ID:   desc.Digest.String(),
        Type: "content",
    })
}
```

Then:
```go
if err := cm.linkContentToContextLease(ctx, desc); err != nil {
    return nil, err
}
if err := cm.recordSnapshotContent(ref.SnapshotID(), desc); err != nil {
    return nil, err
}
```

### 11. Remove resolver-local temporary lease wrappers that become redundant
Files:
* `core/builtincontainer.go`
* `core/container_image.go`
* `core/schema/host.go`

Sites:
* the `leaseutil.WithLease(...)` wrappers around builtin image copy/import/host image copy

Purpose:
* Once dagql field resolvers and lazy callbacks run under the generic operation lease, these local wrappers become redundant.
* Keep the model cohesive: resolver work gets its temp lease from dagql, not ad hoc at random callsites.

Changes:
* Delete the local `leaseutil.WithLease(...)` setup in those files.
* Use the incoming context directly.

### 12. Keep internal short-lived helper leases, but make them fallback-only
Files:
* `engine/snapshots/remote.go`
* `engine/snapshots/blobs.go`
* `engine/snapshots/snapshotter/merge.go`
* `engine/snapshots/snapshotter/diffapply_linux.go`

Purpose:
* These are not persistent ownership; they are short-lived internal worker/mount/content helpers.
* They may still need a temp lease in non-dagql contexts.
* But if a lease is already in context, they should reuse it instead of minting another.

Changes:
* Gate all local `leaseutil.WithLease(...)` creation on `leases.FromContext(ctx)`.

Sketch:
```go
if _, ok := leases.FromContext(ctx); !ok {
    leaseCtx, done, err := leaseutil.WithLease(ctx, lm, leaseutil.MakeTemporary)
    if err != nil {
        return err
    }
    defer done(context.WithoutCancel(leaseCtx))
    ctx = leaseCtx
}
```

### 13. Tests and instrumentation updates
Files:
* `engine/snapshots/manager_test.go`
* any snapshot tests that directly construct refs or assert lease-manager behavior

Purpose:
* Existing tests assume ref-owned leases and direct `leaseID` fields.
* Rewrite them around the new model:
  * no persistent lease on reopened immutable refs
  * no persistent lease on committed snapshots
  * metadata-only local remove path
  * containerd GC / lease-manager behavior tested separately where still relevant

Specific updates:
* remove `leaseID` from test ref constructors
* stop asserting `LeaseManager.Create/Delete` in immutable reopen paths
* keep temporary helper lease tests only where those helpers still own the lease

## Expected Success Condition
After the cutover, on an idle engine after `engine local-cache prune`:
* dagql snapshot links: `0`
* `dagql/result/...` owner leases: `0`
* opaque flat snapshot leases in `containerdmeta.db`: `0`
* committed snapshots in snapshot metadata DB: `0`

If the system still leaves opaque flat leases around after dagql links are gone, then we still have a snapshots-package lifetime bug.
