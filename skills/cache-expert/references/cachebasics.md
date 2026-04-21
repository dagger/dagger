# DagQL Cache Basics

This is the high-level overview doc for the dagql cache.

It is intentionally focused on the basics:

- what the cache is trying to do
- what a call is
- what `GetOrInitCall` does
- what `Result`, `ObjectResult`, and `sharedResult` are
- what the main cache APIs are that `core`, `core/schema`, and `core/sdk` actually use

For details on specific subsystems, see the other docs:

- `egraph.md`
- `cache_persistence.md`
- `cache_pruning.md`
- `session_resources.md`
- `lazy_evaluation.md`
- `typedefs.md`
- `dynamicinputs.md`
- `dagqltypes.md`

## Goals

The cache exists to give dagql a coherent identity and lifecycle model for engine values.

At a high level, it wants to:

- dedupe repeated operations by semantic identity
- track exact result dependencies as a DAG
- keep live results retained while something still owns them
- allow detached tentative results during execution and materialize them only when needed
- support content-based equivalence, persistence, pruning, lazy evaluation, and session-resource gating on top of the same core result model

The fundamental operation is: given a call, either get the existing result for that call or initialize it once.

That is `GetOrInitCall`.

## What a call is

The cache keys off a `CallRequest`.

`CallRequest` has two layers:

1. `ResultCall`, which is the semantic/provenance description of the operation
2. request-only cache policy, like:
   - `ConcurrencyKey`
   - `TTL`
   - `DoNotCache`
   - `IsPersistable`

The semantic `ResultCall` is the important part for identity. It includes:

- field name
- return type
- receiver
- module provenance
- explicit args
- implicit inputs
- view
- nth list selection
- extra digests

So when we say "cache key" here, what we really mean is "the identity derived from this structured call."

This is also why module inputs are worth mentioning explicitly: the module associated with a call is itself represented as another result reference inside `ResultCall.Module`, so it participates in both identity and dependency tracking.

## What `GetOrInitCall` does

`Cache.GetOrInitCall(ctx, sessionID, resolver, req, fn)` is the basic call path.

High level flow:

1. Validate the request.
2. If `DoNotCache` is set, just run `fn` directly and return an uncached detached result, unless `fn` returned an already attached result.
3. Otherwise derive the request's recipe identity from `req.ResultCall`.
4. Look for a matching cached result.
5. If there is a hit, return the attached result and add a session ownership edge.
6. If there is a miss, maybe dedupe against an in-flight equivalent call using `ConcurrencyKey`.
7. If still a miss, run `fn`, materialize/attach the result, normalize dependencies, and publish it.
8. Track session ownership, TTL, persistence eligibility, lazy state, and so on.

That is the central cache contract.

One important practical note: most `core` and `core/schema` code does not call `GetOrInitCall` directly. Normal dagql field selection does that for you. The code outside dagql more often interacts with already-created results and uses APIs like `Evaluate`, `AttachResult`, `AddExplicitDependency`, and result helper methods.

## `sharedResult`: the real thing

`sharedResult` is the fundamental underlying cache object.

It is what actually lives in the cache. `Result` and `ObjectResult` are wrappers around it.

The most important parts of `sharedResult` are:

- `id`
  The stable cache-local result identity. This is the integer identity used throughout the cache and persistence.
- `self`
  The actual typed payload.
- `resultCall`
  The authoritative call/provenance metadata for how this result was produced.
- `deps`
  The exact child result dependencies that this result owns.
- `incomingOwnershipCount`
  The authoritative liveness count derived from session edges, persisted edges, and result dependency edges.

It also holds:

- session-resource requirement metadata
- lazy-evaluation state
- persisted-import state
- prune/accounting metadata
- `onRelease` hooks

But the main basics are still: ID, payload, call metadata, dependencies, and ownership.

## Result IDs

When you ask a cache-backed `Result` or `ObjectResult` for its `ID()`, what you get is essentially the `sharedResult.id`, encoded as a dagql/call handle-form ID with the current type view attached.

That means:

- attached results have IDs
- detached results do not
- the same underlying `sharedResult` can be exposed through different type views without becoming a different cached result

That last point matters especially for nullables; see `dagqltypes.md`.

## `Result` and `ObjectResult`

`Result[T]` is the lightweight typed wrapper around a `sharedResult`.

It adds per-call/view behavior, like:

- whether the caller hit cache
- nullable wrapping view
- dereferenced view

`ObjectResult[T]` is the object-specialized version of `Result[T]`. It carries the dagql class/object-type machinery needed for field selection.

The important mental model is:

- `sharedResult` is the real cache entry
- `Result` / `ObjectResult` are cheap wrappers/views over that shared entry

This is why things like nullable/non-null views are handled at the `Result` layer rather than by making separate cached objects.

## Dependencies form a DAG

Every cache-backed result can depend on other cache-backed results, and those dependencies form the result DAG.

There are two main kinds of dependencies.

### Structural dependencies

These come from the call structure itself.

If a `ResultCall` refers to another result through:

- receiver
- arg values
- implicit inputs
- module provenance

then that reference becomes part of the result's structural identity and dependency closure.

### Explicit dependencies

Sometimes a result stores another result on itself outside the normal call structure. In those cases, the cache still needs to know about the edge.

There are two ways this is handled:

- most types implement `dagql.HasDependencyResults`, which lets attachment normalize and record embedded child results
- code can call `cache.AddExplicitDependency(...)` when it needs to add an ad hoc retained edge after attachment

Examples:

- `TypeDef`, `Function`, `ObjectTypeDef`, `Directory`, `Container`, and many others implement `HasDependencyResults`
- SDK codegen paths use `AddExplicitDependency` to retain loaded/generated module results, for example in `core/sdk/module_typedefs.go`

This matters for both retention and pruning: if the cache does not know the edge exists, it cannot keep the dependency alive correctly.

## Detached results

When you create a result with:

- `dagql.NewResultForCall`
- `dagql.NewResultForCurrentCall`
- `dagql.NewObjectResultForCall`
- `dagql.NewObjectResultForCurrentCall`

you are creating a detached result.

That means:

- it has payload and a call frame
- it is not yet materialized in the cache
- its underlying `sharedResult.id` is still zero
- asking for an ID will fail

This is intentional and useful.

Detached results let code build up tentative values, pass them around, rewrite metadata, or throw them away on error without immediately paying the cost of full cache materialization.

If you later need a real cache-backed result, you can attach it.

## `AttachResult`

`Cache.AttachResult(...)` materializes a detached result into the cache.

If the detached result is equivalent to an already attached cached result, attachment can reuse that existing result instead of creating a duplicate.

If a new attached result really is needed, attachment:

- normalizes pending result-call refs
- initializes the shared result
- attaches dependency results
- tracks session ownership

It is safe to use when you truly need an attached result. It is just more expensive than leaving something detached, because now the cache has to track it properly.

By default, attachment also creates a session ownership edge, so if the session ends and nothing else owns the result, it can still be released normally.

## Session ownership is automatic

Whenever a session obtains an attached result through normal cached execution or attachment, the cache records that the session owns that result.

Releasing the session drops those session edges. If nothing else still owns the results, they become releasable.

That ownership model is the reason "just attach it if you really need to" is generally safe, even if it is not always the cheapest thing to do.

## The cache APIs you will actually use

These are the cache APIs that show up most often in `core`, `core/schema`, and `core/sdk`.

### `EngineCache(ctx)`

This is how most code gets the current cache instance.

### `Evaluate(results...)`

This forces lazy-backed results to finish materializing.

This is probably the most commonly used cache API from `core` code. Many container, directory, file, service, changeset, and schema helpers call it before they need concrete snapshots or fields.

See `lazy_evaluation.md` for the real details.

### `AttachResult(...)`

Use this when you have a detached result but now need a real cache-backed result with an ID and normal lifecycle tracking.

One example is SDK helper code that scopes a module result, assigns it a content digest, and then explicitly attaches it.

### `AddExplicitDependency(...)`

Use this when one attached result should retain another attached result even though the edge is not implied by the `ResultCall`.

Example: SDK module-types generation retains a loaded/generated module result via an explicit dependency edge.

### `TeachCallEquivalentToResult(...)`

Use this when you want to teach the cache that some call is semantically equivalent to an already existing result, even though that equivalence was only discovered after execution.

The current notable example is `Directory.Without(...)` teaching the cache that a no-op removal is equivalent to the parent directory result.

This is an e-graph identity/publication API; see `egraph.md` for the deeper story.

### `Result.WithContentDigest(...)`

This is technically a result API, but it is one of the most important identity tools used throughout `core`.

When attached, it delegates to cache content-digest teaching. When detached, it updates the detached call metadata so the future attached result will carry that content digest identity.

This is used in many places to make semantically equivalent values share cache identity by content rather than by exact recipe.

### `MakeResultUnpruneable(...)`

Marks a result as retained for the life of the engine.

The main current example is core typedef retention in `core/schema/coremod.go`.

### `BindSessionResource(...)` / `ResolveSessionResource(...)`

These are the cache hooks for session resources like secrets and sockets.

See `session_resources.md`.

### `GetOrInitArbitrary(...)`

This is the in-memory-only sibling for caching arbitrary non-dagql values by key.

It is used in places like git remote metadata caching, where the value is not a dagql result DAG node.

### `LoadResultByResultID(...)`, `ResultCallByResultID(...)`, `WalkResultCall(...)`

These are more specialized introspection/provenance helpers.

They are used when code needs to:

- reload a result by handle ID
- inspect a stored call frame
- walk the graph of refs inside a call

Examples show up in query/module provenance and persistence helper code.

## TTL

TTL is currently mainly used for function caching, but in the new dagql cache it is a general mechanism on `CallRequest`.

The important subtlety:

- TTL guarantees expiry after the TTL
- TTL does not guarantee retention until the TTL

In other words, TTL affects cache-hit eligibility and persisted-edge expiry. It does not mean "this result must be kept alive until then no matter what."

## Concurrency key

The concurrency key controls in-flight deduplication, not general cache hits.

Two calls can be deduped while they are actively running only if they share:

- the same call identity
- the same `ConcurrencyKey`

In normal dagql field execution, the default concurrency key is the client ID.

That is a deliberate tradeoff. Dedupe across clients is possible in principle, but it complicates cancellation/disconnect handling a lot. So today, in-flight singleflight mostly stays within a client.

## The short mental model

If you just need the shortest usable mental model, it is:

- `GetOrInitCall` is the basic cache operation
- `ResultCall` is the semantic description of the operation
- `sharedResult` is the real cache entry
- `Result` / `ObjectResult` are wrappers over that cache entry
- dependencies form a DAG and drive retention
- detached results are tentative values with no cache ID yet
- attach only when you actually need materialization
- most `core` code interacts with the cache through `Evaluate`, `AttachResult`, explicit-dependency helpers, and result identity helpers

That is the basic shape of how the dagql cache works.
