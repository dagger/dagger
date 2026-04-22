# Filesync (Host <-> Engine File Sync)

Filesync transfers host files between client and engine. It powers `host.directory` / `host.file` imports and local exports.

This doc focuses on protocol flow, engine sync behavior, and cache interactions relevant to correctness/performance.

## Key Components

### Client side (`engine/client/filesync.go`)

Client exposes two gRPC services per session:

1. `FileSync` (source)
- streams host filesystem stats/data to engine
- supports stat-only and single-file fast paths
- can mark gitignored entries in stats

2. `FileSend` (target)
- receives filesystem/file streams from engine for exports

### Protocol (`internal/fsutil`)

Wire protocol uses packetized stat/data/request frames:
- sender walks filesystem and emits stats
- receiver requests file contents by stat index when needed
- paths are normalized for cross-platform transfer semantics

## Server Side (`engine/filesync`)

### Snapshot entry (`FileSyncer.Snapshot`)

High-level flow:
1. resolve client metadata and absolute input path via stat-only filesync
2. get/create per-client mutable ref mirror (`getRef`)
3. sync parent dirs when needed
4. sync requested subtree into mirror and produce immutable snapshot

### `remoteFS` (`engine/filesync/remotefs.go`)

`remoteFS` reads from client stream:
- exposes `Walk` and `ReadFile`
- lazily requests file data
- skips content requests for gitignored regular files

### `localFS` (`engine/filesync/localfs.go`)

`localFS` applies diff from remote stream into per-client mirror:
- compares remote and local view
- applies mkdir/symlink/hardlink/write/delete changes
- computes content hash for resulting subtree
- reuses existing immutable ref when content hash matches

## Filesync Cache Model (Current)

Filesync now uses a dedicated in-package typed cache:
- `engine/filesync/change_cache.go`

This cache is intentionally narrow:
- key: local path (string)
- value: `*ChangeWithStat`
- behavior: in-memory singleflight + refcount + release
- no TTL, no persistence, no content-key indexing, no generic adapters

## Why the Change Cache Exists

`localFS.Sync` uses change-cache entries to:
- dedupe equivalent concurrent mutations on same path
- detect mid-sync host mutations (conflict detection)
- avoid false conflicts after sync completes by releasing all held entries

This cache is shared across syncs for the same client mirror via `localFSSharedState`.

## Conflict Detection

Applied changes are validated against expected remote stats (`verifyExpectedChange`).

If a cached/applied change does not match expected change, sync fails with conflict instead of silently producing mixed-state snapshot.

## GitIgnore Behavior

With gitignore-enabled import:
- client marks entries as ignored in stats
- server treats ignored paths as present-but-ignored
- updates/deletes under ignored prefixes are skipped to keep mirror stable and avoid cross-sync interference

## Content Reuse

After sync operations:
- content hash is computed for subtree
- if matching immutable ref already exists, reuse it
- otherwise copy changed paths into new ref and commit

This is separate from dagql call cache; it is snapshot-content reuse at filesync layer.

## Export Path (Engine -> Client)

Exports use client `FileSend` service:
- tree exports via fsutil receive
- single-file/tar streams via chunked bytes

## Gotchas

- Parent directory modtimes are not fully normalized to client today.
- Device files and named pipes are skipped.
- Filesync change cache is not durable state; it is lifecycle-scoped dedupe/consistency machinery.

## Code Map

- Client handlers: `engine/client/filesync.go`
- Filesync protocol: `internal/fsutil/send.go`, `internal/fsutil/receive.go`
- Server sync logic: `engine/filesync/filesyncer.go`, `engine/filesync/localfs.go`, `engine/filesync/remotefs.go`
- Filesync change cache: `engine/filesync/change_cache.go`
- API glue: `core/schema/host.go`, `core/host.go`
