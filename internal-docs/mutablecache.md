# Mutable-Backed Cache Objects

This doc covers a small set of specialized cache-backed objects whose internal state is meaningfully mutable:

- `HTTPState`
- `RemoteGitMirror`
- `ClientFilesyncMirror`
- `CacheVolume`

These are all a little different, but they share one important theme:

- the engine sometimes needs mutable snapshot-backed state for performance or correctness
- we still model that state as dagql cache objects so lifecycle, retention, persistence, accounting, and pruning all stay inside the same big cache story

If these mutable snapshots lived somewhere ambiently outside the dagql cache model, they could consume disk space and mutate over time without the cache knowing they existed. By keeping them attached to real cache objects, we can:

- release them when their owning objects die
- persist and reload them when appropriate
- include them in cache usage accounting
- let pruning reason about them

## Shared Pattern

All four of these objects follow the same broad shape:

- the dagql object owns some snapshot-backed state
- that state is protected by a mutex because it can be reused and updated over time
- the object implements normal cache lifecycle hooks like `OnRelease`
- the object exposes snapshot usage/accounting through `PersistedSnapshotRefLinks`, `CacheUsageIdentities`, `CacheUsageSize`, and `CacheUsageMayChange`
- when persisted, the object stores just enough metadata plus a snapshot link to reopen the underlying snapshot later

The most important difference between them is what they expose to callers:

- some expose the mutable snapshot directly as the useful thing
- some use the mutable snapshot only as an internal backing store and still return fresh immutable snapshots to callers

## HTTP

`HTTPState` is the most controlled version of this pattern.

### What it owns

`HTTPState` stores:

- URL
- ETag
- Last-Modified
- content digest
- a canonical snapshot for the downloaded bytes

The snapshot is stored as an immutable ref plus a persisted snapshot ID for reload.

### How it is used

The public `Query.http` API has two paths:

1. If auth or service-host context is involved, it bypasses `HTTPState` and does a direct fetch with `FetchHTTPFile`.
2. Otherwise it routes through the internal persistent `_httpState` object and then `_resolve`.

So the mutable-backed state object is specifically the normal unauthenticated/no-service-host path.

### Update logic

`HTTPState.Resolve` does conditional requests:

- sends `If-None-Match` when it has an ETag
- otherwise sends `If-Modified-Since` when it has a Last-Modified value

Then:

- on `304 Not Modified`, it keeps the existing canonical snapshot and just reuses it
- on `200 OK`, it downloads into a new canonical snapshot and compares the resulting content digest

If the content digest changed, it replaces the stored snapshot. If the digest did not change, it releases the new download snapshot and keeps the existing one, while still updating validators like ETag and Last-Modified.

That is the mildly clever part: content identity, not just validator churn, decides whether the owned snapshot actually changes.

### Why it creates a new layer for the caller

The canonical internal snapshot always stores the downloaded payload at a fixed path, `contents`, with canonical permissions.

When a caller resolves it to a `File`, `HTTPState.fileResult`:

- creates a new mutable layer on top of the canonical snapshot
- renames `contents` to the requested filename
- applies the requested permissions
- commits that as a fresh immutable snapshot

So the internal mutable-ish state stays hidden in `HTTPState`, while callers still get ordinary immutable `File` results they can build on top of.

This is a good pattern to keep in mind: mutable internal backing state, immutable outward-facing result.

## Remote Git Mirror

`RemoteGitMirror` is the backing store for remote git fetches.

### What it owns

It owns a mutable snapshot containing a bare git repository for a specific remote URL.

That snapshot is:

- persistable
- reopened as a mutable ref on decode
- protected by a mutex

### How it is used

The schema creates or loads one through the internal `_remoteGitMirror` query field, and `RemoteGitRepository` stores it as a dependency.

When git needs to operate on the remote, it does not fetch directly into a throwaway checkout. Instead:

1. lock by remote URL
2. acquire the mirror's mutable ref
3. mount it locally
4. initialize a bare repo there if needed
5. fetch new refs into that bare repo
6. expire reflog

So the mirror itself is mutated in place over time as fetches happen.

### What callers actually get

Callers do not get the mutable bare mirror snapshot itself.

For a specific `RemoteGitRef.Tree(...)`, the flow is:

1. consult a separate immutable checkout snapshot cache keyed by the tree call identity
2. if absent, use the mutable bare mirror to ensure the needed refs are fetched
3. create a separate mutable checkout ref
4. check out the requested tree there
5. commit that checkout ref as an immutable snapshot
6. index that immutable snapshot by the tree cache key

So the mirror is mutable backing state for fetch acceleration and reuse, but the returned `Directory` snapshots are still immutable point-in-time checkouts.

That split is the key thing to understand.

### Adjacent but separate: remote metadata

Remote git also uses `GetOrInitArbitrary` to cache `ls-remote` metadata as plain JSON strings scoped by session plus auth configuration.

That is related operationally, but it is not the mutable-snapshot part. The mutable-snapshot part is specifically the bare mirror object.

## Client Filesync Mirror

`ClientFilesyncMirror` is the mutable backing store for host file imports.

### What it owns

It stores:

- stable client ID or ephemeral ID
- drive
- a mutable snapshot mirror
- lazily-mounted runtime state:
  - mounter
  - mounted path
  - `filesync.MirrorSharedState`
- a usage count for active sync users

The mutable snapshot is persistable for stable clients.

### Stable vs ephemeral mirrors

Host imports use two modes:

- if the client has a stable client ID, the engine loads a persistable `_clientFilesyncMirror` keyed by stable client ID plus drive
- otherwise it creates an ephemeral mirror object for that client

So for stable clients, the mutable mirror survives beyond a single import and can be reused across reconnects.

### Runtime mounting is separate from snapshot ownership

`EnsureCreated` only ensures the mutable snapshot exists.

The mounted runtime state is created lazily in `ensureRuntimeLocked` when a sync actually needs it. `acquire` reference-counts active users, and when the last user releases, the runtime mount is torn down again.

That means:

- the mutable snapshot can persist for a long time
- the mounted filesystem view of it is only held while syncs are actively using it

### What callers actually get

The mirror snapshot is not returned directly to the caller.

Instead, `ClientFilesyncMirror.Snapshot(...)` hands its `MirrorSharedState` to `engine/filesync.FileSyncer`, which:

- syncs the requested client subtree into the mirror
- uses the in-package change cache for dedupe/conflict detection during the sync
- produces an immutable snapshot plus content digest for the requested subtree

So again, the mutable object is backing state, while the outward-facing result is an ordinary immutable directory/file snapshot.

### Interaction with `noCache`

`host.directory` / `host.file` do not bypass the mirror for `noCache`.

Instead, they set a filesync cache-buster in `SnapshotOpts`. That forces a fresh snapshot result while still using the existing mutable mirror as the synchronization base.

## Cache Volumes

`CacheVolume` is the most direct mutable object in this group.

### What it owns

It stores:

- key
- namespace
- optional source directory ID
- sharing mode
- owner
- a mutable snapshot
- a selector within that snapshot

### Identity model

The subtle but important point here is that we do not try to have one underlying ambient mutable volume and then vary parameters like source/owner/sharing outside the cache identity.

Instead, those parameters all contribute to the cache object identity upstream:

- `cacheVolume(...)` includes them directly
- omitted namespace can be dynamically filled in
- `PRIVATE` sharing injects a nonce so it becomes unique

So different parameter combinations become different cache-volume objects with different owned mutable snapshots.

That makes the behavior much easier to reason about.

### Initialization

`InitializeSnapshot` lazily creates the mutable ref when first needed.

If a source directory is configured, it:

- evaluates that directory
- gets its snapshot and selector
- creates the cache volume snapshot from that source snapshot

If an owner is configured, it may first synthesize or chown a source directory before creating the mutable ref.

### What callers actually get

Unlike HTTP, remote git, and filesync mirrors, cache volumes are often consumed as the mutable thing itself.

Container exec paths call:

- `InitializeSnapshot` if needed
- `getSnapshot()`
- `getSnapshotSelector()`

and mount that mutable ref directly into the container as a cache mount.

So cache volume is the case where the mutable backing object is not merely internal machinery. It is the actual user-facing semantic object.

## Lifecycle and persistence differences

There are a few important differences between these cases:

### `HTTPState`

- owns an immutable canonical snapshot
- mutates metadata and may swap the owned snapshot
- returns fresh immutable snapshots to callers

### `RemoteGitMirror`

- owns a mutable bare repo snapshot
- mutates it in place across fetches
- returns separate immutable checkout snapshots to callers

### `ClientFilesyncMirror`

- owns a mutable mirror snapshot
- lazily mounts/unmounts runtime state around it
- returns separate immutable synced subtree snapshots to callers

### `CacheVolume`

- owns a mutable snapshot
- exposes that mutable snapshot directly to consumers

## Why this modeling matters

This is the main design point of the whole doc:

these objects are mutable enough that they would be awkward or dangerous if they lived outside the dagql cache model, but they are still regular dagql cache objects, so:

- they can be retained and released with normal object lifecycle
- they can be persisted and reopened
- their disk usage can be measured
- pruning and accounting can see them
- dependencies can retain them when needed

That is the reason we accept the extra complexity of modeling them explicitly instead of hiding them as ambient engine state.
