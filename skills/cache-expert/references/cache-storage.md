# Cache Storage

This document covers how dagql results are cached. The Dagger Engine is in the middle of a long-term transition from BuildKit-based caching to dagql-native caching.

## Architecture Overview

There are two cache systems:

1. **dagql cache** - The "outer" primary cache. In-memory storage of `Result[T]` and `ObjectResult[T]` values. Has optional SQLite DB for TTL metadata.

2. **BuildKit cache** - The "inner" legacy cache. Persists filesystems (snapshots) on disk. Only checked if dagql cache misses. Being phased out.

dagql knows nothing about BuildKit's internals. BuildKit knows nothing about dagql types.

## dagql Cache Layers

### Base Cache (`engine/cache/`)

The `Cache[K, V]` interface (`cache.go:21`) is the core abstraction:

```go
type Cache[K KeyType, V any] interface {
    GetOrInitializeValue(ctx, key, val) (Result[K, V], error)
    GetOrInitialize(ctx, key, fn) (Result[K, V], error)
    GetOrInitializeWithCallbacks(ctx, key, fn) (Result[K, V], error)
    Size() int
    GCLoop(ctx)
}
```

The implementation (`cache[K, V]`) has:
- **`ongoingCalls`**: Map of in-progress calls, keyed by `(callKey, concurrencyKey)`. Used for call deduplication.
- **`completedCalls`**: Map of completed results, keyed by storage key. This is the actual cache.
- **`db`**: Optional SQLite database for TTL metadata.

### Session Cache (`dagql/session_cache.go`)

`SessionCache` wraps the base cache with session-scoped behavior:

```go
type SessionCache struct {
    cache   cache.Cache[CacheKeyType, CacheValueType]  // Base cache (shared)
    results []cache.Result[...]                        // Results to release on close
    // ...
}
```

**Relationship to base cache:**
- **Base cache**: One per engine, shared across all clients. Results stay in memory until refCount drops to 0.
- **Session cache**: One per client session (created on connect, destroyed on disconnect). Wraps the shared base cache.

**Session cache responsibilities:**
- Keeps references open for all results accessed during the session
- Releases all references when session ends (`ReleaseAndClose`)
- Handles telemetry bookkeeping (seen keys, avoiding duplicate spans)

## What Gets Cached

**Values stored**: `AnyResult` (which includes `Result[T]` and `ObjectResult[T]`)

**Key type**: `string` (the digest)

The cache stores the full dagql result objects in memory. Large data (filesystems, container images) is not stored in the dagql cache itself - that's handled by the underlying BuildKit layer (being phased out).

## CacheKey Structure

```go
type CacheKey[K KeyType] struct {
    CallKey        K       // Primary lookup key (usually ID digest)
    ConcurrencyKey K       // For deduping in-progress calls
    TTL            int64   // Time-to-live in seconds (0 = no expiration)
    DoNotCache     bool    // Skip caching entirely
}
```

### Call Deduplication

When `ConcurrencyKey` is set, concurrent calls with the same `(CallKey, ConcurrencyKey)` are deduplicated - only one actually executes, others wait and receive the same result.

If `ConcurrencyKey` is empty, no deduplication occurs.

### DoNotCache Behavior

When `DoNotCache` is true:
- The call executes without checking cache
- A random storage key is generated (for BuildKit compatibility)
- The result is not stored in cache
- But the returned `Result` can still be passed around and cached under its own digest elsewhere

## SQLite Database

The optional SQLite DB (`engine/cache/db/`) stores **only TTL metadata**, not actual values:

```sql
CREATE TABLE calls (
    call_key TEXT PRIMARY KEY,
    storage_key TEXT NOT NULL,
    expiration INTEGER NOT NULL
);
```

**Purpose**: Track when cached results expire for TTL-based function caching.

**Fields**:
- `call_key`: The cache lookup key
- `storage_key`: The actual storage key (may differ due to session ID mixing)
- `expiration`: Unix timestamp when this entry expires

**GC**: A background loop (`GCLoop`) periodically deletes expired entries (every 10 minutes, in batches of 1000).

## Cache Lifecycle

### Lookup Flow

1. Check `DoNotCache` - if true, skip to execution
2. Compute `storageKey` (usually = `callKey`, but may incorporate session ID for TTL entries)
3. If TTL set, check SQLite for expiration
4. Check `completedCalls` map - if hit, increment refCount and return
5. Check `ongoingCalls` map - if hit, wait for completion
6. Start new call, add to `ongoingCalls`
7. On completion: remove from `ongoingCalls`, add to `completedCalls`

### Reference Counting

Each `result` has a `refCount`. When you get a result from cache, refCount increments. When you `Release()`, it decrements. When refCount reaches 0 and no waiters remain, the entry is removed from cache.

### Callbacks

`ValueWithCallbacks` allows attaching lifecycle hooks:

```go
type ValueWithCallbacks[V any] struct {
    Value              V
    PostCall           PostCallFunc    // Called on every return (cached or not)
    OnRelease          OnReleaseFunc   // Called when removed from cache
    SafeToPersistCache bool            // OK to persist TTL metadata
}
```

## TTL-Based Caching (Function Calls)

TTL-based caching currently only applies to module function calls. The feature is in early stages and will expand over time.

### How TTL Works

1. When a call has `TTL > 0`, the cache checks SQLite for an existing entry
2. If no entry exists, or it's expired: generate new `storageKey` (with session ID mixed in), execute call
3. If `SafeToPersistCache` is true after execution: persist TTL metadata to SQLite
4. Future calls within TTL use the same `storageKey` (even from different sessions)

### SafeToPersistCache

Results that reference **unreproducible data** cannot be persisted. Currently, the only case is `SetSecret`:

- `SetSecret` mutably stores an in-memory secret value
- For security, secrets can't be written to disk
- Any result chain containing a `SetSecret`-created secret has `SafeToPersistCache = false`

### Storage Key and Session ID

The `storageKey` (what actually keys the cache) differs from `callKey` for TTL entries:

**Why mix session ID into storage key?**

- For non-persistable results (`SafeToPersistCache = false`): The session-specific storage key ensures the cache entry only lasts for that session
- For persistable results: The first session's storage key gets persisted in SQLite. Future sessions (until TTL expires) look up and reuse that same storage key, achieving cross-session caching

This handles the tricky case where we don't know if something is safe to persist until *after* the call completes.

## Recursive Call Detection

If a call with a given cache key tries to make another call with the same cache key, it would deadlock (waiting for itself). The cache detects this via context and returns `ErrCacheRecursiveCall`.

Note: This detection doesn't work across network/IPC boundaries, so it's not perfect.

## Code Locations

| File | Contents |
|------|----------|
| `engine/cache/cache.go` | `Cache` interface, `cache` implementation, `CacheKey`, `result` |
| `engine/cache/db/` | SQLite schema, queries, models for TTL metadata |
| `dagql/session_cache.go` | `SessionCache` - session-scoped wrapper |

## Storage Key and BuildKit Integration

During the transition period, the dagql storage key flows into BuildKit's cache key system. This is how they connect:

### The Flow

1. **dagql sets storage key in context**: `ctxWithStorageKey(ctx, storageKey)` in `engine/cache/cache.go`

2. **Module function reads it**: In `core/modfunc.go:666`, `cache.CurrentStorageKey(ctx)` is read and hashed into `execMD.CacheMixin`

3. **CacheMixin passed to container exec**: The `ExecutionMetadata.CacheMixin` is passed to `withExec` operations

4. **Mixed into operation cache key**: In `core/schema/container.go:983`, the `CacheMixin` is mixed into the operation's digest, which becomes part of BuildKit's cache key

### dagql Miss, BuildKit Hit

When dagql cache misses but BuildKit has a cached result:

1. dagql calls the operation (e.g., `container.withExec`)
2. The operation goes to BuildKit, which finds a cached filesystem snapshot
3. BuildKit returns the cached snapshot
4. A dagql result (e.g., `*core.Container`) is constructed from the BuildKit result
5. This new dagql result is stored in the dagql cache

So BuildKit's persistent cache can "warm" the dagql in-memory cache.

### Future Direction

The BuildKit solver will be removed entirely, including BuildKit's operation cache key storage. Parts of BuildKit that manage filesystem snapshots may remain in modified forms, but the cache key machinery is being replaced by dagql-native caching.
