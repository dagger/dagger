# WSFS: Workspace Filesystem Mounts with Lazy JIT Copy

Status: Draft  
Scope: Prototype

## Problem

Today, running non-Dagger-aware tools in a container with workspace inputs requires one of two bad options:

1. Pre-upload a large repo to the engine before execution.
2. Build tool-specific "dependency detection" phases that guess which files to mount.

Both are costly and brittle. For example, the Docusaurus dogfood flow traces Node.js FS calls first, writes a config of accessed paths, then re-runs the real build. This is extra orchestration and only works for specific runtimes.

We want one execution pass, for arbitrary Linux tools, while preserving Dagger semantics (typed APIs, caching, tracing, reproducibility, orchestration).

## Solution

Add a new container mount type for `Workspace`:

```graphql
extend type Container {
  withMountedWorkspace(
    path: String!
    source: Workspace!
    owner: String = ""
    expand: Boolean = false
  ): Container!
}
```

`withMountedWorkspace` mounts a writable filesystem view backed by a WSFS FUSE daemon:

- Reads are fetched lazily from `Workspace` APIs.
- Writes are allowed and persist across `withExec` calls on the returned container lineage.
- Writes are ephemeral to Dagger state and are not synced back to the host workspace.

## Goals

- Eliminate pre-upload of full workspaces for common tool runs.
- Run tools once, without tool-specific dependency discovery phases.
- Preserve normal container mount ergonomics.
- Keep cache invalidation content-aware without hashing entire repositories.

## Non-Goals (Prototype)

- Bidirectional sync back to host workspace.
- Non-Linux support.
- Perfect POSIX fidelity for all inode/xattr/device edge cases.

## User-Facing Semantics

1. `withMountedWorkspace` behaves like existing writable mounts from the container user perspective.
2. Writes persist across subsequent `withExec` calls on the same container lineage.
3. Re-mounting at the same path replaces prior mount state at that path.
4. Branching containers keeps branch-local write history.
5. Workspace host updates are visible unless shadowed by prior writes in that lineage.
6. No writes are synced back to host workspace.

## Architecture

### Control Plane

1. User calls `container.withMountedWorkspace("/work", ws)`.
2. Container stores a `WorkspaceMountSource` in `ContainerMount`.
3. During `withExec`, engine prepares WSFS runtime config for each workspace mount.
4. WSFS daemon runs alongside the exec and mounts FUSE at target path.
5. WSFS records read dependency trace and materializes writes in an upper layer directory.

### Data Plane

WSFS uses overlay-like behavior:

- Upper layer: writable state persisted in Dagger mount state (per container lineage).
- Lower layer: lazy on-demand reads from `Workspace`.
- Deletes/renames represented in upper layer (whiteout/tombstone semantics internal to WSFS).

This gives writable behavior without mutating host workspace.

## FUSE Operation Mapping (Critical)

Target mapping to avoid over-upload:

| FS op | WSFS behavior | Workspace API usage |
| --- | --- | --- |
| `read` | Fetch file on first access, cache locally for handle lifetime | `Workspace.file(path)` |
| `readdir` | List immediate children only; do not recursively materialize descendants | Preferred: shallow listing API. Candidate fallback: `Workspace.directory(path, include:["*"], exclude:["*/*"])` if proven shallow in practice |
| `stat` | Return metadata without full subtree materialization | Preferred: dedicated `Workspace.stat(path)`; fallback is object lookup + stat from returned object |

### Prototype with Existing Workspace API (v0)

WSFS can start with the API we already have:

- `read` uses `Workspace.file(path)`.
- `readdir` uses `Workspace.directory(path, include:["*"], exclude:["*/*"]).entries`.
- `stat` uses `Workspace.file(path).stat` then fallback to `Workspace.directory(path).stat` (or equivalent existence/type checks).

This is enough for a functional prototype and validates the end-to-end architecture.

### Why This Needs Attention

`readdir` is where accidental large copies happen. If existing include/exclude behavior is not reliably shallow for large trees, we should extend `Workspace` with explicit shallow primitives:

```graphql
extend type Workspace {
  stat(path: String!, doNotFollowSymlinks: Boolean = false): Stat
  entries(path: String! = "."): [WorkspaceEntry!]!
}

type WorkspaceEntry {
  name: String!
  stat: Stat!
}
```

This keeps directory browsing metadata-only and avoids copying grandchildren just to list names.

## Caching (Critical, Visible)

Workspace-aware module function caching already has special behavior:

1. `Workspace` args are auto-injected when omitted.
2. Functions with `Workspace` args propagate content-sensitive digests in returned values.
3. Cache invalidation follows relevant workspace content, not whole-repo hashing.

`withMountedWorkspace` must transpose that model to container execs.

### Prototype Cache Policy (v0)

For the first implementation, keep caching conservative:

1. If a container has any workspace mount, `withExec` is `CachePerCall`.
2. If no workspace mount is present, keep the current `withExec` cache key behavior.

This avoids stale cache hits while WSFS read-trace digesting is not implemented yet.

### Important Separate Track: Workspace Injection Caching

Today `currentWorkspace` is `CachePerCall`, which makes workspace-receiving calls always re-run.  
Remote git workspaces should be improved separately so they can use stable content-derived cache identity instead of per-call randomization.

This is orthogonal to WSFS: WSFS v0 can ship with conservative exec invalidation, while workspace injection caching evolves in parallel.

### Future WSFS Cache Model (v1)

After v0, add dynamic content-aware caching:

1. Record per-exec, per-mount read traces (`read`, `readdir`, `stat`).
2. Resolve trace to a workspace content/metadata digest.
3. Feed that digest into mount dependency digesting for `ContainerDagOp`.

Effect:

- Unrelated workspace changes do not invalidate cached execs.
- Accessed file/metadata changes do invalidate.
- No up-front full repository hash/upload.

## Security and Isolation

- Path resolution remains sandboxed to workspace root.
- WSFS must reject `..` escapes and normalize paths exactly once.
- Client ownership model follows existing workspace client binding behavior.
- No new host write capability is introduced.

## Implementation Sketch

## API and Core Types

- Add `container.withMountedWorkspace` in `core/schema/container.go`.
- Extend `core.ContainerMount` with `WorkspaceSource`.
- Add `WorkspaceMountSource` with:
  - workspace ID/source
  - persisted upper-layer directory state
  - dependency trace
  - computed digest

## Exec Wiring

- Extend container exec preparation to initialize WSFS mounts.
- Start WSFS daemon before process start; unmount/flush on exit.
- Persist upper-layer directory and trace back into updated container mount state.

## Caching Wiring

- v0: in `withExecCacheKey`, if any workspace mount is present, force `CachePerCall`.
- v1: extend mount digest derivation to include WSFS trace digest when present.

## Observability

- Add spans/counters per mount:
  - files fetched
  - bytes fetched
  - directory lists
  - stat calls
  - cache hits/misses

## Test Plan (Prototype)

1. Read laziness: reading one file does not upload siblings.
2. Readdir laziness: listing large dir does not upload descendants.
3. Write persistence: write in exec A is visible in exec B in same lineage.
4. No host sync-back: host workspace unchanged after writes.
5. Cache behavior: unrelated workspace edits keep cache hit; related edits invalidate.
6. Path traversal safety for read/stat/list and write paths.

## Future Evolution

- Two-way sync from WSFS writes back to workspace.
- Explicit conflict policy between host updates and upper-layer shadowed paths.
- Richer metadata APIs if needed (`lstat`, xattrs, chmod/chown semantics).
