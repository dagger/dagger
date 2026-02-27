# Lockfile: Lookup Resolution

## Status: Draft

Builds on:
- [Part 1: Workspaces and Modules](https://gist.github.com/shykes/e4778dc5ec17c9a8bbd3120f5c21ce73)
- [Part 2: Workspace API](https://gist.github.com/shykes/86c05de3921675944087cb0849e1a3be)

## Scope and Workspace Relationship

The lockfile design predates Workspace and is orthogonal to it.

Workspace makes lockfile support urgent because `.dagger/config.toml` intentionally stores symbolic lookup inputs and does not store pinned lookup results. That means `modules.resolve` lock entries are now a blocker for deterministic Workspace loading.

This document defines:
- Target state: one lock model for all lookup functions (`modules`, `git`, `http`, `container`, and future extension points).
- V1 delivery scope: `modules.resolve` first, with the same model and terminology used for future rollout.

## Terminology

| Term | Meaning |
| --- | --- |
| Lookup function | A function that turns symbolic lookup inputs into a concrete lookup result. |
| Lookup inputs | The symbolic arguments to a lookup function (branch, tag, URL, image tag, module ref). |
| Lookup result | The concrete resolved value (commit, digest, immutable identifier, etc.). |
| Lock entry | A recorded mapping from `(namespace, operation, inputs)` to `{value, policy}`. |
| Policy | Update intent for a lock entry: `pin` (manual update) or `float` (auto-update allowed). |
| Lock mode | Run-level behavior for lock mutation: `strict`, `auto`, or `update`. |
| Offline flag | `--offline`; separate from lock mode, disables network and requires local resolution/content. |

Notes:
- Using `lookup` terminology maps directly to Dagger API calls.
- This also leaves room for future user-defined lookup functions (for example via a `+lookup` annotation) without changing the lock model.

## Problem

1. Symbolic lookup inputs can drift over time.
2. Workspace currently lacks `.dagger/lock` read/write behavior for `modules.resolve`.
3. There is no single policy model for manual-only vs auto-updatable lookup entries.
4. Airgapped execution requires explicit lock semantics plus clear failure behavior.

## Proposal

Add a machine-managed `.dagger/lock` file that records lookup results and their policy.

- Every lookup entry is recorded in the same tuple format.
- Policy is explicit per entry: `pin` or `float`.
- Lock mode controls when entries may be discovered or updated.
- `--offline` is a separate flag that composes with lock mode.

## Normative Engine Behavior

For each lookup call:

1. Build lock key from `(namespace, operation, inputs)`.
2. Determine requested policy from API annotation (or API default).
3. Read current lock mode.
4. If an entry exists:
   - In `strict`: use entry; do not mutate.
   - In `auto`: use policy-specific behavior table below.
   - In `update`: refresh and rewrite.
5. If no entry exists: apply missing-entry behavior table below.
6. If requested policy differs from stored policy: treat as a lock mutation and require an update-capable flow (`lock=update` or explicit update command).

## Lock Schema

Use PR #11156 tuple format (JSON lines), with a structured result envelope:

```json
[["version","1"]]
["","modules.resolve",["github.com/dagger/go-toolchain@v1.0"],{"value":"3d23f8ef4f4f8f95e8f4c10a6f2b8b7d1646e001","policy":"pin"}]
["","git.resolveRef",["https://github.com/acme/ci","refs/heads/main"],{"value":"6e4d4d1f5d8e7d8f1a8f74264f63e1f9c5f2bb0c","policy":"float"}]
["github.com/acme/release","lookupVersion",["stable"],{"value":"v1.2.3","policy":"float"}]
```

Tuple shape:
1. `namespace` (string)
2. `operation` (string)
3. `inputs` (ordered JSON array)
4. `result` (JSON object)

Ordering invariant (normative):
- ALL STRUCTURES IN LOCKFILE MUST BE ORDERED.
- Lock entry ordering is deterministic by `(namespace, operation, inputs-json)`.
- `inputs` must always be encoded as ordered arrays of positional argument values.
- Argument names are implicit by function signature and must not be serialized.
- Unordered object/map/dict encodings are forbidden in serialized lock key inputs.

Result envelope:
- `value`: concrete lookup result.
- `policy`: `pin` or `float`.

### Naming Proposal

Canonical key naming:
- `namespace`:
  - `""` for core lookup functions.
  - `"<module-address>"` for lookup functions defined by modules.
- `operation`:
  - `functionName` or `Type.functionName`.
  - In modules, if `Type` is the module main type, type name MUST be omitted.
  - In core, operation naming is implementation-defined and may use:
    - `functionName`
    - `Type.functionName`
    - virtual type/group prefixes such as `git.resolveRef`.

Rationale:
- One core namespace is sufficient and consistent with treating core as a special module.
- Module namespace naturally maps to module ownership.
- Main-type omission rule prevents duplicate entries for the same logical lookup.

Collision guidance:
- Core operations should include subsystem-style prefixes (for example `git.*`, `http.*`, `container.*`, `modules.*`) to avoid name collisions inside `namespace=""`.
- Stateful typed lookups should include a stable receiver fingerprint in `inputs` when object state affects lookup result.

## API Annotation

Lookup APIs expose a single annotation:
- `pin: true` => policy `pin`
- `pin: false` => policy `float`

No third intent in v1.

If omitted, each lookup API defines a default policy. Explicit annotation overrides the default.

## Lock Modes

### Entry exists behavior

| Mode | `policy=pin` | `policy=float` |
| --- | --- | --- |
| `strict` | Use lock result, no update | Use lock result, no update |
| `auto` | Use lock result, no update | Resolve live and update lock entry |
| `update` | Resolve live and update lock entry | Resolve live and update lock entry |

### Missing entry behavior

| Mode | Requested policy | Behavior |
| --- | --- | --- |
| `strict` | `pin` or `float` | Error |
| `auto` | `pin` | Error |
| `auto` | `float` | Resolve live and create lock entry |
| `update` | `pin` or `float` | Resolve live and create lock entry |

## Offline Flag (`--offline`)

`--offline` is separate from lock mode and is only sketched here. Full offline design should be specified in a dedicated follow-up doc.

Current constraints:
- disables remote lookup/fetch.
- requires lookup results to come from lock state.
- requires content bytes to exist in local cache/store.
- `--offline` with `lock=update` is invalid.

## V1 Rollout vs Target State

| Dimension | V1 | Target state |
| --- | --- | --- |
| Lookup coverage | `modules.resolve` | modules + git + http + container + extension points |
| Lock policy model | `pin|float` | same |
| Lock modes | apply to workspace lookup path first | apply across all lookup paths |
| Offline | sketched only | fully specified and implemented |

## Workspace-Specific Behavior (V1)

### File location

`.dagger/lock` at workspace root (sibling to `config.toml`).

### Read path

1. `workspace.Detect()` reads `.dagger/config.toml` and `.dagger/lock`.
2. `detectAndLoadWorkspaceWithRootfs()` maps config modules to `pendingModule`.
3. For each module source:
   - read lock entry `("", "modules.resolve", [source])`.
   - apply mode/policy behavior.
4. `loadModule()` passes `refPin` when a locked lookup result exists.

### Write path

#### `workspace.install`

- Resolve module source.
- If source is git, write/update:
  - `["","modules.resolve",[source],{"value":<commit>,"policy":...}]`
- If source is local, no lock entry.
- Optional prune (v1.1): remove orphaned `modules.resolve` entries not present in config sources.

#### `workspace.moduleInit`

- Installs local module.
- No lock entry.

#### `workspace.configWrite`

- Keep generic key writing.
- Optional orphan prune after write.
- No implicit live lookup resolution here.

### Workspace update UX

`dagger update [MODULE...]`:
- No args: update all workspace git module lock entries.
- Args: update selected modules.
- Leaves `config.toml` unchanged.
- Rewrites corresponding lock entries.
- Output per change: `<name> <oldPin> -> <newPin>`.

`dagger module update [DEPENDENCY...]` remains the module-dependency update command.

### CLI command mapping

| Command | Scope |
| --- | --- |
| `dagger install <module>` | Install a module in the current workspace (`workspace.install`). |
| `dagger module install <module>` | Install a dependency in the current module (`dagger.json`). |
| `dagger update [MODULE...]` | Update workspace git module lock entries (`workspace.update`). |
| `dagger module update [DEPENDENCY...]` | Update dependencies in the current module (`dagger.json`). |

### Compat and migration

- Compat-mode toolchains/blueprints in legacy `dagger.json` carry forward their `pin` into lookup result usage.
- `dagger migrate` writes lock entries with `policy="pin"` when legacy `pin` fields exist.

### Remote workspace behavior

- Remote workspace (`-C <git-ref>`) reads and applies lock entries.
- Mutating commands remain disallowed for remote workdirs.

## Error Model

- Missing lock file:
  - `strict`: error on first required remote lookup.
  - `auto|update`: allowed; entries may be created per mode/policy rules.
- Malformed lock file: hard error.
- Unknown lock entries: ignored.
- Invalid result envelope (`value`/`policy` missing or invalid): hard error.
- `pin` policy result unavailable at fetch time (and not locally cached): hard error.

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

- [x] **#1: Add lock schema in `core/workspace`**  
  Port/adapt `util/lockfile` from PR #11156 and add workspace helper wrappers for `"modules.resolve"`.

#### #1 Detailed Checklist (file-level)

1. Add generic tuple lockfile package.
   File: `util/lockfile/lockfile.go`
   Scope: port deterministic tuple format from PR #11156, but expose byte-oriented APIs for workspace integration.
   API:
   - `func Parse(data []byte) (*Lockfile, error)`
   - `func (l *Lockfile) Marshal() ([]byte, error)`
   - `func (l *Lockfile) Get(namespace, operation string, inputs []any) (any, bool)`
   - `func (l *Lockfile) Set(namespace, operation string, inputs []any, result any) error`
   - `func (l *Lockfile) Delete(namespace, operation string, inputs []any) bool`
   Behavior:
   - Require version header `[["version","1"]]` on non-empty files.
   - Preserve unknown entries (do not drop tuples outside workspace usage).
   - Stable output ordering by `(namespace, operation, inputs-json)`.
   - Reject object/map/dict lock key inputs; key material must be positional ordered values only.
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
   - maps to tuple key `("", "modules.resolve", [source])`.

4. Add workspace lock wrapper tests.
   File: `core/workspace/lock_test.go`
   Cases:
   - source -> pin+policy lookup/set/delete.
   - policy validation (`pin`/`float` only).
   - prune behavior against `config.toml` source set.
   - unknown tuple preservation (`""/"git.*` operations, module namespaces, etc.).
   - marshal determinism.

5. Add workspace constants.
   Files: `core/workspace/detect.go` (constants block) and `core/workspace/lock.go`
   Constants:
   - `LockFileName = "lock"`
   - `PolicyPin = "pin"` and `PolicyFloat = "float"`
   - tuple constants for `"modules"` / `"resolve"` to avoid string duplication.

6. Keep task #1 isolated.
   Do not wire engine loading or CLI mutation in this step.
   Deliverable for #1 is parse/serialize + workspace wrappers + unit tests only.

- [x] **#2: Thread lock policy into module loading (`engine/server/session.go`)**  
  Extend `pendingModule` with `Pin` and `Policy`, lookup lock tuple by source during workspace gathering, pass `refPin` in `loadModule`, and gate behavior by lock mode.

- [x] **#3: Update `workspace.install` lock write path (`core/schema/workspace.go`)**  
  Persist git lookup results via `"modules.resolve"` lock entries with `policy`; optional orphan pruning.

- [x] **#4: Prune stale lock entries on config mutation (`core/schema/workspace.go`)**  
  Run optional orphan pruning after `configWrite`.

- [x] **#5: Preserve legacy `pin` fields in compat mode (`core/workspace/legacy.go`, `engine/server/session.go`)**  
  Use parsed legacy `Pin` when loading toolchains/blueprints from `dagger.json`.

- [x] **#6: Add workspace update API + CLI (`core/schema/workspace.go`, `cmd/dagger/workspace.go`)**  
  Implement `workspace.update` and wire top-level `dagger update` for workspace lock updates.

- [x] **#7: Migration lock output (`core/workspace/migrate.go`)**  
  Emit lock entries when legacy `pin` fields exist.

- [x] **#8: Tests (`core/integration/workspace_test.go` + unit tests)**  
  Cover lock read/write, `pin|float` policy semantics, lock mode behavior (`strict|auto|update`), stale entry pruning, update flow, compat pin behavior, and remote read behavior.

### Phase 2+ tasks (after v1 modules.resolve)

- [ ] Apply lock mode enforcement across non-workspace lookup paths.
- [ ] Add full `--offline` behavior with dedicated design and implementation.
- [ ] Add lock read/write hooks for core operations (`git.*`, `http.*`, `container.*`) under `namespace=""`.
- [ ] Design and implement extension model for user-defined lookup functions.

## Open questions

1. Default policies when `pin` annotation is omitted for each lookup API (`git`, `http`, `container`, `module`).
2. Should policy flips (`pin` <-> `float`) require explicit user command even under `lock=update`?
