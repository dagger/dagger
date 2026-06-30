# Service Directory Mount Snapshots

## Problem

Long-running daemon services sometimes need to process a large `Directory`
snapshot and return a modified `Directory` to the calling function. Today the
practical workaround is to copy the input into a `CacheVolume`, let the daemon
mutate the cache, then copy or diff the cache back into a normal `Directory`.
That works, but it is too expensive when the directory is large.

The daemon should stay stable across calls. Mounting each input with
`Container.withMountedDirectory(...).asService()` is not enough because the
input directory becomes part of the service identity and starts a different
service for each input.

## Existing Mechanics

`Container.withExec` already has the main primitive. Writable mounted
directories are backed by mutable snapshot refs during the exec; after the exec
finishes, Dagger commits the mutable ref and writes the resulting immutable
`Directory` back into the returned `Container`.

Services already mount mutable refs too, but the refs are only protected and
released for service lifetime. There is no public API to commit a service mount
back into a `Directory`.

`Service.runAndSnapshotChanges` proves that a running service can be remounted
with a mutable copy of a `Directory`, allowed to process it, and then snapshotted
back into an immutable `Directory`. It is private, MCP-specific, and takes a Go
callback, so it is not directly usable from SDKs.

## Solution

Expose a runtime directory mount handle for running services. The handle owns a
mutable snapshot ref mounted into the service, while callers only receive normal
immutable `Directory` and `Changeset` values.

```graphql
"""
A directory mounted into a running service.
"""
type ServiceDirectoryMount {
  """Path where this directory is mounted in the service container."""
  path: String!

  """The service this mount belongs to."""
  service: Service!

  """Force the runtime mount side effect."""
  sync: ServiceDirectoryMount!

  """
  Snapshot the current mount contents as an immutable Directory.

  By default, Dagger detaches the old mutable mount and remounts a fresh mutable
  copy of the snapshot so the service can keep using the same path.
  """
  directory(keepMounted: Boolean = true): Directory!

  """
  Return changes from the original source, or from an explicit base Directory.
  """
  changes(from: Directory): Changeset!

  """Detach the mount from the service and release its mutable backing ref."""
  unmount: Service!
}

type Service {
  """
  Mount a Directory snapshot into this running service and return a handle that
  can snapshot changes made through that mount.

  The service is started if it is not already running. The mount is exclusive by
  path: another mount at the same path fails until this mount is snapshotted with
  keepMounted=false or unmounted.
  """
  mountDirectory(path: String!, source: Directory!, expand: Boolean = false): ServiceDirectoryMount!
}
```

Example:

```typescript
const daemon = dag.container()
  .from("my-daemon")
  .withWorkdir("/workspace")
  .withExposedPort(7777)
  .asService()

const mount = daemon.mountDirectory("/workspace", input)
await mount.sync()

await callDaemon(await daemon.endpoint({ port: 7777, scheme: "http" }))

return mount.directory()
```

## Snapshot Semantics

The returned `Directory` must be immutable even if the daemon keeps running.
That means snapshotting cannot leave the mutable ref being committed mounted at
the service path.

The default `directory()` behavior should be:

1. Lock the service mount handle so only one snapshot/remount operation happens
   at a time.
2. Detach the current mount from the service path.
3. Commit the detached mutable ref into an immutable ref.
4. Return that immutable ref as a normal `Directory`.
5. If `keepMounted` is true, create a new mutable ref from the immutable snapshot
   and mount it back at the same service path.
6. If `keepMounted` is false, release the mutable ref and mark the handle
   closed.

This closes the most important hole in the current private helper: future
path-based writes by the service cannot mutate the mutable ref that was just
committed. The default is a fresh remount rather than a permanent unmount
because many daemons use the mounted path as their working directory.

There is still an application-level quiescence requirement. If the daemon is
writing while Dagger detaches and commits, or if it holds open writable file
descriptors to the old mount, the boundary is inherently racy. Dagger can make
the snapshot/ref lifecycle safe, but it cannot know when the daemon has flushed
its own work. Callers should use a daemon protocol such as "finish request,
fsync/close files, then acknowledge" before calling `directory()`.

For stronger guarantees later, add an option that stops or pauses the service
before snapshotting. That is heavier and should not be the default.

## Implementation Plan

1. Add `ServiceDirectoryMount` core and schema types, gated as new public API.
2. Add runtime mount state to `RunningService`: mount ID, target path, current
   mutable ref, source selector, platform, services, and an exclusive lock.
3. Implement `Service.mountDirectory` by starting or reusing the service,
   creating a mutable ref from the source directory snapshot, mounting it with
   the existing mount-namespace remount helper, and tracking the ref on the
   running service lease.
4. Implement `ServiceDirectoryMount.directory` as the detach, commit, and
   optional fresh-remount operation described above.
5. Implement `ServiceDirectoryMount.changes` by snapshotting the mount and
   comparing it with the original or explicit base directory.
6. Refactor `Service.runAndSnapshotChanges` to use the same runtime mount
   primitive instead of keeping a separate MCP-only implementation.
7. Add integration tests for stable daemon reuse, two different input
   directories, repeated snapshots, immutable earlier snapshots, unmount
   behavior, stopped service errors, and same-path mount conflicts.

## Why This Shape

The service stays stable because per-call directory data is not part of the
service identity.

The data path stays efficient because Dagger passes snapshot refs and commits
mutable refs instead of copying a large tree through a cache volume.

The public result stays idiomatic because callers receive immutable `Directory`
and `Changeset` values.

The runtime behavior is explicit. Mounting and snapshotting a running service is
imperative, so the schema should mark these fields uncached and document the
quiescence boundary.

## Status

Implemented in `core.ServiceDirectoryMount` and `Service.mountDirectory`.

The final `directory(keepMounted: false)` snapshot closes the live mount and
removes it from the running service's active mount table. Re-evaluating that
same closed snapshot returns the pinned immutable `Directory`; callers must use a
fresh `mountDirectory` call for further service writes at that path.
