# WHITEBOARD

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

# Secrets + Sockets

## Design
### Goals
* Do a complete hard cut from the current secret/socket model, which is heavily shaped by older buildkit-era assumptions.
* Reassess the problem from zero using the current dagql cache model rather than preserving the old stores / transfer / replay mechanics.
* Keep the design centered on one simple invariant:
  * a cache hit on an operation that depends on a session resource is only valid if the current session has already loaded an equivalent resource
* Never replay a resource provider during cache lookup.
  * Cache lookup must not go read `env://FOO`, `file:///...`, `cmd://...`, Vault, libsecret, AWS, 1Password, or anything similar.
  * Cache lookup only checks current-session availability of an already-loaded equivalent resource.

### Session Scope
* Start with **session-scoped** secret/socket availability, not client-scoped.
* This is the simplest coherent replacement for the old transfer logic.
* Nested clients in the same session should naturally share the same loaded-resource availability set.
* If this ends up being too coarse later, we can revisit finer scoping, but the first hard cut should be session-based.

### Resource Categories
#### Secrets
* `setSecret`
  * in-memory, one-off
  * never reproducible from recipe
  * never persisted to disk
  * cacheable by plaintext content hash while it exists in memory for the session
* provider secret
  * sourced from client-facing providers (`env://`, `file://`, `cmd://`, Vault, libsecret, AWS, etc.)
  * plaintext never written to disk
  * provider recipe/source is not trusted for cache-hit validity
  * cacheability and equivalence should be based on effective content hash, not provider identity

#### Sockets
* SSH auth socket
  * partially reproducible in the sense that the current client/session can expose an SSH socket and we can inspect agent identities
  * the meaningful cache identity is the fingerprint set, not path/session metadata
  * the socket object itself should not be trusted as persistable/replayable state
* arbitrary Unix socket
  * unreproducible and effectively opaque
  * no meaningful stable cache identity beyond "some socket was mounted"
  * keep this per-call and uncacheable rather than trying to model an abstract cacheable handle for it
* host IP / port-forward sockets
  * these are also external-service-shaped and should be treated closer to arbitrary sockets than SSH sockets
  * simplest first cut: keep them per-call and uncacheable

### Session resource handles
* The graph should carry **abstract session resource handles** for the resource categories where caching/equivalence matters.
* For the first cut, this means:
  * secrets use abstract handles
  * SSH sockets use abstract handles
  * arbitrary Unix sockets and host-IP/forwarded sockets do **not** use abstract cacheable handles; they stay per-call and uncacheable
* A concrete loaded secret/SSH socket is session-local runtime state.
* The abstract handle should include an explicit cache-facing identity:

```go
type SessionResourceHandle string
```

* The abstract handle object in the graph carries that `SessionResourceHandle`.
* Once that abstract handle result becomes attached/cache-backed, the cache should store the same handle string explicitly on its `sharedResult`.
* That handle string, not a `*sharedResult` pointer, is what downstream cached objects actually depend on and what cache lookup reasons about.
* The cache owns the mapping:
  * session + `SessionResourceHandle` -> one or more attached concrete current-session resource instances
* The cache also owns the flattened dependency summary:
  * shared result -> `SessionResourceHandle`s required directly or indirectly

### Core Invariant
* If an operation structurally depends on an abstract session resource handle and the cache finds an otherwise-valid candidate hit, the hit is only accepted if the current session already has a concrete resource bound for an equivalent handle.
* "Equivalent" must mean "same equivalence identity for this resource category", not "same recipe" or "same provider/source metadata".
* This invariant should be checked during cache-hit validation, not deferred until later execution.
* This avoids the bad state where an early cache hit smuggles a resource dependency through the graph and a later uncached operation fails because the current session never actually loaded the resource.

### Resource Identity / Equivalence
#### Secrets
* `setSecret`
  * change identity to be based on plaintext content hash, using the same Argon-derived content-hash style already used for provider secrets
  * the user-visible name is metadata, not identity
  * current code is wrong here: `setSecret` is currently keyed by `name + accessor` rather than plaintext content
* provider secret
  * default identity is content hash derived from plaintext
  * the provider URI itself is not the cache identity
  * two different providers producing the same plaintext should be equivalent for cache-hit purposes
* `secret(uri, cacheKey)`
  * can remain for now as an escape hatch that overrides the content key
  * conceptually this is just "user overrides the effective content identity"
  * it is a foot gun if misused, but that is a pre-existing issue and not inherently in conflict with the new model
  * if it proves awkward later, it can still be deleted

#### Sockets
* SSH auth socket
  * equivalence should be based on SSH agent fingerprints
  * current `ScopedSSHAuthSocketDigest` already points in this direction
* arbitrary Unix socket
  * no equivalence class worth trusting
  * consumers should be uncacheable
* host IP / forwarded sockets
  * treat as uncached/external for the first cut

### What Gets Persisted
* Concrete session-local secret/socket resource instances should not be trusted as persisted/replayable values.
  * `setSecret`: no
  * provider secret: no
  * SSH socket: no, at least for the first cut
  * arbitrary Unix socket: definitely no
* Abstract session resource handles may still appear in persisted/cached downstream results.
* The `Secret` and `Socket` object types themselves should own their persisted form.
  * That is cleaner than teaching every consumer type to special-case their internals.
  * Their persisted form should omit concrete session-local fields and preserve only the handle-level shape when appropriate.
* Downstream results that depended on them **can** still be persistable/cacheable, as long as hit validity is gated by the current session already having loaded an equivalent resource binding for the handle.
* If persistence encounters a concrete provider/path/session-local self payload that should never have been persisted:
  * do not fail persistence just for that
  * instead, omit the concrete session-local fields from the persisted self payload and persist the surrounding object/result anyway
  * if the rest of the design is correct, those persisted concrete fragments should never become valid cache hits on their own
* In other words:
  * "can this concrete resource instance be replayed from recipe?" and
  * "can a cached downstream result depending on it be reused?"
  are separate questions and should not be conflated.

### Availability and resolution
* Replace the old transfer/replay/store mindset with a cache-owned generic session resource registry.
* The cache should not model separate "secret store" and "socket store" concepts anymore.
* Instead it should track generic session resource bindings:
  * session -> `SessionResourceHandle` -> attached concrete current-session resource instance(s)
* When a session creates or loads a secret or SSH socket resource object, the constructor should do two explicit things:
  * stamp the abstract handle result with its `SessionResourceHandle`
  * attach/cache-retain the concrete resource instance for the session and bind it under that handle
* Cache lookup for dependent operations checks whether a binding exists for each required handle before accepting a hit.
* Execution-time consumers resolve the abstract handle through the cache to a concrete current-session resource instance.
* Opaque per-call sockets sit outside this abstract-handle registry because they are intentionally uncacheable.

### Execution-Time Resolution
* The cache-hit validity check must **not** go to providers.
* But when an operation actually executes, it still needs the concrete current-session secret/socket material.
* The replacement model should therefore allow:
  * "is there a current-session binding for this abstract handle?" for cache-hit validation
  * "resolve this abstract handle to a concrete current-session resource" for execution
* For secrets, this should be simple:
  * resolve the abstract handle to the concrete secret for the current session
  * then call the concrete secret methods already needed for execution, such as `Plaintext`
* For SSH sockets, execution similarly resolves the abstract handle to the concrete current-session SSH socket material.
* Opaque sockets remain direct per-call execution inputs and do not participate in this resolution path.

### Consumer-Specific Behavior
#### Container withExec
* Secret env vars and secret file mounts:
  * cacheable
  * cache identity based on secret handle equivalence (content hash or cache-key override)
  * cache hit valid only if current session already loaded an equivalent resource handle binding
* SSH socket mounts:
  * cacheable
  * cache identity based on SSH handle equivalence (fingerprint digest)
  * cache hit valid only if current session already loaded an equivalent resource handle binding
* arbitrary Unix socket mounts:
  * uncacheable
* This should apply both to direct exec and to any other execution path that materializes secret/socket mounts.

#### Git
* HTTP auth token / header:
  * cacheable when the current session has already loaded an equivalent secret
  * cache hit must not bypass the requirement that the session actually has that secret available
* SSH auth socket:
  * cacheable when the current session has already loaded an equivalent SSH socket
* No provider replay during cache hit.
* If the session has not already loaded the equivalent secret/socket, the operation should execute normally instead of hitting cache.

#### Registry auth
* `withRegistryAuth` / `withoutRegistryAuth` are historically weird and do not fit the otherwise explicit container-state model.
* Do **not** try to normalize that API in this cut.
* The only required change for now is:
  * when `withRegistryAuth` needs the secret plaintext, it should use the new secret API and resolve it correctly for the current session
* We are deliberately not redesigning registry auth into explicit container graph state in this cut.

### Current-Code Mismatches To Replace
#### Secrets
* [core/schema/secret.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/secret.go)
  * `setSecret` currently derives identity from `name + accessor`
  * this must switch to plaintext-content-based identity
  * `secret(uri, cacheKey)` currently uses provider plaintext hashing by default, with manual override via `cacheKey`
* [core/secret.go](/home/sipsma/repo/github.com/sipsma/dagger/core/secret.go)
  * current `SecretStore` is a store-shaped runtime object keyed by canonical digest with recipe aliases
  * this whole approach should be replaced by cache-owned abstract-handle bindings + session resolution, and the store abstraction itself should be deleted

#### Sockets
* [core/schema/host.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/host.go)
  * `host.unixSocket` currently derives a digest from an accessor/path, which makes arbitrary sockets look cacheable when they should not be
  * `_sshAuthSocket` already uses fingerprint scoping, which is closer to the desired model
* [core/socket.go](/home/sipsma/repo/github.com/sipsma/dagger/core/socket.go)
  * current `SocketStore` is a store-shaped runtime mapping from digest to concrete socket metadata and aliasing
  * this should be replaced by:
    * cache-owned abstract-handle binding for SSH sockets
    * direct per-call handling for opaque sockets
  * the store abstraction itself should be deleted

#### Git
* [core/schema/git.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/git.go)
  * currently carries auth/socket state explicitly on `GitRepository`, which is good
  * but still has special shaping around default/scoped SSH socket injection and auth-token creation that needs reevaluation under the new hit-gating model
* [core/git_remote.go](/home/sipsma/repo/github.com/sipsma/dagger/core/git_remote.go)
  * currently resolves auth/socket material from stores at execution time
  * the new model should still resolve concrete session-local resources at execution time, but through cache-owned abstract-handle resolution rather than old store/transfer assumptions

#### Registry auth
* [core/schema/container.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/container.go)
  * `withRegistryAuth` is currently a side-effecting path that loads the secret and mutates auth provider state
  * we are intentionally leaving that odd shape in place for now
  * the only hard-cut change in this area is that it must use the new secret plaintext API rather than the old store lookup

### Non-Goals For The First Cut
* Do not preserve the old transfer model.
* Do not replay providers during cache lookup.
* Do not try to make arbitrary Unix sockets cacheable.
* Do not try to make secret/socket objects themselves replayable from persisted recipe/disk state.

### Design Direction
* The likely hard cut is:
  * explicit resource-bearing objects still appear in the DAG
  * secret and SSH-socket constructors attach/cache-retain concrete session-local resources under abstract handles
  * downstream cached objects depend on abstract session resource handles
  * cache owns session-scoped handle->resource bindings
  * cache hit validity checks for required bound handles
  * execution-time consumers resolve concrete current-session resources through the cache
  * opaque sockets stay per-call and uncacheable rather than entering the abstract-handle caching model
  * old store/transfer/replay logic is deleted rather than adapted

### Follow-Up Design Work
* Define the exact cache-owned session resource registry shape.
* Decide how resource kinds are tagged/classified so cache lookup knows:
  * content-hash secret
  * SSH fingerprint socket
  * opaque/un-cached socket
* Rework `setSecret` identity to plaintext-content hashing.
* Decide whether the old `cacheKey` override on provider secrets survives permanently or gets cut later.

## Implementation Plan
### Preliminary choices for the first implementation
* Scope resource availability by **session**, not client.
* Keep `secret(uri, cacheKey)` for now and treat it as overriding the effective equivalence/content key.
* Rework `setSecret` to use the same Argon-derived plaintext content hash as provider secrets.
* Treat `Nullable<user module object>` the same as a user module object for workspace-return content hashing.
* Prefer a generic **SessionResource**-style type/result interface over cache-internal special casing by "secret" vs "socket".
* Do not model separate secret/socket stores in the cache.
* The cache should traffic in generic abstract handles and current-session bindings.

### dagql/cache.go
#### Session resource registry
* Add one generic session-resource registry under `sessionMu`.
* This registry needs to answer two questions:
  * **availability**: does this session currently have any concrete binding for this abstract handle?
  * **resolution**: if execution actually needs the resource, which concrete current-session instance do we use?
* The concrete shape should be explicit in `Cache` rather than implied by comments:

```go
type SessionResourceHandle string

type Cache struct {
    // existing fields...

    sessionResultIDsBySession         map[string]map[sharedResultID]struct{}
    sessionArbitraryCallKeysBySession map[string]map[string]struct{}
    sessionLazySpansBySession         map[string]map[sharedResultID]trace.SpanContext

    // new: generic session-resource bindings
    sessionResourcesBySession map[string]map[SessionResourceHandle]*set.TreeSet[*sharedResult]
    sessionHandlesBySession   map[string]*set.TreeSet[SessionResourceHandle]
}
```

* The keys and values in `sessionResourcesBySession` are:
  * map key = explicit `SessionResourceHandle`
  * tree-set members = concrete current-session bound resources for that handle
* `*sharedResult` is still fine for the concrete bound resources in the tree-set because those are actual attached cache objects.
* But the top-level map key should be the explicit handle string, not a pointer-keyed map.
* The tree-set comparator can stay simple and deterministic:

```go
func compareSharedResults(a, b *sharedResult) int {
    switch {
    case a == nil && b == nil:
        return 0
    case a == nil:
        return -1
    case b == nil:
        return 1
    case a.id < b.id:
        return -1
    case a.id > b.id:
        return 1
    default:
        return 0
    }
}
```

* This gives us stable de-dup and deterministic selection for concrete resources without inventing more identity machinery up front.
* When multiple concrete resources bind to the same handle, the cache just chooses one deterministically wherever a single concrete resource is needed.
  * That is acceptable for `plaintext`, `name`, `uri`, and execution use because equivalent resources are intentionally interchangeable.
* `ReleaseSession` must delete both:
  * `sessionResourcesBySession[sessionID]`
  * `sessionHandlesBySession[sessionID]`
  alongside the other session state.
* The registry itself should not be the thing that keeps results alive.
  * Instead, the binding helper should call the existing `trackSessionResult` path on the concrete bound resource so session teardown keeps working the same way.
* Maintaining `sessionHandlesBySession` in parallel is worth it here because cache-hit gating sits in hot lookup paths and should not need to rebuild the "available handles" set over and over under lock.

#### sharedResult resource requirements
* Add a flattened abstract session-resource requirement summary onto `sharedResult`.
* This should live directly on the cache-owned result state:

```go
type sharedResult struct {
    // existing fields...
    deps map[sharedResultID]struct{}

    sessionResourceHandle    SessionResourceHandle
    requiredSessionResources *set.TreeSet[SessionResourceHandle]
}
```

* `sessionResourceHandle` is only set on an attached abstract-handle leaf result.
* `requiredSessionResources` should mean exactly:
  * every `SessionResourceHandle` this result depends on, directly or transitively
* This must be precomputed eagerly while materializing dependencies.
* Cache-hit validation should never need to recursively walk the graph to rediscover these requirements.
* The set should contain only handle strings, never concrete session-local resource instances.
* If the result itself is an abstract session-resource handle, then `sessionResourceHandle` should be set and the requirement set should include that handle.

#### Materialization-time requirement derivation
* Extend `initCompletedResult` and explicit-dependency wiring so `requiredSessionResources` is always up to date on attached results.
* The key rule is:
  * if a result embeds abstract session-resource handles anywhere in its attached dependency graph, the parent `sharedResult` must record those handles explicitly and transitively
* The simplest concrete helper here is:

```go
func compareSessionResourceHandles(a, b SessionResourceHandle) int {
    switch {
    case a < b:
        return -1
    case a > b:
        return 1
    default:
        return 0
    }
}

func (c *Cache) recomputeRequiredSessionResourcesLocked(res *sharedResult) error {
    var reqs *set.TreeSet[SessionResourceHandle]

    if res.sessionResourceHandle != "" {
        reqs = set.NewTreeSet(compareSessionResourceHandles)
        reqs.Insert(res.sessionResourceHandle)
    }
    for depID := range res.deps {
        dep := c.resultsByID[depID]
        if dep == nil {
            return fmt.Errorf("missing dep result %d", depID)
        }
        if dep.requiredSessionResources != nil {
            if reqs == nil {
                reqs = dep.requiredSessionResources.Copy()
            } else {
                reqs.Union(dep.requiredSessionResources)
            }
        }
    }
    if reqs == nil || reqs.Size() == 0 {
        res.requiredSessionResources = nil
    } else {
        res.requiredSessionResources = reqs
    }
    return nil
}
```

* The important thing is not to hide this in some generic abstraction. The algorithm should be visible:
  * start with `res.sessionResourceHandle` if one is set
  * union each dep's already-flattened requirement set
* In `initCompletedResult`, the sequencing should be:
  1. materialize `oc.res`
  2. add any `resultCallDeps` into `oc.res.deps`
  3. call `recomputeRequiredSessionResourcesLocked(oc.res)` before dropping `egraphMu`
  4. later, when `attachDependencyResults` adds explicit child deps through `AddExplicitDependency`, recompute again there
* That means `addExplicitDependencyLocked` should end with:

```go
parentRes.deps[depRes.id] = struct{}{}
c.incrementIncomingOwnershipLocked(ctx, depRes)
if err := c.recomputeRequiredSessionResourcesLocked(parentRes); err != nil {
    return err
}
```

* This keeps the flattened summary correct no matter whether the dep arrived from:
  * result-call refs collected in `initCompletedResult`
  * attached dependency results added later through `AddExplicitDependency`
* The first implementation should compute this eagerly and straightforwardly.
  * If we later need to optimize incremental updates, that can be a follow-up once the behavior is proven correct.

#### Result / ObjectResult handle plumbing
* Do **not** expose a public cache method just for stamping the handle onto a result.
* Instead, follow the same pattern as `WithContentDigest`:
  * `Result[T].WithSessionResourceHandle(ctx, handle) (Result[T], error)`
  * `Result[T].WithSessionResourceHandleAny(ctx, handle) (AnyResult, error)`
  * `ObjectResult[T].WithSessionResourceHandle(ctx, handle) (ObjectResult[T], error)`
  * `ObjectResult[T].WithSessionResourceHandleAny(ctx, handle) (AnyResult, error)`
* The interface surface in `dagql/types.go` should mirror the existing content-digest pattern:

```go
type AnyResult interface {
    // existing methods...
    WithSessionResourceHandleAny(context.Context, SessionResourceHandle) (AnyResult, error)
}
```

* Attached results should mutate through the singleton cache, not through detached payload cloning.
* Detached results should stay detached and just return a detached clone with:
  * `sharedResult.sessionResourceHandle = handle`
  * `sharedResult.requiredSessionResources = {handle}`
* The attached `Result[T]` shape should be:

```go
func (r Result[T]) WithSessionResourceHandle(ctx context.Context, handle SessionResourceHandle) (Result[T], error) {
    if handle == "" {
        return r, fmt.Errorf("set session resource handle on %T: empty handle", r.Self())
    }
    if r.shared == nil {
        return r, fmt.Errorf("set session resource handle on %T: missing shared result", r.Self())
    }
    if r.shared.id != 0 {
        cache, err := EngineCache(ctx)
        if err != nil {
            return r, fmt.Errorf("set session resource handle on %T: current dagql cache: %w", r.Self(), err)
        }

        cache.egraphMu.Lock()
        defer cache.egraphMu.Unlock()

        cached := cache.resultsByID[r.shared.id]
        if cached == nil {
            return r, fmt.Errorf("set session resource handle on %T: missing cached result %d", r.Self(), r.shared.id)
        }
        cached.sessionResourceHandle = handle
        if err := cache.recomputeRequiredSessionResourcesLocked(cached); err != nil {
            return r, err
        }
        return r, nil
    }

    state := r.shared.loadPayloadState()
    frame := r.shared.loadResultCall()
    r.shared = &sharedResult{
        self:                   state.self,
        isObject:               state.isObject,
        resultCall:             cloneResultCall(frame),
        hasValue:               state.hasValue,
        persistedEnvelope:      state.persistedEnvelope,
        persistedSnapshotLinks: slices.Clone(r.shared.persistedSnapshotLinks),
        createdAtUnixNano:      state.createdAtUnixNano,
        lastUsedAtUnixNano:     state.lastUsedAtUnixNano,
        sizeEstimateBytes:      r.shared.sizeEstimateBytes,
        usageIdentity:          r.shared.usageIdentity,
        description:            r.shared.description,
        recordType:             r.shared.recordType,
        sessionResourceHandle:  handle,
        requiredSessionResources: singletonSessionResourceHandleSet(handle),
    }
    return r, nil
}
```

* `ObjectResult[T]` should just wrap the `Result[T]` version the same way `WithContentDigest` already does.

#### Session registration / resolution helpers
* Add explicit cache methods for:
  * binding a concrete current-session resource under an abstract handle
  * checking whether a session satisfies a result's requirement set
  * resolving a concrete current-session resource from an abstract handle
* Keep these APIs generic over session-resource classification rather than branching on secret/socket in the cache itself.
* The likely first-cut helper shapes are:

```go
func (c *Cache) BindSessionResource(
    ctx context.Context,
    sessionID string,
    handle SessionResourceHandle,
    concrete AnyResult,
) error

func (c *Cache) sessionSatisfiesResourceRequirementsLocked(
    sessionID string,
    res *sharedResult,
) bool

func (c *Cache) ResolveSessionResource(
    sessionID string,
    handle SessionResourceHandle,
) (AnyResult, error)
```

* `BindSessionResource` should do very explicit validation:
  * `sessionID` must be non-empty
  * `handle` must be non-empty
  * `concrete` must be attached
  * `concrete` must classify as a concrete resource instance of the same generic resource kind as the handle
* Then the body should be conceptually simple:

```go
func (c *Cache) BindSessionResource(ctx context.Context, sessionID string, handle SessionResourceHandle, concrete AnyResult) error {
    concreteShared := concrete.cacheSharedResult()

    c.trackSessionResult(ctx, sessionID, concrete, false)

    c.sessionMu.Lock()
    if c.sessionResourcesBySession == nil {
        c.sessionResourcesBySession = make(map[string]map[SessionResourceHandle]*set.TreeSet[*sharedResult])
    }
    if c.sessionHandlesBySession == nil {
        c.sessionHandlesBySession = make(map[string]*set.TreeSet[SessionResourceHandle])
    }
    if c.sessionResourcesBySession[sessionID] == nil {
        c.sessionResourcesBySession[sessionID] = make(map[SessionResourceHandle]*set.TreeSet[*sharedResult])
    }
    if c.sessionHandlesBySession[sessionID] == nil {
        c.sessionHandlesBySession[sessionID] = set.NewTreeSet(compareSessionResourceHandles)
    }
    bindings := c.sessionResourcesBySession[sessionID][handle]
    if bindings == nil {
        bindings = set.NewTreeSet(compareSharedResults)
        c.sessionResourcesBySession[sessionID][handle] = bindings
    }
    bindings.Insert(concreteShared)
    c.sessionHandlesBySession[sessionID].Insert(handle)
    c.sessionMu.Unlock()
    return nil
}
```

* `sessionSatisfiesResourceRequirementsLocked` should just walk `res.requiredSessionResources` and require that each handle has at least one bound concrete resource in `sessionResourcesBySession[sessionID]`.
* `ResolveSessionResource` should:
  * look up `sessionResourcesBySession[sessionID][handle]`
  * choose the first deterministic concrete resource from that tree set
  * wrap that `sharedResult` back into the correct typed/object result form
* `ResolveSessionResource` should not itself go to providers.
  * It is only cache/session-local resolution.
* If callers need typed helper layers such as `ResolveSecret` or `ResolveSSHSocket`, those should be thin callers above this generic cache path, not separate source-of-truth stores.

### dagql/cache_egraph.go
#### Cache-hit validity gate
* Add the session resource-availability check to cache-hit acceptance.
* The current lookup code only returns one deterministic hit candidate:
  * `firstResultDeterministicallyAtLocked`
  * `firstResultForTermSetDeterministicallyAtLocked`
  * `firstResultForOutputEqClassDeterministicallyAtLocked`
  * `lookupMatchForDigestsLocked`
  * `lookupMatchForCallLocked`
* That is no longer sufficient once hit validity depends on current-session resource availability.
* We need to switch these lookup helpers from "pick the first hit" to "enumerate deterministic candidates and then accept the first one that satisfies the session-resource gate".
* The concrete data shape should become:

```go
type lookupCandidate struct {
    res             *sharedResult
    hitRecipeDigest bool
}

type lookupMatch struct {
    selfDigest            digest.Digest
    inputDigests          []digest.Digest
    inputEqIDs            []eqClassID
    primaryLookupPossible bool
    missingInputIndex     int
    candidates            []lookupCandidate
    termDigest            string
    termSetSize           int
}
```

* `hitRecipeDigest` now belongs on the candidate, not on the aggregate match, because the first rejected candidate may differ from the accepted one.
* The new behavior should be:
  * enumerate deterministic candidates
  * validate each candidate's `requiredSessionResources` against the current session registry
  * accept the first candidate that satisfies the session-resource gate
  * only return a miss once all deterministic candidates are exhausted
* Favor correctness over performance for the first cut.
* Try to minimize expensive work inside lock critical sections where it is obvious how to do so cleanly.
* If avoiding extra work under the lock becomes tricky or forces design contortions, keep the logic correct first and leave a very explicit comment about the performance tradeoff and likely follow-up optimization seam.

#### Requirement identity
* Do not invent extra digest canonicalization in the cache here unless proven necessary.
* The preliminary plan is to store and compare explicit `SessionResourceHandle` strings directly.
* If we ever need to reason deeper about equivalence, we can always inspect the underlying abstract-handle result/type that produced that handle string.

#### Deterministic candidate enumeration helpers
* Replace the `firstResult*` helpers with append-style enumeration helpers that preserve the same deterministic ordering but do not stop at the first result.
* The simplest first-cut shapes are:

```go
func (c *Cache) appendResultsDeterministicallyAtLocked(
    dst []lookupCandidate,
    resultSet *set.TreeSet[sharedResultID],
    nowUnix int64,
    seen map[sharedResultID]struct{},
    hitRecipeDigest bool,
) []lookupCandidate

func (c *Cache) appendResultsForOutputEqClassDeterministicallyAtLocked(
    dst []lookupCandidate,
    outputEqID eqClassID,
    nowUnix int64,
    seen map[sharedResultID]struct{},
) []lookupCandidate

func (c *Cache) appendResultsForTermSetDeterministicallyAtLocked(
    dst []lookupCandidate,
    termSet *set.TreeSet[egraphTermID],
    nowUnix int64,
    seen map[sharedResultID]struct{},
) []lookupCandidate
```

* These helpers should:
  * filter out expired results
  * skip missing/deleted results
  * de-dup by `sharedResultID`
  * preserve the same deterministic order the current code already relies on
* The direct digest path should preserve priority:
  1. exact request recipe digest bucket
  2. each extra-digest bucket in order
  3. only after that, the structural term path
* The term-set helper should preserve the same preference the current code has:
  * first all directly associated results from matching terms, in deterministic `sharedResultID` order
  * then output-eq-class fallback results, again in deterministic `sharedResultID` order

#### `lookupMatchForDigestsLocked`
* This helper should stop returning a single `hitRes`.
* Instead it should gather all deterministic digest candidates:

```go
func (c *Cache) lookupMatchForDigestsLocked(
    recipeDigest digest.Digest,
    extraDigests []call.ExtraDigest,
    nowUnix int64,
) lookupMatch {
    match := lookupMatch{
        primaryLookupPossible: true,
        missingInputIndex:     -1,
    }
    seen := map[sharedResultID]struct{}{}

    if recipeDigest != "" {
        match.candidates = c.appendResultsDeterministicallyAtLocked(
            match.candidates,
            c.egraphResultsByDigest[recipeDigest.String()],
            nowUnix,
            seen,
            true,
        )
    }
    for _, extra := range extraDigests {
        if extra.Digest == "" {
            continue
        }
        match.candidates = c.appendResultsDeterministicallyAtLocked(
            match.candidates,
            c.egraphResultsByDigest[extra.Digest.String()],
            nowUnix,
            seen,
            false,
        )
    }
    return match
}
```

* This keeps the current priority order while allowing later candidate scanning.

#### `lookupMatchForCallLocked`
* This helper should keep the same overall control flow:
  * try digest candidates first
  * if there are any, return them
  * otherwise compute structural term candidates
* But instead of returning a single hit, it should return a candidate list:

```go
func (c *Cache) lookupMatchForCallLocked(
    ctx context.Context,
    frame *ResultCall,
    recipeDigest digest.Digest,
    selfDigest digest.Digest,
    inputDigests []digest.Digest,
    nowUnix int64,
) (lookupMatch, error) {
    match := c.lookupMatchForDigestsLocked(recipeDigest, frame.ExtraDigests, nowUnix)
    if len(match.candidates) > 0 {
        return match, nil
    }

    // existing input eq-class derivation stays
    // ...

    if match.primaryLookupPossible {
        match.termDigest = calcEgraphTermDigest(selfDigest, match.inputEqIDs)
        termSet := c.egraphTermsByTermDigest[match.termDigest]
        if termSet != nil {
            match.termSetSize = termSet.Size()
        }
        seen := map[sharedResultID]struct{}{}
        match.candidates = c.appendResultsForTermSetDeterministicallyAtLocked(
            match.candidates,
            termSet,
            nowUnix,
            seen,
        )
    }
    return match, nil
}
```

* `lookupMatchForIDLocked` should get the same candidate-list treatment so structural input resolution still has deterministic fallback choices available.

#### Candidate acceptance
* Session gating belongs in the actual cache-hit acceptance path, not in the raw e-graph lookup helpers.
* That means:
  * `lookupMatchFor*Locked` only enumerate candidates
  * `lookupCacheForRequestLocked` and `lookupCacheForDigests` decide whether a candidate is acceptable for the current session
* Add one explicit helper for the gate:

```go
func (c *Cache) selectLookupCandidateForSessionLocked(
    sessionID string,
    candidates []lookupCandidate,
) (lookupCandidate, bool)

func (c *Cache) selectLookupCandidateForSessionLocked(
    sessionID string,
    candidates []lookupCandidate,
) (lookupCandidate, bool) {
    c.sessionMu.Lock()
    defer c.sessionMu.Unlock()

    for _, candidate := range candidates {
        res := candidate.res
        if res == nil {
            continue
        }
        if c.sessionSatisfiesResourceRequirementsLocked(sessionID, res) {
            return candidate, true
        }
    }
    return lookupCandidate{}, false
}
```

* `selectLookupCandidateForSessionLocked` is the gate that walks the deterministic candidate list and picks the first acceptable candidate for the current session.
* `sessionSatisfiesResourceRequirementsLocked` should assume `sessionMu` is already held and should use the set relationship directly:

```go
func (c *Cache) sessionSatisfiesResourceRequirementsLocked(sessionID string, res *sharedResult) bool {
    if res == nil || res.requiredSessionResources == nil || res.requiredSessionResources.Size() == 0 {
        return true
    }
    available := c.sessionHandlesBySession[sessionID]
    if available == nil || available.Size() == 0 {
        return false
    }
    return available.Subset(res.requiredSessionResources)
}
```

* The data types line up for this cleanly:
  * `res.requiredSessionResources` is a `*set.TreeSet[SessionResourceHandle]`
  * `available` is the precomputed `c.sessionHandlesBySession[sessionID]`
* The important semantic detail is that `TreeSet.Subset` means "is the argument a subset of the receiver", so the call shape should be:
  * `available.Subset(res.requiredSessionResources)`
  * not the reverse

#### `lookupCacheForRequestLocked`
* This helper now needs the `sessionID`, because hit acceptance depends on the current session:

```go
func (c *Cache) lookupCacheForRequestLocked(
    ctx context.Context,
    sessionID string,
    req *CallRequest,
    requestDigest digest.Digest,
    requestSelf digest.Digest,
    requestInputs []digest.Digest,
    requestInputRefs []ResultCallStructuralInputRef,
) (AnyResult, bool, error)
```

* The control flow should become:
  1. compute `match := lookupMatchForCallLocked(...)`
  2. trace the lookup attempt
  3. if `len(match.candidates) == 0`, trace miss and return miss
  4. `candidate, ok := selectLookupCandidateForSessionLocked(sessionID, match.candidates)`
  5. if no candidate passes the gate, trace a gated miss and return miss
  6. continue the existing hit path using `candidate.res`
* The fast-path rule should now be tied to the accepted candidate:

```go
candidate, ok := c.selectLookupCandidateForSessionLocked(sessionID, match.candidates)
if !ok {
    return nil, false, nil
}

res := candidate.res
if requestDigest != "" &&
   len(req.ResultCall.ExtraDigests) == 0 &&
   req.TTL == 0 &&
   !req.IsPersistable &&
   candidate.hitRecipeDigest {
    // existing fast-path behavior
}
```

* The same candidate-gated selection needs to happen in `lookupCacheForDigests`, not just `lookupCacheForRequest`.

#### Test-only structural-ID helpers
* `lookupMatchForIDLocked` and `resolveSharedResultForInputIDLocked` are only used from unit tests today.
* They should be removed from production `cache_egraph.go`.
* If tests still need that coverage, move the equivalent logic into test-only helpers in `_test.go` code instead of carrying the API in production.

#### Locking / critical-section note
* The first implementation can do candidate gating while still inside the `egraphMu`-protected lookup flow if that is the cleanest way to keep the hit-selection semantics correct.
* We should still try to minimize unnecessary work while both cache state and session state are locked.
* But correctness wins for the first cut.
* If we end up validating candidates under lock in a way that is obviously heavier than we want long-term, leave an explicit comment in the code explaining:
  * why the work is happening under lock
  * what the likely optimization seam is later

### core/query.go
#### Remove store-centric APIs
* Remove `Secrets(context.Context) (*SecretStore, error)` and `Sockets(context.Context) (*SocketStore, error)` from the `core.Server` interface entirely.
* The `Server` interface should stop advertising per-client store objects as the way core code accesses session resources.
* The new center of gravity should be:
  * `dagql.EngineCache(ctx)` for binding/resolution
  * `SpecificClientAttachableConn(ctx, clientID)` for direct client attachable gRPC access
  * `SecretSalt()` for plaintext-hash derivation
* The concrete interface delta should be:

```go
type Server interface {
    // remove:
    // Secrets(context.Context) (*SecretStore, error)
    // Sockets(context.Context) (*SocketStore, error)

    Auth(context.Context) (*auth.RegistryAuthProvider, error)
    Buildkit(context.Context) (*buildkit.Client, error)
    SpecificClientAttachableConn(context.Context, string) (*grpc.ClientConn, error)
    SecretSalt() []byte
    // ...
}
```

* No new secret/socket-specific methods should be added here as a replacement.
* Callers that used `query.Secrets(ctx)` or `query.Sockets(ctx)` should instead do one of:
  * `cache, err := dagql.EngineCache(ctx)` and then use generic session-resource binding/resolution
  * `query.SpecificClientAttachableConn(ctx, clientID)` and direct concrete-resource RPC calls for already-concrete provider-backed resources
* `CurrentQuery`, `CurrentDagqlServer`, and the rest of the query context plumbing do not need redesign for this cut.

### engine/server/session.go
#### Remove store objects
* Delete the per-client store fields from `daggerClient`:

```go
type daggerClient struct {
    // remove:
    // secretStore *core.SecretStore
    // socketStore *core.SocketStore

    dag       *dagql.Server
    dagqlRoot *core.Query
    // ...
}
```

* Delete the eager store construction in `initializeDaggerClient`:

```go
func (srv *Server) initializeDaggerClient(...) error {
    // remove:
    // client.secretStore = core.NewSecretStore(srv.bkSessionManager)
    // client.socketStore = core.NewSocketStore(srv.bkSessionManager)

    client.buildkitSession, err = srv.newBuildkitSession(ctx, client)
    // ...
}
```

* Delete the exported server methods:

```go
func (srv *Server) Secrets(ctx context.Context) (*core.SecretStore, error)
func (srv *Server) Sockets(ctx context.Context) (*core.SocketStore, error)
```

* There should be no replacement fields on `daggerClient` or `daggerSession` for secret/socket runtime state.
* Session resource state lives in the singleton dagql cache, not in session.go-owned store objects.
* This file should continue to own:
  * request/session lifecycle
  * telemetry/session state
  * buildkit session creation
  * auth provider/session services
* It should stop owning secret/socket resource lookup state.
* Important cross-file note:
  * removing these fields is coupled to `engine/server/bk_session.go`, which currently wires `c.secretStore.AsBuildkitSecretStore()` and `c.socketStore` into the buildkit session
  * that file should hard-cut those attachables entirely rather than replacing them here

### engine/server/bk_session.go
#### Hard cut secret/socket attachables
* In `newBuildkitSession`, delete these lines entirely:

```go
sess.Allow(secretsprovider.NewSecretProvider(c.secretStore.AsBuildkitSecretStore()))
sess.Allow(c.socketStore)
```

* Delete the imports that only existed for those lines.
* Do **not** replace them with cache-backed wrappers in this cut.
* The end state for this step is simply that the buildkit session no longer exposes secret/socket attachables from these old store objects.
* Any fallout from that should surface naturally and be handled explicitly in the consumer paths that still rely on the old behavior.

### core/secret.go
#### Secret model
* Rework `Secret` so one struct can represent both:
  * the abstract secret handle returned into the DAG
  * the concrete current-session secret instance bound under that handle
* The concrete shape should be:

```go
type Secret struct {
    Handle dagql.SessionResourceHandle

    // concrete-only fields:
    URIVal       string
    NameVal      string
    PlaintextVal []byte `json:"-"`
    SourceClientID string
}
```

* Intended meaning:
  * abstract secret handle:
    * `Handle != ""`
    * `URIVal == ""`
    * `PlaintextVal == nil`
    * `NameVal` may be empty or best-effort metadata, but it is not identity
  * concrete provider secret:
    * `Handle == ""`
    * `URIVal != ""`
    * `SourceClientID != ""`
  * concrete `setSecret`:
    * `Handle == ""`
    * `PlaintextVal != nil`
    * `NameVal` set
* `BuildkitSessionID` goes away completely.
* Replace it with `SourceClientID`, but only on the concrete session-ephemeral value.
  * handles do not carry client identity
  * concrete provider-backed values do

#### Secret identity helpers
* Split the helpers into:
  * one helper for computing the effective secret handle string
  * methods on `Secret` for session resolution and plaintext access
* The handle computation should be explicit:

```go
func SecretHandleFromCacheKey(cacheKey string) dagql.SessionResourceHandle
func SecretHandleFromPlaintext(secretSalt []byte, plaintext []byte) dagql.SessionResourceHandle
```

* `SecretHandleFromPlaintext` should keep the current Argon2-based design and just return `dagql.SessionResourceHandle("argon2:"+b64Key)`.
* `SecretHandleFromCacheKey` can keep using a simple stable hash of the caller override, as the current code does with `hashutil.HashStrings(...)`.
* Delete `SecretDigest(...)`.
* Instead, the secret value itself should expose the behavior we need directly:

```go
func (secret *Secret) Name(ctx context.Context) (string, error)
func (secret *Secret) URI(ctx context.Context) (string, error)
func (secret *Secret) Plaintext(ctx context.Context) ([]byte, error)
```

* Each of these methods should follow the same pattern:
  * if `secret.Handle == ""`, use the concrete value directly
  * otherwise:
    * get `cache := dagql.EngineCache(ctx)`
    * get `clientMetadata := engine.ClientMetadataFromContext(ctx)`
    * `resolvedAny, err := cache.ResolveSessionResource(clientMetadata.SessionID, secret.Handle)`
    * cast to `dagql.ObjectResult[*Secret]`
    * continue the operation using `resolved.Self()`
* `Name(ctx)` should return `resolved.NameVal`
* `URI(ctx)` should return `resolved.URIVal`
* `Plaintext` should:
  * follow the same "resolve through the handle if needed" pattern first
  * if `resolved.URIVal == ""`, return `resolved.PlaintextVal`
  * otherwise, get `query := CurrentQuery(ctx)`
  * `conn, err := query.SpecificClientAttachableConn(ctx, resolved.SourceClientID)`
  * `resp, err := secrets.NewSecretsClient(conn).GetSecret(ctx, &secrets.GetSecretRequest{ID: resolved.URIVal})`
  * return `resp.Data`
* The important semantic point is:
  * handle secrets resolve through the cache
  * concrete secrets do the real plaintext work
  * no standalone helper function is the center of gravity anymore

#### Cache-backed resource implementation
* Delete `SecretStore` entirely:
  * `type SecretStore struct { ... }`
  * `NewSecretStore`
  * `AddSecret`
  * `AddSecretFromOtherStore`
  * `HasSecret`
  * `GetSecret`
  * `GetSecretName`
  * `GetSecretURI`
  * `GetSecretNameOrURI`
  * `GetSecretPlaintext`
  * `AsBuildkitSecretStore`
  * `buildkitSecretStore`
* The generic cache APIs from `dagql/cache.go` become the only long-lived source of secret session state:
  * `BindSessionResource(...)`
  * `ResolveSessionResource(...)`
* URI-backed plaintext fetch should move behind `(*Secret).Plaintext(ctx)` rather than a standalone helper.
* That method should no longer rely on stored `BuildkitSessionID`.
* Instead, it should rely on:
  * `resolved.SourceClientID`
  * `query.SpecificClientAttachableConn(ctx, resolved.SourceClientID)`
* This is an intentional hard cut:
  * remove the old stored-caller identity
  * let the URI-backed plaintext path use the new current-session/current-context resolution model instead of preserving the old one

#### Persistence
* `Secret` should own its own persisted object form.
* Add the usual persisted-result holder field and methods:

```go
type Secret struct {
    // existing fields...
    persistedResultID uint64
}

func (secret *Secret) PersistedResultID() uint64
func (secret *Secret) SetPersistedResultID(resultID uint64)
```

* Add an explicit persisted payload type:

```go
type persistedSecretPayload struct {
    Handle  dagql.SessionResourceHandle `json:"handle,omitempty"`
    NameVal string                      `json:"name,omitempty"`
}
```

* Encoding rules:
  * if `secret.Handle != ""`, persist:
    * `Handle`
    * optionally `NameVal` if we want that best-effort metadata to survive
  * if `secret.Handle == ""`, do **not** persist:
    * `URIVal`
    * `PlaintextVal`
    * `SourceClientID`
  * if a concrete secret leaks into persistence, emit an empty-ish payload rather than failing
* The shape should be:

```go
func (secret *Secret) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
    payload := persistedSecretPayload{}
    if secret.Handle != "" {
        payload.Handle = secret.Handle
        payload.NameVal = secret.NameVal
    }
    return json.Marshal(payload)
}

func (*Secret) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
    var persisted persistedSecretPayload
    if err := json.Unmarshal(payload, &persisted); err != nil {
        return nil, err
    }
    secret := &Secret{
        Handle:  persisted.Handle,
        NameVal: persisted.NameVal,
    }
    secret.SetPersistedResultID(resultID)
    return secret, nil
}
```

* This keeps persisted secret objects aligned with the handle model:
  * handles survive
  * concrete session-local provenance does not

### core/schema/secret.go
#### `Query.secret`
* Keep provider resolution only at construction time.
* Keep `cacheKey` override for now.
* Stop using `parent.Self().Secrets(ctx)` entirely.
* The constructor needs three explicit phases:
  1. build the concrete provider-backed secret instance
  2. compute the abstract `SessionResourceHandle`
  3. build the abstract handle result, stamp it, attach the concrete result, and bind the session resource
* The concrete first-cut flow should be:

```go
func (s *secretSchema) secret(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Query],
    args secretArgs,
) (dagql.ObjectResult[*core.Secret], error) {
    srv, err := core.CurrentDagqlServer(ctx)
    clientMetadata, err := engine.ClientMetadataFromContext(ctx)
    cache, err := dagql.EngineCache(ctx)

    concreteVal := &core.Secret{
        URIVal:         args.URI,
        SourceClientID: clientMetadata.ClientID,
    }
    concrete, err := dagql.NewObjectResultForCurrentCall(ctx, srv, concreteVal)

    var handle dagql.SessionResourceHandle
    if args.CacheKey.Valid {
        handle = core.SecretHandleFromCacheKey(string(args.CacheKey.Value))
    } else {
        plaintext, err := concreteVal.Plaintext(ctx)
        if err != nil {
            // preserve the current random-fallback behavior for now
        }
        handle = core.SecretHandleFromPlaintext(parent.Self().SecretSalt(), plaintext)
    }

    abstractVal := &core.Secret{
        Handle: handle,
    }
    abstract, err := dagql.NewObjectResultForCurrentCall(ctx, srv, abstractVal)
    abstract, err = abstract.WithContentDigest(ctx, digest.Digest(handle))
    abstract, err = abstract.WithSessionResourceHandle(ctx, handle)

    attachedConcreteAny, err := cache.AttachResult(ctx, clientMetadata.SessionID, srv, concrete)
    attachedConcrete := attachedConcreteAny.(dagql.ObjectResult[*core.Secret])
    err = cache.BindSessionResource(ctx, clientMetadata.SessionID, handle, attachedConcrete)

    return abstract, err
}
```

* The important points are:
  * the returned result is the handle result
  * the concrete secret instance is separately attached to the cache with `cache.AttachResult(...)`
  * the current session binds that concrete attached result under the handle
* The current random-fallback-on-provider-error behavior for cache-key derivation can stay in the first cut if we still want the same tolerance.

#### `Query.setSecret`
* Change identity from `name + accessor` to `SecretHandleFromPlaintext(...)`.
* Keep the sanitized call args with redacted plaintext.
* Stop calling `core.GetClientResourceAccessor(...)` for cache identity.
* The shape should mirror `Query.secret`:
  * build a concrete `setSecret` value with `NameVal` and `PlaintextVal`
  * compute `handle := core.SecretHandleFromPlaintext(parent.Self().SecretSalt(), concreteVal.PlaintextVal)`
  * build a handle result with `Handle: handle`
  * `WithContentDigest(ctx, digest.Digest(handle))`
  * `WithSessionResourceHandle(ctx, handle)`
  * attach the concrete result with `cache.AttachResult(...)`
  * `BindSessionResource(...)` for the session
* The concrete result should still use the sanitized call frame so plaintext does not leak into returned identity.

#### `Secret` field resolvers
* `name`, `uri`, and `plaintext` should all go through methods on `*core.Secret`, not through standalone schema-local resolution helpers.
* The schema-level code should become very small:

```go
func (s *secretSchema) name(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
    return secret.Self().Name(ctx)
}

func (s *secretSchema) uri(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
    return secret.Self().URI(ctx)
}

func (s *secretSchema) plaintext(ctx context.Context, secret dagql.ObjectResult[*core.Secret], args struct{}) (string, error) {
    plaintext, err := secret.Self().Plaintext(ctx)
    if err != nil {
        return "", err
    }
    return string(plaintext), nil
}
```

* Then the field resolvers become straightforward:
  * `name`: `secret.Self().Name(ctx)`
  * `uri`: `secret.Self().URI(ctx)`
  * `plaintext`: `secret.Self().Plaintext(ctx)`
* If multiple equivalent concrete secrets are bound for the session, these methods will pick one deterministically through the cache.
* `name` / `uri` are therefore best-effort metadata on the resolved concrete secret, not enduring identity properties of the handle.

### core/schema/address.go
#### Secret / socket routing
* Keep `address.secret` and `address.socket` as thin routing layers.
* If `cacheKey` stays, keep parsing it out here for `address.secret`.
* No registration/binding logic should live here; that belongs in the underlying secret/socket constructors.
* `address.secret` can stay almost exactly as it is now:

```go
func (s *addressSchema) secret(...) (dagql.ObjectResult[*core.Secret], error) {
    // normalize shorthand env syntax
    // strip ?cacheKey=... from the address
    // then:
    q := selectSecret(addr, cacheKey)
    return select via srv.Select(...)
}
```

* The one cleanup is to delete the stale `FIXME` about the secret store, since there is no store anymore.
* `selectSecret(addr, cacheKey)` should stay as the helper that builds the selector list for `Query.secret`.
* `address.socket` should remain a thin `unix://... -> host.unixSocket(path: ...)` adapter:

```go
func (s *addressSchema) socket(...) (dagql.ObjectResult[*core.Socket], error) {
    path := strings.TrimPrefix(addr, "unix://")
    q := []dagql.Selector{
        {Field: "host"},
        {Field: "unixSocket", Args: []dagql.NamedInput{ ... }},
    }
    return select via srv.Select(...)
}
```

* No extra cache or session-resource logic should be added here.

### core/modfunc.go
#### User default object decoding
* `UserDefault.Value(...)` still has a special-case for decoded secrets:

```go
if secret, ok := dagql.UnwrapAs[dagql.ObjectResult[*Secret]](result); ok {
    secretStore, err := query.Secrets(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to get secret store: %w", err)
    }
    if err := secretStore.AddSecret(ctx, secret); err != nil {
        return nil, fmt.Errorf("failed to add secret: %w", err)
    }
}
```

* That whole block should be deleted.
* Under the new model, `address.secret` / `Query.secret` already do the session binding work when the secret is constructed.
* `modfunc.go` should not try to re-register decoded secrets anywhere.

### core/socket.go
#### Socket model
* Replace the current `IDDigest`-only socket shape with explicit fields that match the new model.
* The first-cut shape should be:

```go
type SocketKind string

const (
    SocketKindSSHHandle SocketKind = "ssh-handle"
    SocketKindUnixOpaque SocketKind = "unix-opaque"
    SocketKindHostIP SocketKind = "host-ip"
)

type Socket struct {
    Kind   SocketKind
    Handle dagql.SessionResourceHandle

    // concrete-only fields:
    URLVal         string
    PortForwardVal PortForward
    SourceClientID string
}
```

* Intended meaning:
  * SSH handle:
    * `Kind == SocketKindSSHHandle`
    * `Handle != ""`
    * concrete fields empty
  * concrete unix socket:
    * `Kind == SocketKindUnixOpaque`
    * `Handle == ""`
    * `URLVal == "unix:///..."`
    * `SourceClientID != ""`
  * concrete host-IP socket:
    * `Kind == SocketKindHostIP`
    * `Handle == ""`
    * `URLVal == "tcp://host:backend"` or `udp://host:backend`
    * `PortForwardVal` set
    * `SourceClientID != ""`
* Delete:
  * `IDDigest`
  * `LLBID()`
  * `SocketIDDigest(...)`
* Later callers that used those should move to:
  * `string(socket.Handle)` for SSH handle identity
  * explicit concrete fields for opaque sockets

#### Cache-backed resource implementation
* Delete `SocketStore` entirely:
  * `type SocketStore struct { ... }`
  * `type storedSocket struct { ... }`
  * `NewSocketStore`
  * `AddUnixSocket`
  * `AddIPSocket`
  * `AddSocketFromOtherStore`
  * `AddSocketAlias`
  * `HasSocket`
  * `GetSocketURLEncoded`
  * `GetSocketPortForward`
  * `CheckAgent`
  * `ForwardAgent`
  * `ConnectSocket`
  * `MountSocket`
  * `Register`
* For SSH sockets:
  * use cache-owned handle + session-bound concrete `Socket` value
* For opaque sockets:
  * keep them as direct per-call concrete execution inputs
  * do not force them into the session-resource-handle cacheability model
* Replace the old store methods with methods on `*Socket` itself:

```go
func (socket *Socket) AgentFingerprints(ctx context.Context) ([]string, error)
func (socket *Socket) MountSSHAgent(ctx context.Context) (string, func() error, error)
func (socket *Socket) URL(ctx context.Context) (string, error)
```

* `URL(ctx)` should:
  * if `socket.Handle == ""`, return `socket.URLVal`
  * otherwise, resolve the bound concrete socket for the current session through `dagql.EngineCache(ctx)` and return that socket's `URLVal`
* `MountSSHAgent(ctx)` should:
  * resolve the concrete socket if `Handle != ""`
  * get `query := CurrentQuery(ctx)`
  * `conn, err := query.SpecificClientAttachableConn(ctx, resolved.SourceClientID)`
  * create a temp unix socket
  * for each accepted local connection:
    * call `sshforward.NewSSHClient(conn).ForwardAgent(...)`
    * set metadata `engine.SocketURLEncodedKey = resolved.URLVal`
    * proxy bytes between the local unix socket connection and the gRPC stream
* `AgentFingerprints(ctx)` should:
  * call `MountSSHAgent(ctx)`
  * dial the mounted local socket
  * use `agent.NewClient(conn).List()`
  * compute/sort fingerprints exactly like the current `SocketStore.AgentFingerprints`
* `MountSSHAgent(ctx)` and `AgentFingerprints(ctx)` should only be valid for SSH-capable sockets:
  * SSH handle sockets
  * concrete unix sockets being used as SSH auth sources
* `URL(ctx)` is the general low-level accessor that opaque host-ip service code can reuse too.

#### SSH-specific helpers
* Keep fingerprint derivation / scoping helpers.
* Move callers to use `(*Socket).AgentFingerprints(ctx)` rather than the old store object directly.
* This file should become the place where socket behavior lives; no separate socket store API should remain.

#### Persistence
* `Socket` should also own its own persisted object form.
* Add the usual persisted-result holder field and methods:

```go
type Socket struct {
    // existing fields...
    persistedResultID uint64
}

func (socket *Socket) PersistedResultID() uint64
func (socket *Socket) SetPersistedResultID(resultID uint64)
```

* Add an explicit persisted payload type:

```go
type persistedSocketPayload struct {
    Kind           SocketKind                 `json:"kind,omitempty"`
    Handle         dagql.SessionResourceHandle `json:"handle,omitempty"`
    PortForwardVal PortForward                `json:"portForward,omitempty"`
}
```

* Encoding rules:
  * if `socket.Handle != ""`, persist:
    * `Kind`
    * `Handle`
  * if a concrete host-IP socket leaks into persistence, persist:
    * `Kind`
    * `PortForwardVal`
    * but omit `URLVal` and `SourceClientID`
  * if a concrete unix-opaque socket leaks into persistence, persist only `Kind`
* The shape should be:

```go
func (socket *Socket) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
    payload := persistedSocketPayload{
        Kind: socket.Kind,
    }
    if socket.Handle != "" {
        payload.Handle = socket.Handle
    } else if socket.Kind == SocketKindHostIP {
        payload.PortForwardVal = socket.PortForwardVal
    }
    return json.Marshal(payload)
}

func (*Socket) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
    var persisted persistedSocketPayload
    if err := json.Unmarshal(payload, &persisted); err != nil {
        return nil, err
    }
    socket := &Socket{
        Kind:           persisted.Kind,
        Handle:         persisted.Handle,
        PortForwardVal: persisted.PortForwardVal,
    }
    socket.SetPersistedResultID(resultID)
    return socket, nil
}
```

* This keeps socket persistence aligned with the earlier rule:
  * handles persist cleanly
  * concrete session-local URL/client identity does not

### core/ssh_auth_socket.go
#### Fingerprint-based equivalence
* Keep the current HMAC-over-fingerprints algorithm, but make it return a session-resource handle directly:

```go
func ScopedSSHAuthSocketHandle(secretSalt []byte, fingerprints []string) dagql.SessionResourceHandle
```

* The body can stay almost exactly the same as `ScopedSSHAuthSocketDigest`, but should return:

```go
return dagql.SessionResourceHandle(digest.NewDigestFromBytes(digest.SHA256, mac.Sum(nil)).String())
```

* Delete:
  * `ScopedSSHAuthSocketDigest`
  * `ScopedSSHAuthSocketDigestFromStore`
* Callers should now do:
  * `fingerprints, err := sourceSocket.Self().AgentFingerprints(ctx)`
  * `handle := core.ScopedSSHAuthSocketHandle(query.SecretSalt(), fingerprints)`

### core/schema/host.go
#### `host.unixSocket`
* Stop hashing the path/accessor into a cacheable digest and stop touching any socket store.
* The new flow should be:

```go
func (s *hostSchema) socket(...) (dagql.Result[*core.Socket], error) {
    clientMetadata, err := engine.ClientMetadataFromContext(ctx)

    sock := &core.Socket{
        Kind:           core.SocketKindUnixOpaque,
        URLVal:         (&url.URL{Scheme: "unix", Path: args.Path}).String(),
        SourceClientID: clientMetadata.ClientID,
    }
    return dagql.NewResultForCurrentCall(ctx, sock)
}
```

* No `WithContentDigest(...)`
* No `GetClientResourceAccessor(...)`
* No nested-module special-case preservation logic
* No registration/binding step
* Persist as much surrounding object structure as needed, but omit `URLVal` / `SourceClientID` if a concrete opaque socket ever leaks into persistence.

#### `host._sshAuthSocket`
* Continue to scope SSH sockets by fingerprints, but do it with the new handle model.
* The new flow should be:

```go
func (s *hostSchema) sshAuthSocket(...) (dagql.Result[*core.Socket], error) {
    srv, err := core.CurrentDagqlServer(ctx)
    query, err := core.CurrentQuery(ctx)
    cache, err := dagql.EngineCache(ctx)
    clientMetadata, err := engine.ClientMetadataFromContext(ctx)

    var concrete dagql.Result[*core.Socket]
    if args.Source.Valid {
        sourceInst, err := args.Source.Value.Load(ctx, srv)
        concrete = sourceInst
    } else {
        if clientMetadata.SSHAuthSocketPath == "" {
            return inst, errors.New("SSH_AUTH_SOCK is not set")
        }
        concreteVal := &core.Socket{
            Kind:           core.SocketKindUnixOpaque,
            URLVal:         (&url.URL{Scheme: "unix", Path: clientMetadata.SSHAuthSocketPath}).String(),
            SourceClientID: clientMetadata.ClientID,
        }
        concrete, err = dagql.NewResultForCurrentCall(ctx, concreteVal)
    }

    fingerprints, err := concrete.Self().AgentFingerprints(ctx)
    handle := core.ScopedSSHAuthSocketHandle(query.SecretSalt(), fingerprints)

    handleVal := &core.Socket{
        Kind:   core.SocketKindSSHHandle,
        Handle: handle,
    }
    inst, err := dagql.NewResultForCurrentCall(ctx, handleVal)
    inst, err = inst.WithContentDigest(ctx, digest.Digest(handle))
    inst, err = inst.WithSessionResourceHandle(ctx, handle)

    attachedConcreteAny, err := cache.AttachResult(ctx, clientMetadata.SessionID, srv, concrete)
    attachedConcrete := attachedConcreteAny.(dagql.Result[*core.Socket])
    err = cache.BindSessionResource(ctx, clientMetadata.SessionID, handle, attachedConcrete)

    return inst, err
}
```

* Important simplifications relative to the current code:
  * no `query.Sockets(ctx)`
  * no `HasSocket(...)`
  * no `AddUnixSocket(...)`
  * no `AddSocketAlias(...)`
  * no `upsertScopedSSHAuthSocket(...)`
  * no nested-module "preserve existing mapping" logic
* Multiple equivalent concrete bindings in one session are now naturally handled by the cache's handle binding set.

#### host IP / forwarded sockets
* Treat them as direct per-call external socket inputs, not abstract cacheable handles.
* The constructor should stop touching any socket store and instead build direct concrete socket values:

```go
for _, port := range ports {
    sock := &core.Socket{
        Kind:           core.SocketKindHostIP,
        URLVal:         (&url.URL{Scheme: port.Protocol.Network(), Host: fmt.Sprintf("%s:%d", args.Host, port.Backend)}).String(),
        PortForwardVal: port,
        SourceClientID: clientMetadata.ClientID,
    }
    socks = append(socks, sock)
}
```

* No registration/binding step.
* No cache handle.
* No host-IP accessor hashing.
* Persist as much surrounding object structure as needed, but omit `URLVal` / `SourceClientID` if a concrete host-IP socket leaks into persistence.

### core/host.go
#### Host env lookup
* `Host.GetEnv(...)` still uses the old secret-store path directly:

```go
secretStore, err := query.Secrets(ctx)
plaintext, err := secretStore.GetSecretPlaintextDirect(ctx, &Secret{URI: "env://" + name})
```

* That needs to hard-cut to the new direct attachable-conn path.
* The shape should become:

```go
func (Host) GetEnv(ctx context.Context, name string) string {
    query, err := CurrentQuery(ctx)
    if err != nil {
        return ""
    }
    clientMetadata, err := engine.ClientMetadataFromContext(ctx)
    if err != nil {
        return ""
    }
    secret := &Secret{
        URIVal:         "env://" + name,
        SourceClientID: clientMetadata.ClientID,
    }
    plaintext, err := secret.Plaintext(ctx)
    if err != nil {
        return ""
    }
    return string(plaintext)
}
```

* No `query.Secrets(ctx)`
* No `BuildkitSessionID`
* No secret-store helper path

### core/container.go
#### Attach secret/socket dependencies
* Change the container state shape so socket sources are real result values:

```go
type ContainerSocket struct {
    Source        dagql.ObjectResult[*Socket]
    ContainerPath string
    Owner         *Ownership
}
```

* `Container.AttachDependencyResults` currently attaches:
  * rootfs
  * directory/file/cache mounts
* It does **not** attach:
  * `container.Secrets`
  * `container.Sockets`
* That is the exact blocker for session-resource requirement propagation.
* The function should grow two additional attachment loops:

```go
for i := range container.Secrets {
    attached, err := attach(container.Secrets[i].Secret)
    typed, ok := attached.(dagql.ObjectResult[*Secret])
    container.Secrets[i].Secret = typed
    owned = append(owned, typed)
}

for i := range container.Sockets {
    attached, err := attach(container.Sockets[i].Source)
    typed, ok := attached.(dagql.ObjectResult[*Socket])
    container.Sockets[i].Source = typed
    owned = append(owned, typed)
}
```

* The important rule is:
  * secret and socket results must become attached dependencies just like rootfs and mount sources already do

#### `Container.Build`
* `Container.Build(...)` definitely needs to change in this cut.
* The current signature still bakes in the old world:

```go
func (container *Container) Build(
    ctx context.Context,
    dockerfileDir *Directory,
    contextDirID *call.ID,
    dockerfile string,
    buildArgs []BuildArg,
    target string,
    secrets []dagql.ObjectResult[*Secret],
    secretStore *SecretStore,
    noInit bool,
    sshSocketID *call.ID,
) (*Container, error)
```

* The hard-cut signature should stop taking `secretStore` and raw `*call.ID` for SSH.
* A cleaner first-cut shape is:

```go
func (container *Container) Build(
    ctx context.Context,
    dockerfileDir *Directory,
    contextDirID *call.ID,
    dockerfile string,
    buildArgs []BuildArg,
    target string,
    secrets []dagql.ObjectResult[*Secret],
    noInit bool,
    sshSocket dagql.ObjectResult[*Socket],
) (*Container, error)
```

* This is intentionally agnostic about whether `sshSocket` is:
  * an SSH handle socket
  * or a raw opaque unix socket
* We do **not** require an SSH-handle socket here.
* We do **not** auto-scope a raw unix socket here.
* We just accept the socket the user provided and use its recipe identity.
  * if that makes this `dockerBuild` less cacheable, that is coherent with the model
* The body should change like this:

```go
secretIDsByLLBID := make(map[string]*call.ID, len(secrets))
returnedSecretMounts := make([]ContainerSecret, 0, len(secrets))
for _, secret := range secrets {
    secretRecipeID, err := secret.RecipeID(ctx)
    if err != nil {
        return nil, fmt.Errorf("get dockerBuild secret recipe ID: %w", err)
    }
    secretName, err := secret.Self().Name(ctx)
    if err != nil {
        return nil, fmt.Errorf("get dockerBuild secret name: %w", err)
    }
    if secretName == "" {
        return nil, fmt.Errorf("secret has no name and cannot be referenced from Dockerfile secret id")
    }
    if existing, found := secretIDsByLLBID[secretName]; found {
        if existing.Digest() != secretRecipeID.Digest() {
            return nil, fmt.Errorf("multiple secrets provided for dockerBuild secret id %q", secretName)
        }
        continue
    }
    secretIDsByLLBID[secretName] = secretRecipeID
    returnedSecretMounts = append(returnedSecretMounts, ContainerSecret{
        Secret:    secret,
        MountPath: fmt.Sprintf("/run/secrets/%s", secretName),
    })
}

sshSocketIDsByLLBID := map[string]*call.ID{}
if sshSocket.Self() != nil {
    sshRecipeID, err := sshSocket.RecipeID(ctx)
    if err != nil {
        return nil, fmt.Errorf("get dockerBuild ssh socket recipe ID: %w", err)
    }
    sshSocketIDsByLLBID[""] = sshRecipeID
}
```

* This is the only special shaping `dockerBuild` needs for SSH right now.
* No store lookup.
* No auto-scoping.
* No special-case based on handle-vs-opaque beyond whatever recipe identity the socket already carries.

#### Persistence gap
* `Container.EncodePersistedObject` currently errors out on `len(container.Secrets) > 0`.
* That must be replaced with real persisted payload encoding for:
  * `Secrets`
  * `Sockets`
* Secret and socket result objects should own their own persisted self form in `core/secret.go` and `core/socket.go`.
* That means container persistence only needs to encode:
  * refs to the secret/socket result objects
  * container-local metadata like env name, mount path, owner, mode, and container path
* The persisted payload types should grow accordingly:

```go
type persistedContainerSecretPayload struct {
    SecretResultID uint64 `json:"secretResultID,omitempty"`
    EnvName        string `json:"envName,omitempty"`
    MountPath      string `json:"mountPath,omitempty"`
    Owner          *Ownership `json:"owner,omitempty"`
    Mode           fs.FileMode `json:"mode,omitempty"`
}

type persistedContainerSocketPayload struct {
    SocketResultID uint64 `json:"socketResultID,omitempty"`
    ContainerPath  string `json:"containerPath,omitempty"`
    Owner          *Ownership `json:"owner,omitempty"`
}
```

* `persistedContainerPayload` should then gain:

```go
Secrets []persistedContainerSecretPayload `json:"secrets,omitempty"`
Sockets []persistedContainerSocketPayload `json:"sockets,omitempty"`
```

* Encoding rules:
  * secrets persist by secret result ref plus their env/mount metadata
  * SSH-handle sockets persist by socket result ref plus container path/owner
  * opaque per-call sockets can still be emitted as empty-ish payload shells if they leak in, with `URLVal` / `SourceClientID` omitted
* Decoding rules:
  * load the secret/socket result refs back through the persisted-object cache
  * rebuild the container slices directly
* This section should no longer talk about “not yet supported.”
  * the hard-cut plan is to encode these states properly

### core/container_exec.go
#### Execution-time resource lookup
* The current file still relies on the old buildkit-session secret/socket path in three places:
  * `secretEnvs(ctx)` returns `[]*pb.SecretEnv`
  * `prepareExecSecretMount(...)` resolves `pb.SecretOpt` through `session.Any(...)`
  * `prepareExecSSHMount(...)` resolves `pb.SSHOpt` through `session.Any(...)` and `sshforward.CheckSSHID`
* This section needs a real hard cut, not just a lookup substitution.

#### Secret envs
* Stop producing `[]*pb.SecretEnv` entirely for the engine-side exec path.
* Replace:

```go
func (container *Container) secretEnvs(ctx context.Context) ([]*pb.SecretEnv, error)
func loadSecretEnv(ctx context.Context, g bksession.Group, sm *bksession.Manager, secretenv []*pb.SecretEnv) ([]string, error)
```

* With:

```go
func (container *Container) secretEnvValues(ctx context.Context) ([]string, error) {
    env := make([]string, 0, len(container.Secrets))
    for _, secret := range container.Secrets {
        if secret.EnvName == "" {
            continue
        }
        plaintext, err := secret.Secret.Self().Plaintext(ctx)
        if err != nil {
            return nil, fmt.Errorf("secret env %q: %w", secret.EnvName, err)
        }
        env = append(env, secret.EnvName+"="+string(plaintext))
    }
    return env, nil
}
```

* Then the exec path should append those env entries directly to `meta.Env`.
* This keeps secret env resolution engine-local and removes the old `pb.SecretEnv` dependency for this path.

#### Secret mounts
* Replace `execMountState.SecretOpt *pb.SecretOpt` with an explicit local secret-mount config:

```go
type execSecretMountConfig struct {
    Secret dagql.ObjectResult[*Secret]
    UID    int
    GID    int
    Mode   fs.FileMode
}
```

* `execMountState` should then hold:

```go
Secret *execSecretMountConfig
```

* `prepareMounts(...)` should fill that from `container.Secrets` directly instead of generating `pb.SecretOpt`.
* `prepareExecSecretMount(...)` should stop using `session.Any(...)` entirely:

```go
func prepareExecSecretMount(
    ctx context.Context,
    cache bkcache.SnapshotManager,
    cfg *execSecretMountConfig,
) (bkcache.Mountable, error) {
    plaintext, err := cfg.Secret.Self().Plaintext(ctx)
    if err != nil {
        return nil, err
    }
    return &execSecretMount{
        uid:   cfg.UID,
        gid:   cfg.GID,
        mode:  cfg.Mode,
        data:  plaintext,
        idmap: cache.IdentityMapping(),
    }, nil
}
```

* This preserves the existing “write a secret file into a temp mount” behavior, but without the old secret-session lookup path.

#### SSH mounts
* Replace `execMountState.SSHOpt *pb.SSHOpt` with an explicit local SSH-mount config:

```go
type execSSHMountConfig struct {
    Socket dagql.ObjectResult[*Socket]
    UID    int
    GID    int
    Mode   fs.FileMode
}
```

* `prepareMounts(...)` should fill that from `container.Sockets` directly.
* `prepareExecSSHMount(...)` should stop using `session.Any(...)` and `sshforward.CheckSSHID(...)`.
* Instead:

```go
func prepareExecSSHMount(
    ctx context.Context,
    cache bkcache.SnapshotManager,
    cfg *execSSHMountConfig,
) (bkcache.Mountable, error) {
    return &execSSHMount{
        socket: cfg.Socket,
        uid:    cfg.UID,
        gid:    cfg.GID,
        mode:   cfg.Mode,
        idmap:  cache.IdentityMapping(),
    }, nil
}
```

* Then `execSSHMountInstance.Mount()` should call:
  * `sockPath, cleanup, err := ssh.socket.MountSSHAgent(ctx)`
  * and bind-mount that `sockPath` into the container with the requested uid/gid/mode
* No `pb.SSHOpt.ID`
* No `sshforward.MountSSHSocket(ctx, caller, opt)`
* No `bksession.Caller` stored on the exec mount

#### Mount-state construction
* In the two places where this file currently populates `execMountState` from `container.Secrets` / `container.Sockets`, switch from protobuf options to the new local config structs.
* The secret/socket sections should look more like:

```go
secretState := &execMountState{
    Dest:      secret.MountPath,
    MountType: pb.MountType_SECRET,
    Secret: &execSecretMountConfig{
        Secret: secret.Secret,
        UID:    uid,
        GID:    gid,
        Mode:   secret.Mode,
    },
}

socketState := &execMountState{
    Dest:      socket.ContainerPath,
    MountType: pb.MountType_SSH,
    SSH: &execSSHMountConfig{
        Socket: socket.Source,
        UID:    uid,
        GID:    gid,
        Mode:   0o600,
    },
}
```

* Opaque unix sockets remain direct per-call inputs, but they can still flow through `MountSSHAgent`-style local proxying if that is the mount mechanism we choose for socket injection.
* The key point is that execution resolves through the `Socket` value itself, not through a socket store or buildkit session ssh ID.

### core/service.go
#### Service startup path
* `Service.Start` currently still uses the old secret/socket plumbing in two places:
  * it calls `ctr.secretEnvs(ctx)` + `loadSecretEnv(...)`
  * reverse-tunnel startup uses `query.Sockets(ctx)` and socket-store lookups
* Both need the same hard cut as `container_exec.go`.
* For service-start exec envs, change:

```go
secretEnvs, err := ctr.secretEnvs(ctx)
secretEnv, err := loadSecretEnv(ctx, bksession.NewGroup(bk.ID()), bk.SessionManager, secretEnvs)
meta.Env = append(meta.Env, secretEnv...)
```

* To:

```go
secretEnv, err := ctr.secretEnvValues(ctx)
if err != nil {
    return nil, err
}
meta.Env = append(meta.Env, secretEnv...)
```

* For host-socket services, remove `query.Sockets(ctx)` entirely.
* `Service.Endpoint(...)` should switch from:

```go
socketStore, err := query.Sockets(ctx)
portForward, ok := socketStore.GetSocketPortForward(svc.HostSockets[0].IDDigest)
```

* To:

```go
portForward, err := svc.HostSockets[0].PortForward(ctx)
if err != nil {
    return "", err
}
port = portForward.FrontendOrBackendPort()
```

* `startReverseTunnel(...)` should similarly:
  * drop the `sockStore` lookup
  * build `checkPorts` from `sock.PortForward(ctx)`
  * construct `c2hTunnel` without a socket store field
* The tunnel helper should become:

```go
type c2hTunnel struct {
    bk    *buildkit.Client
    ns    buildkit.Namespaced
    socks []*Socket
}
```

* And its loop should switch from `sockStore.GetSocketURLEncoded` / `sockStore.ConnectSocket` to socket methods:

```go
port, err := sock.PortForward(ctx)
urlEncoded, err := sock.URL(ctx)
upstreamClient, err := sock.ForwardAgentClient(ctx)
```

* This does mean `core/socket.go` needs one more helper for the tunnel path:

```go
func (socket *Socket) PortForward(ctx context.Context) (PortForward, error)
func (socket *Socket) ForwardAgentClient(ctx context.Context) (sshforward.SSH_ForwardAgentClient, error)
```

* That is cleaner than keeping store-specific tunnel logic alive in `core/service.go`.

### core/c2h.go
#### Reverse-tunnel helper
* `c2hTunnel` still directly depends on `SocketStore` today.
* It should become a thin user of socket methods instead:

```go
type c2hTunnel struct {
    bk    *buildkit.Client
    ns    buildkit.Namespaced
    socks []*Socket
}
```

* Then the main listener/proxy loop should switch from:

```go
port, ok := d.sockStore.GetSocketPortForward(sock.IDDigest)
urlEncoded, ok := d.sockStore.GetSocketURLEncoded(sock.IDDigest)
upstreamClient, err := d.sockStore.ConnectSocket(ctx, sock.IDDigest)
```

* To:

```go
port, err := sock.PortForward(ctx)
urlEncoded, err := sock.URL(ctx)
upstreamClient, err := sock.ForwardAgentClient(ctx)
```

* This file should not know anything about stores, digests, or alias maps anymore.
* It should just operate on socket values and their methods.

### core/schema/container.go
#### Existing explicit state paths
* `withSecretVariable`, `withMountedSecret`, and `withUnixSocket` already put explicit state on `Container`.
* That is good and should remain the model.
* As part of this cut, `ContainerSocket.Source` should become `dagql.ObjectResult[*Socket]`, and these schema/container helpers should pass real socket result values through accordingly.
* Once socket kind exists, mounting an opaque/unreproducible socket should force downstream uncacheability.

* Concretely:
  * `withSecretVariable` can stay almost exactly as-is; it already loads a `core.SecretID` to a `dagql.ObjectResult[*Secret]`
  * `withMountedSecret` can stay almost exactly as-is for the same reason
  * `withUnixSocket` needs to stop passing `socket.Self()` and instead pass the full result value
* So this:

```go
return ctr.WithUnixSocketFromParent(ctx, parent, path, socket.Self(), args.Owner)
```

* Should become something like:

```go
return ctr.WithUnixSocketFromParent(ctx, parent, path, socket, args.Owner)
```

* And `core.Container.WithUnixSocketFromParent(...)` should be updated accordingly to accept `dagql.ObjectResult[*Socket]`.

#### `withRegistryAuth`
* Keep the historically weird side-effecting behavior for now.
* Do **not** redesign it into explicit container state in this cut.
* The only required implementation change is:
  * when it needs the password/secret bytes, call the new secret plaintext API instead of going through `query.Secrets(ctx)`
* Concretely, the body should shift from:

```go
secretStore, err := query.Secrets(ctx)
secretDigest := core.SecretDigest(ctx, secret)
secretBytes, err := secretStore.GetSecretPlaintext(ctx, secretDigest)
```

* To something like:

```go
secretBytes, err := secret.Self().Plaintext(ctx)
```

* The rest of the auth-provider mutation can stay as-is for now.

### core/schema/directory.go
#### dockerBuild secret/SSH integration
* Keep the current high-level mapping approach where Dockerfile secret/SSH names map back to Dagger resource inputs.
* `dockerBuild` itself should stay fairly small; the bigger logic moves into `core.Container.Build(...)`.
* The concrete callsite changes in `directorySchema.dockerBuild(...)` should be:
  * delete `query.Secrets(ctx)` and the `secretStore` lookup entirely
  * stop converting the SSH arg to a raw `*call.ID`
  * pass the loaded socket result directly through to `ctr.Build(...)`
* The shape should become:

```go
secrets, err := dagql.LoadIDResults(ctx, srv, args.Secrets)
if err != nil {
    return nil, err
}

var sshSocket dagql.ObjectResult[*core.Socket]
if args.SSH.Valid {
    sshSocket, err = args.SSH.Value.Load(ctx, srv)
    if err != nil {
        return nil, fmt.Errorf("failed to load SSH socket: %w", err)
    }
}

return ctr.Build(
    ctx,
    parent.Self(),
    buildctxDirID,
    args.Dockerfile,
    collectInputsSlice(args.BuildArgs),
    args.Target,
    secrets,
    args.NoInit,
    sshSocket,
)
```

* The key design choice here is:
  * `dockerBuild(ssh: ...)` is agnostic to whether the provided socket is an SSH handle or an opaque unix socket
  * we do not require a handle
  * we do not auto-scope a raw unix socket here
  * we just use the socket the user provided and let its recipe identity drive cacheability
* Named Dockerfile secrets are still a real requirement here, so `core.Container.Build(...)` will still call `secret.Self().Name(ctx)` when constructing the Dockerfile secret-name map.

### core/git_remote.go
#### Real execution path
* Leave `Remote(ctx)` and its `GetOrInitArbitrary(...)` session-scoped cache behavior alone for the first cut.
* Do **not** try to redesign that arbitrary-cache path in the same change as the secret/socket hard cut.
* The only required changes in this file are around how concrete auth/socket material is obtained at execution time and how auth/socket scope is represented in `remoteCacheScope(...)`.

#### `remoteCacheScope`
* Keep the current session-scoped remote metadata cache key logic as-is for now:

```go
func (repo *RemoteGitRepository) remoteCacheKey(ctx context.Context) (string, error) {
    clientMetadata, err := engine.ClientMetadataFromContext(ctx)
    inputs := []string{clientMetadata.SessionID, repo.URL.Remote()}
    inputs = append(inputs, repo.remoteCacheScope(ctx)...)
    return hashutil.HashStrings(inputs...).String(), nil
}
```

* But update `remoteCacheScope(...)` to use the new secret/socket values:
  * for auth token/header, append `string(secret.Self().Handle)`
  * for SSH auth socket, append `string(socket.Self().Handle)`
* The shape should become:

```go
func (repo *RemoteGitRepository) remoteCacheScope(ctx context.Context) []string {
    scope := make([]string, 0, 4)
    if token := repo.AuthToken; token.Self() != nil {
        if token.Self().Handle != "" {
            scope = append(scope, "token:"+string(token.Self().Handle))
        }
    }
    if header := repo.AuthHeader; header.Self() != nil {
        if header.Self().Handle != "" {
            scope = append(scope, "header:"+string(header.Self().Handle))
        }
    }
    if repo.AuthUsername != "" {
        scope = append(scope, "username:"+repo.AuthUsername)
    }
    if sshSock := repo.SSHAuthSocket; sshSock.Self() != nil {
        if sshSock.Self().Handle != "" {
            scope = append(scope, "ssh-auth-scope:"+string(sshSock.Self().Handle))
        }
    }
    return scope
}
```

#### `setup(ctx)`
* Replace the old store-backed execution lookups with direct secret/socket methods.
* SSH setup should change from:

```go
socketStore, err := query.Sockets(ctx)
sockpath, cleanup, err := socketStore.MountSocket(ctx, repo.SSHAuthSocket.Self().IDDigest)
opts = append(opts, gitutil.WithSSHAuthSock(sockpath))
```

* To:

```go
sockpath, cleanup, err := repo.SSHAuthSocket.Self().MountSSHAgent(ctx)
opts = append(opts, gitutil.WithSSHAuthSock(sockpath))
```

* HTTP token auth should change from store lookup to:

```go
password, err := repo.AuthToken.Self().Plaintext(ctx)
opts = append(opts, gitutil.WithHTTPTokenAuth(repo.URL, string(password), repo.AuthUsername))
```

* HTTP auth header should change similarly:

```go
authHeader, err := repo.AuthHeader.Self().Plaintext(ctx)
opts = append(opts, gitutil.WithHTTPAuthorizationHeader(repo.URL, string(authHeader)))
```

* No `query.Secrets(ctx)`
* No `query.Sockets(ctx)`
* No `SecretDigest(...)`
* No `socketStore.MountSocket(...)`

### core/schema/git.go
#### Explicit dependency shaping
* Keep auth token/header/socket explicit on `GitRepository`.
* Continue to ensure default SSH auth is reintroduced into the DAG explicitly through reinvocation.
* Keep the current overall selection/reinvoke structure in place; the important changes are to use handle-based identity and drop store-specific assumptions.

#### SSH socket shaping
* Keep the current `sshAuthSocketScoped` reinvoke pattern in place.
* The difference is that the scoped socket now carries a real `SessionResourceHandle`, not an `IDDigest`.
* So in the final `dgstInputs` shaping for both `git(...)` and `ref(...)`, replace:

```go
dgstInputs = append(dgstInputs, "sshAuthSock", sshAuthSock.Self().IDDigest.String())
```

* With:

```go
dgstInputs = append(dgstInputs, "sshAuthSock", string(sshAuthSock.Self().Handle))
```

* The existing logic that scopes a provided raw SSH socket through `_sshAuthSocket` can stay as-is conceptually.
  * it should now just produce a handle-bearing `Socket`
  * not a store alias

#### HTTP auth shaping
* For HTTP auth token/header, keep the current explicit DAG shaping:
  * if auth is supplied directly, load it
  * if auth is discovered from git credentials, reinvoke with an explicit `setSecret(...)`
* But the digest shaping should stop using bool-presence markers and should use the actual handle values.
* Replace:

```go
if httpAuthToken.Self() != nil {
    dgstInputs = append(dgstInputs, "authToken", strconv.FormatBool(httpAuthToken.Self() != nil))
}
if httpAuthHeader.Self() != nil {
    dgstInputs = append(dgstInputs, "authHeader", strconv.FormatBool(httpAuthHeader.Self() != nil))
}
```

* With:

```go
if httpAuthToken.Self() != nil && httpAuthToken.Self().Handle != "" {
    dgstInputs = append(dgstInputs, "authToken", string(httpAuthToken.Self().Handle))
}
if httpAuthHeader.Self() != nil && httpAuthHeader.Self().Handle != "" {
    dgstInputs = append(dgstInputs, "authHeader", string(httpAuthHeader.Self().Handle))
}
```

* That makes the cache identity reflect the actual secret equivalence handle rather than mere auth-method presence.

#### Persistence gap
* `GitRepository.EncodePersistedObject` for remote repos currently drops:
  * `SSHAuthSocket`
  * `AuthToken`
  * `AuthHeader`
  * `Services`
* The first cut should at least fix the auth/socket part.
* Extend `persistedRemoteGitRepositoryPayload` to include object refs:

```go
type persistedRemoteGitRepositoryPayload struct {
    URL               string   `json:"url"`
    SSHKnownHosts     string   `json:"sshKnownHosts,omitempty"`
    AuthUsername      string   `json:"authUsername,omitempty"`
    Platform          Platform `json:"platform"`
    SSHAuthSocketID   uint64   `json:"sshAuthSocketID,omitempty"`
    AuthTokenID       uint64   `json:"authTokenID,omitempty"`
    AuthHeaderID      uint64   `json:"authHeaderID,omitempty"`
}
```

* Encoding should:
  * persist `SSHAuthSocket`, `AuthToken`, and `AuthHeader` via `encodePersistedObjectRef(...)` if present
  * keep leaving `Services` alone for now unless we explicitly choose to tackle that too
* Decoding should:
  * load those result refs back with `loadPersistedObjectResultByResultID[...]`
  * set them back on `RemoteGitRepository`
* This keeps remote-repo persistence aligned with the new handle model without forcing a larger service-persistence redesign at the same time.

### core/schema/http.go
#### Direct secret-backed execution
* Replace direct secret-store lookups with the new secret methods.
* The change in `http(...)` should be very small:

```go
if args.AuthHeader.Valid {
    secret, err := args.AuthHeader.Value.Load(ctx, srv)
    if err != nil {
        return inst, err
    }
    authHeaderRaw, err := secret.Self().Plaintext(ctx)
    if err != nil {
        return inst, err
    }
    authHeader = string(authHeaderRaw)
}
```

* Delete:
  * `parent.Self().Secrets(ctx)`
  * `core.SecretDigest(ctx, secret)`
  * the old store lookup path
* Because the auth secret is already an explicit input to the HTTP file object, cache-hit validity can rely on the general session-resource gate rather than any HTTP-specific cache policy.

### core/git.go
#### Existing dependency attachment
* `GitRepository.AttachDependencyResults` already attaches `SSHAuthSocket`, `AuthToken`, and `AuthHeader`.
* That is good and means Git is much closer to the new cache model than Container is.
* Keep that attachment logic; it is already the right shape.

#### Remote repository persistence
* The main change in this file is persistence, not attachment.
* `persistedRemoteGitRepositoryPayload` should grow the auth/socket ref IDs as described above.
* `EncodePersistedObject(...)` for remote repos should:

```go
payload.Remote = &persistedRemoteGitRepositoryPayload{
    URL:           backend.URL.String(),
    SSHKnownHosts: backend.SSHKnownHosts,
    AuthUsername:  backend.AuthUsername,
    Platform:      backend.Platform,
}
if backend.SSHAuthSocket.Self() != nil {
    payload.Remote.SSHAuthSocketID, err = encodePersistedObjectRef(cache, backend.SSHAuthSocket, "git repository ssh auth socket")
}
if backend.AuthToken.Self() != nil {
    payload.Remote.AuthTokenID, err = encodePersistedObjectRef(cache, backend.AuthToken, "git repository auth token")
}
if backend.AuthHeader.Self() != nil {
    payload.Remote.AuthHeaderID, err = encodePersistedObjectRef(cache, backend.AuthHeader, "git repository auth header")
}
```

* `DecodePersistedObject(...)` should mirror that:

```go
if persisted.Remote.SSHAuthSocketID != 0 {
    backend.SSHAuthSocket, err = loadPersistedObjectResultByResultID[*Socket](ctx, dag, persisted.Remote.SSHAuthSocketID, "git repository ssh auth socket")
}
if persisted.Remote.AuthTokenID != 0 {
    backend.AuthToken, err = loadPersistedObjectResultByResultID[*Secret](ctx, dag, persisted.Remote.AuthTokenID, "git repository auth token")
}
if persisted.Remote.AuthHeaderID != 0 {
    backend.AuthHeader, err = loadPersistedObjectResultByResultID[*Secret](ctx, dag, persisted.Remote.AuthHeaderID, "git repository auth header")
}
```

* No secret/socket store logic should reappear here.

### core/schema/socket.go
* No major logic currently lives here.
* Only touch this file if we decide to expose session-resource kind metadata through the schema itself.
