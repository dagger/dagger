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

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

# Snapshot and BuildKit Session Cleanup

## Design
* Delete `engine/server/bk_session.go` entirely.
  * There is not a second server-created "buildkit session" layer beside real client attachables.
  * The model hard-cuts to one coherent client capability surface: real client attachables for client-owned capabilities, plus direct engine-owned implementations for everything else.
  * Anything that currently depends on buildkit-session side channels, attachable proxying, or `session.Manager` lookups gets an engine-native replacement rather than another shim.
  * That specifically includes the fake session-owned OCI content-store attachables used by `oci-layout` source paths today.
  * Those do not get replaced by another attachable indirection layer.
  * Non-registry image import paths should either:
    * read directly from an engine-owned store, or
    * copy content from a real client attachable into the engine OCI store,
    * and then import from that engine OCI store through one coherent core-local path.
  * Likewise, engine-local tarball export must not pretend the engine is a filesync target.
  * If the engine already has a real local destination path or writer, it should write tarball bytes there directly.

* Registry resolution, pull, push, and auth hard-cut to a Dagger-session-owned resolver facade in `engine/server/resolver`.
  * It is created once per `daggerSession`.
  * All nested `daggerClient`s in that Dagger session share it.
  * It is not global.
  * It is not per nested client.
  * It is not keyed by `session.Manager`.
  * It does not speak auth gRPC.
  * It does not know about `session.Group`.

* The resolver is implemented on top of `containerd/core/remotes/docker`, not as a port of `internal/buildkit/util/resolver`.
  * Reuse containerd's Docker authorizer for challenge parsing, basic/bearer auth, scope-aware token caching, auth invalidation, and token-fetch behavior.
  * Reuse containerd's Docker resolver/request logic for trusted host capabilities, `docker.io` handling, `401` challenge retries, `HEAD` to `GET` fallback for manifests, built-in request retries, multi-host fallback, and manifest-size enforcement.
  * Reuse containerd's fetcher/download machinery, including its download limiter and concurrent layer-fetch behavior, as-is.
  * Reuse containerd's distribution-source handling for push/export paths.
  * Reuse containerd's default transport and HTTPS-to-HTTP fallback behavior as-is.

* Preserve the current engine registry config input format.
  * The authoritative engine input remains `map[string]resolverconfig.RegistryConfig`.
  * We are not switching the engine's public config surface to `hosts.toml` / `certs.d`.
  * Under that boundary, adapt the current config format into containerd `docker.RegistryHosts` behavior as directly as possible.

* Dagger-owned behavior in this area is:
  * per-session auth lookup: Dagger session memory first, then real client attachables if needed
  * resolver lifecycle and explicit cleanup on Dagger session end
* Dagger resolve-mode semantics: `Default` and `ForcePull`
  * the engine-native public API shape: `ResolveImageConfig`, `Pull`, and `Push`
  * integration with `engine/snapshots` and the later removal of the remaining `session.Group`-shaped provider plumbing

* Conscious drops from the old BuildKit-shaped resolver model:
  * global resolver pools
  * pooled auth-handler namespaces shared across unrelated sessions
  * resolver idle-GC / `Clear()` hooks
  * `WithSession` and other resolver cloning around `session.Group`
  * token-authority protocol
  * `FetchToken` RPC proxying
  * cross-session auth-handler linking
  * basic-credential equality reuse across sessions
  * BuildKit-specific `limited` policies such as push-side limiting and "json gets an extra slot"
  * BuildKit-specific transport tweaks beyond containerd defaults
  * BuildKit-specific resolver warm-up / counter behavior
  * Windows-specific image/layer compatibility behavior
  * fake `oci-layout` session attachables for engine-owned stores
  * fake buildkit-session filesync source/target attachables for engine-local tarball export

* Finish porting and simplifying `engine/snapshots`.
  * `Container`, `Directory`, `File`, `CacheVolume`, and other snapshot-backed objects are the authoritative owners of snapshots.
  * The snapshot package is the coherent low-level snapshot/store implementation those objects use, not a competing source of truth about cache ownership.
  * There is no surviving `core/containersource` architecture.
  * Image resolution/import lives directly in `core` and feeds explicit image payloads into `engine/snapshots`.
  * There is no lazy blob concept in snapshot manager.
  * There is no snapshot-manager progress plumbing for pull/download work.
  * Laziness is a dagql concern only.
  * Image-pull snapshot creation belongs in the snapshot package, not in `internal/buildkit/util/pull`.
  * `internal/buildkit/util/pull` is split apart and deleted rather than preserved as a shared utility package.
  * The core center of gravity of the snapshot package is:
    * create new mutable snapshots
    * reopen existing mutable snapshots when a caller truly needs that
    * commit mutable snapshots to immutable snapshots
    * mount and release snapshots
    * look up snapshots by explicit identity
    * report snapshot size
  * The snapshot package does not own generic merge/diff graph abstractions, lazy remote content, progress, or resolver/session-shaped remote fetch callbacks.
  * The important image-import reuse rule is simple and explicit:
    * same parent snapshot identity plus same layer blob digest means the same immutable imported layer snapshot
    * shared layer-chain prefixes between images must therefore reuse the already-imported prefix snapshots
  * The lifecycle model should be crystal clear:
    * dagql owns which ordinary results are live
    * core objects own which snapshot handles they hold
    * snapshot handles own which containerd leases/resources they pin
    * containerd snapshot parentage owns filesystem ancestry
    * persistable dagql owner objects own named mutable snapshots
    * live runtime state is owned by explicit session-scoped runtime managers, not by dagql values and not by the snapshot manager
    * the snapshot manager is a thin layer for create/open/mount/commit/metadata/lease attachment, not a second liveness graph
  * Ordinary snapshot-backed values must not rely on snapshot-manager retain policy for lifetime.
  * Service/terminal runtime is a distinct third ownership bucket:
    * `Service` values are declarative recipes, not owners of runtime refs
    * `RunningService` and `Services` own live process/runtime state for the session
    * interactive terminal runtime rides on that same session-runtime bucket
    * dagql `SessionResourceHandle` remains for opaque session capabilities like secrets and sockets, not for service runtime ownership
  * Containerd leases still matter, but for the right reasons:
    * protecting in-progress mutable work
    * crash-safe cleanup of abandoned temporary work
    * deterministic owner leases for dagql-owned snapshots across restart
  * The snapshot manager should not maintain its own in-memory parent liveness graph or ref-counting graph for ordinary immutable values.
  * Ideally, there is no snapshot-manager ref counting left at all beyond per-handle cleanup of a concrete lease/mount cache.

* Make snapshot identity, retention, and size accounting line up with the dagql cache model.
  * Local cache accounting and pruning are now dagql-native.
  * Snapshot size, blob size, lazy content access, ref identity, and retain/release behavior should become one authoritative, comprehensible path that matches that model.
  * Cleaning this up should put us in a much better position to fix the current local-cache size-accounting failures without papering over deeper ownership confusion.

## Implementation Plan

### engine/server/server.go
#### Status
- [x] Pass the snapshot manager directly into `dagql.NewCache(...)`.
- [x] Keep the existing wipe-and-retry cold-start behavior around cache startup failures.
- [x] Open the builtin OCI content store once during server initialization and keep it on `Server` state.
- [x] Replace `bkresolver.NewRegistryConfig(...)` with the engine-owned `newRegistryHosts(...)` adapter.

#### Engine-wide registry host config
* Keep the existing engine config merge shape that produces `map[string]resolverconfig.RegistryConfig`.
* Replace `internal/buildkit/util/resolver.NewRegistryConfig(...)` with an engine-owned adapter built around containerd `core/remotes/docker` host types and helpers.
* `srv.registryHosts` remains an engine-wide static host-topology/TLS/header/capability callback.
* That engine-wide host config does not own per-session auth state.
* Prefer containerd defaults and semantics here instead of carrying forward BuildKit-specific host/transport behavior.
* The engine should also own the builtin OCI content store directly rather than surfacing it through a fake session attachable.
* Open that store once during server initialization and keep it on `Server` state alongside the main engine OCI store.
* The builtin OCI content store is a static engine-owned source of content, not part of the normal runtime lease/GC ownership model.
  * Do not try to apply ordinary result-owner or temporary-work lease semantics directly to the builtin source store.
  * If builtin content needs to participate in the normal import/ownership lifecycle, first copy the selected manifest closure into the metadata-backed engine OCI store.
  * From that point onward, builtin-image import follows the same `FromOCIStore(...)` / `ImportImage(...)` path as any other local OCI import.

```go
func newRegistryHosts(
    registries map[string]resolverconfig.RegistryConfig,
) docker.RegistryHosts

func applyRegistryHostConfig(
    host string,
    cfg resolverconfig.RegistryConfig,
    h docker.RegistryHost,
) (docker.RegistryHost, error)

func openBuiltinOCIStore() (content.Store, error)
```

* `newRegistryHosts(...)` keeps our current engine config format and adapts it into containerd host behavior.
* This is where we map our current `resolverconfig.RegistryConfig` values onto containerd host configuration concepts.
* This function does not look at session auth and does not create per-session authorizers.
* `newRegistryHosts(...)` must apply config per concrete host it is constructing, not just per origin registry.
  * For the origin host, use that origin host's config entry.
  * For each mirror entry:
    * parse the mirror into concrete `mirrorHost` plus path
    * look up `registries[mirrorHost]`
    * if present, use that mirror host config
    * otherwise fall back to the origin registry config
* Mirror-host-specific config must therefore win over origin-registry config for:
  * `PlainHTTP`
  * `Insecure`
  * CA bundles
  * client certificate/key pairs
* Origin-registry config is only the fallback default when a mirror host does not have its own explicit entry.
* `applyRegistryHostConfig(...)` is the narrow helper that applies those static host transport/TLS settings to one concrete `docker.RegistryHost`.
* Session auth stays separate and is layered on later by the session-owned resolver; `newRegistryHosts(...)` is only about static per-host transport/TLS/capability behavior.
* `openBuiltinOCIStore()` is the one place that binds the builtin SDK/content directory into an engine-owned `content.Store`.
  * It opens a static source store.
  * It is not itself a lease-managed runtime content store.
* After this cut there is no `bk_session.go`-level `sessioncontent.NewAttachable(...)` for builtin or engine OCI stores.
* Server startup should keep the cache construction order simple and direct:
  * construct the snapshot manager first, as it already does
  * then construct the dagql cache by passing that snapshot manager directly into `dagql.NewCache(...)`
  * let `dagql.NewCache(...)` perform the full startup import/hydration/owner-lease sync while it already has the persistence DB open
  * keep the current coarse behavior that startup import failure wipes the SQLite cache DB and cold-starts

```go
srv.engineCache, err = dagql.NewCache(ctx, dagqlCacheDBPath, srv.workerCache)
if err != nil {
    slog.Error("failed to create dagql cache, attempting to recover by removing existing cache db", "error", err)
    if err := os.Remove(dagqlCacheDBPath); err != nil && !os.IsNotExist(err) {
        slog.Error("failed to remove existing dagql cache db", "error", err)
    }
    srv.engineCache, err = dagql.NewCache(ctx, dagqlCacheDBPath, srv.workerCache)
    if err != nil {
        return nil, fmt.Errorf("failed to create dagql cache after removing existing db: %w", err)
    }
}
```

### engine/server/resolver/resolver.go
#### Status
- [x] Add the session-owned resolver package and public typed API surface.
- [x] Replace the current placeholder internals with the real containerd-backed resolve / pull / push implementation.
- [x] Clear resolver-owned host/auth cache state during `Close()`.

#### Responsibility
* This becomes the Dagger-session-owned facade for registry resolution, pull auth, and push auth.
* It is created once per `daggerSession` during Dagger session initialization and reused for the entire lifetime of that Dagger session.
* All nested `daggerClient`s within the same Dagger session share it.
* It is a thin engine-native wrapper around `containerd/core/remotes/docker`.
* It does not expose raw BuildKit- or session-shaped auth surfaces.
* It does not port BuildKit's custom resolver pool or custom authorizer logic forward.

#### Public shape
* The public surface should be centered on the actual external operations we perform today:
  * resolve image config
  * pull image metadata/layers
  * push image content

```go
var ErrCredentialsNotFound = errors.New("registry credentials not found")

type Credentials struct {
    Username string
    Secret   string
}

type AuthSource interface {
    Credentials(ctx context.Context, host string) (Credentials, error)
}

type ResolveMode int

const (
    ResolveModeDefault ResolveMode = iota
    ResolveModeForcePull
)

type Opts struct {
    Hosts        docker.RegistryHosts
    Auth         AuthSource
    ContentStore content.Store
    LeaseManager leases.Manager
}

type Resolver struct {
    hosts        docker.RegistryHosts
    auth         AuthSource
    contentStore content.Store
    leaseManager leases.Manager

    resolveConfigG flightcontrol.Group[*resolveImageConfigResult]

    mu      sync.Mutex
    closers []func()
}

func New(opts Opts) *Resolver
func (r *Resolver) Close() error

func (r *Resolver) ResolveImageConfig(
    ctx context.Context,
    ref string,
    opts ResolveImageConfigOpts,
) (resolvedRef string, dgst digest.Digest, config []byte, err error)

func (r *Resolver) Pull(
    ctx context.Context,
    ref string,
    opts PullOpts,
) (*PulledImage, error)

func (r *Resolver) PushImage(
    ctx context.Context,
    img *PushedImage,
    ref string,
    opts PushOpts,
) error

type ResolveImageConfigOpts struct {
    Platform    *specs.Platform
    ResolveMode ResolveMode
}

type PullOpts struct {
    Platform    specs.Platform
    ResolveMode ResolveMode
    LayerLimit  *int
}

type PulledImage struct {
    Ref          string
    ManifestDesc ocispecs.Descriptor
    ConfigDesc   ocispecs.Descriptor
    Layers       []ocispecs.Descriptor
    Nonlayers    []ocispecs.Descriptor

    // Releases the temporary localization lease held by Pull() until the
    // caller has rebound longer-lived ownership through ImportImage or
    // decided it is done with the localized closure.
    release func(context.Context) error
}

func (img *PulledImage) Release(context.Context) error

type PushedImage struct {
    RootDesc ocispecs.Descriptor

    // Local content closure already assembled in the engine content store.
    Provider content.InfoReaderProvider

    // Distribution/source annotations keyed by descriptor digest.
    // These are handed to push separately rather than serialized back into
    // final manifest/config JSON.
    SourceAnnotations map[digest.Digest]map[string]string
}

type PushOpts struct {
    Insecure    bool
    ByDigest    bool
    Annotations map[digest.Digest]map[string]string
}
```

* `ResolveImageConfig` is the only surviving image-config resolution API in the design.
* Delete the old worker/source-metadata resolution chain instead of layering the new resolver behind it.
* `Pull` replaces the remote-registry portion of the current image-pull path.
* `Pull` is an eager localization step.
* When `Pull(...)` returns successfully, the selected manifest/config/layer/nonlayer closure must already be local in the engine content store.
* We are explicitly not preserving a lazy/provider seam between `Pull(...)` and `ImportImage(...)`.
* `Push` replaces the meaningful registry path currently spread across exporter + `push.Push`.
* Containerd remotes machinery is an internal implementation detail of this package rather than the public API shape.

#### Credentials lookup
* Credentials lookup should become direct and boring.
* No session-shaped auth surface in the resolver API.
* The auth source only answers the one question we actually need from Dagger session state:
  * "do we have credentials for this host?"
* The implementation of `AuthSource` elsewhere in `engine/server` will check:
  * the per-session in-memory registry auth first
  * then the real client attachable connection if we need to ask the client
* The resolver package should not care how that answer was obtained.

```go
func (r *Resolver) credentials(
    ctx context.Context,
    host string,
) (Credentials, bool, error) {
    creds, err := r.auth.Credentials(ctx, host)
    switch {
    case err == nil:
        return creds, true, nil
    case errors.Is(err, ErrCredentialsNotFound):
        return Credentials{}, false, nil
    default:
        return Credentials{}, false, err
    }
}
```

```go
func NewSessionAuthSource(
    authProvider *auth.RegistryAuthProvider,
    getMainClientConn func(context.Context) (*grpc.ClientConn, error),
) AuthSource
```

* `NewSessionAuthSource(...)` is the concrete bridge between Dagger session state and the resolver package.
* It should implement:
  * in-memory session registry auth first
  * real main-client attachable fallback second
* The resolver package itself should only depend on `AuthSource`, not on session/client/server internals.

#### Containerd-backed internals
* Build session-local authorizers with `docker.NewDockerAuthorizer(...)`.
* Feed containerd authorizers from the Dagger `AuthSource`.
* Build session-local `docker.RegistryHosts` by wrapping the engine-wide host config with those session-local authorizers/clients.
* Build containerd resolvers with `docker.NewResolver(...)`.
* Let containerd own:
  * challenge parsing
  * basic/bearer auth mechanics
  * token caching and invalidation
  * oauth POST / GET fallback behavior
  * request retry behavior
  * multi-host fallback
  * manifest resolution semantics
  * download limiter / concurrent layer fetch behavior
  * distribution source handling
* The only resolver logic Dagger keeps here is:
  * choosing the correct local-vs-remote behavior for `ResolveMode`
  * assembling our `PulledImage` result shape for downstream snapshot creation
  * integrating registry pull/push behavior with our snapshot/content ownership model
* `PushImage(...)` is the resolver-owned registry push path.
  * It should accept a typed local image closure rather than raw provider/manager/root primitives.
  * It should use the session-owned auth state of the resolver directly.
  * It should consume distribution/source annotations as a separate input, not by rediscovering them from older exporter state.
* Auth mutation semantics are intentionally simple:
  * `withRegistryAuth` / `withoutRegistryAuth` mutate the session auth provider in place
  * the session-owned resolver is not recreated on auth mutation
  * there is no explicit token/authorizer invalidation hook
  * future operations are eventually consistent with those auth mutations
  * already-cached bearer/basic auth state may continue to be used until normal token expiry or challenge failure causes containerd to refresh it
* We are explicitly choosing that simplicity over exact immediate consistency for mid-session auth edits.

```go
func (r *Resolver) newResolver(
    ctx context.Context,
    mode ResolveMode,
) (remotes.Resolver, error)

func (r *Resolver) newHosts(
    ctx context.Context,
) (docker.RegistryHosts, error)
```

* `newHosts(...)` wraps the engine-wide `docker.RegistryHosts` with session-local `http.Client` / `docker.Authorizer` instances.
* `newResolver(...)` is the single internal constructor for the containerd resolver used by `ResolveImageConfig`, `Pull`, and `Push`.
* Session-local HTTP clients/transports created here must be tracked so `Close()` can deterministically drain them later.

#### Resolve-mode semantics
* `ResolveModeDefault`
  * for non-canonical refs like tags, resolve through the registry to learn the digest
  * for canonical refs, allow local engine content / OCI-store fallback before going remote when we already have the closure locally
* `ResolveModeForcePull`
  * use registry resolution only
  * no local canonical-content fallback

* We are explicitly not preserving BuildKit `images.Store`-based local resolve semantics in this cut.
* That does not ban `images.Store` everywhere.
  * It is still the right abstraction at the local host image boundary and any surviving local image-store export boundary.
  * What we are rejecting is using it as part of the engine-owned registry resolver semantics.
* Tag/name resolution is remote.
* The only local behavior we preserve is narrower and more honest:
  * canonical digest refs can reuse already-local engine content / OCI-store closure
* There is no surviving engine-owned `PreferLocal` mode.

#### `Pull(...)` contract
* `Pull(...)` should:
  * normalize/resolve the ref to the selected canonical descriptor
  * for canonical refs, first try a local-only closure walk against the engine content store
  * if the selected closure is already local, return it immediately without touching the registry
  * otherwise fetch/localize the full selected manifest/config/layer/nonlayer closure into the engine content store
  * create a flat temporary work lease that keeps that localized closure alive until the caller releases it
* The returned `PulledImage` should contain only:
  * resolved ref
  * selected manifest descriptor
  * config descriptor
  * layer descriptors
  * nonlayer descriptors
  * a `Release(...)` hook for the temporary localization lease
* It should not contain `content.Provider`.
* `Pull(...)` is the place where canonical local-content fallback lives.
* Tag/name resolution is still remote.

```go
func (r *Resolver) Pull(
    ctx context.Context,
    ref string,
    opts PullOpts,
) (_ *PulledImage, rerr error) {
    resolvedRef, rootDesc, err := r.resolveRootDescriptor(ctx, ref, opts)
    if err != nil {
        return nil, err
    }

    platform := platforms.Only(opts.Platform)

    if parsed, err := reference.Parse(resolvedRef); err == nil && parsed.Digest() != "" {
        if closure, found, done, err := r.tryLocalCanonicalClosure(ctx, resolvedRef, rootDesc, platform, opts.LayerLimit); err != nil {
            return nil, err
        } else if found {
            return &PulledImage{
                Ref:          resolvedRef,
                ManifestDesc: closure.ManifestDesc,
                ConfigDesc:   closure.ConfigDesc,
                Layers:       closure.Layers,
                Nonlayers:    closure.Nonlayers,
                release:      done,
            }, nil
        }
    }

    closure, done, err := r.fetchCanonicalClosure(ctx, resolvedRef, rootDesc, platform, opts.LayerLimit)
    if err != nil {
        return nil, err
    }
    return &PulledImage{
        Ref:          resolvedRef,
        ManifestDesc: closure.ManifestDesc,
        ConfigDesc:   closure.ConfigDesc,
        Layers:       closure.Layers,
        Nonlayers:    closure.Nonlayers,
        release:      done,
    }, nil
}
```

* `tryLocalCanonicalClosure(...)` should:
  * verify the canonical root digest exists locally
  * verify the stored content has a matching distribution source
  * walk the selected closure locally from the engine content store only
  * attach that local closure to that temporary work lease and return it
* `fetchCanonicalClosure(...)` should:
  * create a flat temporary work lease
  * fetch/localize the full selected closure into the engine content store
  * attach the whole localized closure to that lease
  * return the now-local closure
* Temporary pull leases should use expiry only as crash-safe fallback cleanup.
  * Normal success/failure paths should still release them explicitly.

#### Lifecycle / cleanup
* `Close()` is required.
* Session end should not rely on Go GC for resolver cleanup.
* The resolver owns the session-local HTTP clients/transports it creates around containerd authorizers.
* `Close()` should explicitly close idle registry connections and clear resolver-owned session-local caches.
* In-flight work is canceled by the Dagger session closing context, not by `Close()`.
* Because tracing/wrapping round-trippers do not themselves expose cleanup, the resolver must retain whatever underlying closeable transport/client handles it needs for deterministic cleanup.

#### Concrete simplification goal
* This file is where we hard-cut the live registry/image stack to Dagger-session ownership plus containerd-backed mechanics.
* After this lands:
  * registry auth lookup is a direct Dagger-session-owned interface
  * registry HTTP auth handling is containerd-backed and session-scoped
  * the old `internal/buildkit/util/resolver` auth/session surface is no longer part of the live architecture

### engine/server/session.go
#### Status
- [x] Add the session-owned resolver to `daggerSession` state and initialize it in `initializeDaggerSession(...)`.
- [x] Expose `RegistryResolver(ctx)` and `BuiltinOCIStore()` through the session/server accessors.
- [x] Close the session-owned resolver during normal session teardown.
- [x] Delete fake per-client buildkit-session creation from `initializeDaggerClient(...)` and stop passing `BkSession` into `buildkit.NewClient(...)`.

#### Session-owned resolver lifecycle
* Add the new resolver to `daggerSession` state.
* Initialize it in `initializeDaggerSession(...)` using:
  * the engine-wide `srv.registryHosts`
  * a session-owned `AuthSource`
  * engine content/image/lease dependencies
* All nested `daggerClient`s in the session should fetch and use that one shared resolver.
* During session teardown in `removeDaggerSession(...)`, explicitly call the resolver's `Close()` as part of normal cleanup rather than waiting for GC.
* That teardown hook is where we should deterministically drain idle registry HTTP connections and drop resolver-owned auth/token caches.
* Keep the responsibility split clear:
  * session closing context cancels in-flight registry work
  * resolver `Close()` cleans up pooled idle resources owned by the session-scoped resolver
* Auth edits do not trigger resolver replacement.
  * `sess.authProvider` is the one mutable session-auth source.
  * `sess.resolver` stays alive for the full Dagger session.
  * Mid-session auth changes are eventually consistent, not strongly invalidated.
* Implement the new `core.Query.Server` resolver accessor here too, alongside the rest of the session-owned accessors.

```go
type daggerSession struct {
    ...
    authProvider *auth.RegistryAuthProvider
    resolver     *serverresolver.Resolver
    ...
}

func (srv *Server) initializeDaggerSession(
    clientMetadata *engine.ClientMetadata,
    sess *daggerSession,
    failureCleanups *cleanups.Cleanups,
) error {
    ...
    sess.authProvider = auth.NewRegistryAuthProvider()
    sess.resolver = serverresolver.New(serverresolver.Opts{
        Hosts: srv.registryHosts,
        Auth: serverresolver.NewSessionAuthSource(
            sess.authProvider,
            func(ctx context.Context) (*grpc.ClientConn, error) {
                return srv.sessionMainClientConn(ctx, sess)
            },
        ),
        ContentStore: srv.contentStore,
        LeaseManager: srv.leaseManager,
    })
    failureCleanups.Add("close session resolver", sess.resolver.Close)
    ...
}

func (srv *Server) removeDaggerSession(ctx context.Context, sess *daggerSession) error {
    ...
    if sess.resolver != nil {
        errs = errors.Join(errs, sess.resolver.Close())
        sess.resolver = nil
    }
    ...
}

func (srv *Server) sessionMainClientConn(
    ctx context.Context,
    sess *daggerSession,
) (*grpc.ClientConn, error)

func (srv *Server) RegistryResolver(ctx context.Context) (*serverresolver.Resolver, error) {
    client, err := srv.clientFromContext(ctx)
    if err != nil {
        return nil, err
    }
    if client.daggerSession.resolver == nil {
        return nil, errors.New("session registry resolver not initialized")
    }
    return client.daggerSession.resolver, nil
}

func (srv *Server) BuiltinOCIStore() content.Store
```

* `sessionMainClientConn(...)` is the concrete bridge from Dagger session state to the real client attachables connection for the main caller.
* The important part is that the session-owned resolver is initialized once here and closed once here.
* Session teardown is the authoritative place where resolver cleanup happens.
* `RegistryResolver(...)` is implemented here because this file already owns the session-scoped server accessors and client/session lookup logic.
* `BuiltinOCIStore()` is just an engine-owned accessor.
  * It returns the static builtin source store opened during server initialization.
  * It is not session-scoped logic.
  * It is not a lease-managed runtime content store.
  * Core callsites that need normal import ownership must first copy from `BuiltinOCIStore()` into `query.OCIStore()`.
* Delete fake per-client buildkit-session creation from `initializeDaggerClient(...)`.
* Remove `client.buildkitSession` from `daggerClient`.
* Stop calling `srv.newBuildkitSession(...)`.
* Stop passing a server-created `BkSession` into `buildkit.NewClient(...)`.
* Real client attachable access continues to go through explicit caller-connection lookups, not through a server-created synthetic session.

### engine/server/bk_session.go
#### Status
- [x] Delete this file entirely.

#### Delete fake server-created buildkit session
* Delete this file entirely.
* There is no surviving server-created buildkit session layer.
* Delete:
  * fake auth proxy registration
  * fake engine-owned OCI store attachables
  * fake filesync source/target attachables
  * the in-memory loopback gRPC/session plumbing used only to host them
* The replacements are:
  * Dagger-session-owned registry resolver plus direct auth lookup
  * engine-owned builtin/main OCI stores accessed directly through server accessors
  * direct real-client attachable connections when caller-owned capabilities are needed

### engine/server/auth.go
#### Status
- [x] Delete this file entirely.

#### Delete fake auth proxy
* Delete this file entirely.
* `authProxy` only exists to proxy auth RPC through the fake server-created buildkit session.
* Registry auth now flows through:
  * Dagger-session in-memory auth state
  * direct main-client attachable fallback
  * the session-owned `engine/server/resolver` facade
* There is no remaining reason to expose `Credentials`, `FetchToken`, `GetTokenAuthority`, or `VerifyTokenAuthority` through a server-created buildkit session service.

### engine/snapshots/manager.go
#### Status
- [x] Add runtime persisted-snapshot metadata maps plus startup hydration from SQLite-backed dagql persistence.
- [x] Add deterministic owner-lease helpers for attach/remove/stale-owner cleanup.
- [x] Expose snapshot metadata rows back to dagql cache-close persistence.
- [x] Add the narrow `ApplySnapshotDiff(...)` primitive for root-level directory diffing without synthetic diff refs.

#### Snapshot-manager API cutover
* Re-center the snapshot manager API around real snapshot lifecycle primitives.
* `GetByBlob(...)` is deleted.
* Generic `Merge(...)` is deleted.
* Generic high-level `Diff(...)` is deleted.
* `Finalize(...)` is deleted.
* The manager must not maintain a second in-memory parent/liveness graph for ordinary immutable results.
* The manager must not maintain snapshot-manager ref counting for ordinary immutable results.
* Snapshot ancestry should come from containerd snapshot parentage, not snapshot-manager-held parent refs.
* Dagql/core objects own ordinary liveness; the snapshot manager only opens and closes concrete snapshot handles.
* Add a snapshot-manager entrypoint that turns an already-resolved image payload into a snapshot ref.
* This API is origin-agnostic: registry pulls and local OCI-layout imports should both feed the same snapshot-materialization path.
* Collapse snapshot-manager identity onto concrete snapshot identity.
* There should not be a second synthetic ref ID distinct from `SnapshotID()`.
* `Ref.ID()` and `SnapshotID()` should be the same value.
* Keep `ID()` only as a compatibility-shaped method if callers still need it, but it no longer means anything richer than the concrete snapshot identity.
* Any caller that means the concrete underlying snapshot resource must use that identity consistently.
* Sweep and update:
  * `CacheUsageIdentity()` implementations
  * metadata/index lookups that still use record IDs
  * any object persistence logic that still mixes `ID()` and `SnapshotID()`

```go
type ImportedImage struct {
    Ref          string
    ManifestDesc ocispecs.Descriptor
    ConfigDesc   ocispecs.Descriptor
    Layers       []ocispecs.Descriptor
    Nonlayers    []ocispecs.Descriptor
}

type ImportImageOpts struct {
    ImageRef   string
    RecordType client.UsageRecordType
}

type Accessor interface {
    ...
    New(ctx context.Context, parent ImmutableRef, opts ...RefOption) (MutableRef, error)
    GetBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (ImmutableRef, error)
    GetMutableBySnapshotID(ctx context.Context, snapshotID string, opts ...RefOption) (MutableRef, error)
    ApplySnapshotDiff(ctx context.Context, lower ImmutableRef, upper ImmutableRef, opts ...RefOption) (ImmutableRef, error)
    ImportImage(ctx context.Context, img *ImportedImage, opts ImportImageOpts) (ImmutableRef, error)
    AttachLease(ctx context.Context, leaseID, snapshotID string) error
    RemoveLease(ctx context.Context, leaseID string) error
    ...
}

type MutableRef interface {
    Ref
    Commit(context.Context) (ImmutableRef, error)
    InvalidateSize(context.Context) error
}

type ImmutableRef interface {
    Ref
}
```

* `ImportImage(...)` is the coherent replacement for the snapshot-creation logic currently buried under `internal/buildkit/util/pull` plus the wrapper pullers in `core/containersource` and `internal/buildkit/source/containerimage`.
* `New(...)`, `GetBySnapshotID(...)`, `GetMutableBySnapshotID(...)`, `ApplySnapshotDiff(...)`, `Commit(...)`, `Mount(...)`, `Release(...)`, and `Size(...)` remain the real core primitives here.
* `MutableRef.Commit(...)` should consume the mutable handle.
  * After `Commit(...)` succeeds, the mutable handle is no longer valid.
  * The returned immutable handle is the only valid handle from that operation.
  * Callers must not `Release(...)`, `Commit(...)`, or `Mount(...)` the consumed mutable afterward.
  * Stale post-commit mutable use should fail loudly with `errInvalid`; it should not silently no-op.
* There is deliberately no generic merge replacement in the snapshot-manager API.
* The only low-level compose/diff primitive that survives is `ApplySnapshotDiff(...)`, because `Directory.diff(...)` still needs to bottom out in the snapshotter's real diff-apply path.
* `WithDirectory(...)` should not use a snapshot-manager merge primitive anymore.
* It should always use the eager copy path with `EnableHardlinkOptimization` and commit the result directly.
* `ApplySnapshotDiff(...)` should also carry an explicit nil/no-op contract in both code and tests:
  * `ApplySnapshotDiff(nil, nil)` returns `nil, nil`
  * `ApplySnapshotDiff(nil, upper)` reopens/returns `upper` directly with no synthetic merge snapshot
  * equivalent `lower` / `upper` snapshots produce an empty diff result rather than relying on implicit snapshotter self-diff behavior
* `ImportImage(...)` is where layer-chain creation, nonlayer lease retention, image ref metadata, record type metadata, and snapshot labeling belong.
* `ImportImage(...)` is also where we persist the layer descriptor metadata still needed by export code:
  * media type
  * URLs
  * uncompressed digest annotation (`labels.LabelUncompressed`)
  * `buildkit/createdat`
  * `compression.EStargzAnnotations`
  * distribution-source annotations
* The saved layer/blob annotation allowlist should be explicit:
  * `labels.LabelUncompressed`
  * `buildkit/createdat`
  * `compression.EStargzAnnotations`
* Distribution-source annotations are a separate bucket.
  * Preserve them for later push/export behavior.
  * Do not lump them into vague generic "other export-relevant annotations" wording.
* Preservation of the uncompressed digest is required, not optional.
  * Export code uses it for `rootfs.diff_ids`.
  * Timestamp-rewrite fallback logic also depends on it.
* That metadata should be stored through the simplified surviving snapshot metadata/content-info path, not through desc handlers.
* `ImportImage(...)` is not a generic blob lookup API.
* It is an explicit image-import path that walks a platform-selected layer chain linearly.
* Shared-prefix reuse is handled here by indexing imported layers by:
  * parent snapshot identity
  * layer blob digest
* Keep the old cross-compression reuse behavior in a much narrower form:
  * first try exact reuse by `(parent snapshot identity, layer blob digest)`
  * if that misses, try fallback reuse by `(parent snapshot identity, uncompressed diffID)`
  * if the diffID fallback hits, reuse that snapshot and record the current blob descriptor as additional associated content on it
* That gives the desired behavior directly:
  * image `ABC` followed by image `ABCDE` reuses the already-imported snapshots for `A`, `AB`, and `ABC`
  * two compressed encodings of the same layer can still reuse the same imported snapshot when their uncompressed diffID matches
  * there is no need for the old `GetByBlob(...)` / blobchain / diffID API shape to survive as public snapshot-manager architecture
* `importImageLayer(...)` must also serialize concurrent imports on the canonical imported-layer identity.
  * The canonical lock key is `(parent snapshot identity, uncompressed diffID)`, not blob digest.
  * That is the broader equivalence class we actually care about:
    * two different compressed blobs with the same parent snapshot and the same uncompressed diffID must converge on one imported snapshot
    * serializing only by blob digest would still allow duplicate cross-compression imports
* Use the existing keyed mutex from `github.com/moby/locker` for this.
  * Do not use a global import lock.
  * Do not lock separately on both blob and diffID identities.
  * Take the diffID-scoped keyed lock first, then do the exact-blob and diffID lookups inside the critical section.
* `New(...)` should use only `parent.SnapshotID()` when preparing a child snapshot.
* It must not clone/finalize/extract the parent first.
* `GetBySnapshotID(...)` and `GetMutableBySnapshotID(...)` are plain handle-open operations.
* Opening another handle to the same snapshot should attach a new ephemeral handle lease to the same underlying snapshot resource rather than bumping an in-memory refcount.
* Reopen-by-snapshot-ID must not:
  * use `leaseID == snapshotID`
  * call `SetCachePolicyRetain()`
  * silently imply special long-lived ownership
  * attach owner leases
  * attach associated content resources
* Reopen is only "open another handle", not "make this snapshot durable again".
* `IdentityMapping()` is dead and should be removed entirely from the snapshot-manager surface.
* Dagql result ownership may still ask the snapshot manager for small helper operations:
  * attach a deterministic owner lease directly to an existing snapshot ID
  * remove that owner lease when the owning result is released
* Those helpers are lease utilities only; they are not a second liveness graph.
* The durable substrate is intentionally split:
  * containerd remains authoritative for intrinsic resource state:
    * snapshots
    * snapshot parentage
    * content existence/info
    * lease objects and lease-resource attachments
  * Dagger persistence remains authoritative for Dagger-specific snapshot metadata:
    * per-result snapshot ownership links
    * persistable mutable-owner snapshot links
    * direct `snapshotID -> content digest` associations
    * imported-layer exact and diffID reuse indexes
* Do not duplicate containerd intrinsic resource state in dagql.
* Also do not abuse containerd labels as a second user-space metadata database for Dagger-specific snapshot metadata.
* Dagger-specific snapshot metadata should live in SQLite through dagql persistence and be hydrated back into in-memory snapshot-manager indexes on startup.
* Containerd lease semantics should be explicit in the plan rather than implied:
  * all Dagger-created leases are flat by default
  * that includes:
    * snapshot-manager internal handle/view/mount leases used while opening and holding refs
    * temporary pull/import/export work leases
    * dagql owner leases that represent real object ownership
  * there is no surviving intentional non-flat Dagger lease in the target design
  * if some future path thinks it needs label-ref-traversing lease semantics, that should be treated as a design blocker rather than introduced implicitly
* Keep the Dagger lease classes distinct even though they are all flat:
  * snapshot-manager internal handle/view/mount leases used while creating/opening snapshots
  * temporary pull/import/export work leases
  * dagql owner leases that represent real object ownership
* Lease expiration is crash-safe eventual cleanup only.
  * `containerd.io/gc.expire` is not prompt reclamation
  * explicit release still matters on the normal success/failure path
* Ordinary release/delete paths should use normal async lease deletion.
  * reserve `leases.SynchronousDelete` for explicit prune or other intentionally immediate-cleanup semantics
  * do not use synchronous delete as the default behavior for ordinary handle release, ordinary owner-release cleanup, or ordinary temporary-work cleanup
* There should be only one ambient lease in context at a time.
  * temporary work runs under that one ambient temporary lease
  * later owner attachment happens explicitly through `AttachLease(...)`, not by stacking ambient lease scopes
* Snapshot creation-time leases are not enough by themselves because:
  * resources can be created before a dagql result ID exists
  * the same snapshot can be owned by more than one dagql result
  * imported persisted results can re-own snapshots created in an earlier engine run
* So when a dagql result or persistable mutable owner object becomes an owner of an existing snapshot, it must attach its own deterministic owner lease at that time.
* `AttachLease(...)` should be idempotent:
  * create the lease if it does not exist
  * attach the snapshot resource if it is not already attached
  * attach any associated content resources already recorded for that snapshot
  * treat already-exists as success
* `AttachLease(...)` should also maintain an in-memory reverse index of live owner leases by `snapshotID`.
  * That reverse index is only for same-process live backfill.
  * It is not persisted metadata and not a second ownership graph.
* `AttachLease(...)` is the owner-binding path, not the handle-open path.
* `AttachLease(...)` should:
  * attach the concrete snapshot resource for `snapshotID`
  * walk real containerd snapshot ancestry starting at `snapshotID`
  * load the direct associated-content digests recorded for each snapshot in that ancestry
  * attach all of those content digests to the owner lease
* That is how a final imported image root snapshot keeps:
  * ancestor layer blobs alive through snapshot ancestry
  * manifest/config/nonlayer content alive through direct content associations on the top snapshot
* `RemoveLease(...)` should delete the lease by ID and treat not-found as success.
  * It should also remove that lease ID from the in-memory reverse index for any snapshot IDs it was attached to.
  * This is an ordinary async delete path, not a `leases.SynchronousDelete` path.
* `recordSnapshotContent(...)` should do two things:
  * record the direct `snapshotID -> content digest` association in the in-memory snapshot-manager map
  * if that snapshot already has live owner leases attached, immediately backfill those owner leases
* The simplest rule is:
  * `recordSnapshotContent(...)` re-runs idempotent `AttachLease(...)` for every currently attached owner lease of that snapshot
  * in-memory metadata is enough during normal runtime; persistence happens later during dagql cache close
* Imported-layer reuse indexes should also be recorded only in the in-memory snapshot-manager maps during normal runtime.
  * Exact key: `(parentSnapshotID, blobDigest) -> snapshotID`
  * Fallback key: `(parentSnapshotID, diffID) -> snapshotID`
* On startup:
  * dagql imports persisted results and mutable-owner objects
  * dagql loads persisted snapshot metadata rows from SQLite
  * snapshot manager hydrates its in-memory `snapshotContent` and imported-layer indexes from those rows
  * dagql then idempotently re-synchronizes desired deterministic owner leases onto containerd
* Startup should therefore be reconciliation, not blind duplication:
  * dagql tells us what owners should exist
  * SQLite tells us the Dagger-specific snapshot metadata we need
  * containerd tells us what intrinsic resources and leases already exist
  * we sync, not duplicate
* `CachePolicyRetain` / `SetCachePolicyRetain()` / `HasCachePolicyRetain()` do not survive this cut.
* Cache policy is not a lifetime mechanism anymore.
* The old BuildKit-style question:
  * "should release delete this snapshot or leave it around because it is retained?"
  is replaced entirely by explicit lease ownership.
* A snapshot survives only because some handle lease or owner lease still points at it.

```go
type snapshotManager struct {
    Snapshotter   snapshot.Snapshotter
    ContentStore  content.Store
    LeaseManager  leases.Manager
    Applier       diff.Applier
    Differ        diff.Comparer
    metadataStore *metadataStore
    mountPool     sharableMountPool

    importedLayerByBlob   map[importedLayerKey]string
    importedLayerByDiffID map[importedLayerDiffKey]string
    snapshotContent       map[string]map[digest.Digest]struct{}

    // runtime-only reverse index for same-process lease backfill
    ownerLeasesBySnapshot map[string]map[string]struct{}
}

type SnapshotPersistentMetadataRows struct {
    SnapshotContent []SnapshotContentRow
    ImportedByBlob  []ImportedLayerBlobRow
    ImportedByDiff  []ImportedLayerDiffRow
}

func (cm *snapshotManager) SnapshotPersistentMetadataRows() SnapshotPersistentMetadataRows
func (cm *snapshotManager) LoadPersistentMetadata(rows SnapshotPersistentMetadataRows) error

func (cm *snapshotManager) New(
    ctx context.Context,
    parent ImmutableRef,
    opts ...RefOption,
) (MutableRef, error) {
    snapshotID := identity.NewID()
    parentSnapshotID := ""
    if parent != nil {
        parentSnapshotID = parent.SnapshotID()
    }
    if err := cm.Snapshotter.Prepare(ctx, snapshotID, parentSnapshotID); err != nil {
        return nil, err
    }
    leaseID := identity.NewID()
    if err := cm.attachSnapshotHandleLease(ctx, leaseID, snapshotID, nil); err != nil {
        return nil, err
    }
    md := cm.ensureMetadata(snapshotID)
    if err := initializeMetadata(md, opts...); err != nil {
        return nil, err
    }
    if err := md.queueSnapshotID(snapshotID); err != nil {
        return nil, err
    }
    if err := md.queueCommitted(false); err != nil {
        return nil, err
    }
    if err := md.commitMetadata(); err != nil {
        return nil, err
    }
    return &mutableRef{
        cm:         cm,
        snapshotID: snapshotID,
        leaseID:    leaseID,
        md:         md,
    }, nil
}

func (cm *snapshotManager) GetBySnapshotID(
    ctx context.Context,
    snapshotID string,
    opts ...RefOption,
) (ImmutableRef, error) {
    md, err := cm.loadMetadataForSnapshot(ctx, snapshotID, true, opts...)
    if err != nil {
        return nil, err
    }
    leaseID := identity.NewID()
    if err := cm.attachSnapshotHandleLease(ctx, leaseID, snapshotID, md); err != nil {
        return nil, err
    }
    return &immutableRef{
        cm:         cm,
        snapshotID: snapshotID,
        leaseID:    leaseID,
        md:         md,
    }, nil
}

func (cm *snapshotManager) ApplySnapshotDiff(
    ctx context.Context,
    lower ImmutableRef,
    upper ImmutableRef,
    opts ...RefOption,
) (ImmutableRef, error) {
    switch {
    case lower == nil && upper == nil:
        return nil, nil
    case lower == nil:
        return cm.GetBySnapshotID(ctx, upper.SnapshotID(), append(opts, NoUpdateLastUsed)...)
    default:
        snapshotID := identity.NewID()
        diffs := []snapshot.Diff{{
            Lower: lower.SnapshotID(),
        }}
        if upper != nil {
            diffs[0].Upper = upper.SnapshotID()
        }
        if err := cm.Snapshotter.Merge(ctx, snapshotID, diffs); err != nil {
            return nil, err
        }
        md := cm.ensureMetadata(snapshotID)
        if err := initializeMetadata(md, opts...); err != nil {
            return nil, err
        }
        if err := md.queueSnapshotID(snapshotID); err != nil {
            return nil, err
        }
        if err := md.queueCommitted(true); err != nil {
            return nil, err
        }
        if err := md.commitMetadata(); err != nil {
            return nil, err
        }
        leaseID := identity.NewID()
        if err := cm.attachSnapshotHandleLease(ctx, leaseID, snapshotID, md); err != nil {
            return nil, err
        }
        return &immutableRef{
            cm:         cm,
            snapshotID: snapshotID,
            leaseID:    leaseID,
            md:         md,
        }, nil
    }
}

func (cm *snapshotManager) AttachLease(
    ctx context.Context,
    leaseID string,
    snapshotID string,
) error

func (cm *snapshotManager) RemoveLease(
    ctx context.Context,
    leaseID string,
) error
```

* `GetBySnapshotID(...)` / `GetMutableBySnapshotID(...)` are intentionally not the place where persisted dagql results or persistable mutable-owner objects rebind ownership after restart.
* After restart:
  * dagql ordinary results reopen snapshots through `GetBySnapshotID(...)`
  * dagql result-owner cleanup still comes from deterministic result leases in `dagql/cache.go`
  * persistable mutable-owner objects reopen snapshots through their persisted snapshot links and then rely on ordinary dagql ownership
* Handle-open and owner-binding must stay separate.
* The migration order for removing retain policy from snapshot manager is:
  * first make reopen-by-snapshot-ID a pure handle-open path
  * then make mutable `Release(...)` lease-driven instead of policy-driven
  * then migrate persistable mutable owner objects and persisted ordinary results to explicit owner leases
  * then delete cache-policy metadata and API surface entirely

### engine/snapshots/pull.go
#### Status
- [x] Add `ImportedImage` / `ImportImageOpts` to the snapshot-manager surface.
- [x] Implement `ImportImage(...)` and explicit imported-layer reuse indexes in the snapshot package.
- [x] Record top-level manifest/config/nonlayer content directly on the imported root snapshot.

#### Image snapshot materialization
* Implement the actual image-import logic here rather than in a shared util package.
* Move in the parts of `internal/buildkit/util/pull/pull.go` that are actually about snapshot creation:
  * walking already-selected image layers in order
  * walking layer descriptors and turning them into refs using normal snapshot primitives
  * recording nonlayer blobs on the final imported snapshot so later owner-lease attachment can rebind them explicitly
  * applying image ref / record type metadata
  * persisting layer descriptor metadata needed later by export code
* This file should not know about `session.Group`, BuildKit session managers, token auth, or progress loggers.
* By the time `ImportImage(...)` runs, registry auth/resolution work is already finished and the selected image closure is already local in the engine content store.
* `ImportImage(...)` should consume only local descriptors/content.
* There is no surviving provider/lazy-fetch seam here.
* We are explicitly not preserving Windows image/layer compatibility behavior in this cut.
* Do not carry forward `SetLayerType("windows")`, `winlayers.UseWindowsLayerMode(...)`, or any equivalent special-case import path.
* `ImportImage(...)` should always return a concrete immutable root snapshot.
* For zero-layer images like `FROM scratch`, `ImportImage(...)` should create and commit a fresh empty root snapshot rather than returning `nil`.
* That explicit empty root snapshot is the top imported image snapshot for ownership purposes:
  * manifest/config/nonlayer digests are recorded directly on it
  * later `AttachLease(...)` rebinding works the same way as any other imported image
  * `core/container_image.go` can hand off one uniform rootfs snapshot handle into the container rootfs directory

```go
func (cm *snapshotManager) ImportImage(
    ctx context.Context,
    img *ImportedImage,
    opts ImportImageOpts,
) (ImmutableRef, error)
```

* `importImageLayer(...)` is the replacement for the old `GetByBlob(...)` path.
* It should:
  * derive the canonical import lock key from the parent snapshot identity plus the layer uncompressed diffID
  * acquire the keyed mutex from `github.com/moby/locker` for that diffID-scoped identity
  * inside that critical section, first look up an existing immutable ref for the exact `(parent snapshot identity, layer blob digest)` key
  * if that misses, look up an existing immutable ref for the fallback `(parent snapshot identity, uncompressed diffID)` key
  * if both miss, apply the layer blob into a newly-created mutable child snapshot and commit it to the final immutable ref
  * before releasing the keyed lock, record:
    * the exact blob index entry
    * the diffID fallback index entry
    * the blob/content metadata needed for export code paths
  * return a normal immutable ref
* The point is that this logic is explicit image-import code, not a generic "snapshot manager can conjure refs from blobs" API.
* `Pull(...)` should own a flat temporary work lease while it is localizing manifest/config/layer/nonlayer content.
* Once `Pull(...)` returns, that temporary work lease keeps the now-local closure safe for immediate `ImportImage(...)`.
* `ImportImage(...)` then records the snapshot->content associations needed for later owner-lease rebinding and normal dagql-owned snapshot ownership.
* That pull/import temporary work lease is a flat temporary lease.
  * It is just the ambient work root for the import operation.
  * It is not a long-lived ownership primitive and it is not a prompt-delete mechanism.
* `snapshotManager` should own a dedicated `importLayerLocker locker.Locker` for these diffID-scoped import critical sections.

```go
type importedLayerKey struct {
    ParentSnapshotID string
    BlobDigest       digest.Digest
}

type importedLayerDiffKey struct {
    ParentSnapshotID string
    DiffID           digest.Digest
}

func importedLayerDiffLockKey(parentSnapshotID string, diffID digest.Digest) string {
    return parentSnapshotID + "\x00" + diffID.String()
}

func (cm *snapshotManager) ImportImage(
    ctx context.Context,
    img *ImportedImage,
    opts ImportImageOpts,
) (ImmutableRef, error) {
    var current ImmutableRef
    for _, layer := range img.Layers {
        next, err := cm.importImageLayer(ctx, layer, current, opts)
        if err != nil {
            if current != nil {
                current.Release(context.WithoutCancel(ctx))
            }
            return nil, err
        }
        if current != nil {
            current.Release(context.WithoutCancel(ctx))
        }
        current = next
    }

    if current == nil {
        mut, err := cm.New(
            ctx,
            nil,
            WithRecordType(client.UsageRecordTypeRegular),
            WithDescription("import image rootfs (empty)"),
            WithImageRef(opts.ImageRef),
        )
        if err != nil {
            return nil, err
        }
        defer func() {
            if mut != nil {
                mut.Release(context.WithoutCancel(ctx))
            }
        }()

        ref, err := mut.Commit(ctx)
        if err != nil {
            return nil, err
        }
        mut = nil
        current = ref
    }

    topLevelContent := []ocispecs.Descriptor{
        img.ManifestDesc,
        img.ConfigDesc,
    }
    topLevelContent = append(topLevelContent, img.Nonlayers...)

    seen := map[digest.Digest]struct{}{}
    for _, desc := range topLevelContent {
        if desc.Digest == "" {
            continue
        }
        if _, ok := seen[desc.Digest]; ok {
            continue
        }
        seen[desc.Digest] = struct{}{}

        if err := cm.linkContentToRefLease(ctx, current, desc); err != nil {
            current.Release(context.WithoutCancel(ctx))
            return nil, err
        }
        if err := cm.recordSnapshotContent(ctx, current.SnapshotID(), desc); err != nil {
            current.Release(context.WithoutCancel(ctx))
            return nil, err
        }
    }
    return current, nil
}

func (cm *snapshotManager) importImageLayer(
    ctx context.Context,
    desc ocispecs.Descriptor,
    parent ImmutableRef,
    opts ImportImageOpts,
) (ImmutableRef, error) {
    parentSnapshotID := ""
    if parent != nil {
        parentSnapshotID = parent.SnapshotID()
    }
    diffID, err := diffIDFromDescriptor(desc)
    if err != nil {
        return nil, err
    }

    lockKey := importedLayerDiffLockKey(parentSnapshotID, diffID)
    cm.importLayerLocker.Lock(lockKey)
    defer cm.importLayerLocker.Unlock(lockKey)

    if hit, err := cm.getImportedLayerByBlob(ctx, parentSnapshotID, desc.Digest, opts); err != nil {
        return nil, err
    } else if hit != nil {
        return hit, nil
    }
    if hit, err := cm.getImportedLayerByDiffID(ctx, parentSnapshotID, diffID, opts); err != nil {
        return nil, err
    } else if hit != nil {
        if err := cm.indexImportedLayerByBlob(ctx, hit, parentSnapshotID, desc); err != nil {
            hit.Release(context.WithoutCancel(ctx))
            return nil, err
        }
        if err := cm.recordSnapshotContent(ctx, hit.SnapshotID(), desc); err != nil {
            hit.Release(context.WithoutCancel(ctx))
            return nil, err
        }
        return hit, nil
    }

    mut, err := cm.New(ctx, parent, opts)
    if err != nil {
        return nil, err
    }
    defer mut.Release(context.WithoutCancel(ctx))

    mnt, err := mut.Mount(ctx, false)
    if err != nil {
        return nil, err
    }
    defer mnt.Release()

    if err := cm.applyImageLayer(ctx, desc, mnt); err != nil {
        return nil, err
    }

    ref, err := mut.Commit(ctx)
    if err != nil {
        return nil, err
    }
    if err := cm.indexImportedLayerByBlob(ctx, ref, parentSnapshotID, desc); err != nil {
        ref.Release(context.WithoutCancel(ctx))
        return nil, err
    }
    if err := cm.indexImportedLayerByDiffID(ctx, ref, parentSnapshotID, diffID); err != nil {
        ref.Release(context.WithoutCancel(ctx))
        return nil, err
    }
    if err := cm.recordSnapshotContent(ctx, ref.SnapshotID(), desc); err != nil {
        ref.Release(context.WithoutCancel(ctx))
        return nil, err
    }
    return ref, nil
}
```

### engine/snapshots/opts.go
#### Descriptor handler cleanup
* Delete descriptor handlers from the snapshot package.
* They only exist to support lazy remote content and export-side blob plumbing that no longer belongs in the core snapshot model.
* Any export-only metadata that still needs to exist should be attached in a narrower export-facing structure rather than threaded through every ref.

### engine/snapshots/remote.go
#### Status
- [x] Replace the live `GetRemotes(...)` export path with local `ExportChain(...)`.
- [x] Remove lazy provider/session plumbing from the live snapshot export path.

#### Remote/lazy cleanup
* Strip this file down to only what export code still concretely needs.
* Remove all lazy remote provider plumbing from this file.
* Delete `lazyRefProvider`, `lazyMultiProvider`, `Unlazier`, and the `session.Group`-carrying remote fetch path.
* Delete `GetRemotes(...)` as the API shape.
* Exporters do not really need "remotes" in the old lazy/provider/session sense.
* What they actually need is much narrower:
  * for a concrete committed snapshot ref
  * produce one ordered local layer descriptor chain for export
  * ensure the needed blobs exist locally in the content store
  * optionally materialize a requested compression variant locally
  * expose a local provider/store for those blobs
* Replace `GetRemotes(...)` with a local-only helper like:

```go
type ExportLayer struct {
    Descriptor  ocispecs.Descriptor
    Description string
    CreatedAt   *time.Time
}

type ExportChain struct {
    Layers   []ExportLayer
    Provider    content.InfoReaderProvider
}

func (sr *immutableRef) ExportChain(
    ctx context.Context,
    refCfg config.RefConfig,
) (*ExportChain, error)
```

* No `session.Group`.
* No `createIfNeeded`.
* No `all`.
* No lazy fetch.
* No provider with hidden side effects.
* If export cannot proceed from local snapshot/content state, that is a bug in the earlier import/materialization pipeline.
* `ExportChain(...)` should:
  * walk the real concrete snapshot parent chain via `Snapshotter.Stat(...)`
  * for each concrete snapshot pair, get or eagerly synthesize an export blob locally
  * optionally perform local compression conversion if exporter opts require it
  * return layers in manifest order, including the per-layer metadata the writer still needs:
    * descriptor
    * description
    * created time
  * return a local content provider only
* Any remaining compression/export support that survives here should be narrowly scoped to exporter needs, not generalized as snapshot-manager laziness.

### engine/snapshots/blobs.go
#### Status
- [x] Add the eager `ensureExportBlob(...)` helper for the live export path.
- [x] Stop using compression-variant leases in the live export path.

#### Replace blobchain graph logic with eager local export-blob helpers
* This file still embodies too much of the old world and needs an explicit cut.
* Delete the synthetic graph/blobchain logic entirely:
  * `computeBlobChain(...)`
  * graph recursion over `Merge` / `Diff` / `Layer`
  * filter/layer-set logic
  * `computeChainMetadata(...)`
  * chainID/blobChainID bookkeeping tied to the old `GetByBlob(...)` architecture
* Keep only the narrow eager export-blob responsibilities that still matter:
  * for a concrete committed snapshot pair `(parentSnapshotID, snapshotID)`, get or create the local export blob
  * record export-relevant blob metadata on the committed snapshot
  * optionally perform local compression conversion if exporter opts still require it
* In other words, `blobs.go` survives only as a helper for eager local export blob synthesis over concrete snapshots, not as generic ref-graph/blobchain machinery.
* The replacement helper shape should be along the lines of:

```go
func (cm *snapshotManager) ensureExportBlob(
    ctx context.Context,
    parentSnapshotID string,
    snapshotID string,
    refCfg config.RefConfig,
) (ocispecs.Descriptor, error)
```

* `ensureExportBlob(...)` should:
  * look for already-recorded export blob metadata on the committed snapshot
  * if missing, diff the concrete `parentSnapshotID -> snapshotID` pair locally
  * write the blob to the local content store
  * record:
    * digest
    * media type
    * size
    * URLs
    * uncompressed digest annotation
    * any export-relevant annotations
  * return the final descriptor
* `ensureExportBlob(...)` must honor `refCfg.Compression`, including `Force=true`.
  * If a local export blob already matches the requested compression config, reuse it.
  * Otherwise synthesize the requested compression variant locally and record that descriptor metadata.
* Compression conversion remains a narrow local export helper only.
* No `session.Group`.
* No lazy fetch.
* No walking synthetic merge/diff graphs.

### engine/snapshots/filelist.go
#### Delete old tar-based file listing
* Delete this file entirely.
* `FileList(...)` is part of the old lazy-blob/export-attestation seam.
* It currently:
  * reads tar blobs
  * forces local blob fetch if needed
  * caches path lists as external metadata
* We already decided the attestation/exporter helper layers using this path are deleted.
* If future exporter functionality still needs file inspection, it should mount the snapshot directly and walk the filesystem there.
* Do not keep a tar-parsing file-list API in snapshot manager.

### engine/snapshots/remote_type.go
#### Status
- [x] Replace the old `Remote` DTO with the exporter-facing `ExportChain` / `ExportLayer` shape.

#### Keep only as the rich export DTO or fold into `remote.go`
* This file is not a deep design issue, but it does need an explicit decision.
* Either:
  * keep the rich `ExportChain` DTO here, or
  * fold the struct into `remote.go`
* In either case, it should be the same export-facing struct used by the writer:

```go
type ExportLayer struct {
    Descriptor  ocispecs.Descriptor
    Description string
    CreatedAt   *time.Time
}

type ExportChain struct {
    Layers      []ExportLayer
    Provider    content.InfoReaderProvider
}
```

* It is no longer a "remote" in the old lazy/pull/provider sense.
* It is just the exporter-facing local descriptor chain shape, including the per-layer history metadata the writer still needs.

### core/containersource/source.go
#### Status
- [x] Delete this file entirely.

#### Delete source architecture
* Delete this file.
* There is no surviving `Source`, `Identifier`, `Resolve`, or `SourceInstance` architecture in core after the cutover.
* The notion of "container source" is a BuildKit-shaped abstraction that no longer fits the design.
* Registry image resolution/pull belongs directly in `core` plus the session-owned resolver.
* OCI-layout / local-store image import belongs directly in `core` plus `engine/snapshots.ImportImage(...)`.
* We are not replacing this package with another identity/resolve/snapshot layer under a different name.
* The surviving core image-import flow is:
  * resolve/pull through `query.RegistryResolver(...)` for registry refs
  * copy manifest closure into `query.OCIStore()` for non-registry or client-provided store content
  * call `FromOCIStore(...)`
  * let `FromOCIStore(...)` be the one core-local path that turns OCI content into a `Container`
* `core/container_image.go`, `core/builtincontainer.go`, and `core/schema/host.go` own those direct paths explicitly.
* There is no surviving `source.Identifier` interface in core and no surviving `session.Manager` / `session.Group` dependency in this path.

### core/containersource/pull.go
#### Status
- [x] Delete this file entirely.

#### Delete wrapper puller
* Delete this file.
* It is only a Dagger-side wrapper around `internal/buildkit/util/pull` plus snapshot-manager details.
* After image snapshot materialization moves into `engine/snapshots` and registry/network behavior lives in the session resolver, this wrapper has no reason to exist.
* Delete the old `puller` state entirely:
  * `CacheAccessor`
  * `LeaseManager`
  * `RegistryHosts`
  * `ImageStore`
  * `Mode`
  * `RecordType`
  * `SessionManager`
  * `ResolverType`
  * `store`
  * `descHandlers`
  * `manifest` / `manifestKey` / `configKey`
* Registry pulls become:
  * `query.RegistryResolver(...).Pull(...)`
  * then `query.SnapshotManager().ImportImage(...)`
* OCI-layout/store-backed imports do not become a special pull mode.
  * They copy a manifest closure into `query.OCIStore()` and then call `FromOCIStore(...)`.
* The whole `core/containersource` package should disappear with it.

### core/containersource/identifier.go
#### Status
- [x] Delete this file entirely.

#### Delete identifier types and old provenance hook
* Delete this file.
* There is no surviving `ImageIdentifier` / `OCIIdentifier` type in core after the cutover.
* Do not replace it with another container-source identity layer.
* Inline the tiny remaining responsibilities at the real call sites in `core/container_image.go`:
  * `reference.Parse(...)`
  * canonical-ref validation
  * platform selection/validation
  * resolve-mode parsing where still relevant
* Delete the old `source.Identifier` conformance entirely.
* Delete the old `Capture(...)` provenance hook entirely.
  * We are not preserving BuildKit-shaped image provenance capture.
  * We do not care about keeping attestation/provenance plumbing alive in the new image-import path.
* Delete dead fields carried only for the old source abstraction, including the `LayerLimit` plumbing in this file.
* Any remaining image-source metadata that matters for runtime behavior belongs directly on the resolver/import inputs, not on a reusable identifier type.

### core/containersource/ocilayout.go
#### Status
- [x] Delete this file entirely.

#### Delete fake OCI-layout resolver path
* Delete this file.
* There is no surviving fake OCI-layout `remotes.Resolver` in core backed by:
  * `session.Manager`
  * `session.Group`
  * `sessioncontent.NewCallerStore(...)`
* Do not replace it with another resolver helper under `core/containersource`.
* Store-backed image import should instead:
  * read from the real client or builtin OCI/content store
  * copy the selected manifest closure into `query.OCIStore()`
  * call `FromOCIStore(...)`
* That means:
  * `host.containerImage(...)` keeps using the real client-facing `bk.ReadImage(...)` seam until we rename it
  * builtin-image import uses `query.BuiltinOCIStore()`
  * `FromInternal(...)` becomes a thin wrapper over `FromOCIStore(...)`
* Delete:
  * `getOCILayoutResolver(...)`
  * `ociLayoutResolver`
  * `withCaller(...)`
  * all `sessioncontent.NewCallerStore(...)` usage in this path
  * all digest-only OCI-layout resolution logic living behind fake resolver semantics

### internal/buildkit/frontend/dockerui/namedcontext.go
#### Status
- [x] Delete the `docker-image://...` named-context image path.
- [x] Delete the `oci-layout://...` named-context image path.

#### Delete dead named-context image path
* Delete this file's image-resolution path outright.
* Do not preserve another caller-backed OCI-layout or named-context resolver stack here.
* There is no surviving reason for this file to resolve image config, fetch OCI-layout content, or hold onto old session-shaped store access.
* This is an explicit feature drop in the hard-cut Dagger Dockerfile path:
  * BuildKit named-context image sources like `docker-image://...` and `oci-layout://...` are not part of the supported hard-cut `Directory.dockerBuild(...)` model
  * we are not preserving those forms implicitly through old frontend baggage
* Ordinary Dockerfile image resolution still works, but through a much smaller explicit adapter.
  * `dockerfile2llb.ConvertOpt.MetaResolver` should be satisfied by a tiny `llb.ImageMetaResolver` implementation backed directly by `query.RegistryResolver(...)`
  * normal `FROM alpine`
  * normal external-image `COPY --from=...` resolution that goes through `MetaResolver`
* What is being dropped here is the old frontend-configured named-context image source mechanism, not ordinary Dockerfile base-image resolution.
* If Dagger ever wants first-class Dockerfile named-context support in the future, it should be designed explicitly in Dagger terms rather than inherited accidentally from `dockerui`.

### core/query.go
#### Status
- [x] Add `RegistryResolver(context.Context)` to the runtime boundary interface.
- [x] Add `BuiltinOCIStore()` to the runtime boundary interface.
- [x] Rename the runtime snapshot accessor from `BuildkitCache()` to `SnapshotManager()`.
- [x] Delete `FileSyncer()` from the core runtime boundary.

#### Runtime boundary interface
* Add a session-scoped resolver accessor on the server/runtime boundary instead of routing image auth/resolve/pull/push through BuildKit session machinery.
* Rename the core runtime snapshot accessor from `BuildkitCache()` to `SnapshotManager()`.
* Keep `Auth()` temporarily, but only as the narrow session-auth mutation seam for `withRegistryAuth` / `withoutRegistryAuth`.
* Delete `BuildkitSession()` entirely from the core runtime boundary.
* Delete `FileSyncer()` from the core runtime boundary.
* Keep `Buildkit()` temporarily as the execution/client seam for the parts of the engine that are still genuinely built on the BuildKit client:
  * service startup and execution paths
  * exec/gateway-driven paths that still need the live BuildKit client
  * host image reader/writer seams such as `ReadImage(...)` / `WriteImage(...)` until they get a better-named home
  * any remaining SDK/module/LLM paths still built directly on that client
* `Buildkit()` is therefore not part of the target resolver/snapshot/filesync ownership architecture anymore, but it does remain as a temporary runtime boundary for the still-live execution and client-transport slices.
* Snapshot lifecycle code in `core` should talk to the engine's snapshot manager by that real name.
* Add an engine-owned builtin OCI store accessor so builtin-image import does not reopen filesystem stores in core.
* Keep the direct caller-attachable-connection seam for the places that genuinely need to speak to the client.

```go
type Server interface {
    ...
    SnapshotManager() bkcache.SnapshotManager
    RegistryResolver(context.Context) (*serverresolver.Resolver, error)
    BuiltinOCIStore() content.Store
    SpecificClientAttachableConn(context.Context, string) (*grpc.ClientConn, error)
    ...
}
```

### core/schema/container.go
#### Status
- [x] Switch schema-level image config resolution from `query.Buildkit(ctx).ResolveImageConfig(...)` to `query.RegistryResolver(...)`.
- [x] Point Dockerfile `MetaResolver` at the explicit `dockerfileImageMetaResolver` adapter instead of the Buildkit client.
- [x] Finish replacing the old live image-lazy path with `ContainerFromImageRefLazy`.
- [x] Stop `exportImage(...)` from manually leasing caller content stores or selecting `asTarball`; route it through `Container.Export(...)` instead.

#### Schema-layer image resolution
* `container.from(...)` must call `query.RegistryResolver(...)` directly.
* Delete `query.Buildkit(ctx).ResolveImageConfig(...)` instead of leaving it behind as an intermediate wrapper.
* Keep the current dagql identity behavior:
  * tag-only refs resolve to a canonical digest once per Dagger session
  * canonical refs are content-addressed directly by digest + platform
* Keep the current lazy object shape:
  * merge image config eagerly
  * defer rootfs materialization to container lazy evaluation
* Distinguish imported empty images from plain scratch containers explicitly:
  * `container()` may still start as a declarative "no imported rootfs yet" container
  * but `container.from(...)` must never complete with a nil rootfs snapshot just because the image has zero filesystem layers
  * once imported-image rootfs materialization runs, the container rootfs directory is backed by a real immutable snapshot handle in all cases
* `import_(...)` remains a shallow schema helper:
  * open the source file
  * call `ctr.Import(ctx, r, args.Tag)`
  * let the real image import logic live in `core/container_image.go`
* The tarball branch of `exportImage(...)` should stop selecting `asTarball`, remounting the resulting file, reopening it, and copying it again.
* Instead it should call the direct tarball writer helper in `engine/buildkit/containerimage.go` with the real destination writer returned by `WriteImage(...)`.
* `withRegistryAuth` / `withoutRegistryAuth` continue to mutate Dagger-session auth state directly in this file.
* Those auth edits are session-global and eventually consistent for future registry operations.
  * They do not rebuild the session resolver.
  * They do not promise immediate eviction of already-cached bearer/basic auth state.
* `Container.Build(...)` should stop passing the Buildkit client itself as `dockerfile2llb.ConvertOpt.MetaResolver`.
* Instead, Dockerfile conversion should use a tiny `llb.ImageMetaResolver` adapter backed directly by `query.RegistryResolver(...)`.
* That adapter is the only surviving BuildKit-parser-facing image metadata seam we keep.
* It should:
  * implement `ResolveImageConfig(ctx, ref, sourceresolver.Opt)`
  * translate Dockerfile/llb resolve-mode strings into `serverresolver.ResolveMode`
  * call `query.RegistryResolver(...).ResolveImageConfig(...)`
  * return:
    * resolved ref string
    * digest
    * config bytes
* It should not:
  * construct `pb.SourceOp`
  * call `Worker.ResolveSourceMetadata(...)`
  * know about `docker-image://` or `oci-layout://` worker source routing
  * speak `session.Manager` / `session.Group`

```go
type dockerfileImageMetaResolver struct {
    resolver *serverresolver.Resolver
}

func (r dockerfileImageMetaResolver) ResolveImageConfig(
    ctx context.Context,
    ref string,
    opt sourceresolver.Opt,
) (string, digest.Digest, []byte, error) {
    resolveMode := serverresolver.ResolveModeDefault
    if opt.ImageOpt != nil {
        switch opt.ImageOpt.ResolveMode {
        case "", pb.AttrImageResolveModeDefault:
            resolveMode = serverresolver.ResolveModeDefault
        case pb.AttrImageResolveModeForcePull:
            resolveMode = serverresolver.ResolveModeForcePull
        default:
            return "", "", nil, fmt.Errorf("unsupported image resolve mode %q", opt.ImageOpt.ResolveMode)
        }
    }

    return r.resolver.ResolveImageConfig(ctx, ref, serverresolver.ResolveImageConfigOpts{
        Platform:    opt.Platform,
        ResolveMode: resolveMode,
    })
}
```

* `Container.Build(...)` should therefore do:

```go
rslvr, err := query.RegistryResolver(ctx)
if err != nil {
    return nil, fmt.Errorf("failed to get registry resolver: %w", err)
}

convertOpt := dockerfile2llb.ConvertOpt{
    Config: dockerui.Config{
        BuildArgs: buildArgMap,
        Target:    target,
    },
    MainContext:    &mainContext,
    TargetPlatform: ptr(container.Platform.Spec()),
    MetaResolver:   dockerfileImageMetaResolver{resolver: rslvr},
}
```

* The generic BuildKit worker/source-metadata path is not part of the target Dagger-owned Dockerfile architecture.
* If any remaining caller still depends on generic llb image-source metadata resolution through `Worker.ResolveSourceMetadata(...)`, that caller is not migrated and should block the cut rather than pulling the old path forward.

```go
if dest := imageWriter.Tarball; dest != nil {
    defer dest.Close()
    err = bk.WriteContainerImageTarball(
        ctx,
        dest,
        inputByPlatform,
        useOCIMediaTypes(args.MediaTypes),
        string(args.ForcedCompression.Value),
    )
    if err != nil {
        return core.Void{}, err
    }
    return core.Void{}, dest.Close()
}
```

```go
func (s *containerSchema) from(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Container],
    args containerFromArgs,
) (inst dagql.ObjectResult[*core.Container], _ error) {
    ...
    rslvr, err := query.RegistryResolver(ctx)
    if err != nil {
        return inst, err
    }
    refName, err := reference.ParseNormalizedNamed(args.Address)
    if err != nil {
        return inst, fmt.Errorf("parse image address %q: %w", args.Address, err)
    }
    refName = reference.TagNameOnly(refName)

    if canonical, ok := refName.(reference.Canonical); ok {
        _, _, cfgBytes, err := rslvr.ResolveImageConfig(ctx, canonical.String(), serverresolver.ResolveImageConfigOpts{
            Platform:    ptr(platform.Spec()),
            ResolveMode: serverresolver.ResolveModeDefault,
        })
        if err != nil {
            return inst, fmt.Errorf("resolve image config %q: %w", canonical.String(), err)
        }
        var imgSpec dockerspec.DockerOCIImage
        if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
            return inst, fmt.Errorf("decode image config %q: %w", canonical.String(), err)
        }

        ctr, err := core.NewContainerChildWithoutFS(ctx, parent)
        if err != nil {
            return inst, err
        }
        ctr.Config = core.MergeImageConfig(ctr.Config, imgSpec.Config)
        ctr.Platform = core.Platform(platforms.Normalize(imgSpec.Platform))
        ctr.ImageRef = canonical.String()
        rootfsDir := &core.Directory{
            Dir:      "/",
            Platform: ctr.Platform,
            Services: ctr.Services,
            Lazy:     &core.DirectoryFromContainerLazy{Container: ctr},
        }
        ctr.FS = &core.ContainerDirectorySource{Value: rootfsDir}
        ctr.Lazy = &core.ContainerFromImageRefLazy{
            LazyState:    core.NewLazyState(),
            CanonicalRef: canonical.String(),
            ResolveMode:  serverresolver.ResolveModeDefault,
        }
        ...
        return inst.WithContentDigest(ctx, hashutil.HashStrings(
            "container.from",
            canonical.Digest().String(),
            ctr.Platform.Format(),
        ))
    }

    _, dgst, _, err := rslvr.ResolveImageConfig(ctx, refName.String(), serverresolver.ResolveImageConfigOpts{
        Platform:    ptr(platform.Spec()),
        ResolveMode: serverresolver.ResolveModeDefault,
    })
    if err != nil {
        return inst, fmt.Errorf("resolve image %q: %w", refName.String(), err)
    }
    canonical, err := reference.WithDigest(refName, dgst)
    if err != nil {
        return inst, fmt.Errorf("set digest on %q: %w", refName.String(), err)
    }
    return reselectFromCanonical(ctx, parent, canonical.String())
}
```

### core/schema/host.go
#### Status
- [x] Stop calling `query.BuildkitSession()` from `host.directory(...)`.
- [x] Stop calling `query.FileSyncer()` directly from `host.directory(...)`.
- [x] Route `host.directory(...)` through the hidden filesync mirror object plus direct caller attachable connection.
- [x] Resolve host-store image manifests through a dedicated local selector with no arbitrary platform fallback.

#### Host directory and image import paths
* `host.directory(...)` should stop calling `query.BuildkitSession()` entirely.
* It should stop calling `query.FileSyncer()` entirely.
* Instead it should:
  * get the current client attachable connection directly
  * resolve the hidden internal `_clientFilesyncMirror(stableClientID, drive)` result
  * ask that mirror object to produce the ordinary immutable snapshot result for this call
* There is no session-manager or fake buildkit-session lookup in this path after the cut.
* The current client metadata already exists in context:
  * use `ClientStableID` to key the persistent mirror object
  * use `ClientID` to get the caller-owned attachable connection directly
* If `ClientStableID` is missing:
  * do not invent a fake durable mirror identity
  * do not route through `_clientFilesyncMirror(...)`
  * create an ephemeral non-persisted `ClientFilesyncMirror` object directly for the current call
  * give it a random non-shared runtime identity and let it die with the ordinary call/result lifecycle
* Stable client identity therefore means durable mirror reuse.
* Missing stable client identity means ephemeral mirror only.
* The filesync snapshot returned from the mirror object here should be handed directly to `NewDirectoryWithSnapshot(...)`.
* Do not reopen it and do not separately release it after successful constructor handoff.
* If object/result construction fails after `NewDirectoryWithSnapshot(...)` succeeds, release the constructed `Directory` object before returning the error.

```go
clientMetadata, err := engine.ClientMetadataFromContext(ctx)
if err != nil {
    return inst, fmt.Errorf("failed to get client metadata: %w", err)
}
callerConn, err := query.SpecificClientAttachableConn(ctx, clientMetadata.ClientID)
if err != nil {
    return inst, fmt.Errorf("failed to get caller attachable conn: %w", err)
}

drive := pathutil.GetDrive(absRootCopyPath)

var mirror *core.ClientFilesyncMirror
if clientMetadata.ClientStableID != "" {
    var persistedMirror dagql.ObjectResult[*core.ClientFilesyncMirror]
    if err := srv.Select(ctx, parent, &persistedMirror, dagql.Selector{
        Field: "_clientFilesyncMirror",
        Args: []dagql.NamedInput{
            {Name: "stableClientID", Value: dagql.NewString(clientMetadata.ClientStableID)},
            {Name: "drive", Value: dagql.NewString(drive)},
        },
    }); err != nil {
        return inst, fmt.Errorf("failed to load client filesync mirror: %w", err)
    }
    mirror = persistedMirror.Self()
} else {
    mirror = &core.ClientFilesyncMirror{
        Drive:       drive,
        EphemeralID: identity.NewID(),
    }
    if err := mirror.EnsureCreated(ctx, query); err != nil {
        return inst, fmt.Errorf("failed to create ephemeral client filesync mirror: %w", err)
    }
}

ref, contentDgst, err := mirror.Snapshot(ctx, query, callerConn, absRootCopyPath, snapshotOpts)
if err != nil {
    return inst, fmt.Errorf("failed to get snapshot: %w", err)
}

dir, err := core.NewDirectoryWithSnapshot("/", query.Platform(), nil, ref)
if err != nil {
    return inst, err
}
inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
if err != nil {
    _ = dir.OnRelease(context.WithoutCancel(ctx))
    return inst, err
}
```

* `host.containerImage(...)` stays, but its implementation hard-cuts away from `core/containersource` and fake `oci-layout` session stores.
* Keep `bk.ReadImage(...)` as the host-side reader because it already talks to the real client attachable connection rather than `bk_session.go`.
* `images.Store` is intentional in this path.
  * Local host image import is the one place where we really do want containerd's named-image abstraction.
  * This path is not registry resolution and it is not snapshot import.
  * `images.Store` is only used here to map `name -> target descriptor` in the client/backend's local image store.
  * The actual imported content still comes from the paired client/backend `content.Store`.
* For the store-backed case:
  * use the client-provided `images.Store` / `content.Store` / `leases.Manager` from `ReadImage(...)`
  * look up the named image in `images.Store`
  * resolve the concrete manifest descriptor from the paired client `content.Store` using a dedicated host-store helper
    * do not use `core.ResolveIndex(...)` here
    * the host-store helper must support:
      * single manifest targets
      * OCI indexes
      * Docker manifest lists
    * it should follow containerd manifest-selection semantics as closely as practical
    * if the requested platform manifest is not actually present locally, fail explicitly
    * do not fall back to an arbitrary locally-present manifest from another platform
  * copy the manifest closure into `query.OCIStore()`
  * call `ctr.FromOCIStore(ctx, manifestDesc, refName.String())`
* For the tarball fallback:
  * keep streaming the tarball
  * call `ctr.Import(ctx, src, "")`
* `_builtinContainer(...)` should:
  * read from `query.BuiltinOCIStore()`
  * copy the builtin manifest closure into `query.OCIStore()`
  * call `ctr.FromOCIStore(ctx, manifestDesc, "")`
  * stop doing a second config-only update pass

```go
func (s *hostSchema) containerImage(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Host],
    args hostContainerArgs,
) (inst dagql.Result[*core.Container], err error) {
    ...
    imageReader, err := bk.ReadImage(ctx, refName.String())
    if err != nil {
        return inst, err
    }

    if imageReader.ContentStore != nil && imageReader.ImagesStore != nil {
        img, err := imageReader.ImagesStore.Get(ctx, refName.String())
        if err != nil {
            return inst, fmt.Errorf("get host image %q: %w", refName.String(), err)
        }
        manifestDesc, err := resolveHostStoreManifest(
            ctx,
            imageReader.ContentStore,
            img.Target,
            query.Platform().Spec(),
        )
        if err != nil {
            return inst, fmt.Errorf("resolve host image manifest: %w", err)
        }

        ctx, release, err := leaseutil.WithLease(ctx, query.LeaseManager(), leaseutil.MakeTemporary)
        if err != nil {
            return inst, err
        }
        defer release(context.WithoutCancel(ctx))

        if err := contentutil.CopyChain(ctx, query.OCIStore(), imageReader.ContentStore, *manifestDesc); err != nil {
            return inst, fmt.Errorf("copy host image content: %w", err)
        }

        ctr := core.NewContainer(query.Platform())
        ctr, err = ctr.FromOCIStore(ctx, *manifestDesc, refName.String())
        if err != nil {
            return inst, err
        }
        return dagql.NewResultForCurrentCall(ctx, ctr)
    }

    if src := imageReader.Tarball; src != nil {
        defer src.Close()
        ctr := core.NewContainer(query.Platform())
        ctr, err := ctr.Import(ctx, src, "")
        if err != nil {
            return inst, err
        }
        return dagql.NewResultForCurrentCall(ctx, ctr)
    }

    return inst, errors.New("invalid image reader")
}

func resolveHostStoreManifest(
    ctx context.Context,
    store content.Store,
    target ocispec.Descriptor,
    platform ocispec.Platform,
) (*ocispec.Descriptor, error) {
    matcher := platforms.Only(platforms.Normalize(platform))

    switch {
    case images.IsManifestType(target.MediaType):
        ok, err := hostManifestMatchesPlatform(ctx, store, target, matcher)
        if err != nil {
            return nil, fmt.Errorf("inspect host manifest %s: %w", target.Digest, err)
        }
        if !ok {
            return nil, fmt.Errorf(
                "host image manifest %s does not match requested platform %s",
                target.Digest,
                platforms.Format(platform),
            )
        }
        return &target, nil

    case images.IsIndexType(target.MediaType):
        return resolveHostStoreManifestFromIndex(ctx, store, target, matcher, platform)

    default:
        return nil, fmt.Errorf("unsupported host image target media type %s", target.MediaType)
    }
}

func resolveHostStoreManifestFromIndex(
    ctx context.Context,
    store content.Store,
    target ocispec.Descriptor,
    matcher platforms.MatchComparer,
    requested ocispec.Platform,
) (*ocispec.Descriptor, error) {
    indexBlob, err := content.ReadBlob(ctx, store, target)
    if err != nil {
        return nil, fmt.Errorf("read host image index %s: %w", target.Digest, err)
    }

    var idx ocispec.Index
    if err := json.Unmarshal(indexBlob, &idx); err != nil {
        return nil, fmt.Errorf("unmarshal host image index %s: %w", target.Digest, err)
    }

    candidates, err := hostManifestCandidates(ctx, store, idx.Manifests, matcher)
    if err != nil {
        return nil, err
    }
    if len(candidates) == 0 {
        return nil, fmt.Errorf(
            "host image %s has no manifest for requested platform %s",
            target.Digest,
            platforms.Format(requested),
        )
    }

    for _, candidate := range candidates {
        switch {
        case images.IsManifestType(candidate.MediaType):
            ok, err := hostManifestMatchesPlatform(ctx, store, candidate, matcher)
            if err != nil {
                return nil, fmt.Errorf(
                    "requested platform manifest %s is not fully present in the local host store: %w",
                    candidate.Digest,
                    err,
                )
            }
            if ok {
                return &candidate, nil
            }

        case images.IsIndexType(candidate.MediaType):
            return resolveHostStoreManifestFromIndex(ctx, store, candidate, matcher, requested)
        }
    }

    return nil, fmt.Errorf(
        "host image %s does not have requested platform %s available locally",
        target.Digest,
        platforms.Format(requested),
    )
}

func hostManifestCandidates(
    ctx context.Context,
    store content.Store,
    manifests []ocispec.Descriptor,
    matcher platforms.MatchComparer,
) ([]ocispec.Descriptor, error) {
    candidates := make([]ocispec.Descriptor, 0, len(manifests))

    for _, desc := range manifests {
        switch {
        case images.IsManifestType(desc.MediaType):
            if desc.Platform != nil {
                if matcher.Match(*desc.Platform) {
                    candidates = append(candidates, desc)
                }
                continue
            }

            ok, err := hostManifestMatchesPlatform(ctx, store, desc, matcher)
            if err != nil {
                return nil, err
            }
            if ok {
                candidates = append(candidates, desc)
            }

        case images.IsIndexType(desc.MediaType):
            if desc.Platform == nil || matcher.Match(*desc.Platform) {
                candidates = append(candidates, desc)
            }
        }
    }

    sort.SliceStable(candidates, func(i, j int) bool {
        if candidates[i].Platform == nil {
            return false
        }
        if candidates[j].Platform == nil {
            return true
        }
        return matcher.Less(*candidates[i].Platform, *candidates[j].Platform)
    })

    return candidates, nil
}

func hostManifestMatchesPlatform(
    ctx context.Context,
    store content.Store,
    desc ocispec.Descriptor,
    matcher platforms.MatchComparer,
) (bool, error) {
    manifestBlob, err := content.ReadBlob(ctx, store, desc)
    if err != nil {
        return false, err
    }

    var manifest ocispec.Manifest
    if err := json.Unmarshal(manifestBlob, &manifest); err != nil {
        return false, fmt.Errorf("unmarshal manifest %s: %w", desc.Digest, err)
    }

    imagePlatform, err := images.ConfigPlatform(ctx, store, manifest.Config)
    if err != nil {
        return false, fmt.Errorf("read config platform for manifest %s: %w", desc.Digest, err)
    }

    return matcher.Match(imagePlatform), nil
}

func (s *hostSchema) builtinContainer(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Query],
    args builtinContainerArgs,
) (inst dagql.ObjectResult[*core.Container], err error) {
    ...
    builtinManifest := specs.Descriptor{Digest: digest.Digest(args.Digest)}

    ctx, release, err := leaseutil.WithLease(ctx, query.LeaseManager(), leaseutil.MakeTemporary)
    if err != nil {
        return inst, err
    }
    defer release(context.WithoutCancel(ctx))

    if err := contentutil.CopyChain(ctx, query.OCIStore(), query.BuiltinOCIStore(), builtinManifest); err != nil {
        return inst, fmt.Errorf("copy builtin image content: %w", err)
    }

    ctr := core.NewContainer(query.Platform())
    ctr, err = ctr.FromOCIStore(ctx, builtinManifest, "")
    if err != nil {
        return inst, err
    }
    return dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)
}
```

### core/container_image.go
#### Status
- [x] Add the core-local OCI-store import path on top of `SnapshotManager().ImportImage(...)`.
- [x] Move `Import(...)`, `FromInternal(...)`, and `FromOCIStore(...)` onto that path.
- [x] Move builtin-image import and host store-backed image import onto `FromOCIStore(...)`.
- [x] Move registry-backed `Container.from(...)` / `FromCanonicalRef(...)` onto the session-owned resolver path.
- [x] Move `AsTarball(...)` into this file and onto the direct tarball writer path.

#### Core-owned image import
* Add a new `core/container_image.go` file for the image-specific parts of container construction.
* Move the image-import-specific lazy type and helper methods out of the generic container file.
* This file becomes the direct replacement for:
  * `ContainerFromLazy`
  * `Import(...)`
  * `FromOCIStore(...)`
  * `FromCanonicalRef(...)`
  * `FromInternal(...)`
  * the image-import part of `BuiltInContainer(...)`
  * `AsTarball(...)`
* It should talk directly to:
  * the session-owned registry resolver for registry pulls
  * the engine OCI store for all non-registry imports
  * other content stores only long enough to copy their closure into the engine OCI store
  * `engine/snapshots.ImportImage(...)` for rootfs materialization
* This file should not use `images.Store`.
  * Local named-image lookup belongs outside this file at the host/image-store boundary.
  * By the time control reaches `FromOCIStore(...)`, the selected manifest descriptor has already been chosen and its closure is being copied from some content store.
* It should not import or depend on `core/containersource`.
* The hard convergence point is:
  * ensure the selected manifest and its content closure are present in `query.OCIStore()`
  * call `FromOCIStore(...)`
  * let `FromOCIStore(...)` be the one core-local path that turns OCI content into a `Container`
* Empty imported images are not represented by nil rootfs state.
  * `FromOCIStore(...)` and registry-backed `Container.from(...)` both rely on `SnapshotManager().ImportImage(...)` returning a concrete immutable root snapshot even for zero-layer images.
  * Plain scratch `container()` remains the only declarative "no imported rootfs yet" state.
  * Once a `Directory`, `File`, or imported container rootfs claims to own snapshot-backed filesystem state, that state must be represented by a real snapshot handle rather than a nil/scratch sentinel.
* This file is also a direct-ownership handoff site:
  * rootfs snapshot handles returned from `SnapshotManager().ImportImage(...)` are transferred directly into the container rootfs directory
  * tarball output snapshots committed in `AsTarball(...)` are transferred directly into `NewFileWithSnapshot(...)`
* Do not separately release those handles after successful handoff.
* If `NewDirectoryWithSnapshot(...)` / `NewFileWithSnapshot(...)` itself returns an error at one of these handoff sites, the original snapshot handle still belongs to the caller and must be released directly.
* If object construction fails after the handoff, release the constructed object before returning.
* The same file should also own the container-image tarball path:
  * `AsTarball(...)` mounts a plain temporary local snapshot
  * opens a real file under that mount
  * calls the direct tarball writer helper
  * commits the result snapshot
* No tarball code here should route through a fake buildkit-session filesync target.
* `AsTarball(...)` should not use `CachePolicyRetain`.
* The working mutable snapshot is just temporary work:
  * it survives because the mutable handle is still open while work runs
  * the committed immutable survives because ownership is transferred into the returned `File`
  * no retain policy is needed anywhere in that flow

```go
type ContainerFromImageRefLazy struct {
    LazyState
    CanonicalRef string
    ResolveMode  serverresolver.ResolveMode
}

func (lazy *ContainerFromImageRefLazy) Evaluate(ctx context.Context, container *Container) error {
    return lazy.LazyState.Evaluate(ctx, "Container.from", func(ctx context.Context) error {
        query, err := CurrentQuery(ctx)
        if err != nil {
            return err
        }
        rslvr, err := query.RegistryResolver(ctx)
        if err != nil {
            return err
        }
        pulled, err := rslvr.Pull(ctx, lazy.CanonicalRef, serverresolver.PullOpts{
            Platform:    container.Platform.Spec(),
            ResolveMode: lazy.ResolveMode,
        })
        if err != nil {
            return fmt.Errorf("pull image %q: %w", lazy.CanonicalRef, err)
        }
        defer pulled.Release(context.WithoutCancel(ctx))
        rootfs, err := query.SnapshotManager().ImportImage(ctx, &bkcache.ImportedImage{
            Ref:          pulled.Ref,
            ManifestDesc: pulled.ManifestDesc,
            ConfigDesc:   pulled.ConfigDesc,
            Layers:       pulled.Layers,
            Nonlayers:    pulled.Nonlayers,
        }, bkcache.ImportImageOpts{
            ImageRef: pulled.Ref,
        })
        if err != nil {
            return fmt.Errorf("import image %q: %w", lazy.CanonicalRef, err)
        }
        if container.FS == nil || container.FS.self() == nil {
            rootfs.Release(context.WithoutCancel(ctx))
            return fmt.Errorf("missing rootfs directory for image import")
        }
        if err := container.FS.self().setSnapshot(rootfs); err != nil {
            rootfs.Release(context.WithoutCancel(ctx))
            return err
        }
        container.Lazy = nil
        return nil
    })
}

func (container *Container) Import(
    ctx context.Context,
    tarball io.Reader,
    tag string,
) (*Container, error)

func (container *Container) AsTarball(
    ctx context.Context,
    platformVariants []*Container,
    forcedCompression ImageLayerCompression,
    mediaTypes ImageMediaTypes,
    filePath string,
) (*File, error)

func (container *Container) FromOCIStore(
    ctx context.Context,
    manifestDesc specs.Descriptor,
    imageRef string,
) (*Container, error)

func (container *Container) FromInternal(
    ctx context.Context,
    desc specs.Descriptor,
) (*Container, error) {
    return container.FromOCIStore(ctx, desc, "")
}
```

* `Import(...)` should:
  * import the tarball into `query.OCIStore()`
  * resolve the selected manifest from that store
  * call `FromOCIStore(...)`
* `AsTarball(...)` should:
  * create a plain temporary mutable snapshot
  * mount it locally
  * open the destination tarball path directly with `os.Create(...)`
  * call `bk.WriteContainerImageTarball(...)`
  * commit the snapshot and return `NewFileWithSnapshot(...)`
* `FromOCIStore(...)` should:
  * load the selected manifest/config directly from `query.OCIStore()`
  * reconstruct the full local `bkcache.ImportedImage` payload from that store
    * manifest descriptor
    * config descriptor
    * layer descriptors
    * nonlayer descriptors if any survive in this path
    * per-layer `labels.LabelUncompressed` / diffID metadata
    * distribution-source annotations required by later push/export behavior
    * any other export-relevant layer annotations we have explicitly decided survive
  * call `query.SnapshotManager().ImportImage(...)`
  * initialize container config and rootfs from that one coherent path
* `contentutil.CopyChain(...)` should remain a dumb OCI closure copier.
  * It should copy blobs and descriptor-tree annotations.
  * It should not become a special copier for arbitrary source-store `content.Info` labels.
* `FromInternal(...)` becomes a thin wrapper around `FromOCIStore(...)`.
* This is also the right place for the small local helper that loads a full `bkcache.ImportedImage` from a content store.
  * `ImportImage(...)` should stay origin-agnostic and consume only the typed local payload it is handed.
  * `ImportImage(...)` should not rediscover layer/config/annotation details by reparsing a manifest descriptor itself.

```go
type LoadedImportedImage struct {
    Image  bkcache.ImportedImage
    Config dockerspec.DockerOCIImage
}

func loadImportedImageFromStore(
    ctx context.Context,
    store content.Store,
    manifestDesc ocispec.Descriptor,
) (*LoadedImportedImage, error)

func hydrateImportedDescriptor(
    ctx context.Context,
    store content.Store,
    desc ocispec.Descriptor,
) (ocispec.Descriptor, error)
```

* `loadImportedImageFromStore(...)` should:
  * read and unmarshal the selected manifest blob
  * read and unmarshal the image config blob
  * build the full `bkcache.ImportedImage`
  * return the decoded image config needed to initialize `Container.Config` / `Container.Platform`
* `hydrateImportedDescriptor(...)` should:
  * start from the descriptor referenced by the copied manifest
  * preserve:
    * media type
    * digest
    * size
    * URLs
    * descriptor annotations already present on the copied descriptor, including distribution-source annotations when present
    * the explicit saved layer/blob annotation allowlist:
      * `labels.LabelUncompressed`
      * `buildkit/createdat`
      * `compression.EStargzAnnotations`
* Per-layer `labels.LabelUncompressed` should be reconstructed from the copied image config's `rootfs.diff_ids`.
  * Align `config.RootFS.DiffIDs[i]` with `manifest.Layers[i]`.
  * Write that diffID back onto the reconstructed layer descriptor annotation.
* Local-only imports should not invent missing registry provenance.
  * Preserve copied distribution-source annotations when they are already present.
  * Do not try to synthesize new registry provenance from source-store-local metadata.
* `FromOCIStore(...)` should then be a single coherent path:
  * `loaded := loadImportedImageFromStore(...)`
  * `rootfs := query.SnapshotManager().ImportImage(ctx, &loaded.Image, ...)`
  * initialize the container rootfs/config/platform from `loaded`
* This should replace the current split where old source/import plumbing materializes the rootfs and core code separately rereads manifest/config blobs afterward.

```go
func (container *Container) AsTarball(
    ctx context.Context,
    platformVariants []*Container,
    forcedCompression ImageLayerCompression,
    mediaTypes ImageMediaTypes,
    filePath string,
) (f *File, rerr error) {
    ...
    err = MountRef(ctx, bkref, nil, func(out string, _ *mount.Mount) error {
        destPath := filepath.Join(out, filePath)
        dest, err := os.Create(destPath)
        if err != nil {
            return err
        }
        defer dest.Close()

        return bk.WriteContainerImageTarball(
            ctx,
            dest,
            inputByPlatform,
            useOCIMediaTypes(mediaTypes),
            string(forcedCompression),
        )
    })
    ...
}
```

### core/container.go
#### Status
- [x] Route `Publish(...)` to typed image-export responses instead of exporter response maps.
- [x] Route `Export(...)` to typed image-export responses instead of base64 descriptor parsing.
- [x] Make `AsTarball(...)` write the archive directly through `WriteContainerImageTarball(...)`.

#### Generic container shape
* After the cutover, keep this file focused on the generic `Container` object model and non-image-specific behavior.
* Move image-import-specific code out to `core/container_image.go`.
* Delete the old `ContainerFromLazy` type here and replace it with the image-specific lazy type in `core/container_image.go`.
* Move `Import(...)`, `FromInternal(...)`, and `FromOCIStore(...)` into `core/container_image.go`.
* Keep the generic rootfs helpers and container-child cloning behavior here.
* The important hard cut is that this file no longer imports or depends on `core/containersource`.
* `Container.OnRelease(...)` remains the place where the container closes:
  * `MetaSnapshot`
  * bare rootfs directory snapshot handles
  * bare mounted directory/file snapshot handles
* `Container.AttachDependencyResults(...)` remains the place where those object relationships become dagql deps.
* When cloning container children, stop cloning snapshot handles implicitly.
* If a child needs independent ownership of an existing snapshot, open a fresh handle by snapshot ID first and then assign it.
* That applies to `MetaSnapshot` too.
* Any clone path that currently does `cp.MetaSnapshot = cp.MetaSnapshot.Clone()` should instead reopen a new immutable handle through `query.SnapshotManager().GetBySnapshotID(...)`.
* Child-container helper paths that rebuild bare `Directory` / `File` sources from existing snapshots must follow the same rule:
  * reopening when a second owner is needed
  * direct handoff when ownership is moving

### core/schema/directory.go
#### Status
- [x] Delete the internal `Query.__immutableRef(...)` field and rewrite the `Changeset` caller directly.
- [x] Make `directory(...)` scratch construction commit and hand off directly without retain/finalize.

#### Delete `__immutableRef`
* Delete the internal `Query.__immutableRef(...)` schema field entirely.
* It is an old-world hack that:
  * takes a raw low-level snapshot-manager ref ID through the schema surface
  * reopens that ref
  * fabricates a `Directory` object result from it
* That is exactly the wrong direction for the new model.
* Dagql result identity should not be reconstructed by routing raw snapshot-manager IDs back through a hidden schema API.
* The only known caller is [core/changeset.go](/home/sipsma/repo/github.com/sipsma/dagger/core/changeset.go), and that caller should be rewritten directly rather than preserving this field.
* `directory(...)` scratch construction should:
  * stop calling `Finalize(...)`
  * stop using `bkcache.CachePolicyRetain`
  * commit the scratch ref
  * transfer the committed handle directly into `NewDirectoryWithSnapshot(...)`
  * if `NewDirectoryWithSnapshot(...)` itself fails, release the committed snapshot handle directly
  * release the constructed `Directory` object on later error before returning

```go
scratchRef, err := parent.Self().SnapshotManager().New(
    ctx,
    nil,
    bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
    bkcache.WithDescription("scratch"),
)
if err != nil {
    return inst, fmt.Errorf("failed to create scratch ref: %w", err)
}
finalRef, err := scratchRef.Commit(ctx)
if err != nil {
    return inst, fmt.Errorf("failed to commit scratch ref: %w", err)
}
```

### core/schema/http.go
#### Status
- [x] Make public `http(...)` session-scoped and add hidden persistable `_httpResolved(...)`.
- [x] Route session-free fetches through `ResolveHTTPVersion(...)` plus `_httpResolved(...)`.
- [x] Keep session-bound auth/service-host fetches on the public field only.
- [x] Release the constructed `File` if `WithContentDigest(...)` fails after object construction.

#### Session-scoped HTTP resolution + persistable resolved fetch
* Public `http(...)` remains, but it is the session-scoped resolution step rather than the final persisted fetch.
* Add `WithInput(dagql.PerSessionInput)` to the public field.
* Remove `IsPersistable()` from the public field.
* Add a hidden internal `_httpResolved(...)` field on `Query`.
* `_httpResolved(...)` is the persistable field, but only for truly session-free HTTP fetches.
* The public `http(...)` field should:
  * resolve any session-bound fetch context (auth header secret plaintext, service-host startup)
  * if no session-bound fetch context is present:
    * resolve the current upstream version once per session
    * reselect `_httpResolved(...)` with the original meaningful args plus hidden resolved-version args
  * if session-bound fetch context is present:
    * perform the fetch/materialization directly in the public field
    * do not route through `_httpResolved(...)`
* The hidden `_httpResolved(...)` field should:
  * be `IsPersistable()`
  * do the actual GET/materialization
  * return the final persisted `File`
* `_httpResolved(...)` must not take:
  * `AuthHeader`
  * `ExperimentalServiceHost`
  * `SecretID`
  * `ServiceID`
  * auth header plaintext
  * any other session-only capability
* We do not need a single opaque token. The resolved version can just be hidden internal args:
  * `resolvedETag`
  * `resolvedLastModified`
  * `resolvedDigest`
* Within a session, we do not care about staleness beyond the first resolve.
* Across sessions, the public field resolves once again, and the hidden persistable field is only reused if the resolved-version args still match.
* We are intentionally not optimizing the secret/service-host path for cross-session reuse.
  * If a fetch depends on `authHeader` or `experimentalServiceHost`, it stays session-scoped and that is fine.
* `http(...)` must stop doing `defer snap.Release(...)` after `NewFileWithSnapshot(...)` takes ownership.
* If `NewFileWithSnapshot(...)` itself returns an error, `snap` was not handed off and must still be released directly by the caller.
* If `dagql.NewObjectResultForCurrentCall(...)` or `WithContentDigest(...)` fails after the `File` is constructed, release the `File` object before returning.

```go
dagql.Fields[*core.Query]{
    dagql.NodeFunc("http", s.http).
        WithInput(dagql.PerSessionInput).
        Doc(`Returns a file containing an http remote url content.`),

    dagql.NodeFunc("_httpResolved", s.httpResolved).
        IsPersistable().
        Doc("Internal resolved HTTP fetch"),
}.Install(srv)
```

```go
type httpResolvedArgs struct {
    URL         string
    Name        *string
    Permissions *int
    Checksum    *string

    ResolvedETag         *string `internal:"true"`
    ResolvedLastModified *string `internal:"true"`
    ResolvedDigest       *string `internal:"true"`
}

func (s *httpSchema) http(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Query],
    args httpArgs,
) (inst dagql.ObjectResult[*core.File], err error) {
    srv, err := core.CurrentDagqlServer(ctx)
    if err != nil {
        return inst, err
    }

    filename, err := s.httpPath(ctx, parent.Self(), args)
    if err != nil {
        return inst, err
    }
    permissions := 0600
    if args.Permissions != nil {
        permissions = *args.Permissions
    }

    if args.AuthHeader.Valid || args.ExperimentalServiceHost.Valid {
        authHeader, detach, err := s.resolveHTTPSessionContext(ctx, parent.Self(), srv, args)
        if err != nil {
            return inst, err
        }
        if detach != nil {
            defer detach()
        }

        fetched, err := core.FetchHTTPFile(ctx, parent.Self(), core.FetchHTTPRequestOpts{
            URL:                 args.URL,
            Filename:            filename,
            Permissions:         permissions,
            Checksum:            args.Checksum,
            AuthorizationHeader: authHeader,
        })
        if err != nil {
            return inst, err
        }

        return s.newHTTPFileResult(ctx, srv, fetched, permissions, args.Checksum)
    }

    resolved, err := core.ResolveHTTPVersion(ctx, parent.Self(), core.ResolveHTTPRequestVersionOpts{
        URL:      args.URL,
        Checksum: args.Checksum,
    })
    if err != nil {
        return inst, err
    }

    if err := srv.Select(ctx, parent, &inst, dagql.Selector{
        Field: "_httpResolved",
        Args: []dagql.NamedInput{
            {Name: "url", Value: dagql.String(args.URL)},
            {Name: "name", Value: optionalStringInput(args.Name)},
            {Name: "permissions", Value: optionalIntInput(args.Permissions)},
            {Name: "checksum", Value: optionalStringInput(args.Checksum)},
            {Name: "resolvedETag", Value: optionalStringPtrInput(resolved.ETag)},
            {Name: "resolvedLastModified", Value: optionalStringPtrInput(resolved.LastModified)},
            {Name: "resolvedDigest", Value: optionalStringPtrInput(resolved.Digest)},
        },
    }); err != nil {
        return inst, err
    }
    return inst, nil
}
```

```go
func (s *httpSchema) resolveHTTPSessionContext(
    ctx context.Context,
    query *core.Query,
    srv *dagql.Server,
    args httpArgs,
) (authHeader string, detach func(), err error)

func (s *httpSchema) newHTTPFileResult(
    ctx context.Context,
    srv *dagql.Server,
    fetched *core.HTTPFetchResult,
    permissions int,
    checksum *string,
) (dagql.ObjectResult[*core.File], error)

func (s *httpSchema) httpResolved(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Query],
    args httpResolvedArgs,
) (inst dagql.ObjectResult[*core.File], err error) {
    srv, err := core.CurrentDagqlServer(ctx)
    if err != nil {
        return inst, err
    }

    fetched, err := core.FetchHTTPFile(ctx, parent.Self(), core.FetchHTTPRequestOpts{
        URL:                  args.URL,
        Filename:             valueOrDefault(args.Name, "index"),
        Permissions:          valueOrDefault(args.Permissions, 0600),
        Checksum:             args.Checksum,
        ResolvedETag:         args.ResolvedETag,
        ResolvedLastModified: args.ResolvedLastModified,
        ResolvedDigest:       args.ResolvedDigest,
    })
    if err != nil {
        return inst, err
    }

    return s.newHTTPFileResult(ctx, srv, fetched, valueOrDefault(args.Permissions, 0600), args.Checksum)
}
```

### core/http.go
#### Status
- [x] Delete the old `DoHTTPRequest(...)` snapshot-metadata/retain path.
- [x] Add `ResolveHTTPVersion(...)`.
- [x] Add `FetchHTTPFile(...)` with direct commit-to-`File` handoff.

#### Resolve once per session, fetch in persistable resolved step
* Delete the old HTTP-specific snapshot metadata cache behavior entirely.
* `core/http.go` should not search snapshot metadata indexes.
* `core/http.go` should not build `If-None-Match` from old snapshot metadata.
* `core/http.go` should not reopen prior HTTP snapshots via `cache.Get(...)`.
* `core/http.go` should not use `CachePolicyRetain`.
* Replace `DoHTTPRequest(...)` with two helpers:
  * `ResolveHTTPVersion(...)`
  * `FetchHTTPFile(...)`
* `ResolveHTTPVersion(...)` is only for the session-free branch and determines stable resolved-version args:
  * prefer `ETag`
  * else `Last-Modified`
  * else fall back to a `GET` and compute a content digest
* `FetchHTTPFile(...)` is used by both branches:
  * the public session-bound branch
  * the hidden persistable `_httpResolved(...)` branch
* The point is to keep HTTP-specific freshness logic in `core/http.go`, not in dagql cache and not in snapshot metadata.
* The mutable snapshot used while downloading/writing the file should follow the new commit contract:
  * `bkref.Commit(ctx)` consumes `bkref`
  * after commit, hand the returned immutable snapshot onward
  * do not separately release the mutable after a successful commit

```go
type ResolveHTTPRequestVersionOpts struct {
    URL      string
    Checksum *string
}

type ResolvedHTTPVersion struct {
    ETag         *string
    LastModified *string
    Digest       *string
}

func ResolveHTTPVersion(
    ctx context.Context,
    query *Query,
    opts ResolveHTTPRequestVersionOpts,
) (*ResolvedHTTPVersion, error)

type FetchHTTPRequestOpts struct {
    URL                 string
    Filename            string
    Permissions         int
    Checksum            *string
    AuthorizationHeader string

    ResolvedETag         *string
    ResolvedLastModified *string
    ResolvedDigest       *string
}

type HTTPFetchResult struct {
    File          *File
    ContentDigest digest.Digest
    LastModified  string
}

func FetchHTTPFile(
    ctx context.Context,
    query *Query,
    opts FetchHTTPRequestOpts,
) (_ *HTTPFetchResult, rerr error)
```

* `FetchHTTPFile(...)` should:
  * perform a normal GET
  * optionally set `Authorization` when the public session-bound branch resolved one
  * enforce checksum if provided
  * if resolved-version args are present, validate the fetched response against them
  * write to a new mutable snapshot
  * commit it
  * transfer ownership directly into `NewFileWithSnapshot(...)`
* No session-bound secret/service-host resolution lives here anymore.
  * that stays in the public session-scoped schema field
* Cross-session persistable reuse is intentionally only supported for the session-free branch.
* No revalidation/state reuse lives here anymore beyond the explicit session-free version resolver step.

### core/cacheref.go
#### Status
- [x] Delete the HTTP-specific snapshot metadata index/helpers.

#### Delete HTTP-specific snapshot metadata index
* Delete the HTTP-specific metadata helpers entirely:
  * `keyHTTP`
  * `keyHTTPChecksum`
  * `keyHTTPETag`
  * `keyHTTPModTime`
  * `indexHTTP`
  * `searchHTTPByDigest(...)`
  * `getHTTPChecksum()`
  * `setHTTPChecksum(...)`
  * `getETag()`
  * `setETag(...)`
  * `getHTTPModTime()`
  * `setHTTPModTime(...)`
* HTTP freshness/versioning is no longer represented through snapshot metadata.
* Git-related metadata helpers can stay as long as they still fit the git plan.

### core/service.go
#### Status
- [x] Delete `Service.Releasers` and keep runtime-owned refs off the pure `Service` value.
- [x] Change `Service.Start(...)` and `runAndSnapshotChanges(...)` to operate on `*RunningService`.
- [x] Move late-bound workspace refs onto `RunningService` ownership.
- [x] Switch service startup to the sessionless `prepareMounts(...)` path and stop building fake client session groups for service mounts.

#### Pure service values, runtime-owned refs
* `Service` should be a pure declarative value.
* Delete `Service.Releasers`.
* Do not store runtime-owned snapshot refs back on the `Service` object.
* `startContainer(...)` should still use the sessionless `prepareMounts(...)` path, but any startup-created or late-bound runtime refs must be owned by the concrete `RunningService`, not by `Service`.
* `runAndSnapshotChanges(...)` should stop mutating `svc.Releasers`.
* It should take the concrete running service and track late-bound refs there.
* `query.BuildkitSession()` is deleted from service startup.
* There is no fake buildkit-session manager involved in service runtime after the cut.
* The concrete inversion of control should be:
  * `Services` precreates the `RunningService` shell
  * `Service.Start(...)` fills in that existing `RunningService`
  * startup-created refs are tracked directly on `running.TrackRef(...)`
  * `Service` never temporarily owns them
* The ownership transfer line for runtime refs should be explicit:
  * local startup code owns a ref until it calls `running.TrackRef(ref)`
  * after `running.TrackRef(ref)`, local cleanup must no longer release that ref
  * if startup later fails after some refs were transferred, manager-owned cleanup on `RunningService` releases them

```go
cache := query.SnapshotManager()

p, err := prepareMounts(
    ctx,
    ctr,
    nil,
    nil,
    nil,
    cache,
    "",
    runtime.GOOS,
    func(_ string, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
        return cache.New(ctx, ref, nil)
    },
)
```

```go
type Service struct {
    Creator trace.SpanContext

    CustomHostname string

    Container                     dagql.ObjectResult[*Container]
    Args                          []string
    ExperimentalPrivilegedNesting bool
    InsecureRootCapabilities      bool
    NoInit                        bool
    ExecMD                        *buildkit.ExecutionMetadata
    ExecMeta                      *executor.Meta

    TunnelUpstream dagql.ObjectResult[*Service]
    TunnelPorts    []PortForward
    HostSockets    []*Socket
}

func (svc *Service) Start(
    ctx context.Context,
    running *RunningService,
    digest digest.Digest,
    io *ServiceIO,
) error

func (svc *Service) runAndSnapshotChanges(
    ctx context.Context,
    running *RunningService,
    target string,
    source *Directory,
    f func() error,
) (res dagql.ObjectResult[*Directory], hasChanges bool, rerr error)
```

* The runtime ref that keeps the mutable remount alive belongs to `running`, not to `svc`.
* That is the critical ownership correction in this file.
* `runAndSnapshotChanges(...)` should follow the same transfer rule:
  * the mutable remount ref is locally owned until it is registered on `running`
  * after that point, rollback/cleanup belongs to `RunningService`, not to the pure `Service` value
* `runAndSnapshotChanges(...)` should serialize workspace mutation per `RunningService`.
  * Add a service-scoped mutex on `RunningService`.
  * Lock it across the whole mutate/snapshot/remount sequence.
  * Overlapping workspace mutations on the same running service are therefore serialized, not forbidden and not last-writer-wins.
* The immutable snapshot returned from `mutableRef.Commit(...)` in `runAndSnapshotChanges(...)` should be transferred directly into `NewDirectoryWithSnapshot(...)`.
* If `NewDirectoryWithSnapshot(...)` itself fails there, release the committed immutable snapshot handle directly.
* Do not defer `immutableRef.Release(...)` after that handoff.
* If result construction fails after the `Directory` is created, release the constructed `Directory` object before returning.

### core/container_exec.go
#### Status
- [x] Remove tmpfs/secret/ssh exec-mount idmap fields and `IdentityMapping()` methods.
- [x] Delete host UID/GID remap branches and use requested UID/GID directly for secret and ssh exec mounts.
- [x] Delete `prepareMounts(...)` session/group parameters and remove the `execMountWithSession(...)` / `sessionMountable` wrapper seam.
- [x] Make exec tmpfs/secret/ssh mountables implement the sessionless snapshot mount interface directly.
- [x] Move exec error reporting onto the same sessionless `ref.Mount(...)` path.

#### Delete dead idmap plumbing from exec mounts
* `IdentityMapping()` is dead in our engine configuration and should be removed entirely from this file.
* Delete idmap fields from:
  * `execTmpFS`
  * `execTmpFSMount`
  * `execSecretMount`
  * `execSecretMountInstance`
  * `execSSHMount`
  * `execSSHMountInstance`
* `execTmpFSMountable(...)` should stop reading `cache.IdentityMapping()`.
* `prepareExecSecretMount(...)` should stop reading `cache.IdentityMapping()`.
* `prepareExecSSHMount(...)` should stop reading `cache.IdentityMapping()`.
* Delete the `IdentityMapping()` methods on the tmpfs/secret/ssh mount instances entirely.
* The host-UID/GID remap branches using `idmap.ToHost(...)` are dead in practice for Dagger today and should be removed rather than preserved.
* After the cut:
  * tmpfs mount setup is unchanged except there is no idmap plumbing
  * secret mount uses the requested UID/GID directly
  * ssh mount uses the requested UID/GID directly
* If some old internal BuildKit path still expects idmap semantics elsewhere, that is compatibility garbage, not a reason to keep it here.

```go
func execTmpFSMountable(cache bkcache.SnapshotManager, opt *pb.TmpfsOpt) bkcache.Mountable {
    return &execTmpFS{opt: opt}
}

type execTmpFS struct {
    opt *pb.TmpfsOpt
}

type execSecretMount struct {
    uid  int
    gid  int
    mode fs.FileMode
    data []byte
}

type execSSHMount struct {
    socket dagql.ObjectResult[*Socket]
    uid    int
    gid    int
    mode   fs.FileMode
}
```

### core/container_emulator.go
#### Status
- [x] Remove emulator idmap fields and `IdentityMapping()` plumbing.
- [x] Delete the `RootPair()` remap branch and copy with direct host defaults only.

#### Delete dead emulator idmap plumbing
* `IdentityMapping()` is dead here too.
* `getEmulator(...)` already returns an `emulator` with no idmap set in practice.
* Remove the `idmap` field from `emulator` and `staticEmulatorMount`.
* Delete `staticEmulatorMount.IdentityMapping()`.
* Delete the `RootPair()` remap branch in `staticEmulatorMount.Mount()`.
* The emulator copy should just use the host defaults directly.

```go
type emulator struct {
    path string
}

type staticEmulatorMount struct {
    path string
}
```

### engine/buildkit/executor_spec.go
#### Status
- [x] Delete dead host-bind `IdentityMapping()` boilerplate from executor mount refs.

#### Delete dead host-bind idmap method
* `hostBindMountRef.IdentityMapping()` is pure dead interface boilerplate.
* Delete it.
* More broadly, as `IdentityMapping()` disappears from the executor mount interfaces, remove the requirement entirely rather than stubbing out `nil`.

### core/directory.go
#### Status
- [x] Remove archive-unpack idmap parameters and `archive.TarOptions.IDMap` plumbing.
- [x] Stop passing `newRef.IdentityMapping()` into archive-unpack helpers.
- [x] Strengthen `Directory.Diff(...)` to require rebased root-level inputs only.

#### Delete dead archive-unpack idmap plumbing
* `newRef.IdentityMapping()` is dead in practice and should be removed from the archive-unpack path.
* `attemptCopyArchiveUnpack(...)` should stop accepting an idmap parameter.
* `unpackArchiveFile(...)` should stop accepting an idmap parameter.
* Delete the `archive.TarOptions.IDMap` plumbing entirely.
* Archive unpack should use the direct non-idmapped behavior only.

```go
func attemptCopyArchiveUnpack(
    ctx context.Context,
    srcRoot string,
    destPath string,
    includePatterns []string,
    excludePatterns []string,
    useGitignore bool,
    ownership *Ownership,
    permissions *int,
    destPathHintIsDirectory bool,
) (bool, error)

func unpackArchiveFile(
    srcPath string,
    destPath string,
    ownership *Ownership,
) (bool, error)
```

### core/services.go
#### Status
- [x] Replace `starting map[ServiceKey]*sync.WaitGroup` with explicit `startingService` state.
- [x] Make `Services` own `starting` / `running` / `exited` transitions and spontaneous-exit cleanup.
- [x] Ensure session teardown cancels in-flight starts and stops running services without late publish.

#### Session-owned runtime manager
* `Services` is the authoritative owner of live service runtime state for a Dagger session.
* This is the explicit third ownership bucket:
  * not an ordinary dagql-owned value
  * not a persistable mutable owner object
  * not snapshot-manager-owned
* `RunningService` should own any runtime-only snapshot refs needed while the service is alive.
* `StopSessionServices(...)` remains the authoritative cleanup path for those runtime resources on session teardown.
* `Detach(...)`, `stop(...)`, and `stopGraceful(...)` should release tracked runtime refs when the running service exits.
* `Services` should also own the startup/exit state machine explicitly:
  * `starting`: startup has begun but the service is not yet published in `running`
  * `running`: startup succeeded and the service is published and reusable under its key
  * `exited`: the service has stopped and must no longer be reusable under that key
* Session teardown must handle both `starting` and `running` entries for the session.
  * Cancel in-flight starts first.
  * Wait for those starts to finish so they cannot late-publish afterward.
  * Then stop any already-running services.
* Spontaneous service exit must also flow back through `Services`.
  * When a running service exits on its own, `Services` must remove it from `ss.running`, clear bindings, and release tracked runtime refs.
  * Later `Get(...)` / `Start(...)` calls must not reuse a dead entry.
* `Services` must distinguish between:
  * deduped shared/background service runtime
  * unique interactive terminal runtime
* Interactive terminals must stay under `Services`, but they must not reuse ordinary service dedupe semantics.
  * Each interactive terminal gets its own runtime instance and IO wiring.
  * Ordinary reusable services keep the existing deduped key semantics.

```go
type ServiceRuntimeKind string

const (
    ServiceRuntimeShared      ServiceRuntimeKind = "shared"
    ServiceRuntimeInteractive ServiceRuntimeKind = "interactive"
)

type ServiceKey struct {
    Digest     digest.Digest
    SessionID  string
    ClientID   string
    Kind       ServiceRuntimeKind
    InstanceID string
}

type RunningService struct {
    Key ServiceKey

    Host  string
    Ports []Port

    Stop func(ctx context.Context, force bool) error
    Wait func(ctx context.Context) error
    Exec func(ctx context.Context, cmd []string, env []string, io *ServiceIO) error

    ContainerID string

    refsMu sync.Mutex
    refs   []bkcache.Ref

    workspaceMu sync.Mutex

    manager *Services

    stopOnce sync.Once
}

type startingService struct {
    running *RunningService

    ctx    context.Context
    cancel context.CancelCauseFunc

    done chan struct{}
    err  error
}

func (svc *RunningService) TrackRef(ref bkcache.Ref) {
    if ref == nil {
        return
    }
    svc.refsMu.Lock()
    defer svc.refsMu.Unlock()
    svc.refs = append(svc.refs, ref)
}

func (svc *RunningService) ReleaseTrackedRefs(ctx context.Context) error {
    svc.refsMu.Lock()
    refs := svc.refs
    svc.refs = nil
    svc.refsMu.Unlock()

    var errs error
    for _, ref := range refs {
        errs = errors.Join(errs, ref.Release(context.WithoutCancel(ctx)))
    }
    return errs
}

type Startable interface {
    Start(
        ctx context.Context,
        running *RunningService,
        digest digest.Digest,
        io *ServiceIO,
    ) error
}

func (ss *Services) startWithKey(
    ctx context.Context,
    key ServiceKey,
    svc Startable,
    sio *ServiceIO,
) (_ *RunningService, release func(), err error)

func (ss *Services) StartInteractive(
    ctx context.Context,
    dig digest.Digest,
    svc Startable,
    sio *ServiceIO,
) (_ *RunningService, release func(), err error)

func (ss *Services) StopRunning(ctx context.Context, running *RunningService, force bool) error

func (ss *Services) handleExit(running *RunningService, err error)
```

* `startWithKey(...)` should precreate the `RunningService` object, pass it into `svc.Start(...)`, and only then publish it as running.
* `startContainer(...)` should therefore receive the precreated `RunningService` and register startup-created refs directly on it.
* Replace the current `starting map[ServiceKey]*sync.WaitGroup` model with a real `startingService` record.
  * It should carry:
    * the precreated `RunningService`
    * the startup context cancel func
    * a `done` channel
    * the startup error
* `startWithKey(...)` should own the `starting -> running` transition explicitly.
  * If startup fails:
    * record the startup error
    * release any refs already transferred onto `running`
    * remove the entry from `starting`
    * do not publish into `running`
  * If startup was canceled during session teardown:
    * do not publish into `running`
    * release any refs already transferred onto `running`
    * return the cancellation cause
  * Only a successful uncanceled start may transition into `running`.
* `Detach(...)`, `stop(...)`, and `stopGraceful(...)` should still release `running.ReleaseTrackedRefs(...)` on shutdown.
* `StopSessionServices(...)` should gather both:
  * `starting` entries for the session
  * `running` entries for the session
* It should then:
  * cancel all matching `starting` entries
  * wait for them to finish
  * stop all matching `running` services
* When startup succeeds and the service is published, `Services` should launch a wait goroutine for that `RunningService`.
  * When `running.Wait(...)` returns, `Services.handleExit(...)` should remove that exact entry from `ss.running`, clear its binding count, and release tracked runtime refs.
* The manager-owned shutdown path should be idempotent.
  * `RunningService` should guard stop/ref-release with a `sync.Once` so explicit stop, session teardown, startup failure cleanup, and spontaneous-exit cleanup cannot double-release runtime refs.
* MCP and any other code that needs to mutate a running service workspace should operate on `*RunningService`, not by smuggling runtime ownership through the pure `Service` value.
* `StartWithIO(...)` should keep the deduped shared-service path:
  * build a `ServiceKey{Kind: ServiceRuntimeShared, ...}`
  * include `ClientID` only when `clientSpecific` is true
  * return the existing `*RunningService` when that exact shared key is already running
* `StartInteractive(...)` should always create a unique interactive key:
  * `Kind: ServiceRuntimeInteractive`
  * same `Digest` / `SessionID` / `ClientID` context as the caller
  * plus a fresh `InstanceID`
* `StartInteractive(...)` should return a `release` closure that detaches through `Services`, not by calling `running.Stop(...)` directly.
* `StopRunning(...)` is the narrow manager-owned helper for the terminal error path when the interactive runtime needs to be force-stopped immediately.

```go
func (ss *Services) startWithKey(
    ctx context.Context,
    key ServiceKey,
    svc Startable,
    sio *ServiceIO,
) (_ *RunningService, release func(), err error) {
    running := &RunningService{
        Key:     key,
        manager: ss,
    }

    svcCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
    start := &startingService{
        running: running,
        ctx:     svcCtx,
        cancel:  cancel,
        done:    make(chan struct{}),
    }

    ss.l.Lock()
    ss.starting[key] = start
    ss.l.Unlock()

    defer close(start.done)

    if err := svc.Start(svcCtx, running, key.Digest, sio); err != nil {
        start.err = err
        _ = running.ReleaseTrackedRefs(context.WithoutCancel(ctx))
        ss.l.Lock()
        delete(ss.starting, key)
        ss.l.Unlock()
        cancel(err)
        return nil, nil, err
    }

    ss.l.Lock()
    delete(ss.starting, key)
    if context.Cause(svcCtx) != nil {
        ss.l.Unlock()
        _ = running.ReleaseTrackedRefs(context.WithoutCancel(ctx))
        return nil, nil, context.Cause(svcCtx)
    }
    ss.running[key] = running
    ss.bindings[key] = 1
    ss.l.Unlock()

    go func() {
        err := running.Wait(context.Background())
        ss.handleExit(running, err)
    }()

    return running, func() { ss.Detach(ctx, running) }, nil
}
```

### core/terminal.go
#### Status
- [x] Rewrite terminal clone helpers to reopen snapshot handles by snapshot ID instead of `Clone()`.
- [x] Route interactive terminal startup through `Services.StartInteractive(...)` instead of a direct `svc.Start(...)` side path.

#### Terminal runtime uses services, not session resources
* Do not model terminal runtime as a dagql `SessionResourceHandle`.
* Interactive terminal runtime should piggyback on the same session-owned `Services` / `RunningService` bucket.
* The terminal-attached container value itself remains an ordinary dagql-owned value.
* Rewrite `cloneContainerForTerminal(...)` and its helpers to stop calling `Snapshot.Clone()`.
* When a terminal clone needs independent ownership of an existing snapshot, explicitly reopen it by snapshot ID through `query.SnapshotManager().GetBySnapshotID(...)`.
* Terminal container cloning should therefore stay in the ordinary dagql ownership model while the running interactive process stays in the service-runtime bucket.

```go
func cloneContainerForTerminal(
    ctx context.Context,
    query *Query,
    ctr *Container,
) (*Container, error)

func cloneTerminalDirectory(
    ctx context.Context,
    query *Query,
    dir *Directory,
) (*Directory, error)

func cloneTerminalFile(
    ctx context.Context,
    query *Query,
    file *File,
) (*File, error)
```

```go
if snapshot != nil {
    reopened, err := query.SnapshotManager().GetBySnapshotID(
        ctx,
        snapshot.SnapshotID(),
        bkcache.NoUpdateLastUsed,
    )
    if err != nil {
        return nil, err
    }
    cp.Snapshot = reopened
    cp.snapshotReady = true
    return cp, nil
}
```

* This file should not introduce a second special lifecycle path.
* It should:
  * attach/evaluate the ordinary container result through dagql
  * reopen snapshot handles explicitly when it needs a second owner
  * start the live interactive process through `query.Services().StartInteractive(...)`
  * let `RunningService` own runtime cleanup
* Container-backed interactive terminals and service-backed interactive terminals should both flow through the same `Services` / `RunningService` manager.
  * There should not be a direct `svc.Start(...)` side path for interactive runtime.
  * `Service.Start(...)` remains the implementation behind the `Services` manager, not a separate ownership path.
* Concretely, terminal startup should look like:

```go
svcs, err := query.Services(ctx)
if err != nil {
    return err
}

runningSvc, release, err := svcs.StartInteractive(
    ctx,
    selectedDigest,
    svc,
    &ServiceIO{
        Stdin:       term.Stdin,
        Stdout:      term.Stdout,
        Stderr:      term.Stderr,
        ResizeCh:    term.ResizeCh,
        Interactive: true,
    },
)
if err != nil {
    return fmt.Errorf("failed to start service: %w", err)
}
defer release()
```
* Terminal force-stop/error paths should also go back through `Services`, not directly through `runningSvc.Stop(...)`.
  * Add and use `svcs.StopRunning(ctx, runningSvc, true)` for the “terminal session failed; kill immediately” case so manager bookkeeping stays authoritative.
* Every current `Snapshot.Clone()` use in terminal cloning should become one of two things:
  * direct ownership handoff if the original owner is being replaced, or
  * `query.SnapshotManager().GetBySnapshotID(...)` if the terminal clone needs its own independent owner.

### core/container_exec.go
#### Status
- [x] Split exec mount output bookkeeping into explicit mutable vs immutable ownership.
- [x] Rewrite failure salvage and terminal rebuild to follow explicit ownership transfer and rollback.
- [x] Remove remaining `Clone()`-based exec failure/output handling in favor of reopen or constructor handoff.

#### Sessionless exec mount plan
* Delete the `session *bksession.Manager` parameter from `prepareMounts(...)`.
* Delete the `g bksession.Group` parameter from `prepareMounts(...)`.
* Delete `execMountWithSession(...)`.
* Delete `sessionMountable`.
* The executor-side mount plan should wrap ordinary sessionless snapshot mountables directly.
* `tmpfs`, `secret`, and `ssh` mountables should drop the `bksession.Group` argument from their `Mount(...)` methods.
* This file should not carry any fake buildkit-session coupling after the cut.
* [core/exec_error.go](/home/sipsma/repo/github.com/sipsma/dagger/core/exec_error.go) should migrate in the same cut.
  * `readSnapshotPathFromRef(...)` should stop doing `ref.Mount(ctx, true, bksession.NewGroup(...))`.
  * `execErrorFromMetaRef(...)`, `moduleErrorIDFromRef(...)`, and `getExecMeta(...)` should all use the same sessionless snapshot mount seam as the main exec path.
  * There should be no surviving `session.Group` usage anywhere in exec error reporting after this cut.
* This file must also be updated to the new mutable-commit contract.
  * `MutableRef.Commit(ctx)` consumes the mutable.
  * So this file must stop storing a mutable in generic cleanup state after it has been committed.
  * The current single `OutputRef bkcache.Ref` slot is too loose for that contract and should be split.
* This file is also one of the densest remaining `Clone()` callsite clusters and needs an explicit ownership transfer sweep:
  * readonly output bind mounts that currently do `state.OutputRef = iref.Clone()` should reopen a new immutable handle by snapshot ID
  * failure reconstruction paths that currently return `out.Clone()` / `sourceRef.Clone()` should reopen by snapshot ID instead
  * `metaFileContents(...)` must not transfer ownership of `container.MetaSnapshot`; it should reopen a fresh immutable handle before constructing the temporary `File`
  * the temporary `File` in `metaFileContents(...)` is not returned through dagql and must call `OnRelease(...)` after use
  * any ref attached to rebuilt terminal output objects via `setSnapshot(...)` is a direct ownership handoff and must not be separately released afterward

```go
func prepareMounts(
    ctx context.Context,
    container *Container,
    rootOutput func(bkcache.ImmutableRef) error,
    metaOutput func(bkcache.ImmutableRef) error,
    mountOutputs []func(bkcache.ImmutableRef) error,
    cache bkcache.SnapshotManager,
    cwd string,
    platform string,
    makeMutable makeExecMutable,
) (materialized materializedExecPlan, err error)

func execMount(mountable bkcache.Mountable) executor.Mount {
    _, readonly := mountable.(bkcache.ImmutableRef)
    return executor.Mount{
        Src:      mountable,
        Readonly: readonly,
    }
}
```

* The only reason `session.Group` is still threaded here today is the old snapshot mount interface.
* Once snapshot mounts are sessionless, this entire seam should disappear rather than being reintroduced under another name.
* For mount output bookkeeping, split the state explicitly:

```go
type execMountState struct {
    ...

    ApplyOutput func(bkcache.ImmutableRef) error

    ActiveRef bkcache.MutableRef

    // Exactly one of these may be non-nil at a time.
    OutputMutable   bkcache.MutableRef
    OutputImmutable bkcache.ImmutableRef
}
```

* `materializeState(...)` should therefore set:
  * `OutputImmutable` for readonly output cases that reopen/clone an immutable source
  * `OutputMutable` for writable output cases that create a mutable work ref
* `releaseOutputRefs(...)` should release both slots explicitly.
  * uncommitted `OutputMutable` is released as a mutable
  * `OutputImmutable` is released as an immutable
* `applyOutputs()` should become the ownership transition point:
  * if `OutputMutable != nil`, commit it
  * immediately clear `OutputMutable`
  * store the returned immutable in `OutputImmutable`
  * call `ApplyOutput(OutputImmutable)`
  * if `ApplyOutput(...)` succeeds, clear `OutputImmutable` because ownership transferred
  * if `ApplyOutput(...)` fails, leave `OutputImmutable` populated so deferred cleanup releases it exactly once

```go
applyOutputs := func() error {
    for _, state := range mountStates {
        if state.ApplyOutput == nil {
            continue
        }

        if state.OutputMutable != nil {
            committed, err := state.OutputMutable.Commit(ctx)
            if err != nil {
                return fmt.Errorf("error committing %s: %w", state.OutputMutable.ID(), err)
            }
            state.OutputMutable = nil
            state.OutputImmutable = committed
        }

        if state.OutputImmutable == nil {
            continue
        }

        if err := state.ApplyOutput(state.OutputImmutable); err != nil {
            return err
        }

        state.OutputImmutable = nil
    }
    return nil
}
```

* Failure salvage must also take ownership out of the state before returning refs, because the generic deferred cleanup still runs afterward.
* `resolveFailureRef(...)` should therefore:
  * return `OutputImmutable` directly and clear that slot
  * commit `OutputMutable`, clear that slot, and return the immutable
  * commit `ActiveRef`, clear that slot, and return the immutable
  * only fall back to reopening/cloning source refs when there is no already-owned output/active ref to salvage

```go
resolveFailureRef := func(state *execMountState) (bkcache.ImmutableRef, error) {
    switch {
    case state.OutputImmutable != nil:
        ref := state.OutputImmutable
        state.OutputImmutable = nil
        return ref, nil

    case state.OutputMutable != nil:
        iref, err := state.OutputMutable.Commit(ctx)
        if err != nil {
            return nil, fmt.Errorf("commit output ref %s: %w", state.OutputMutable.ID(), err)
        }
        state.OutputMutable = nil
        return iref, nil

    case state.ActiveRef != nil:
        iref, err := state.ActiveRef.Commit(ctx)
        if err != nil {
            return nil, fmt.Errorf("commit active ref %s: %w", state.ActiveRef.ID(), err)
        }
        state.ActiveRef = nil
        return iref, nil

    default:
        sourceRef, ok := state.SourceRef.(bkcache.ImmutableRef)
        if ok && sourceRef != nil {
            return query.SnapshotManager().GetBySnapshotID(ctx, sourceRef.SnapshotID(), bkcache.NoUpdateLastUsed)
        }
        return nil, nil
    }
}
```

* The important invariant after this rewrite is:
  * no consumed mutable handle remains stored in `execMountState`
  * generic deferred cleanup never sees a mutable that has already been committed
  * every committed output is either:
    * transferred to its destination object
    * salvaged into failure-handling state
    * or released exactly once by deferred cleanup
* The terminal-exec-error rebuild path should also follow an explicit ownership-transfer rule.
  * Salvaged failure refs start as locally owned by the deferred failure handler.
  * When a salvaged ref is installed onto the rebuilt terminal container, ownership transfers at that exact moment.
  * Once transferred, that ref must be removed from the local failure-cleanup set.
  * If terminal rebuild later fails after any transfer, the temporary rebuilt terminal container must be explicitly released through `Container.OnRelease(...)`.
* `metaRef` must not be shared between two owners.
  * The local `execErrorFromMetaRef(...)` path still needs one immutable handle.
  * If the rebuilt terminal container also needs a meta snapshot, reopen a second immutable handle by `SnapshotID()` for `terminalContainer.MetaSnapshot`.
  * Do not install the same `metaRef` handle onto the terminal container and then continue using it locally afterward.
* Concretely, the terminal failure-rebuild branch should:
  * keep a local `resolvedRefs` cleanup set for salvaged immutable refs
  * track a `terminalContainerNeedsRelease` rollback flag
  * when transferring a salvaged ref into rebuilt terminal state:
    * perform the normal constructor handoff (`NewDirectoryWithSnapshot(...)` / `NewFileWithSnapshot(...)`) where possible
    * immediately remove that ref from local cleanup ownership
    * set `terminalContainerNeedsRelease = true`
  * if `newSyntheticTerminalContainerResult(...)` or `TerminalExecError(...)` then fails:
    * call `terminalContainer.OnRelease(context.WithoutCancel(ctx))`
    * leave any still-local salvaged refs in the ordinary deferred cleanup set
  * only clear `terminalContainerNeedsRelease` after `TerminalExecError(...)` succeeds
* This should use the same direct-handoff rules as the rest of the cutover:
  * rootfs rebuild should hand the salvaged root snapshot into `NewDirectoryWithSnapshot(...)`
  * writable directory mount rebuild should hand the salvaged mount snapshot into `NewDirectoryWithSnapshot(...)`
  * writable file mount rebuild should hand the salvaged mount snapshot into `NewFileWithSnapshot(...)`
  * after each successful constructor handoff, the salvaged ref is no longer locally owned

```go
resolvedRefs := []bkcache.ImmutableRef{}
trackResolvedRef := func(ref bkcache.ImmutableRef) bkcache.ImmutableRef {
    if ref != nil {
        resolvedRefs = append(resolvedRefs, ref)
    }
    return ref
}
untrackResolvedRef := func(target bkcache.ImmutableRef) {
    for i, ref := range resolvedRefs {
        if ref == target {
            resolvedRefs = append(resolvedRefs[:i], resolvedRefs[i+1:]...)
            return
        }
    }
}
defer func() {
    for _, ref := range resolvedRefs {
        if ref != nil {
            _ = ref.Release(context.WithoutCancel(ctx))
        }
    }
}()

var terminalContainer *Container
terminalContainerNeedsRelease := false
defer func() {
    if terminalContainerNeedsRelease && terminalContainer != nil {
        _ = terminalContainer.OnRelease(context.WithoutCancel(ctx))
    }
}()

// Reopen a second meta handle for terminal ownership; keep the original
// metaRef for execErrorFromMetaRef(...).
if metaRef != nil {
    terminalMetaRef, err := query.SnapshotManager().GetBySnapshotID(
        ctx,
        metaRef.SnapshotID(),
        bkcache.NoUpdateLastUsed,
    )
    if err != nil {
        return err
    }
    trackResolvedRef(terminalMetaRef)
    terminalContainer.MetaSnapshot = terminalMetaRef
    untrackResolvedRef(terminalMetaRef)
    terminalContainerNeedsRelease = true
}

rootDir, err := NewDirectoryWithSnapshot(..., rootRef)
if err != nil {
    return err
}
untrackResolvedRef(rootRef)
terminalContainer.setBareRootFS(rootDir)
terminalContainerNeedsRelease = true

outputDir, err := NewDirectoryWithSnapshot(..., mountRef)
if err != nil {
    return err
}
untrackResolvedRef(mountRef)
...

outputFile, err := NewFileWithSnapshot(..., mountRef)
if err != nil {
    return err
}
untrackResolvedRef(mountRef)
...

terminalContainerRes, err := newSyntheticTerminalContainerResult(...)
if err != nil {
    return err
}
if err := terminalContainer.TerminalExecError(...); err != nil {
    return err
}
terminalContainerNeedsRelease = false
```
* Apply this ownership-state rewrite consistently in both places that currently duplicate the exec mount-state logic:
  * `prepareMounts(...)` / `materializedExecPlan`
  * `ContainerExecState.Evaluate(...)`
* This is the one non-trivial caller family for the `Commit()` contract change.
  * Most other callsites are direct handoff sites or simple post-commit cleanup removals.

### core/changeset.go
#### Replace `__immutableRef` with a synthetic directory result
#### Status
- [x] Give merged `After` directories a synthetic dagql result identity.
- [x] Implement `Changeset.AttachDependencyResults(...)` for `Before` / `After`.

* `newChangesetFromMerge(...)` is the only known caller of `Query.__immutableRef(...)`.
* Delete that schema round-trip entirely.
* After `gitMergeWithPatches(...)` / `gitOctopusMergeWithPatches(...)` returns a concrete merged `*Directory`, create the `After` result directly in code with `dagql.NewObjectResultForCall(...)` and a synthetic `ResultCall`.
* Do not route through a hidden `Query` field just to turn a snapshot back into a `Directory`.
* The synthetic call should carry enough identity to distinguish the merged output:
  * `SyntheticOp: "changeset_merge_output"` (or similarly explicit)
  * implicit input for the merged directory snapshot ID
  * implicit input for the merged subdir path
  * platform may also be included if needed for clarity/identity
* Do not use `dagql.NewObjectResultForCurrentCall(...)` here.
* That would stamp the merged directory with the current `Changeset` call frame instead of a real directory-specific result identity.
* The synthetic call is the clean replacement because it gives the merged `Directory` a first-class dagql result identity without leaking snapshot-manager internals into the schema layer.

```go
func newChangesetFromMerge(
    ctx context.Context,
    before dagql.ObjectResult[*Directory],
    afterDir *Directory,
) (*Changeset, error) {
    srv, err := CurrentDagqlServer(ctx)
    if err != nil {
        return nil, err
    }

    afterRef, err := afterDir.getSnapshot()
    if err != nil {
        return nil, fmt.Errorf("evaluate merged directory snapshot: %w", err)
    }
    if afterRef == nil {
        return nil, fmt.Errorf("evaluate merged directory snapshot: nil")
    }

    after, err := dagql.NewObjectResultForCall(afterDir, srv, &dagql.ResultCall{
        Kind:        dagql.ResultCallKindSynthetic,
        Type:        dagql.NewResultCallType(afterDir.Type()),
        SyntheticOp: "changeset_merge_output",
        ImplicitInputs: []*dagql.ResultCallArg{
            {
                Name: "snapshotID",
                Value: &dagql.ResultCallLiteral{
                    Kind:        dagql.ResultCallLiteralKindString,
                    StringValue: afterRef.SnapshotID(),
                },
            },
            {
                Name: "dir",
                Value: &dagql.ResultCallLiteral{
                    Kind:        dagql.ResultCallLiteralKindString,
                    StringValue: afterDir.Dir,
                },
            },
        },
    })
    if err != nil {
        return nil, fmt.Errorf("create synthetic merged directory result: %w", err)
    }

    return NewChangeset(ctx, before, after)
}
```

#### Attach `Before` / `After` result ownership explicitly
* `Changeset` should implement `dagql.HasDependencyResults`.
* The `Before` and `After` directory results are real child dependencies and must be attached explicitly.
* This is what makes the synthetic merged `After` directory safe:
  * dagql will attach the `Changeset`'s `Before` and `After` results
  * `AddExplicitDependency(...)` will record them on the parent `Changeset` result
  * the merged synthetic directory stays owned for as long as the `Changeset` stays live
* Without this, replacing `__immutableRef` with a synthetic result would still leave `Changeset` under-specified from a lifetime perspective.

```go
var _ dagql.HasDependencyResults = (*Changeset)(nil)

func (ch *Changeset) AttachDependencyResults(
    ctx context.Context,
    _ dagql.AnyResult,
    attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
    if ch == nil {
        return nil, nil
    }

    var deps []dagql.AnyResult

    if ch.Before.Self() != nil {
        attached, err := attach(ch.Before)
        if err != nil {
            return nil, fmt.Errorf("attach changeset before: %w", err)
        }
        before, ok := attached.(dagql.ObjectResult[*Directory])
        if !ok {
            return nil, fmt.Errorf("attach changeset before: unexpected result %T", attached)
        }
        ch.Before = before
        deps = append(deps, before)
    }

    if ch.After.Self() != nil {
        attached, err := attach(ch.After)
        if err != nil {
            return nil, fmt.Errorf("attach changeset after: %w", err)
        }
        after, ok := attached.(dagql.ObjectResult[*Directory])
        if !ok {
            return nil, fmt.Errorf("attach changeset after: unexpected result %T", attached)
        }
        ch.After = after
        deps = append(deps, after)
    }

    return deps, nil
}
```

#### Direct snapshot handoff
* `Changeset` helpers that commit a new snapshot and immediately wrap it in `NewFileWithSnapshot(...)` or `NewDirectoryWithSnapshot(...)` are already conceptually transfer-of-ownership callsites.
* After the constructor cutover, keep them as direct handoff paths:
  * commit
  * pass the committed handle to the constructor
  * if the constructor itself fails, release the committed handle directly
  * do not separately release the handle after successful handoff
* If a later step fails after constructing the object, release the constructed object.

### core/git.go
#### Status
- [x] Stop persisting bare-repo snapshot links directly on `GitRepository`.
- [x] Attach the internal remote mirror object as a dependency result instead.

#### Bare-repo ownership moves to the internal mirror object
* `GitRepository` should not directly own the bare mutable repo snapshot through `PersistedSnapshotRefLinks()`.
* That snapshot is owned by the internal persistable `RemoteGitMirror` object instead.
* `GitRepository.OnRelease(...)` should therefore only clean up any currently-open runtime handle it directly holds, not act as the persistent owner of the bare repo.
* Keep this file aligned with the new split:
  * bare repo snapshot ownership lives on the internal mirror object
  * checkout/tree `Directory` results remain ordinary dagql-owned values
* `GitRepository` should attach the internal mirror object as a dependency result where needed instead of persisting the bare snapshot itself.
* This file should not start cloning or reopening bare-repo snapshot handles just to satisfy object constructors.

### core/git_local.go
#### Status
- [x] Keep `Cleaned(...)` and checkout/tree constructor handoff as direct ownership-transfer sites.
- [x] Stop using `CachePolicyRetain` for ordinary local-git work refs.

#### Direct snapshot handoff for local git results
* The `Cleaned(...)` and checkout/tree paths that commit a new snapshot and then call `NewDirectoryWithSnapshot(...)` should stay as direct ownership-transfer sites.
* Those paths should also follow the new mutable-commit contract:
  * `bkref.Commit(ctx)` consumes `bkref`
  * do not separately release the mutable after a successful commit
* Do not reopen or clone those committed handles before constructor handoff.
* If `NewDirectoryWithSnapshot(...)` itself fails in those paths, release the committed snapshot handle directly.
* If `dagql.NewObjectResultForCurrentCall(...)` fails after the `Directory` is constructed, release the constructed `Directory` object before returning.

### core/mcp.go
#### Status
- [x] Resolve the concrete `RunningService` through `query.Services().Get(...)` and pass it to `runAndSnapshotChanges(...)`.

#### Service workspace sync follows runtime ownership
* `callBatchMCPServer(...)` is impacted by the `runAndSnapshotChanges(...)` signature change.
* It should resolve the concrete `RunningService` through `query.Services().Get(...)` and pass that to `runAndSnapshotChanges(...)`.
* It must not continue to pass a raw container ID string.
* The workspace snapshot returned from `runAndSnapshotChanges(...)` is transferred directly into the `Directory` object it returns through that helper; `core/mcp.go` should not try to manage those refs itself.

### core/builtincontainer.go
#### Builtin OCI content path
* Keep this file only if we still want a small core helper around builtin image import.
* If it survives, it should:
  * treat `query.BuiltinOCIStore()` as a static source store only
  * copy the builtin manifest closure from `query.BuiltinOCIStore()` into `query.OCIStore()`
  * create the flat temporary work lease on `query.OCIStore()`, not on the builtin source store
  * call `container.FromOCIStore(...)`
* Delete the current dependency on `core/containersource`.
* Delete `BuiltInContainerUpdateConfig(...)`.
* Builtin-image rootfs import and config loading should happen together through the same one-pass import path used for other OCI-store imports.

### core/persisted_object.go
#### Status
- [x] Delete dead retained-chain helper and keep persisted snapshot links as reopen-only helpers.

#### Persisted snapshot links reopen handles only
* Persisted snapshot links should only be used to reopen snapshot handles by snapshot ID.
* They are not the place where retention is enforced.
* Delete `retainImmutableRefChain(...)`.
* Do not call `SetCachePolicyRetain()` or similar from persisted-object loading code.
* The lease ownership for persisted ordinary results belongs with the owning dagql results, not here.

```go
func loadPersistedImmutableSnapshotByResultID(
    ctx context.Context,
    dag *dagql.Server,
    resultID uint64,
    label, role string,
) (bkcache.ImmutableRef, dagql.PersistedSnapshotRefLink, error) {
    link, err := loadPersistedSnapshotLinkByResultID(ctx, dag, resultID, label, role)
    if err != nil {
        return nil, dagql.PersistedSnapshotRefLink{}, err
    }
    query, err := persistedDecodeQuery(dag)
    if err != nil {
        return nil, dagql.PersistedSnapshotRefLink{}, err
    }
    ref, err := query.SnapshotManager().GetBySnapshotID(ctx, link.RefKey, bkcache.NoUpdateLastUsed)
    if err != nil {
        return nil, dagql.PersistedSnapshotRefLink{}, fmt.Errorf("open persisted immutable snapshot %q: %w", link.RefKey, err)
    }
    return ref, link, nil
}
```

### core/cache.go
#### Status
- [x] Persist `CacheVolume.Source` as ordinary owner-object state and restore it on decode.
- [x] Keep durable `snapshotID` / `selector` state even when the mutable handle is closed.
- [x] Make closed-state cache usage identity and size accounting work from persisted `snapshotID`.

#### Cache-volume persistable owner object
* `CacheVolume` is not an ordinary dagql-owned immutable value.
* It is itself the canonical persistable dagql owner object for its mutable snapshot.
* There is no separate durable-owner registry/index and no separate semantic owner key in snapshot manager.
* Every distinct cache-volume constructor input set means a distinct cache volume object and a distinct mutable snapshot.
  * `key`
  * `namespace`
  * `source`
  * `sharing`
  * `owner`
  * plus the existing `privateNonce` behavior for `PRIVATE`
* That means:
  * `SHARED`, `LOCKED`, and `PRIVATE` are all distinct cache volumes because `sharing` is part of identity
  * `source` / `owner` changes also mean a different cache volume
  * the snapshot manager does not need to understand any of those semantics
* Replace generic `CachePolicyRetain` usage with ordinary persistable-object ownership:
  * `CacheVolume` persists its own snapshot link
  * it lazily reopens its mutable handle by `snapshotID`
  * dagql ownership keeps that snapshot alive
* `source` must survive persistence as ordinary owner-object state.
  * If a cache volume was configured from a source directory, `EncodePersistedObject(...)` should persist that source input in the normal object payload.
  * `DecodePersistedObject(...)` should restore it there too.
  * That way, if the persisted snapshot was pruned, `InitializeSnapshot(...)` can actually recreate from source after restart instead of silently degrading to empty-state recreation.
* `CacheVolume.OnRelease(...)` should only close the currently-open mutable handle.
* It should not destroy the cache volume resource itself.
* `InitializeSnapshot(...)` should stop passing `bkcache.CachePolicyRetain` to `New(...)`.
* If the persisted `snapshotID` no longer exists, treat that as a cache miss and recreate the snapshot from source/empty state.
* `LOCKED` is a runtime access-serialization policy on the owner object, not a different snapshot-manager ownership mode.
* `SHARED` allows concurrent use of the same cache volume object.
* `PRIVATE` still becomes unique through the existing dagql cache-key nonce behavior.

```go
func (cache *CacheVolume) InitializeSnapshot(ctx context.Context) error {
    cache.mu.Lock()
    defer cache.mu.Unlock()

    if cache.snapshot != nil {
        return nil
    }

    query, err := CurrentQuery(ctx)
    if err != nil {
        return err
    }

    if cache.snapshotID != "" {
        ref, err := query.SnapshotManager().GetMutableBySnapshotID(ctx, cache.snapshotID, bkcache.NoUpdateLastUsed)
        if err == nil {
            cache.snapshot = ref
            return nil
        }
        if !cerrdefs.IsNotFound(err) {
            return err
        }

        // Pruned or otherwise missing: treat as cache miss and recreate.
        cache.snapshotID = ""
        cache.selector = "/"
    }

    sourceRef, sourceSelector, err := cache.resolveSourceSnapshot(ctx)
    if err != nil {
        return err
    }
    ref, err := query.SnapshotManager().New(
        ctx,
        sourceRef,
        bkcache.WithRecordType(bkclient.UsageRecordTypeCacheMount),
        bkcache.WithDescription(fmt.Sprintf("cache volume %q", cache.Key)),
    )
    if err != nil {
        return err
    }
    cache.snapshot = ref
    cache.snapshotID = ref.SnapshotID()
    cache.selector = sourceSelector
    return nil
}
```

```go
func (cache *CacheVolume) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
    cache.mu.Lock()
    defer cache.mu.Unlock()

    if cache.snapshotID == "" {
        return nil
    }
    slot := cache.selector
    if slot == "" {
        slot = "/"
    }
    return []dagql.PersistedSnapshotRefLink{{
        RefKey: cache.snapshotID,
        Role:   "snapshot",
        Slot:   slot,
    }}
}
```

* `DecodePersistedObject(...)` should restore:
  * `snapshotID` / `selector` from persisted links
  * ordinary persisted owner-object fields like `source`
* It should not eagerly reopen the mutable handle during startup import.
* If we need explicit runtime serialization for `LOCKED`, it should live on the `CacheVolume` object itself:
  * `acquire(ctx)` ensures the mutable snapshot is open
  * `LOCKED` serializes active users
  * `SHARED` allows concurrent active users
  * `PRIVATE` just uses its unique object identity
* Closed-state cache accounting must not depend on a live mutable handle being open.
  * `CacheUsageIdentity()` should use persisted `snapshotID` directly when the runtime mutable handle is closed.
  * `CacheUsageSize()` should stat by `snapshotID` when the runtime mutable handle is closed.
  * If a live mutable handle is open, it can still use that directly.
* The same closed-state accounting rule should apply to other persistable mutable-owner objects such as `ClientFilesyncMirror` and `RemoteGitMirror`.

### core/client_filesync_mirror.go
#### Dagql-owned persistent filesync mirror
* Add a new internal core type for the per-stable-client filesync mirror.
* This is not an ordinary immutable value result.
* It is an internal persistable dagql resource object keyed by:
  * stable client ID
  * drive
* This is the canonical pattern for persistable mutable owner objects in the new design.
  * cache volumes and internal bare git mirrors should follow this same shape
  * persist `snapshotID`
  * lazily reopen mutable runtime state by snapshot ID
  * let dagql object identity, not snapshot-manager semantic lookup, be the authoritative resource identity
* The key hard cut is that filesync mirror lifetime moves into dagql instead of living as hidden retained snapshot-manager state.
* That means:
  * dagql can see and persist it
  * dagql can account for its size
  * dagql can prune it
  * if pruned, the next filesync rebuilds it from scratch
* Persisted state for the object should be minimal:
  * stable client ID
  * drive
  * mutable snapshot link through `PersistedSnapshotRefLinks()`
* Runtime-only state is rebuilt lazily on first use:
  * reopened mutable handle
  * mount/mounter
  * mounted root path
  * shared change cache
  * in-memory usage count

```go
type ClientFilesyncMirror struct {
    StableClientID string
    Drive          string

    // Non-empty only for the ephemeral, non-persisted fallback when the client
    // has no stable identity.
    EphemeralID string

    mu sync.Mutex

    // Persisted identity of the mutable mirror snapshot.
    snapshotID string

    // Runtime-only reopened mutable handle.
    snapshot bkcache.MutableRef

    // Runtime-only mount state.
    mounter snapshot.Mounter
    mntPath string

    sharedState *filesyncMirrorSharedState
    usageCount  int

    persistedResultID uint64
}

type filesyncMirrorSharedState struct {
    rootPath    string
    changeCache *changeCache
}
```

* `persistedResultID` is the dagql persisted-result identity of the mirror object, just like other persistable core objects.
* When `StableClientID == ""`, the object is ephemeral-only:
  * it must not be returned from `_clientFilesyncMirror(...)`
  * it must not persist itself through dagql
  * it is just a convenient internal owner object for the current call/runtime
* This object should implement the usual dagql persistence and release hooks:
  * `PersistedResultID()`
  * `SetPersistedResultID(uint64)`
  * `PersistedSnapshotRefLinks()`
  * `EncodePersistedObject(...)`
  * `DecodePersistedObject(...)`
  * `OnRelease(...)`
  * `CacheUsageSize(...)`
  * `CacheUsageIdentity()`
  * `CacheUsageMayChange()`
* Decode should restore only `snapshotID` from persisted links.
* It should not reopen the mutable handle or remount eagerly during startup import.
* Reopening the mutable snapshot handle and rebuilding `sharedState` should happen lazily on first use.

```go
func (m *ClientFilesyncMirror) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
    if m == nil || m.snapshotID == "" {
        return nil
    }
    return []dagql.PersistedSnapshotRefLink{{
        RefKey: m.snapshotID,
        Role:   "snapshot",
        Slot:   "/",
    }}
}

func (m *ClientFilesyncMirror) CacheUsageMayChange() bool {
    return true
}

func (m *ClientFilesyncMirror) CacheUsageIdentity() (string, bool) {
    if m == nil || m.snapshotID == "" {
        return "", false
    }
    return m.snapshotID, true
}
```

* `CacheUsageSize()` should also work when the mutable runtime handle is closed.
  * Use the persisted `snapshotID` to stat the snapshot in that case.
  * Do not require `m.snapshot != nil` just for accounting/prune visibility.

* The object should own the runtime mirror lifecycle explicitly.
* It should have:
  * `EnsureCreated(ctx, query)` for first creation
  * `acquire(ctx, query)` which lazily reopens the mutable handle, mounts it, builds `sharedState`, and increments `usageCount`
  * a release closure from `acquire(...)` that decrements `usageCount` and, when it drops to zero, unmounts and releases the mutable handle
* `OnRelease(...)` should be the final cleanup hook that unmounts and releases any currently-open runtime state.
* Closing the runtime handle in `OnRelease(...)` does not destroy the mirror resource; the persisted result link is what keeps the mirror pruneable/persistent in dagql.

```go
func (m *ClientFilesyncMirror) EnsureCreated(ctx context.Context, query *Query) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if m.snapshotID != "" || m.snapshot != nil {
        return nil
    }

    ref, err := query.SnapshotManager().New(
        ctx,
        nil,
        bkcache.WithRecordType(bkclient.UsageRecordTypeLocalSource),
        bkcache.WithDescription(func() string {
            if m.StableClientID != "" {
                return fmt.Sprintf("client filesync mirror for %s%s", m.Drive, m.StableClientID)
            }
            return fmt.Sprintf("ephemeral client filesync mirror for %s%s", m.Drive, m.EphemeralID)
        }()),
    )
    if err != nil {
        return err
    }
    m.snapshot = ref
    m.snapshotID = ref.SnapshotID()
    return nil
}
```

* The mirror object itself is the persistent thing.
* The immutable snapshot returned from one filesync call is not the persistent thing.
* That is the main architectural correction compared to the current hidden-mirror path.

### core/schema/query.go
#### Hidden internal filesync mirror field
#### Status
- [x] Add hidden persistable `Query._clientFilesyncMirror(...)`.
- [x] Make it create the mirror snapshot on first creation and return the persisted mirror object thereafter.

* Add a hidden internal `Query._clientFilesyncMirror(...)` field.
* It is `IsPersistable()`.
* It takes normal explicit args:
  * `stableClientID`
  * `drive`
* It is not `PerClientInput`, `PerSessionInput`, or `PerCallInput`.
* It is just a hidden internal persistable query field keyed by ordinary args.
* On first creation it should call `EnsureCreated(...)`.
* On later hits it should just return the persisted mirror object and let runtime state reopen lazily on use.
* This field must reject empty `stableClientID`.
  * The no-stable-ID case is handled by creating an ephemeral mirror object directly in `host.directory(...)`, not by persisting a fake identity here.

```go
type clientFilesyncMirrorArgs struct {
    StableClientID string
    Drive          string `default:""`
}

dagql.Fields[*core.Query]{
    dagql.NodeFunc("_clientFilesyncMirror", s.clientFilesyncMirror).
        IsPersistable().
        Doc(`(Internal-only) Returns the persistent filesync mirror for a stable client and drive.`).
        Args(
            dagql.Arg("stableClientID").Doc("Stable client identifier."),
            dagql.Arg("drive").Doc("Drive prefix for Windows clients; empty otherwise."),
        ),
}.Install(srv)
```

#### Hidden internal bare-git mirror field
#### Status
- [x] Add hidden persistable `Query._remoteGitMirror(...)`.
- [x] Make it create the bare mutable snapshot on first creation and return the persisted mirror object thereafter.

* Add a hidden internal `Query._remoteGitMirror(...)` field.
* It is `IsPersistable()`.
* It takes the normalized remote URL as an ordinary explicit arg.
* It is the authoritative persistable dagql owner object for the mutable bare-repo snapshot.
* This URL-only mirror identity is deliberate.
  * Higher-level dagql git result identity may still remain auth-scoped where needed.
  * The internal bare mirror is just the long-lived local transport/cache substrate for that remote URL.
  * It must not become a persistent sink for auth-derived identity or credential state.
* On first creation it should create the bare mutable snapshot.
* On later hits it should return the persisted mirror object and let runtime state reopen lazily on use.

```go
type remoteGitMirrorArgs struct {
    RemoteURL string
}

dagql.Fields[*core.Query]{
    dagql.NodeFunc("_remoteGitMirror", s.remoteGitMirror).
        IsPersistable().
        Doc(`(Internal-only) Returns the persistent bare git mirror for a remote URL.`).
        Args(
            dagql.Arg("remoteURL").Doc("Normalized remote repository URL."),
        ),
}.Install(srv)
```

### core/git_remote.go
#### Status
- [x] Reopen cached checkout hits by concrete snapshot identity instead of metadata-record `cache.Get(...)`.
- [x] Stop using `CachePolicyRetain` for ordinary remote checkout work refs.
- [x] Clean up deterministic `refs/dagger.fetch/...` scratch refs after named-ref fallback fetches.
- [x] Move bare remote mutable ownership fully onto the internal persistable mirror object.
- [x] Make `RemoteGitMirror` participate in closed-state cache accounting and prune visibility.

#### Internal persistable bare-git mirror owner
* Split the two git snapshot cases cleanly:
  * the bare remote repo is owned by an internal persistable dagql mirror object
  * checkout/tree snapshots are ordinary dagql-owned immutable results
* Do not keep a separate durable-owner registry/index for bare repos.
* The canonical owner of the mutable bare repo snapshot should be an internal persistable object keyed by the normalized remote URL only.
* Auth/session inputs are runtime capabilities for fetch/update; they are not part of the bare-repo mirror identity.
* Keeping URL-only mirror identity is a deliberate tradeoff.
  * Higher-level dagql git metadata/result identity may stay auth-scoped to avoid cache confusion across auth modes.
  * The persistent bare mirror itself remains URL-keyed only.
  * We accept that it may retain fetched git objects across credential rotation because it is a local mirror/cache substrate, not an auth boundary.
* The important hard rule is that auth must remain runtime-only and ephemeral.
  * Do not persist token/header/SSH-handle identity on `RemoteGitMirror`.
  * Do not write auth-derived config into the persisted mirror object as durable state.
  * Drive auth through transient per-operation git invocation wiring instead of durable mirror metadata.
* Temporary fetch namespace refs must not accumulate indefinitely.
  * Deterministic refs under `refs/dagger.fetch/...` are temporary operational refs only.
  * Clean them up after the fetch/hydration operation completes.
  * The mirror should not grow unbounded scratch refs across retries, repeated fetches, or credential rotation.
* The bare mirror object should:
  * persist its `snapshotID`
  * lazily reopen the mutable snapshot by `snapshotID`
  * serialize mutating bare-repo operations with an internal mutex
  * recreate the bare repo from scratch if the persisted snapshot was pruned
* `RemoteGitRepository` should no longer pretend to directly own the bare mutable snapshot through its own persisted snapshot links.
* Instead, it should resolve the internal mirror object and use that as the runtime owner of the bare repo state.
* The checkout/tree snapshot should be created as an ordinary immutable snapshot and released by the returned `Directory` object through dagql.
* The cached checkout hit path and the newly committed checkout path should both transfer ownership of the immutable snapshot handle directly into `NewDirectoryWithSnapshot(...)`.
* The newly committed checkout path should also follow the new mutable-commit contract:
  * `checkoutRef.Commit(ctx)` consumes `checkoutRef`
  * do not separately release the mutable after a successful commit
* If `NewDirectoryWithSnapshot(...)` itself fails in either path, release the immutable snapshot handle directly.
* Do not separately release the handle after successful constructor handoff.
* Because snapshot-manager identity is collapsing onto snapshot identity, cached checkout reopen should move from metadata-record `cache.Get(ctx, md.ID())` to reopening by concrete snapshot identity.

```go
type RemoteGitMirror struct {
    RemoteURL string

    mu sync.Mutex

    snapshotID string
    snapshot   bkcache.MutableRef

    persistedResultID uint64
}

func (repo *RemoteGitRepository) initRemote(...) error {
    query, err := CurrentQuery(ctx)
    if err != nil {
        return err
    }

    mirror, err := query.remoteGitMirror(ctx, normalizeRemoteURL(repo.URL.Remote()))
    if err != nil {
        return err
    }

    remoteRef, release, err := mirror.acquire(ctx, query)
    if err != nil {
        return err
    }
    defer release()

    repo.setSnapshot(remoteRef)
    ...
}
```

* The `searchGitSnapshot(cacheKey)` checkout reuse path can stay as a snapshot metadata lookup, but it should not imply retain policy or any special long-lived ownership by itself.
* Auth/session state must therefore not accumulate on the runtime mirror object either.
  * `repo.setup(...)`, `ls-remote`, `fetch`, and similar operations can borrow current auth/runtime capabilities while they run.
  * Once the operation completes, the surviving long-lived mirror state is just the bare repo plus the URL-keyed mutable snapshot.

### engine/buildkit/client.go
#### Status
- [x] Delete `Client.ID()`.
- [x] Remove `BkSession` from `buildkit.Client.Opts`.
- [x] Keep `WriteImage(...)` as the real client-side destination seam.

#### Host image reader
* Delete `Client.ID()` outright.
  * It only exists to expose the old server-created `BkSession` identity.
  * There should be no surviving Dagger-owned caller that needs a buildkit-client session ID after this cut.
  * Any remaining caller of `c.ID()` or any `session.Group` built from it is a missed migration, not a reason to keep a replacement identity.
* Remove `BkSession` from `buildkit.Client.Opts`.
* Remove any remaining Dagger-owned `session.Group` creation that depends on `buildkit.Client`.
* Keep `ReadImage(...)` for now.
* It already talks to the real client attachable connection, not the fake `bk_session.go` path.
* The two valid result shapes are:
  * content/images/leases store access
  * tarball stream fallback
* `ImagesStore` is intentional here.
  * This seam is the local host image boundary.
  * `ImagesStore` is the correct named-image abstraction for that boundary.
  * It should not be generalized into the session-owned registry resolver or snapshot import architecture.
* `host.containerImage(...)` should keep using this seam until we have a better-named home for it.
* What changes is what happens next:
  * store-backed reads use `ImagesStore` to get the local named image target, use `ContentStore` to pick and copy a manifest closure that actually exists locally, then call `FromOCIStore(...)`
  * tarball reads stream into `Import(...)`
* `ReadImage(...)` should not gain any dependency on `core/containersource` or fake `oci-layout` session attachables.
* `WriteImage(...)` may still remain as the real client-side destination seam.
* When it returns a tarball writer, that writer should be handed directly to `WriteContainerImageTarball(...)`.
* We should not materialize a temporary tarball file and reopen it just to satisfy that API.

### engine/filesync/filesyncer.go
#### Status
- [x] Remove `session.Group` from the filesync API surface and internal mutable-mount calls.
- [x] Remove `session.Manager` from the filesync API entirely.
- [x] Delete the stateful per-client mirror registry and move persistent mirror ownership to `core/client_filesync_mirror.go`.

#### Stateless filesync helpers only
* Remove `session.Manager` and `session.Group` from the filesync API entirely.
* This file should not know about `BuildkitSession()` or fake server-created buildkit sessions.
* It should also stop owning the persistent per-client mirror registry entirely.
* Delete the stateful mirror-management pieces:
  * `refs map[string]*filesyncCacheRef`
  * `perClientMu`
  * `filesyncCacheRef`
  * `getRef(...)`
  * `searchSharedKey(...)`
  * `setSharedKey(...)`
  * creation/reuse of retained mutable refs keyed by stable client ID
* The per-client mirror is now owned by the internal dagql object result in `core/client_filesync_mirror.go`, not by this package.
* What survives here is only the lower-level sync implementation:
  * build a `filesync.FileSyncClient` from the real caller connection
  * stat/diff the remote path
  * mutate the already-open local mirror state
  * produce the ordinary immutable result snapshot for the current call
* It is fine if this file keeps a thin stateless helper or namespace type for those mechanics, but it should not own persistence, reuse, or pruning anymore.

### engine/filesync/localfs.go
#### Status
- [x] Drop `session.Group` from `localFS.Sync(...)` and use sessionless snapshot mounts there.
- [x] Remove `Finalize(...)` from the committed `finalRef` path.
- [x] Stop retaining `finalRef` or manually releasing the consumed `newCopyRef` after `Commit(...)`.
- [x] Move persistent mirror ownership entirely out of this file and into the dagql mirror object.

#### Mirror mutation + ordinary result materialization
* `localFS.Sync(...)` should also drop `session.Group`.
* `newCopyRef.Mount(...)` and all other snapshot mounts in this flow should use the sessionless mount API.
* The persistent mirror is no longer managed here through retain policy or durable-owner logic.
* `localFS.Sync(...)` should assume it was given a `localFSSharedState` backed by an already-open mirror object.
* The mirror object's mutable snapshot remains the shared mutable base that is mutated in place.
* The ordinary temporary copy/ref work inside sync stays temporary.
* The committed `finalRef` returned from `localFS.Sync(...)` is just the ordinary immutable result for the current call.
* It is not the persistent mirror resource.
* Remove `Finalize(...)` from this flow.
* `Commit(...)` should directly produce the committed snapshot returned to the caller and consume the mutable `newCopyRef`.
* There should be no `BindDurableOwner(...)`, no `AttachLease(...)`, and no `SetCachePolicyRetain()` in this flow.
* The persistence/pruning/accounting of the mirror itself is handled entirely by the internal dagql mirror object.

```go
func (local *localFS) Sync(...) (_ bkcache.ImmutableRef, _ digest.Digest, rerr error) {
    ...
    finalRef, err := newCopyRef.Commit(ctx)
    if err != nil {
        return nil, "", err
    }
    return finalRef, dgst, nil
}
```

### engine/contenthash/checksum.go
#### Status
- [x] Remove `session.Group` from checksum mounts and checksum APIs.
- [x] Stop swallowing `GetCacheContext(...)` failures in `Checksum(...)`.
- [x] Persist copied cache contexts immediately in `SetCacheContext(...)` when the destination ref ID differs.

#### Sessionless checksum mounts
* Remove `session.Group` from checksum mounts entirely.
* Checksum should mount already-committed immutable snapshots readonly.
* It must not force `Finalize(...)` / `Extract(...)` / unlazy behavior.
* If a snapshot is not locally materialized and checksum cannot proceed, that is a bug in the earlier pipeline that produced the object.
* `Checksum(...)` must not silently swallow `GetCacheContext(...)` failures.
  * If cache-context lookup fails, return the error.
  * Under the new model, missing/broken checksum context is a real correctness failure, not something to hide with `("", nil)`.
* `SetCacheContext(...)` must work correctly when assigning an existing cache context to a different committed ref ID.
  * If the destination `md.ID()` differs from the source cache-context metadata ID, the copied cache context for the destination ref must be persisted immediately, not just placed in the in-memory LRU.
  * The need for any `Set/Get/Set` dance is therefore a bug in `SetCacheContext(...)`, not something callers should work around.

```go
type mount struct {
    mountable cache.Mountable
    mountPath string
    unmount   func() error
}

func (m *mount) mount(ctx context.Context) (string, error) {
    if m.mountPath != "" {
        return m.mountPath, nil
    }
    mounts, err := m.mountable.Mount(ctx, true)
    if err != nil {
        return "", err
    }
    lm := snapshot.LocalMounter(mounts)
    mp, err := lm.Mount()
    if err != nil {
        return "", err
    }
    m.mountPath = mp
    m.unmount = lm.Unmount
    return mp, nil
}
```

### core/contenthash.go
#### Status
- [x] Stop calling `Finalize(...)` from `getContentHashFromRef(...)`.
- [x] Route content hashes through the sessionless checksum API only.

#### Contenthash without finalize
* `getContentHashFromRef(...)` should stop calling `Finalize(...)`.
* It should operate on already-committed immutable snapshots only.
* The contenthash path should not be responsible for changing snapshot lifecycle state.

```go
func getContentHashFromRef(ctx context.Context, ref bkcache.ImmutableRef, subdir string) (digest.Digest, error) {
    if ref == nil {
        return "", fmt.Errorf("cannot get content hash from nil ref")
    }
    md := contenthash.CacheRefMetadata{RefMetadata: ref}
    if subdir == "/" {
        if dgst, ok := md.GetContentHashKey(); ok {
            return dgst, nil
        }
    }
    dgst, err := bkcontenthash.Checksum(ctx, ref, subdir, bkcontenthash.ChecksumOpts{
        FollowLinks: true,
    })
    if err != nil {
        return "", err
    }
    if subdir == "/" {
        if err := md.SetContentHashKey(dgst); err != nil {
            return "", err
        }
    }
    return dgst, nil
}
```

### engine/filesync/localfs.go
#### Remove contenthash workaround dance
#### Status
- [x] Delete the `Set/Get/Set` cache-context workaround dance after committing `finalRef`.

* `localFS.Sync(...)` should not do the old BuildKit `SetCacheContext / GetCacheContext / SetCacheContext` workaround after committing `finalRef`.
* After `finalRef.Commit(...)`, it should do exactly one cache-context assignment:
  * `bkcontenthash.SetCacheContext(ctx, finalRef, cacheCtx)`
* If that fails, return the error.
* The correctness fix belongs in `engine/contenthash/checksum.go`, not in filesync callers.

### engine/buildkit/worker_source_metadata.go
#### Status
- [x] Delete the worker-side exporter registration cases for `ExporterImage`, `ExporterOCI`, and `ExporterDocker`.
- [x] Make the live `docker-image` / `oci-layout` `ResolveSourceMetadata(...)` branches fail instead of silently preserving the old path.
- [x] Delete the remaining dead image-resolution helpers and worker-side resolver state from this file entirely.

#### Delete old source-metadata image-resolution path
* Delete the image-resolution path in this file outright.
* Do not keep `Worker.ResolveSourceMetadata(...)` as a registry or OCI-layout resolution hop.
* Delete the worker-side exporter registration cases for:
  * `ExporterImage`
  * `ExporterOCI`
  * `ExporterDocker`
* Dagger production code must not request those worker exporter names anymore after the cut.
* If some remaining path still requires those registrations, that path is not migrated and should block the hard cut.
* Delete:
  * the `docker-image` and `oci-layout` branches in `ResolveSourceMetadata(...)`
  * `resolveRegistryImageConfig(...)`
  * `resolveOCILayoutImageConfig(...)`
  * `resolveImageResult`
  * `ociLayoutResolver`
* Delete the worker-global resolve dedupe fields in [`engine/buildkit/worker.go`](/home/sipsma/repo/github.com/sipsma/dagger/engine/buildkit/worker.go):
  * `registryResolveImageConfigG`
  * `ociLayoutResolveImageConfigG`
* All live Dagger-owned image resolution should go directly through:
  * `query.RegistryResolver(...).ResolveImageConfig(...)`
  * `query.RegistryResolver(...).Pull(...)`
  * `query.OCIStore()` / `query.BuiltinOCIStore()`
  * `query.SnapshotManager().ImportImage(...)`
* If that leaves only generic worker helpers in this file, move them to an ordinary worker file and delete `worker_source_metadata.go` entirely.

### engine/buildkit/containerimage.go
#### Status
- [x] Add a deterministic typed `buildExportRequest(...)`.
- [x] Delete `ContainerImageToTarball(...)` and replace it with direct `WriteContainerImageTarball(...)`.
- [x] Route `PublishContainerImage(...)` and `ExportContainerImage(...)` through `engine/buildkit/imageexport`.
- [x] Delete exporter response-map parsing from Dagger-owned production callers.

#### Image push / export callsites
* This file is part of the live production image export path today.
* It should stop routing through the generic exporter-plugin shape as the primary architecture.
* The canonical in-house home for image export logic should be a new package:
  * `engine/buildkit/imageexport`
* This file should call that package directly for:
  * tarball writing
  * registry push
  * image-store export
* Image publish/export paths should route through the session-owned resolver facade for registry push behavior instead of `internal/buildkit/util/push` + `internal/buildkit/util/resolver`.
* This file should take ownership of direct tarball writing for live Dagger APIs.
* Add a helper that writes an image archive directly to an `io.Writer`.
* Delete `ContainerImageToTarball(...)`.
* `PublishContainerImage(...)` and `ExportContainerImage(...)` should stop instantiating exporter plugins entirely and instead call the new `engine/buildkit/imageexport` package directly.
* Tarball-producing APIs should not route through exporter tar mode, fake buildkit-session filesync targets, or a `sessionID` plumbing seam.
* Replace `combineContainerRefs(...)` with a Dagger-owned typed request builder.
  * The current helper is building a fake `exporter.Source` by stuffing platform/config state into `exptypes` metadata keys.
  * That encoding should not survive as part of the canonical export architecture.
  * The new helper should build a typed `imageexport.ExportRequest` directly.
  * It must sort the platform inputs deterministically before returning so multi-platform index ordering does not depend on Go map iteration.

```go
func (c *Client) buildExportRequest(
    ctx context.Context,
    inputByPlatform map[string]ContainerExport,
) (*imageexport.ExportRequest, error) {
    inputs := make([]imageexport.PlatformExportInput, 0, len(inputByPlatform))

    for platformKey, input := range inputByPlatform {
        platform, err := platforms.Parse(platformKey)
        if err != nil {
            return nil, err
        }

        cfg := dockerspec.DockerOCIImage{
            Image: specs.Image{
                Platform: specs.Platform{
                    Architecture: platform.Architecture,
                    OS:           platform.OS,
                    OSVersion:    platform.OSVersion,
                    OSFeatures:   platform.OSFeatures,
                    Variant:      platform.Variant,
                },
            },
            Config: input.Config,
        }

        manifestAnnotations := map[string]string{}
        manifestDescriptorAnnotations := map[string]string{}
        for _, annotation := range input.Annotations {
            manifestAnnotations[annotation.Key] = annotation.Value
            manifestDescriptorAnnotations[annotation.Key] = annotation.Value
        }

        inputs = append(inputs, imageexport.PlatformExportInput{
            Key:                           platformKey,
            Platform:                      platform,
            Ref:                           input.Ref,
            Config:                        cfg,
            ManifestAnnotations:           manifestAnnotations,
            ManifestDescriptorAnnotations: manifestDescriptorAnnotations,
        })
    }

    slices.SortFunc(inputs, func(a, b imageexport.PlatformExportInput) int {
        return cmp.Compare(a.Key, b.Key)
    })

    return &imageexport.ExportRequest{
        Platforms: inputs,
    }, nil
}

func (c *Client) WriteContainerImageTarball(
    ctx context.Context,
    w io.Writer,
    inputByPlatform map[string]ContainerExport,
    useOCIMediaTypes bool,
    forceCompression string,
) error

func (c *Client) PublishContainerImage(
    ctx context.Context,
    inputByPlatform map[string]ContainerExport,
    refName string,
    useOCIMediaTypes bool,
    forceCompression string,
) (*imageexport.ExportResponse, error)

func (c *Client) ExportContainerImage(
    ctx context.Context,
    destPath string,
    inputByPlatform map[string]ContainerExport,
    forceCompression string,
    tarExport bool,
    leaseID string,
    useOCIMediaTypes bool,
) (*imageexport.ExportResponse, error)
```

* `WriteContainerImageTarball(...)` should:
  * call `buildExportRequest(...)`
  * call the canonical `imageexport` writer with the new typed/sessionless signature
  * use the returned `ExportedImage.Provider` as the local archive provider
  * call `archiveexporter.Export(ctx, provider, w, ...)`
* `forceCompression` must be preserved as real behavior here.
  * It is not just a preferred default compression for newly-created blobs.
  * It means the tarball path sets the requested compression in `RefCfg.Compression` and also sets `Force=true`, so existing layers are recompressed when needed.
* This helper is the direct replacement for the current tar branch buried under `engine/buildkit/exporter/oci/export.go`.
* `exportImage(...)` tarball mode in `core/schema/container.go` and `AsTarball(...)` in `core/container_image.go` should both call this helper directly.
* `PublishContainerImage(...)` should:
  * call `buildExportRequest(...)`
  * call `imageexport.Export(...)` with `Push=true`
  * return the typed `*imageexport.ExportResponse`
* `ExportContainerImage(...)` should:
  * call `buildExportRequest(...)`
  * call `imageexport.Export(...)` with `Store=true`
  * return the typed `*imageexport.ExportResponse`
* There is no surviving `solverresult.Result[bkcache.ImmutableRef]` packaging in this file after the cut.
* There is no surviving exporter `Resolve(...)` / `Export(...)` call path in this file after the cut.
* There is no surviving exporter `map[string]string` response parsing in this file after the cut.
* `core/container.go` should consume the typed `ExportResponse` directly.
  * `Publish(...)` should stop parsing `containerimage.digest` out of an exporter response map and instead use `resp.RootDesc.Digest`.
  * `Export(...)` should stop decoding `containerimage.descriptor` from base64 and instead return `resp.RootDesc` directly.
* Delete exporter response-map parsing from Dagger-owned production code.

```go
func (c *Client) WriteContainerImageTarball(
    ctx context.Context,
    w io.Writer,
    inputByPlatform map[string]ContainerExport,
    useOCIMediaTypes bool,
    forceCompression string,
) error {
    req, err := c.buildExportRequest(ctx, inputByPlatform)
    if err != nil {
        return err
    }

    refCfg := cacheconfig.RefConfig{
        Compression: compression.New(compression.Default),
    }
    if forceCompression != "" {
        ctype, err := compression.Parse(forceCompression)
        if err != nil {
            return err
        }
        refCfg.Compression = compression.New(ctype).SetForce(true)
    }

    assembled, err := c.Worker.imageExportWriter.Assemble(ctx, req, imageexport.CommitOpts{
        RefCfg:   refCfg,
        OCITypes: useOCIMediaTypes,
    })
    if err != nil {
        return err
    }

    return archiveexporter.Export(ctx, assembled.Provider, w, archiveexporter.WithManifest(assembled.RootDesc))
}
```

### engine/buildkit/imageexport/writer.go
#### Status
- [x] Create the canonical `engine/buildkit/imageexport` package and typed writer/request/response surfaces.
- [x] Assemble typed `ExportedImage` values for live Dagger production export callsites.
- [x] Eliminate the remaining legacy metadata-bag / legacy writer internals under `Writer.Assemble(...)`.
- [x] Switch live layer loading over to `ExportChain(...)` instead of `GetRemotes(...)` / `LayerChain()`.

#### Canonical in-house image writer
* Create this package as the canonical home for Dagger's real image export logic.
* Move the useful core of `engine/buildkit/exporter/containerimage/writer.go` here.
* This file should own:
  * loading local export chains from snapshot refs
  * normalization of layer descriptors and history
  * config/manifest/index creation
  * optional timestamp rewrite if that survives
  * content-store writes for config/manifest/index blobs
  * honoring `RefCfg.Compression`, including `Force=true` recompression of existing layers when requested
* This file should expose typed direct APIs, not generic exporter-plugin entrypoints.
* The old `patch.go` helper should be folded into this file rather than surviving as a separate abstraction.
* Keep this file tightly focused on local image assembly logic.
* It should not know about:
  * exporter plugin `Resolve(...)`
  * `result.Result[cache.ImmutableRef]`
  * metadata bags encoded with `exptypes.*` keys
  * lazy `InlineCache` callbacks
  * `session.Group`
  * lazy remotes
  * attestation-specific file inspection

```go
type PlatformExportInput struct {
    Key      string
    Platform ocispecs.Platform
    Ref      cache.ImmutableRef

    Config    dockerspec.DockerOCIImage
    BaseImage *dockerspec.DockerOCIImage

    ManifestAnnotations           map[string]string
    ManifestDescriptorAnnotations map[string]string
}

type ExportRequest struct {
    Platforms []PlatformExportInput

    IndexAnnotations           map[string]string
    IndexDescriptorAnnotations map[string]string

    // Raw inline cache payload keyed by platform key.
    // This is eagerly materialized by the outer caller if it survives at all.
    InlineCache map[string][]byte
}
```

```go
type WriterOpt struct {
    Snapshotter  snapshot.Snapshotter
    ContentStore content.Store
    Applier      diff.Applier
    Differ       diff.Comparer
}

type Writer struct {
    opt WriterOpt
}

func NewWriter(opt WriterOpt) (*Writer, error)

type ExportedImage struct {
    RootDesc ocispecs.Descriptor

    // Per-platform manifest/config descriptors emitted by this assembly step.
    Platforms []ExportedPlatform

    // Local content closure for manifest/config/layers already assembled in the engine content store.
    Provider content.InfoReaderProvider

    // Distribution/source annotations keyed by descriptor digest.
    // This is handed to push separately rather than encoded back into final manifest/config JSON.
    SourceAnnotations map[digest.Digest]map[string]string
}

type ExportedPlatform struct {
    Key          string
    Platform     ocispecs.Platform
    ManifestDesc ocispecs.Descriptor
    ConfigDesc   ocispecs.Descriptor
}

type CommitOpts struct {
    RefCfg           cacheconfig.RefConfig
    OCITypes         bool
    Epoch            *time.Time
    RewriteTimestamp bool
}

func (w *Writer) Assemble(
    ctx context.Context,
    req *ExportRequest,
    opts CommitOpts,
) (*ExportedImage, error)

func (w *Writer) commitPlatformManifest(
    ctx context.Context,
    input PlatformExportInput,
    chain *cache.ExportChain,
    inlineCache []byte,
    opts CommitOpts,
) (manifest ocispecs.Descriptor, config ocispecs.Descriptor, err error)
```

* `refCfg.Compression` is the one authoritative compression control surface in the new export path.
  * There should not be a second tarball-only compression knob below this layer.
  * When `refCfg.Compression.Force` is true, local export synthesis must recompress existing blobs to the requested type instead of only preferring that type for newly-created blobs.
* `ExportRequest.Platforms` must already be normalized and deterministically ordered before `Assemble(...)` runs.
  * There should not be a `Ref` vs `Refs` split in the canonical path.
  * There should not be any need to rediscover platform/config/base-config state from metadata keys.
* If inline cache survives at all, it survives here only as raw payload bytes in `ExportRequest.InlineCache`.
  * The canonical writer should not accept a callback that it has to invoke later.
* The canonical writer should not accept attestation input at all.
  * Attestation/provenance helper layers are already slated for deletion.
* `ExportedImage` is the single typed handoff from local image assembly into publish/store logic.
  * It is the replacement for pushing raw `provider + manager + root digest` through the canonical path.
  * It must carry everything publish/store needs:
    * final root descriptor
    * per-platform manifest/config descriptors needed for typed response shaping
    * local content provider
    * source/distribution annotations
* `Assemble(...)` should:
  * walk `req.Platforms` directly
  * call `ref.ExportChain(ctx, opts.RefCfg)` for each concrete ref
  * use `ExportChain.Layers` as the one source of truth for:
    * layer descriptors
    * per-layer history description
    * per-layer created time
  * build config/manifest/index blobs directly from the typed request fields
  * never construct or return `solver.Remote`
  * never parse `exptypes.ExporterPlatformsKey`, `ExporterImageConfigKey`, or other metadata keys

### engine/buildkit/imageexport/publish.go
#### Status
- [x] Add typed `Export(...)` push/store flows on top of `ExportedImage`.
- [x] Move caller image-store lifecycle work into this package.
- [x] Route registry push through the resolver-owned push seam.

#### Canonical in-house push / store flows
* Move the useful production logic from the old exporter packages here.
* This file should own:
  * registry push
  * image-store updates
* It should consume the typed writer APIs from `engine/buildkit/imageexport/writer.go`.
* It should not expose or depend on generic exporter-plugin lifecycles.
* It should not thread `sessionID string` through the call graph.
* This package should use one narrow session-scoped push seam rather than depending directly on resolver internals.
* `images.Store` is the correct abstraction for this boundary.
  * Just like host image import, this path is about updating named local image records.
  * It is not a reason to reintroduce `images.Store` into resolver or snapshot-import semantics.
* The canonical push/export path must also make distribution-source handling explicit.
  * Imported snapshots and export blobs must preserve the source annotations needed for cross-repo mount/source-label behavior.
  * The writer/publish split must say how those annotations are recovered from imported snapshot/export metadata, how they are excluded from final manifest/config JSON where appropriate, and how they are handed separately into the resolver-owned push path.
* The surviving saved layer/blob annotation allowlist for export/import should stay explicit and narrow:
  * `labels.LabelUncompressed`
  * `buildkit/createdat`
  * `compression.EStargzAnnotations`
* Distribution-source annotations remain a separate source-annotation bucket handed into push/export logic by digest.
* The image-store export path must also own the caller-store lifecycle work that is currently done manually in schema code:
  * create a flat temporary work lease on the destination/caller store before writing content there
  * ensure the exported descriptor closure is present in that destination content store under that lease
  * run `images.SetChildrenMappedLabels(..., images.ChildGCLabels)` over the exported root descriptor in that destination content store
  * only then create/update the destination `images.Image` record
  * release that temporary work lease explicitly on the normal path; expiry is only crash-safe fallback cleanup
* Canonical Dagger export does not support exporter-style `unpack`.
  * Remove `unpackImage(...)`.
  * Remove `OptKeyUnpack`.
  * Remove `unpack` from the typed export API entirely.
* Canonical Dagger export does not support `StoreAllowIncomplete`.
  * Remove `storeAllowIncomplete`.
  * Remove `unsafe-internal-store-allow-incomplete`.
  * The export path is local-only and should fail if it cannot produce the complete descriptor closure.
* Canonical Dagger export does not support attestation/SBOM supplementation.
  * There is no replacement for the old `Remote` / `FileList()`-based attestation/exporter helpers in this path.
  * If any attestation functionality survives later, it must be rebuilt around local mounts and typed local export data after the hard cut.

```go
type PushOpts struct {
    PushByDigest bool
    Insecure     bool
}

type RegistryPusher interface {
    PushImage(
        ctx context.Context,
        img *ExportedImage,
        ref string,
        opts PushOpts,
    ) error
}

type Deps struct {
    Images        images.Store
    ContentStore  content.Store
    LeaseManager  leases.Manager
    Writer        *Writer
    Pusher        RegistryPusher
}

type ExportOpts struct {
    Names          []string
    NameCanonical  bool
    DanglingPrefix string

    Push         bool
    PushByDigest bool
    Store        bool
    Insecure     bool

    Commit CommitOpts
}

type ExportResponse struct {
    RootDesc   ocispecs.Descriptor
    Platforms  []ExportedPlatform
    ImageNames []string
}

func Export(
    ctx context.Context,
    deps Deps,
    req *ExportRequest,
    opts ExportOpts,
) (*ExportResponse, error)
```

```go
type resolverPusher struct {
    resolver *serverresolver.Resolver
}

func (p resolverPusher) PushImage(
    ctx context.Context,
    img *ExportedImage,
    ref string,
    opts PushOpts,
) error {
    return p.resolver.PushImage(ctx, &serverresolver.PushedImage{
        RootDesc:          img.RootDesc,
        Provider:          img.Provider,
        SourceAnnotations: img.SourceAnnotations,
    }, ref, serverresolver.PushOpts{
        Insecure: opts.Insecure,
        ByDigest: opts.PushByDigest,
    })
}
```

* The canonical export workflow should therefore be:
  1. `imageexport/writer.go` assembles a fully local `ExportedImage` from a typed `ExportRequest`
  2. it preserves distribution/source annotations in `SourceAnnotations`
  3. it keeps those annotations out of final manifest/config JSON where appropriate
  4. `imageexport/publish.go` performs store/push operations directly from that typed `ExportedImage`
  5. registry push goes through `deps.Pusher.PushImage(...)`
  6. the session-owned resolver adapter performs the actual registry push with session auth
  7. `imageexport/publish.go` returns a typed `ExportResponse`

* `DescriptorReference` does not belong in the canonical path.
  * It is removed, not adapted.
* The old exporter response map does not belong in the canonical path.
  * Typed callers consume `ExportResponse` directly.
* The old exporter ABI does not survive as a Dagger-owned compatibility layer.
  * It is removed from Dagger production code, not hidden behind a shim.

```go
func exportToImageStore(
    ctx context.Context,
    deps Deps,
    imageName string,
    img *ExportedImage,
) error {
    leaseCtx, done, err := leaseutil.WithLease(ctx, deps.LeaseManager, leaseutil.MakeTemporary)
    if err != nil {
        return err
    }
    defer done(context.WithoutCancel(leaseCtx))

    if err := ensureDescriptorClosureInStore(leaseCtx, deps.ContentStore, img.RootDesc, img.Provider); err != nil {
        return err
    }

    handler := images.ChildrenHandler(deps.ContentStore)
    handler = images.SetChildrenMappedLabels(deps.ContentStore, handler, images.ChildGCLabels)
    if err := images.WalkNotEmpty(leaseCtx, handler, img.RootDesc); err != nil {
        return err
    }

    imageRecord := images.Image{
        Name:   imageName,
        Target: img.RootDesc,
    }
    if _, err := deps.Images.Update(leaseCtx, imageRecord); err != nil {
        if !errors.Is(err, cerrdefs.ErrNotFound) {
            return err
        }
        _, err = deps.Images.Create(leaseCtx, imageRecord)
        return err
    }
    return nil
}

func pushExportedImage(
    ctx context.Context,
    deps Deps,
    imageRef string,
    img *ExportedImage,
    opts ExportOpts,
) error {
    return deps.Pusher.PushImage(ctx, img, imageRef, PushOpts{
        PushByDigest: opts.PushByDigest,
        Insecure:     opts.Insecure,
    })
}
```

### engine/buildkit/exporter/exporter.go
#### Status
- [x] Remove the live Dagger path from the worker/exporter ABI and leave this package unused by production code.

#### Delete worker-facing exporter ABI from Dagger production paths
* Dagger production image export should not use the worker/exporter plugin ABI at all after this cut.
* The real image export logic lives in `engine/buildkit/imageexport`.
* If no remaining production call path requires this worker/exporter surface, delete this package.
* If some remaining path still depends on it, that dependency must be rewritten in the same hard-cut slice rather than hidden behind a compatibility wrapper.

```go
// Delete this surface from Dagger-owned production code rather than
// re-architecting around it.
```

### engine/buildkit/exporter/oci/export.go
#### Status
- [x] Delete the old engine-side OCI exporter implementation and leave only a dead shell package.

#### Delete this file from the Dagger-owned production path
* Dagger tarball export already hard-cuts to `WriteContainerImageTarball(...)`.
* Dagger store/export already hard-cuts to `engine/buildkit/imageexport`.
* There is no surviving reason for Dagger production code to keep this package.
* Delete it instead of turning it into a shim.

### engine/buildkit/exporter/containerimage/export.go
#### Status
- [x] Delete the old engine-side containerimage exporter implementation.

#### Delete this file from the Dagger-owned production path
* Dagger production image push/store export should not keep this package alive as a shim.
* The logic moves into `engine/buildkit/imageexport`.
* Delete this file from the Dagger-owned path instead of translating old exporter types into the new API.
* That means deleting Dagger-owned dependence on:
  * `*exporter.Source`
  * `exptypes.ExporterPlatformsKey`
  * `exptypes.ExporterImageConfigKey`
  * `exptypes.ExporterImageBaseConfigKey`
  * lazy `exptypes.InlineCache`
  * `exporter.DescriptorReference`
* If a remaining caller still depends on this file after the cut, that caller is not migrated and should block the cut rather than forcing a shim layer back into the design.

### engine/buildkit/exporter/containerimage/writer.go
#### Status
- [x] Delete the old engine-side containerimage writer implementation.

#### Delete this file from the Dagger-owned production path
* Move the real image assembly logic into `engine/buildkit/imageexport/writer.go`.
* Delete the public exporter-side writer API instead of carrying a second one:
  * `Commit(ctx, inp *exporter.Source, ...)`
  * `exportChains(...) ([]solver.Remote, error)`
  * `commitDistributionManifest(... remote *solver.Remote, ...)`
* `patchImageLayers(...)` and the actual manifest/config/index assembly logic belong only in the canonical writer.
* This file should not survive as a second architecture or as translation glue.

### internal/buildkit/exporter/containerimage/export.go
#### Legacy duplicate package: freeze and delete later
* This file is not the canonical live Dagger engine path anymore.
* The live engine already instantiates the `engine/buildkit/exporter/containerimage` fork.
* Do not port new logic here.
* Do not use this file as the target home for cleanup work.
* Once the remaining old internal exporter stack is deleted or replaced, delete this duplicate file rather than trying to keep it in sync.

### internal/buildkit/exporter/containerimage/writer.go
#### Legacy duplicate package: freeze and delete later
* Same story as `internal/buildkit/exporter/containerimage/export.go`.
* This is duplicate upstream/legacy BuildKit baggage, not the in-house production path we should build around.
* Do not port new logic here.
* Once the old internal exporter stack is gone, delete this file.

### engine/buildkit/exporter/containerimage/attestations.go
#### Status
- [x] Delete the old engine-side containerimage attestation helper implementation.

#### Delete attestation helper layer
* Delete this file entirely.
* Exporter-specific file/layer inspection for attestations should not keep the old session-shaped seam alive.
* If any attestation functionality survives later, it should be rebuilt around local mounts and local export data after the main hard cut.

### engine/buildkit/exporter/attestation/make.go
#### Delete attestation reader helpers
* Delete this file entirely.
* The current `session.Group`-based attestation read path should not survive the hard cut.

### engine/buildkit/exporter/attestation/unbundle.go
#### Delete attestation unbundle helpers
* Delete this file entirely.
* Bundled-attestation expansion should not keep the old mount/session/exporter coupling alive.

### internal/buildkit/util/pull/pull.go
#### Delete package and split responsibilities
* Delete this file and the package concept with it.
* Its current responsibilities split cleanly into:
  * registry/network/auth work
    * stays with `engine/server/resolver` on top of containerd remotes
  * snapshot creation from pulled image descriptors
    * moves into `engine/snapshots`
  * lazy remote-provider/session plumbing
    * is removed entirely
* We are not porting this package forward under a new name.
* We are not preserving its `SessionResolver`, `Provider func(session.Group) content.Provider`, retry wrapper, limiter, or progress assumptions.

### internal/buildkit/util/pull/pullprogress/progress.go
#### Delete dead progress plumbing
* Delete this file.
* Pull progress is dead code for the new architecture.
* DagQL telemetry is the authoritative progress/visibility mechanism now.

### internal/buildkit/source/containerimage/source.go
#### Invalidate old BuildKit source path
* This file is part of the old BuildKit source-vertex architecture.
* It should not drive the new design.
* As live Dagger callsites move to the session resolver plus snapshot-manager import path, this file should be deleted rather than adapted.

### internal/buildkit/source/containerimage/pull.go
#### Invalidate old BuildKit source puller
* This file is part of the same old BuildKit source-vertex architecture.
* It should not survive as a second copy of the image-pull pipeline.
* Once the surviving Dagger callsites are moved, delete this file rather than porting its wrapper logic forward.

### engine/snapshots/refs.go
#### Status
- [x] Make `Mount(...)` sessionless at the ref interface and implementation level.
- [x] Remove `GetRemotes(...)` / `LayerChain()` from the live export path in favor of `ExportChain(...)`.
- [x] Delete `Finalize(...)`.
- [x] Delete lazy-content / unlazy plumbing from refs.
- [x] Delete `Clone()` and the synthetic equal-ref graph machinery.
- [x] Narrow `Ref` so it no longer embeds `RefMetadata`.
- [x] Stop embedding `cacheRecord` in live ref handles and keep it as manager-internal state only.
- [x] Keep readonly view handling as a mount-time detail instead of ref-lifetime state.
- [x] Finish the explicit `ApplySnapshotDiff(...)` nil/no-op contract.

#### Ref model simplification
* Refs become thin lease-backed handles, not nodes in a second ownership graph.
* Remove `equalMutable`, `equalImmutable`, and every codepath that exists to support them.
* Delete `Finalize(...)`.
* Mutable means mutable until `Commit(...)`.
* `Commit(...)` is the only transition from mutable to immutable.
* `Commit(...)` should consume the mutable handle.
  * After commit succeeds, that mutable handle is invalid.
  * `Release(...)`, `Commit(...)`, or `Mount(...)` on the consumed mutable should fail with `errInvalid`.
  * Callers that need an immutable after commit must use the returned immutable handle only.
* Remove generic merge/diff parent graph modeling from refs:
  * `parentRefs`
  * `diffParents`
  * `mergeParents`
  * `refKind`
* Remove lazy-content and progress plumbing from refs:
  * `Extract(...)`
  * `FileList(...)`
  * `ensureLocalContentBlob(...)`
  * `unlazy(...)`
  * `unlazyLayer(...)`
  * `unlazyDiffMerge(...)`
* Keep real mount management here, including sharable mutable mount handling.
* Readonly views may still exist as a mount-time containerd implementation detail, but not as a lifetime/identity state on the ref.
* Delete the in-memory `refs` handle set and snapshot-manager refcounting.
* Delete `Clone()`.
* Opening another handle to the same snapshot should create another lease on the same underlying snapshot resource, not bump an in-memory count.
* Keep `GetMutableBySnapshotID(...)`.
* Keep `GetBySnapshotID(...)`.
* Keep `Mount(...)`, `Commit(...)`, `Release(...)`, and `Size(...)`.
* Delete `IdentityMapping()` from the snapshot package surface.
* There is no replacement parent-ref graph.
* The only surviving ancestry is real containerd snapshot parentage.
* If code needs ancestry after the cut, it should walk `Snapshotter.Stat(...)` on snapshot IDs, not retain `immutableRef` parents.
* `Commit(...)` should not copy parent object refs into the committed record.
* It should record only scalar metadata about the newly committed snapshot and return a new plain handle to it.

```go
type Ref interface {
    Mount(ctx context.Context, readonly bool) (snapshot.Mountable, error)
    ID() string // exactly the same value as SnapshotID()
    SnapshotID() string
    Release(context.Context) error
    Size(context.Context) (int64, error)
}

type ImmutableRef interface {
    Ref
}

type MutableRef interface {
    Ref
    Commit(context.Context) (ImmutableRef, error)
    InvalidateSize(context.Context) error
}
```

* The ref model should mirror the real underlying lifecycle instead of maintaining synthetic equality/finalization states.
* Handle release should only:
  * drop any mount cache owned by that handle
  * delete that handle's lease
* It should not walk parent refs or maintain object liveness.
* Ordinary `Release(...)` should use normal async lease deletion.
  * Do not use `leases.SynchronousDelete` here.
  * Synchronous delete is reserved for explicit prune/immediate-cleanup semantics outside ordinary ref release.
* Mutable `Release(...)` should not branch on cache policy anymore.
* The current behavior:
  * "if retained, leave the mutable snapshot around"
  * "if not retained, remove it"
  should be deleted entirely.
* The new rule is:
  * release always drops the handle
  * release always deletes that handle's lease
  * record cleanup is in-memory bookkeeping only
  * whether the underlying snapshot survives is determined entirely by whatever other leases still exist
* There is no longer any supported pattern of:
  * commit mutable
  * then release the same mutable
  * or keep using it for later mount/commit operations

```go
type immutableRef struct {
    cm         *snapshotManager
    snapshotID string
    leaseID    string
    md         *cacheMetadata
    mountCache snapshot.Mountable
}

type mutableRef struct {
    cm         *snapshotManager
    snapshotID string
    leaseID    string
    md         *cacheMetadata
    mountCache snapshot.Mountable
}

func (sr *immutableRef) Release(ctx context.Context) error {
    if sr.mountCache != nil {
        sr.mountCache = nil
    }
    return sr.cm.LeaseManager.Delete(ctx, leases.Lease{ID: sr.leaseID})
}

func (sr *mutableRef) Release(ctx context.Context) error {
    if sr.mountCache != nil {
        sr.mountCache = nil
    }
    return sr.cm.LeaseManager.Delete(ctx, leases.Lease{ID: sr.leaseID})
}

func (sr *mutableRef) Commit(ctx context.Context) (ImmutableRef, error) {
    immutableSnapshotID := identity.NewID()
    if err := sr.cm.Snapshotter.Commit(ctx, immutableSnapshotID, sr.snapshotID); err != nil {
        return nil, err
    }
    md := sr.cm.ensureMetadata(immutableSnapshotID)
    if err := md.queueSnapshotID(immutableSnapshotID); err != nil {
        return nil, err
    }
    if err := md.queueCommitted(true); err != nil {
        return nil, err
    }
    if err := md.commitMetadata(); err != nil {
        return nil, err
    }
    ref, err := sr.cm.GetBySnapshotID(ctx, immutableSnapshotID)
    if err != nil {
        return nil, err
    }

    // Commit consumes the mutable handle.
    if sr.mountCache != nil {
        sr.mountCache = nil
    }
    if err := sr.cm.LeaseManager.Delete(ctx, leases.Lease{ID: sr.leaseID}); err != nil && !cerrdefs.IsNotFound(err) {
        return nil, err
    }
    sr.leaseID = ""
    sr.mutable = false

    return ref, nil
}
```

* `parentRefs.release(...)` being a no-op in the current code is the clearest sign that snapshot-manager parent liveness is already the wrong model.
* The replacement for current parent-ref behavior is:
  * dagql result deps for object liveness
  * deterministic owner leases for dagql-owned snapshot ownership
  * real snapshot parentage only for filesystem ancestry and owner-lease content-closure walks

### engine/snapshots/metadata.go
#### Metadata collapse
* Delete metadata keys and indexes that only exist for dead concepts.
* Remove:
  * `keyEqualMutable`
  * merge/diff parent metadata
  * blob-chain / chain indexes
  * any keys used only for lazy fetch or remote unlazying
  * any metadata whose only purpose is snapshot-manager-owned liveness bookkeeping
* Keep only the metadata that still serves the simplified model:
  * description
  * created time / last used if still needed
  * record type
  * image refs if still needed
  * concrete snapshot identity / content metadata required by surviving export code paths
* The metadata store survives in a much smaller form.
* Dagger-specific durable snapshot metadata does **not** live here.
  * Do not store imported-layer reuse indexes here.
  * Do not store direct `snapshotID -> content digest` associations here.
  * Do not use containerd labels as a second user-space metadata database for that data.
* Those restart-relevant Dagger metadata sets live in SQLite through dagql persistence and are hydrated into runtime indexes on startup.
* Delete cache-policy metadata and helpers entirely:
  * `keyCachePolicy`
  * `HasCachePolicyDefault()`
  * `SetCachePolicyDefault()`
  * `HasCachePolicyRetain()`
  * `SetCachePolicyRetain()`
  * `CachePolicyDefault(...)`
  * `CachePolicyRetain(...)`
* There is no replacement metadata knob for lifetime.
* Lifetime is represented only by leases plus dagql ownership.

* Export-facing content metadata that survives the cut should be limited to what `GetRemotes(...)` still needs:
  * media type
  * URLs
  * uncompressed digest annotation (`labels.LabelUncompressed`)
  * saved export-relevant annotations
* This is the replacement for today’s desc-handler fallback path.
* Content ownership metadata should exist in memory during runtime in the simplest possible direct form:
  * for each snapshot ID, record the direct associated content digests it owns
  * do not keep providers there
  * do not keep session-shaped fetch callbacks there
  * do not keep a second ownership graph there
* On dagql cache close, the persistence worker mirrors that in-memory metadata to SQLite.
* On startup import, that SQLite state is hydrated back into the in-memory snapshot-manager indexes.
* `AttachLease(...)` should use that SQLite-backed direct mapping together with real snapshot ancestry to reattach the full content closure after restart.
* Live backfill is a separate same-process rule:
  * when `recordSnapshotContent(...)` adds a new digest to a snapshot that already has live owner leases
  * it must immediately backfill those live owner leases
  * the preferred implementation is to re-run idempotent `AttachLease(...)` for each currently attached owner lease of that snapshot
* The intended pattern is:
  * each imported layer snapshot records its own layer blob digest directly
  * the final imported image root snapshot records manifest/config/nonlayer digests directly
  * export-generated blobs and compression variants record their digests directly on the snapshot they belong to
* Then owner-lease binding to the top snapshot can recover everything it needs by:
  * attaching direct content on the top snapshot
  * walking parent snapshots and attaching each ancestor snapshot's direct content
* This is also the `FROM scratch` story:
  * there may be no layer ancestry at all
  * `ImportImage(...)` still materializes a concrete empty top snapshot for that image
  * manifest/config/nonlayer content is directly associated and reattachable on that empty top snapshot

* The replacement for the old blobchain search is a much narrower image-import index:
  * parent snapshot identity
  * layer blob digest
  * plus the fallback diffID key for cross-compression reuse
* During normal runtime this index lives only in memory.
* On dagql cache close it is mirrored to SQLite.
* On startup import it is hydrated back into the in-memory snapshot-manager maps.
* That is enough to preserve shared image-layer-prefix reuse without preserving the old `GetByBlob(...)` architecture.
* Metadata is not a liveness system.
* It is lookup, accounting, and export/import support only.
* Persistable mutable owner objects reopen by their own persisted snapshot links, not through a second snapshot-manager identity index.
* Snapshot->content metadata is also not an ownership graph.
* It is just the persisted direct association needed so `AttachLease(...)` can rebind content ownership after restart.

### internal/buildkit/snapshot/merge.go
#### Preserve the real diff-apply primitive only
* Keep the low-level snapshotter merge/diff-apply implementation in this file.
* This is the real behavior we still want:
  * `mergeSnapshotter.Merge(...)`
  * `diffApply(...)`
  * overlay/hardlink/base-layer optimizations
  * correct whiteout and usage handling
* The hard cut is above this layer:
  * do not preserve snapshot-manager merge/diff ref kinds
  * do not preserve lazy merge/diff refs
  * do not preserve ancestor arithmetic or merge flattening in the snapshot manager
* `ApplySnapshotDiff(...)` in `engine/snapshots/manager.go` should bottom out directly in `Snapshotter.Merge(ctx, snapshotID, diffs)`.
* There is no corresponding generic merge operation in the new snapshot-manager API.
* `WithDirectory(...)` should rely on eager copy plus hardlink optimization instead of routing through this file.

### dagql/cache.go
#### Status
- [x] Add `snapshotManager` to `Cache` and thread it through `NewCache(...)`.
- [x] Keep cache startup as a one-step constructor that performs persisted import plus startup owner-lease sync.
- [x] Install deterministic snapshot-owner lease cleanup on imported and newly materialized results.
- [x] Rename the in-memory direct snapshot-owner link state to match its authoritative runtime role.

#### Authoritative ordinary liveness
* Dagql is the authoritative liveness system for ordinary snapshot-backed values.
* Keep:
  * session roots
  * explicit child dependencies
  * persisted roots
  * `incomingOwnershipCount`
  * `onRelease`
* Do not move snapshot-manager parent/ref ownership here, because it already exists here in the right form.
* The key hard cut is that snapshot-backed object lifetime should only flow through:
  * dagql result/session/persisted edges
  * object-level `AttachDependencyResults(...)`
  * object-level `OnRelease(...)`
* When a dagql result becomes unowned, `onRelease` should:
  * delete the deterministic owner leases for that result's snapshot links
  * close the snapshot handles held by the typed object, if any
* There should be no second hidden liveness system in the snapshot manager to keep the same thing alive again.
* `SessionResourceHandle` remains for opaque caller/session capabilities like secrets and sockets.
* It should not become the ownership mechanism for running services or terminal runtime.
* Live runtime state belongs to explicit session-owned managers like `Services`, not to dagql session-resource bindings.
* Do not add a second snapshot-retention closure algorithm here.
* Dagql already owns result closure through `deps` and `incomingOwnershipCount`.
* Each result should just own its own direct snapshot leases.
* If result `A` depends on result `B`, then `B` stays live through normal dagql dependency ownership, and `B` keeps its own leases alive for as long as it stays live.
* SQLite persistence does not drive lease retention.
  * It mirrors the dagql graph and the Dagger-specific snapshot metadata we need across restart.
  * It does not itself keep snapshots/content alive.
* The runtime in-memory dagql graph is what drives owner leases.
* This is also the replacement for old ordinary-result `CachePolicyRetain` usage.
* Ordinary live or persisted results should never depend on snapshot metadata policy to survive release.
* If an ordinary result needs its snapshot to survive, it must be because:
  * the result is still owned by dagql
  * its deterministic result-owner lease is still attached
* Cache startup should stay a one-step constructor, not a second startup orchestration layer.
  * `dagql.NewCache(...)` should take the snapshot manager directly.
  * It should return either:
    * a fully initialized cache with persisted state imported, snapshot metadata hydrated, and startup owner-lease sync completed
    * or a cold-started empty cache after the existing wipe-and-retry behavior
  * There should be no extra `InitializePersistentState(...)` phase and no extra startup hook abstraction.

```go
func NewCache(
    ctx context.Context,
    dbPath string,
    snapshotManager bkcache.SnapshotManager,
) (*Cache, error)

type Cache struct {
    ...
    snapshotManager bkcache.SnapshotManager
    ...
}

type sharedResult struct {
    onRelease OnReleaseFunc

    // exact child-result ownership edges
    deps map[sharedResultID]struct{}

    // authoritative ordinary liveness count
    incomingOwnershipCount int64

    // direct snapshot links materially owned by this result; not child-result deps
    snapshotOwnerLinks []PersistedSnapshotRefLink
}

func (c *Cache) AddExplicitDependency(
    ctx context.Context,
    parent AnyResult,
    dep AnyResult,
    reason string,
) error

func (c *Cache) ReleaseSession(ctx context.Context, sessionID string) error

func resultSnapshotLeaseID(resultID sharedResultID, role, slot string) string

func joinOnRelease(a, b OnReleaseFunc) OnReleaseFunc

func (c *Cache) authoritativeSnapshotLinksForResult(res *sharedResult) ([]PersistedSnapshotRefLink, bool)
func (c *Cache) syncResultSnapshotLeases(ctx context.Context, res *sharedResult) error
func (c *Cache) desiredImportedOwnerLeaseIDs(ctx context.Context) (map[string]struct{}, error)
```

* Ordinary snapshot-backed types must continue implementing:
  * `dagql.OnReleaser`
  * `dagql.HasDependencyResults`
* If an object needs another object's snapshot to stay alive, that relationship must be expressed as an object dependency here, not hidden in snapshot-manager parent refs.
* Fresh materialized results should sync owner leases from the direct links exposed by `PersistedSnapshotRefLinks()` on the typed self.
* Because some results only gain a snapshot during lazy evaluation, the lease-sync path must run both:
  * when a completed result is first published with direct snapshot links
  * after lazy evaluation succeeds and materializes new direct snapshot links
* The authoritative source of direct owner links is:
  * imported `result_snapshot_links` before typed decode/materialization
  * typed self `PersistedSnapshotRefLinks()` after decode/materialization
* Imported snapshot links are bootstrap state only.
  * They exist so imported results can release their owned snapshots before they are ever decoded.
  * Once typed self exists and exposes direct links, typed self becomes authoritative.
* Lease IDs should be deterministic and owner-shaped, not snapshot-shaped:

```go
func resultSnapshotLeaseID(resultID sharedResultID, role, slot string) string {
    if slot == "" {
        return fmt.Sprintf("dagql/result/%d/%s", resultID, url.PathEscape(role))
    }
    return fmt.Sprintf(
        "dagql/result/%d/%s/%s",
        resultID,
        url.PathEscape(role),
        url.PathEscape(slot),
    )
}

type snapshotOwnerKey struct {
    Role string
    Slot string
}

func (c *Cache) authoritativeSnapshotLinksForResult(res *sharedResult) ([]PersistedSnapshotRefLink, bool) {
    if res == nil {
        return nil, false
    }

    state := res.loadPayloadState()
    if state.hasValue && state.self != nil {
        if links := persistedSnapshotLinksFromTyped(state.self); len(links) > 0 {
            return links, true
        }
    }

    res.payloadMu.Lock()
    defer res.payloadMu.Unlock()
    if len(res.snapshotOwnerLinks) == 0 {
        return nil, false
    }
    return append([]PersistedSnapshotRefLink(nil), res.snapshotOwnerLinks...), true
}

func (c *Cache) syncResultSnapshotLeases(ctx context.Context, res *sharedResult) error {
    if c == nil || c.snapshotManager == nil || res == nil || res.id == 0 {
        return nil
    }

    links, ok := c.authoritativeSnapshotLinksForResult(res)
    if !ok {
        return nil
    }

    res.payloadMu.Lock()
    oldLinks := append([]PersistedSnapshotRefLink(nil), res.snapshotOwnerLinks...)
    res.payloadMu.Unlock()

    oldByKey := map[snapshotOwnerKey]PersistedSnapshotRefLink{}
    newByKey := map[snapshotOwnerKey]PersistedSnapshotRefLink{}

    for _, link := range oldLinks {
        oldByKey[snapshotOwnerKey{Role: link.Role, Slot: link.Slot}] = link
    }
    for _, link := range links {
        newByKey[snapshotOwnerKey{Role: link.Role, Slot: link.Slot}] = link
    }

    for key, oldLink := range oldByKey {
        newLink, ok := newByKey[key]
        if !ok || newLink.RefKey != oldLink.RefKey {
            if err := c.snapshotManager.RemoveLease(
                ctx,
                resultSnapshotLeaseID(res.id, key.Role, key.Slot),
            ); err != nil {
                return err
            }
        }
    }

    for key, newLink := range newByKey {
        oldLink, ok := oldByKey[key]
        if !ok || oldLink.RefKey != newLink.RefKey {
            if err := c.snapshotManager.AttachLease(
                ctx,
                resultSnapshotLeaseID(res.id, key.Role, key.Slot),
                newLink.RefKey,
            ); err != nil {
                return err
            }
        }
    }

    res.payloadMu.Lock()
    res.snapshotOwnerLinks = append([]PersistedSnapshotRefLink(nil), links...)
    res.payloadMu.Unlock()

    return nil
}
```

* Owner leases are per-result, not per-snapshot.
* That is what makes shared snapshots safe without a second refcounting layer: if two results own the same snapshot, they each have their own lease, and deleting one lease does not affect the other owner.
* Lease sync uses replace-with-diff semantics, not seed-once semantics and not union semantics.
  * same `(role, slot)` with different `RefKey` is a replacement:
    * remove the old deterministic owner lease
    * attach the new snapshot under the same deterministic lease ID
* This makes the direct links mean “what this result directly owns right now”, not “everything it has ever owned”.

### dagql/cache_persistence_worker.go
#### Status
- [x] Mirror snapshot metadata rows from the snapshot manager into SQLite on cache close.
- [x] Keep `result_snapshot_links` and snapshot metadata mirroring runtime-only; do not drive lease ownership from the worker.

#### Snapshot-link mirror plus Dagger snapshot metadata
* This file should continue mirroring dagql state into SQLite during cache close.
* It should not drive snapshot lease ownership.
* Snapshot lease ownership is runtime behavior attached directly to result lifetime in [dagql/cache.go](/home/sipsma/repo/github.com/sipsma/dagger/dagql/cache.go).
* The persistence worker's job here is only:
  * mirror persisted-root edges
  * mirror direct per-result snapshot links
  * mirror Dagger-specific snapshot metadata that containerd does not already own intrinsically:
    * direct `snapshotID -> content digest` associations
    * imported-layer blob index rows
    * imported-layer diffID index rows
  * mirror result deps and e-graph state
* Keep `result_snapshot_links` as a mirror of each result's direct owned snapshot links.
* Do not duplicate intrinsic containerd resource state here:
  * not snapshots themselves
  * not snapshot ancestry
  * not content existence/info
  * not lease-resource attachments
* Do not add retained-lease reconciliation logic here.
* The SQLite tables for the Dagger-specific snapshot metadata should be explicit and narrow:
  * `result_snapshot_links`
  * `snapshot_content_links`
  * `imported_layer_blob_index`
  * `imported_layer_diff_index`

```go
func (c *Cache) snapshotOwnerLinksForResultLocked(res *sharedResult) []PersistedSnapshotRefLink

func (c *Cache) snapshotPersistentMetadataRowsLocked() SnapshotPersistentMetadataRows

type SnapshotContentRow struct {
    SnapshotID string
    Digest     digest.Digest
}

type ImportedLayerBlobRow struct {
    ParentSnapshotID string
    BlobDigest       digest.Digest
    SnapshotID       string
}

type ImportedLayerDiffRow struct {
    ParentSnapshotID string
    DiffID           digest.Digest
    SnapshotID       string
}
```

* If a fully materialized result has direct snapshot-owning self state, this mirror should prefer those direct links.
* Imported persisted rows should restore those links into the in-memory shared result as bootstrap ownership state so runtime cleanup can use them later even before the typed object is decoded.
* The persistence worker should gather the Dagger-specific snapshot metadata from the snapshot manager's in-memory maps during cache close.
  * It should not ask the snapshot manager to synchronously persist anything during normal runtime.

### dagql/cache_persistence_import.go
#### Status
- [x] Load snapshot metadata rows from SQLite during startup import.
- [x] Hydrate snapshot-manager runtime indexes before startup owner-lease sync.
- [x] Restore imported result snapshot links and chain generic lease cleanup onto imported results immediately.

#### Imported result lease cleanup
* Imported persisted rows should restore `result_snapshot_links` into the in-memory shared result as direct owner links.
* Imported persisted rows should install generic lease cleanup immediately, even before typed self payload decode.
* This is needed because imported results can be released before they are ever decoded into a typed object with its own `OnRelease(...)`.
* That generic cleanup should only delete the deterministic owner leases for the imported result's direct links.
* It should not reopen snapshots, and it should not try to infer dependency closure.
* After a persisted hit is decoded into a typed object:
  * restore object-specific `OnRelease(...)`
  * keep the generic lease cleanup chained with it
  * run `syncResultSnapshotLeases(...)` so typed self can replace bootstrap links if needed
* On startup import, if we perform a consistency check that each imported `result_snapshot_link` still resolves via `GetBySnapshotID(...)`, that check should be mandatory and fail hard on mismatch.
* If that check fails, `dagql.NewCache(...)` should fail its startup import, causing the existing wipe-and-cold-start recovery path to run.
* Startup import stays inside `dagql.NewCache(...)`.
  * `dagql.NewCache(...)` already has the persistence DB open.
  * It now also has the snapshot manager directly.
  * So the startup import path should do the full job there instead of inventing a second startup phase.
* Startup import should also load the persisted Dagger-specific snapshot metadata rows from SQLite and hydrate them into the snapshot manager before startup owner-lease sync runs.
* That means:
  * result/object ownership intent comes from dagql persistence
  * snapshot-content associations and imported-layer reuse indexes also come from SQLite
  * containerd still remains authoritative for intrinsic resources and actual lease attachments
  * startup then idempotently syncs desired deterministic owner leases onto containerd
* Startup owner-lease sync should:
  * compute the set of desired Dagger-owned owner lease IDs from imported dagql state
  * idempotently `AttachLease(...)` all desired owner leases
  * delete stale Dagger-owned owner leases that are no longer desired
* Startup should not assume old Dagger-owned owner leases are correct just because containerd still has them.
  * Because containerd is trusted and internal, this stays a narrow Dagger-owned lease sync, not a general repair pass.

```go
for _, row := range resultSnapshotRows {
    resultID := sharedResultID(row.ResultID)
    res := c.resultsByID[resultID]
    if res == nil {
        return fmt.Errorf("import result_snapshot_link: missing result %d", row.ResultID)
    }
    res.snapshotOwnerLinks = append(res.snapshotOwnerLinks, PersistedSnapshotRefLink{
        RefKey: row.RefKey,
        Role:   row.Role,
        Slot:   row.Slot,
    })
}

for _, res := range c.resultsByID {
    res.onRelease = joinOnRelease(res.onRelease, c.resultSnapshotLeaseCleanup(res))
}
```

```go
func NewCache(
    ctx context.Context,
    dbPath string,
    snapshotManager bkcache.SnapshotManager,
) (*Cache, error) {
    c := &Cache{
        traceBootID:     newTraceBootID(),
        snapshotManager: snapshotManager,
    }

    ...

    if err := c.importPersistedState(ctx); err != nil {
        c.tracePersistStoreWipedImportFailure(ctx, err)
        slog.Warn("dagql persistence import failed; wiping and cold-starting", "err", err)
        ...
    }

    return c, nil
}
```

```go
if err := c.snapshotManager.LoadPersistentMetadata(SnapshotPersistentMetadataRows{
    SnapshotContent: snapshotContentRows,
    ImportedByBlob:  importedLayerBlobRows,
    ImportedByDiff:  importedLayerDiffRows,
}); err != nil {
    return err
}
```

```go
desiredLeaseIDs, err := c.desiredImportedOwnerLeaseIDs(ctx)
if err != nil {
    return err
}
if err := c.syncAllImportedOwnerLeases(ctx); err != nil {
    return err
}
if err := c.snapshotManager.DeleteStaleDaggerOwnerLeases(ctx, desiredLeaseIDs); err != nil {
    return err
}
```

```go
if onReleaser, ok := UnwrapAs[OnReleaser](decoded); ok {
    res.onRelease = joinOnRelease(onReleaser.OnRelease, c.resultSnapshotLeaseCleanup(res))
}
if err := c.syncResultSnapshotLeases(ctx, res); err != nil {
    return nil, err
}
```

### core/directory.go
#### Status
- [x] Make `NewDirectoryWithSnapshot(...)` adopt ownership instead of cloning.
- [x] Sweep immediate handoff callsites and local temporary `Directory` wrappers for the new ownership contract.
- [x] Hard-cut `WithDirectory(...)` to the eager child-snapshot copy path with no merge branches.
- [x] Cut `Directory.Diff(...)` over to `ApplySnapshotDiff(...)` with no wrapper snapshot layer.

#### Directory-owned snapshot handles
* `Directory` already sits on the right side of the boundary:
  * it owns its snapshot handle directly
  * it releases it in `OnRelease(...)`
  * it attaches upstream object dependencies in `AttachDependencyResults(...)`
* Keep that model and make it the template for other ordinary snapshot-backed types.
* `NewDirectoryWithSnapshot(...)` should adopt ownership of the provided snapshot handle.
* It should not clone the handle internally.
* Ownership transfers only when `NewDirectoryWithSnapshot(...)` returns success.
* If the constructor returns an error, the caller still owns the original handle and must release it.
* If a caller wants a second owner for the same snapshot, it must explicitly open a second handle by snapshot ID before constructing the `Directory`.
* Callers that successfully construct a `Directory` and then hit a later error before returning/attaching it must release that `Directory` object explicitly.
* Internal `Directory` methods that currently use `Clone()` on a snapshot handle should instead:
  * reopen by snapshot ID when they need a second owner, or
  * transfer ownership directly when replacing the current snapshot
* The concrete clone-to-reopen sweep in this file includes:
  * `applyPatchToSnapshot(...)` empty-patch fast path
  * no-op change application paths that currently do `currentSnapshot.Clone()`
* `WithDirectory(...)` should be hard-cut simplified:
  * delete the direct merge optimization branch entirely
  * delete the fallback "copy to scratch then merge" branch entirely
  * always create a new mutable child snapshot on top of the destination snapshot
  * always run the copy logic directly into that child snapshot
  * keep `EnableHardlinkOptimization: true` in the copy step
  * commit and set the resulting snapshot directly
* This keeps the practical optimization we actually care about:
  * source files can still be hardlinked into the new snapshot when safe
* And it deletes the merge-specific complexity we no longer want:
  * `canDoDirectMerge`
  * merge refs
  * merge finalization
  * merge-specific cache behavior
  * "copy to scratch then merge" as a special architecture
* `Directory.Diff(...)` should keep using the underlying snapshotter diff-apply path, but only through the new narrow `SnapshotManager().ApplySnapshotDiff(...)` primitive.
* `Directory.Diff(...)` should stop creating a diff ref plus an extra wrapper snapshot.
* Schema already rebases both sides to `/` before constructing `DirectoryDiffLazy`, so `Directory.Diff(...)` should strengthen that assumption and work directly on root-level snapshot IDs.
* `Directory.Diff(...)` should also make the nil/no-op cases explicit and test-backed:
  * scratch-vs-upper returns the upper content directly
  * equivalent directories produce an empty root-level diff
  * no extra wrapper snapshot should be created just to normalize those cases
* Drop the old diff cleverness entirely:
  * no ancestor arithmetic
  * no diff-as-merge fast path
  * no lazy diff refs
  * no extra diff metadata graph

```go
func NewDirectoryWithSnapshot(
    dir string,
    platform Platform,
    services ServiceBindings,
    snapshot bkcache.ImmutableRef,
) (*Directory, error) {
    if snapshot == nil {
        return nil, fmt.Errorf("new directory with snapshot: nil snapshot")
    }
    dirInst := &Directory{
        Dir:      dir,
        Platform: platform,
        Services: slices.Clone(services),
    }
    if err := dirInst.setSnapshot(snapshot); err != nil {
        return nil, err
    }
    return dirInst, nil
}

func (dir *Directory) OnRelease(ctx context.Context) error {
    if dir.Snapshot != nil {
        return dir.Snapshot.Release(ctx)
    }
    return nil
}
```

```go
if len(patch) == 0 {
    if parentRef != nil {
        query, err := CurrentQuery(ctx)
        if err != nil {
            return nil, err
        }
        return query.SnapshotManager().GetBySnapshotID(ctx, parentRef.SnapshotID(), bkcache.NoUpdateLastUsed)
    }
    return nil, nil
}
```

```go
func (dir *Directory) WithDirectory(
    ctx context.Context,
    parent dagql.ObjectResult[*Directory],
    destDir string,
    src dagql.ObjectResult[*Directory],
    filter CopyFilter,
    owner string,
    permissions *int,
    doNotCreateDestPath bool,
    attemptUnpackDockerCompatibility bool,
    requiredSourcePath string,
    destPathHintIsDirectory bool,
    copySourcePathContentsWhenDir bool,
) error {
    dagqlCache, err := dagql.EngineCache(ctx)
    if err != nil {
        return err
    }
    if err := dagqlCache.Evaluate(ctx, parent, src); err != nil {
        return err
    }

    dirRef, err := parent.Self().getSnapshot()
    if err != nil {
        return fmt.Errorf("failed to get directory ref: %w", err)
    }
    query, err := CurrentQuery(ctx)
    if err != nil {
        return fmt.Errorf("failed to get current query: %w", err)
    }

    destDir = path.Join(dir.Dir, destDir)
    if doNotCreateDestPath {
        if err := ensureCopyDestParentExists(ctx, dirRef, destDir); err != nil {
            return err
        }
    }

    srcRef, err := src.Self().getSnapshot()
    if err != nil {
        return fmt.Errorf("failed to get source directory ref: %w", err)
    }
    if requiredSourcePath != "" {
        if err := ensureRequiredCopySourcePathExists(ctx, srcRef, src.Self().Dir, requiredSourcePath); err != nil {
            return err
        }
    }

    newRef, err := query.SnapshotManager().New(
        ctx,
        dirRef,
        bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
        bkcache.WithDescription("Directory.withDirectory"),
    )
    if err != nil {
        return err
    }
    defer newRef.Release(context.WithoutCancel(ctx))

    err = MountRef(ctx, newRef, nil, func(copyDest string, destMnt *mount.Mount) error {
        resolvedCopyDest, err := containerdfs.RootPath(copyDest, destDir)
        if err != nil {
            return err
        }
        if srcRef == nil {
            if err := os.MkdirAll(resolvedCopyDest, 0755); err != nil {
                return err
            }
            if permissions != nil {
                if err := os.Chmod(resolvedCopyDest, os.FileMode(*permissions)); err != nil {
                    return err
                }
            }
            if owner != "" {
                ownership, err := parseDirectoryOwner(owner)
                if err != nil {
                    return err
                }
                if err := os.Chown(resolvedCopyDest, ownership.UID, ownership.GID); err != nil {
                    return err
                }
            }
            return nil
        }

        mounter, err := srcRef.Mount(ctx, true)
        if err != nil {
            return err
        }
        ms, unmountSrc, err := mounter.Mount()
        if err != nil {
            return err
        }
        defer unmountSrc()

        srcMnt := ms[0]
        lm := snapshot.LocalMounterWithMounts(ms)
        mntedSrcPath, err := lm.Mount()
        if err != nil {
            return err
        }
        defer lm.Unmount()

        resolvedSrcPath, err := containerdfs.RootPath(mntedSrcPath, src.Self().Dir)
        if err != nil {
            return err
        }
        srcResolver, err := pathResolverForMount(&srcMnt, mntedSrcPath)
        if err != nil {
            return err
        }
        destResolver, err := pathResolverForMount(destMnt, copyDest)
        if err != nil {
            return err
        }

        var ownership *Ownership
        if owner != "" {
            ownership, err = parseDirectoryOwner(owner)
            if err != nil {
                return err
            }
        }

        var opts []fscopy.Opt
        opts = append(opts, fscopy.WithCopyInfo(fscopy.CopyInfo{
            AlwaysReplaceExistingDestPaths: true,
            CopyDirContents:                true,
            EnableHardlinkOptimization:     true,
            SourcePathResolver:             srcResolver,
            DestPathResolver:               destResolver,
            Mode:                           permissions,
        }))
        ...
        return fscopy.Copy(ctx, effectiveSrcPath, ".", resolvedCopyDest, ".", opts...)
    })
    if err != nil {
        return err
    }

    snap, err := newRef.Commit(ctx)
    if err != nil {
        return err
    }
    return dir.setSnapshot(snap)
}

func (dir *Directory) Diff(
    ctx context.Context,
    parent dagql.ObjectResult[*Directory],
    other dagql.ObjectResult[*Directory],
) error {
    dagqlCache, err := dagql.EngineCache(ctx)
    if err != nil {
        return err
    }
    if err := dagqlCache.Evaluate(ctx, parent, other); err != nil {
        return err
    }

    thisDirRef, err := parent.Self().getSnapshot()
    if err != nil {
        return fmt.Errorf("failed to get directory ref: %w", err)
    }
    otherDirRef, err := other.Self().getSnapshot()
    if err != nil {
        return fmt.Errorf("failed to get other directory ref: %w", err)
    }

    thisDirPath := dir.Dir
    if thisDirPath == "" {
        thisDirPath = "/"
    }
    otherDirPath := other.Self().Dir
    if otherDirPath == "" {
        otherDirPath = "/"
    }
    if thisDirPath != "/" || otherDirPath != "/" {
        return fmt.Errorf("internal error: Directory.diff expects rebased root dirs, got %q and %q", thisDirPath, otherDirPath)
    }

    query, err := CurrentQuery(ctx)
    if err != nil {
        return err
    }

    var snapshot bkcache.ImmutableRef
    switch {
    case thisDirRef == nil && otherDirRef == nil:
        snapshot = nil
    case thisDirRef == nil:
        snapshot, err = query.SnapshotManager().GetBySnapshotID(
            ctx,
            otherDirRef.SnapshotID(),
            bkcache.NoUpdateLastUsed,
        )
    default:
        snapshot, err = query.SnapshotManager().ApplySnapshotDiff(
            ctx,
            thisDirRef,
            otherDirRef,
            bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
            bkcache.WithDescription("directory diff"),
        )
    }
    if err != nil {
        return fmt.Errorf("failed to diff directories: %w", err)
    }

    return dir.setSnapshot(snapshot)
}
```

### core/file.go
#### Status
- [x] Make `NewFileWithSnapshot(...)` adopt ownership instead of cloning.
- [x] Sweep immediate handoff callsites and local temporary `File` wrappers for the new ownership contract.
- [x] Reopen a second immutable handle for `WithReplaced(...)` temporary source wrappers.

#### File-owned snapshot handles
* `File` should follow the same ownership model as `Directory`.
* `NewFileWithSnapshot(...)` should adopt ownership of the provided handle and not clone it.
* Ownership transfers only when `NewFileWithSnapshot(...)` returns success.
* If the constructor returns an error, the caller still owns the original handle and must release it.
* `OnRelease(...)` remains the place where the file closes its snapshot handle.
* `AttachDependencyResults(...)` remains the place where file source dependencies become dagql deps.
* Callers that successfully construct a `File` and then hit a later error before returning/attaching it must release that `File` object explicitly.
* Internal `File` methods that currently use `Clone()` on a snapshot handle should instead reopen a second immutable handle by snapshot ID.
* The concrete clone-to-reopen sweep in this file includes:
  * temporary search/source wrappers built from a parent file snapshot
  * no-op replace paths that currently do `parentSnapshot.Clone()`
* Temporary helper `File` wrappers that are not returned through dagql must call `OnRelease(...)` after use.
  * `WithReplaced(...)` is one concrete case.

```go
func NewFileWithSnapshot(
    filePath string,
    platform Platform,
    services ServiceBindings,
    snapshot bkcache.ImmutableRef,
) (*File, error) {
    if snapshot == nil {
        return nil, fmt.Errorf("new file with snapshot: nil snapshot")
    }
    file := &File{
        File:     filePath,
        Platform: platform,
        Services: slices.Clone(services),
    }
    if err := file.setSnapshot(snapshot); err != nil {
        return nil, err
    }
    return file, nil
}
```

```go
parentSnapshot, err := parent.Self().getSnapshot()
if err != nil {
    return err
}
query, err := CurrentQuery(ctx)
if err != nil {
    return err
}
sourceSnapshot, err := query.SnapshotManager().GetBySnapshotID(
    ctx,
    parentSnapshot.SnapshotID(),
    bkcache.NoUpdateLastUsed,
)
if err != nil {
    return err
}
sourceFile, err := NewFileWithSnapshot(file.File, file.Platform, file.Services, sourceSnapshot)
if err != nil {
    return err
}
```

## Design and Implementation Plan points to assess before implementation

* None currently.

## Final completion flow

* After implementation appears finished:
  * spawn a fresh sub-agent to verify whether everything laid out in this implementation plan is actually done
  * if the sub-agent finds anything missing, finish that work before moving on
* Once the implementation is really complete:
  * make the git commit
* Only after that commit exists:
  * run integration tests in this order:
    * `test directory`
    * `test file`
    * `test container`
    * `test module`
    * `test engine`
