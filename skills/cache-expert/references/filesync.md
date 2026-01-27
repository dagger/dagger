# Filesync (Host <-> Engine File Sync)

Filesync is the mechanism that transfers host files between the Dagger client and the engine. It underpins `host.directory`/`host.file` imports and local exports. The design combines:
- A gRPC streaming protocol (implemented in `internal/fsutil` and `engine/filesync`).
- A server-side snapshotter (`engine/filesync`) that syncs into a per-client mutable cache ref and produces immutable snapshots. The mutable cache ref acts as a lazily filled in mirror of the client's filesystem, updating as files requested to be loaded are added, modified, or deleted. Immutable snapshots are copied from this mutable mirror.
- dagql cache behavior that controls how host filesystem access is memoized.

Important design aspects:
- We fully control the client and server implementations, the code of which is in this repo. The client does not need to handle alternative server implementations. The server does not need to handle alternative client implementations.
- Performance is important. Minimizing round-trips from client->server and otherwise minimizing the latency of a filesync operation is a key goal.

This doc focuses on how the protocol works, how the engine consumes it, and how it connects to caching.

## Key Components

### Client-side (engine/client/filesync.go)
The client exposes two gRPC services during a session:

- **FileSync (source)**: streams local files to the engine.
  - Entry point: `FilesyncSource.DiffCopy`.
  - Reads `engine.LocalImportOpts` from context.
  - Handles special modes:
    - `GetAbsPathOnly`: return a single `Stat` with the absolute path.
    - `StatPathOnly`: return the `Stat` for the root path (with optional abs path).
    - `ReadSingleFileOnly`: return file contents as a single `BytesMessage` (bounded by `MaxFileSize`).
    - Default: full directory sync using `fsutil.Send`.
  - Uses `fsutil.NewFS` + `fsutil.NewFilterFS` for include/exclude/follow-path filtering.
  - When streaming, resets `Uid/Gid` to 0 on outgoing stats (see `FilterOpt.Map`).
  - Optional `.gitignore` *marking* via `fsxutil.NewGitIgnoreMarkedFS` (stats are flagged with `GitIgnored`, but the walk is not filtered).

- **FileSend (target)**: receives files from the engine to the host.
  - Entry point: `FilesyncTarget.DiffCopy`.
  - Reads `engine.LocalExportOpts` from context.
  - Removes `RemovePaths` before sync when requested.
  - If `IsFileStream` is false, uses `fsutil.Receive` to apply a full directory tree.
  - If `IsFileStream` is true, receives `BytesMessage` chunks and writes a single file (or tarball).
  - Honors `AllowParentDirPath`, `FileOriginalName`, and `FileMode` to resolve output path.

There are also proxy wrappers (`FilesyncSourceProxy`, `FilesyncTargetProxy`) that simply forward the gRPC streams.

### Protocol (internal/fsutil)
The filesync wire protocol is implemented in `internal/fsutil/send.go` and `internal/fsutil/receive.go`.

Packets (see `internal/fsutil/types/stat.proto`):
- `PACKET_STAT`: file metadata; an empty stat marks end-of-list.
- `PACKET_REQ`: receiver requests file contents by ID.
- `PACKET_DATA`: file content chunks; empty data marks end-of-file.
- `PACKET_FIN`: transfer complete.
- `PACKET_ERR`: error payload.

Behavior:
- Sender walks the filesystem in lexicographic order and emits `STAT` packets.
- Receiver decides which files need data and requests them by index (`ID` in STAT stream).
- Paths are sent over the wire as unix-style (`/`) and normalized back to platform-specific paths on receive.
- Cross-platform transfers are best-effort: unrepresentable paths and non-portable metadata are rejected or dropped.
- `STAT` includes a `git_ignored` flag (Go: `Stat.GitIgnored`) so the receiver can treat ignored paths as “present but ignored.”

`fsutil.NewFilterFS` applies include/exclude rules (plus `FollowPaths`) on top of an `FS` snapshot. The map callback allows stat mutation (e.g., zeroing uid/gid on send).

## Server-side (engine/filesync)

### Snapshot entry point
`FileSyncer.Snapshot` drives import of host directories into the engine:
1. Reads `ClientMetadata` to identify the session and client.
2. Performs a **stat-only** filesync call to the client (`LocalImportOpts{StatPathOnly, StatReturnAbsPath, StatResolvePath}`) to resolve absolute paths and symlinks.
3. Gets (or creates) a **per-client mutable ref** via `getRef`:
   - Keyed by `ClientStableID` (plus drive letter on Windows).
   - Stored in cache ref with `CachePolicyRetain` and shared-key metadata.
   - Mounted locally to provide a mutable root (`filesyncCacheRef.mntPath`).
4. Calls `syncParentDirs` to ensure parent directories are synced.
5. Calls `sync` on the requested path to produce an **immutable snapshot ref**.

### remoteFS (engine/filesync/remotefs.go)
`remoteFS` implements `ReadFS` over the filesync protocol:
- `Walk` starts a single `DiffCopy` stream to the client and consumes `STAT` packets.
- Regular files get `io.Pipe` readers so `ReadFile` can stream content on demand.
- Regular files marked `GitIgnored` do not get a pipe, so content is never requested.
- File content is requested by sending `PACKET_REQ` with the file ID.

### localFS (engine/filesync/localfs.go)
`localFS` represents the engine-side cached view of a client's filesystem and applies a diff against `remoteFS`.

Core mechanics:
- **Filtering**: uses `fsutil.NewFilterFS` for include/exclude views of the cached ref (gitignore is handled via remote `GitIgnored` stats).
- **Diff**: `doubleWalkDiff` (from `engine/filesync/diff.go`) walks local and remote in parallel.
  - Regular files are compared using size + mtime as a proxy for content.
- **Apply changes**:
  - Directories: `Mkdir`
  - Symlinks: `Symlink`
  - Hardlinks: deferred until after all file writes
  - Regular files: `WriteFile` reads from `remoteFS.ReadFile` and writes to the local ref
  - Deletes: `RemoveAll`

### GitIgnored propagation
When `GitIgnore` is enabled for a snapshot:
- The client still walks the tree but marks gitignored paths in `Stat.GitIgnored` instead of filtering them out.
- Ignored directories are still *stat’d* and still walked to preserve negation semantics (e.g., `!dir/keep.txt`).
- The server treats `GitIgnored` paths as “present but ignored”: it skips applying updates and suppresses deletes under those prefixes, keeping the shared mirror stable across clients.

### Consistency + conflict detection
`localFS` uses an in-memory cache (`engine/cache.Cache`) keyed by path to dedupe concurrent updates and detect mid-sync changes:
- `changeCache` stores `ChangeWithStat` results for each path.
- `verifyExpectedChange` compares applied changes with expected stats (mode/uid/gid/size/devs/linkname/modtime).
- If the client filesystem changes during the sync, a conflict error is raised (`ErrConflict`).
- This handles concurrent syncs across multiple clients since they all share the same mutable ref.
- When a sync is finished, the cache ref for the change applied is released. When all refs are released, the underlying file/dir is available for new changes to be applied again.

### Content hashing and reuse
`localFS.Sync` builds a content hash for the snapshot:
- Per-path file content hashes are stored in xattrs (`user.daggerContentHash`) to avoid re-hashing unchanged files.
- A `CacheContext` tracks changes and computes the final content hash for the synced subtree.
- If an identical content hash already exists, it reuses an existing immutable ref (`contenthash.SearchContentHash`).
- Otherwise it copies only changed paths into a new mutable ref, commits, and sets content hash metadata.

## API Glue: `host.directory` / `host.file`

### `host.directory`
- Implemented in `core/schema/host.go` as a `NodeFuncWithCacheKey` using `DagOpDirectoryWrapper(..., WithHashContentDir)`.
- Caching behavior is **as requested** via `HostDirCacheConfig`:
  - Default: `CachePerClient`.
  - `noCache: true`: `CachePerCall` (forces re-evaluation).
- Host path handling:
  - Resolves absolute path via `buildkit.AbsPath`.
  - Optional gitignore root discovery via `Host.FindUp` (searches for `.git`).
  - Include/exclude patterns are re-rooted relative to the chosen git root.
- Ultimately calls `Host.Directory` (core/host.go), which invokes `FileSyncer.Snapshot`.
- `WithHashContentDir` converts the directory ID digest to a **content hash**, enabling dagql cache dedupe across identical content.

### `host.file`
- Implemented as a selection pipeline that calls `host.directory` with an `include` of the filename, then selects `file`.
- Inherits the same cache behavior as `host.directory` (`noCache` -> `CachePerCall`).

## Export Path (engine -> client)
File exports use the **FileSend** service on the client:
- Directory export: engine streams `fsutil.Receive` packets; `Merge` controls overwrite vs merge semantics.
- File or tar export: engine sends `BytesMessage` chunks; client writes to a target file.
- `RemovePaths` in `LocalExportOpts` is applied before writing to support delete semantics.

## Behavior and Gotchas

- **Path normalization**: wire paths are always unix-style; conversion happens at send/receive boundaries.
- **Windows drives**: `FileSyncer` splits drive letters and includes them in per-client cache keys.
- **Devices and named pipes**: skipped during sync (server-side warning).
- **Parent dir modtimes**: not reset to match client (documented in `localFS.Sync`).
- **CacheBuster**: `SnapshotOpts.CacheBuster` is set when `noCache` is requested, but is currently unused in `engine/filesync`.
- **Gitignored paths**: ignored entries are still represented in stats; directories may still be walked to preserve negation semantics, while the server skips updates/deletes for those paths to avoid cross-client conflicts.

## Code Map

- Client gRPC handlers: `engine/client/filesync.go`
- Filesync protocol: `internal/fsutil/send.go`, `internal/fsutil/receive.go`, `internal/fsutil/types/stat.proto`
- Server sync logic: `engine/filesync/filesyncer.go`, `engine/filesync/localfs.go`, `engine/filesync/remotefs.go`, `engine/filesync/diff.go`
- API glue: `core/schema/host.go`, `core/host.go`
- Content-hash ID conversion: `core/contenthash.go` (via `WithHashContentDir`)
- Gitignore helpers: `util/fsxutil/gitignore_mark_fs.go`, `util/fsxutil/gitignore_matcher.go`
