# Overlay read root forces a full Host.directory sync

**Status:** re-implemented 2026-07-10 on the rebased branch, against the updated
\#13600 snapshot (`757ddc16f workspace overlay and export`). The original two-commit
implementation (`8e2ffa59e9` sparse diff + `9c4f9bfa5d` read-root drop) was dropped
during the rebase and re-expressed as one commit in upstream's new overlay model —
see "Re-implementation on the updated #13600" below. One test in the suite —
`TestWorkspaceSDKReadersUseStagedOverlay` — hangs **with or without this change**;
see "Pre-existing hang" at the bottom. Remaining validation: the real-repo trace
check (no filterless `Host.directory` on an `Edit()`).

## Re-implementation on the updated #13600 (delta-native changeset)

The #13600 update rewrote the overlay state model out from under the original
commits: `WorkspaceSourceOverlay` is now just `{Base, Changes}` — no stored
`Root`/`BaseRoot`. The read root is *derived* (`SourceDirectory()` returns
`Changes.After`) and chained edits reuse `Changes.Before` as the next diff base.
The full-sync problem survived the restructure (`workspaceOverlayRootfs` still
resolved a filterless host tree, with a comment asking for exactly this fix:
"Avoiding this initial sync requires a delta-native Changeset").

The fix collapses into that model instead of porting the old shape:

- **`Changes.After` IS the delta root** — edits applied to an empty base (first
  edit) or to the prior `After` (accumulation), never referencing the host tree.
  The original design's separate `DeltaRoot` field was always redundant with
  `Changes.After`; dropped.
- **`Changes.Before` IS the sparse base** — `host.directory(".", include:
  touchedAll patterns)`. Forcing the changeset (changes/export/layer/asPatch)
  syncs only touched files.
- **`TouchedPaths` is the only new state.** It cannot be derived from the
  changeset: an edit reverted to host content drops out of the diff, and if the
  next sparse base then excluded it while the delta chain still contains it, it
  would resurface as a phantom addition. Persisted as plain JSON; no new object
  refs, no attach changes.
- **`SourceDirectory()` gate:** the overlay case returns false when `Base` is
  `*WorkspaceSourceClientLocal` — the sparse `After` must never masquerade as
  the workspace tree (the rejected `Root = deltaRoot` landmine, which the new
  model would otherwise reintroduce silently). Value/git/rootless overlays keep
  returning `After` (full in-engine trees).
- **Dispatch is `ClientLocalBase()`, not `HostPath() != ""`.** The updated
  #13600 introduced *rootless* local workspaces (`WorkspaceSourceRootlessLocal`)
  which carry a host path but read as an empty directory. They must stay on the
  in-engine full-root path — routing them through the sparse machinery would
  read real host files into a deliberately-empty workspace.
- **Renames survive the sparse base.** Rename detection pairs a removal with an
  addition; a removal only ever comes from an edit, which makes both paths
  touched and therefore present in the base. (This refutes the old
  `workspaceOverlayRootfs` comment's stated blocker.)
- **Config + lock staging merged upstream** (`stageWorkspaceConfigAndLock`):
  touched = config path plus the lock path when it changed, both writes in one
  `overlayEdit` callback.
- **New full-sync sites the original commits never saw:**
  `readWorkspaceLockForOverlay` (stat/read of the lockfile through
  `workspaceOverlayRootfs`, full sync even on pristine hosts) and
  `workspaceLockChangeset` (filterless `resolveRootfs(".")` base). Fixed in a
  follow-up commit by include-scoping to the lock paths — install/update flows,
  not the Edit() hot path.

Everything below this section describes the original diagnosis and design against
the pre-rebase model; the reasoning (uniform merge formula, requested-only read
base, descend + trim, gitignore host-side only, caller audit) carries over
unchanged. Where it says `DeltaRoot`, read `Changes.After`; where it says "store
no Root", read "the `SourceDirectory()` gate".

## The symptom

An `Edit()` call from `modules/doug2/main.dang` (the object-tools coding agent)
still uploads the **entire** workspace tree via a filterless
`Host.directory(path: …)`, even though the sparse-diff work (`8e2ffa59e9 feat(core):
diff workspace overlays against a sparse base`) landed. On this repo that's ~11s of
`uploading /home/vito/src/dagger/llm-workspace` (bin, cmd, core, sdk, …) per edit.

## How I found it — the trace

```console
./bin/dagger trace -vvvvvvvvv 104de731955ae21d2f1f4280105da61d --span 6945fc5a8a735870
```

- `<trace-id>` = full trace id; `--span <id>` zooms into one span (here the `Edit()`
  tool call). Crank `-v` (many `v`s) to expand rolled-up spans — see recent commits
  `6790731cd7 feat(trace): fetch whole trace at high verbosity` and `6ca2c38b5f
  fix(dagui): let verbosity expand rolled-up spans`.
- **Read the tree for `Host.directory(path: …)`:** with an `include: [...]` arg =
  sparse (good). With **no** `include` = full-tree upload (the bug).

What the trace showed:

- The `edit()` tool body is **already fully sparse** (all 0.0s):
  - `Workspace.file(path: "README.md")` — sparse
  - `Workspace.directory(path: ".", include: ["README.md"])` — sparse
  - `.changes(from: Host.directory(path: …, include: ["README.md"]))` — sparse
- The full sync is in the **`Workspace.withChanges`** that applies the edit's
  `Changeset` to the workspace overlay (11.4s):

  ```text
  Workspace.withChanges(changes: …edit…): Workspace!            11.4s
  ├╴ Host.directory(path: ".../llm-workspace"): Directory!      11.4s   ← NO include
  │  ╰╴ uploading /home/vito/src/dagger/llm-workspace (bin, cmd, core, sdk, …)
  ```

## Root cause

The full sync is `overlayEdit` in `core/schema/workspace.go` (~line 989), building
the overlay's **read root**:

```go
fullBase, _ := s.workspaceOverlayRootfs(ctx, ws)   // pristine host → resolveRootfs(ws, ".", CopyFilter{}, false)
                                                   //             → host.directory(".")  *** no include → full tree ***
fullRoot, _ := edit(fullBase)                      // for withChanges: fullBase.withChanges(changes)
...
newWS.SetRootfs(fullRoot)                          // stored as the overlay Root, used for reads
```

Two facts combine:

1. `workspaceOverlayRootfs(pristineHostWs)` returns `host.directory(".")` with **no
   filter** — the whole tree (lazy until forced).
2. `Directory.WithChanges` (`core/directory.go:2794`) does
   `cache.Evaluate(ctx, parent, changes)` and applies the changeset via
   `ApplySnapshotDiff` — a snapshot-level merge that **must materialize its base**.
   So `fullBase.withChanges(changes)` **forces** the full `host.directory(".")`
   upload at overlay-build time.

It cannot be made lazy at this layer: directory ops are snapshot-backed (not LLB),
and the whiteout-preserving merge only exists as `ApplySnapshotDiff`. (Routing a
diff layer through `withDirectory` would drop whiteouts — that's why `WithChanges`
exists.)

Note that for `withNewFile`-style edits `fullRoot` stays lazy, but the first *read*
through it would force the same full upload. So the fix must change how reads
resolve, not just avoid the eager `withChanges` — which is why the read root has to
go away entirely, not merely be deferred.

Why a read root exists at all: later `Workspace.file` / `Workspace.directory` reads
(via `resolveRootfs → ws.SourceDirectory()`) must reflect *host + overlay edits*,
including reads of arbitrary untouched files.

Note: pristine host workspaces read sparsely already — `buildCoreWorkspace`
(`engine/server/session_workspaces.go:~825`) sets only `hostPath` (no rootfs), so
`resolveRootfs` takes its host branch (`host.directory(absPath, include: filter)`).
The problem is exclusively the **overlay** path.

## The overlay's three representations (context)

`overlayEdit` currently maintains three representations of "host + edits":

1. **`Root`** (`fullRoot`) — full host tree + edits, for reads. **The bug.**
2. **`DeltaRoot`** — edits applied to an *empty* base; never references the host.
3. **`Changes`** — `deltaRoot.changes(from: sparseHostBase(touchedAll))`, the
   cumulative sparse changeset. `Directory.changes` is lazy (wraps Before/After).

Representations 2 and 3 are cheap and stay. Representation 1 is removed.

## Agreed design

**A host-backed overlay workspace is `hostPath + DeltaRoot + TouchedPaths +
Changes`. It has no Root.** Reads resolve sparsely per-call against the host with
the changeset applied on top. Anything else that wants "the tree as a Directory"
fails loudly instead of silently serving a sparse or stale tree.

An earlier draft proposed `Root = deltaRoot`. Rejected: `SourceDirectory()` would
return a sparse tree while claiming to be *the* workspace tree — and there is a
live caller that breaks: see `readConfigBytes` below (untouched `dagger.toml` would
read as *not found* from the delta, and `workspaceConfigInitIfMissing` would then
silently **reinitialize the config**).

### 1. `overlayEdit`: host branch never builds a full root

Restructure so the value/git branch (`ws.HostPath() == ""`) keeps its current
behavior (`fullRoot = edit(fullBase)` → `overlayWorkspaceWithMutation`; in-engine,
nothing to upload), and the host branch:

- runs `edit` **only** on the delta base (empty or prior `DeltaRoot`),
- computes `Changes = deltaRoot.changes(from: sparseHostBase(touchedAll))` as today,
- does **not** call `workspaceOverlayRootfs`, does **not** `SetRootfs`, and stores
  the overlay source with an **empty Root**.

`SourceDirectory()` then returns false for host overlays (already handles nil
Root). Persistence (`RootResultID = 0`) and `attachWorkspaceSource` (skips nil
Root) already tolerate this.

### 2. `resolveRootfs`: host-overlay branch (uniform merge formula)

Add a host-overlay case **before** the generic `SourceDirectory()` branch (which no
longer fires for host overlays anyway) and before the pristine-host branch (which
would silently ignore edits). For a read of `(resolvedPath, filter, gitignore)`:

```text
requested = filter.Include re-rooted under resolvedPath      // "src" + ["foo.go"] → ["src/foo.go"]
            (no includes && resolvedPath != "." → ["<resolvedPath>", "<resolvedPath>/**"])
            (no includes && resolvedPath == "." → no include arg: full host — legitimate)
base   = host.directory(".", include: requested, exclude: re-rooted excludes, gitignore…)
merged = base.withChanges(overlayChanges)                    // sparse ApplySnapshotDiff
result = resolveRootfsFromDirectory(merged, resolvedPath, filter, gitignore-trim?)  // descend + trim
```

Design points, in decided order:

- **Uniform formula, no per-path dispatch (for now).** Always merge; touched-file
  syncs are cached and ~0s. A fast path (touched-only file reads served straight
  from `DeltaRoot`; untouched reads identical to pristine reads) is a possible
  follow-up if traces justify it — matching rules (removed paths, dir prefixes) are
  subtle, so don't build it speculatively.
- **Requested-only base** (not `requested ∪ touched`). The changeset's diff layer
  carries full new content for added/modified files and whiteouts for removals, and
  `ApplySnapshotDiff` already tolerates applying onto bases missing those paths —
  that is exactly how `DeltaRoot` is built on an *empty* base today. Requested-only
  keeps the `host.directory` cache key stable across edits (union would re-key every
  read after every edit). If apply-onto-partial-base surprises in practice, the
  union form is the fallback — cover this with a targeted test (read an untouched
  file, an edited file, and a removed file through the overlay).
- **Over-inclusion is handled by descend + trim.** The diff layer adds *all*
  touched files to `merged`, including ones outside the requested scope. Descending
  to `resolvedPath` and re-applying the filter (what `resolveRootfsFromDirectory`
  already does) trims them. Touched files *inside* the requested scope belong there.
- **Gitignore threads to the base host call only** (same
  `gitignore`/`gitIgnoreRoot` args as the pristine branch — buildkit applies it
  host-side during sync, and it already composes with includes there). Do not
  re-apply gitignore to the merged result: overlay edits win even if a path is
  gitignored, and the sparse tree lacks the `.gitignore` context to evaluate it
  anyway.
- **Read-time host semantics.** Reads now see the host *at read time* + edits,
  rather than host-at-overlay-build-time. This matches pristine workspaces (which
  resolve per-call) — a consistency improvement, worth a line in the commit message.

### 3. `readConfigBytes`: explicit overlay branch

`readConfigBytes` (`core/schema/workspace_config.go:214`) checks
`SourceDirectory()` **before** the host path. With Root gone it would fall through
to the pristine-host branch — wrong when the config file is itself edited: the
config builders (`withConfigValue` → `loadWorkspaceConfigForOverlay` →
`readConfigBytes`, `workspace_builders.go`) chain read-modify-write through the
overlay, so a second `withConfigValue` must see the first.

Add a host-overlay branch: config path ∈ `TouchedPaths` (or under a touched path) →
read from `DeltaRoot` (removed-but-touched then correctly errors not-found);
untouched → existing `bk.ReadCallerHostFile` (a file RPC — no directory sync at
all). Apply the same treatment to lockfile reads if they flow through a
`SourceDirectory()`-first path.

## Caller audit (done — blast radius is bounded)

Every `SourceDirectory()` / `Rootfs()` / `workspaceRootfs` caller, verified against
host overlays with no Root:

- `resolveRootfs` first branch — replaced by the new overlay branch (§2).
- `readConfigBytes` (`workspace_config.go:223`) — **the one real landmine**; fixed
  explicitly (§3).
- `findUp` (~1501), module settings introspection (`workspace_module.go:215`),
  `.git` stat in `ensureWorkspaceGitDirectory` (~1237) — all take the
  `HostPath() != ""` branch first; they already read the *pristine host*, bypassing
  overlay edits. Pre-existing semantics, unchanged by this fix; noted as known.
- `gitRefWorkspaceChanges` (~1384, uses `workspaceRootfs`) — only reachable from
  `workspaceGitUncommitted` when the workspace has a git-ref source; git/value
  overlays keep their full Root. Not reachable for host overlays (those return the
  overlay `Changes` directly at ~1326).
- `overlayWorkspaceWithMutation` (~945) — value/git only after the restructure.
- `export` / `changes` — use `OverlayChanges` only; Root-free already.
- Persistence + attach (`core/workspace.go`) — nil Root already supported
  (`RootResultID` omitted when zero; attach skips nil).

## Out of scope (decided)

- **Whole-tree reads stay expensive.** `Workspace.directory(".")` with no filter
  legitimately materializes the full host (now *only* when actually requested).
  Gitignore-by-default / workspace-configured excludes for whole-tree
  materialization is a semantic change needing its own design.
- **Overlay-aware Directory in core** — a first-class `(host, changeset)` Directory
  variant would make every consumer correct automatically instead of patching
  schema call sites, but reaches into snapshot/filesync internals. Separate
  project if the sharp edges multiply.
- **Per-path fast dispatch** — see §2; follow-up only with trace evidence.

## Validation

- **Re-run the trace** on an `Edit()` after the fix: the `Workspace.withChanges`
  span must contain **no** filterless `Host.directory(path: …)` — only
  `Host.directory(…, include: [...])`. Then read an untouched file and check that
  span is sparse too.
- **Targeted engine test** (the full `TestWorkspaceAPI` suite hangs — previously
  misdiagnosed as a "1800s connection-leak timeout"; the real cause is the
  pre-existing `TestWorkspaceSDKReadersUseStagedOverlay` deadlock described at the
  bottom of this doc, so ALWAYS narrow with `--run` and EXCLUDE that test):

  ```console
  dagger --x-release 1.0.0-beta.6 call engine-dev test \
    --run 'TestWorkspaceAPI/(TestHostWorkspaceOverlayAndExport|TestHostWorkspaceSparseOverlayDiff|TestWorkspaceConfigBuildersStageOverlay|TestHostWorkspaceFunctionalOverlayAPIsChain)' \
    --pkg ./core/integration --test-verbose
  ```

  (Bash tool needs `dangerouslyDisableSandbox: true`.)
- **New tests** on a host overlay (after one edit):
  - `Workspace.file` of an *untouched* file → host content.
  - `Workspace.file` of the *edited* file → edited content.
  - `Workspace.file` of a *removed* file → not-found (whiteout applies onto a
    requested-only base).
  - `Workspace.directory(".", include:[glob])` `.entries`/`.glob` → merged view,
    extras trimmed (doug2's `reminderPrompt` pattern:
    `directory("/", include: ["**/*.md"]).glob("*.md")`).
  - Chained `withConfigValue` after an *unrelated* file edit → second read sees the
    first config write (the `readConfigBytes` regression).

## Reproduce

```console
go build -o ./bin/dagger ./cmd/dagger/          # rebuild CLI
# (after schema changes also redeploy the engine: hack/dev)
dagger -W <repo> -m ./modules/doug2 call agent  # LLM shell with doug2 tools: read/write/edit/todoWrite
# have the model edit a file twice; inspect the Edit() span in the trace (link printed in the TUI)
```

## Key code locations (as-built, post-rebase)

- `core/schema/workspace.go`
  - `overlayEdit` — the single edit funnel: value/git/rootless branch keeps the
    upstream full-root accumulation; host (ClientLocal) branch applies the edit
    to the delta base (`OverlayDeltaRoot()` = prior `Changes.After`, or empty)
    and computes `Changes = deltaRoot.changes(from: sparseHostBase(touchedAll))`.
  - `sparseHostBase`, `unionPaths`, `changesetTouchedPaths` — diff-side helpers.
  - `resolveRootfs` — host-overlay branch first, gated on `ClientLocalBase()`.
  - `resolveHostOverlayRootfs` + `rerootPatterns` — sparse requested base +
    `withChanges` + descend/trim (§2's uniform merge formula).
  - `workspaceOverlayRootfs` — whole-tree reads only (module-source loading,
    install flows); for host overlays resolves as full host + changeset.
  - `withChanges`/`withNewFile`/`withNewDirectory` route through `overlayEdit`;
    also `workspace_builders.go` (`stageWorkspaceConfigAndLock`, `withoutModule`,
    `workspaceWithChangeset`).
- `core/schema/workspace_config.go`
  - `readConfigBytes` — host branch dispatches on `OverlayPathTouched(configFile)`:
    touched → `DirectoryReadFile(Changes.After)`, untouched → `ReadCallerHostFile`.
- `core/schema/workspace_lock.go`
  - `readWorkspaceLockForOverlay` / `workspaceLockChangeset` — include-scoped to
    the lock paths (follow-up commit); previously full-tree even on pristine hosts.
- `core/workspace.go`
  - `WorkspaceSourceOverlay{Base, TouchedPaths, Changes}` — TouchedPaths is the
    only field added to upstream's model.
  - `SourceDirectory()` — overlay case returns false when Base is ClientLocal.
  - `ClientLocalBase()`, `OverlayDeltaRoot()`, `OverlayTouchedPaths()`,
    `OverlayPathTouched()` — accessors.
  - persistence: `TouchedPaths` as plain JSON alongside `ChangesID`.
- `core/directory.go`
  - `Directory.WithChanges` — `cache.Evaluate` + `ApplySnapshotDiff`; forces
    its base (why a stored full root can't be lazy). Tolerates sparse/empty
    bases — that's how the delta root is built.
- `engine/server/session_workspaces.go`
  - `buildCoreWorkspace` — local host workspace stores `hostPath` only, no
    rootfs (pristine reads are sparse). Non-git local dirs become rootless
    sources (host path set, reads pinned to empty).

## Branch context

- The object-tools rework landed out-of-band (`hack/designs/workspace-agents.md`): the
  Dang eval harness (`dang_eval`/`inspect`, `core/llm_dang*.go`) is **replaced** by
  `LLM.withTools(object)` → one tool per method of a bound object
  (`core/llm_object_tools.go`); `Query.currentNode` lets a module bind itself.
  `modules/doug2/main.dang` is the reference agent (read/write/edit/todoWrite). HEAD ~
  `6790731cd7`.
- The sparse-*diff* commit `8e2ffa59e9` is in history; object-tools sits on top. This
  read-root full-sync is the remaining half: the diff (`changes`/`export`) was
  optimized but reads were deliberately left on the full Root — which the overlay
  *build* forces.
- `modules/doug2/main.dang` already uses `source.directory(".", include:[path])` /
  `source.file(path)` — the sparse intent is in the module; the leak is engine-side in
  the overlay build, not the Dang.

## Pre-existing hang: `TestWorkspaceSDKReadersUseStagedOverlay` (dagql self-deadlock)

Diagnosed 2026-07-10 while validating this fix. **Unrelated to the overlay work**
— reproduced identically on `6790731cd7` without these changes (solo run, fresh
nested engine, byte-identical goroutine stack). This is what the old "1800s
connection-leak timeout" note actually was.

**Symptom:** the test's query hangs forever on `Workspace.sdks`. In the trace the
`sdks` span shows `∅ <minutes>` while all of its inner spans complete in 0.0s —
the resolver finishes; the hang is in cache bookkeeping afterward, so it is
invisible to tracing.

**Mechanism** (single-goroutine self-deadlock):

1. `Workspace.sdks` returns a `dagql.ObjectResultArray[*core.WorkspaceSDK]` whose
   items are built with `NewObjectResultForCurrentCall`
   (`core/schema/workspace_sdk.go` → `workspaceSDKResults`) — each item's
   `ResultCall` frame IS the `sdks` call itself.
2. When the call completes, `dagql.(*Cache).initCompletedResult` (cache.go:~4343)
   **indexes the result in the egraph** (`indexWaitResultInEgraphLocked`), opens
   `res.attachDepsWaitCh`, then walks the value's deps. For arrays,
   `ObjectResultArray.AttachDependencyResults` attaches **each item as a result**.
3. `attachResult(item)` (cache.go:1926): the item is fresh (`shared.id == 0`), so
   it derives a request digest from the item's frame — **the parent call's own
   digest** — and `lookupCacheForRequest` hits the just-indexed in-flight parent.
   `ensurePersistedHitValueLoaded` (cache_persistence_import.go:567) then waits on
   the parent's `attachDepsWaitCh` — which only the current goroutine will close,
   after this very walk returns. Deadlock.

Goroutine dumps saved during diagnosis: `/tmp/nested-engine-goroutines.txt` (group
run), `/tmp/nested-engine-goroutines2.txt` (solo, with fix),
`/tmp/control-goroutines.txt` (solo, control without fix) — all show one stuck
goroutine:

```text
ensurePersistedHitValueLoaded  cache_persistence_import.go:567  [select, N minutes]
lookupCacheForRequest          cache_egraph.go:915
attachResult                   cache.go:1983
attachDependencyResults        (ObjectResultArray items)
initCompletedResult            (the "sdks" call, own attachDepsWaitCh open)
```

To capture a dump from a hung nested dev engine (no wget/curl in the image; bash
`/dev/tcp` works):

```bash
docker exec <outer-engine-ctr> sh -c \
  'nsenter -t <nested-engine-pid> -n bash -c \
   "exec 3<>/dev/tcp/127.0.0.1/6060; printf \"GET /debug/pprof/goroutine?debug=2 HTTP/1.0\r\n\r\n\" >&3; cat <&3"'
```

Killing the stuck `/.dagger-cli --silent query` process (SIGQUIT) inside the outer
container unhangs the run (the server-side wait exits via `ctx.Done`).

**Fix direction (engine, not workspace):** `attachResult` /
`ensurePersistedHitValueLoaded` needs a self-hit guard — never block on an
`attachDepsWaitCh` owned by the attach walk currently on the stack (e.g. compare
the hit's shared result against the parent being completed and short-circuit, or
thread the in-flight attach set through ctx). Alternatively, don't index the
completed result in the egraph until after its deps are attached. Note
`Workspace.sdk` (singular) does not hang: its value is the object itself, so the
walk attaches the object's *inner* deps rather than the object-as-result. Any
other resolver returning an `ObjectResultArray` of `NewObjectResultForCurrentCall`
items is exposed to the same deadlock.
