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

# Lazy Persistence

## Design
* Keep `dagql.Cache.Evaluate(ctx, results...)` as the evaluation API.
* Stop treating lazy realization as an opaque closure installed directly on the owning object.
* `ResultCall` remains the dagql identity / cache-key / lineage record.
* Lazy realization becomes explicit structured object state owned by the object itself.
* We are not going to serialize closures.
* We are not going to reconstruct lazy behavior by replaying `ResultCall`.
* `ResultCall.Field` is only the authoritative discriminator for which public lazy type to decode.
* The actual lazy args come from explicit structured lazy payloads encoded by each concrete lazy implementation.
* Internal container-only lazy states are reconstructed by container decode, not by a duplicated `lazyKind` string and not by top-level `Directory` / `File` decode.
* Materialization clears `obj.Lazy = nil`.
* `Directory.Services` / `File.Services` stay explicit persisted object state.
* `AfterEvaluate` / `OnEvaluateComplete` are removed from `LazyState`; they are dead.
* There is no `Kind()` method and there is no `lazyKind` payload field.

The shared abstraction is:

```go
type Lazy[T dagql.Typed] interface {
    Evaluate(context.Context, T) error
    AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error)
    EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error)
}

type LazyState struct {
    Mu       *sync.Mutex
    Complete bool
}
```

Each core object gets a named typed lazy field instead of embedded callback state. For example:

```go
type Directory struct {
    Dir      string
    Platform Platform
    Services ServiceBindings

    Lazy Lazy[*Directory]

    snapshotMu        sync.RWMutex
    snapshotReady     bool
    snapshotSource    dagql.ObjectResult[*Directory]
    Snapshot          bkcache.ImmutableRef
    persistedResultID uint64
}
```

The real container complexity that we keep is:
* result-backed rootfs / mount children
* bare owned `*Directory` / `*File` rootfs / mount children

What we delete is the accidental callback machinery that hoisted bare-child pending work into chained container-level callbacks.

## Implementation Plan

### core/lazy_state.go
#### Replace callback-based lazy state with single-flight state
* Delete:
  * `AfterEvaluate []func(context.Context)`
  * `OnEvaluateComplete(...)`
* Replace the file with:
  * the generic typed `Lazy[T]` interface
  * the slim single-flight/completion helper `LazyState`

```go
type Lazy[T dagql.Typed] interface {
    Evaluate(context.Context, T) error
}

type LazyState struct {
    Mu       *sync.Mutex
    Complete bool
}

func NewLazyState() LazyState {
    return LazyState{Mu: new(sync.Mutex)}
}

func (lazy *LazyState) Evaluate(
    ctx context.Context,
    typeName string,
    run func(context.Context) error,
) error {
    if lazy.Complete {
        return nil
    }
    if lazy.Mu == nil {
        return fmt.Errorf("invalid %s lazy state: missing mutex", typeName)
    }

    lazy.Mu.Lock()
    defer lazy.Mu.Unlock()

    if lazy.Complete {
        return nil
    }
    if err := run(ctx); err != nil {
        return err
    }
    lazy.Complete = true
    return nil
}
```

* Promote shared eager-validation helpers into core:
  * `core.ValidateFileName(...)`
  * `core.ParseDirectoryOwner(...)`
  * `core.ParseFileOwner(...)`

### core/directory.go
#### Replace callback state with a typed lazy field
* Remove the embedded `LazyState` and callback field from `Directory`.
* `Directory` stores `Lazy[*Directory]` directly; no separate directory-specific lazy interface is needed.

* `NewDirectoryChild(...)` clears lazy state instead of cloning pending work.
* `Directory.LazyEvalFunc()` delegates to `dir.Lazy`.
* `setSnapshot(...)` and `setSnapshotSource(...)` clear `dir.Lazy = nil`.

#### Concrete lazy directory types
* Public lazy directory operations:
  * `DirectoryWithDirectoryLazy`
  * `DirectoryWithFileLazy`
  * `DirectoryWithPatchFileLazy`
  * `DirectoryWithNewFileLazy`
  * `DirectoryWithNewDirectoryLazy`
  * `DirectoryWithTimestampsLazy`
  * `DirectoryDiffLazy`
  * `DirectoryWithChangesLazy`
  * `DirectoryWithoutLazy`
  * `DirectoryWithSymlinkLazy`
  * `DirectoryChownLazy`
* Internal container-only state:
  * `DirectoryFromSourceLazy`

Representative shape:

```go
type DirectoryWithDirectoryLazy struct {
    LazyState

    Parent                           dagql.ObjectResult[*Directory]
    DestDir                          string
    Source                           dagql.ObjectResult[*Directory]
    Filter                           CopyFilter
    Owner                            string
    Permissions                      *int
    DoNotCreateDestPath              bool
    AttemptUnpackDockerCompatibility bool
    RequiredSourcePath               string
    DestPathHintIsDirectory          bool
    CopySourcePathContentsWhenDir    bool
}
```

Each concrete lazy type owns:
* `Evaluate(ctx, dir)`
* `AttachDependencies(...)`
* `EncodePersisted(...)`

#### AttachDependencyResults delegates to the concrete lazy op
* `Directory.AttachDependencyResults(...)` continues to normalize `snapshotSource` itself.
* After that, it delegates to `dir.Lazy.AttachDependencies(...)`.

#### Persisted directory payload
* Keep:
  * `snapshot`
  * `source`
  * `lazy`

```go
const (
    persistedDirectoryFormSnapshot = "snapshot"
    persistedDirectoryFormSource   = "source"
    persistedDirectoryFormLazy     = "lazy"
)

type persistedDirectoryPayload struct {
    Form     string          `json:"form"`
    Dir      string          `json:"dir,omitempty"`
    Platform Platform        `json:"platform"`
    Services ServiceBindings `json:"services,omitempty"`

    SourceResultID uint64          `json:"sourceResultID,omitempty"`
    LazyJSON       json.RawMessage `json:"lazyJSON,omitempty"`
}
```

* `EncodePersistedObject(...)`:
  * `snapshot` if materialized with a snapshot
  * `source` if the ready state is source-backed
  * `lazy` if `dir.Lazy != nil`
  * otherwise fail loudly
* `DecodePersistedObject(...)`:
  * handles `snapshot` and `source` as today
  * handles `lazy` by requiring a non-nil `call`, switching on `call.Field`, unmarshalling `LazyJSON` into the corresponding concrete lazy struct, and then resetting `lazy.LazyState = NewLazyState()`
* `DirectoryFromSourceLazy` is internal-only:
  * top-level directory decode does not switch on it
  * nested container decode reconstructs it when a value-backed container child is a pending source-backed shell
  * it is additive to `snapshotSource`, not a replacement for it
  * `Evaluate(...)` just `cache.Evaluate(ctx, source)` and then clears `dir.Lazy`
  * it does not replace or mutate the existing `snapshotSource`
  * `AttachDependencies(...)` does not return the source as an extra dependency, because `snapshotSource` remains the structural reference already handled by `Directory.AttachDependencyResults(...)`

#### Concrete per-op notes
* `DirectoryWithFileLazy` stores a `dagql.ObjectResult[*File]`
* `DirectoryWithPatchFileLazy` stores a patch file result and uses the file snapshot directly during evaluation
* `DirectoryWithNewFileLazy` stores inline content bytes; this is accepted explicitly
* `DirectoryWithChangesLazy` stores `Parent` and `Changes`
* `DirectoryWithChangesLazy.Evaluate(...)` should:
  * `cache.Evaluate(ctx, Parent, Changes)`
  * `patch, err := Changes.Self().AsPatch(ctx)` as the authoritative patch source
  * apply that patch via the same direct core helper used by `DirectoryWithPatchFileLazy`
  * `paths, err := Changes.Self().ComputePaths(ctx)`
  * apply removals via the same direct core helper used by `DirectoryWithoutLazy`
  * finalize by setting/cloning the resulting snapshot
* If patch is empty and removals are empty, keep current behavior and clone the final snapshot; do not add no-op equivalence teaching here unless we decide that explicitly later.
* `DirectoryWithoutLazy` stores the final normalized path list
* `DirectoryWithoutLazy.Evaluate(...)` must preserve the current no-op equivalence behavior:
  * source of truth is `dagql.CurrentCall(ctx)`, not a stored `opCall` field on the lazy struct
  * if no paths were actually removed, call `cache.TeachCallEquivalentToResult(ctx, sessionID, currentCall, parent)`
* `DirectoryDiffLazy` stores already-rebased effective args from schema shaping

### core/schema/directory.go
#### Install structured lazy directory objects directly
* Schema resolvers stop asking core for callbacks.
* They do current eager validation / shaping, then install the concrete lazy struct directly on the new child directory.
* Resolver-time shaping that must stay in schema:
  * `withDirectory` old arg compatibility (`source` vs `directory`)
  * `withFile` directory-source fallback
  * `diff` path rebasing
  * eager filename validation
  * eager owner parsing

#### Patch lowering
* Keep both public APIs:
  * `withPatch(patch: String)`
  * `withPatchFile(patch: FileID)`
* The core lazy implementation is canonical on the file-backed form.
* The string form lowers in schema by creating a normal file DAG object and then installing `DirectoryWithPatchFileLazy`.
* Do not add a string-form core lazy op.

#### Lowerings that stay lowerings
* `withFiles` lowers to repeated `withFile`
* `withoutDirectory` / `withoutFile` / `withoutFiles` lower to `DirectoryWithoutLazy`

### core/file.go
#### Replace callback state with a typed lazy field
* Remove the embedded `LazyState` and callback field from `File`.
* `File` stores `Lazy[*File]` directly; no separate file-specific lazy interface is needed.

* `File.LazyEvalFunc()` delegates to `file.Lazy`.
* `setSnapshot(...)` and `setSnapshotSource(...)` clear `file.Lazy = nil`.

#### Concrete lazy file types
* Public lazy file operations:
  * `FileWithNameLazy`
  * `FileWithReplacedLazy`
  * `FileWithTimestampsLazy`
  * `FileChownLazy`
* Internal container-only state:
  * `FileFromSourceLazy`

#### `withName` must carry `SourcePath`
* `withName` cannot be reconstructed from the visible final file path alone.
* The lazy struct stores:
  * `SourcePath`
  * `Name`

#### Persisted file payload
* File persistence mirrors directory:

```go
const (
    persistedFileFormSnapshot = "snapshot"
    persistedFileFormSource   = "source"
    persistedFileFormLazy     = "lazy"
)

type persistedFilePayload struct {
    Form                    string          `json:"form"`
    File                    string          `json:"file,omitempty"`
    Platform                Platform        `json:"platform"`
    Services                ServiceBindings `json:"services,omitempty"`
    DirectorySourceResultID uint64          `json:"directorySourceResultID,omitempty"`
    FileSourceResultID      uint64          `json:"fileSourceResultID,omitempty"`
    LazyJSON                json.RawMessage `json:"lazyJSON,omitempty"`
}
```

* `DecodePersistedObject(...)` handles:
  * `snapshot`
  * `source`
  * `lazy` by switching on `call.Field`, unmarshalling the corresponding concrete lazy struct, and then resetting `lazy.LazyState = NewLazyState()`
* `FileFromSourceLazy` is internal-only and is reconstructed by nested container decode.
* It is additive to `FileSnapshotSource`, not a replacement for it.
* `Evaluate(...)` just evaluates the existing source and then clears `file.Lazy`.
* This must handle both:
  * file-backed `FileSnapshotSource`
  * directory-backed `FileSnapshotSource`

#### Explicit boundary
* `File.WithContents(...)` remains unsupported in this migrated slice.
* If a pending lazy `WithContents` reaches persistence, fail loudly.

### core/schema/file.go
#### Install structured lazy file objects directly
* Schema resolvers install concrete lazy structs directly on the new file child.
* Keep current eager shaping rules in schema.
* `withName` explicitly captures `SourcePath` from the parent before rename.
* `chown` continues to do eager owner parsing in schema.

#### `Query.file(...)` stays a lowering
* `Query.file(name, contents, permissions)` remains:
  * `directory()`
  * `withNewFile(...)`
  * `file(...)`
* It is not a separate migrated file lazy operation.

### core/changeset.go
#### Keep changeset as a helper, not a new lazy object family
* We do not need a `ChangesetLazy` model.
* `DirectoryWithChangesLazy.Evaluate(...)` calls changeset helpers directly.
* If the current helper surface is awkward, add a direct core helper that returns:
  * patch file / patch ref
  * removals
* Do not bounce back through schema selectors.

### core/container.go
#### Replace callback state with a typed lazy field
* Remove the embedded `LazyState` and callback field from `Container`.
* `Container` stores `Lazy[*Container]` directly; no separate container-specific lazy interface is needed.

#### Keep the real complexity, delete the accidental complexity
* Keep the legitimate distinction between:
  * result-backed rootfs / mount children
  * bare owned `*Directory` / `*File` rootfs / mount children
* Delete:
  * `configureBareSourceLazyInit(...)`
  * `setLazyInit(...)`
  * `wrapLazyInitWithContainerEval(...)`

#### Bare owned source-backed shells become first-class structured lazy state
* Use:
  * `DirectoryFromSourceLazy`
  * `FileFromSourceLazy`
* Cloned bare children keep their source-backed ready form and also carry the internal pending shell lazy:

```go
func cloneBareDirectoryForContainerChild(src *Directory, source dagql.ObjectResult[*Directory]) *Directory {
    if src == nil {
        return nil
    }
    cp := *src
    cp.Services = slices.Clone(cp.Services)
    cp.snapshotMu = sync.RWMutex{}
    cp.snapshotReady = true
    cp.snapshotSource = source
    cp.Snapshot = nil
    cp.Lazy = &DirectoryFromSourceLazy{
        LazyState: NewLazyState(),
        Source:    source,
    }
    return &cp
}
```

The file analog works the same way with `FileSnapshotSource`.
* The semantic split is:
  * `snapshotSource` / `FileSnapshotSource` remain the structural source-of-truth and selection fast path
  * `DirectoryFromSourceLazy` / `FileFromSourceLazy` only mean “this owned shell still has pending evaluation work”
  * evaluating the lazy shell clears `Lazy`, but keeps the existing source-backed ready form intact
* `Container` / `Directory` / `File` attachment should continue to treat source-backed fields as structural references and must not double-return them as extra dependency edges.

#### Container pendingness is synthesized, not chained
* `Container.LazyEvalFunc()` returns a function if either:
  * `container.Lazy != nil`
  * or any owned bare rootfs / mount child still has `Lazy != nil`
* If there is no top-level container lazy, the synthesized evaluator just evaluates the owned bare children in place.

#### Ordinary filesystem mutation paths stop hoisting child work into container callback chains
* For ordinary fs mutations:
  * `withDirectory`
  * `withFile`
  * `withoutDirectory`
  * `withoutFile`
  * `withSymlink`
* container no longer builds callback chains.
* These operations become top-level structured container lazy ops whenever the output stays pending on a bare owned child:
  * `ContainerWithDirectoryLazy`
  * `ContainerWithFileLazy`
  * `ContainerWithoutPathLazy`
  * `ContainerWithSymlinkLazy`
* This is required for persistence:
  * nested bare `Directory` / `File` values do not get an authoritative `call.Field` during container decode
  * so a retained pending bare-child mutation cannot be reconstructed safely unless the container result itself carries the recipe
* Instead:
  * if the target child is result-backed, keep using that result-backed child and its own lazy behavior
  * if the target child is a bare owned value, keep the child as the output shell and attach the pending recipe to the container via one of the top-level `Container*Lazy` types above
  * `Container*Lazy.Evaluate(...)` is responsible for:
    * evaluating `state.Parent`
    * evaluating any still-pending owned child shell it needs as input
    * applying the directory/file mutation to the output shell
    * clearing `container.Lazy` after success
* Do not store an additional ordinary `Directory` / `File` lazy payload on the nested child for these container operations.
  * the authoritative reconstruction source is the top-level container call
  * the nested child remains only the output shell layout that top-level container decode rewires

#### Top-level lazy container operations
* `from` remains a real top-level lazy container operation.
* Add `ContainerFromLazy`.
* Ordinary pending bare-child fs mutations are also real top-level lazy container operations:
  * `ContainerWithDirectoryLazy`
  * `ContainerWithFileLazy`
  * `ContainerWithoutPathLazy`
  * `ContainerWithSymlinkLazy`
* `withExec` is also a real top-level lazy container operation and is handled by the shared exec-state model below.
* `ContainerFromLazy` must preserve current `from` identity behavior exactly:
  * schema still applies the existing `WithContentDigest(...)` override after constructing the result
  * the structured lazy refactor must not change `from` identity semantics

#### Persisted container payload
* Container persistence supports:
  * ready/materialized containers
  * lazy top-level container ops
* For the first cut, keep the current persistence restriction for containers with:
  * `Services`
  * `Secrets`
  * `Sockets`
* Any pending lazy `withExec` on such a container remains non-persistable for now and should fail loudly during persistence.
* FIXME: remove this restriction immediately after the first cut by adding explicit structural persistence for services, secrets, and sockets.

```go
const (
    persistedContainerFormReady = "ready"
    persistedContainerFormLazy  = "lazy"
)

type persistedContainerPayload struct {
    Form     string          `json:"form"`
    // existing stable container self-state...
    LazyJSON json.RawMessage `json:"lazyJSON,omitempty"`
}
```

* For top-level lazy container decode:
  * `call.Field` selects the public lazy op type
  * `LazyJSON` carries the concrete structured args
* Container self-state is the source of truth for nested output shell layout:
  * `FSValue`
  * `Mounts[i].DirectorySourceValue`
  * `Mounts[i].FileSourceValue`
* Nested bare-child source shells and exec-output shells are reconstructed by container nested value decode.
* Container value-backed encode/decode must therefore use dedicated helpers instead of blindly calling top-level `Directory.EncodePersistedObject(...)` / `File.EncodePersistedObject(...)` on nested values.

#### Nested container-only value forms
* Container-only nested directory/file values need internal forms in addition to the ordinary top-level `snapshot` / `source` object payloads.
* These are not new public `Directory` / `File` persistence forms.
* They are container-internal wrappers around the normal directory/file payloads:

```go
const (
    persistedContainerValueFormPlain         = "plain"
    persistedContainerValueFormSourcePending = "sourcePending"
    persistedContainerValueFormOutputPending = "outputPending"
)

type persistedContainerDirectoryValue struct {
    Form  string          `json:"form"`
    Value json.RawMessage `json:"value"`
}

type persistedContainerFileValue struct {
    Form  string          `json:"form"`
    Value json.RawMessage `json:"value"`
}

type decodedContainerDirectoryValue struct {
    Dir  *Directory
    Kind string
}

type decodedContainerFileValue struct {
    File *File
    Kind string
}
```

* `plain` means:
  * decode the embedded normal directory/file payload
  * do not add internal lazy state
* `sourcePending` means:
  * decode the embedded normal source-backed directory/file payload
  * attach `DirectoryFromSourceLazy` / `FileFromSourceLazy`
* `outputPending` means:
  * decode the embedded normal directory/file placeholder payload
  * do not attach any lazy state yet
  * return a marker so top-level lazy container decode can attach the generic container-output wrapper afterward
* Container nested decode helpers should therefore return both:
  * the decoded `*Directory` / `*File`
  * the internal container-only value kind
* Container nested encode helpers should therefore inspect the nested value before encoding:
  * `DirectoryFromSourceLazy` / `FileFromSourceLazy` -> wrap the underlying ordinary source-backed payload as `sourcePending`
  * `DirectoryFromContainerLazy` / `FileFromContainerLazy` -> wrap the underlying ordinary placeholder payload as `outputPending`
  * anything else -> wrap the ordinary object payload as `plain`

Representative helper shape:

```go
func encodePersistedContainerDirectoryValue(ctx context.Context, cache dagql.PersistedObjectCache, dir *Directory) (json.RawMessage, error) {
    switch lazy := dir.Lazy.(type) {
    case *DirectoryFromSourceLazy:
        payload, err := encodeDirectoryAsOrdinarySourcePayload(ctx, cache, dir)
        if err != nil {
            return nil, err
        }
        return json.Marshal(persistedContainerDirectoryValue{
            Form:  persistedContainerValueFormSourceShell,
            Value: payload,
        })
    case *DirectoryFromContainerLazy:
        payload, err := encodeDirectoryAsOrdinaryShellPayload(ctx, cache, dir)
        if err != nil {
            return nil, err
        }
        return json.Marshal(persistedContainerDirectoryValue{
            Form:  persistedContainerValueFormOutputShell,
            Value: payload,
        })
    default:
        payload, err := dir.EncodePersistedObject(ctx, cache)
        if err != nil {
            return nil, err
        }
        return json.Marshal(persistedContainerDirectoryValue{
            Form:  persistedContainerValueFormPlain,
            Value: payload,
        })
    }
}
```

The file helper is the analogous `encodePersistedContainerFileValue(...)`.

### core/container_exec.go
#### Replace gate callback spray with one shared explicit exec state
* `withExec` is one deferred action with multiple sibling outputs:
  * the container itself
  * the output rootfs
  * writable mounted directories
  * writable mounted files
  * the meta snapshot for stdout / stderr / exit code
* Model that directly with one shared plain Go pointer:

```go
type ContainerExecState struct {
    LazyState

    Parent             dagql.ObjectResult[*Container]
    Opts               ContainerExecOpts
    ExecMD             *buildkit.ExecutionMetadata
    ExtractModuleError bool

    Container *Container
}

type ContainerExecLazy struct {
    State *ContainerExecState
}
```

* The nested output shells use a generic wrapper that delegates back through the container:

```go
type DirectoryFromContainerLazy struct {
    Container *Container
}

type FileFromContainerLazy struct {
    Container *Container
}

func (lazy *DirectoryFromContainerLazy) Evaluate(ctx context.Context, dir *Directory) error {
    return lazy.Container.Lazy.Evaluate(ctx, lazy.Container)
}

func (lazy *FileFromContainerLazy) Evaluate(ctx context.Context, file *File) error {
    return lazy.Container.Lazy.Evaluate(ctx, lazy.Container)
}
```

#### `WithExec(...)` setup
* `WithExec(...)` creates one shared `*ContainerExecState`.
* The core `WithExec(...)` signature changes to take the explicit parent container result:
  * schema `withExec`
  * direct internal callers like module runtime / SDK runtime flows
  must pass that parent result intentionally.
* It attaches:
  * `ContainerExecLazy` to the output container
  * `DirectoryFromContainerLazy` to the output rootfs shell
  * `DirectoryFromContainerLazy` to each writable directory mount output shell
  * `FileFromContainerLazy` to each writable file mount output shell
* The child wrappers delegate back through the container's top-level lazy.
* This replaces the current `gateRun` closure sprayed across multiple objects.

Concrete setup shape:

```go
func (container *Container) WithExec(
    ctx context.Context,
    parent dagql.ObjectResult[*Container],
    opts ContainerExecOpts,
    execMD *buildkit.ExecutionMetadata,
    extractModuleError bool,
) error {
    state := &ContainerExecState{
        LazyState:          NewLazyState(),
        Parent:             parent,
        Opts:               opts,
        ExecMD:             execMD,
        ExtractModuleError: extractModuleError,
        Container:          container,
    }

    container.Lazy = &ContainerExecLazy{State: state}
    container.ImageRef = ""
    container.MetaSnapshot = nil

    rootfsOutput := &Directory{
        Dir:      "/",
        Platform: container.Platform,
        Services: slices.Clone(container.Services),
        Lazy:     &DirectoryFromContainerLazy{Container: container},
    }
    if container.FS != nil && container.FS.self() != nil {
        rootfsOutput.Dir = container.FS.self().Dir
        rootfsOutput.Platform = container.FS.self().Platform
        rootfsOutput.Services = slices.Clone(container.FS.self().Services)
    }
    container.FS = newContainerDirectoryValueSource(rootfsOutput)

    for i, ctrMount := range container.Mounts {
        if ctrMount.Readonly {
            continue
        }

        switch {
        case ctrMount.DirectorySource != nil:
            if ctrMount.DirectorySource.self() == nil {
                return fmt.Errorf("mount %d has nil directory source", i)
            }
            dirMnt := ctrMount.DirectorySource.self()
            outputDir := &Directory{
                Dir:      dirMnt.Dir,
                Platform: dirMnt.Platform,
                Services: slices.Clone(dirMnt.Services),
                Lazy:     &DirectoryFromContainerLazy{Container: container},
            }
            ctrMount.DirectorySource = newContainerDirectoryValueSource(outputDir)
            container.Mounts[i] = ctrMount

        case ctrMount.FileSource != nil:
            if ctrMount.FileSource.self() == nil {
                return fmt.Errorf("mount %d has nil file source", i)
            }
            fileMnt := ctrMount.FileSource.self()
            outputFile := &File{
                File:     fileMnt.File,
                Platform: fileMnt.Platform,
                Services: slices.Clone(fileMnt.Services),
                Lazy:     &FileFromContainerLazy{Container: container},
            }
            ctrMount.FileSource = newContainerFileValueSource(outputFile)
            container.Mounts[i] = ctrMount
        }
    }

    return nil
}
```

#### `ContainerExecState.Evaluate(...)`
* Move the current `withExec` gate body into a named method on `ContainerExecState`.
* It should:
  * `cache.Evaluate(ctx, state.Parent)` as the explicit replacement for the old parent-lazy chain
  * call `container.execMeta(ctx, opts, ExecMD)`
  * materialize input rootfs and mounts from the parent container
  * run the exec
  * commit outputs
  * bind snapshots into:
    * the output container rootfs shell
    * writable output directory mount shells
    * writable output file mount shells
    * `MetaSnapshot`
  * clear wrapper lazies after binding
* Keep the current exec side-effect surface and ordering intact while moving the code:
  * secret env injection
  * secret mounts
  * SSH/socket mounts
  * tmpfs/cache mounts
  * qemu injection
  * services start/detach
  * terminal error container path
  * module-error extraction
  * cache-mount invalidation
  * output ref cleanup / release behavior
* The refactor changes the coordination mechanism, not the operational steps.

Concrete shape:

```go
func (state *ContainerExecState) Evaluate(ctx context.Context) (rerr error) {
    if state == nil {
        return nil
    }

    return state.LazyState.Evaluate(ctx, "Container.withExec", func(ctx context.Context) (rerr error) {
        dagqlCache, err := dagql.EngineCache(ctx)
        if err != nil {
            return err
        }
        if err := dagqlCache.Evaluate(ctx, state.Parent); err != nil {
            return err
        }

        parent := state.Parent.Self()
        if parent == nil {
            return fmt.Errorf("exec parent is nil")
        }
        container := state.Container
        if container == nil {
            return fmt.Errorf("exec output container is nil")
        }

        query, err := CurrentQuery(ctx)
        if err != nil {
            return fmt.Errorf("get current query: %w", err)
        }

        secretEnv, err := container.secretEnvValues(ctx)
        if err != nil {
            return err
        }

        execMD, err := container.execMeta(ctx, state.Opts, state.ExecMD)
        if err != nil {
            return err
        }
        state.ExecMD = execMD

        metaSpec, err := container.metaSpec(ctx, state.Opts)
        if err != nil {
            return err
        }

        bkClient, err := query.Buildkit(ctx)
        if err != nil {
            return fmt.Errorf("failed to get buildkit client: %w", err)
        }
        if bkClient.Worker == nil {
            return fmt.Errorf("missing buildkit worker")
        }

        inputRootFS := parent.FS
        inputMounts := slices.Clone(parent.Mounts)

        rootfsOutput := container.FS.Value
        rootOutputBinding := func(ref bkcache.ImmutableRef) error {
            if rootfsOutput == nil {
                return fmt.Errorf("exec rootfs output is nil")
            }
            return rootfsOutput.setSnapshot(ref)
        }
        metaOutputBinding := func(ref bkcache.ImmutableRef) error {
            container.MetaSnapshot = ref
            return nil
        }
        mountOutputBindings := make([]func(bkcache.ImmutableRef) error, len(container.Mounts))
        for i, ctrMount := range container.Mounts {
            if ctrMount.Readonly {
                continue
            }
            switch {
            case ctrMount.DirectorySource != nil && ctrMount.DirectorySource.Value != nil:
                outputDir := ctrMount.DirectorySource.Value
                mountOutputBindings[i] = func(ref bkcache.ImmutableRef) error {
                    return outputDir.setSnapshot(ref)
                }
            case ctrMount.FileSource != nil && ctrMount.FileSource.Value != nil:
                outputFile := ctrMount.FileSource.Value
                mountOutputBindings[i] = func(ref bkcache.ImmutableRef) error {
                    return outputFile.setSnapshot(ref)
                }
            }
        }

        cache := query.BuildkitCache()
        bkSessionGroup := NewSessionGroup(bkClient.ID())
        causeCtx := trace.SpanContextFromContext(ctx)
        opWorker := bkClient.Worker

        // The rest of this method is the current gate body translated directly:
        //   * build root/meta/mount execMountState slices from inputRootFS/inputMounts
        //   * evaluate any result-backed inputs via dagqlCache
        //   * read bare owned directory/file snapshots directly from the already-evaluated parent
        //   * materialize mounts
        //   * run services
        //   * exec via opWorker.ExecWorker(causeCtx, *execMD)
        //   * commit outputs
        //   * call rootOutputBinding / metaOutputBinding / mountOutputBindings
        //   * preserve current terminal/module-error handling
        //
        // The old retryBareDirectorySnapshot / retryBareFileSnapshot fallback goes away.
        // After `dagqlCache.Evaluate(ctx, state.Parent)`, any bare owned child on the
        // parent must already be readable via `getSnapshot()`. If it still is not,
        // that is an invariant failure and should return an error rather than retrying
        // by evaluating the bare child directly.
        //
        // Concretely, the current code from the existing gate body should move here
        // nearly line-for-line, with the old closure captures replaced by:
        //   parent       -> state.Parent.Self()
        //   container    -> state.Container
        //   execMD       -> state.ExecMD
        //   opts         -> state.Opts
        //   rootOutput   -> rootOutputBinding
        //   mountOutputs -> mountOutputBindings

        execWorker := opWorker.ExecWorker(causeCtx, *execMD)
        meta := *metaSpec
        meta.Env = slices.Clone(meta.Env)
        meta.Env = append(meta.Env, secretEnv...)

        // Existing execMountState / materializeState / applyOutputs flow lives here.
        // The important structural difference is only that the coordination state is
        // now explicit instead of hidden in a shared callback closure.
        _ = cache
        _ = bkSessionGroup
        _ = execWorker
        _ = rootOutputBinding
        _ = metaOutputBinding
        _ = mountOutputBindings
        _ = inputRootFS
        _ = inputMounts

        container.Lazy = nil
        return nil
    })
}
```

#### `ExecMD` stays the real contract
* Do not invent a replacement type like `ExecSeed`.
* The real field is `*buildkit.ExecutionMetadata`, because that is the actual current contract of `WithExec(...)`.
* `ContainerExecLazy.EncodePersisted(...)` persists:
  * `ParentResultID`
  * `Opts`
  * `ExecMD`
  * `ExtractModuleError`
* Do not put `MetaSnapshot` in the lazy payload.
* Do not put rootfs output shell layout or writable mount output shell layout in the lazy payload.
* The container payload already carries that layout via `FSValue` / mount value sources, and that remains the sole source of truth.
* `MetaSnapshot` remains nil for pending `withExec` results, is populated only when evaluation completes, and then persists through the normal container snapshot-ref link path.
* Current producers of pre-populated `ExecMD` are internal flows like module function execution and SDK runtime/codegen, and they already construct real `buildkit.ExecutionMetadata`.
* Session-bound fields like `SecretToken`, `Hostname`, `ClientStableID`, `SSHAuthSocketPath`, and `ClientVersionOverride` are filled later by executor setup, not when the lazy `withExec` state is installed.

#### `withExec` decode
* `call.Field == "withExec"` selects `ContainerExecLazy`.
* Decode:
  * reconstructs the output container
  * reconstructs the shared `*ContainerExecState`
  * walks the already-decoded container self-state and attaches `DirectoryFromContainerLazy` / `FileFromContainerLazy` to any nested values marked as exec-output shells
* `DirectoryFromContainerLazy` / `FileFromContainerLazy` are internal nested wrapper states:
  * they are reconstructed by `withExec` container decode
  * they are not top-level `Directory` / `File` decode branches

```go
type persistedContainerExecLazy struct {
    ParentResultID     uint64                      `json:"parentResultID"`
    Opts               ContainerExecOpts           `json:"opts"`
    ExecMD             *buildkit.ExecutionMetadata `json:"execMD,omitempty"`
    ExtractModuleError bool                        `json:"extractModuleError,omitempty"`
}
```

Concrete decode shape:

```go
case "withExec":
    var persistedLazy persistedContainerExecLazy
    if err := json.Unmarshal(payload.LazyJSON, &persistedLazy); err != nil {
        return nil, fmt.Errorf("decode persisted container withExec lazy payload: %w", err)
    }

    parent, err := loadPersistedObjectResultByResultID[*Container](ctx, dag, persistedLazy.ParentResultID, "container exec parent")
    if err != nil {
        return nil, err
    }

    state := &ContainerExecState{
        LazyState:          NewLazyState(),
        Parent:             parent,
        Opts:               persistedLazy.Opts,
        ExecMD:             persistedLazy.ExecMD,
        ExtractModuleError: persistedLazy.ExtractModuleError,
        Container:          container,
    }

    container.Lazy = &ContainerExecLazy{State: state}
    container.ImageRef = ""
    container.MetaSnapshot = nil

    if container.FS != nil && container.FS.Value != nil && decodedRootFS.Kind == persistedContainerValueFormOutputShell {
        container.FS.Value.Lazy = &DirectoryFromContainerLazy{Container: container}
    }

    for i, decodedMount := range decodedMounts {
        if container.Mounts[i].Readonly {
            continue
        }
        switch decodedMount.Kind {
        case persistedContainerValueFormOutputShell:
            switch {
            case container.Mounts[i].DirectorySource != nil && container.Mounts[i].DirectorySource.Value != nil:
                container.Mounts[i].DirectorySource.Value.Lazy = &DirectoryFromContainerLazy{Container: container}
            case container.Mounts[i].FileSource != nil && container.Mounts[i].FileSource.Value != nil:
                container.Mounts[i].FileSource.Value.Lazy = &FileFromContainerLazy{Container: container}
            }
        }
    }

    return container, nil
```

The container decode path above assumes the regular nested-value decode already returned markers, e.g.:

```go
decodedRootFS, err := decodePersistedContainerDirectoryValue(ctx, dag, resultID, "fs", persisted.FSValue)
if err != nil {
    return nil, err
}
container.FS = newContainerDirectoryValueSource(decodedRootFS.Dir)

type decodedContainerMount struct {
    Kind string
}

decodedMounts := make([]decodedContainerMount, 0, len(persisted.Mounts))
for _, persistedMount := range persisted.Mounts {
    decodedMount := decodedContainerMount{}
    switch {
    case len(persistedMount.DirectorySourceValue) > 0:
        dirVal, err := decodePersistedContainerDirectoryValue(ctx, dag, resultID, role, persistedMount.DirectorySourceValue)
        if err != nil {
            return nil, err
        }
        mnt.DirectorySource = newContainerDirectoryValueSource(dirVal.Dir)
        decodedMount.Kind = dirVal.Kind
    case len(persistedMount.FileSourceValue) > 0:
        fileVal, err := decodePersistedContainerFileValue(ctx, dag, resultID, role, persistedMount.FileSourceValue)
        if err != nil {
            return nil, err
        }
        mnt.FileSource = newContainerFileValueSource(fileVal.File)
        decodedMount.Kind = fileVal.Kind
    }
    decodedMounts = append(decodedMounts, decodedMount)
}
```

### core/schema/container.go
#### Keep resolver-time shaping in schema
* Schema still owns:
  * env expansion
  * owner parsing
  * `withFile` directory-source fallback
  * `withFiles` lowering
  * `withoutFiles` lowering
  * `withNewFile` lowering
* These are part of the effective semantics and are captured before structured lazy objects are installed.

#### `from`
* `from` installs a top-level `ContainerFromLazy` on the child container.

#### Ordinary filesystem mutations
* Ordinary fs-mutation resolvers keep doing current shaping, then let core mutate:
  * result-backed children
  * or bare owned value children
* They do not create top-level container lazy ops unless the operation is truly a top-level container op.

#### Mount setters stay non-top-level-lazy
* `withMountedDirectory` / `withMountedFile` remain source setters.
* They are not top-level container lazy ops.

#### `withFiles`, `withoutFiles`, `withNewFile` stay lowerings
* Keep these as lowerings.
* Do not create separate persisted top-level container lazy shapes for them.

#### `withExec`
* `withExec` continues to do resolver-time shaping in schema:
  * validate stdin / redirect invariants
  * expand args
  * load internal `execMD` if present
* Then it installs the top-level structured `withExec` state rather than callback chains.

### core/modulesource.go
#### No production change expected
* `ModuleSource` itself does not need a new lazy model for this slice.
* The important regression is that its retained `ContextDirectory` now survives restart because directory lazy persistence works.
* Keep production code unchanged unless tests prove otherwise.

### dagql/cache_persistence_self_test.go
#### Add focused roundtrip coverage for structured lazy forms
* Add explicit roundtrip coverage for:
  * all migrated public directory lazy types
  * all migrated public file lazy types
  * `ContainerFromLazy`
  * container nested bare-child source shells
  * `ContainerExecLazy`
* Add a negative test for unsupported pending lazy `File.WithContents(...)`.

### core/schema/modulesource_test.go
#### Add the concrete regression case we already traced
* Add restart / persistence coverage around retained `ModuleSource.ContextDirectory` with no `dagger.json`.

### core/integration/engine_persistence_test.go
#### Add end-to-end restart coverage
* Add restart tests for:
  * retained lazy directory operations
  * retained lazy file operations
  * retained `ModuleSource -> ContextDirectory`
  * retained `container.from`
  * retained containers with owned pending bare rootfs / mount children from ordinary fs mutations
  * retained `withExec`
    * container itself
    * stdout / stderr / exit code
    * rootfs output
    * writable output directory mount
    * writable output file mount
* The `withExec` coverage should prove that any one output can be evaluated first and the shared exec state still runs exactly once.

### core/integration/directory_test.go
#### Add focused behavior coverage for migrated directory lazy ops
* Add focused coverage around:
  * `withPatch` string lowering to file-backed lazy
  * `withFiles` lowering to repeated `withFile`
  * `withoutDirectory` / `withoutFile` / `withoutFiles` lowering to `DirectoryWithoutLazy`

### core/integration/file_test.go
#### Add focused behavior coverage for migrated file lazy ops
* Add focused coverage around:
  * `withName` preserving source path correctly
  * `withReplaced`
  * `withTimestamps`
  * `chown`
* Add one explicit unsupported-case test for pending lazy `WithContents(...)` if a production path can reach it.

### core/integration/container_test.go
#### Add focused behavior coverage for the container slice
* Add coverage for:
  * `from` staying lazy
  * bare owned rootfs child pending state
  * bare owned mount directory/file pending state
  * ordinary fs mutation paths after removing callback chaining
  * `withExec` shared-state output behavior

### core/integration/container_exec_test.go
#### Add `withExec`-specific structured-lazy coverage
* Add focused coverage for:
  * evaluating the container first
  * evaluating rootfs first
  * evaluating a writable directory output first
  * evaluating a writable file output first
* All of those should observe one shared execution and coherent sibling outputs.
