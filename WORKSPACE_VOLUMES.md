# Plan: `Workspace.withMountedCache` (cache volumes mounted into a Workspace)

## Implementation status (in progress)

Engine-side implementation is complete and compiles (`go build ./core/...
./dagql/...` passes, gofmt/vet clean). **Not yet done: SDK/doc regeneration and
tests.** The generated SDK/schema files are therefore stale (a CI drift check
would flag them until regenerated against a from-source engine), and no
end-to-end validation has been run yet.

Done:

- [x] **Data model** (`core/workspace.go`): `WorkspaceCacheMount{Target, Volume,
  Changes}` type, internal `mounts []WorkspaceCacheMount` field on `Workspace`,
  accessors `Mounts()`, `MountForPath()` (deepest-match, returns mount-relative
  subpath), `WithMount()` / `WithoutMount()` (copy-on-write the slice).
- [x] **Persistence & dependency tracking** (`core/workspace.go`):
  `persistedWorkspaceCacheMount{Target, VolumeResultID, ChangesID}` encode/decode
  and `AttachDependencyResults` for each mount's Volume + Changes.
- [x] **Baseline materialization + write-through** (`core/workspace_volume.go`):
  `CacheVolume.SnapshotDirectory` (copy-on-read of the live mutable ref into an
  immutable `Directory`), `CacheVolume.CommitChanges` and `Changeset.CommitInto`
  (apply a per-mount delta into the mutable cache ref).
- [x] **Live baseline field** (`core/schema/cache.go`): internal DoNotCache
  `CacheVolume.__snapshotDirectory` so reads reflect the volume at call time;
  content-derived digest still dedups downstream.
- [x] **Schema wiring** (`core/schema/workspace.go`): `withMountedCache` +
  `withoutMount` fields/handlers, `resolveCacheMountBaseline` /
  `resolveCacheMountEffective`, `.refs`-prefix rejection.
- [x] **Read routing**: `resolveRootfs` deepest-match mount branch (covers
  `directory`/`file`); `search`, `glob`, and `findUp` union base + per-mount with
  base subtree shadowing (via `mountShadowPredicate`) and Target-prefixed
  results.
- [x] **Write routing**: `mountEdit` + `workspaceEdit` dispatcher wired into
  `withNewFile`, `withNewDirectory`, `withoutFile`, `withoutDirectory`;
  `withChanges` **rejects** boundary-crossing changesets (v1).
- [x] **`export` write-through**: base host export + per-mount `CommitChanges`,
  restructured so mount flush runs regardless of a valid host base; mount deltas
  stay excluded from `changes`.

Not done / follow-ups:

- [ ] **SDK/doc regen** (`sdk/go/dagger.gen.go`, `docs/docs-graphql/schema.graphqls`,
  other SDKs) — must run against a from-source engine.
- [ ] **Tests** across the four workspace types (see Test plan below). Not
  written yet; the write-through / persistence paths are unverified end-to-end.
- [ ] COW baseline snapshot instead of copy-on-read (perf); `LOCKED` sharing on
  export commit; `withChanges` boundary splitting; whole-tree reads
  (`directory("/")`) do **not** currently shadow base content at a subtree mount
  (consistent with mounts being excluded from `workspaceOverlayRootfs`).

### Deviations from the original plan below

- Baseline is materialized via a dedicated **DoNotCache `__snapshotDirectory`
  field on `CacheVolume`** (rather than an ad-hoc per-call op), which both keeps
  reads live and yields a first-class, selectable `Directory` result.
- `withChanges` across a mount boundary is **rejected** (as the plan's suggested
  v1 default), not split.

---

## Goal

Add `Workspace.withMountedCache("/foo", cacheVolume("bar"))`, porting the
Container `withMountedCache` concept to `Workspace`.

A mounted cache is a **mutable baseline backed by a `CacheVolume`, mounted at a
subtree of the workspace**. It is the direct analog of how a local host-backed
workspace is a mutable baseline backed by the host:

| | Host-backed workspace | Cache-mounted subtree |
|---|---|---|
| mutable baseline | `WorkspaceSourceClientLocal{HostPath}`, read live per call | `CacheVolume` snapshot, read live per call |
| pending edits | primary overlay changeset (`WorkspaceSourceOverlay`) | **per-mount** delta, kept off `Workspace.changes` |
| `Workspace.changes` | the overlay delta only (baseline excluded) | **excluded entirely** |
| write-out | `export` writes the delta to the host path | `export` writes the per-mount delta **into the cache volume** |
| reads (`directory`/`file`/`search`/`glob`/`findUp`) | resolve against the host | resolve against the cache (shadowing base content at that path) |

### Confirmed decisions (from design discussion)

1. **Scope**: implement the *subtree* mount primitive `withMountedCache(path, cache)`.
   `path` may be `/` to cover the whole workspace. A dedicated whole-workspace
   cache-backed `WorkspaceSource` peer is **not** required; the subtree mount is
   the deliverable.
2. **Write-through on export**: edits made through the workspace tools under a
   mount path are committed **into the cache volume** on `export`, so containers
   / modules that mount the same `cacheVolume("bar")` observe them.
3. **Excluded from `changes`, multiple commit targets**: `Workspace.changes`
   represents only what the **base** workspace receives (host/dir/git). Cache
   mount edits never appear in `changes`; they are a separate commit target
   flushed to the volume on `export`. One workspace, multiple commit targets.
4. **Shadow**: a mount fully shadows base workspace content at its path (like a
   container cache mount shadows the image path).

## Key background (as of this codebase)

- `core.Workspace` (`core/workspace.go`) holds a private `source WorkspaceSource`
  plus `rootfs`, `references`, `Cwd`, `ConfigFile`, `hostPath`, `ClientID`, etc.
  It is a dagql *value*: every `with*` returns a new `Workspace`
  (`ws.Clone()` is a shallow `cp := *ws`).
- The **references** mechanism (`WorkspaceReferencePrefix = ".refs"`) is the
  closest existing precedent for "mounted content that is readable but excluded
  from the changeset": a separate `references` Directory field, injected in
  `resolveRootfs` ahead of host/overlay resolution, guarded against edits, never
  diffed or exported. Mounts follow the same *field + inject + guard* shape but
  add write-through and search/glob visibility.
- Read backend: `resolveRootfs` (`core/schema/workspace.go`) is the shared entry
  for `directory` and `file` (via `fileAt`). It already special-cases:
  1. `.refs` → `ws.ReferencesDir()`
  2. host + overlay → `resolveHostOverlayRootfs` (sparse host sync + changeset)
  3. `SourceDirectory()` (value/git) → `resolveRootfsFromDirectory`
  4. rootless / host / remote fallbacks.
- `search`/`glob` (`core/schema/workspace.go`): host workspaces push down to the
  client (`SearchCallerHostPath` / `GlobCallerHostPath`); non-host search the
  in-engine `Directory` (`Directory.Search`, `Directory.glob`). Overlay results
  are merged via `mergeOverlaySearchResults` / `mergeOverlayGlobMatches` with
  per-path replacement. **References are currently NOT searchable** — mounts will
  be, which is net-new.
- `CacheVolume` (`core/cache.go`) backs a `bkcache.MutableRef`
  (`InitializeSnapshot` opens/creates it). It is already a `PersistedObject`
  (with snapshot ref links) and `HasDependencyResults`. Containers explicitly
  refuse to read cache content (`locatePath`: `"cannot retrieve path from cache"`),
  so exposing a cache as a `Directory` is net-new but mechanically supported:
  `MountRef` can mount a mutable ref read-only, and the `git_local.go` pattern
  (`bkref.Commit(ctx)` → `Directory{Snapshot: ...}`) shows how to turn a ref into
  a `Directory`.

## Data model changes (`core/workspace.go`)

Add a mounts field to `Workspace`:

```go
// WorkspaceCacheMount is a CacheVolume mounted as a mutable baseline at a
// workspace subtree. It shadows base workspace content at Target, is excluded
// from Workspace.changes, and its pending edits are committed into the volume
// on export.
type WorkspaceCacheMount struct {
    // Target is the workspace-root-relative mount path, cleaned, no leading
    // slash (e.g. "foo", "build/cache"). "" / "." means the whole workspace.
    Target string
    // Volume is the cache volume backing this mount.
    Volume dagql.ObjectResult[*CacheVolume]
    // Changes is the pending per-mount overlay delta: edits made through the
    // workspace tools under Target, diffed against the cache's baseline at
    // mount/edit time. Empty/nil when the mount has no pending edits. Committed
    // into the volume on export; never part of Workspace.changes.
    Changes dagql.ObjectResult[*Changeset]
}
```

Add `mounts []WorkspaceCacheMount` to `Workspace` (internal, not a GraphQL
field). Accessors mirroring `ReferencesDir`/`SetReferencesDir`:

- `Mounts() []WorkspaceCacheMount`
- `MountForPath(resolvedPath string) (WorkspaceCacheMount, string, bool)` —
  deepest-match lookup (iterate longest Target first, like container
  `locatePath`), returning the mount and the mount-relative subpath.
- `WithMount(m WorkspaceCacheMount) *Workspace` — clone + **copy-on-write the
  slice** (append into a fresh slice; `Clone`'s shallow copy aliases the backing
  array) + replace-by-Target semantics.
- `WithoutMount(target string) *Workspace` (for a future `withoutMount`).

### Persistence & dependency tracking

- `persistedWorkspacePayload`: add
  `Mounts []persistedWorkspaceCacheMount` with fields
  `{ Target string; VolumeResultID uint64; ChangesID uint64 }`.
- `EncodePersistedObject`: for each mount, `encodePersistedObjectRef` the Volume
  and (if present) the Changes.
- `DecodePersistedObject`: `loadPersistedObjectResultByResultID[*CacheVolume]`
  and `[*Changeset]` per mount.
- `AttachDependencyResults`: attach each mount's `Volume` and `Changes` (so
  liveness/GC and cross-session handles track them). `CacheVolume` already
  contributes snapshot ref links.

## Baseline materialization (`core/schema/workspace.go` or `core/cache.go`)

Need a point-in-time `Directory` view of a cache's *current* content.

`resolveCacheMountBaseline(ctx, srv, mount) (dagql.ObjectResult[*Directory], error)`:

1. `cache := mount.Volume.Self()`; if `cache.getSnapshot() == nil` →
   `cache.InitializeSnapshot(ctx)`.
2. Produce an immutable `Directory` from the cache's current mutable ref.
   - v1 (safe): **copy-on-read** — new mutable ref, `MountRef(cacheRef, ...,
     mountRefAsReadOnly)` + copy content in (reuse `layercopy` as
     changeset/directory copies do), `Commit()` → `ImmutableRef`, wrap in
     `&core.Directory{ Snapshot: ... }` (see `git_local.go` lines ~182-201).
   - Follow-up optimization: COW snapshot of the mutable ref to avoid a full
     copy per read (see Risks).
3. The materialize op **must be `DoNotCache` / per-call** — the cache is mutable,
   so a result keyed only on the cache-volume ID would go stale. (The resulting
   `Directory`'s snapshot digest is content-derived, so downstream reads of an
   unchanged cache still dedup; only the "read the live cache" step is uncached.)

`resolveCacheMountEffective(ctx, srv, mount, ...)`: baseline from above, then if
`mount.Changes` is non-empty apply it via `directory.withChanges` (mirrors
`resolveHostOverlayRootfs`'s base+changeset composition). This is the tree reads
resolve against.

## Read routing

### `resolveRootfs` (`directory`, `file`)

At the top of `resolveRootfs`, before the `.refs` / host / overlay branches:

```go
if mount, rel, ok := ws.MountForPath(resolvedPath); ok {
    eff, err := s.resolveCacheMountEffective(ctx, srv, mount)
    if err != nil { return inst, err }
    return s.resolveRootfsFromDirectory(ctx, srv, ws, eff, rel, filter, gitignore)
}
```

Deepest-match + first-check gives **shadowing** for free. `file` already routes
through `resolveRootfs` via `fileAt`, so it works with no extra change.

Interaction with `.refs`: mounts and references occupy disjoint namespaces.
Reject (at mount time) any `Target` under `WorkspaceReferencePrefix`; keep the
`.refs` check first so references still win in their reserved prefix.

### `search`

- **Base**: run as today (host ripgrep or in-engine `Directory.Search`), but
  **exclude mount subtrees** (shadowing) — post-filter base results dropping any
  `FilePath` under a mount Target (helper like `searchPathInScopes`), or pass
  exclude globs.
- **Per mount**: materialize the effective mount `Directory`, run
  `Directory.Search` scoped to the requested `Paths`/`Globs`, **prefix each
  result's `FilePath` with the mount Target**, then merge with base via the
  existing `mergeOverlaySearchResults` shape (per-path replacement, sorted,
  `Limit`-capped). Reuse `emitSearchResults` for stdio.
- Applies to all workspace types (host and in-engine base alike).

### `glob`

Same pattern as `search`: base glob excluding mount subtrees + per-mount
`Directory.glob` with Target-prefixed matches, merged via
`mergeOverlayGlobMatches`.

### `findUp`

Extend the stat callback so a candidate under a mount Target is stat'd against
the mount's effective `Directory` (via a `DirectoryStatFS`) instead of the base
FS; otherwise unchanged. Shadowing preserved.

### `moduleSource` / `checks` / `generators` / `services` / `git`

No interaction: mounts are not modules and are not part of the materialized
source tree (`workspaceOverlayRootfs`). `git` operates on the base only.

## Write routing (edits under a mount)

Add a `mountEdit` path parallel to `overlayEdit`, selected by
`ws.MountForPath(resolvedPath)`, used by `withNewFile`, `withNewDirectory`,
`withoutFile`, `withoutDirectory`, and (split) `withChanges`:

```go
if mount, rel, ok := ws.MountForPath(resolvedPath); ok {
    return s.mountEdit(ctx, parent, mount, rel, editFn)
}
// else existing overlayEdit(...)
```

`mountEdit`:

1. `baseline := resolveCacheMountBaseline(mount)`.
2. `effective := baseline + mount.Changes` (apply current delta).
3. `edited := editFn(effective)` at `rel` (reuse the same `srv.Select`
   `withNewFile`/`withDirectory`/`withoutFile`/`withoutDirectory` closures the
   overlay path uses, retargeted to `rel`).
4. `newDelta := edited.changes(from: baseline)` (full in-engine diff — cheap,
   the cache is in-engine, like value-workspace overlays).
5. Return `parent.Clone().WithMount(mount{Changes: newDelta})`.

This keeps mount edits **entirely off** the base overlay/`changes` (decision 3).

`withChanges` whose changeset spans both base and mount paths: for v1 either
reject a boundary-crossing changeset with a clear error, or split it by Target
(base paths → `overlayEdit`, per-mount paths → `mountEdit`). Start with reject +
clear message; splitting is a follow-up.

Reference-style guard stays for `.refs`; add a symmetric check so mount edits go
through `mountEdit`, never the reference guard.

## `changes` (unchanged behavior)

`s.changes` keeps returning only `ws.OverlayChanges()` (the base delta). Mount
deltas are intentionally excluded (decision 3). No code change beyond making sure
mount edits never land in the base overlay.

## `export` (multiple commit targets)

`s.export` currently: resolve host path, export base changeset to host. Extend:

1. **Base**: unchanged — export the primary overlay changeset to the host path
   (only when there is a local git workspace + non-empty base changeset).
2. **Mounts**: for each mount with a non-empty `Changes`, commit the delta into
   the volume:
   - `cache.InitializeSnapshot(ctx)`; open the mutable ref.
   - `MountRef(cacheRef, ...)` **read-write**; apply the changeset into the mount
     root (write added/modified, remove deleted). Reuse `Changeset.Export`
     against the mounted cache dir path, or add `Changeset.CommitInto(ctx, ref)`.
   - Consider `LOCKED` sharing to serialize concurrent writers during commit.
3. Reset flushed mounts' pending deltas on the returned/effective state.

Gating detail: base export errors for non-local workspaces (`ExportHostPath`).
Mount write-through should **not** be gated on a valid host base — a synthetic /
git / value workspace can still have cache mounts to flush. Restructure so:

- if there is a base changeset and no host → error (as today);
- mount flush runs regardless (or provide a dedicated internal used by `export`).
`export` stays `DoNotCache`.

## Schema wiring (`core/schema/workspace.go` `Install`)

```go
dagql.NodeFunc("withMountedCache", s.withMountedCache).
    View(AfterVersion("v1.0.0-0")).
    Doc("Return this workspace with a cache volume mounted at a path.",
        "The mounted cache shadows base workspace content at that path, is",
        "excluded from Workspace.changes, and is committed into the volume on export.").
    Args(
        dagql.Arg("path").Doc("Mount path. Relative resolves from cwd; absolute from root."),
        dagql.Arg("cache").Doc("Cache volume to mount."),
    ),
// (optional) dagql.NodeFunc("withoutMount", s.withoutMount)
```

Handler `withMountedCache(ctx, parent, args{ Path string; Cache core.CacheVolumeID })`:

1. `resolvedPath := resolveWorkspacePath(args.Path, ws.Cwd)`; normalize to
   root-relative Target (strip leading `/`, `""`/`.` ⇒ whole workspace).
2. Reject Targets under `.refs`; reject obviously invalid paths.
3. Load the cache volume; `WithMount(WorkspaceCacheMount{Target, Volume})`.
4. `dagql.NewObjectResultForCurrentCall`.

Regenerate SDKs (`dagger.gen.go`, docs) via the normal `generate` step.

Consider porting later (not v1): `sharing`, `source`, `owner` args from the
container variant.

## Determinism / caching notes

- Cache mounts make reads depend on mutable state. The **materialize step** must
  be `DoNotCache`/per-call; downstream `Directory` ops still content-dedup on the
  materialized snapshot digest. This mirrors how host reads are already live.
- Client-owned workspace results already require a session-resource handle
  (`WorkspaceClientHandle`); cache volumes are global and ride along as
  dependencies with no extra gating.

## Test plan (`core/integration/workspace_*_test.go` patterns)

Cover each workspace type (synthetic/directory, git-remote, local host, overlay):

- `directory`/`file` read content from a mounted cache; base content at the same
  path is shadowed.
- `search`/`glob` union base + mount, with mount shadowing base at the path;
  `Limit` respected.
- `findUp` traverses into a mount subtree.
- `changes` **excludes** mount edits (edit under a mount → `changes` empty /
  base-only).
- **Write-through**: edit under a mount → `export` → a `Container.withMountedCache`
  (or another workspace) on the same `cacheVolume` observes the file.
- Overlapping/deepest-mount selection; mount rejected under `.refs`; mount at `/`
  (whole workspace).
- Persistence round-trip (encode/decode) with a mount carrying a pending delta.

Reference existing patterns in `workspace_overlay_search_test.go`,
`workspace_api_test.go`, and the search/glob merge helpers.

## Risks / open implementation questions

- **Materialization cost**: copy-on-read per cache read is correct but O(content).
  Investigate a cheap COW snapshot of the mutable ref (without sealing the live
  ref, and compatible with `SHARED`/`LOCKED` sharing) as a follow-up.
- **Concurrent writers**: another container writing the same volume during a read
  yields a racy point-in-time view — acceptable (same contract as the live host
  baseline), but export-commit should serialize (`LOCKED`).
- **`withChanges` across the mount boundary**: reject vs. split (start: reject).
- **`export` gating** for workspaces with no valid host base but with mounts to
  flush (see `export` section).
- **`Clone` slice aliasing**: mount mutations must copy-on-write `mounts`.

## Suggested implementation order

1. Data model + accessors + persistence + dependency attach (`core/workspace.go`).
2. `withMountedCache` schema field + handler; baseline materialization.
3. Read routing: `resolveRootfs` (→ `directory`/`file`), then `search`, `glob`,
   `findUp`.
4. Write routing: `mountEdit` + wire the `with*` edit resolvers.
5. `export` write-through (multi-target) + `changes` exclusion verification.
6. Tests across all four workspace types; SDK/doc regen.
