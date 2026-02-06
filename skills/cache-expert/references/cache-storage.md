# Cache Storage

This document explains how cache storage works in the current dagql cache stack.

## Architecture Overview

There are two layers to reason about:

1. Base cache (`dagql/cache.go`)
- In-memory result storage and lookup
- Optional SQLite metadata for TTL expiration bookkeeping

2. Session cache (`dagql/session_cache.go`)
- Per-session wrapper around base cache
- Tracks result lifetimes for session close
- Adds telemetry dedupe and error-retry behavior

The base cache is specialized for dagql results.

## Base Cache Data Model (`dagql/cache.go`)

### Call Result Entries

`sharedResult` holds cached call entry state:
- identity/index fields: `storageKey`, `resultCallKey`, `contentDigestKey`
- payload: constructor ID + typed value + object type
- lifecycle: waiters, refcount, release callback
- persistence control: `safeToPersistCache`, `persistToDB`

Per-call `Result[T]` wraps shared payload and carries per-call metadata:
- hit flags
- per-call ID override (`idOverride`) used for content-digest-hit identity preservation
  (when a lookup hits by content digest instead of recipe digest, the payload is reused
  but the caller still sees the ID it asked for)

### Arbitrary In-Memory Entries

`GetOrInitArbitrary` caches opaque `any` values by plain string key using `sharedArbitraryResult`.

This path is:
- in-memory only
- refcounted/released like normal call results
- intentionally separate from call-ID-based result caching

## Cache Keys and Indexes

`CacheKey` fields:
- `ID` (required)
- `ConcurrencyKey`
- `TTL`
- `DoNotCache`

`GetOrInitCall` builds and uses:
- `callKey = ID.Digest().String()`
- `storageKey` (may differ from callKey for TTL/session handling)
- `contentKey = ID.ContentDigest().String()` (optional)

In-memory indexes:
- `completedCalls[storageKey]`
- `completedCalls[resultCallKey]` when result key differs
- `completedCallsByContent[contentDigestKey]` when present
- `ongoingCalls[(callKey, concurrencyKey)]` for in-flight dedupe

## Lookup and Execution Flow

High-level `GetOrInitCall` behavior:

1. Validate key
2. `DoNotCache` path:
- force random storage key for buildkit compatibility
- execute resolver directly
- return normalized detached result (no cache ownership)

3. Non-`DoNotCache` path:
- compute storage key (possibly TTL/db influenced)
- set storage key in context
- recursive-call guard
- check completed by storage key
- check completed by content key fallback
- dedupe on in-flight key if `ConcurrencyKey` is set
- run resolver if needed

4. On success (`wait`):
- move from ongoing -> completed indexes
- increment refcount
- persist TTL metadata only when safe
- return result

5. On failure:
- remove stale in-flight entries once no waiters/refs remain

## Content-Digest Fallback Semantics

If storage-key lookup misses but content digest hits:
- cache reuses payload
- returned result uses caller’s requested ID via per-call override

Why: caller should keep recipe identity they requested, while still reusing equivalent content.

## TTL Metadata (SQLite)

SQLite lives under `dagql/db/*` and stores TTL metadata only:
- call key
- storage key
- expiration

Important details:
- Result payloads are in-memory, not stored in SQLite.
- Metadata is persisted only after successful call and only if `safeToPersistCache` is true.
- DB is configured for performance (WAL, `synchronous=OFF`), so persistence is best-effort cache metadata, not durability-critical state.

### How TTL Works

When `CacheKey.TTL > 0` and DB is enabled:
1. Cache looks up `callKey` in SQLite.
2. If there is no row (or row expired), cache picks a fresh storage key and prepares a deferred DB write.
3. Call executes and stores in-memory result under that storage key.
4. Only after success, and only when `safeToPersistCache` is true, cache writes `(callKey -> storageKey, expiration)` to SQLite.
5. Later sessions within TTL resolve the same call key to that persisted storage key and reuse it.

If `safeToPersistCache` is false, the result still works in-memory for the current session but no TTL metadata is persisted.

### Why Mix Session ID into Storage Key?

For TTL calls, storage key creation currently mixes in `SessionID` when creating a fresh key.

Reason:
- Before call execution completes, cache does not yet know if the result is safe to persist (for example, secret-sensitive paths).
- Session-scoped storage key prevents accidental cross-session reuse for non-persistable results.
- If the result is persistable, that same storage key is then recorded in SQLite and reused across sessions until TTL expiry.

This is a deliberate transitional tradeoff; code comments in `dagql/cache.go` call out future model cleanup.

## Reference Counting and Release

Both call and arbitrary results are refcounted.

Entry removal happens when:
- `refCount == 0`
- and `waiters == 0`

On removal, optional `OnRelease` callbacks run.

This is why callers must release results when done (session cache automates this per session).

## Session Cache Responsibilities (`dagql/session_cache.go`)

`SessionCache` wraps base cache to provide:
- session-close release of all tracked results (`ReleaseAndClose`)
- telemetry dedupe (`seenKeys`)
- retry behavior after errors (`noCacheNext`)

Error-retry behavior:
- after an error, next attempt for that call key is forced `DoNotCache`
- on success, it attempts reinsertion under normal key
- this is a compatibility behavior tied to current solver interactions

## BuildKit Coupling (Current Transitional Detail)

`CurrentStorageKey(ctx)` exposes storage key via context so downstream code can mix cache identity into buildkit-facing execution metadata.

This is transitional integration, but still important when debugging function-caching behavior end to end.

## Filesync Is Separate

Filesync does not use dagql base cache internals for its change-tracking cache.

Current filesync change cache is a dedicated typed implementation in:
- `engine/filesync/change_cache.go`

See `filesync.md` for details.

## Code Map

- Base cache: `dagql/cache.go`
- TTL metadata schema/queries: `dagql/db/`
- Session wrapper: `dagql/session_cache.go`
- Preselect/cache key generation: `dagql/objects.go`
