# Writing Core APIs With The DagQL Cache

This is a practical guide for writing `core`, `core/schema`, and `core/sdk`
APIs in the current dagql cache model.

Almost everything here is covered in more depth elsewhere:

- `cachebasics.md`
- `egraph.md`
- `cache_persistence.md`
- `cache_pruning.md`
- `session_resources.md`
- `lazy_evaluation.md`
- `dynamicinputs.md`
- `dagqltypes.md`

The point of this doc is not to replace those. The point is to give you a
workflow for the questions you usually need to answer when adding a new API.

## Default Mental Model

Start from this assumption:

- your field is deterministic from receiver + args
- it returns an ordinary immutable value or object
- it does not need special session scoping
- it does not own snapshots or other external resources
- it does not lazily defer heavy work

If that is true, dagql caching mostly just works.

The rest of this guide is about the cases where you need to opt into something
more.

## The Decision Checklist

When writing a core API, these are the cache questions to walk through:

1. Is this field actually cacheable, or does it need scoped identity?
2. What result do I construct: detached, attached, lazy, snapshot-backed?
3. Do I need to change identity beyond the default recipe?
4. Does the returned object embed other results that the cache must know about?
5. Does the object own snapshots or other resources that need lifecycle hooks?
6. Should the field be persistable across engine restarts?
7. Am I in one of the rare advanced cases that needs explicit equivalence
   teaching or other specialist APIs?

If you consciously answer those questions, you usually end up in a good place.

## Step 1: Pick The Right Caching Mode

For most fields, the right answer is:

- keep caching enabled
- let the default recipe identity do its job

Only opt out or scope it differently when you have a real reason.

### Prefer scoped identity over `DoNotCache`

If a field is client-, session-, schema-, or call-scoped, usually what you want
is not `DoNotCache`, but one of:

- `WithInput(dagql.PerClientInput)`
- `WithInput(dagql.PerSessionInput)`
- `WithInput(dagql.PerCallInput)`
- `WithInput(dagql.CurrentSchemaInput)`
- `WithInput(dagql.RequestedCacheInput("noCache"))`
- a dynamic input hook

That keeps the field in the cache model while making the identity as specific as
it needs to be.

### Be very cautious with `DoNotCache`

`DoNotCache` still exists, but it is a much narrower tool now than people often
assume.

Practical guidance:

- it is usually only a good fit for scalar-returning APIs
- object-returning `DoNotCache` fields are usually a bad fit in practice because
  chaining and downstream caching get much harder to reason about
- if you think you want `DoNotCache` on an object field, first ask whether you
  really want scoped identity instead

There are also hard implementation limits:

- a newly created do-not-cache result cannot be lazy
- a newly created do-not-cache result cannot implement `OnReleaser`

So if the result needs cache-managed lifecycle or deferred materialization, it
is already a bad candidate for `DoNotCache`.

## Step 2: Start Detached By Default

When a resolver creates a result with:

- `dagql.NewResultForCurrentCall`
- `dagql.NewResultForCall`
- `dagql.NewObjectResultForCurrentCall`
- `dagql.NewObjectResultForCall`

it is creating a detached result.

That is the normal starting point.

Detached results are good because:

- you can build them up incrementally
- you can attach metadata before publication
- if the operation errors, you can throw them away cheaply
- they do not need a cache-local result ID yet

You do **not** need to attach everything immediately.

### When to attach explicitly

Reach for `cache.AttachResult(...)` only when you genuinely need a cache-backed
result with a real result ID and normal lifecycle tracking.

Common reasons:

- another API needs an attached result specifically
- you need to retain it via explicit dependency APIs
- you are manufacturing a canonical cache-backed object intentionally

If you are just assembling an ordinary return value inside one resolver,
detached is usually right.

## Step 3: Decide Whether The Default Recipe Identity Is Enough

The default identity is the structured `ResultCall`:

- receiver
- explicit args
- implicit inputs
- module provenance
- view
- nth selection
- extra digests

Often that is enough.

When it is not, these are the normal tools.

### Implicit inputs and dynamic inputs

Use these when the call needs extra cache scoping or canonicalization.

Examples:

- per-client workdirs and host sockets
- per-session HTTP state resolution
- schema-sensitive queries like `currentTypeDefs`
- canonicalizing or synthesizing internal args before cache lookup

Read `dynamicinputs.md` if you need anything beyond the very simplest cases.

### `WithContentDigest`

Use `WithContentDigest` when the result has a stable semantic content identity
that should override or augment recipe identity.

This is extremely common and extremely useful.

Examples:

- host imports with content digests
- secret/socket handle objects
- scoped module objects
- HTTP file outputs

The usual question is:

"If two different recipes produce the same semantic thing, do I want the cache
to know they are equivalent by content?"

If yes, this is probably the tool.

### `WithSessionResourceHandle`

Most APIs do **not** need to know about this.

This is only for building new session-resource-style objects, like secrets or
sockets, where cache hits must become conditional on the caller having loaded
the matching resource handle.

If you think you need this, stop and read `session_resources.md`.

## Step 4: Model Embedded Cached Objects As Result Wrappers

If your object stores another cached object, store it as a result wrapper:

- `dagql.ObjectResult[*Foo]`
- `dagql.Result[*Bar]`
- arrays of result wrappers where appropriate

Do **not** casually unwrap everything to raw `*Foo` pointers if what you really
mean is "this object depends on that cache-backed object."

Why this matters:

- preserves identity
- preserves dependency tracking
- preserves persistence/reconstruction options
- avoids rebuilding parallel pointer graphs

This is especially important for graph-shaped metadata like typedefs, but it
applies more broadly too.

## Step 5: Teach The Cache About Dependencies

This is one of the most important parts of writing correct APIs.

If your returned object refers to other results outside the normal call
structure, the cache must be told about that.

Otherwise:

- dependencies may be pruned while the parent still exists
- persistence closure may be incomplete
- session-resource requirements may not propagate correctly

### Usually: implement `HasDependencyResults`

This is the normal mechanism.

Implement `AttachDependencyResults` on your object if it embeds child results
that should be normalized onto attached/cache-backed results before lifecycle
bookkeeping and persistence.

Typical examples:

- `Directory`
- `File`
- `Container`
- `Module`
- typedef-related objects
- `GitRepository`

Rule of thumb:

- if your object stores child results on fields, you probably need this

### Sometimes: `AddExplicitDependency`

Use `cache.AddExplicitDependency(...)` when you need an extra retained edge after
attachment that is not naturally represented by your object's fields.

This is rarer.

Examples today include some SDK generation paths retaining loaded/generated
module results.

## Step 6: Decide Whether To Be Lazy

Lazy evaluation is for cases where returning the object shell immediately is
cheap, but fully materializing it right away is expensive or unnecessary.

If you need it:

- put the deferred recipe on a concrete `Lazy[...]` implementation
- use `LazyState`
- use `LazyAccessor` for fields that should not be read directly before
  evaluation

Practical warning:

- if your implementation needs concrete data from a lazy dependency, call
  `cache.Evaluate(...)` or go through the relevant `LazyAccessor`
- do not assume the value is already materialized

See `lazy_evaluation.md` for the real shape.

## Step 7: If The Object Owns Snapshots Or Similar Resources, Implement The Lifecycle Hooks

This is the part people most often forget when writing more complex objects.

If the object owns snapshots or other cache-meaningful external state, you
probably need some combination of:

- `OnReleaser`
- `PersistedSnapshotRefLinks`
- `CacheUsageIdentities`
- `CacheUsageSize`
- `CacheUsageMayChange`

### `OnReleaser`

Implement this when the object must release owned resources when the cache drops
the result.

Very common for snapshot-backed objects.

### `PersistedSnapshotRefLinks`

Implement this when persisted results need to record which snapshots they own.

Without this, persistence can encode the object payload but still miss the
snapshot linkage.

### `CacheUsageIdentities` / `CacheUsageSize` / `CacheUsageMayChange`

Implement these when the object's snapshot usage should participate correctly in
cache accounting and pruning.

These matter for:

- pruning heuristics
- deduplicated usage accounting across shared snapshots
- mutable-backed objects whose usage may change over time

Rule of thumb:

- if the object owns snapshots and you want pruning/accounting to make sense,
  implement these methods

Good reference patterns:

- `Directory`
- `File`
- `Container`
- `HTTPState`
- `RemoteGitMirror`
- `ClientFilesyncMirror`
- `CacheVolume`

## Step 8: Decide Whether The Field Should Be Persistable

This is a schema-field decision first.

On the field spec, `IsPersistable()` means the result is eligible for persistent
cache retention across engine restarts.

The decision is usually:

- is this expensive enough to recompute that persistence is worth the overhead?

Do not persist tiny cheap things just because you can.

### What else persistable objects need

If a persistable field returns an object, that object usually needs:

- `dagql.PersistedObject`
- `dagql.PersistedObjectDecoder`

In practice that means implementing:

- `EncodePersistedObject`
- `DecodePersistedObject`

The basic pattern is usually JSON, with special handling where needed for:

- embedded result references
- snapshot links
- lazy forms

If the object owns snapshots, persistence usually also needs
`PersistedSnapshotRefLinks`, as noted above.

### Best-effort, not database semantics

Persistable cache is still cache persistence, not robust application-state
storage.

So:

- treat it as a performance optimization
- not as a correctness requirement

## Step 9: Inspecting Call Metadata

If you need to look at how something was called, you can inspect call metadata.

Useful entry points:

- `res.ResultCall()`
- `cache.ResultCallByResultID(...)`
- `cache.WalkResultCall(...)`

This is useful for:

- provenance-sensitive logic
- debugging
- advanced graph inspection

Do not overuse it when simpler structured fields on your object would do, but it
is there when you need it.

## Step 10: Rare Advanced APIs

Most APIs should stop before this point.

These are specialist tools.

### `TeachCallEquivalentToResult`

Use this when you discovered after execution that a call is semantically
equivalent to an existing result and you want to publish that equivalence into
the cache/e-graph.

Good example: teaching that a no-op `Directory.without(...)` is equivalent to
its parent directory.

### `MakeResultUnpruneable`

Use this only when a result truly should live for the life of the engine.

The main example today is core typedef retention.

### `GetOrInitArbitrary`

Use this when you need in-memory cached arbitrary values that are not dagql DAG
results at all.

This is not the normal path for graph objects.

## Worked Example 1: A Normal Cached Object Field

This is the 80% case.

You have a deterministic field that returns an object and maybe wants a content
digest.

Sketch:

```go
func (s *thingSchema) thing(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args thingArgs,
) (inst dagql.ObjectResult[*core.Thing], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	obj := &core.Thing{
		Name: args.Name,
	}

	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, obj)
	if err != nil {
		return inst, err
	}

	dgst := hashutil.HashStrings(args.Name)
	return inst.WithContentDigest(ctx, dgst)
}
```

What is happening here:

- the resolver creates a detached object result
- dagql field selection will later publish it through `GetOrInitCall`
- the content digest teaches semantic equivalence if needed
- there are no special dependency or lifecycle hooks because the object is just
  ordinary immutable data

That is the happy path.

## Worked Example 2: A Snapshot-Backed Persistable Object

This is the richer pattern.

Suppose your object owns a snapshot and is worth persisting.

You probably need:

1. A persistable field:
   - `dagql.NodeFunc(...).IsPersistable()`
2. An object that can release its snapshot:
   - `OnRelease`
3. Persistence hooks:
   - `EncodePersistedObject`
   - `DecodePersistedObject`
   - `PersistedSnapshotRefLinks`
4. Usage/accounting hooks:
   - `CacheUsageIdentities`
   - `CacheUsageSize`
   - `CacheUsageMayChange` if appropriate
5. `HasDependencyResults` if the object embeds child results too

In practice, the best reference patterns are not synthetic examples, but real
objects:

- `Directory` / `File`
  good for immutable snapshot-backed object patterns
- `HTTPState`
  good for "mutable internal backing state, immutable outward-facing result"
- `CacheVolume`
  good for "user-facing mutable snapshot object"
- `ClientFilesyncMirror` / `RemoteGitMirror`
  good for "mutable backing object that powers separate immutable outputs"

When in doubt, copy one of those shapes rather than inventing a new pattern.

## Practical Warnings

- Prefer scoped identity over `DoNotCache`.
- Start detached and attach only when you actually need it.
- Store cached child objects as result wrappers, not just raw pointers.
- If your object embeds child results, implement `HasDependencyResults`.
- If your object owns snapshots, implement the release/persistence/accounting
  methods too.
- If your dependency may be lazy, explicitly evaluate it before reading concrete
  state.
- Only opt into persistence when the recomputation cost justifies it.
- Session-resource APIs are specialized; do not casually invent them.

## Reading Map

When you need more detail, jump out from here like this:

- result model and public cache APIs: `cachebasics.md`
- dynamic/implicit input identity shaping: `dynamicinputs.md`
- lazy object patterns: `lazy_evaluation.md`
- persistence: `cache_persistence.md`
- pruning and retention: `cache_pruning.md`
- session-resource conditional hits: `session_resources.md`
- advanced equivalence/e-graph model: `egraph.md`

## Final Mental Model

When writing a core API, try to keep this sentence in your head:

"What is the semantic identity of this result, what other results/resources does
it own, and what lifecycle hooks does the cache need in order to manage it
correctly?"

If you answer that directly in the code, the rest usually falls into place.
