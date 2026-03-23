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

# Laziness Refactor

## Brainstorming

### Current State Catalog

#### True `LazyState` owners

The actual core object types that currently own `LazyState` are narrower than the
overall set of types that have `Evaluate` / `Sync` methods:

* `Container`
  * owner type: `core/container.go`
  * embeds `LazyState` at `core/container.go:114`
  * main evaluation entrypoints:
    * `Evaluate` at `core/container.go:180`
    * `Sync` at `core/container.go:187`
  * creation/reset sites:
    * `NewContainer` at `core/container.go:143`
    * `NewContainerChild` / child-clone reset at `core/container.go:150`
    * persisted decode / reconstruction at `core/container.go:399`
  * lazy-producing operations:
    * `FromCanonicalRef` returns a `LazyInitFunc` for digest-addressed image pulls at `core/container.go:602`
    * rootfs import path creates a lazy rootfs `Directory` and assigns `rootfsDir.LazyInit` at `core/container.go:2305` and `core/container.go:2315`
    * `WithExec` creates the shared exec gate and rewires container laziness at `core/container_exec.go:945-949`
  * important interactions:
    * container laziness is tightly coupled to `FS`, mounts, `MetaSnapshot`, services, and Buildkit query/session state
    * `WithExec` is not just "lazy container eval"; it also manufactures lazy filesystem outputs that participate in the same gate
  * schema/container explicit evaluation triggers are widespread:
    * `core/schema/container.go:1119`
    * `core/schema/container.go:1152`
    * `core/schema/container.go:1189`
    * `core/schema/container.go:1226`
    * `core/schema/container.go:1233`
    * `core/schema/container.go:1660`
    * `core/schema/container.go:1703`
    * `core/schema/container.go:1715`
    * `core/schema/container.go:1733`
    * `core/schema/container.go:1745`
    * `core/schema/container.go:1775`
    * `core/schema/container.go:1840`
    * `core/schema/container.go:2233`
    * `core/schema/container.go:2260`
    * `core/schema/container.go:2314`
    * `core/schema/container.go:2398`
    * `core/schema/container.go:2519`
    * `core/schema/container.go:2551`
    * `core/schema/container.go:2578`
    * `core/schema/container.go:2633`
    * `core/schema/container.go:2645`
    * `core/schema/container.go:2706`
    * `core/schema/container.go:2718`
    * `core/schema/container.go:2769`
    * `core/schema/container.go:2781`

* `Directory`
  * owner type: `core/directory.go`
  * embeds `LazyState` at `core/directory.go:50`
  * main evaluation entrypoints:
    * `Evaluate` at `core/directory.go:111`
    * `Sync` at `core/directory.go:115`
    * `getSnapshot` at `core/directory.go:139`
    * `getParentSnapshot` at `core/directory.go:260`
  * creation/reset sites:
    * clone/reset path at `core/directory.go:91`
    * persisted decode / reconstruction at `core/directory.go:232`
    * service snapshot wrapper at `core/service.go:1066`
    * changeset materialization at `core/changeset.go:811`
    * git local wrappers at `core/git_local.go:184` and `core/git_local.go:278`
    * git remote wrappers at `core/git_remote.go:624` and `core/git_remote.go:687`
    * builtin container wrapper at `core/builtincontainer.go:67`
    * host/schema wrappers at `core/schema/host.go:279`, `core/schema/directory.go:389`, `core/schema/directory.go:434`, and `core/schema/container.go:932`
    * container rootfs output wrapper at `core/container_exec.go:956`
    * writable directory-mount output wrappers at `core/container_exec.go:995`
  * lazy-producing operations on `Directory` itself:
    * `WithNewFile` at `core/directory.go:432`
    * `WithPatch` at `core/directory.go:513`
    * `Directory.File` derived subfile wrapper at `core/directory.go:713`
    * `WithDirectory` at `core/directory.go:753`
    * `WithFile` at `core/directory.go:1376`
    * `WithTimestamps` at `core/directory.go:1540`
    * `WithNewDirectory` at `core/directory.go:1585`
    * `Diff` at `core/directory.go:1633`
    * `WithChanges` at `core/directory.go:1702`
    * `Without` at `core/directory.go:1776`
    * `WithSymlink` at `core/directory.go:2002`
    * `Chown` at `core/directory.go:2071`
  * important interactions:
    * directories recurse through `Parent` via `getParentSnapshot`
    * many lazy operations also depend on separate source snapshots (`src.getSnapshot`, source file snapshot, changeset snapshots)
    * directories talk directly to Buildkit/query helpers rather than going through dagql cache scheduling

* `File`
  * owner type: `core/file.go`
  * embeds `LazyState` at `core/file.go:46`
  * main evaluation entrypoints:
    * `Evaluate` at `core/file.go:99`
    * `Sync` at `core/file.go:103`
    * `getSnapshot` at `core/file.go:127`
    * `getParentSnapshot` at `core/file.go:248`
  * creation/reset sites:
    * clone/reset path at `core/file.go:91`
    * persisted decode / reconstruction at `core/file.go:220`
    * derived `sourceFile` wrapper in replace logic at `core/file.go:466` and no-op `LazyInit` assignment at `core/file.go:474`
    * directory subfile wrapper at `core/directory.go:706` and `core/directory.go:713`
    * schema query/http helper wrappers at `core/schema/query.go:160` and `core/schema/http.go:158`
    * writable file-mount output wrappers at `core/container_exec.go:1018`
  * lazy-producing operations on `File` itself:
    * `WithContents` at `core/file.go:255`
    * `WithReplaced` at `core/file.go:452`
    * `WithName` at `core/file.go:675`
    * `WithTimestamps` at `core/file.go:726`
    * `Chown` at `core/file.go:907`
  * important interactions:
    * files recurse through parent directories for snapshot ancestry
    * some file transforms also create helper/source wrappers with their own lazy state
    * like directories, file laziness directly reaches query/buildkit helpers

#### Shared gate / coupling patterns

The single biggest special case today is `Container.WithExec`:

* `core/container_exec.go:945`
  * creates one shared `gate := NewLazyState()`
* `core/container_exec.go:949`
  * rewires `container.LazyInit = gateRun`
* `core/container_exec.go:962`
  * assigns the same `gateRun` to the rootfs output `Directory`
* `core/container_exec.go:1001`
  * assigns the same `gateRun` to writable directory-mount outputs
* `core/container_exec.go:1024`
  * assigns the same `gateRun` to writable file-mount outputs
* `core/container_exec.go:1038`
  * the actual heavy exec/materialization logic is stored on `gate.LazyInit`
* `core/container_exec.go:1255-1264`
  * during gate execution, `WithExec` reaches back into `inputRootFS.getSnapshot(ctx)` to materialize the new rootfs mount

So the current model is not "each object lazily knows how to realize itself in isolation".
It is often:

* one object's `LazyInit` evaluating
* then reaching into parent/source/rootfs objects
* which may themselves still be lazy
* while all of that runs under primitive object-local mutexes rather than any dagql/cache-aware scheduling

#### Adjacent participants that are not true `LazyState` owners

These types participate in lazy workflows but do not themselves own `LazyState`:

* `Service`
  * `Evaluate` is a no-op at `core/service.go:97`
* `Changeset`
  * `Evaluate` is a no-op at `core/changeset.go:243`
* `EngineCacheEntrySet`
  * `Evaluate` is a no-op at `core/engine.go:68`
* `TerminalLegacy`
  * `Evaluate` is a no-op at `core/container.go:2831`
* `mountObj` / `getRefOrEvaluate`
  * helper path in `core/util.go:380-415`
  * these do not own laziness but they absolutely trigger it

This distinction matters because some future refactor work should target:

* true lazy state ownership
* lazy creation
* lazy triggering

rather than treating every `Evaluate` method as equally meaningful.

#### Immediate observations

* Laziness is distributed through direct object-to-object calls, especially:
  * parent filesystem ancestry (`getParentSnapshot`)
  * source object snapshot loading
  * `WithExec` gate reuse across container + filesystem outputs
* The synchronization primitive is only a plain `sync.Mutex` in `core/lazy_state.go`.
  * That gives per-object singleflight, but no graph awareness, no re-entrancy story, no telemetry integration, and no cache scheduler integration.
* Evaluation triggers are spread across:
  * core object internals (`getSnapshot`, `getParentSnapshot`)
  * schema-level explicit `parent.Evaluate(ctx)` calls
  * helper functions like `mountObj` / `getRefOrEvaluate`
* `Container.WithExec` is the densest and most coupled lazy path today.
  * It is where container laziness, directory/file laziness, mount materialization, Buildkit execution, and service startup all meet.
* The current hang debugging strongly suggests the filesystem/container lazy graph is one of the most important places to simplify before doing more cache-level debugging.

### Creation / Trigger Matrix

The goal of this subsection is to separate:

* where lazy state is created or reset
* where new lazy work is attached
* what directly triggers evaluation
* what indirectly triggers evaluation through parent/source/rootfs traversal
* what other state each owner reaches into while realizing itself

#### `Container`

Creation / reset sites:

* `NewContainer` at `core/container.go:143`
  * creates a fresh lazy container shell
* `NewContainerChild` at `core/container.go:150`
  * clones container state but resets `LazyState`
* persisted decode / reconstruction at `core/container.go:399`
  * rebuilds a lazy container from persisted payload

Lazy-producing operations:

* `FromCanonicalRef` at `core/container.go:602`
  * returns a `LazyInitFunc` that resolves an image reference through Buildkit/container source APIs
  * interacts with:
    * current query
    * Buildkit client/session
    * registry/container source resolution
* rootfs import helper at `core/container.go:2305` / `core/container.go:2315`
  * creates a lazy `Directory` for imported rootfs state and installs a snapshot-loader `LazyInit`
  * interacts with:
    * Buildkit client/session
    * OCI/container source snapshotting
    * `UpdatedRootFS`
* `WithExec` shared gate at `core/container_exec.go:945-1038`
  * replaces container laziness with a single gate
  * reuses that same gate for derived rootfs output and writable mount outputs
  * interacts with:
    * existing rootfs (`inputRootFS`)
    * mount sources
    * cache volumes
    * services
    * Buildkit worker/session/cache
    * execution metadata

Direct evaluation triggers:

* `Evaluate` at `core/container.go:180`
* `Sync` at `core/container.go:187`
  * first evaluates the container
  * then forces `FS.Evaluate` if `FS` exists
* schema/container explicit parent evaluation sites:
  * `core/schema/container.go:1119`
  * `core/schema/container.go:1152`
  * `core/schema/container.go:1189`
  * `core/schema/container.go:1226`
  * `core/schema/container.go:1233`
  * `core/schema/container.go:1660`
  * `core/schema/container.go:1703`
  * `core/schema/container.go:1715`
  * `core/schema/container.go:1733`
  * `core/schema/container.go:1745`
  * `core/schema/container.go:1775`
  * `core/schema/container.go:1840`
  * `core/schema/container.go:2233`
  * `core/schema/container.go:2260`
  * `core/schema/container.go:2314`
  * `core/schema/container.go:2398`
  * `core/schema/container.go:2519`
  * `core/schema/container.go:2551`
  * `core/schema/container.go:2578`
  * `core/schema/container.go:2633`
  * `core/schema/container.go:2645`
  * `core/schema/container.go:2706`
  * `core/schema/container.go:2718`
  * `core/schema/container.go:2769`
  * `core/schema/container.go:2781`

Indirect evaluation triggers:

* any path that needs the rootfs snapshot:
  * `core/container_exec.go:496`
  * `core/container_exec.go:1260`
* any path that needs directory/file mount snapshots:
  * `core/container_exec.go:532`
  * `core/container_exec.go:543`
  * `core/container_exec.go:1293`
  * `core/container_exec.go:1304`
* cache-volume snapshot checks inside exec:
  * `core/container_exec.go:554`
  * `core/container_exec.go:559`
  * `core/container_exec.go:1315`
  * `core/container_exec.go:1320`
* helper paths that first force filesystem state before continuing:
  * `core/container.go:2139`
  * `core/container.go:2251`
  * `core/container.go:721`

Coupling / state reached during realization:

* `FS`
* mount list
* `MetaSnapshot`
* services/service bindings
* Buildkit worker/session/cache
* execution metadata
* image/container source resolution

#### `Directory`

Creation / reset sites:

* clone/reset at `core/directory.go:91`
* persisted decode at `core/directory.go:232`
* wrappers created by:
  * `core/service.go:1066`
  * `core/changeset.go:811`
  * `core/git_local.go:184`
  * `core/git_local.go:278`
  * `core/git_remote.go:624`
  * `core/git_remote.go:687`
  * `core/builtincontainer.go:67`
  * `core/schema/host.go:279`
  * `core/schema/directory.go:389`
  * `core/schema/directory.go:434`
  * `core/schema/container.go:932`
  * `core/container_exec.go:956`
  * `core/container_exec.go:995`

Lazy-producing operations:

* `WithNewFile` at `core/directory.go:432`
  * depends on parent snapshot only
* `WithPatch` at `core/directory.go:513`
  * depends on parent snapshot only
* derived subfile wrapper at `core/directory.go:713`
  * creates a child `File` whose lazy init reads the parent directory snapshot
* `WithDirectory` at `core/directory.go:753`
  * depends on:
    * destination parent snapshot
    * source directory snapshot
    * current query/server for source ID loading
* `WithFile` at `core/directory.go:1376`
  * depends on:
    * source file snapshot
    * destination parent snapshot
    * current query/buildkit cache
* `WithTimestamps` at `core/directory.go:1540`
  * depends on parent snapshot
* `WithNewDirectory` at `core/directory.go:1585`
  * depends on parent snapshot
* `Diff` at `core/directory.go:1633`
  * depends on both this-parent snapshot and other snapshot
* `WithChanges` at `core/directory.go:1702`
  * depends on current dir snapshots while replaying changeset paths
* `Without` at `core/directory.go:1776`
  * depends on parent snapshot
* `WithSymlink` at `core/directory.go:2002`
  * depends on parent snapshot
* `Chown` at `core/directory.go:2071`
  * depends on parent snapshot

Direct evaluation triggers:

* `Evaluate` at `core/directory.go:111`
* `Sync` at `core/directory.go:115`
* `getSnapshot` at `core/directory.go:139`
  * this is effectively the main direct evaluation gateway for all read-like directory paths

Indirect evaluation triggers through snapshot reads:

* parent ancestry:
  * `core/directory.go:147`
  * `core/directory.go:260`
* read-like operations on the directory itself:
  * `core/directory.go:268`
  * `core/directory.go:294`
  * `core/directory.go:361`
  * `core/directory.go:581`
  * `core/directory.go:1918`
  * `core/directory.go:1985`
* lazy mutators that pull parent/source snapshots:
  * `core/directory.go:447`
  * `core/directory.go:515`
  * `core/directory.go:714`
  * `core/directory.go:767`
  * `core/directory.go:794`
  * `core/directory.go:1386`
  * `core/directory.go:1391`
  * `core/directory.go:1546`
  * `core/directory.go:1600`
  * `core/directory.go:1635`
  * `core/directory.go:1657`
  * `core/directory.go:1744`
  * `core/directory.go:1764`
  * `core/directory.go:1782`
  * `core/directory.go:2008`
  * `core/directory.go:2081`

Coupling / state reached during realization:

* `Parent`
* source directories/files
* changesets
* current query
* current dagql server for ID loading in some mutators
* Buildkit cache/snapshots
* services/platform metadata copied through wrappers

#### `File`

Creation / reset sites:

* clone/reset at `core/file.go:91`
* persisted decode at `core/file.go:220`
* wrappers created by:
  * replace helper source-file wrapper at `core/file.go:466`
  * directory subfile wrapper at `core/directory.go:706`
  * schema helpers at `core/schema/query.go:160` and `core/schema/http.go:158`
  * writable file-mount outputs at `core/container_exec.go:1018`

Lazy-producing operations:

* `WithContents` at `core/file.go:255`
  * depends on parent snapshot
* `WithReplaced` at `core/file.go:452`
  * depends on:
    * parent snapshot via explicit `parent` result
    * helper/source file wrapper
* helper `sourceFile` no-op `LazyInit` at `core/file.go:474`
  * special case wrapper used during replace logic
* `WithName` at `core/file.go:675`
  * depends on explicit parent snapshot
* `WithTimestamps` at `core/file.go:726`
  * depends on explicit parent snapshot
* `Chown` at `core/file.go:907`
  * depends on explicit parent snapshot

Direct evaluation triggers:

* `Evaluate` at `core/file.go:99`
* `Sync` at `core/file.go:103`
* `getSnapshot` at `core/file.go:127`

Indirect evaluation triggers through snapshot reads:

* parent ancestry:
  * `core/file.go:135`
  * `core/file.go:248`
  * `core/file.go:252`
* read-like file operations:
  * `core/file.go:345`
  * `core/file.go:422`
  * `core/file.go:593`
  * `core/file.go:632`
  * `core/file.go:787`
  * `core/file.go:846`
  * `core/file.go:864`
* lazy mutators that read parent/source snapshots:
  * `core/file.go:269`
  * `core/file.go:461`
  * `core/file.go:684`
  * `core/file.go:732`
  * `core/file.go:917`

Coupling / state reached during realization:

* parent directory/file result
* source/temporary helper file wrappers
* current query
* Buildkit cache/snapshots

#### Helper trigger paths

These do not own lazy state, but they are a meaningful part of the current
mental model because they trigger realization outside the owner types:

* `mountObj` / `getRefOrEvaluate` in `core/util.go:380-415`
  * helper path that takes a file/directory and forces snapshot realization
* schema-level mutators in `core/schema/file.go`
  * attach new `LazyInit` closures for file transforms:
    * `core/schema/file.go:205`
    * `core/schema/file.go:236`
    * `core/schema/file.go:283`
    * `core/schema/file.go:305`
* schema-level mutators in `core/schema/directory.go`
  * attach new `LazyInit` closures for directory transforms:
    * `core/schema/directory.go:480`
    * `core/schema/directory.go:525`
    * `core/schema/directory.go:586`
    * `core/schema/directory.go:645`
    * `core/schema/directory.go:673`
    * `core/schema/directory.go:735`
    * `core/schema/directory.go:783`
    * `core/schema/directory.go:805`
    * `core/schema/directory.go:911`
    * `core/schema/directory.go:929`
    * `core/schema/directory.go:947`
    * `core/schema/directory.go:1039`
    * `core/schema/directory.go:1136`
    * `core/schema/directory.go:1642`
    * `core/schema/directory.go:1672`

## Design

### Overall model

The next laziness pass should make dagql cache the orchestration center for
evaluation while keeping the concrete evaluation logic on the object itself.

This should be treated as a hard cut away from the current distributed model
where objects recurse into each other directly through helpers like
`getSnapshot`, `getParentSnapshot`, `Sync`, and `Evaluate`.

The intended split is:

* dagql cache owns:
  * the central `Evaluate` entrypoint
  * evaluation singleflight / in-progress tracking
  * cycle detection
  * observability / telemetry around evaluation
  * the fact that a shared result is lazy vs already evaluated
* the object itself owns:
  * the concrete lazy callback
  * the in-place mutation of its own realized state
  * knowledge of which already-declared dependencies it specifically needs to evaluate

### One internal concept

Internally, we want one concept and one verb: `Evaluate`.

That means:

* `Sync` should not remain a separate internal realization model
* public schema/API entrypoints can still be called `sync`
  * that is a boundary concern only
* internally, those boundary entrypoints should route to the same evaluation model

### Cache-driven evaluation

The cache should expose one central evaluation path, conceptually
`cache.Evaluate(ctx, res)`.

The expected behavior is:

* if the result is not lazy, `Evaluate` is a no-op
* if the result is lazy and already evaluated, `Evaluate` is a no-op
* if the result is lazy and currently being evaluated, `Evaluate` waits on the
  in-progress evaluation rather than re-running it
* if the result is lazy and not yet evaluated, cache runs the object's callback
* cache does not report the evaluation complete until all realized state has
  been published back onto the relevant objects

### Dependency model

We want to stay within the existing shared-result dependency model rather than
inventing a second first-class graph for evaluation.

The intended rule is:

* we do not store a separate evaluation graph
* we do not blindly evaluate the full dependency graph
* instead, the existing dependency graph is the legal universe of what a lazy
  callback may request evaluation for
* at runtime, the callback chooses the specific subset of dependencies it
  actually needs in that codepath

That means:

* if a lazy callback needs some other result to be evaluated, that result must
  already be represented as a dependency
  * via ordinary result-call dependencies
  * or explicit attached/owned-result dependencies
* if a lazy callback reaches for something not represented as a dependency, that
  is a bug

So:

* "all dependencies that keep me alive" and "the dependencies I actually
  evaluate in this codepath" are not identical sets
* but they still live inside one stored graph model

### Returned lazy results

Returned lazy results must be attached shared results.

The intended rule is:

* temporary detached results during an in-flight operation are acceptable
* once an operation returns a lazy result, it must be attached
* `DoNotCache` is incompatible with laziness
  * this should be treated the same way we now treat `DoNotCache` with
    `OnReleaser`

### Evaluation callback contract

The intended lazy callback contract is:

* mutate realized state in place on the object itself
* when another result must be evaluated first, ask dagql cache to evaluate it
* do not directly recurse through object internals in the old style

Examples of old-style patterns we want to remove:

* `parent.Self().getSnapshot(ctx)`
* `src.getSnapshot(ctx)`
* `container.Evaluate(ctx)` called from another object's lazy internals

Instead, the callback should do something conceptually like:

* ask cache to evaluate the specific dependency it needs
* then read that dependency's already-realized state / snapshot / metadata

### Explicit read / write discipline

We want a non-magical first pass.

That means:

* fields impacted by lazy evaluation should not be read directly
* fields impacted by lazy evaluation should not be written directly outside the
  lazy publish path
* those fields should be accessed through package-private or otherwise explicit
  methods
* those accessor methods should not trigger evaluation automatically
* if a caller asks for lazily-realized state before evaluation completed, the
  accessor should return an error
* callers are responsible for explicitly evaluating first and reading second

So the intended boundary is explicit:

* one piece of code orchestrates evaluation
* a separate piece of code reads already-realized state
* temporary detached filesystem objects may still be created inside an
  in-flight operation as return values that will immediately become attached
* but if a helper wants immediate readback from a would-be child filesystem
  object, it must not try to make detached lazy evaluation work
* instead, that helper must stay in dagql selection space from an already
  attached parent or source result and select the child/read field there

This is intentionally more explicit and less magical than today.

### Snapshot delegation model

Some objects participate in lazy evaluation even when they do not materialize a
new snapshot of their own.

The intended model is:

* objects that create a new snapshot use their lazy callback to evaluate the
  dependencies they need and then call `setSnapshot(...)`
* objects that do not create a new snapshot still participate in lazy
  evaluation, but as pass-through snapshot delegates
* a pass-through callback evaluates the attached dependency result it needs
  through dagql cache and then records that attached result as its
  `snapshotSource` instead of copying the dependency's snapshot ref onto itself

The concrete source shapes are:

* `Directory` delegates to one attached `dagql.ObjectResult[*Directory]`
* `File` delegates through an explicit `FileSnapshotSource` struct with:
  * `Directory dagql.ObjectResult[*Directory]`
  * `File dagql.ObjectResult[*File]`
* for `File`, exactly one of those source fields should be set at a time

That means:

* we keep one read helper named `getSnapshot`
* `getSnapshot` is a pure read helper and never triggers evaluation
* `getSnapshot` returns an error if evaluation has not already completed
* if an object has its own concrete snapshot, `getSnapshot` returns it
* if an object has no concrete snapshot but has a snapshot-source dependency,
  `getSnapshot` reads through to that dependency's `getSnapshot`
* we remove the separate `getParentSnapshot` helper entirely

This keeps the model simple:

* one explicit cache-driven evaluation path
* one explicit snapshot read path
* no duplicate snapshot refs copied onto pass-through objects
* no bare-to-bare lazy delegation that bypasses dagql cache awareness

### Explicit dependency inputs instead of hidden parent fields

Where lazy callbacks need another result later, that dependency should be
passed explicitly when constructing the callback rather than stored as broad,
always-present hidden object state.

That means for directory/file-style snapshot ancestry:

* remove hidden `Parent` fields from the core objects
* pass the required attached dependency results explicitly into the callback
  constructor for the operation that needs them
* keep only narrow explicit `snapshotSource` result dependencies on evaluated
  pass-through objects
* rely on existing result-call / explicit dependency machinery for ownership
  and evaluation legality, rather than object-level hidden parent pointers

### Dependency attachment terminology

The existing `AttachOwnedResults` concept is really dependency attachment.

The intended terminology is:

* `HasOwnedResults` becomes `HasDependencyResults`
* `AttachOwnedResults(...)` becomes `AttachDependencyResults(...)`
* cache-side `attachOwnedResults(...)` becomes `attachDependencyResults(...)`

The intended meaning is narrow:

* this is for payload-embedded result refs that the current result truly
  depends on
* it is not for derived outputs like `withExec` rootfs or writable mount outputs

So:

* arrays, modules, module sources, module objects, interface values, git
  objects, and similar payload-embedded refs stay in this bucket
* directory/file hidden-parent uses go away with the parent-field cut
* `Container.WithExec` stops using this mechanism for rootfs and writable mount outputs

### Container filesystem state model

Container rootfs and mount filesystem state should stay result-backed only as
long as that is the honest and useful representation.

The intended rule is:

* if rootfs or a mount source is still fundamentally another result-backed
  directory/file operation, keep it result-backed
* if an operation like `withExec`, `from`, or builtin container init produces
  filesystem state that is no longer honestly an independent object result of
  its own, stop pretending and store it as a bare `Directory` / `File`

That means:

* container rootfs and directory/file mount slots become "result-backed or bare"
* `withExec` no longer fabricates child dagql results for rootfs and writable
  mount outputs
* `from` / builtin-rootfs init no longer fabricate child dagql results for rootfs
* when a caller later selects `rootfs`, `directory(path)`, or `file(path)`,
  that field resolver becomes the result boundary for bare filesystem state
* when a later container operation keeps a bare slot unchanged, the new child
  bare object delegates through the honest attached parent selection for that
  slot:
  * bare rootfs -> parent container `rootfs`
  * bare directory mount -> parent container `directory(path: <mount target>)`
  * bare file mount -> parent container `file(path: <mount target>)`

This is specifically intended to avoid:

* inventing fake child-result identity where it does not really exist
* backward dependency edges from parent result to child output result
* circular-dependency pressure in the lazy-evaluation model
* confusing size/accounting attribution for exec-produced snapshots

### Publication and locking model

The critical concurrency rule is that we only gate the fields actually impacted
by lazy evaluation.

We do not want:

* a cache-wide evaluation lock
* a huge shared-result lock around all object access
* long-held locks during Buildkit work, dependency evaluation, or other slow work

The intended pattern is:

* dagql cache owns evaluation orchestration and singleflight
* each object type keeps only a tiny state lock around the fields that lazy
  evaluation mutates or publishes
* the lazy callback does heavy work without holding those tiny object locks
* once realized data has been computed, the callback publishes that realized
  state through explicit setter/publish helpers
* those publish helpers briefly take the tiny object lock for the target object
* read accessors take that same tiny object lock, verify the state has already
  been published, and either return it or return an error

This should be kept manual and explicit rather than relying on magic.

### `WithExec` and derived outputs

`WithExec` should no longer model rootfs and writable mount outputs as child
dagql results stored inside the container payload.

The intended model is:

* the returned `withExec` container result is the one lazy dagql result
* rootfs and writable mount outputs produced by that exec are stored as bare
  `Directory` / `File` state inside the container
* the `withExec` callback mutates:
  * the container itself
  * the bare rootfs output
  * the bare writable mount outputs
* later `rootfs`, `directory(path)`, and `file(path)` selections become the
  result boundary for that bare state when a caller actually asks for it

This means we do **not** add a separate `AttachChildResults` mechanism for this problem.
The old container shape was the wrong representation; the fix is to stop
pretending these outputs are independent child results in the first place.

### What should shrink or disappear

Most of today's object-local `LazyState` machinery should go away.

The intended direction is:

* the callback / "how to evaluate me" hook may still live on the object
* object-local mutex / completion bookkeeping should mostly move into dagql cache
* cache should become the place that knows:
  * this result is lazy
  * this result is currently being evaluated
  * this result has already been evaluated
* implicit "evaluate as a side effect of read" helpers should shrink or disappear

### Phase boundaries

For this phase:

* the cache-facing lazy interface, callback placement on objects, and
  cache-owned shared-result lazy state are considered settled by the
  `HasLazyEvaluation`, `LazyState`, and `sharedResult` sections below
* recursive lazy evaluation is an error
  * use a plain error in this phase, e.g. `recursive lazy evaluation detected`
  * do not build fancy cycle pretty-printing unless it falls out trivially from
    the existing stack tracking
  * if any cycle detail is included, cap it at 32 stack entries
* telemetry/observability changes are explicitly deferred
  * do not design or implement telemetry-specific evaluation wrappers in this phase
* snapshot/ref ownership cleanup is also explicitly deferred to the next phase
  * this phase only needs a conservative, non-lossy ownership rule

## Implementation plan

The first implementation pass should focus on moving the generic orchestration
into dagql/cache and shrinking the old object-local laziness runtime, before we
start converting many individual core methods.

### `dagql`

#### `dagql/types.go`

##### Lazy object discovery interface

Add the minimal dagql-side interface that lets cache discover whether an
attached object has lazy evaluation and, if so, how to run it.

Specific change:

* add `type LazyEvalFunc func(context.Context) error`
* add:
  * `type HasLazyEvaluation interface { LazyEvalFunc() LazyEvalFunc }`
* cache will treat:
  * `nil` callback as "not lazy"
  * non-`nil` callback as "this attached result has deferred evaluation"

Why this shape:

* it keeps the dagql-side contract to one small interface
* it lets the object continue owning the concrete callback
* it does not invent a callback framework around dependency traversal
* it gives cache something concrete to register on the attached shared result

#### `dagql/cache.go`

##### `sharedResult` laziness state

Add cache-owned laziness state to `sharedResult`.

Specific change:

* add a dedicated per-result lazy-evaluation state block on `sharedResult`
* the state should include:
  * a per-result mutex for evaluation orchestration only
  * the registered `LazyEvalFunc`
  * a boolean for "evaluation complete"
  * a wait channel used while one evaluation is in progress
* do not use this mutex to guard arbitrary object reads/writes
  * it is only for cache-owned orchestration state on that shared result

Concrete intended fields:

* `lazyMu sync.Mutex`
* `lazyEval LazyEvalFunc`
* `lazyEvalComplete bool`
* `lazyEvalWaitCh chan struct{}`
* `lazyEvalCancel context.CancelCauseFunc`
* `lazyEvalWaiters int`

##### `Cache.Evaluate`

Add the central `Cache.Evaluate(ctx, res)` entrypoint.

Expected behavior:

* works on attached results
* no-op for non-lazy / already-realized results
* singleflights in-progress evaluation
* owns cycle/error handling
* does not mark completion until publication is finished

Specific change:

* `Cache.Evaluate(ctx, res AnyResult) error` requires an attached cache-backed result
  * detached results should return an error
* nested evaluation ancestry should be tracked in `ctx` with a private cache
  context key storing the current stack of `sharedResultID`s
* when evaluating result `B` while `A` is already on the stack:
  * if `B` is not listed in `A.deps`, return an error for undeclared lazy dependency access
  * if `sharedResultID(B) == sharedResultID(A)` or `sharedResultID(B)` already
    exists in the evaluation stack, return a cycle error
  * equality here must be by attached result ID only, not by object pointer,
    wrapper value, or callback identity
* singleflight flow:
  * lock `shared.lazyMu`
  * if `lazyEval == nil` or `lazyEvalComplete`, unlock and return nil
  * if `lazyEvalWaitCh != nil`, increment `lazyEvalWaiters`, unlock, and wait
  * otherwise:
    * create an evaluation context using the same policy as `getOrInitCall`
    * `evalCtx = context.WithCancelCause(context.WithoutCancel(stackCtx))`
    * set `lazyEvalWaitCh`
    * set `lazyEvalCancel`
    * set `lazyEvalWaiters = 1`
    * unlock
    * run the callback under `evalCtx`
    * then publish success/failure back under `lazyMu`
* waiter behavior should intentionally mirror `getOrInitCall` / `wait`:
  * if evaluation finishes first, waiter receives the original callback error or nil
  * if the caller context is canceled first, waiter returns that caller-context cause
  * when the last waiter leaves, cancel the shared evaluation context with that cause
* success flow:
  * callback returns nil
  * cache marks `lazyEvalComplete = true`
  * cache clears `lazyEval`
  * cache closes `lazyEvalWaitCh`
  * cache clears `lazyEvalCancel`
  * cache clears `lazyEvalWaiters`
* failure flow:
  * cache closes `lazyEvalWaitCh`
  * cache clears the in-progress channel/cancel/waiter bookkeeping
  * cache leaves `lazyEval` installed so a later caller can retry
* undeclared-dependency and recursive-evaluation failures are plain errors in
  this phase; do not add typed error taxonomy just for them yet

The callback itself is expected to call `cache.Evaluate(ctx, dep)` for any lazy
dependency it needs; the dependency legality check above is how cache enforces
the "only declared deps may be evaluated" rule.

##### Lazy registration ownership

There should be exactly one internal lazy-registration helper for newly-created
attached shared results.

Specific change:

* add one internal cache helper that inspects the final attached payload for
  `HasLazyEvaluation` and, when present, installs that callback onto the new
  `sharedResult`
* `initCompletedResult` and `attachResult` must both use that same helper
* no other code path should write `sharedResult.lazyEval` directly

##### `initCompletedResult`

When a result becomes an attached shared result:

* detect whether the payload carries lazy evaluation
* if so, register that laziness on the shared result
* reject invalid combinations like attached `DoNotCache` + laziness

Specific change:

* after the attached result payload has been normalized and dependency results have
  been attached, call the one shared lazy-registration helper described above
* if `req.DoNotCache` is set, that helper rejects any non-`nil` lazy callback

Order requirement:

* register lazy evaluation only after dependency results/dependencies have been attached
* that way, when the callback later asks cache to evaluate a dependency, the
  dependency edge is already present and enforceable

##### `AttachResult` / `attachResult`

Preserve lazy registration when attaching detached results.

Expected behavior:

* the attached shared result is the thing cache evaluates
* laziness does not stay as detached-only object state once the result is returned

Specific change:

* after detached payload attachment/normalization is complete, call the same
  shared lazy-registration helper used by `initCompletedResult`
* do not duplicate callback-inspection or lazy-state writes in a second place
* attached results should never rely on object-local `Evaluate` bookkeeping once
  this registration step exists

##### `attachDependencyResults`

Keep using this to make non-call implicit deps explicit.

This remains important because lazy callbacks may only evaluate declared dependencies.

Specific change:

* rename `HasOwnedResults` to `HasDependencyResults`
* rename `AttachOwnedResults(...)` to `AttachDependencyResults(...)`
* rename cache-side `attachOwnedResults(...)` to `attachDependencyResults(...)`
* do not change the overall role of this mechanism
* rely on it as the generic mechanism that makes lazy dependency legality enforceable
* if a lazy callback needs some dependency that is not already represented via
  result-call structure, the owning object must expose that dependency through
  `HasDependencyResults` so `attachDependencyResults` can make it explicit

That means:

* we are not adding a second lazy-dependency registration API in dagql
* `HasDependencyResults` remains the one explicit out-of-band dependency hook
* `withExec` rootfs/mount outputs are no longer a use case for this hook

##### Non-goal

Do not invent a second persistent evaluation-graph data structure here.

#### `dagql/cache_test.go`

##### Generic evaluation orchestration tests

Add dagql unit tests for the new orchestration model before broad core rollout.

Specific tests to add:

* one fake attached object implementing `HasLazyEvaluation` whose callback
  increments a counter
  * assert `Cache.Evaluate` runs it once
  * assert concurrent `Cache.Evaluate` calls still increment once
* one fake lazy parent whose callback calls `Cache.Evaluate` on an attached child
  that is declared via ordinary or explicit deps
  * assert nested dependency evaluation succeeds
* one fake lazy parent whose callback calls `Cache.Evaluate` on a child that is
  not in `deps`
  * assert `Cache.Evaluate` returns an undeclared-dependency error
* one fake lazy object whose callback calls `Cache.Evaluate` on itself or on an
  already-active ancestor
  * assert cycle error
* one attachment/materialization test that returns a lazy object from a
  `DoNotCache` field/request
  * assert the attach/materialize path rejects it before publication

### `core`

#### `core/lazy_state.go`

##### Hard-shrink object-local orchestration

Specific change:

* rename `LazyInitFunc` to `LazyEvalFunc`
* make `LazyEvalFunc` an alias of the dagql-side callback type so core and
  dagql are speaking about the same callback concept
* shrink `LazyState` to one field:
  * `LazyEval LazyEvalFunc`
* keep `NewLazyState()` only as a convenience constructor returning the zero
  callback state
* delete:
  * `LazyMu`
  * `AfterEvaluate`
  * `LazyInitComplete`
  * `(*LazyState).Evaluate`
  * `(*LazyState).OnEvaluateComplete`

Consequence:

* object-local laziness bookkeeping disappears from core
* core objects continue carrying callbacks, but dagql cache becomes the only
  place that orchestrates evaluation

#### `core/container.go`

##### Container filesystem-source representation

Specific change:

* replace result-only filesystem slots with explicit "result-backed or bare" wrappers
* add:
  * `type ContainerDirectorySource struct { Result *dagql.ObjectResult[*Directory]; Value *Directory }`
  * `type ContainerFileSource struct { Result *dagql.ObjectResult[*File]; Value *File }`
* change:
  * `Container.FS` from `*dagql.ObjectResult[*Directory]` to `*ContainerDirectorySource`
  * `ContainerMount.DirectorySource` from `*dagql.ObjectResult[*Directory]` to `*ContainerDirectorySource`
  * `ContainerMount.FileSource` from `*dagql.ObjectResult[*File]` to `*ContainerFileSource`

Representation rule:

* exactly one of `Result` or `Value` should be set on a given source wrapper
* result-backed sources are used for ordinary mount/rootfs composition APIs
* bare sources are used for `withExec`, `from`, builtin container init, and
  terminal failure-rebuild
* when a container operation leaves a slot unchanged:
  * unchanged result-backed slot keeps the same result-backed wrapper
  * unchanged bare slot gets a fresh bare child object whose `LazyEval`
    callback evaluates the appropriate attached parent selection result and then
    calls `setSnapshotSource(parentSelectionResult)`

##### Constructors and cloning

Concrete targets:

* `Container` struct definition
* `NewContainer`
* `NewContainerChild`

Specific change:

* constructors and child-clone logic must copy the new source wrappers correctly
* result-backed wrappers are copied as wrappers and stay result-backed
* bare source wrappers must not be aliased as jointly-owned mutable bare objects
* when a child inherits unchanged bare filesystem state, it must get a fresh
  bare child object with delegation semantics instead of reusing the exact same
  bare object pointer
* any core container mutator that needs to preserve or delegate unchanged bare
  filesystem slots must receive the attached parent container result explicitly
  so it can build the honest parent selections for those delegates

##### Dependency attachment

Specific change:

* rename `Container.AttachOwnedResults` to `Container.AttachDependencyResults`
* keep it only for true dependency results:
  * result-backed `FS`
  * result-backed directory/file mounts
  * cache volume mounts
* bare rootfs and bare directory/file mount sources must not be attached as
  dependency results

##### `OnRelease`

Specific change:

* extend `Container.OnRelease` so the container releases any bare filesystem
  state it fully owns
* it should continue releasing `MetaSnapshot`
* it should additionally release:
  * bare rootfs `Directory`
  * bare directory mount sources
  * bare file mount sources
* it must not release result-backed rootfs or mount sources
  * dagql lifecycle remains responsible for those

##### `Evaluate` / `Sync`

Specific change:

* delete `(*Container).Evaluate`
* delete `(*Container).Sync`

Replacement rule:

* callers that currently evaluate a container through the bare object must be
  rewritten to evaluate the attached result through dagql cache instead
* any schema resolver that still needs to force container evaluation must keep
  the attached `dagql.ObjectResult[*Container]` available so it can call
  `dagql.EngineCache(ctx).Evaluate(ctx, parent)`

##### Persistence payload shape

Specific change:

* keep result-ID persistence for result-backed rootfs and mount sources
* add embedded bare-value persistence for bare rootfs and bare dir/file mounts

Concrete payload changes:

* `persistedContainerPayload`:
  * keep `FSResultID`
  * add `FSValue json.RawMessage`
* `persistedContainerMountPayload`:
  * keep `DirectorySourceResultID`
  * keep `FileSourceResultID`
  * add `DirectorySourceValue json.RawMessage`
  * add `FileSourceValue json.RawMessage`

Concrete snapshot-link changes:

* `Container.PersistedSnapshotRefLinks()` should emit:
  * existing `meta`
  * `fs` for bare rootfs snapshots
  * `mount_dir:<index>` for bare directory mount snapshots
  * `mount_file:<index>` for bare file mount snapshots

Concrete encode/decode behavior:

* if a source wrapper is result-backed, persist it by result ID exactly as today
* if a source wrapper is bare, persist it by embedding the normal `Directory` /
  `File` object JSON from that bare value's own `EncodePersistedObject(...)`
  implementation, using the container-level snapshot-link role names above
* `DecodePersistedObject` must rebuild the wrappers accordingly:
  * result-backed sources become `Result` wrappers
  * bare sources become `Value` wrappers reconstructed from the nested embedded
    `Directory` / `File` decode path
* container persistence does not invent a second bare-filesystem JSON format;
  it reuses the normal bare `Directory` / `File` payload shape directly
* nested delegated bare values should therefore persist their normal delegated
  source-result IDs exactly the same way they do outside a container; container
  decode relies on the nested `Directory` / `File` decode path to restore that
  state rather than reimplementing it

Concrete helper work in this file:

* add local encode/decode helpers for:
  * bare rootfs directory payload
  * bare directory mount payload
  * bare file mount payload
* remove the assumption that every persisted filesystem slot can be reconstructed
  exclusively from a result ID

##### Rootfs and mount source helpers

Specific change:

* add local helper functions in `core/container.go` that branch on result-backed
  vs bare filesystem sources instead of open-coding `Result != nil` checks

Concrete helper responsibilities:

* source access:
  * return the result-backed source when present
  * return the bare `Directory` / `File` when present
* snapshot access:
  * if the source is result-backed, evaluate the attached result through dagql
    cache and then read `Self().getSnapshot()`
  * if the source is bare, read `getSnapshot()` directly

These helpers should be used by:

* `RootFS`
* `Directory`
* `File`
* `Exists`
* `Stat`
* `openFile`
* `WithExec` input preparation

##### `RootFS`

Specific change:

* if `container.FS` is result-backed, keep returning `container.FS.Result.Self()`
* if `container.FS` is bare, return `container.FS.Value`
* if `container.FS` is nil, keep the scratch-directory fallback

##### `WithRootFS`

Specific change:

* keep `WithRootFS(ctx, dir dagql.ObjectResult[*Directory])`
* store it as a result-backed `ContainerDirectorySource`
* add a sibling internal helper:
  * `setBareRootFS(dir *Directory)`
* `withExec`, `from`, builtin container init, and terminal failure-rebuild
  should use the bare-rootfs helper instead of `WithRootFS`

##### Mutation routing APIs

Concrete targets:

* `WithDirectory`
* `WithFile`
* `WithFiles`
* `WithSymlink`
* `WithoutPaths` / `withoutPath`
* any sibling core mutator in this file that can preserve unchanged bare rootfs
  or mount slots

Specific change:

* these core mutators must all gain an explicit attached parent container input:
  * `parent dagql.ObjectResult[*Container]`
* concrete methods in this bucket include:
  * `WithDirectory(ctx, parent, srv, ...)`
  * `WithFile(ctx, parent, srv, ...)`
  * `WithFiles(ctx, parent, srv, ...)`
  * `WithoutPaths(ctx, parent, srv, ...)`
  * any helper they delegate to that needs to preserve unchanged bare slots
* if the relevant rootfs/mount source is result-backed, keep the current
  `srv.Select(... sourceResult, selector ...)` path
* if the relevant rootfs/mount source is bare:
  * operate directly on the bare `Directory` / `File`
  * do not synthesize fake intermediate child results first
  * write the updated bare value back into the appropriate container slot
* if a container operation leaves a bare slot unchanged:
  * create a fresh bare child object for the new container state
  * its `LazyEval` callback should close over the honest parent selection result
    for that slot:
    * rootfs -> parent container `rootfs`
    * directory mount -> parent container `directory(path: <mount target>)`
    * file mount -> parent container `file(path: <mount target>)`
  * during evaluation, that callback should:
    * evaluate the parent selection result through dagql cache
    * call `setSnapshotSource(parentSelectionResult)`

##### Read routing APIs

Concrete targets:

* `Directory`
* `File`
* `Exists`
* `Stat`
* `openFile`

Specific change:

* if routing lands on a result-backed source, keep the current behavior
* if routing lands on a bare source:
  * for `Directory` / `File`, operate on the bare object and wrap the returned
    bare value with `dagql.NewObjectResultForCurrentCall(...)` at the current
    field-selection boundary
  * for `Exists` / `Stat` / `openFile`, operate directly on the bare object
* when wrapping a bare `Directory` / `File` with `dagql.NewObjectResultForCurrentCall(...)`,
  preserve the object's existing `LazyEval` callback so evaluating that result
  routes back through the correct container/bare-source lazy path

##### Ownership and readback helpers

Concrete targets:

* `chownDir`
* `chownFile`
* `openFile`
* `ownership`
* `ResolveOwnership`

Specific change:

* these helpers must stop assuming they can always route through a result-backed
  `Directory` / `File`
* when the relevant container slot is bare:
  * resolve filesystem reads directly against the bare object
  * only evaluate through dagql cache when the bare object's callback requires it
* owner-lookup callsites must remain correct when `/etc/passwd` or `/etc/group`
  live under bare rootfs/mount state

##### Export / publish / import paths

Concrete targets:

* `filterEmptyContainers`
* `getVariantRefs`
* `Publish`
* `AsTarball`
* `Export`
* `Import`
* `FromInternal`

Specific change:

* anywhere these paths currently assume `container.FS.Self()` exists must branch
  on result-backed vs bare rootfs
* `getVariantRefs` must get the rootfs snapshot through the new source helpers
  instead of directly reaching through `variant.FS.Self().getSnapshot(...)`
* `Import` / `FromInternal` must stop wrapping imported rootfs through
  `UpdatedRootFS` and instead store bare rootfs state directly
* export/publish paths must continue to work regardless of whether the final
  container rootfs is result-backed or bare

##### Deletions

Specific change:

* delete `UpdatedRootFS`
* delete `updatedDirMount`
* delete `updatedFileMount`
* delete `updatedContainerSelectionResult`

Those helpers are the synthetic child-result machinery we are intentionally
removing from `withExec`, `from`, and builtin-rootfs initialization.

#### `core/container_exec.go`

##### `WithExec` output representation

Specific change:

* keep one lazy callback only on the returned `Container`
* stop creating lazy child result objects for:
  * rootfs output
  * writable directory mount outputs
  * writable file mount outputs
* instead create bare `Directory` / `File` values for those outputs and store
  them in the new container source wrappers

Concrete consequence:

* no shared child-result gate wiring
* no `UpdatedRootFS` / `updatedDirMount` / `updatedFileMount` in `WithExec`
* the returned `withExec` container remains the only lazy dagql result in this path

##### `WithExec` lazy callback

Specific change:

* `container.LazyEval` remains the one callback that runs the exec
* it should capture:
  * input rootfs source wrapper
  * input mount source wrappers
  * bare output rootfs directory
  * bare writable output mount directories/files
* when preparing inputs:
  * if an input source is result-backed, evaluate it through dagql cache and
    read `getSnapshot()` from its `Self()`
  * if an input source is bare, read `getSnapshot()` directly
* when publishing outputs:
  * rootfs output calls `setSnapshot(...)` on the bare rootfs directory
  * writable mount outputs call `setSnapshot(...)` on the bare output file/dir
  * `MetaSnapshot` is set on the container as today

##### `prepareMounts`

Specific change:

* replace all direct assumptions that `container.FS`, `ctrMount.DirectorySource`,
  and `ctrMount.FileSource` are result-backed objects
* route all rootfs/mount input snapshot loading through the new container source
  helper methods in `core/container.go`

##### Metadata readers

Concrete targets:

* `Stdout`
* `Stderr`
* `CombinedOutput`
* `ExitCode`
* `metaFileContents`

Specific change:

* these remain metadata reads off `MetaSnapshot`
* no child-result model should be introduced for them
* schema/read callers must evaluate the attached container result through dagql
  cache before calling these helpers

##### Terminal failure-rebuild path

Specific change:

* when rebuilding `terminalContainer` after exec failure, stop creating
  synthetic updated rootfs/mount child results
* rebuild `terminalContainer.FS` and writable mount sources as bare values
  inside the new wrapper types

#### `core/builtincontainer.go`

##### Builtin rootfs initialization

Specific change:

* stop wrapping the builtin rootfs bare directory through `UpdatedRootFS`
* store it as bare rootfs state on the container

#### `core/container.go` `FromCanonicalRef`

##### Canonical image rootfs initialization

Specific change:

* stop wrapping the canonical-image rootfs bare directory through `UpdatedRootFS`
* store it as bare rootfs state on the container
* keep the container itself as the lazy result; rootfs becomes bare state
  surfaced later through `rootfs` / `directory` / `file`

#### `core/terminal.go`

##### Terminal paths

Concrete targets:

* `cloneContainerForTerminal`
* `Container.terminal`
* `Container.TerminalExecError`
* `Directory.Terminal`

Specific change:

* terminal code must stop using bare-object `container.Evaluate(ctx)`
* where the terminal path needs the container built first, it must evaluate the
  attached container result through dagql cache before continuing
* terminal rebuild paths that currently manufacture synthetic updated rootfs/mount
  result objects must follow the same bare rootfs+mount model as `WithExec`

#### `core/directory.go`

##### `Directory` snapshot state

Specific change:

* remove `Parent dagql.ObjectResult[*Directory]` from `Directory`
* add a dedicated tiny lock for lazily-published snapshot state on `Directory`
  * `snapshotMu sync.RWMutex`
* add explicit readiness tracking for the snapshot state
  * `snapshotReady bool`
* add explicit snapshot delegation state
  * `snapshotSource dagql.ObjectResult[*Directory]`
* keep `Snapshot` on the object, but treat it as private state that must not be
  read directly outside helper methods
* keep the existing helper names but change the signatures and semantics:
  * `setSnapshot(ref bkcache.ImmutableRef) error`
  * `setSnapshotSource(src dagql.ObjectResult[*Directory]) error`
  * `getSnapshot() (bkcache.ImmutableRef, error)`
* delete `getParentSnapshot` entirely

Helper behavior:

* `setSnapshot`:
  * briefly takes `snapshotMu`
  * if `snapshotReady` is already true, returns an error
  * otherwise stores the snapshot, clears `snapshotSource`, and sets
    `snapshotReady = true`
  * in this phase, `setSnapshot` takes ownership of the passed ref directly
    rather than trying to solve the bigger ref-ownership model
* `setSnapshotSource`:
  * briefly takes `snapshotMu`
  * if `snapshotReady` is already true, returns an error
  * if `src.Self() == nil`, returns an error
  * otherwise stores the attached source result, clears `Snapshot`, and sets
    `snapshotReady = true`
* `getSnapshot`:
  * never triggers evaluation
  * reads `snapshotReady`, `Snapshot`, and `snapshotSource` under `snapshotMu`
  * unlocks before any recursive read-through
  * returns an error if `snapshotReady` is false
  * if `Snapshot != nil`, returns the stored snapshot
  * otherwise requires `snapshotSource.Self() != nil` and reads through
    `snapshotSource.Self().getSnapshot()`

Direct consequences in this file:

* all direct reads of directory snapshot state in helpers like:
  * `CacheUsageSize`
  * `CacheUsageIdentity`
  * `PersistedSnapshotRefLinks`
  * `EncodePersistedObject`
  must use the new locked helper behavior
* delegated directories do not own snapshots
  * `CacheUsageSize` returns `(0, false, nil)` unless `Snapshot != nil`
  * `CacheUsageIdentity` returns `("", false)` unless `Snapshot != nil`
  * `PersistedSnapshotRefLinks` emits links only for concrete snapshots, not
    source-only delegated state

##### Constructors and cloning

Concrete targets:

* `NewDirectoryChild`
* concrete snapshot-backed constructors used from `core/schema`

Specific change:

* `NewDirectoryChild(parent)` keeps cloning visible state from the parent:
  * `Dir`
  * `Platform`
  * `Services`
* but it resets all lazily-realized snapshot state:
  * `snapshotReady = false`
  * `Snapshot = nil`
  * `snapshotSource = dagql.ObjectResult[*Directory]{}`
* it does not retain the parent result anywhere on the child object
* add an explicit core constructor:
  * `NewDirectoryWithSnapshot(dir string, platform Platform, services ServiceBindings, snapshot bkcache.ImmutableRef) (*Directory, error)`
* this constructor initializes a ready snapshot-backed directory without
  requiring `core/schema` to reach into private fields
* in this phase, `NewDirectoryWithSnapshot(...)` should clone the passed ref
  before storing it
  * the caller keeps ownership of the original ref
  * broader ref-ownership cleanup is deferred to the next phase

##### `OnRelease`

Specific change:

* `Directory.OnRelease` only releases the concrete `Snapshot` owned by the
  directory itself
* it must not release `snapshotSource`
  * source-result lifecycle stays with dagql dependency tracking

##### Dependency attachment

Specific change:

* rename `Directory.AttachOwnedResults` to `Directory.AttachDependencyResults`
* `Directory` no longer attaches a broad hidden parent result
* it now attaches only `snapshotSource`, when that delegated source result is set
* the attach path rewrites `snapshotSource` to the attached result returned by
  dagql and returns that result as the directory's explicit dependency

##### Persistence

Concrete targets:

* `persistedDirectoryPayload`
* `EncodePersistedObject`
* `DecodePersistedObject`

Specific change:

* delete the parent-lazy persisted form entirely
  * `persistedDirectoryFormParentLazy`
  * `ParentResultID`
* replace it with a narrow delegated-source form:
  * `SourceResultID uint64`
* `EncodePersistedObject` only succeeds when `snapshotReady` is true
* encode behavior becomes:
  * if `Snapshot != nil`, persist the concrete snapshot-backed form
  * else if `snapshotSource.Self() != nil`, persist the delegated-source form
    using `SourceResultID`
  * else return `dagql.ErrPersistStateNotReady`
* decode behavior becomes:
  * snapshot form loads the concrete snapshot by container/object snapshot role
  * source form loads the attached source directory result by result ID and
    stores it in `snapshotSource`
* when decoding a delegated-source directory, clone `Services` from the source
  directory if available so service bindings still propagate correctly
* invalid mixed states must be rejected:
  * concrete `Snapshot` plus non-zero `snapshotSource`
  * `snapshotReady == true` with neither concrete snapshot nor source result

##### `Evaluate` / `Sync`

Specific change:

* delete `(*Directory).Evaluate`
* delete `(*Directory).Sync`

Replacement rule:

* any attached-result caller that needs evaluation must call
  `dagql.EngineCache(ctx).Evaluate(ctx, result)`
* no code in this file should call back into object-local evaluation helpers

##### Snapshot-read operations

Concrete targets:

* `Digest`
* `Entries`
* `Glob`
* `Search`
* `Exists`
* `Stat`
* `Export`

Specific change:

* these remain pure snapshot readers
* they should call only `getSnapshot()`
* they should not try to evaluate dependencies on demand
* schema/core callers are responsible for explicitly evaluating attached
  results before invoking them

##### `Subdirectory`

Target:

* `(*Directory).Subdirectory`

Specific change:

* keep the signature `Subdirectory(ctx, parent, subdir)`
* this method is now explicitly responsible for the existence/type check path:
  * fetch `dagql.EngineCache(ctx)`
  * evaluate the attached parent directory result
  * then call `parent.Self().Stat(...)` against already-realized state
* returned child directory is a pass-through delegate:
  * clone visible metadata from the parent
  * update `Dir`
  * set `LazyEval` to a callback that:
    * evaluates the attached parent result through dagql cache
    * calls `setSnapshotSource(parent)`

##### `Subfile`

Target:

* `(*Directory).Subfile`

Specific change:

* keep the signature `Subfile(ctx, parent, file)`
* this method also performs the eager existence/type check:
  * evaluate `parent` through dagql cache
  * call `parent.Self().Stat(...)`
* returned file is a pass-through delegate sourced from a directory result:
  * construct a `File`
  * set its `FileSnapshotSource.Directory = parent`
  * set `LazyEval` to a callback that:
    * evaluates `parent` through dagql cache
    * calls `setSnapshotSource(FileSnapshotSource{Directory: parent})`

##### Mutators that derive from a parent directory

Concrete targets:

* `WithNewFile`
* `WithPatch`
* `WithTimestamps`
* `WithNewDirectory`
* `Without`
* `WithSymlink`
* `Chown`

Specific change:

* all of these change from implicit `dir.Parent` ancestry to explicit attached
  parent result input:
  * `WithNewFile(ctx, parent dagql.ObjectResult[*Directory], ...)`
  * `WithPatch(ctx, parent dagql.ObjectResult[*Directory], patch string)`
  * `WithTimestamps(ctx, parent dagql.ObjectResult[*Directory], unix int)`
  * `WithNewDirectory(ctx, parent dagql.ObjectResult[*Directory], ...)`
  * `Without(ctx, parent dagql.ObjectResult[*Directory], opCall, paths...)`
  * `WithSymlink(ctx, parent dagql.ObjectResult[*Directory], target, linkName)`
  * `Chown(ctx, parent dagql.ObjectResult[*Directory], chownPath, owner)`
* each callback must:
  * fetch `dagql.EngineCache(ctx)`
  * evaluate `parent`
  * read `parent.Self().getSnapshot()`
  * perform the Buildkit work
  * publish the result with `setSnapshot(...)`
* no mutator in this group reads a hidden parent field

##### `WithDirectory`

Target:

* `(*Directory).WithDirectory`

Specific change:

* change the source input from `srcID DirectoryID` to
  `src dagql.ObjectResult[*Directory]`
* change the signature to take the receiver ancestry explicitly as well:
  * `WithDirectory(ctx, parent dagql.ObjectResult[*Directory], destDir string, src dagql.ObjectResult[*Directory], ...)`
* remove the internal `srcID.Load(...)` path from core
* callback behavior becomes:
  * evaluate `parent`
  * evaluate `src`
  * read `parent.Self().getSnapshot()`
  * read `src.Self().getSnapshot()`
  * preserve the existing direct-merge / copy-to-scratch / `COPY --link`
    optimization structure
  * publish only via `setSnapshot(...)`
* this keeps source-directory dependency explicit in the result graph instead of
  reloading it by ID inside core

##### `WithFile`

Target:

* `(*Directory).WithFile`

Specific change:

* change the signature to:
  * `WithFile(ctx, parent dagql.ObjectResult[*Directory], destPath string, src dagql.ObjectResult[*File], ...)`
* callback behavior becomes:
  * evaluate `parent`
  * evaluate `src`
  * read `parent.Self().getSnapshot()`
  * read `src.Self().getSnapshot()`
  * perform the existing copy/unpack logic
  * publish only via `setSnapshot(...)`

##### `Diff`

Target:

* `(*Directory).Diff`

Specific change:

* change the signature to:
  * `Diff(ctx, parent dagql.ObjectResult[*Directory], other dagql.ObjectResult[*Directory])`
* callback behavior becomes:
  * evaluate `parent`
  * evaluate `other`
  * read `parent.Self().getSnapshot()`
  * read `other.Self().getSnapshot()`
  * preserve the existing path-alignment checks
  * publish only via `setSnapshot(...)`

##### `WithChanges`

Target:

* `(*Directory).WithChanges`

Specific change:

* change the signature to:
  * `WithChanges(ctx, parent dagql.ObjectResult[*Directory], changes dagql.ObjectResult[*Changeset])`
* callback behavior becomes:
  * keep `parent` as the current working result for `srv.Select(...)` chaining
  * where it needs snapshot reads from that result, first evaluate the current
    attached directory result through dagql cache and then call `getSnapshot()`
  * do not read any hidden parent field
  * final publication still happens only via `setSnapshot(...)`

#### `core/file.go`

##### `File` snapshot state

Specific change:

* remove `Parent dagql.ObjectResult[*Directory]` from `File`
* add a dedicated tiny lock for lazily-published snapshot state on `File`
  * `snapshotMu sync.RWMutex`
* add explicit readiness tracking for the snapshot state
  * `snapshotReady bool`
* replace the old hidden parent fallback with an explicit source struct:
  * `type FileSnapshotSource struct {`
  * `Directory dagql.ObjectResult[*Directory]`
  * `File dagql.ObjectResult[*File]`
  * `}`
* add `snapshotSource FileSnapshotSource`
* keep `Snapshot` on the object, but require all access to go through helpers
* keep the existing helper names but change the semantics:
  * `setSnapshot(ref bkcache.ImmutableRef) error`
  * `setSnapshotSource(src FileSnapshotSource) error`
  * `getSnapshot() (bkcache.ImmutableRef, error)`
* delete `getParentSnapshot`

Helper behavior:

* `setSnapshot`:
  * briefly takes `snapshotMu`
  * errors if `snapshotReady` is already true
  * otherwise stores the concrete snapshot, clears `snapshotSource`, and marks
    `snapshotReady = true`
  * in this phase, `setSnapshot` takes ownership of the passed ref directly
* `setSnapshotSource`:
  * briefly takes `snapshotMu`
  * errors if `snapshotReady` is already true
  * errors if both `src.Directory` and `src.File` are set
  * errors if both are nil
  * stores the explicit source struct, clears `Snapshot`, and marks
    `snapshotReady = true`
* `getSnapshot`:
  * never triggers evaluation
  * reads `snapshotReady`, `Snapshot`, and `snapshotSource` under `snapshotMu`
  * unlocks before any recursive read-through
  * errors if `snapshotReady` is false
  * returns `Snapshot` when set
  * otherwise reads through exactly one delegated source:
    * `snapshotSource.File.Self().getSnapshot()`
    * or `snapshotSource.Directory.Self().getSnapshot()`

Direct consequences in this file:

* delegated files do not own snapshots
  * `CacheUsageSize` returns `(0, false, nil)` unless `Snapshot != nil`
  * `CacheUsageIdentity` returns `("", false)` unless `Snapshot != nil`
  * `PersistedSnapshotRefLinks` emits links only for concrete snapshots

##### Constructors and cloning

Concrete targets:

* `NewFileChild`
* concrete snapshot-backed constructors used from `core/schema`

Specific change:

* `NewFileChild(parent)` keeps cloning visible file metadata:
  * `File`
  * `Platform`
  * `Services`
* but resets all lazily-realized snapshot state:
  * `snapshotReady = false`
  * `Snapshot = nil`
  * `snapshotSource = FileSnapshotSource{}`
* it does not retain any hidden parent directory result
* add an explicit core constructor:
  * `NewFileWithSnapshot(file string, platform Platform, services ServiceBindings, snapshot bkcache.ImmutableRef) (*File, error)`
* in this phase, `NewFileWithSnapshot(...)` should clone the passed ref before
  storing it
  * the caller keeps ownership of the original ref
  * broader ref-ownership cleanup is deferred to the next phase

##### `OnRelease`

Specific change:

* `File.OnRelease` only releases the concrete `Snapshot` owned by the file
* it must not release delegated source results

##### Dependency attachment

Specific change:

* rename `File.AttachOwnedResults` to `File.AttachDependencyResults`
* `File` no longer attaches a broad parent directory
* it now attaches only explicit delegated snapshot sources:
  * `snapshotSource.Directory`
  * `snapshotSource.File`
* the attach path rewrites whichever source is set to the attached result
  returned by dagql

##### Persistence

Concrete targets:

* `persistedFilePayload`
* `EncodePersistedObject`
* `DecodePersistedObject`

Specific change:

* delete the parent-lazy persisted form entirely
  * `persistedFileFormParentLazy`
  * `ParentResultID`
* replace it with explicit source-result ID fields:
  * `DirectorySourceResultID uint64`
  * `FileSourceResultID uint64`
* `EncodePersistedObject` only succeeds when `snapshotReady` is true
* encode behavior becomes:
  * if `Snapshot != nil`, persist the concrete snapshot-backed form
  * else if `snapshotSource.Directory.Self() != nil`, persist that result ID
  * else if `snapshotSource.File.Self() != nil`, persist that result ID
  * else return `dagql.ErrPersistStateNotReady`
* decode behavior becomes:
  * concrete snapshot form loads the snapshot by snapshot-link role
  * delegated-directory form loads `DirectorySourceResultID`
  * delegated-file form loads `FileSourceResultID`
  * if both source-result ID fields are set, decode returns an error
* when decoding delegated-source files, clone `Services` from the loaded source
  object when appropriate
* invalid mixed states must be rejected:
  * concrete `Snapshot` plus any non-zero delegated source result ID
  * both delegated source result IDs set at once
  * `snapshotReady == true` with neither concrete snapshot nor delegated source

##### `Evaluate` / `Sync`

Specific change:

* delete `(*File).Evaluate`
* delete `(*File).Sync`

Replacement rule:

* any attached-result caller that needs evaluation must call
  `dagql.EngineCache(ctx).Evaluate(ctx, result)`

##### Snapshot-read operations

Concrete targets:

* `Contents`
* `Search`
* `Digest`
* `Stat`
* `Open`
* `Export`
* `Mount`
* `AsJSON`
* `AsEnvFile`

Specific change:

* all of these remain pure snapshot readers
* they should call only `getSnapshot()`
* they should not trigger evaluation themselves

##### `WithContents`

Target:

* `(*File).WithContents`

Specific change:

* change the signature to:
  * `WithContents(ctx, parent dagql.ObjectResult[*Directory], content []byte, permissions fs.FileMode, ownership *Ownership)`
* callback behavior becomes:
  * evaluate `parent`
  * read `parent.Self().getSnapshot()`
  * create the new file layer as today
  * publish only via `setSnapshot(...)`
* this removes the last hidden dependence on a stored parent directory result

##### File-to-file mutators

Concrete targets:

* `WithReplaced`
* `WithName`
* `WithTimestamps`
* `Chown`

Specific change:

* keep the parent file result explicit in the signature for all of these
* each callback must:
  * evaluate the attached parent file result through dagql cache
  * read `parent.Self().getSnapshot()`
  * preserve the existing filesystem work
  * publish only via `setSnapshot(...)`
* helper code inside these mutators that creates temporary source files should
  construct concrete snapshot-backed `File` values rather than reviving hidden
  parent fallback

#### `core/util.go`

##### Snapshot helper contract

Concrete targets:

* `Syncable`
* `fileOrDirectory`
* `mountObj`
* `getRefOrEvaluate`

Specific change:

* keep `Syncable` for non-filesystem types that still own their own `Sync()`
  semantics in this phase
* `Directory`, `File`, and `Container` drop out of `Syncable` once their
  object-local `Sync()` methods are deleted
* change the helper contract to match the new pure-read snapshot API:
  * `getSnapshot() (bkcache.ImmutableRef, error)`
  * `setSnapshot(bkcache.ImmutableRef) error`
* delete `getRefOrEvaluate`
  * implicit "evaluate while reading the ref" is exactly the behavior we are removing
* `mountObj` becomes a pure helper for already-evaluated objects:
  * call `obj.getSnapshot()`
  * error if the object has not already been evaluated
  * when `withSavedSnapshot(...)` is used, publish the committed snapshot
    through `obj.setSnapshot(...)`
* every caller that previously relied on `mountObj` or `getRefOrEvaluate` to
  force evaluation must evaluate the attached result explicitly first

#### `core/contenthash.go`

##### Content-hash helpers

Concrete targets:

* `GetContentHashFromDirectory`
* `GetContentHashFromFile`

Specific change:

* both helpers must evaluate the attached result through dagql cache before
  calling `Self().getSnapshot()`
* this keeps content hashing aligned with the explicit evaluate-then-read model

#### `core/changeset.go`

##### Explicit evaluation before snapshot access

Concrete targets:

* `withMountedDirs`
* `AsPatch`
* `Export`
* `newChangesetFromMerge`

Specific change:

* remove all `getRefOrEvaluate(...)` usage
* `withMountedDirs` must:
  * evaluate `ch.Before`
  * evaluate `ch.After`
  * then read `Self().getSnapshot()`
* `AsPatch` must do the same before mounting refs
* `Export` should stop relying on `mountObj(dir.Self())` to force evaluation
  * evaluate the attached `dir` result first
  * then mount `dir.Self().getSnapshot()`
* `newChangesetFromMerge` should use the new pure-read `afterDir.getSnapshot()`

##### Snapshot-backed temporary outputs

Concrete targets:

* `AsPatch`
* any helper that constructs a bare `File` or `Directory` from a known snapshot

Specific change:

* temporary patch files/directories created from already-known committed
  snapshots should use `NewFileWithSnapshot(...)` /
  `NewDirectoryWithSnapshot(...)` rather than reaching into old `LazyState`
  completion flags

#### `core/service.go`

##### Snapshot helpers

Concrete targets:

* `runAndSnapshotChanges`
* service-side snapshot object construction

Specific change:

* stop using `getRefOrEvaluate(source)`
* require callers to pass already-evaluated directory objects or evaluate the
  corresponding attached result before the helper is called
* snapshot directory objects constructed here from concrete refs should use
  `NewDirectoryWithSnapshot(...)`

#### `core/git_local.go`

##### Temporary file and snapshot reads

Concrete targets:

* `File(...)`
* `Cleaned(...)`

Specific change:

* `LocalGitRepository.File(...)` can keep returning a bare `*File`
  constructor-style value because it does not immediately read from that file
  in this codepath
* if a future helper here needs immediate readback from a git child file, it
  must not do `repo.Directory.Self().Subfile(...).Contents(...)` directly
* instead, it must stay in dagql selection space from the attached
  `repo.Directory` result and select `file(path: ...)` / `contents`
* the remaining snapshot reads in this file should keep following the explicit
  evaluate-then-read rules already covered above

#### `core/modfunc.go`

##### Direct `WithExec` caller

Specific change:

* keep the direct call to `Container.WithExec`
* update any assumptions there that derived rootfs/mount outputs are result-backed
* make sure this path uses the same lazy container / bare rootfs+mount model as
  the schema `withExec` path

##### Reading files selected from exec output

Concrete target:

* module-function result file readback after `ctrOutputDir.Self().Subfile(...)`

Specific change:

* stop using `ctrOutputDir.Self().Subfile(...)` followed by `Contents(...)`
* keep the attached directory result `ctrOutputDir` as the boundary
* read the module output by dagql selection:
  * `file(path: modMetaOutputPath)`
  * then `contents`
* do not construct a detached child file here just to read from it

### `core/schema`

#### `core/schema/util.go`

##### Public `sync`

Specific change:

* keep the field name `sync`
* change `Syncer(...)` so it is no longer generically constrained to
  `core.Syncable`
* `Syncer(...)` should branch at runtime:
  * if `self.Self()` exposes laziness for the converted filesystem model,
    evaluate the attached result through dagql cache and do not expect an
    object-local `Sync()`
  * otherwise, if `self.Self()` still implements legacy `core.Syncable`, call
    its existing `Sync()` path exactly as today
  * otherwise return an internal error explaining that the type does not
    support `sync`
* this keeps public behavior stable while limiting the hard cut to the
  filesystem-laziness slice we are actually converting now

#### `core/schema/envfile.go`

##### File-to-envfile conversion

Concrete target:

* `asEnvFile`

Specific change:

* stop dropping the attached file result immediately into
  `parent.Self().AsEnvFile(...)`
* keep the attached file result as the boundary and read file contents there
  first through dagql selection or explicit attached-result evaluation
* then build the `EnvFile` from those contents

#### `core/schema/llm.go`

##### Prompt-file ingestion

Concrete target:

* `withPromptFile`

Specific change:

* stop loading the file ID and then passing only `file.Self()` into
  `llm.WithPromptFile(...)`
* keep the attached file result as the boundary and read the prompt contents
  through dagql first
* then call `llm.WithPrompt(...)` with the resolved contents

#### `core/schema/modulesource.go`

##### Generated-context helper file reads

Concrete targets:

* `.gitattributes` read in generated-context update
* `.gitignore` read in generated-context update

Specific change:

* stop selecting an attached file result and then immediately calling
  `gitAttrsFile.Self().Contents(...)` / `gitIgnoreFile.Self().Contents(...)`
* keep the attached file result as the boundary and select `contents` from it
  through dagql
* preserve the current not-found behavior by keeping the existing first-stage
  `file(path: ...)` selection/error handling

#### `core/schema/container.go`

##### Receiver signature cleanup for evaluation

Specific change:

* any resolver in this file that currently takes `*core.Container` and needs to
  force evaluation must switch to `dagql.ObjectResult[*core.Container]`
* evaluation then happens through `dagql.EngineCache(ctx).Evaluate(ctx, parent)`
  rather than `parent.Evaluate(ctx)` / `parent.Sync(ctx)`

Concrete targets include:

* `rootfs`
* `from`
* `stdout`
* `stderr`
* `combinedOutput`
* `exitCode`
* `exists`
* `stat`
* any other resolver in this file that currently depends on bare-object
  `Evaluate` / `Sync`

##### `from`

Specific change:

* stop using synthetic updated-rootfs child results in the canonical-image path
* when `from` initializes container rootfs, store bare rootfs state on the
  container instead
* keep the `from` container itself as the result boundary

##### `withExec`

Specific change:

* keep returning `dagql.NewObjectResultForCurrentCall(ctx, srv, ctr)` for the
  container result itself
* `withExec` should no longer rely on synthetic child result creation inside
  `core.Container.WithExec`
* schema does not need a special child-result API for rootfs/mount outputs in
  this model

##### Exec output readers

Concrete targets:

* `stdout`
* `stdoutLegacy`
* `stderr`
* `stderrLegacy`
* `combinedOutput`
* `exitCode`

Specific change:

* all of these paths must stop relying on bare-object `Evaluate`
* they should evaluate the attached container result through dagql cache before
  calling the metadata reader helpers on the bare `*Container`
* the legacy fallback-to-`withExec(args: [])` behavior can stay, but the
  resulting container must also follow the new bare rootfs/mount model

##### `rootfs`

Specific change:

* if the container rootfs source is result-backed, keep returning the existing
  result-backed/bare value from that source
* if the container rootfs source is bare and the container is lazy, evaluate
  the attached container result through dagql cache before returning the bare
  `*Directory`
* do not fabricate an internal rootfs child result just to keep up appearances

##### `directory` / `file`

Specific change:

* if routing lands on a result-backed source, keep the current behavior
* if routing lands on a bare rootfs/mount source:
  * evaluate the attached container result first when needed
  * perform the directory/file operation on the bare object
  * wrap the returned bare object with `dagql.NewObjectResultForCurrentCall(...)`
    at the current field-selection boundary
  * preserve that bare object's existing `LazyEval` callback when wrapping it

##### Rootfs/mount mutators

Concrete targets:

* `withRootfs`
* `withMountedDirectory`
* `withMountedFile`
* `withDirectory`
* `withFile`
* `withFiles`
* `withoutDirectory`
* `withoutFile`
* `withoutFiles`

Specific change:

* these schema resolvers keep taking attached result-backed inputs from IDs as
  they do today
* when they call into core container mutators, they must now pass the attached
  parent container result explicitly as well
* the core container methods they call now branch on whether the target rootfs
  or mount slot is result-backed or bare
* no resolver in this group should attempt to recreate synthetic child results
  for `withExec`/`from`/builtin-rootfs bare state

Concrete additional targets in the same bucket:

* `withMountedCache`
* `withMountedTemp`
* `withoutMount`
* `withNewFile`
* `withNewFileLegacy`
* `withSymlink`

These do not all take filesystem object inputs, but they all mutate container
filesystem state and therefore must preserve the result-backed-or-bare slot
representation instead of normalizing everything back into synthetic child results.

##### Read helpers that currently force evaluation

Concrete targets:

* `exists`
* `stat`

Specific change:

* where these currently force evaluation through object-local `Evaluate`,
  switch them to evaluate the attached container result through dagql cache
  when the receiver is still lazy
* then route the actual filesystem query through the new result-backed-or-bare
  container source helpers

##### Fallbacks and dynamic-input helpers

Concrete targets:

* `withFile` directory-source fallback
* `withMountedCacheDynamicInputs`

Specific change:

* any path here that currently assumes a source always has an ID/result form
  must stay explicitly limited to result-backed outer API inputs
* do not try to make these helper paths operate on bare rootfs/mount state
  inside a container

##### Export / publish / import

Concrete targets:

* `publish`
* `asTarball`
* `exportImage`
* `export`
* `exportLegacy`
* `asTarball` helper selection path
* `import_`

Specific change:

* anywhere these resolvers currently evaluate bare containers through
  `parent.Self().Evaluate(ctx)` must switch to dagql-cache-driven evaluation
* the core export/publish/import helpers they call must already support
  result-backed-or-bare rootfs state
* no export/import resolver should assume `container.FS` is always a
  result-backed object result

##### Terminal

Concrete targets:

* `terminal`
* `terminalLegacy`

Specific change:

* terminal-related resolvers keep using the attached container result as the
  outer evaluation boundary
* the underlying terminal helpers must already follow the new bare rootfs+mount
  model and must not resurrect synthetic updated child results

#### `core/schema/directory.go`

##### Concrete directory constructors

Concrete targets:

* `immutableRef`
* `directory`

Specific change:

* stop constructing snapshot-backed directories by reaching into old
  `LazyState` completion flags from schema
* use `core.NewDirectoryWithSnapshot(...)` for concrete snapshot-backed
  directories instead
* these returned directory results are immediately ready and do not carry a
  `LazyEval` callback

##### `subdirectory`

Specific change:

* keep calling `parent.Self().Subdirectory(ctx, parent, args.Path)`
* returned bare directory already carries the explicit pass-through `LazyEval`
  callback described in `core/directory.go`
* wrap it with `dagql.NewObjectResultForCurrentCall(...)` and do not mutate any
  hidden parent field from schema

##### Snapshot-reading resolvers

Concrete targets:

* `entries`
* `glob`
* `search`
* `digest`
* `exists`
* `stat`
* `export`
* `exportLegacy`

Specific change:

* each of these resolvers must:
  * evaluate the attached parent directory result through dagql cache
  * then call the corresponding bare directory read method
* no snapshot-reading resolver should rely on object-local auto-evaluation

##### Mutators with explicit parent dependency

Concrete targets:

* `withNewDirectory`
* `withDirectory`
* `withTimestamps`
* `withPatch`
* `withNewFile`
* `withFile`
* `withoutDirectory`
* `withoutFile`
* `withoutFiles`
* `diff`
* `withChanges`
* `withSymlink`
* `chown`

Specific change:

* all of these keep creating child results with `core.NewDirectoryChild(parent)`
* all of them stop assigning `LazyInit`
* all of them assign `LazyEval`
* each call into core passes explicit attached result dependencies instead of
  relying on hidden parent fields:
  * parent directory receiver when ancestry is needed
  * loaded attached source directory/file results for source inputs
  * loaded attached `Changeset` result for `withChanges`
  * loaded attached other directory result for `diff`

##### `withDirectory`

Specific change:

* load `args.Source` / `args.Directory` to an attached
  `dagql.ObjectResult[*core.Directory]`
* pass that attached result directly into `dir.WithDirectory(...)`
* stop forcing core to reload the source directory by ID

##### `withFile`

Specific change:

* keep the explicit parent receiver result
* keep loading `args.Source` to an attached `dagql.ObjectResult[*core.File]`
* pass that attached source result directly into `dir.WithFile(...)`
* when the directory-source fallback path triggers, keep it entirely in
  schema-level selection space by selecting `withDirectory(...)` on the parent
  result; do not try to recreate hidden ancestry inside core

##### `withPatchFile`

Specific change:

* keep the current "read patch contents in schema, then call `withPatch`"
  lowering for now
* but make the read path explicit:
  * load the attached patch-file result
  * then read `contents` from that attached result through dagql selection
  * do not drop to `patchFile.Self().Contents(...)`

##### `file`

Specific change:

* keep calling `parent.Self().Subfile(ctx, parent, args.Path)`
* wrap the returned bare file with `dagql.NewObjectResultForCurrentCall(...)`
* let the returned file's own `LazyEval` handle delegation to the attached
  parent directory result
* `GetContentHashFromFile(...)` already becomes explicit-evaluate in
  `core/contenthash.go`, so content-hash decoration still works

##### `findUp`, `applyDockerIgnore`, `dockerBuild`, `terminal`

Concrete targets:

* `findUp`
* `getDockerIgnoreFileContent`
* `applyDockerIgnore`
* `dockerBuild`
* `terminal`

Specific change:

* any helper here that needs immediate readback from a would-be child
  subfile/subdirectory must stay in dagql selection space from the attached
  parent directory result
* `getDockerIgnoreFileContent` is the clearest example:
  * select `file(path: ...)` from the attached parent directory result
  * then select `contents`
  * do not call `parent.Self().Subfile(...)` followed by a bare-file read
* `dockerBuild` and `findUp` keep working on attached directory results, but
  any snapshot reads they trigger must happen only after explicit evaluation

#### `core/schema/file.go`

##### Concrete file constructors

Concrete targets:

* top-level `file` query constructor

Specific change:

* where schema creates a concrete file from known contents and a scratch
  directory, keep the scratch-directory selection result explicit
* call the new `File.WithContents(ctx, parentDirResult, ...)`
* if that constructor path chooses to realize immediately, use the new concrete
  `core.NewFileWithSnapshot(...)` path rather than old `LazyInitComplete`

##### Snapshot-reading resolvers

Concrete targets:

* `contents`
* `size`
* `stat`
* `digest`
* `search`
* `export`
* `exportLegacy`
* `asJSON`

Specific change:

* switch resolvers that need file contents/snapshot access to use
  `dagql.ObjectResult[*core.File]` receivers where needed
* each of these resolvers must:
  * evaluate the attached file result through dagql cache
  * then call the bare file read helper
* `name` stays a trivial bare-field resolver and does not need evaluation

##### File mutators

Concrete targets:

* `withName`
* `withReplaced`
* `withTimestamps`
* `chown`

Specific change:

* keep using `core.NewFileChild(parent)`
* stop assigning `LazyInit`
* assign `LazyEval`
* pass the attached parent file result explicitly into the core mutator

#### `core/schema/query.go`

##### Schema JSON file construction

Specific change:

* stop constructing files by setting `Parent` / `LazyInitComplete` directly
* keep the scratch directory result explicit
* call the new `File.WithContents(ctx, parentDirResult, ...)` API
* if the file is realized immediately in this path, finish it through the new
  `core.NewFileWithSnapshot(...)` behavior

#### `core/schema/http.go`

##### HTTP file construction

Specific change:

* stop relying on `LazyInitComplete`
* construct the HTTP result file through `core.NewFileWithSnapshot(...)`
  because this path already has the immutable snapshot ref in hand

#### `core/schema/host.go`

##### Host directory/file construction

Concrete targets:

* `directory`
* `file`

Specific change:

* stop constructing snapshot-backed directory/file objects by reaching into old
  lazy-state internals from schema
* use `core.NewDirectoryWithSnapshot(...)` / `core.NewFileWithSnapshot(...)`
  instead
* keep content-digest decoration unchanged

### Validation

Before implementation starts, the whiteboard should reflect these explicit
checks:

* delegated `Directory` persistence uses attached source-result IDs, not hidden
  parent state
* delegated `Directory` persistence round-trips correctly
* delegated `File` persistence cleanly distinguishes directory-source vs
  file-source without a separate object-level tag type
* delegated `File` persistence round-trips correctly for both:
  * directory-source delegation
  * file-source delegation
* delegated directory/file objects do not claim snapshot ownership in
  cache-usage accounting
* unchanged bare container slot delegation works across one or more child
  container operations
* `Container.OnRelease` releases bare rootfs/mount ownership correctly without
  touching result-backed sources
* result-backed and bare container slots behave equivalently for read APIs once
  evaluated
* `Cache.Evaluate` waiter/cancellation behavior matches the existing
  `getOrInitCall` / `wait` semantics
* every public resolver that reads snapshot-backed directory/file/container
  state evaluates the attached result first
* every helper that needs immediate readback from a would-be child
  directory/file stays in dagql selection space from an attached parent/source
  result rather than trying to evaluate a detached child object
* no remaining directory/file/container plan item depends on:
  * hidden `Parent` fields
  * `getParentSnapshot`
  * object-local `Evaluate` / `Sync`
  * synthetic `withExec` child results
