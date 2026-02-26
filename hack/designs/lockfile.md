# Workspace Lockfile

## Status: Draft

Builds on:
- [Part 1: Workspaces and Modules](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73)
- [Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)

## Problem

1. Lookup inputs across Dagger are often symbolic (`branch`, `tag`, `latest`, mutable HTTP URLs), so resolution can drift over time.
2. Workspace loading currently has no `.dagger/lock` read path, so there is no deterministic lock resolution for workspace-installed git modules.
3. Legacy `dagger.json` toolchain/blueprint `pin` fields are parsed but not preserved in the new workspace flow.
4. There is no consistent policy model for whether a lookup should stay fixed vs automatically refresh.
5. Airgapped execution needs both resolution determinism and clear failure behavior when network is unavailable.

## Solution

Add a machine-managed `.dagger/lock` file that records lookup results for modules, git, HTTP, and container references. Keep `.dagger/config.toml` human-editable and symbolic. The engine reads lock entries at lookup time and enforces per-entry update policy.

Adopt the lockfile tuple format and core plumbing from PR [#11156](https://github.com/dagger/dagger/pull/11156) (branch `universal-lockfile`), adapted to workspace location and module-v2 loading.

Each lock entry stores:
- resolved value.
- policy: `pin` (manual update) or `float` (auto-update allowed).

Execution mode controls updates:
- `lock=strict`: no updates, no missing-entry discovery.
- `lock=update`: update all entries (including `pin` policy entries).
- `lock=auto` (default): `strict` for `pin` policy, `update` for `float` policy.

`--offline` is a separate execution flag layered on top of lock mode. It disables network access and requires resolution/content from lock + local stores.

Implementation rolls out incrementally:
- v1: workspace `modules.resolve` with `pin|float` policy.
- v2+: apply same model to `core.git.*`, `core.http.*`, and container registry lookups.

## Design

### Goals

- Deterministic lookup resolution with explicit update policy (`pin|float`).
- Keep `config.toml` ergonomic for humans.
- Make lock behavior engine-driven (CLI remains thin).
- Preserve DRY by sharing read/write helpers with existing workspace config flows.
- Keep one lock format for all lookup classes.

### Non-goals (v1)

- Shipping every lookup class in the first implementation increment.
- Changing `dagger.json` dependency lock behavior.
- Defining all update-command UX flags now (targeted update filters can follow).

### File location

`.dagger/lock` at workspace root (sibling to `config.toml`).

### Lock schema

Use the tuple format from PR #11156 (JSON lines):

```json
[["version","1"]]
["modules","resolve",["github.com/dagger/go-toolchain@v1.0"],{"value":"3d23f8ef4f4f8f95e8f4c10a6f2b8b7d1646e001","policy":"pin"}]
["core","git.resolve",["https://github.com/acme/ci","refs/heads/main"],{"value":"6e4d4d1f5d8e7d8f1a8f74264f63e1f9c5f2bb0c","policy":"float"}]
```

Tuple shape:
1. `module` (string)
2. `function` (string)
3. `args` (ordered JSON array)
4. `result` (JSON value)

Result envelope:
- `value`: resolved result (commit digest/content digest/etc)
- `policy`: `pin` or `float`

For workspace module resolution:
- `module = "modules"`
- `function = "resolve"`
- `args = [<workspace-module-source>]`
- `result.value = <git-commit-sha>`
- `result.policy = "pin" | "float"`

Local modules (`modules/foo`, `../foo`) are not looked up remotely and do not require lock entries.

### Reuse from PR #11156

Reusable as-is:
- `util/lockfile` deterministic tuple reader/writer (`Load`, `Save`, `Get`, `Set`)
- Version header convention: `[["version","1"]]`
- Ordered argument arrays for deterministic keys

Adapt for workspaces:
- Path resolution: old code used `dagger.lock` next to `dagger.json`; new code should use `.dagger/lock` from workspace detection.
- Lookup namespace: old examples used `core.*` functions; workspace module locking uses `modules.resolve`.
- v1 does not require the session attachable RPC (`engine/session/lockfile`) because workspace module locking can be handled directly in workspace load/mutate flows.

### API annotation

Lookups get a single policy annotation:
- `pin: true` -> `policy = "pin"` (manual update only)
- `pin: false` -> `policy = "float"` (auto-update allowed)

If annotation is omitted, API-specific defaults apply (to be documented per lookup API). Explicit annotation always wins.

### Lock modes

`lock=strict`:
- lock entries are required for remote lookup keys.
- no entry may be updated.
- lock file is read-only for the run.

`lock=update`:
- existing entries are refreshed.
- missing entries may be discovered and written.
- `pin` and `float` policy entries are both updated (explicit user intent).

`lock=auto` (default):
- `pin` policy entries follow `strict`.
- `float` policy entries follow `update`.

### Offline behavior

`--offline` is a separate execution flag, not a lock mode. It composes with lock mode:
- disables remote discovery/fetch.
- requires lookup resolution to come from lock state.
- requires content bytes to exist in local cache/store.

`--offline` with `lock=update` is invalid (cannot update without network).

### Engine load path

1. `workspace.Detect()` reads `.dagger/config.toml` and `.dagger/lock` (if present).
2. `detectAndLoadWorkspaceWithRootfs()` maps config modules to `pendingModule`.
3. For each pending module:
   - lookup `("modules", "resolve", [source])`.
   - if present, set `pendingModule.Pin` from `result.value` and track `result.policy` (`pin`/`float`).
   - if missing, behavior depends on lock mode (`strict` fails, `auto/update` may resolve).
4. `loadModule()` passes `refPin` when `pendingModule.Pin != ""`.

This keeps module resolution engine-driven and lets lock mode decide if/when updates occur.

### Compat mode and migration

- Compat-mode legacy toolchains/blueprints should carry `pin` directly from legacy `dagger.json` into `pendingModule.Pin` (no lockfile required for this path).
- `dagger migrate` writes lock entries with `policy="pin"` when legacy toolchain/blueprint entries include `pin`.

### Write path (mutating workspace operations)

#### `workspace.install`

- Resolve module as today.
- If resolved source is git:
  - write/update lock tuple `["modules","resolve",[source],{"value":pin,"policy":...}]`.
  - policy comes from install annotation/flag (`pin` default for workspace installs unless overridden).
- If local source:
  - no lock entry is needed.
- Optional prune (v1.1): remove orphaned `"modules.resolve"` entries whose source is not present in current `config.toml`.

#### `workspace.moduleInit`

- Installs a local module in workspace config.
- No lock entry is written.

#### `workspace.configWrite`

- Keep generic key writing as-is.
- After config write, optionally prune orphaned `"modules.resolve"` entries (same helper as install).
- Do not auto-resolve new entries from `configWrite`; resolution stays explicit in install/update commands.

### Workspace update UX

Add `dagger workspace update [MODULE...]`:

- No args: update all git workspace modules (including `pin` policy entries, by explicit user intent).
- Args: update selected workspace module names.
- Leaves `config.toml` untouched; rewrites `.dagger/lock` module resolutions.
- Output per changed module: `<name> <oldPin> -> <newPin>`.

This introduces a lock-driven update workflow without changing existing module dependency update semantics (`dagger update` for `dagger.json` modules).

### Remote workspace behavior

- Remote workspace (`-C <git-ref>`) reads lock file from cloned repo and applies lock resolutions/policies.
- Mutating commands remain disallowed for remote workdirs (no lock writes).

### Error model

- Missing lock file:
  - `lock=strict`: hard error on first remote lookup key.
  - `lock=auto|update`: no error; entries are created as needed.
- Malformed lock file: hard error (explicit fix required).
- Unknown lock entries (module/function not used by current engine path): ignore.
- Invalid result envelope (missing `value` or bad `policy`): hard error.
- `pin` policy result unavailable at fetch time (for example missing digest in upstream + no local content): hard error.

## Tasks

### Dependency graph

```text
#1 Lock file types + parse/serialize helpers
 |
 v
#2 Engine read path (Detect -> pendingModule.Pin+Policy -> loadModule refPin)
 |
 +-----------------------+
 |                       |
 v                       v
#3 Workspace install    #5 Compat mode legacy-pin passthrough
   write/prune lock        (`dagger.json` `pin` field)
 |
 v
#4 Workspace configWrite
   stale-lock pruning
 |
 v
#6 Workspace update command/API
 |
 v
#7 Migration writes lock entries
 |
 v
#8 Integration tests
```

### Task list

- [ ] **#1: Add lock schema in `core/workspace`**  
  Port/adapt `util/lockfile` from PR #11156 and add workspace helper wrappers for `"modules.resolve"`.

#### #1 Detailed Checklist (file-level)

1. Add generic tuple lockfile package.
   File: `util/lockfile/lockfile.go`
   Scope: port deterministic tuple format from PR #11156, but expose byte-oriented APIs for workspace integration.
   API:
   - `func Parse(data []byte) (*Lockfile, error)`
   - `func (l *Lockfile) Marshal() ([]byte, error)`
   - `func (l *Lockfile) Get(module, function string, args []any) (any, bool)`
   - `func (l *Lockfile) Set(module, function string, args []any, result any) error`
   - `func (l *Lockfile) Delete(module, function string, args []any) bool`
   Behavior:
   - Require version header `[["version","1"]]` on non-empty files.
   - Preserve unknown entries (do not drop tuples outside workspace usage).
   - Stable output ordering by `(module, function, args-json)`.
   - Support structured result envelope `{value, policy}`.

2. Add generic lockfile tests.
   File: `util/lockfile/lockfile_test.go`
   Cases:
   - parse/marshal round-trip.
   - deterministic ordering.
   - duplicate-key overwrite semantics.
   - malformed and empty-file handling.

3. Add workspace-specific wrappers for module resolutions.
   File: `core/workspace/lock.go`
   API:
   - `func ParseLock(data []byte) (*Lock, error)`
   - `func NewLock() *Lock`
   - `func (l *Lock) Marshal() ([]byte, error)`
   - `func (l *Lock) GetModuleResolve(source string) (pin string, policy LockPolicy, ok bool)`
   - `func (l *Lock) SetModuleResolve(source, pin string, policy LockPolicy) error`
   - `func (l *Lock) DeleteModuleResolve(source string) bool`
   - `func (l *Lock) PruneModuleResolveEntries(validSources map[string]struct{}) int`
   Representation:
   - maps to tuple key `("modules", "resolve", [source])`.

4. Add workspace lock wrapper tests.
   File: `core/workspace/lock_test.go`
   Cases:
   - source -> pin+policy lookup/set/delete.
   - policy validation (`pin`/`float` only).
   - prune behavior against `config.toml` source set.
   - unknown tuple preservation (`core.git.*` etc.).
   - marshal determinism.

5. Add workspace constants.
   Files: `core/workspace/detect.go` (constants block) and `core/workspace/lock.go`
   Constants:
   - `LockFileName = "lock"`
   - `PolicyPin = "pin"` and `PolicyFloat = "float"`
   - tuple namespace constants for `"modules"` / `"resolve"` to avoid string duplication.

6. Keep task #1 isolated.
   Do not wire engine loading or CLI mutation in this step.
   Deliverable for #1 is parse/serialize + workspace wrappers + unit tests only.

- [ ] **#2: Thread lock policy into module loading (`engine/server/session.go`)**  
  Extend `pendingModule` with `Pin` and `Policy`, lookup lock tuple by source during workspace gathering, pass `refPin` in `loadModule`, and gate behavior by lock mode.

- [ ] **#3: Update `workspace.install` lock write path (`core/schema/workspace.go`)**  
  Persist git resolutions via `"modules.resolve"` lock entries with `policy`; optional orphan pruning.

- [ ] **#4: Prune stale lock entries on config mutation (`core/schema/workspace.go`)**  
  Run optional orphan pruning after `configWrite`.

- [ ] **#5: Preserve legacy `pin` fields in compat mode (`core/workspace/legacy.go`, `engine/server/session.go`)**  
  Use parsed legacy `Pin` when loading toolchains/blueprints from `dagger.json`.

- [ ] **#6: Add workspace update API + CLI (`core/schema/workspace.go`, `cmd/dagger/workspace.go`)**  
  Implement `workspace.update` and wire `dagger workspace update`.

- [ ] **#7: Migration lock output (`core/workspace/migrate.go`)**  
  Emit lock entries when legacy `pin` fields exist.

- [ ] **#8: Tests (`core/integration/workspace_test.go` + unit tests)**  
  Cover lock read/write, `pin|float` policy semantics, lock mode behavior (`strict|auto|update`), stale entry pruning, update flow, compat pin behavior, and remote read behavior.

### Phase 2+ tasks (after v1 modules.resolve)

- [ ] Add lock mode plumbing (`--lock strict|auto|update`) and engine enforcement for non-workspace lookups.
- [ ] Add `--offline` execution flag plumbing (`strict`-compatible, network-disabled, local-content required).
- [ ] Extend schema hooks to write/read lock entries for `core.git.*`, `core.http.*`, and container registry lookups.

## Open questions

1. What are default policies when `pin` annotation is omitted for each lookup API (`git`, `http`, `container`, `module`)?
2. Should `dagger update` eventually dispatch between module-dependency update and workspace lock update based on context, or keep `workspace update` separate?
3. For v1 rollout, should we implement only `"modules.resolve"` writes first, while preserving other tuples for forward compatibility?
