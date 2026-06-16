# DagQL E-Graph Cache

This document describes the current `dagql` cache implementation centered on
the e-graph in `dagql/cache.go` and `dagql/cache_egraph.go`.

The code is the source of truth. This doc is meant to make the current model
faster to load into your head, not replace reading the code.

For test-running and debugging workflow, see `debugging.md`. This doc is about
how the cache is structured and how it behaves.

## What This "E-Graph" Is Here

This is not a full rewrite engine or a generic academic e-graph implementation.
In practice, the important pieces are:

- a union-find over digest equivalence classes
- symbolic terms keyed by operation shape plus canonical input eq-classes
- congruence repair when eq-classes merge
- separate materialized results attached to terms and digest indexes

The useful mental model is:

1. A call has a recipe identity.
2. Some different recipes can still be known equivalent because they produce the
   same output.
3. The cache stores those equivalences as digest eq-classes.
4. Structural lookup uses a term:
   `selfDigest + canonical input eq-classes -> output eq-class`.
5. Actual cached payloads are `sharedResult`s hanging off the graph, not nodes in
   the graph itself.

So the "e-graph" part is mostly the union-find + congruence closure over terms.
The cache behavior around ownership, sessions, persistence, and lazy payloads
lives around it.

## Identity Layers

There are four distinct identity layers worth keeping separate in your head:

1. **Recipe digest**
   - The authoritative identity of a specific call recipe.
   - Derived from `ResultCall` / `call.ID`.
   - Used for exact request lookup first.

2. **Extra digests**
   - Additional equivalence evidence attached to a result or request.
   - The most important one is content digest.
   - They do not replace recipe identity; they enlarge equivalence.

3. **Structural term**
   - A symbolic cache lookup key made from:
     - self digest
     - canonicalized input eq-classes
   - This is the real "e-graph lookup" key.

4. **Shared result ID**
   - A local cache identity for one materialized result payload.
   - This is not semantic identity. It is ownership/lifecycle identity inside the
     current cache instance.

## The Core Invariant

The most important invariant is:

- recipe digests identify specific calls
- eq-classes identify interchangeable outputs
- terms express congruence over equivalent inputs
- `sharedResult`s hold real payloads and lifecycle state

That separation is why the implementation works as well as it does:

- lookup can stay symbolic and cheap
- ownership does not depend on the e-graph shape
- multiple materialized results can share one output eq-class
- a hit can preserve request-facing recipe shape while still reusing an
  equivalent output

## Main Data Structures

### Cache Lock Domains

`dagql/cache.go` splits the cache into three main lock domains:

- `callsMu`
  - in-flight call bookkeeping
  - arbitrary in-memory call maps

- `sessionMu`
  - per-session result ownership tracking
  - per-session resource handles
  - per-session lazy span tracking

- `egraphMu`
  - eq-classes
  - terms
  - result indexes
  - dependency ownership graph
  - persisted edges

This split matters both for correctness and performance. In particular, recent
fixes deliberately avoid cross-locking `callsMu` and `egraphMu` in observational
paths.

### Eq-Classes

Eq-class state lives in `Cache`:

- `egraphDigestToClass`
- `eqClassToDigests`
- `eqClassExtraDigests`
- `egraphParents`
- `egraphRanks`

This is a standard union-find setup:

- every known digest belongs to at most one eq-class
- merging two digests means merging their classes
- path compression and ranks keep lookup cheap

The main entry points are:

- `ensureEqClassForDigestLocked`
- `findEqClassLocked`
- `mergeEqClassesLocked`
- `repairClassTermsLocked`

`eqClassExtraDigests` is worth calling out separately: it stores labeled
class-level digest facts known for an output eq-class. It is not the main
direct-hit index. Direct digest hits come from `egraphResultsByDigest`.

### Terms

Terms live in `dagql/cache_egraph.go` as `egraphTerm`:

- `selfDigest`
- `inputEqIDs`
- `termDigest`
- `outputEqID`

Important detail: terms are symbolic only. They do **not** hold payloads.

`termDigest` is the digest of:

- self digest
- canonical input eq-class IDs

That means if input eq-classes merge later, the term may need to move to a new
`termDigest`. That is what congruence repair does.

Term indexes:

- `egraphTerms`
- `egraphTermsByTermDigest`
- `inputEqClassToTerms`
- `outputEqClassToTerms`
- `termInputProvenance`

### Materialized Results

Materialized results are `sharedResult`s in `dagql/cache.go`.

These hold:

- payload (`self`, `hasValue`, `isObject`)
- authoritative `ResultCall`
- dependency edges (`deps`)
- session resource requirements
- snapshot ownership links
- TTL / prune metadata
- persisted envelope for imported lazy payloads
- ownership count (`incomingOwnershipCount`)
- dependency-attachment barrier state
- lazy-evaluation state

This is a major design point: the e-graph decides equivalence, but the
`sharedResult` owns the payload and lifecycle.

### Result-to-Graph Associations

The bridge between symbolic graph and concrete payload is:

- `resultOutputEqClasses`
- `termResults`
- `resultTerms`
- `egraphResultsByDigest`

These let the cache answer questions like:

- which results are known for this output eq-class?
- which results were directly observed for this exact term?
- which results are indexed under this digest?

That distinction matters for hit selection.

## Why Terms And Results Are Separate

One output eq-class can have multiple materialized results.

For example:

- different sessions may have compatible but not identical attached resources
- a result may be imported from persistence and later re-materialized
- the cache may know several digests are equivalent before it has canonicalized
  down to one surviving payload

Because of that, lookup prefers:

1. results explicitly associated with the matching term
2. then results found by output eq-class / digest equivalence

That behavior lives mainly in:

- `appendTermSetResultsLocked`
- `firstResultForTermSetDeterministicallyAtLocked`
- `firstResultForOutputEqClassDeterministicallyAtLocked`

## How Call Identity Feeds The E-Graph

The e-graph consumes structural identity produced upstream by `ResultCall` /
`call.ID`.

The key functions are in `dagql/call/id.go`:

- `SelfDigestAndInputRefs`
- `SelfDigestAndInputs`

The shape is:

- receiver contributes as an ordered structural input
- ID-valued literals contribute as structural inputs, not self bytes
- implicit inputs contribute to self digest
- module contributes as an input, not self

That distinction is why structural equivalence works: the cache can say
"same operation over equivalent inputs" without flattening the whole call into
one undifferentiated digest.

Content-preferred digest is related but separate. In `dagql/call/id_content.go`,
it expresses "if outputs are interchangeable by content, what digest should we
prefer?" It is used as equivalence evidence, not as the authoritative recipe.

## Main Entry Points Into The E-Graph

These are the functions that really matter when reading the system.

### 1. `lookupCacheForRequest`

This is the normal lookup path for a new call.

High-level flow:

1. Derive recipe digest.
2. Derive structural self digest and structural input refs.
3. Try exact digest lookup first.
4. If that misses, do structural term lookup.
5. Filter candidates by session resource requirements.
6. Bind session ownership before returning.
7. Normalize imported payloads before the hit escapes.

The actual structural lookup is in `lookupMatchForCallLocked`.

### 2. `lookupCacheForDigests`

This is a digest-only lookup path.

It does not compute a structural term. It looks up by:

- recipe digest
- then extra digests

This is useful when the caller already has digest evidence and does not need the
full request-term path.

### 3. `indexWaitResultInEgraphLocked`

This is the main publication path for a newly completed result.

It:

- gathers request and response digests
- creates or merges output eq-classes
- ensures the result has a `sharedResultID`
- associates the result with request term and, when different, response term
- indexes request/response digests onto the result
- accumulates extra-digest evidence on the output eq-class

This is the main place where fresh execution results become lookup-visible.

### 4. `teachResultIdentityLocked`

This is the "we got a hit, now teach the graph more about what this result is"
path.

This matters because a cache hit may arrive through one route, but we still want
future requests for the new recipe shape to resolve directly.

Typical uses:

- a structurally equivalent hit should become addressable by the new request
  digest too
- new extra digests learned later should merge into the same equivalence set

### 5. `TeachCallEquivalentToResult`

This is an externalized way to say:

"this call frame is equivalent to this existing result"

It attaches the result if needed, derives the structural identity for the call,
and then teaches that identity onto the result.

### 6. `TeachContentDigest`

This is the path for late output-equivalence evidence.

It updates the result call frame with a content digest and teaches that new
digest into the e-graph without replacing recipe identity.

### 7. `AttachResult`

This is a big one conceptually even though it is not "e-graph logic" in the
narrow sense.

It takes a detached result, normalizes any pending `ResultCallRef`s, tries to
resolve it against the cache, and if necessary publishes it as a new
`sharedResult`.

This is one of the main ways semantic attachment and result-call references get
bridged into the e-graph world.

### 8. `importPersistedState`

On engine startup, persisted mirror tables are read back into memory.

This reconstructs:

- eq-classes
- digests
- results
- terms
- result/term associations
- persisted edges
- snapshot ownership links

This is how the in-memory e-graph is restored after restart.

### 9. `removeResultFromEgraphLocked`

This removes a materialized result from the graph when ownership drains to zero.

It:

- removes result-term associations
- removes digest indexes for the result
- removes terms that no longer have any live results in their output eq-class
- possibly resets the whole e-graph if nothing remains

### 10. `compactEqClassesLocked`

This is maintenance rather than hot-path lookup, but it matters.

Union-find IDs only grow. After lots of merges and pruning, the class ID space
can get sparse. Compaction rebuilds the live eq-class space to keep it smaller
and more coherent.

Today this runs after prune when needed.

## Lookup In Detail

### Exact Digest Hits Come First

`lookupMatchForDigestsLocked` does:

1. recipe digest lookup
2. if that misses, extra-digest lookup

If the request is a simple pure recipe hit:

- no extra digests
- no TTL
- not persistable

then `lookupCacheForRequestLocked` takes a fast path and skips teaching the graph
anything new. This keeps exact-hit overhead down.

The direct-hit index here is `egraphResultsByDigest`, which indexes request and
response recipe digests plus extra digests for concrete results.

### Structural Term Hits Are The Fallback

If exact digest lookup misses, `lookupMatchForCallLocked`:

1. resolves each input digest to its current eq-class root
2. aborts primary structural lookup if any input digest is still unknown
3. computes `termDigest = hash(selfDigest, canonical input eq IDs)`
4. looks up terms under that digest
5. gathers candidate results from those terms

Candidate gathering has an intentional preference order:

1. results explicitly associated with the matching term
2. if none exist, results found through the output eq-class

That means the cache prefers "we have seen this exact structural shape produce
this result" over "something equivalent exists somewhere in the same output
class."

### Session Resource Filtering

Even if a result is structurally equivalent, it may depend on session-scoped
resources the current session does not have.

`selectLookupCandidateForSessionLocked` filters candidates using:

- `requiredSessionResources` on the result
- `sessionHandlesBySession` for the current session

So the e-graph gives you semantic candidates, then session/resource filtering
chooses a result that is actually usable.

### Canonical Equivalent Selection

Some paths do not start from a fresh request lookup. They start from an already
attached result or a specific result ID and need the best reusable equivalent
for the current session.

That is what `canonicalEquivalentSharedResultLocked` does.

This matters because one output eq-class can have multiple materialized results.
The cache can legitimately canonicalize from one `sharedResult` onto another
session-compatible sibling in the same equivalence region.

## Publication In Detail

### `GetOrInitCall` / `wait`

Normal execution goes:

1. lookup miss
2. maybe singleflight via `ongoingCall`
3. underlying function runs
4. `wait()` calls `initCompletedResult`

### `initCompletedResult`

This is the center of fresh result publication.

Important work done here:

- materialize a `sharedResult`
- preserve existing attached result when the returned value is already cache-backed
- derive request and response structural terms
- collect result-call dependency refs embedded in the authoritative `ResultCall`
- index the result into the e-graph
- add exact dependency ownership edges
- recompute required session resources
- install persisted edge if needed
- take a temporary handoff ownership hold
- set up the dependency-attachment barrier
- run `attachDependencyResults`
- sync snapshot owner leases
- register lazy evaluation

That temporary handoff hold is important. It prevents publication races where a
freshly published result becomes unowned before the producing waiters or direct
attach path have claimed the real session ownership.

### Why There Is A Dependency-Attachment Barrier

The current publication order intentionally publishes before attachment is fully
complete, because semantic module object attachment needs the parent result to
already exist in the cache.

That creates a visibility race: another reader could otherwise observe the
payload while `AttachDependencyResults` is still rewriting it.

The fix is not "publish later." The fix is:

- publish
- keep an attachment barrier on `sharedResult`
- make hit-return paths wait for that barrier in
  `ensurePersistedHitValueLoaded`

That preserves the required publication order for semantic attachment while
blocking readers from seeing partially rewritten payloads.

## Congruence Repair

The most e-graph-specific logic lives in `mergeEqClassesLocked` and
`repairClassTermsLocked`.

When output digests or extra digests are merged:

1. union-find merges the eq-classes
2. any terms that mention the merged class as an input may now have different
   canonical input roots
3. those terms get repaired:
   - input roots are rewritten
   - their `termDigest` is recomputed
   - reverse indexes are updated
4. if previously distinct terms become congruent under the repaired digest, their
   output eq-classes are merged too

That last step is the actual congruence-closure behavior.

This is why the implementation is more than "union-find over digests." The term
repair step is what propagates "equivalent inputs imply equivalent outputs."

## Why Input Provenance Exists

Each term/result association stores per-input provenance:

- `result`
- `digest`

That is subtle but important. The cache wants to remember not just that a result
matched a term, but whether the match came from fully attached result-backed
inputs or from digest-only witness inputs.

Today this mostly matters for preserving more honest associations and for keeping
the door open to stricter behavior if provenance-sensitive cases matter more in
the future.

## Persistence And Imported Hits

Persisted import is not a side feature. It is a first-class part of the cache
model.

`importPersistedState` reconstructs the in-memory graph from mirrored rows.

Imported results may start life in a partially materialized state:

- `persistedEnvelope != nil`
- `hasValue == false`

That means the cache knows the result exists and can index it, but has not yet
decoded the typed payload.

`ensurePersistedHitValueLoaded` is the boundary that makes those hits safe before
they escape:

1. wait for dependency attachment barrier if present
2. if payload is already materialized, wrap and return it
3. otherwise decode the persisted envelope using the authoritative `ResultCall`
4. install the decoded payload into the shared result
5. sync snapshot owner leases
6. wrap back into the correct result shape

This function is why imported object hits no longer silently leak out as
unresolved nil payloads.

## Ownership And The E-Graph

The e-graph does not own liveness by itself.

Liveness comes from:

- session ownership edges
- persisted edges
- dependency edges between results

`incomingOwnershipCount` on `sharedResult` is the real truth for whether a result
is still live.

When that count reaches zero, the cache can collect the result and remove its
graph associations.

This is an important architectural boundary:

- e-graph answers equivalence / lookup questions
- ownership graph answers liveness / release questions

## Determinism

A lot of the implementation is deliberately deterministic.

Important examples:

- `TreeSet`s are used for term/result indexes so iteration order is stable
- candidate selection prefers the lowest `sharedResultID`
- eq-class compaction remaps roots in sorted order
- cache debug snapshots sort IDs and digests before writing

This is not just nice-to-have polish. It makes:

- tests more stable
- cache behavior easier to reason about
- debugging traces diffable
- tie-breaking predictable

## Performance Notes

### Exact Hits Are Kept Cheap

The fast path for pure recipe hits avoids extra teaching work.

### Structural Lookup Only Happens When Inputs Are Known

If any input digest has never been seen before, structural term lookup does not
try to invent anything. It records that primary lookup was not possible and
falls back to miss behavior.

### Miss Path Avoids A Second E-Graph Lookup Under `callsMu`

`GetOrInitCall` intentionally does not do a second lookup while holding
`callsMu`. That means it can occasionally re-execute work instead of catching a
very late hit, but it avoids adding more lookup cost and lock coupling on the
normal miss path.

### Congruence Repair Is Localized

Repair work is driven from `inputEqClassToTerms`, so merges only revisit terms
that actually mention the merged class as an input.

### Compaction Is Deferred

Eq-class slots are compacted after prune rather than continuously. That keeps
the hot path simpler.

### Session Filtering Happens After Candidate Discovery

The graph finds semantic candidates first. Session resource compatibility is a
second-stage filter. This keeps the graph structure global while still making
session-scoped results safe to reuse.

### Debug Snapshot Writing Is Streaming

`WriteDebugCacheSnapshot` writes JSON incrementally instead of building one huge
in-memory object. This matters because the cache can get large, and the debug
path itself should not become a second problem.

## Debugging Surface

The live engine exposes two especially useful debug endpoints:

- `/debug/dagql/egraph`
  - object snapshot of the symbolic graph
  - good for quick inspection

- `/debug/dagql/cache`
  - streamed full cache snapshot
  - includes results, result calls, digest indexes, term associations, ownership
    counts, payload state, snapshot links, and in-flight call state

The streamed cache snapshot is generally the more useful one when debugging real
cache behavior, because it shows both the graph and the payload / lifecycle side
around it.

## Things That Are Easy To Miss

### 1. This Cache Preserves Request Shape On Hits

A hit does not mean "return the canonical result call frame and forget the
request." The cache teaches new request identity onto the result so future
lookups can resolve through that route too.

### 2. Output Eq-Class Membership And Term Association Are Not The Same

If a result shares an output eq-class with a term, that does not mean it was
directly observed for that term. The implementation intentionally tracks both.

### 3. Terms Are Not The Ownership Graph

Do not confuse term edges with real retention edges. Real retention lives in
`sharedResult.deps`, persisted edges, and session ownership.

### 4. Imported Results Can Exist Before Typed Payload Decode

That is valid. It is why `ensurePersistedHitValueLoaded` exists.

### 5. Session Compatibility Is Part Of Hit Selection

Two results can be semantically equivalent and still not both be valid hits for
the same session.

## Recommended Reading Order In Code

If you are trying to load this implementation into your head, this order works
well:

1. `dagql/cache.go`
   - `Cache`
   - `sharedResult`
   - `GetOrInitCall`
   - `wait`
   - `initCompletedResult`
   - `AttachResult`

2. `dagql/cache_egraph.go`
   - eq-class and term structs
   - `lookupMatchForDigestsLocked`
   - `lookupMatchForCallLocked`
   - `lookupCacheForRequestLocked`
   - `teachResultIdentityLocked`
   - `indexWaitResultInEgraphLocked`
   - `mergeEqClassesLocked`
   - `repairClassTermsLocked`
   - `removeResultFromEgraphLocked`
   - `compactEqClassesLocked`

3. `dagql/cache_persistence_import.go`
   - `importPersistedState`
   - `ensurePersistedHitValueLoaded`

4. `dagql/call/id.go` and `dagql/call/id_content.go`
   - structural identity
   - content-preferred digest

## Short Summary

If you only remember one sentence, make it this:

The current dagql cache is a symbolic equivalence engine over call structure and
output digests, with concrete cached payloads managed separately by explicit
ownership edges, persistence state, and session/resource constraints.
