# Lockfile: Lookup Resolution

## Status: Partially Implemented

This is the general design reference for Dagger lockfiles.

It describes:

- the lock entry format
- lock policy and lock mode semantics
- lock update flows
- what is implemented now
- what remains to be built

## Problem

1. Symbolic lookup inputs drift over time.
2. Dagger needs one lock model across lookup functions, not one-off behavior per subsystem.
3. Reproducible runs need a clear distinction between recorded results, live resolution, and explicit refresh.
4. Lock maintenance must work both as a whole-lockfile operation and while running real workloads.
5. Some consumers are implemented today, but the full target surface is larger.

## Terminology

| Term | Meaning |
| --- | --- |
| Lookup function | A function that turns symbolic inputs into a concrete resolved result. |
| Lookup inputs | The symbolic arguments to the lookup function. |
| Lookup result | The concrete resolved value: digest, commit SHA, immutable ID, and so on. |
| Lock entry | A recorded mapping from `(namespace, operation, inputs)` to `(value, policy)`. |
| Lock policy | Entry-level refresh intent: `pin` or `float`. |
| Lock mode | Run-level read/write behavior: `disabled`, `live`, `pinned`, or `frozen`. |
| Lockfile snapshot | Parsed `.dagger/lock` state loaded into session-owned live state. |
| Lockfile delta | Tuple upserts buffered in session-owned live state before final export. |

## Lock Entry Format

Lockfiles are JSON lines. The first line is the version tuple:

```json
[["version","1"]]
```

Each entry is a flat ordered tuple:

```json
[namespace, operation, inputs, value, policy]
```

Examples:

```json
["","container.from",["alpine:latest","linux/amd64"],"sha256:3d23f8","pin"]
["","git.branch",["https://github.com/dagger/dagger.git","main"],"495a8c8ce85670e58560a9561626297a436225c0","float"]
```

Rules:

- `namespace` is `""` for core lookups.
- `operation` is a stable lookup key such as `container.from` or `git.branch`.
- `inputs` is always an ordered positional array.
- `value` is the resolved immutable result.
- `policy` is `pin` or `float`.
- dictionaries, maps, and named-argument encodings are forbidden anywhere in lock entries
- ordering is deterministic by `(namespace, operation, inputs-json)`
- legacy object-shaped result envelopes are invalid

## Lock Policy

Lock policy is stored per entry.

| Policy | Meaning |
| --- | --- |
| `pin` | Prefer the recorded value when the mode allows it. |
| `float` | Prefer live resolution when the mode allows it. |

What users should memorize:

- `pin`: stay on this recorded result
- `float`: refresh this result when live resolution is allowed

## Lock Mode

Lock mode is chosen per run, typically with `--lock`.

| Mode | Meaning |
| --- | --- |
| `disabled` | Ignore the lockfile completely. |
| `live` | Resolve everything live and record the result. |
| `pinned` | Reuse pinned entries, resolve everything else live, and record the result. |
| `frozen` | Resolve only from the lockfile and fail on misses. |

What users should memorize:

- `disabled`: feature off
- `live`: refresh while running
- `pinned`: prefer stable pins, refresh the rest
- `frozen`: use the lockfile only

## Behavior Matrix

| Mode | Existing `pin` entry | Existing `float` entry | Missing entry |
| --- | --- | --- | --- |
| `disabled` | resolve live, do not read or write lockfile | resolve live, do not read or write lockfile | resolve live, do not write |
| `live` | resolve live and rewrite | resolve live and rewrite | resolve live and write |
| `pinned` | use lockfile value | resolve live and rewrite | resolve live and write |
| `frozen` | use lockfile value | use lockfile value | error |

Important consequence:

- in `frozen`, an existing `float` entry is still treated as a recorded snapshot
- `float` only matters in modes that allow live resolution

## Design Delta From Current Branch

This section is the proposed diff from the current `lockfile` branch.

It is intentionally narrow:

- it only changes the ambient live lock path
- it does not introduce a new public DagQL lockfile API
- it does not redesign `currentWorkspace.update()` / `dagger lock update()` in the same
  change

| Area | Current branch | Proposed |
| --- | --- | --- |
| Ambient reads | each lock-aware consumer rereads `.dagger/lock` from caller host | read `.dagger/lock` at most once per bound workspace in a session, via lazy init into `daggerSession` state |
| Ambient writes | reread + merge + export on each touched lookup | mutate session-owned workspace state in memory; export once on graceful shutdown |
| State owner | schema-local `workspaceLookupLock` helper | `daggerSession` |
| Concurrency | repeated sync caller-host I/O guarded only at export time | one workspace-keyed lock state map on `daggerSession`, guarded by an RW mutex |
| Synchronization boundary | schema helpers still own part of write coordination | locking and merge/export coordination live in `engine/server` only |
| Hot path boundary | schema consumers do caller-host lockfile I/O directly | schema consumers call lock methods exposed through `core.Query.Server` |
| DagQL role | not part of the live path today | still not part of the live path in this change |
| Explicit update | `currentWorkspace.update(): Changeset!` | unchanged in this change |

Concretely, the design change is:

- store live lock state on `daggerSession`, keyed by workspace binding
- initialize it lazily on first lock access
- guard it with an RW mutex on `daggerSession`
- expose read/write through engine server methods and the `core.Query.Server` interface
- export it back once when the main client shuts down gracefully
- keep lockfile synchronization and final export inside `engine/server`, not `core/schema`

## Update Flows

There are three real update paths:

### `dagger lock update`

Refresh entries already present in `.dagger/lock`.

Properties:

- best-effort by entry type
- uses the current environment's ambient authentication
- does not discover new entries on its own
- thin CLI wrapper over `currentWorkspace.update()`

### `--lock=live`

Run the real workload in live lock mode.

Properties:

- refreshes existing entries the run touches
- discovers missing entries the run touches
- reads `.dagger/lock` at most once per bound workspace in a session
- mutates the lockfile server-side throughout the session
- exports the final lockfile once on graceful session shutdown
- is the authoritative discovery path for new lock entries

### `currentWorkspace.update(): Changeset!`

Engine API for refreshing entries already present in `.dagger/lock`.

Properties:

- returns a `Changeset` instead of writing directly
- refreshes supported existing entries only
- errors if `.dagger/lock` does not exist

This design update leaves explicit maintenance alone. It only changes the ambient live
path.

## Session-State Lifecycle

### Session State

Store live lockfile state on `daggerSession` in `engine/server/session.go`.

One session may host more than one bound workspace, so this state should be a
map keyed by workspace binding, not a single session-global lockfile.

Recommended shape:

- `lockFiles map[workspaceLockKey]*workspaceLockState`
- `lockFileMu sync.RWMutex`

Where:

- `workspaceLockKey` identifies the bound workspace for lockfile purposes
- `workspaceLockState` holds:
  - parsed `*workspace.Lock`
  - `loaded` bit
  - `dirty` bit
  - any precomputed lockfile path needed for final export

Properties:

- lazy init on first lock access
- read `.dagger/lock` from caller host at most once per bound workspace
- all later reads come from in-memory session state
- all live writes update that same in-memory session state
- clients that share a bound workspace share one live lock state
- clients bound to different workspaces get different live lock states

### Access Pattern

Expose lockfile access through engine server methods:

- add methods on the engine server that find the current client/session
- expose corresponding methods on `core.Query.Server`
- have `core/` and `core/schema/` callers use those methods

This follows the existing server/session pattern already used elsewhere in the engine.

### Live Execution Path

Ambient execution (`--lock=live`, plus the write-through cases of `pinned`) should:

- read current session lockfile state
- resolve the live lookup
- update current session lockfile state in memory

It should not:

- reread `.dagger/lock` from the caller host on each lookup
- export `.dagger/lock` after each lookup
- route lock mutation through nested DagQL calls

### Final Export

When the main client shuts down:

- if a workspace lock state was never loaded, do nothing
- if it was loaded but never modified, do nothing
- if it was modified, export it back once
- serialize cross-session export by lockfile path inside `engine/server`

The natural place for this is the `/shutdown` endpoint.

To preserve current behavior under cross-session contention, the final export can reuse
the existing "merge against latest on-disk state" logic at shutdown time instead of on
every lookup.

The important cleanup constraint is:

- `core/schema/lockfile.go` should not own any global mutex map or export-time
  synchronization
- it should only adapt schema callers to `core.Query.Server`
- `engine/server` should own the actual read/write/merge/export implementation

### Anti-goals

- do not add a new public DagQL lockfile API as part of this change
- do not make hot-path lock reads/writes re-enter DagQL
- do not keep direct per-consumer caller-host lockfile reads in schema code
- do not redesign `currentWorkspace.update()` / `dagger lock update()` in the same
  change

## Lookup Coverage

Target model: one lock system for all lookup functions.

Current core operation keys:

| Operation | Inputs | Result |
| --- | --- | --- |
| `container.from` | `[imageRef, platform]` | image digest |
| `modules.resolve` | `[source]` | commit SHA |
| `git.head` | `[remoteURL]` | commit SHA |
| `git.branch` | `[remoteURL, branchName]` | commit SHA |
| `git.tag` | `[remoteURL, tagName]` | commit SHA |
| `git.ref` | `[remoteURL, refName]` | commit SHA |

Notes:

- `git.commit` is already pinned by input and does not create lock entries
- `modules.resolve` defaults to `pin` for tags and explicit commits, `float`
  otherwise
- `git.ref` only creates lock entries for mutable refs
- the recorded Git URL should be the resolved canonical remote URL used for transport

## Current Implementation

### Implemented

- [x] tuple lockfile substrate in `util/lockfile`
- [x] flat lock entry format `[namespace, operation, inputs, value, policy]`
- [x] hard cutover to ordered positional tuples only
- [x] lock policy parsing and validation
- [x] lock mode parsing and transport through CLI and client metadata
- [x] nested-client and module-runtime lock mode propagation
- [x] local workspace lockfile read/write helpers
- [x] serialized lockfile writes with merge against latest on-disk state
- [x] `container.from` lookup locking
- [x] `modules.resolve` lookup locking
- [x] Git lookup locking for `head`, `branch`, `tag`, and mutable `ref`
- [x] `currentWorkspace.update(): Changeset!` temporary umbrella API
- [x] `dagger lock update`
- [x] execution-driven discovery via `--lock=live`
- [x] unit and integration coverage for substrate, CLI, container, Git, module, and nested execution
- [x] session-backed live lock state on `daggerSession`
- [x] lazy one-time lockfile load on first live lock access
- [x] graceful-shutdown export of dirty session lock state
- [x] `core.Query.Server` lock methods for hot-path consumers
- [x] removal of direct per-consumer caller-host lockfile reads
- [x] engine-owned lock synchronization for final export

### Not Yet Implemented From This Design

- [ ] integration coverage for session shutdown export behavior

### Implemented Semantics

- [x] `--lock=disabled|live|pinned|frozen`
- [x] default lock mode is `disabled`
- [x] `live` writes through
- [x] `pinned` writes through for `float` and missing entries
- [x] `frozen` reuses both `pin` and `float` entries and fails on misses

### Current Consumer Defaults

- [x] `container.from` defaults to `pin`
- [x] `modules.resolve` defaults to `pin` for tags and commits, `float` otherwise
- [x] `git.branch` defaults to `float`
- [x] `git.head` defaults to `float`
- [x] `git.tag` defaults to `pin`
- [x] `git.ref` defaults to `pin` for tags and `float` for other mutable refs

## Current Implementation Constraints

These are current branch facts, not necessarily the final target for all future workspace behavior.

- lockfile location is derived from the detected workspace directory
- on `workspace-plumbing`, that means `.dagger/lock` sits under the current detected workspace path, not necessarily repo root
- lockfile mutation is local-only
- remote workspaces currently error for lock-aware mutation paths
- hot lookup paths do not reread `.dagger/lock` from the caller host after the session
  snapshot is loaded
- live lock writes are buffered in session-owned workspace state and exported once on
  graceful shutdown
- final export still rereads latest on-disk state and merges the session delta before
  writing
- `dagger lock update` relies on ambient authentication for private registries and repositories

## Implementation Principle

New lockfile consumers should attach to existing lookup resolution flows rather than
introducing new engine hooks just for locking.

Why:

- the existing lookup path is already the source of truth for symbolic input parsing
  and live resolution
- reusing that path keeps lock semantics aligned with normal runtime behavior
- it avoids duplicating resolution logic in parallel lock-specific plumbing
- it makes the same consumer reusable across workspace-specific and generic API
  entrypoints

Implication:

- when adding a new consumer such as `modules.resolve`, hook lock read/write behavior
  into the current module resolution path
- have that path consult the session-owned live lock state, not raw caller-host
  file reads
- do not refactor the engine to create a second resolution hook whose only purpose is
  lockfile integration

## Implementation Plan

This section is intentionally concrete. It is the level of detail that should have
been reviewed before implementation so type shapes and file boundaries stay aligned.

### `core/query.go`

#### Server lock accessors

```go
type Server interface {
 CurrentWorkspaceLock(context.Context) (*workspacepkg.Lock, bool, error)
 SetCurrentWorkspaceLookup(context.Context, string, string, []any, workspacepkg.LookupResult) error
}
```

These are the only live-path hooks schema consumers should need.

### `core/schema/lockfile.go`

#### Thin schema adapter for live lookups

```go
type workspaceLookupLock struct {
 ctx   context.Context
 query *core.Query
 lock  *workspace.Lock
}

func loadWorkspaceLookupLock(ctx context.Context, query *core.Query) (*workspaceLookupLock, error)
func (l *workspaceLookupLock) SetLookup(namespace, operation string, inputs []any, result workspace.LookupResult) error
```

The live-path responsibilities in this file should be narrow:

- ask `core.Query.Server` for the current lock snapshot
- ask `core.Query.Server` to stage an upsert
- update the local clone returned to the resolver so repeated lookups in one call stay coherent

This file should not:

- own any global mutex map
- coordinate final export
- expose engine-facing lockfile I/O helpers

### `core/schema/container.go`

#### `container.from` lock integration

```go
lookupLock, err := loadWorkspaceLookupLock(ctx, query)
resolution, err := resolveLookupFromLock(lockMode, lookupLock.lock, lockContainerFromOperation, inputs, workspace.PolicyPin)
```

After live resolution:

```go
if resolution.ShouldWrite {
 err = lookupLock.SetLookup(lockCoreNamespace, lockContainerFromOperation, inputs, result)
}
```

### `core/schema/modulesource.go`

#### `modules.resolve` lock integration

The shape is the same as `container.from`:

- read session-backed lock state through `loadWorkspaceLookupLock`
- use `resolveLookupFromLock` for policy/mode behavior
- stage the updated lookup through `SetLookup` after live resolution

### `core/schema/git.go`

#### Git lookup integration

Each mutable Git lookup follows the same pattern:

- `git.head`
- `git.branch`
- mutable `git.ref`

Pinned Git lookups such as immutable refs do not create lock entries.

### `core/workspace/lock.go`

#### Shared lock mutation helpers

```go
func (l *Lock) Clone() (*Lock, error)
func (l *Lock) Merge(other *Lock) error
```

These helpers are the shared mutation substrate used by both:

- session-owned live lock state
- explicit `currentWorkspace.update()` refresh paths

### `engine/server/session.go`

#### Session-owned lock state

```go
type daggerSession struct {
 lockFiles  map[workspaceLockKey]*workspaceLockState
 lockFileMu sync.RWMutex
}

type workspaceLockKey struct {
 ownerClientID string
 lockPath      string
}

type workspaceLockState struct {
 ws       *core.Workspace
 lockPath string
 lock     *workspace.Lock
 delta    *workspace.Lock
 loaded   bool
 dirty    bool
}
```

#### Live-path server methods

```go
func (srv *Server) CurrentWorkspaceLock(ctx context.Context) (*workspace.Lock, bool, error)
func (srv *Server) SetCurrentWorkspaceLookup(ctx context.Context, namespace string, operation string, inputs []any, result workspace.LookupResult) error
```

These methods:

- resolve the current workspace binding
- lazy-load the lock snapshot once per workspace key
- clone on read so callers cannot mutate session state directly
- stage writes into both the working lock and the `delta`

#### Engine-owned lockfile I/O helpers

```go
func workspaceLockPath(ws *core.Workspace) (string, error)
func readWorkspaceLockState(ctx context.Context, bk interface{ ReadCallerHostFile(context.Context, string) ([]byte, error) }, ws *core.Workspace) (*workspace.Lock, bool, error)
func exportWorkspaceLockToHost(ctx context.Context, bk *buildkit.Client, ws *core.Workspace, lock *workspace.Lock) error
```

These helpers stay in `engine/server` for the live path. `core/schema` should not be
used as a utility package for engine-owned merge/export logic.

#### Final export on shutdown

```go
func (srv *Server) flushWorkspaceLocks(ctx context.Context, client *daggerClient) error
```

The export flow should be:

```go
srv.locker.Lock(export.lockPath)
defer srv.locker.Unlock(export.lockPath)

latest, _, err := readWorkspaceLockState(workspaceCtx, bk, export.ws)
if err == nil {
 err = latest.Merge(export.delta)
}
if err == nil {
 err = exportWorkspaceLockToHost(workspaceCtx, bk, export.ws, latest)
}
```

Important constraints:

- the per-session `lockFileMu` protects session-owned state
- cross-session serialization by lock path happens in `engine/server`
- no schema-level synchronization participates in this flow

## Remaining Work

### High-priority design/implementation gaps

- [ ] add direct coverage for session shutdown export behavior
- [ ] `http` lookup locking
- [ ] decide whether additional Git lookup operations such as `refs`, `symrefs`, or `isPublic` belong in the lock model
- [ ] remote-workspace read semantics, if any
- [ ] final initialized-workspace semantics for `.dagger/lock` anchoring

### UX and maintenance follow-ups

- [ ] decide whether `disabled` should remain the long-term default
- [ ] decide whether `dagger lock update` should gain richer output or selection flags
- [ ] decide whether lock update should prune stale entries
- [ ] decide whether to add a public lockfile DagQL API later

### Longer-term extensions

- [ ] full offline / airgapped design
- [ ] extension model for user-defined lookup functions
- [ ] broader conformance coverage as new lookup consumers are added

## Workspace Relationship

Lockfiles are attached to workspace bindings.

Why:

- the lockfile path is derived from the bound workspace
- host filesystem access for local workspaces routes through the workspace owner
- deterministic workspace loading eventually needs recorded lookup results
- `modules.resolve` is the clearest workspace-driven lookup consumer

So the intended long-term shape is:

- one lock model for core lookups
- one lock model for workspace-owned lock state
- one maintenance interface for refreshing recorded results

## Reference Commands

```bash
dagger --lock=disabled call ...
dagger --lock=live call ...
dagger --lock=pinned call ...
dagger --lock=frozen call ...
dagger lock update
```
