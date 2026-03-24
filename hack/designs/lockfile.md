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

## Update Flows

There are three update paths:

### `dagger lock update`

Refresh entries already present in `.dagger/lock`.

Properties:

- best-effort by entry type
- uses the current environment's ambient authentication
- does not discover new entries on its own

### `--lock=live`

Run the real workload in live lock mode.

Properties:

- refreshes existing entries the run touches
- discovers missing entries the run touches
- is the authoritative discovery path for new lock entries

### `currentWorkspace.update(): Changeset!`

Engine API for refreshing entries already present in `.dagger/lock`.

Properties:

- returns a `Changeset` instead of writing directly
- currently refreshes supported existing entries only

## Lookup Coverage

Target model: one lock system for all lookup functions.

Current core operation keys:

| Operation | Inputs | Result |
| --- | --- | --- |
| `container.from` | `[imageRef, platform]` | image digest |
| `git.head` | `[remoteURL]` | commit SHA |
| `git.branch` | `[remoteURL, branchName]` | commit SHA |
| `git.tag` | `[remoteURL, tagName]` | commit SHA |
| `git.ref` | `[remoteURL, refName]` | commit SHA |

Notes:

- `git.commit` is already pinned by input and does not create lock entries
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
- [x] Git lookup locking for `head`, `branch`, `tag`, and mutable `ref`
- [x] `currentWorkspace.update(): Changeset!`
- [x] `dagger lock update`
- [x] execution-driven discovery via `--lock=live`
- [x] unit and integration coverage for substrate, CLI, container, Git, module, and nested execution

### Implemented Semantics

- [x] `--lock=disabled|live|pinned|frozen`
- [x] default lock mode is `disabled`
- [x] `live` writes through
- [x] `pinned` writes through for `float` and missing entries
- [x] `frozen` reuses both `pin` and `float` entries and fails on misses

### Current Consumer Defaults

- [x] `container.from` defaults to `pin`
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
- do not refactor the engine to create a second resolution hook whose only purpose is
  lockfile integration

## Remaining Work

### High-priority design/implementation gaps

- [ ] `modules.resolve` lock entries and workspace-backed module loading
- [ ] `http` lookup locking
- [ ] decide whether additional Git lookup operations such as `refs`, `symrefs`, or `isPublic` belong in the lock model
- [ ] remote-workspace read semantics, if any
- [ ] final initialized-workspace semantics for `.dagger/lock` anchoring

### UX and maintenance follow-ups

- [ ] decide whether `disabled` should remain the long-term default
- [ ] decide whether `dagger lock update` should gain richer output or selection flags
- [ ] decide whether lock update should prune stale entries

### Longer-term extensions

- [ ] full offline / airgapped design
- [ ] extension model for user-defined lookup functions
- [ ] broader conformance coverage as new lookup consumers are added

## Workspace Relationship

Lockfiles are not specific to Workspace, but Workspace makes them more important.

Why:

- Workspace config stores symbolic lookup inputs
- deterministic workspace loading eventually needs recorded lookup results
- `modules.resolve` is the clearest workspace-driven missing piece

So the intended long-term shape is:

- one lock model for core lookups
- one lock model for workspace-owned lookup state
- one maintenance interface for refreshing recorded results

## Reference Commands

```bash
dagger --lock=disabled call ...
dagger --lock=live call ...
dagger --lock=pinned call ...
dagger --lock=frozen call ...
dagger lock update
```
