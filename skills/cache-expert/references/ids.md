# IDs and Digests

IDs are the cache identity backbone in dagql.

An ID is an immutable representation of a call chain. Cache keying starts from that ID, so understanding ID digest behavior is the first step for any cache work.

## Why This Matters for Cache

The base cache treats call IDs as the source of truth:
- Primary lookup key: `CacheKey.ID.Digest()` (recipe digest)
- Secondary lookup key: `CacheKey.ID.ContentDigest()` (content digest), when present

This is why subtle ID changes (args, view, module, custom digest, content digest) directly affect cache behavior.

## Structure

IDs are encoded DAGs of calls (base64 protobuf), defined in `dagql/call/callpbv1/call.proto` and implemented in `dagql/call/id.go`.

Key concepts:
- A call node stores field, type, args, receiver, module, view, and digests.
- Calls reference other calls by digest, forming a Merkle-like DAG.
- IDs are immutable: operations like `Append`, `WithDigest`, `WithContentDigest`, `WithArgument` return new IDs.

## Two Digest Types

### Recipe Digest (`ID.Digest()`)

Represents the call recipe (operation + declared inputs). This is the default cache identity.

Used for:
- cache call keying
- ID DAG references
- most cache hit/miss reasoning

### Content Digest (`ID.ContentDigest()`)

Optional digest representing actual result content.

Used for:
- content-based cache fallback when recipes differ but output content matches
- hashing behavior of callers that reference this ID

Important: content digest does not replace recipe digest globally; both coexist.

## Digest Computation Rules That Affect Cache

From `dagql/call/id.go` behavior:
- Receiver contribution prefers receiver content digest when present, else receiver recipe digest.
- Literal ID arguments similarly prefer content digest when present.
- Arg ordering is deterministic and affects digest.
- Sensitive args are omitted from encoded args and digest calculation.

These rules explain many "why did this key change?" cases.

## How Cache Uses ID Data Today

In `dagql/cache.go`:
- `GetOrInitCall` derives `callKey` from `CacheKey.ID.Digest().String()`.
- It also derives a content fallback key from `CacheKey.ID.ContentDigest().String()`.
- After running `fn`, the cache indexes results under:
  - storage key (primary)
  - result call digest (`resultCallKey`) if different
  - result content digest (`contentDigestKey`) when present

On content-digest hit, cache reuses payload but keeps caller-facing ID equal to the requested ID (via per-call override), so external recipe identity remains stable.

## Cache Key Rewrites

`GetCacheConfig` hooks can rewrite `CacheKey.ID` before execution.

`dagql/cachekey.go` helpers mutate the ID digest by hashing in additional scope data:
- `CachePerClient`
- `CachePerSession`
- `CachePerCall`
- `CachePerSchema`
- `CachePerClientSchema`

After rewrite, `preselect` re-decodes execution args from the final returned ID (`dagql/objects.go`) so execution/cache/telemetry stay aligned.

## Gotchas

- Do not assume recipe and content digest are interchangeable; cache semantics differ.
- If you rewrite IDs in cache config, ensure the returned ID fully represents intended execution args.
- Sensitive args cannot be recovered from decoded IDs.
- `Display()` is useful for debugging but can be expensive on large DAGs.

## Code Map

- ID implementation: `dagql/call/id.go`
- ID protobuf schema: `dagql/call/callpbv1/call.proto`
- Cache key rewriting helpers: `dagql/cachekey.go`
- Cache lookup/storage using IDs: `dagql/cache.go`
- ID rewrite + arg re-decode: `dagql/objects.go`
