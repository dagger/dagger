# Lockfile: Lookup Resolution

## Status: In Progress

Depends on: Workspace API (done)

## Workspace Relationship

The lockfile is read from the bound workspace's `.dagger/lock`. See
[workspace.md](./workspace.md) for binding rules.

Workspace makes lockfile support urgent because `.dagger/config.toml`
intentionally stores symbolic lookup inputs and does not store pinned lookup
results. That means `modules.resolve` lock entries are now a blocker for
deterministic Workspace loading.

This document defines:
- Target state: one lock model for all lookup functions (`modules`, `git`, `http`, `container`, and future extension points).
- Immediate implementation scope after workspace foundation: restore core lookup locking (`git`, `http`, `container`) following the same tuple model used by `modules.resolve`.

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
5. Core lookup paths (`git`, `http`, `container`) are not yet consistently locked in the current workspace-first implementation.

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
- Hard cutover: named argument tuple encodings (for example `[["source", "..."]]`) are invalid.

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

## Current Implementation vs Design Scope

| Dimension | Current implementation | Design scope |
| --- | --- | --- |
| Lookup coverage | `modules.resolve` | `modules.resolve` + core `git.*` + `http.*` + `container.*` + extension points |
| Lock policy model | `pin|float` | same |
| Lock modes | applied on workspace module lookup path | applied across all lookup paths |
| Offline | sketched only | fully specified and implemented |

## Core Lookup Coverage (Restored)

Core operation locking should be restored in this design scope (as in the earlier universal lockfile work), using `namespace=""` and ordered positional `inputs`.

| Operation key (core namespace) | Inputs (positional) | Result example |
| --- | --- | --- |
| `git.resolve` | `[remoteURL, ref]` | commit SHA |
| `git.head` | `[remoteURL]` | head ref name |
| `git.ref` | `[remoteURL, refName]` | commit SHA |
| `git.refs` | `[remoteURL]` | ordered ref name list |
| `git.symrefs` | `[remoteURL]` | ordered key/value tuple list |
| `git.isPublic` | `[remoteURL]` | boolean |
| `container.from` | `[imageRef, platform]` | image digest |
| `http.get` | `[url]` | content digest |

Notes:
- Final naming can follow current core schema conventions, but coverage above is required.
- Any key-value-like values in `inputs` must still use ordered tuple encoding (never JSON object).

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

## Error Model

- Missing lock file:
  - `strict`: error on first required remote lookup.
  - `auto|update`: allowed; entries may be created per mode/policy rules.
- Malformed lock file: hard error.
- Unknown lock entries: ignored.
- Invalid result envelope (`value`/`policy` missing or invalid): hard error.
- `pin` policy result unavailable at fetch time (and not locally cached): hard error.

## Tasks

### Completed baseline

- [x] Lock schema + parse/serialize wrappers (`util/lockfile`, `core/workspace/lock.go`)
- [x] Workspace module lock read path in engine loading (`modules.resolve`)
- [x] Workspace lock write paths (`install`, `configWrite` prune, `update`, `migrate`)
- [x] Compat-mode legacy `pin` passthrough for migrated loading paths
- [x] Initial workspace lock tests and integration coverage

### Current task list (v1 closure)

Dependency order:

```text
#9 Core lookup coverage
 -> #10 Uniform lock-mode semantics
 -> #11 All-client workspace/lock binding
 -> #12 Spawned-client grant policy alignment
 -> #13 Conformance + race coverage
```

- [ ] **#9: Complete core lookup lock coverage**
  - [x] shared schema lock helpers (`core/schema/lockfile.go`)
  - [ ] `git.resolve|head|ref|refs|symrefs|isPublic` lock hooks
  - [ ] `http.get` lock hook
  - [ ] `container.from` hook parity with final mode semantics

- [ ] **#10: Enforce uniform lock-mode semantics for all lookup hooks**
  Apply the same matrix used by this design across `git`, `http`, and `container`:
  - `strict`: require existing lock entry, use locked value, never mutate
  - `auto + pin`: require existing entry, use locked value, never mutate
  - `auto + float`: resolve live and write/update lock entry
  - `update`: resolve live and write/update lock entry

- [ ] **#11: Bind lockfile behavior to workspace binding for all clients**
  - [x] inherit workspace binding by default for module clients
  - [x] support explicit workspace rebind on connect (`workspace`)
  - [ ] ensure lookup lock state always comes from the same bound workspace as `currentWorkspace`

- [ ] **#12: Align spawned-client policy with workspace access model**
  Locking is engine-internal and not grant-gated:
  - grants are assigned by spawning APIs (`module runtime`, `withExec`, `asService`), not by child connect metadata
  - ambient workspace restrictions MUST NOT disable internal lookup lock behavior
  - explicit `Workspace` argument delegation remains valid independent of ambient grants

- [ ] **#13: Conformance and regression coverage for desired state**
  Add coverage for:
  - lock mode matrix (`strict|auto|update`) across `modules`, `git`, `http`, `container`
  - nested client classes (main client, direct module runtime, dependency runtime, `withExec`, `asService`)
  - workspace inheritance vs explicit rebind behavior
  - concurrent lock writes (no dropped updates / deterministic output)

### Phase 2+ tasks

- [ ] Full `--offline` behavior with dedicated design and implementation.
- [ ] Extension model for user-defined lookup functions.

## Open questions

1. Default policies when `pin` annotation is omitted for each lookup API (`git`, `http`, `container`, `module`).
2. Should policy flips (`pin` <-> `float`) require explicit user command even under `lock=update`?
