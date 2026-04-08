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

## Problems
* Unlazy of parent affecting children is confusing + messy and probably broken
* Remembering to need to eval before accessing fields impacted by laziness is error-prone, not efficient if not done in parallel, etc.
* container exec is still very very complicated
* we currently end up needing to eval in places we don't need to in order to avoid bugs
* there is likely some snapshot/lease bug lurking that is hard to identify due to the complication of this system
* persistence encode/decode is also extremely overly convoluted

## High-level design ideas
* Each field of an object like Directory/File/Container that is impacted by laziness is wrapped in an accessor that unlazies on demand
  * if a field of a child object depends on a lazy field of a parent object, it is still wrapped in an accessor that ensures parent is unlazied and returns that value

## Agreed on Points
* **Lazy-sensitive fields should be behind accessors, and code should not be able to bypass them accidentally.**
  * The goal is not just to add helper methods; it is to make the correct access path the only practical path.
  * Callers should not need to remember which fields require explicit eval and which happen to be safe to read directly.
* **We are keeping the ability to persist lazy/source forms and reconstruct them on load.**
  * This is a little boilerplatey, but it avoids even trickier questions elsewhere and is still the preferred direction for now.
  * The model should be made solid and understandable rather than replaced with a design that only persists already-normalized state.
* **We are keeping the `Result` vs `Value` duality for container rootfs/mounts for now.**
  * In particular, container exec makes a pure “everything is its own attached object result” model very messy.
  * Trying to force mount outputs into their own attached object results introduces circular-dependency/ownership confusion and makes snapshot accounting harder, since the exec result is the true owner of the produced snapshot state.
  * We should still simplify this duality wherever possible, but not by forcing a model that breaks exec semantics.
* OnRelease must not accidentally evaluate.

## Implementation Plan

### Directory
* **Target object shape**
  * Hard-cut `core.Directory` to:
    ```go
    type Directory struct {
    	Platform Platform
    	Services ServiceBindings

    	Lazy     Lazy[*Directory]
    	Dir      *LazyAccessor[string, *Directory]
    	Snapshot *LazyAccessor[bkcache.ImmutableRef, *Directory]
    }
    ```
  * Delete `snapshotMu`, `snapshotReady`, `snapshotSource`, `getSnapshot`, `setSnapshot`, and `setSnapshotSource`.
  * Delete `NewDirectoryChild`, `NewDirectoryWithSnapshot`, and any similar “copy some hidden state” helpers. Construction is always explicit inline so the reader can see exactly which fields are being set.
  * `LazyAccessor.Peek` is non-evaluating and context-free. `GetOrEval` remains explicit and warning-heavy about requiring the matching dagql result wrapper.

* **Core design rules**
  * Every `Directory` allocates both accessors at construction time.
  * `Dir` is not guaranteed to be peekable. `Peek()` is a best-effort non-evaluating inspection API, not a promise that the field is already known.
    * If code truly needs the selected path, it must use `Dir.GetOrEval(...)`.
    * If code must not evaluate, it must use `Dir.Peek()` and handle the missing case explicitly.
  * `Dir` should still be pre-seeded at construction time whenever the selected path is semantically known without evaluation.
    * `query.directory` => `"/"`
    * `withDirectory`, `withDirectoryDockerfileCompat`, `withNewDirectory`, `withNewFile`, `withPatch`, `withPatchFile`, `withFile`, `withFiles`, `withTimestamps`, `withChanges`, `withoutDirectory`, `withoutFile`, `withoutFiles`, `withSymlink`, `chown` => same selected path as parent when already known
    * `diff` => `"/"` after schema-side rebasing
    * `subdirectory` is its own lazy case and is allowed to leave `Dir` unset until evaluation
  * `Snapshot` is only set by:
    * materialized constructors
    * successful lazy evaluation
    * successful eager mutator methods
  * We do not keep “resolved but nil snapshot” as a stable materialized directory state.
    * Any path that would otherwise resolve to an empty/nil directory snapshot must normalize back to the canonical scratch directory by selecting `query.directory` and using its snapshot.
    * Persistence should therefore only need two stable forms: materialized snapshot form and lazy form.
  * Non-evaluating paths (`OnRelease`, cache usage, persisted snapshot-link reporting, persistence encode deciding lazy-vs-materialized form, schema rebase decisions) must use `Peek()` only and must never call `GetOrEval`.
  * `Subdirectory` gets its own dedicated lazy op, `DirectorySubdirectoryLazy`.
    * We are not preserving `DirectoryFromSourceLazy`.
    * We are not trying to keep a generic source-backed directory abstraction alive during the directory cut.

* **Representative code shape**
  * Materialized directory construction:
    ```go
    dir := &Directory{
    	Platform: platform,
    	Services: slices.Clone(services),
    	Dir:      new(LazyAccessor[string, *Directory]),
    	Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
    }
    dir.Dir.setValue("/")
    dir.Snapshot.setValue(finalRef)
    ```
  * Lazy directory construction that preserves parent metadata explicitly:
    ```go
    dir := &Directory{
    	Platform: parent.Self().Platform,
    	Services: slices.Clone(parent.Self().Services),
    	Lazy:     &DirectoryWithNewFileLazy{...},
    	Dir:      new(LazyAccessor[string, *Directory]),
    	Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
    }
    if parentDir, ok := parent.Self().Dir.Peek(); ok {
    	dir.Dir.setValue(parentDir)
    }
    ```
  * Subdirectory lazy shape:
    ```go
    dir := &Directory{
    	Platform: parent.Self().Platform,
    	Services: slices.Clone(parent.Self().Services),
    	Lazy: &DirectorySubdirectoryLazy{
    		LazyState: NewLazyState(),
    		Parent:    parent,
    		Subdir:    subdir,
    	},
    	Dir:      new(LazyAccessor[string, *Directory]),
    	Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
    }
    ```
  * Mutator/evaluation shape:
    ```go
    parentDir, err := parent.Self().Dir.GetOrEval(ctx, parent.Result)
    parentRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
    dir.Dir.setValue(parentDir)
    // do work...
    dir.Snapshot.setValue(ref)
    dir.Lazy = nil
    ```

#### core/directory.go
* **Type / lifecycle surface**
  * `type Directory`:
    * remove all old snapshot-side state
    * add accessor fields
  * `OnRelease`:
    * use `dir.Snapshot.Peek()` only
    * if unset, return nil; do not evaluate
  * `AttachDependencyResults`:
    * remove all directory-owned source attachment logic
    * if `dir.Lazy == nil`, return nil
    * otherwise delegate entirely to `dir.Lazy.AttachDependencies`
  * `LazyEvalFunc`:
    * keep current shape and just call `dir.Lazy.Evaluate`
  * Delete any helper that “syncs metadata from parent” or otherwise hides copied state. Platform and services must already be set explicitly by the constructor that created the directory.

* **Cache usage / persistence**
  * `CacheUsageSize`, `CacheUsageIdentities`, and `PersistedSnapshotRefLinks`:
    * use `dir.Snapshot.Peek()` only
    * if unset, return empty/unknown without evaluation
  * `EncodePersistedObject`:
    * payload `Dir` comes from `dir.Dir.Peek()`
    * payload form is:
      * `snapshot` if `Snapshot.Peek()` returns a real non-nil snapshot ref
      * `lazy` if snapshot is unset and `dir.Lazy != nil`
      * error if neither is true
    * it must never evaluate
    * if a materialized directory somehow still has `Snapshot.Peek() == (nil, true)`, that is a bug; those states should have been normalized back to canonical scratch earlier
  * `DecodePersistedObject`:
    * allocate `Dir` and `Snapshot` accessors in both forms
    * snapshot form:
      * set `Dir` from payload
      * load the persisted snapshot ref and set `Snapshot`
      * leave `Lazy = nil`
    * lazy form:
      * set `Dir` from payload only if already known
      * leave `Snapshot` unset
      * decode and attach `Lazy`
  * `decodePersistedDirectoryLazy`:
    * keep existing payload formats for directory lazy ops
    * add dedicated payload decoding for `DirectorySubdirectoryLazy`
    * never reconstruct old snapshot/source fields

* **Lazy ops**
  * Keep the existing mutator-style lazy ops for:
    * `DirectoryWithDirectoryLazy`
    * `DirectoryWithDirectoryDockerfileCompatLazy`
    * `DirectoryWithPatchFileLazy`
    * `DirectoryWithNewFileLazy`
    * `DirectoryWithFileLazy`
    * `DirectoryWithTimestampsLazy`
    * `DirectoryWithNewDirectoryLazy`
    * `DirectoryDiffLazy`
    * `DirectoryWithChangesLazy`
    * `DirectoryWithoutLazy`
    * `DirectoryWithSymlinkLazy`
    * `DirectoryChownLazy`
  * Add `DirectorySubdirectoryLazy`:
    * `Evaluate`:
      * evaluate the parent
      * compute the final selected path from the parent path plus `Subdir`
      * if `Subdir` is non-empty, validate it exists and is a directory
      * reopen the parent snapshot by `SnapshotID` onto the child
      * set both `Dir` and `Snapshot` on the child
      * if the parent directory is empty/nil, normalize the child to canonical scratch rather than materializing a resolved nil snapshot
    * `AttachDependencies`:
      * attach/update `Parent`
    * `EncodePersisted`:
      * persist `ParentResultID` plus `Subdir`
  * We do not keep `DirectoryFromSourceLazy`.

* **Concrete method changes**
  * `Digest`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `Dir.GetOrEval`
  * `Entries`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `Dir.GetOrEval`
  * `Glob`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `Dir.GetOrEval`
  * `Search`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `Dir.GetOrEval`
  * `WithNewFile`:
    * read parent path/snapshot through accessors
    * set output `Dir` explicitly
    * set `Snapshot` on success
    * if the result would otherwise be empty/nil, normalize to canonical scratch
  * `applyPatchToSnapshot` and `withoutPathsFromSnapshot`:
    * keep them as explicit snapshot/path helpers
    * thread selected path explicitly as a parameter rather than reading object fields internally
  * `WithPatchFile`:
    * read parent path/snapshot through accessors
    * set output `Dir` explicitly
    * set `Snapshot` on success
    * normalize nil result snapshot to canonical scratch
  * `Subdirectory`:
    * return a lazily constructed child with `DirectorySubdirectoryLazy`
    * do not evaluate the parent eagerly in the constructor
  * `Subfile`:
    * read the parent selected path through accessors
    * keep file-source attachment in the old file model for now
  * `WithDirectory` and `WithDirectoryDockerfileCompat`:
    * use parent/source `Dir.GetOrEval` and `Snapshot.GetOrEval`
    * set output `Dir` explicitly from the parent selected path
    * set output `Snapshot` on success
    * if merge/copy would otherwise yield nil, normalize to canonical scratch
  * `WithFile`:
    * same parent-directory pattern as `WithDirectory`
    * source file snapshot/path still use the file-side old model for now
    * set output `Dir` explicitly
    * set output `Snapshot` on success
  * `WithTimestamps`, `WithNewDirectory`, `Without`, `WithSymlink`, `Chown`:
    * read parent path/snapshot through accessors
    * set output `Dir` explicitly
    * set output `Snapshot` on success
    * normalize nil result snapshot to canonical scratch
  * `Diff`:
    * read both paths/snapshots through accessors
    * preserve the root-rebased invariant
    * set output `Dir` to `"/"`
    * set output `Snapshot` on success
    * if the diff is empty, normalize to canonical scratch
  * `WithChanges`:
    * read parent path/snapshot through accessors
    * set output `Dir` explicitly
    * any selected directories used internally must already be in accessor form
    * if merge/remove/add-dir work produces an empty result, normalize to canonical scratch
  * `Exists`, `Stat`, `Export`, and `Mount`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `Dir.GetOrEval`

* **Helpers intentionally unchanged except for threaded parameters**
  * `copyFile`
  * `attemptCopyArchiveUnpack`
  * `copyAttemptUnpackNonArchiveSingleFile`
  * `resolveAttemptUnpackMatches`
  * `unpackArchiveFile`
  * `isArchivePath`
  * `isDir`
  * `ensureCopyDestParentExists`
  * owner-parsing helpers
  * validation / enum / json helpers

#### core/schema/directory.go
* **Resolver construction rule**
  * Every resolver that returns a new `*core.Directory` must construct it explicitly inline.
  * No use of `NewDirectoryChild`, `NewDirectoryWithSnapshot`, or any helper that hides which fields are copied.
  * Every constructed directory allocates both accessors.
  * Constructors copy `Platform` and `Services` explicitly from `parent.Self()` where appropriate.
  * Constructors pre-seed `Dir` when it is already semantically known and available via `Peek()`, but they do not force evaluation just to populate it.

* **Materialized constructor**
  * `directory(...)`:
    * inline scratch directory construction
    * allocate both accessors
    * set `Dir = "/"` and `Snapshot = finalRef`

* **Lazy constructor resolvers**
  * `withNewDirectory`
  * `withDirectory`
  * `withDirectoryDockerfileCompat`
  * `withTimestamps`
  * `withPatch`
  * `withPatchFile`
  * `withNewFile`
  * `withFile`
  * `withoutDirectory`
  * `withoutFile`
  * `withoutFiles`
  * `withChanges`
  * `withSymlink`
  * `chown`
  * For all of the above:
    * inline construct the `Directory`
    * copy `Platform` and `Services` explicitly
    * allocate `Dir` and `Snapshot`
    * pre-seed `Dir` from `parent.Self().Dir.Peek()` when available
    * attach the corresponding lazy op
  * `subdirectory`:
    * return the explicit lazy child from `core.Directory.Subdirectory`
    * do not force evaluation just to populate `Dir`
  * `diff`:
    * after rebasing logic, inline construct the result directory
    * set `Dir` to `"/"`
    * attach `DirectoryDiffLazy`

* **Resolvers that must stop reading raw directory fields**
  * `name`:
    * change signature to accept `dagql.ObjectResult[*core.Directory]`
    * call `parent.Self().Dir.GetOrEval(ctx, parent.Result)`
  * `entries`, `glob`, `search`, `digest`, `exists`, `stat`, `export`, `exportLegacy`:
    * pass the owning result wrapper through to the updated core methods
  * `file`:
    * compute parent selected path through `parent.Self().Dir.GetOrEval(ctx, parent.Result)` rather than reading a raw field
  * `diff`:
    * use accessor-based path checks during rebasing
    * rebasing remains non-evaluating only when `Peek()` already has the information; otherwise evaluation is acceptable if the code truly needs the path
  * `getDockerIgnoreFileContent`, `applyDockerIgnore`, `dockerBuild`, `terminal`, and `asGit`:
    * they must not become new raw-field escape hatches
    * if they need selected path or snapshot locally, use accessor-based APIs only
  * `withFiles`:
    * remains a follow-up dependency on the file accessor cutover because it still needs file-side path access

* **Done criterion for the directory cut**
  * `core/directory.go` and `core/schema/directory.go` have no remaining references to:
    * `snapshotMu`
    * `snapshotReady`
    * `snapshotSource`
    * `getSnapshot`
    * `setSnapshot`
    * `setSnapshotSource`
    * `NewDirectoryChild`
    * `NewDirectoryWithSnapshot`
    * `DirectoryFromSourceLazy`
    * any helper that implicitly copies directory state
  * All lazy-sensitive directory reads go through `Dir` / `Snapshot` accessors.
  * All non-evaluating paths use `Peek()` only.
  * Empty/resolved directory states normalize back to canonical scratch rather than persisting nil snapshots.

### File
* **Target object shape**
  * Hard-cut `core.File` to:
    ```go
    type File struct {
    	Platform Platform
    	Services ServiceBindings

    	Lazy     Lazy[*File]
    	File     *LazyAccessor[string, *File]
    	Snapshot *LazyAccessor[bkcache.ImmutableRef, *File]
    }
    ```
  * Delete `snapshotMu`, `snapshotReady`, `snapshotSource`, `getSnapshot`, `setSnapshot`, and `setSnapshotSource`.
  * Delete `FileSnapshotSource`, `FileFromSourceLazy`, `NewFileChild`, `NewFileWithSnapshot`, and any helper that implicitly copies file state.
  * `LazyAccessor.Peek` / `GetOrEval` semantics are exactly the same as for `Directory`.

* **Core design rules**
  * Every `File` allocates both accessors at construction time.
  * `File` is not guaranteed to be peekable. `Peek()` is best-effort only.
    * If code truly needs the selected file path, it must use `File.GetOrEval(...)`.
    * If code must not evaluate, it must use `File.Peek()` and handle the missing case explicitly.
  * `File` should be pre-seeded at construction time whenever the selected path is semantically known without evaluation.
    * `withName` => full resulting path immediately when it can be derived from the parent selected path plus the rename argument
    * `withReplaced`, `withTimestamps`, `chown` => same selected path as parent when already known
    * `directory.file(path)` / `Subfile(path)` are the file equivalent of `subdirectory`: they are allowed to leave `File` unset until evaluation if the parent directory path is not already known
  * `file.File` always means the full selected path inside the snapshot, never just a basename.
    * `directory.file("foo/bar")` over a directory selected at `"/src"` means the resulting file path is `"/src/foo/bar"`.
    * `withName("baz")` over a file selected at `"/src/foo/bar"` must produce the full resulting path, not just `"baz"`.
  * For file-path composition in this model, use `filepath` consistently.
    * We are on Linux in the engine and these are Linux paths, so consistency is more valuable than mixing `path` and `filepath`.
  * We do not keep “resolved but nil snapshot” as a valid materialized file state.
    * A materialized `File` must always have a real non-nil snapshot ref.
    * No-op operations must reopen the existing parent snapshot instead of leaving `Snapshot` unset or explicitly nil.
    * Missing-file states are errors, not a materialized “empty file” equivalent to directory scratch.
  * Non-evaluating paths (`OnRelease`, cache usage, persisted snapshot-link reporting, persistence encode deciding lazy-vs-materialized form) must use `Peek()` only and must never call `GetOrEval`.
  * We are not preserving a generic source-backed file abstraction on `File` itself during this cut.
    * `FileSnapshotSource` and `FileFromSourceLazy` go away.
    * The one real directory/file source case that matters in this bubble becomes a dedicated lazy op for `directory.file(...)`.

* **Representative code shape**
  * Materialized file construction:
    ```go
    file := &File{
    	Platform: platform,
    	Services: slices.Clone(services),
    	File:     new(LazyAccessor[string, *File]),
    	Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
    }
    file.File.setValue(filePath)
    file.Snapshot.setValue(snapshot)
    ```
  * Lazy file construction that preserves parent metadata explicitly:
    ```go
    file := &File{
    	Platform: parent.Self().Platform,
    	Services: slices.Clone(parent.Self().Services),
    	Lazy:     &FileWithReplacedLazy{...},
    	File:     new(LazyAccessor[string, *File]),
    	Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
    }
    if parentPath, ok := parent.Self().File.Peek(); ok {
    	file.File.setValue(parentPath)
    }
    ```
  * Directory subfile lazy shape:
    ```go
    file := &File{
    	Platform: parent.Self().Platform,
    	Services: slices.Clone(parent.Self().Services),
    	Lazy: &FileSubfileLazy{
    		LazyState: NewLazyState(),
    		Parent:    parentDirectory,
    		Path:      requestedPath,
    	},
    	File:     new(LazyAccessor[string, *File]),
    	Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
    }
    ```
  * Mutator/evaluation shape:
    ```go
    parentPath, err := parent.Self().File.GetOrEval(ctx, parent.Result)
    parentRef, err := parent.Self().Snapshot.GetOrEval(ctx, parent.Result)
    file.File.setValue(parentPath)
    // do work...
    file.Snapshot.setValue(ref)
    file.Lazy = nil
    ```

#### core/file.go
* **Type / lifecycle surface**
  * `type File`:
    * remove all old snapshot-side state
    * add accessor fields
  * `OnRelease`:
    * use `file.Snapshot.Peek()` only
    * if unset, return nil; do not evaluate
  * `AttachDependencyResults`:
    * remove all source attachment logic from `File` itself
    * if `file.Lazy == nil`, return nil
    * otherwise delegate entirely to `file.Lazy.AttachDependencies`
  * `LazyEvalFunc`:
    * keep current shape and just call `file.Lazy.Evaluate`
  * Delete any helper that copies hidden file state (`NewFileChild`, `NewFileWithSnapshot`, metadata sync helpers, source setters/getters).

* **Cache usage / persistence**
  * `CacheUsageSize`, `CacheUsageIdentities`, and `PersistedSnapshotRefLinks`:
    * use `file.Snapshot.Peek()` only
    * if unset, return empty/unknown without evaluation
  * `EncodePersistedObject`:
    * payload `File` comes from `file.File.Peek()`
    * payload form is:
      * `snapshot` if `Snapshot.Peek()` returns a real non-nil snapshot ref
      * `lazy` if snapshot is unset and `file.Lazy != nil`
      * error if neither is true
    * it must never evaluate
    * a materialized file with `Snapshot.Peek() == (nil, true)` is a bug
  * `DecodePersistedObject`:
    * allocate `File` and `Snapshot` accessors in both forms
    * snapshot form:
      * set `File` from payload
      * load the persisted snapshot ref and set `Snapshot`
      * leave `Lazy = nil`
    * lazy form:
      * set `File` from payload only if already known
      * leave `Snapshot` unset
      * decode and attach `Lazy`
  * `decodePersistedFileLazy`:
    * keep payload formats for mutator-style file lazy ops
    * add dedicated payload decoding for `FileSubfileLazy`
    * do not reconstruct old snapshot/source fields

* **Lazy ops**
  * Keep the mutator-style lazy ops for:
    * `FileWithReplacedLazy`
    * `FileWithNameLazy`
    * `FileWithTimestampsLazy`
    * `FileChownLazy`
  * Change `FileWithNameLazy` payload shape:
    * stop persisting `SourcePath`
    * persist the target filename explicitly instead
  * Add `FileSubfileLazy`:
    * `Evaluate`:
      * evaluate the parent directory
      * compute the final selected file path from the parent directory path plus `Path`
      * validate the path exists and is not a directory via `parent.Stat(...)`
      * reopen the parent snapshot by `SnapshotID` onto the child file
      * set both `File` and `Snapshot` on the child
      * never materialize a nil snapshot
    * `AttachDependencies`:
      * attach/update the parent directory result
    * `EncodePersisted`:
      * persist `ParentResultID` plus `Path`
  * We do not keep `FileFromSourceLazy`.

* **Concrete method changes**
  * `WithContents`:
    * change signature to take the destination file path explicitly rather than reading output state opaquely
    * use the parent directory snapshot accessor (`parent.Self().Snapshot.GetOrEval(ctx, parent.Result)`)
    * set output `File` explicitly from the provided path
    * set `Snapshot` on success
  * `Contents`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `File.GetOrEval`
  * `Open`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `File.GetOrEval`
    * do not call `file.Lazy.Evaluate` directly
  * `Search`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `File.GetOrEval`
  * `Digest`:
    * add the owning result wrapper parameter
    * metadata-including path uses `Snapshot.GetOrEval` and `File.GetOrEval`
    * metadata-excluding path delegates through `Open(ctx, self)`
  * `Stat`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `File.GetOrEval`
  * `Export`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `File.GetOrEval`
  * `Mount`:
    * add the owning result wrapper parameter
    * use `Snapshot.GetOrEval` and `File.GetOrEval`
  * `AsJSON` and `AsEnvFile`:
    * add the owning result wrapper parameter
    * delegate through `Contents(ctx, self, ...)`
  * `WithReplaced`:
    * read parent path/snapshot through accessors
    * set output `File` explicitly from the parent path
    * stop constructing a cloned `sourceFile` via `NewFileWithSnapshot`
    * reuse the parent result directly for internal `Search` / `Contents` calls
    * on no-op (`all == true` and no matches), reopen the parent snapshot and set it on the output file instead of returning with no snapshot
  * `WithName`:
    * stop mutating raw `file.File` before evaluation
    * read the parent path/snapshot through accessors
    * use the explicit rename argument from the lazy op / method argument
    * compute the destination path explicitly from the parent selected path and the rename argument
    * set output `File` to the full resulting path, never to the raw rename argument alone
    * set `Snapshot` on success
  * `WithTimestamps`:
    * read parent path/snapshot through accessors
    * set output `File` explicitly
    * set `Snapshot` on success
  * `Chown`:
    * read parent path/snapshot through accessors
    * set output `File` explicitly
    * set `Snapshot` on success

#### core/schema/file.go
* **Resolver construction rule**
  * Every resolver that returns a new `*core.File` must construct it explicitly inline.
  * No use of `NewFileChild`, `NewFileWithSnapshot`, or any helper that hides which fields are copied.
  * Every constructed file allocates both accessors.
  * Constructors copy `Platform` and `Services` explicitly from `parent.Self()` where appropriate.
  * Constructors pre-seed `File` when it is already semantically known and available via `Peek()`, but they do not force evaluation just to populate it.

* **Materialized constructor**
  * `query.file(...)` can stay as the selector chain through `query.directory().withNewFile(...).file(...)`.
    * It does not need a separate direct file materialization path during this cut.

* **Lazy constructor resolvers**
  * `withName`
    * inline construct the result file
    * copy `Platform` and `Services` explicitly
    * allocate `File` and `Snapshot`
    * if the parent selected path is already known, pre-seed `File` to the full resulting path derived from the parent path plus the rename argument
    * attach `FileWithNameLazy{Parent: parent, Filename: args.Name}`
  * `withReplaced`
    * inline construct the result file
    * copy `Platform` and `Services` explicitly
    * allocate `File` and `Snapshot`
    * pre-seed `File` from `parent.Self().File.Peek()` when available
    * attach `FileWithReplacedLazy`
  * `withTimestamps`
    * inline construct the result file
    * copy `Platform` and `Services` explicitly
    * allocate `File` and `Snapshot`
    * pre-seed `File` from `parent.Self().File.Peek()` when available
    * attach `FileWithTimestampsLazy`
  * `chown`
    * inline construct the result file
    * copy `Platform` and `Services` explicitly
    * allocate `File` and `Snapshot`
    * pre-seed `File` from `parent.Self().File.Peek()` when available
    * attach `FileChownLazy`

* **Resolvers that must stop reading raw file fields**
  * `name`:
    * change signature to accept `dagql.ObjectResult[*core.File]`
    * call `file.Self().File.GetOrEval(ctx, file.Result)`
  * `contents`, `size`, `stat`, `digest`, `search`, `export`, `exportLegacy`, `asJSON`:
    * pass the owning result wrapper through to the updated core methods
    * remove redundant `cache.Evaluate` when the core method already goes through `GetOrEval`

#### directory/file boundary
* **core/directory.go**
  * `Subfile`:
    * stop constructing a file with `setSnapshotSource`
    * return an explicit lazy file using `FileSubfileLazy`
  * `WithFile`:
    * once file accessors land, replace `src.Self().getSnapshot()` and raw `src.Self().File` reads with:
      * `src.Self().Snapshot.GetOrEval(ctx, src.Result)`
      * `src.Self().File.GetOrEval(ctx, src.Result)`
* **core/schema/directory.go**
  * `file(...)`:
    * after selecting the child file result, use the returned file accessor path for content hashing rather than reconstructing the path through raw parent state
  * `withFiles(...)`:
    * replace `path.Base(file.Self().File)` with accessor-based path retrieval from the source file result
* **Path-semantics audit result**
  * The intended invariant across the directory/file seam is:
    * `dir.Dir` is the full selected directory path inside the snapshot
    * `file.File` is the full selected file path inside the snapshot
  * `Subfile`, `directory.file(...)`, `Directory.WithFile`, and `directory.withFiles(...)` should all preserve and consume that full-path model.
  * The main currently-known divergence to fix is `withName`, which must preserve full-path semantics instead of treating the rename argument as the entire selected file path.

#### Expected Fanout
* This file cut will intentionally fan out outside the immediate bubble because many places still construct or consume `File` through the old raw-field helper model.
* Expected follow-up compile breakage / update points include:
  * [core/directory.go](/home/sipsma/repo/github.com/sipsma/dagger/core/directory.go)
  * [core/schema/directory.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/directory.go)
  * [core/schema/query.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/query.go)
  * [core/container.go](/home/sipsma/repo/github.com/sipsma/dagger/core/container.go)
  * [core/schema/container.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/container.go)
  * [core/container_exec.go](/home/sipsma/repo/github.com/sipsma/dagger/core/container_exec.go)
  * [core/container_image.go](/home/sipsma/repo/github.com/sipsma/dagger/core/container_image.go)
  * [core/http.go](/home/sipsma/repo/github.com/sipsma/dagger/core/http.go)
  * [core/changeset.go](/home/sipsma/repo/github.com/sipsma/dagger/core/changeset.go)
  * [core/llm.go](/home/sipsma/repo/github.com/sipsma/dagger/core/llm.go)
  * [core/schema/envfile.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/envfile.go)
  * [core/schema/llm.go](/home/sipsma/repo/github.com/sipsma/dagger/core/schema/llm.go)
  * [core/sdk/dang_helpers.go](/home/sipsma/repo/github.com/sipsma/dagger/core/sdk/dang_helpers.go)
* Those are acceptable follow-up breakages for this phase. The goal of this plan is to get `File` itself, plus the immediate directory/file seam, into the right shape first.

* **Done criterion for the file cut**
  * `core/file.go` and `core/schema/file.go` have no remaining references to:
    * `snapshotMu`
    * `snapshotReady`
    * `snapshotSource`
    * `FileSnapshotSource`
    * `FileFromSourceLazy`
    * `getSnapshot`
    * `setSnapshot`
    * `setSnapshotSource`
    * `NewFileChild`
    * `NewFileWithSnapshot`
    * any helper that implicitly copies file state
  * All lazy-sensitive file reads go through `File` / `Snapshot` accessors.
  * All non-evaluating file paths use `Peek()` only.
  * No materialized file state can survive with a nil snapshot.
  * The immediate directory/file seam (`Subfile`, `Directory.WithFile`, `directory.file`, `directory.withFiles`) is updated to the new file accessor model.
