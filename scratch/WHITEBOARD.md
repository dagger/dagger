# WHITEBOARD

## Agreement

## TODO
* Make sure that storing ID of objects on so many objects like Directory/File/Container doesn't actually result in O(n^2) space
   * Requires that pointers in call.ID are shared (or something fancier)
   * Probably just make sure shallow clone is used less, only when utterly needed for id op. make stuff more immutable
* Assess changeset merge decision to always use git path (removed `conflicts.IsEmpty()` no-git fast path), with specific focus on performance impact
   * Compare runtime/cost of old no-git path vs current always-git path in no-conflict workloads
   * Confirm whether correctness/cohesion benefits outweigh any measured regression and document outcome
* Remove internal `__immutableRef` schema API once and for all
   * Replace remaining stable-ID use cases with a cleaner non-internal API pattern in dagql/core
* Review the new HTTP implementation for clarity/cohesion
   * Current implementation is functional but confusing; do a low-priority cleanup pass
* Fix `query.__schemaJSONFile` implementation to avoid embedding megabytes of file contents in query args
   * Build/write via ref/snapshot path directly instead of passing huge inline string payloads through select args
* Clean up `cloneContainerForTerminal` usage
   * Find a cleaner container-child pattern for terminal/service callsites instead of special clone helper
* replacing CurrentOpOpts CauseCtx with trace.SpanContextFromContext seems sus, needs checking
* Reassess file mutator parent-passing + lazy-init shape (`WithName`/`WithTimestamps`/`Chown`/`WithReplaced`)
   * Current implementation passes parent object results through schema into core and appears correct in tests, but may not be the most cohesive long-term model.
   * Follow-up: revisit whether lazy-init/parent snapshot modeling can eliminate this explicit parent threading while preserving correctness for service-backed files.
* Assess whether we dropped any git lazyness (especially tree) and whether we should restore it
* Assess whether we really want persistent cache for every schema json file, that's probably a lot of files that are actually kinda sizable!
* Find a way to enable pruning of filesync mirror snapshots
   * Pretty sure filesync mirrors are currently not accounted for by dagql prune/usage accounting.
* Persistence follow-up: understand what the full-state mirror should do with `Service` results.
  * A shutdown flush in the disk-persistence coverage surfaced `persist result ... encode persisted object payload: type "Service" does not implement persisted object encoding`.
  * We need to decide whether `Service` should get a real persisted representation or be explicitly excluded from mirrored persistence, and then make that behavior intentional.
* !!! Check if we are losing a lot of parallelism in places, especially seems potentially apparent in the dockerBuild of e.g. TestLoadHostContainerd, which looks hella linear and maybe slower than it used to be
   * Probably time now or in near future to do eager parallel loading of IDs in DAG during ID load and such

## Notes
* For persistence, it's basically like an export. Don't try to store in-engine numeric ids or something, it's the whole DAG persisted in single-engine-agnostic manner. When loading from persisted disk, you are importing it (including e-graph union and stuff)
  * But also for now, let's be biased towards keeping everything in memory rather than trying to do fancy page out to disk

* **CRITICAL CACHE MODEL RULE: OVERLAPPING DIGESTS MEAN EQUALITY AND FULL INTERCHANGEABILITY.**
  * If two values share any digest / end up in the same digest-equivalence set, that is not merely "evidence" or "similarity"; it means they are the same value for dagql cache purposes and may be reused interchangeably.
  * Design implication: once a lookup lands on an output eq class, any materialized result in that eq class is a valid cache hit, even if it was originally materialized under a different but equivalent term.

* Cache/e-graph design decision: once a structural lookup has identified a term / output eq class, it is acceptable to return any materialized result in that output eq class, even if that result was originally materialized under a different but equivalent term.
  * In other words, output equivalence is authoritative; we do not require cache hits to stay confined to "results originally attached to this exact term".

* A lot of eval'ing of lazy stuff is just triggered inline now; would be nice if dagql cache scheduler knew about these and could do that in parallel for ya
   * This is partially a pre-existing condition though, so not a big deal yet. But will probably make a great optimization in the near-ish future

# Persistence Redesign (Current Iteration)

* **THIS IS A NEW ITERATION OF THE PERSISTENCE DESIGN. PREVIOUS PERSISTENCE DESIGNS IN THIS FILE ARE OUTDATED AND SHOULD BE TREATED AS HISTORICAL CONTEXT ONLY.**

## Current Design Read / Problem Statement

We are very close, but there is still a deep-model smell in the relationship between:

* `call.ID`
* `term` / `eq class`
* `sharedResult`
* the `Result` / `ObjectResult` wrappers that bridge those worlds

The categories themselves seem right. The smell is that their **boundaries are still muddled**.

Right now we effectively have three coordinate systems for talking about "the same thing":

* `call.ID`
  * caller-facing absolute recipe DAG
  * specific to a caller/client/session view of the world
  * what telemetry and user-facing identities naturally want to talk about
* `term` + `eq class`
  * symbolic congruence/reuse layer
  * intentionally lossy
  * abstracts call inputs into eq classes and exists to prove cache hits / interchangeability
* `sharedResult`
  * materialized value/liveness/persistence identity
  * actual payload, refs, snapshots, cleanup, import/export, etc.

That split is fine.

What is **not** fine is that interior materialized object graphs still store too many **bridge wrappers** (`Result` / `ObjectResult`) or raw `call.ID`s, which drags caller-specific recipe identity into places where we really want internal materialized-value references.

This is the core smell:

* `Result` / `ObjectResult` are not purely public IDs
* they are not purely internal materialized refs
* they are both at once
* that is okay at boundaries
* that is **not** okay as the long-term representation of owned child edges inside materialized values

## Provisional Model We Have Converged On

The current best design direction is:

* `call.ID` remains the **public / caller-facing absolute recipe DAG**
* `term` / `eq class` remain the **symbolic cache proof layer**
* `sharedResult` remains the **materialized internal value identity**
* we likely need one additional concept:
  * a **non-lossy internal call/construction frame** for each materialized result

Candidate names for that concept:

* `resultCallFrame`
* `constructionFrame`
* `executionFrame`
* `resultRecipeFrame`

The current favorite is probably `resultCallFrame`.

### Scope of `resultCallFrame`

This needs to be explicit:

* `resultCallFrame` is **not** an execution-closure structure
* it is **not** for making results executable
* it is **not** another cache-proof structure

Current explicit decision:

* `resultCallFrame` exists **only** for presentation/reconstruction purposes
* specifically:
  * recreating caller-facing `call.ID`s on cache hits
  * recreating telemetry/span hierarchy for cached/lazy work
  * especially for the function-cache and lazy-value cases we have been talking through

So:

* terms / eq classes remain the symbolic cache-proof layer
* real payload/lazy execution state remains in the actual results/payloads
* `resultCallFrame` is just the non-lossy internal call-node structure needed to present/reconstruct public IDs later

### Important refinement: avoid a new public wrapper-type family if possible

Current preference:

* **do not** introduce a whole second public family of wrapper/result types unless we absolutely have to
* keep `Result` / `ObjectResult`
* add one new **internal** struct on `sharedResult`, most likely `resultCallFrame`
* express the semantic split through:
  * internal metadata on `sharedResult`
  * stronger invariants
  * new methods on existing wrapper types

Rationale:

* Go type proliferation here would be painful
* it would create a lot of churn and ongoing maintenance cost
* it would be annoying to teach and easy to misuse
* the deeper problem is not really "we need more wrapper types"
* the deeper problem is that `.ID()` is still being overused as if it were the one true identity in every context

So the working direction is:

* keep the existing wrapper types
* make their intended roles clearer
* add internal frame metadata
* add explicit caller-facing ID reconstruction methods instead of relying on raw `.ID()` everywhere

The key idea is:

* do **not** store the entire absolute historical `call.ID` DAG on `sharedResult`
* do **not** throw away all recipe structure either
* instead, store enough non-lossy metadata on `sharedResult` to reconstruct the **call node** that produced it
* that node should point to:
  * internal child results / owned refs
  * literals
  * other needed call metadata
* it should **not** point to one historical caller's absolute ID DAG

So the stack becomes:

1. `call.ID`
   * public absolute recipe
   * boundary type
2. `resultCallFrame` (proposed)
   * internal non-lossy construction/provenance node
   * enough to reconstruct telemetry hierarchy and public IDs when needed
3. `term` / `eq class`
   * lossy symbolic congruence / cache proof
4. `sharedResult`
   * materialized value + lifecycle + persistence identity

### Current multiplicity decision

Current explicit decision for v1:

* start with **one frame per `sharedResult`**
* do **not** prepare for multiple frames yet

Reason:

* simpler
* likely enough to get the design working
* if later we add more client/session-specific “best presentation ID” logic, we can revisit whether one frame is enough at that time

Current posture:

* keep it simple now
* re-evaluate only when we actually need more

### Frame creation / mutability rule

Current explicit decision:

* first writer wins
* once a `sharedResult` has a frame in v1, treat that frame as immutable

This keeps the one-frame-per-result model simple and avoids “best frame so far” churn.

### Current concrete shape of `resultCallFrame`

Current direction:

* use `message Call` in `dagql/call/callpbv1/call.proto` as the conceptual basis
* but make the frame distinct in one crucial way:
  * where `Call` stores digests/call-digest references, `resultCallFrame` should store **internal refs**

Current expected contents:

* scalar-ish call-node metadata stored directly:
  * `type`
  * `field`
  * `view`
  * `nth`
  * `effectIDs`
* `receiver`
  * internal result reference, not call digest
* `module`
  * likely:
    * internal result reference to the module-producing result
    * plus scalar module metadata (`name`, `ref`, `pin`) for clarity/reconstruction
* `args`
* `implicitInputs`

Important nuance:

* we do **not** think the frame should store the call node digests or other cache/e-graph digest indexes
* those digests already live in cache/e-graph state
* however, literal leaves like `DigestedString` are an exception:
  * the digest on a `DigestedString` is part of the literal's own recipe structure
  * so that digest **does** belong in the frame/literal representation

### Current concrete shape of frame args / literals

Current direction:

* do **not** use raw `call.Literal` directly for `resultCallFrame`
* instead use an internal literal tree with roughly the same shape, but with internal result refs instead of public ID/call-digest leaves

Why:

* trying too hard to reuse `call.Literal` right now would likely force the old caller-specific entanglement back in
* we want something very close to that shape, but not literally the same type

Current expected internal literal categories:

* scalar leaves by value:
  * null
  * bool
  * int
  * float
  * string
  * enum
* `DigestedString`-style leaf:
  * value
  * digest
* result-ref leaf:
  * internal result reference instead of a public `call.ID` / call digest
* recursive forms:
  * list
  * object
* for args / implicit inputs specifically:
  * keep arg name
  * keep `isSensitive`

This means the frame can represent:

* scalar args directly
* ID-like args via internal result refs
* implicit inputs using the same machinery

That is currently the preferred direction.

### Sensitive args / private-data handling in frames

Current explicit decision:

* the only truly special sensitive-arg case we care about right now is the plaintext argument to `set_secret`
* we do **not** want that plaintext to be written to disk

Current persistence rule:

* keep writing the frame/result as normal
* but for that specific sensitive scalar leaf, persist the empty string instead of the real plaintext

So the current intended behavior is:

* do not fail persistence/shutdown because of it
* do not skip persisting the entire result/frame because of it
* do not write the actual plaintext
* just scrub that specific sensitive scalar leaf to `""`

Current working theory:

* if something involving a secret can legitimately hit cache across sessions, that should only happen because another caller already produced an equivalent `set_secret` value
* so the necessary secret-bearing value should already exist in memory if such a hit is happening at all

That theory is still a little muddy, so implementation should stay empirical, but the hard persistence rule above is already settled.

For now:

* do **not** treat hidden/private fields as a separate special case here
* do **not** try to solve every current public-ID exposure quirk in this iteration
* just handle the sensitive `set_secret` plaintext case as above

### Important persistence implication of `resultCallFrame`

This new frame concept likely affects persistence in a meaningful but **not** fundamentally disruptive way.

Current expectation:

* we do **not** need to blow up the new mirror-state persistence design and start over
* we **do** likely need one important substitution in the persisted `results` row shape

Most likely substitution:

* remove `results.canonical_id`
* add persisted `resultCallFrame`

Why this is attractive:

* `canonical_id` as a full public `call.ID` DAG is likely wasteful in space
* over long chains it is prone to triangle-number / effectively quadratic growth because each stored ID contains the whole previous chain plus one more node
* persisting one non-lossy call frame per result avoids that blowup and lines up with the new conceptual model much better

Current preference:

* persist the frame on the `results` row itself
* likely as a single JSON/blob field such as:
  * `call_frame_json`
  * or similar

Why a single field is preferred for now:

* `resultCallFrame` is recursive
* its internal literal tree is recursive
* we do not currently need relational queries over frame internals
* unlike eq classes / terms / deps / snapshot links, the frame is mostly write/load/debug state
* trying to normalize every frame edge / literal leaf into many SQL tables is likely unnecessary complexity right now

So the current likely DB direction is:

* `results`
  * drop `canonical_id`
  * add `call_frame_json` (or equivalent)
* keep the rest of the mirrored schema conceptually intact

## Boundary Rule We Believe In

Very important litmus test:

* if a field means:
  * "this value owns / points to / depends on that child value as part of its materialized state"
  * then that field should **not** fundamentally be a public `call.ID`
  * and probably should not be a full `Result` / `ObjectResult` bridge wrapper either
  * it should be an internal owned/attached child reference
* if a field means:
  * "this value semantically stores a recipe/handle as data"
  * then it is okay for that field to be recipe-like
  * but it still should likely **not** be one historical caller's absolute public ID

This gives us two distinct categories that we must not conflate:

* **owned child value edge**
* **ID/recipe-valued data**

Today we still conflate them too often.

## Why This Matters For Examples

### Example: `Container.directory` / mounted directory / child selections

If a caller hits cache for a `Container` and then selects `.directory` or another child field:

* we do **not** want to resurface some old caller's absolute recipe
* we do **not** need to
* the caller-facing ID can simply be:
  * current caller's container ID
  * plus `.directory`

That is a **caller-local projection** of the current boundary path.

This is one of the strongest arguments that owned child values should not be stored as raw public IDs.

### Example: function cache returns a lazy container

This is the trickier and more important case.

Suppose a function does a bunch of container work:

* `withExec`
* `withExec`
* returns the resulting container

But the container remains lazy and is only forced later by another caller after a function-cache hit.

What telemetry should look like:

* the outer root should belong to the **current caller's function-call path**
* but the inner operation structure must still be preserved
* we do **not** want "functionCall -> giant undifferentiated blob"
* we want:
  * `functionCall`
  * inner container chain
  * `withExec`
  * `withExec`
  * etc.

This means:

* some recipe-like structure **must** be retained
* but it should not be a stale foreign caller DAG

This is one of the main motivations for the proposed `resultCallFrame`:

* the current caller owns the outer boundary
* the inner hierarchy comes from stored internal construction frames

### Example: host directories / secrets / caller-specific resources

This is where absolute stored public IDs become especially weird.

Suppose:

* a cached function result involves a host directory or secret
* a later caller hits cache through equivalence/content hashing
* now we need to present a public ID and/or do lazy work

Absolute historical caller IDs are wrong here because they may mention:

* another caller's host filesystem recipe
* another caller's secret recipe
* other caller-specific paths/details that the current caller never issued

The saving grace is:

* functions cannot freely inline-load arbitrary host dirs/secrets/etc.
* these values must flow through function args

That means the frontier is explicit, which makes **rebinding** tractable.

## Rebinding / Relative Recipe Idea

We are still slightly nervous about a very abstract "rebindable ID" concept, but the current thinking is:

* the right opposite of "stored absolute historical `call.ID`" is **not always** "no recipe at all"
* sometimes the right opposite is:
  * a **relative/rebindable recipe expression**
  * grounded on internal owned results / inputs
  * instantiated/projected for the current caller when needed

This is especially relevant for:

* ID-valued fields on user module objects
* function-cache values that need to later surface IDs or reconstruct inner telemetry

The current best concrete framing is:

* `sharedResult` stores a non-lossy `resultCallFrame`
* that frame points to internal owned child refs / literals, not one public DAG
* when we need a public `call.ID`, we reconstruct it:
  * starting from the current caller boundary
  * recursively expanding the internal call frames
  * rebinding frontier nodes to current caller-local equivalent IDs where possible

This is very similar to:

* closures / closure conversion
* expression DAGs
* "free variables" being rebound at instantiation time

We suspect this is the right mental model.

### Important refinement: for now, stage caller/session-specific “best” IDs

One real concern is that in the ideal world we would like to present the **best caller-local ID** to a given caller.

Example:

* multiple callers may each load equivalent host directories with the same content digest
* in the most ideal world, if caller B later gets a cached value, we would like to surface the host-directory-shaped ID that is most specific to caller B, not some arbitrary equivalent ID from caller A

We think this concern is real.

Current staged decision:

* **do not** add cache-wide client/session-specific recipe indexes in the first cut
* first build the design around:
  * current boundary/root caller IDs
  * `resultCallFrame`
  * internal result-graph rebinding
* if that proves insufficient, later add a more caller/session-specific preference layer

Why stage it:

* the core structural/design win comes from `resultCallFrame` and internal refs
* in many important cases, especially function-cache hits, the current caller's root/boundary ID already gives us the right frontier bindings
* jumping straight to cache-wide client/session indexing is a lot of extra machinery at a different level of the design

So current plan:

* v1 should use the best currently-available boundary/frontier bindings
* future refinement can prefer more client/session-local equivalents if we find that necessary

This staging decision also applies to persistence:

* we are **not** currently planning to persist caller/session-specific preferred IDs
* persisted state should persist the internal frame/ref structure
* caller/session-specific “best presentation” remains a possible future refinement on top of that

## Proposed / Suggested Types

These names are **not final**, but they are useful handles for discussion:

* `resultCallFrame`
  * likely stored on `sharedResult`
  * enough metadata to reconstruct the call node that created the result
  * would likely include things like:
    * field
    * view
    * module info
    * nth
    * args / implicit inputs
    * receiver edge
    * result/literal-valued inputs
  * result-valued edges should point to internal refs, not absolute public IDs

* `OwnedObjectRef[T]`
* `AttachedObjectResult[T]`
* `OwnedResultRef[T]`
  * possible future interior type for owned attached child values
  * narrower than a full boundary `ObjectResult`
  * may just be a light wrapper around a cache-backed object result, but semantically much more constrained

* `StoredIDExpr`
* `RelativeIDExpr`
* `RebindableID`
  * possible future type for ID/recipe-valued data fields
  * if a field semantically stores an ID, it should probably not store an absolute historical public ID
  * instead it likely wants a relative/rebindable expression built on the same underlying machinery as `resultCallFrame`

Again: these are **discussion names**, not final API decisions.

### Narrow exceptional policy for raw `call.ID` data

Current explicit decision:

* yes, there may be a **very narrow** exceptional raw-`call.ID` data case for now
* but it is explicitly not the main model

Important caution:

* this exception does **not** need to be taken far
* if implementation of that narrow exceptional case starts causing real complication, that is a tripwire
* at that point we should stop and reassess rather than bending the design around it

### Shared internal expression / literal representation

Current explicit decision:

* yes, we want one shared internal expression/literal representation

That representation should be reused for:

* `resultCallFrame` args / implicit inputs
* recipe-ish / ID-ish semantic data that needs the same rebinding machinery

We do **not** want two separate, almost-the-same recursive trees for those purposes.

### Current type preference

Current preferred shape:

* keep `Result` / `ObjectResult` as the boundary adapters
* add `resultCallFrame` (or similar) internally on `sharedResult`
* avoid inventing parallel public wrapper types like `OwnedObjectRef` unless we later prove they are truly necessary

So:

* possible "owned ref" names above are useful discussion handles
* but they are **not** the preferred first implementation direction right now

## Current preferred caller-facing ID API

If we keep the existing wrapper types, then the important change is not a new type family.
The important change is a new method family.

Current favorite naming:

* `IDForCaller(...)`

This name is preferred because it is:

* simple
* descriptive
* less abstract than `ReboundID(...)`

Possible uses:

* telemetry / span reconstruction
* surfacing ID-valued fields from cached/persisted results
* rebuilding caller-facing IDs for lazy cached values
* any path where raw historical `.ID()` would be wrong or misleading

Working interpretation:

* `.ID()` remains the currently attached/public handle on the wrapper
* `IDForCaller(...)` becomes the deliberate API for:
  * "give me the correct caller-facing ID in this current context"

This is a key part of avoiding hidden ambiguity while still keeping the existing wrapper types.

### `IDForCaller(...)` lives on existing wrappers

Current understanding:

* yes, `IDForCaller(...)` should be a method on `Result` / `ObjectResult`
* that is where the current caller/boundary-facing `.ID()` already lives
* that wrapper is the natural place to seed reconstruction of caller-facing IDs

Working shape:

* public API:
  * `IDForCaller(ctx context.Context) (*call.ID, error)`
* internal helper(s):
  * likely some helper that works from:
    * `sharedResult`
    * `resultCallFrame`
    * a caller-local frontier map

The public wrapper method is preferred because:

* it can start from the wrapper's currently valid public `.ID()`
* it avoids introducing yet another external helper abstraction just to ask the question

### `IDForCaller(...)` fallback behavior

Current explicit decision:

* if `IDForCaller(...)` cannot do the ideal reconstruction/rebinding
* the fallback should be **only**:
  * the raw existing `.ID()`

We do **not** want layered clever fallback behavior.

Specifically:

* do not invent a pile of alternative “best available” fallback heuristics
* do not search wider cache history
* do not try to get fancy

Reason:

* complex fallback logic becomes quicksand fast
* the point of the new design is to make the primary path correct and understandable
* if we need richer fallback later, we can revisit it deliberately

So current rule:

* ideal path: reconstruct via frame + frontier + current boundary
* fallback: raw `.ID()`

This is intentionally the only fallback.

### What “frontier” means right now

The word “frontier” is being used in a very concrete sense.

Current meaning:

* a map from internal `sharedResultID` to already-known caller-facing `*call.ID`
* in the **current** reconstruction context

In other words:

* frontier is not some global cache-wide structure
* frontier is not the full set of all IDs any caller has ever seen
* frontier is just:
  * "which internal results do we already know the right caller-facing public ID for in this current boundary context?"

Current simplest seed:

* if a wrapper already has:
  * attached `sharedResult.id = X`
  * current caller-facing `.ID() = Y`
* then `IDForCaller(...)` can begin with:
  * `frontier[X] = Y`

Then, as reconstruction walks `resultCallFrame`s and projections, it can extend the frontier with new caller-local bindings.

This is one of the main reasons we think we may get quite far **without** client/session indexes in the first cut.

### Expected reconstruction behavior

Current expected reconstruction model:

* `IDForCaller(ctx)` starts from the wrapper's already-known public boundary `.ID()`
* that seeds the initial frontier
* `sharedResult.resultCallFrame` provides the non-lossy internal node structure
* reconstruction walks those frames and:
  * projects caller-local child IDs where possible
  * reuses existing frontier bindings when known
  * recursively reconstructs nested IDs only when needed

This means:

* not every field selection needs deep reconstruction
* simple projected child selections like `.directory` can remain very cheap
* harder lazy / cached / ID-valued cases can use the same machinery when necessary

## Important Distinction About `Result` / `ObjectResult`

Current thinking:

* `Result` / `ObjectResult` are still good as **boundary adapters**
* they bridge:
  * public caller-facing `call.ID`
  * materialized cache-backed value when present
* but they should not be the long-term representation of owned child edges inside materialized values

Restated bluntly:

* boundary wrappers are fine at boundaries
* letting them escape deep into interior object graphs is probably the last major entanglement left

### Refinement to that statement

We are **not** currently planning to ban storing `Result` / `ObjectResult` on core structs outright.

Instead, the more precise rule is:

* it is okay to keep storing `Result` / `ObjectResult`
* but the stored wrapper must obey stronger invariants
* and code must stop assuming raw `.ID()` is always the right thing to use

That lets us avoid a giant public type explosion while still fixing the model.

## Specific Design Concern Corrected During Discussion

One earlier formulation was too sloppy:

* saying that lazy hidden work from a cached function result should "belong to the current caller's path" is only partially true

The refined position is:

* the **outer boundary** should belong to the current caller's path
* but the **inner operation hierarchy** still needs to be shown, preserved, and attributable as nested spans/ops
* therefore we need enough retained internal call structure to expand that hierarchy later

So:

* not "reuse the stale historical absolute caller DAG"
* not "throw away all inner recipe structure"
* instead:
  * keep internal non-lossy call frames
  * project them under the current caller boundary when needed

This is one of the strongest reasons we think `resultCallFrame` is necessary even if we keep the existing wrapper types.

Another way to say it:

* the current caller should own the **outer boundary**
* the stored `resultCallFrame`s should supply the **inner operation hierarchy**
* rebinding should project that internal hierarchy under the current caller's boundary/root IDs

This is the exact intended use for function-cache hits that later force lazy work:

* outer boundary comes from the current caller
* inner `withExec` / etc. hierarchy comes from stored call frames

## Concrete Open Questions For Next Iterations

These are not settled yet:

* What exact fields belong on `resultCallFrame`?
* Is `resultCallFrame` one per `sharedResult`, or do we need to tolerate multiple equivalent frames?
* How do we want to represent the receiver/arg edges:
  * direct internal result refs
  * integer result IDs
  * lightweight owned-ref wrappers
* How should synthetic/promoted results participate?
  * nth/deref promotions
  * imported/lazy-decoded results
  * other synthetic construction paths
* For ID-valued semantic fields:
  * when should they be modeled as owned values instead?
  * when do they truly need a relative/rebindable ID expression?
* How much of the current `Result` / `ObjectResult` type survives as-is once we introduce a narrower interior owned-ref type?

## Representative Examples To Keep Reusing

We should keep testing the design against these examples:

* cached `Container` child selection like `.directory`
* function cache returning a lazy container that later forces hidden `withExec` work
* host-directory / secret-like function args that must rebind to the current caller's equivalent frontier values
* module object fields that semantically store IDs / recipe-like data

These examples are the best way to tell if the next iteration is actually simpler and more correct, or if it just sounds nice in the abstract.

### Additional concrete reading of those examples

#### Cached container child selection

This remains the easy/clean case.

If the caller has a cached `Container` and selects `.directory`:

* we should not reuse some stale historical child ID
* the caller-facing ID can simply be:
  * current container boundary ID
  * projected with `.directory`

This is one of the strongest arguments for:

* not storing historical public child IDs on the object graph
* and letting caller-facing IDs be projected from the current boundary where possible

#### Function cache returning a lazy container

Current refined reading:

* the caller gets a function-cache hit
* the caller-facing outer root should be the current caller's function-call path
* if later forcing the lazy container triggers hidden inner `withExec` work:
  * telemetry must still show the real inner hierarchy
  * not just a generic blob under the function call

This is the strongest example motivating `resultCallFrame`:

* the current caller supplies the outer boundary/root
* the stored call frames supply the inner structure

#### ID-valued data on module objects

Current refinement:

* for fields that semantically store IDs, we do **not** currently think the right answer is:
  * “only store a naked frame”
  * or “store one historical public `call.ID`”
* the current likely right answer is:
  * store an internal result reference
  * which in practice probably means the internal `sharedResultID`

Why this is attractive:

* that internal result already has:
  * the materialized/shared result state
  * the `resultCallFrame`
  * the lifecycle/persistence guarantees that it remains available
* so later, when we need to surface that field as a public ID:
  * we can load the result by internal ref
  * use its frame
  * reconstruct the caller-facing ID through `IDForCaller(...)`

This is currently the preferred direction for module-object ID-valued data.

Important nuance:

* we do **not** currently think that storing only a naked `resultCallFrame` for such fields is enough
* the better representation is likely:
  * the internal result reference
* because once we have the result ref, we already have:
  * the frame
  * the materialized/liveness state
  * the persistence/import guarantees for that result

So the likely principle is:

* if ID-valued semantic data corresponds to a real cached result, store the internal result ref
* then use that result's frame plus the current frontier to reconstruct the public caller-facing ID

Current additional explicit decision:

* if a module-object field stores an internal result ref like this
* that target must stay live as long as the parent object/result is valid

In other words:

* for module-object stored result refs, this is **not** an optional retention edge
* we do **not** want callers later selecting a field and discovering that the referred-to result was pruned out from under the object

So for this class of fields:

* internal result ref
* plus explicit dep/liveness retention semantics

This is an important clarification from the more general “some semantic stored refs may imply deps” discussion:

* for module-object stored result refs specifically, we currently believe the answer is **yes, retain them**

### Additional clarification: this should be the in-memory model too

Important nuance:

* this should **not** be only a persistence-time transformation
* it should become the in-memory model as well

In other words:

* if a module-object field semantically stores an object/value reference
* then in memory that field should move away from absolute public `call.ID`
* and toward the internal result reference model

Reason:

* if we only convert the representation during persistence encode, then persistence gets cleaner
* but the in-memory model still carries the same conceptual smell
* and runtime `IDForCaller(...)`, telemetry, liveness, and cache-hit behavior still have to paper over that smell

So current hard-cut preference:

* change the in-memory model first
* then persistence naturally serializes the new internal representation

### Current concrete implication for `ModuleObject`

Looking at the current shape in `core/object.go`, persisted module object values already distinguish:

* `result_id`
* `call_id`
* scalar / array / object forms

Current design conclusion:

* `result_id` is the direction
* `call_id` is the lingering smell

So the current likely hard cut is:

* for module object values that semantically represent dagql object/value references:
  * normalize them to internal result refs in memory
  * persist them as result refs / result IDs
* do **not** continue treating historical public `call.ID` blobs as the main representation for those fields

This means the old `call_id` path should likely disappear or become a very narrow exceptional case later.

### Current downsides / costs we accept

This change does impose stronger invariants, but we currently think those are the right invariants:

1. A module-object field that semantically stores an object/value reference must correspond to a real attached/cache-backed result.
2. That target result must remain live as long as the parent module object remains valid.
3. We lose the convenience of treating public `call.ID` as casual opaque data in those fields.

Current read:

* these are good costs to pay
* they make the model more honest and prevent a lot of later weirdness

### One remaining nuance

There may still be some truly private/internal fields that stash arbitrary `call.ID` values as opaque implementation detail rather than semantic object references.

Current stance:

* do **not** let those rare cases define the main model
* for user-visible module-object fields and function-cache-visible values, the correct direction is internal result refs plus retention

## Discipline Rules If We Keep Existing Wrapper Types

If we keep `Result` / `ObjectResult` instead of introducing new wrapper families, we need to be disciplined or the old confusion will just stay hidden.

Current rules to enforce:

1. If a core struct stores a `Result` / `ObjectResult` as an owned child, that result must be attached/cache-backed.
   * No more "maybe detached, maybe historical wrapper, we will normalize it later" as the default model.

2. Raw `.ID()` must not be the default for rebinding-sensitive or caller-facing reconstruction paths.
   * If code is doing telemetry reconstruction, surfacing cached ID-valued fields, rebuilding lazy inner work hierarchy, or otherwise presenting an ID to a caller, it should be assumed that raw `.ID()` is probably wrong until proven otherwise.

3. There must be an explicit API for caller-facing reconstruction.
   * Current preferred name: `IDForCaller(...)`
   * The existence of that explicit method is what keeps us from silently overloading `.ID()` forever.

These rules are important enough that if we violate them casually, we are likely just reintroducing the same design smell in a subtler form.

4. Any cache-backed result that can cross a public/cache boundary should eventually have a frame.
   * This includes promoted/synthetic cache-backed results.
   * We do not want `IDForCaller(...)` and telemetry to become a giant special-case maze.

Current reading of some synthetic cases:

* nth promotion
  * straightforward: derive/update the frame from the nth selection
* deref promotion
  * same basic idea: derive/update the frame from the deref/select path that produced the promoted result
* imported lazy-decoded results
  * should import their frame from persistence
* SDK-scoped / synthetic module-ish values
  * current leaning: if they are cache-backed and can surface across a public boundary, they should also get a real frame derived from the operation that made them visible
  * keep this simple and consistent rather than making a special “some cache-backed results just have no frame” exception model

## SDK-scoped and synthetic/module-ish values

Current explicit design direction:

* SDK-scoped and other synthetic/module-ish cache-backed values should get **real `resultCallFrame`s**
* the scoping/promotion/normalization transformation that created them should itself be modeled as the frame node
* we should **not** keep treating them as:
  * the same old value
  * plus magical ID surgery on the side

This is a very important simplification.

### Why this is the right direction

Today values like the ones created through SDK scoping in `core/sdk/utils.go` are already conceptually operations:

* scope module source for SDK operation
* scope module for SDK operation
* carry forward source-content scoping / extra-digest scoping / operation-specific identity tweaks

Those are already transformations.
The design should admit that explicitly.

Why this helps:

* telemetry has a real operation hierarchy to show
* `IDForCaller(...)` has a real frame node to reconstruct from
* persistence/import has a real internal provenance node to store/load
* we avoid continuing the “special-case magical ID rewrite” model

### Current intended rule

If a value is:

* cache-backed
* and can cross a public/cache boundary

then it should have a real frame.

This applies even if the value exists only because of:

* SDK scoping
* module scoping
* nth promotion
* deref promotion
* other synthetic transformations

In other words:

* boundary-visible synthetic values are still real results
* therefore they deserve real frames

### Proposed synthetic frame-node categories

Current conceptual categories:

* normal schema field selection
* nth projection
* deref projection
* internal scoped-ID transformation
* possibly a small number of other imported/synthetic reconstruction nodes if truly needed later

We do **not** need to finalize an enum or type taxonomy yet, but conceptually these are the kinds of frame nodes we are expecting.

### Source/content scoping node

For things like:

* `scopeSourceForSDKOperation(...)`
* source-content scoped IDs
* source-content based synthetic source/module-ish values

the frame should explicitly express:

* scope label / operation name
* scope digest or equivalent scalar scope metadata
* resulting type

Meaning:

* “this value is the SDK-scoped/source-content-scoped projection produced for operation X”

This is much more honest than pretending it is just the original raw recipe.

### Module scoping node

For things like:

* `ScopeModuleForSDKOperation(...)`

the frame should explicitly express:

* scope label / operation name
* source-content scope digest or equivalent scalar scope metadata
* resulting type / module metadata

Meaning:

* “this module result is the scoped projection produced for operation X”

Again, the key point is:

* explicit scoping frame node
* not magical rewritten public ID

Important refinement:

* scoped/synthetic frame nodes do **not** need to force-attach a base result just to carry a backlink
* if a base internal result ref is already naturally available, it is fine to record it
* but it is **not** a requirement for the node to be valid

Concrete example:

* `scopeSourceForSDKOperation(...)`
  * starts from an attached `ObjectResult[*ModuleSource]`
  * but we still do **not** need to record a base-result backlink just because one happens to be available
  * the honest v1 node is just the synthetic scoping operation plus its scalar metadata
* `ScopeModuleForSDKOperation(...)`
  * starts from `*Module` plus `mod.ResultID`
  * the important cache identity is the new `scopedID`
  * the old `mod.ResultID` is primarily an input to deriving that `scopedID`
  * therefore we do **not** want to force-attach the original module just to populate a base-result ref on the scoped frame

So the v1 rule is:

* synthetic/scoped frame nodes must honestly describe the synthetic operation that produced the result
* they may optionally include a base internal result ref when that ref is already naturally available
* they should **not** introduce extra attachment behavior solely to satisfy frame structure

### Promotion/projection nodes

These remain the more straightforward synthetic cases:

* nth promotion
  * parent/base result ref
  * nth index
  * resulting type
* deref promotion
  * parent/base result ref
  * deref/projection node kind
  * resulting type

Current read:

* these are easy to frame explicitly
* this is one reason we are confident the broader synthetic-frame direction is sound

### We should not lie about synthetic nodes

Current preference:

* synthetic/internal frame nodes should not pretend to be ordinary schema-visible field names if they are not

So if a node is really:

* an internal scoping transformation

then we would rather represent it honestly as that transformation than try to disguise it as an ordinary public field.

This is an **internal frame**, not a public schema contract.
Honesty matters more than prettiness here.

### How `IDForCaller(...)` should treat synthetic nodes

Current conceptual rule:

* if the caller actually performed a public operation corresponding to the node, use that caller-local path directly
* if the node is purely internal/synthetic, reconstruct it as an internal call-like node under the current caller boundary/root

This is one of the main reasons explicit synthetic frame nodes are preferred:

* they let us say what actually happened
* without having to reuse a stale historical absolute ID

### Are scoped/synthetic values distinct results?

Current leaning:

* yes, if they are cache-backed and boundary-visible, treat them as distinct cache-backed results in their own right
* but let their frame explicitly reference the base result they were derived from

That gives us the best of both worlds:

* clear identity and lifecycle as a real result
* explicit provenance back to the base result

So a scoped module result is currently expected to look like:

* real `sharedResult`
* with its own frame
* whose frame node says:
  * this result came from scoping that base module for that operation with that scope metadata

This is far cleaner than treating the scoped value as “the same thing but with an ID tweak”.

### Why this matters

Without this design, we keep generating the same family of questions:

* what ID should the caller see?
* what should telemetry show?
* is this a real value or just an alias?
* do we copy the old ID or derive a new one?

With explicit synthetic frame nodes, these all reduce to one simpler question:

* what frame node produced this result?

That is the more cohesive model.

## Persistence / Import Implications of the Current Design

Current working belief:

* this design does **not** require a fresh persistence redesign
* it **does** require a meaningful update to the results-row representation

### Likely schema changes

Current expected changes:

* **remove**
  * `results.canonical_id`
* **add**
  * `results.call_frame_json` (or equivalent)

Current expectation for the rest of the mirror-state schema:

* keep:
  * `eq_classes`
  * `eq_class_digests`
  * `terms`
  * `term_inputs`
  * `result_output_eq_classes`
  * `result_deps`
  * `result_snapshot_links`

Why those still stay:

* `resultCallFrame`
  * provenance / caller-facing ID reconstruction / telemetry structure
* `term` / `eq class`
  * symbolic cache proof / congruence
* `result_deps`
  * explicit lifetime/ownership edges
* `snapshot_links`
  * external reopen/bootstrap state

These are still distinct roles and should remain distinct in the persisted model.

### Why the frame likely belongs as a single encoded field

Current preference is to encode the frame as a single structured field, not normalize it heavily in SQL.

Reasons:

* the frame is recursive
* the frame-literal tree is recursive
* we do not need to relationally query inside it right now
* it is mostly:
  * persisted
  * imported
  * debugged
  * used in-memory for reconstruction

So JSON/blob is currently the preferred first implementation direction.

### Import strategy

Current expectation:

* import should be **multi-phase**, but it does **not** need a topological sort

Likely shape:

1. Load all result rows first.
   * create `sharedResult` shells
   * assign IDs
   * assign payload/envelope metadata
   * assign basic prune/lifetime metadata
   * do **not** fully wire frame refs yet

2. Load and rebuild the symbolic and lifetime graph as we already do:
   * eq classes
   * eq class digests
   * terms
   * term inputs
   * result output eq classes
   * explicit deps
   * snapshot links

3. Decode and attach `resultCallFrame`s.
   * by this point all `sharedResult`s already exist by ID
   * frame refs can resolve by result ID against those already-existing shells
   * forward references are okay because the target shell already exists

This is why we currently believe no topo sort is needed:

* shell creation first
* frame wiring later

You only need topo ordering if creation of one node requires the *fully constructed* target node to exist first. That is not what we currently expect for frames.

### Hard-cut / no-legacy import rule

Current explicit decision:

* do **not** design for importing legacy cached values that have no frame
* do **not** add backward compatibility for older persistence state here

This remains a hard cutover.

Important reminder:

* none of this persistence redesign is merged to main yet
* any older persistence state is effectively dev/ephemeral history
* we do not need to carry that forward

So the rule is:

* old persisted state without frames is invalid
* schema/version mismatch or equivalent should wipe it
* we do **not** spend design effort on frame-less legacy compatibility

### Additional persistence simplification likely to follow

If module-object ID-valued data and similar fields move to storing internal result refs, persisted object payloads may get simpler too.

That would mean:

* internal graph edges persist as result IDs
* symbolic cache state persists as terms / eq classes
* public caller-facing IDs are reconstructed, not stored verbatim

This is a very good sign for the design.

## Pruning Implications

Current position:

* pruning does **not** seem fundamentally impacted yet
* we should leave pruning logic alone for now unless a specific case forces a redesign

Important caveat:

* `resultCallFrame` is **not** automatically a pruning/liveness graph
* a frame pointing at another result does **not** by itself mean prune should retain that child forever

Current role split should remain:

* `resultCallFrame`
  * provenance / reconstruction / telemetry
* `result_deps`
  * explicit lifetime/ownership edges
* structural proof inputs / term provenance
  * symbolic proof-driven retention behavior where already intended
* snapshot links
  * reopen/bootstrap state

So the current rule is:

* frame refs do **not** imply new pruning semantics by default

However:

* if we convert some semantic ID-valued data field to store an internal result ref
* and that field must remain valid as long as the parent remains valid
* then that relationship may need to become an explicit dep / retention edge

So the staged plan is:

* leave pruning alone for now
* when converting specific semantic ID-valued fields, evaluate whether each one must also produce explicit dep/lifetime edges

This keeps the design understandable and avoids accidentally turning the frame itself into a second liveness graph.

Important clarification:

* for module-object stored result refs, we currently believe that evaluation already comes out clearly:
  * yes, they should retain their targets

So the remaining “evaluate case by case” guidance mainly applies to other future semantic stored-ref uses, not that module-object case.

## Current best summary of the likely next implementation direction

If the design keeps feeling right after a few more iterations, the likely implementation shape is:

* keep `Result` / `ObjectResult`
* add `resultCallFrame` to `sharedResult`
* model frame args/implicit inputs with an internal literal tree
* let result-valued leaves point to internal result refs
* add `IDForCaller(ctx)` to existing wrappers
* seed reconstruction from the wrapper's current public `.ID()`
* use a caller-local frontier map during reconstruction
* stage caller/session-specific best-ID preference for later if needed
* represent module-object ID-valued data via internal result refs rather than absolute stored public IDs

That is the current most coherent version of the model we have reached so far.

## Telemetry integration note

Current intended integration point:

* the place where engine/client telemetry is bridged today is `core/telemetry.go` via the around-func path
* that around-func currently accepts a `call.ID`

Current likely implementation direction:

* any path that currently invokes the server's telemetry/around-func callback should first compute `IDForCaller(...)`
* then pass that resulting caller-facing ID into the existing around-func boundary

Why this is attractive:

* it keeps telemetry’s outer interface largely unchanged
* it localizes the new reconstruction logic at the callsites that already have the wrapper/result context
* telemetry can remain mostly “none the wiser” and keep operating on a `call.ID`

So current expectation is:

* reconstruction happens before crossing into the around-func telemetry boundary
* not inside telemetry itself

## First Draft Phased Implementation Plan

This is the first concrete implementation plan derived from the design above.

Goals for the plan:

* split the work into a small number of large-but-reviewable phases
* keep each phase conceptually coherent
* avoid “halfway” migrations where old and new models are both half-alive
* call out the exact code seams that are expected to move
* call out the tripwires that should stop implementation rather than inviting improvisation

Current proposed shape:

* **Phase 1:** add the `resultCallFrame` substrate and populate it for cache-backed results **(completed)**
* **Phase 2:** add `IDForCaller(...)` and move runtime caller-facing reconstruction / synthetic-node creation onto frames
* **Phase 3:** hard-cut module-object and other semantic ID-valued data to internal result refs
* **Phase 4:** hard-cut persistence/import from `canonical_id` to persisted `resultCallFrame`

That is intentionally only four phases. Each one is still substantial, but each one should also have a clear review boundary.

### Phase 1: Add `resultCallFrame` as a real `sharedResult` substrate

Status:

* completed

Purpose:

* introduce the internal frame model without yet changing persistence or caller-facing behavior
* make every newly cache-backed result capable of carrying a frame
* establish the one-frame-per-result, first-writer-wins invariant in code

Primary files / seams:

* `dagql/cache.go`
  * `sharedResult` struct needs a new `resultCallFrame` field
  * detached-payload clone paths like `withDetachedPayload()` need to preserve/copy frame state appropriately
  * `attachResult(...)` and result materialization paths need to enforce “cache-backed results should end up with a frame”
* `dagql/cache_egraph.go`
  * any place that creates, imports, or deletes cache-backed results needs to preserve/reset frame state correctly
  * this includes the same lifecycle cluster that currently manages `sharedResult.id` and `resultCanonicalIDs`
* `dagql/server.go`
  * `loadNthValue(...)`
  * `promoteDerivedObjectResult(...)`
  * `toSelectable(...)`
  * these are important because they create/promote detached/synthetic results that later become cache-backed
* `dagql/nullables.go`
  * deref promotion paths currently build detached results from `call.ID`
  * they need a frame story when those results later become cache-backed
* `core/sdk/utils.go`
  * `scopeSourceForSDKOperation(...)`
  * `ScopeModuleForSDKOperation(...)`
  * these are the clearest current synthetic/scoped result constructors

New code expected:

* a new internal frame representation, probably in a new dagql-local file such as `dagql/result_call_frame.go`
* likely types:
  * `resultCallFrame`
  * `resultCallFrameArg`
  * `resultCallFrameLiteral`
  * `resultFrameRef`
  * a small explicit node-kind concept for synthetic/internal nodes
* frame-literal support for:
  * scalar values
  * `DigestedString`
  * list/object recursion
  * internal result refs instead of public `call.ID`

Expected implementation strategy for v1:

* build the first frame from the result’s currently available `call.ID`
* that means:
  * use current IDs as the source of truth for deriving frame node metadata
  * convert result-valued inputs into internal result refs during capture
  * keep scalar/literal structure by value
* this is acceptable as a transitional implementation strategy because the frame becomes the durable internal representation, not the old full public ID

Important explicit invariant to encode in this phase:

* once a cache-backed `sharedResult` gets a frame, it is immutable in v1
* first writer wins

Important synthetic-node work in this phase:

* do **not** leave synthetic/scoped/promotion results as “same old value but with magical ID surgery”
* give them real explicit frame nodes:
  * nth projection
  * deref projection
  * SDK source-content scoping
  * SDK module scoping
  * module/source content-scoped synthetic nodes such as `_sourceContentScoped`

Refinement for SDK/module scoping in this phase:

* do **not** force-attach an original/base module result solely so that a scoped synthetic frame can point back to it
* for v1, the scoped frame node only needs to honestly represent the synthetic scoping operation and its scalar metadata
* if a natural base internal ref is already available, it is fine to include it, but it is not required

Phase-1 test focus:

* unit tests in `dagql/cache_test.go` for:
  * frame creation on normal cache-backed results
  * first-writer-wins immutability
  * nth promotion frame creation
  * deref promotion frame creation
  * at least one SDK-scoped or source-content-scoped synthetic result getting an explicit synthetic frame node

Phase-1 review boundary:

* after this phase, cache-backed results should have frames
* but no caller-facing behavior, telemetry behavior, or persistence schema should have changed yet

Phase-1 tripwire:

* if deriving a frame from the current `call.ID` cannot reliably resolve result-valued inputs to internal result refs for cache-backed results, stop and escalate
* that would mean the current result-creation path is still missing information we assumed would be available
* however, this tripwire does **not** apply to synthetic/scoped nodes whose honest v1 representation does not actually require a base internal result ref
  * SDK/module scoping is the concrete example here

### Phase 2: Add `IDForCaller(...)` and move runtime presentation onto frames *(completed)*

Purpose:

* make frames actually useful at runtime
* stop relying on raw historical `.ID()` for presentation-sensitive behavior
* move synthetic/scoped/promotion call reconstruction onto the new model

Primary files / seams:

* `dagql/cache.go`
  * `Result[T]` / `ObjectResult[T]` get `IDForCaller(ctx)` on the existing wrapper types
  * the raw `.ID()` method remains, but becomes the explicit fallback only
* the new dagql frame file (from Phase 1)
  * add the reconstruction algorithm
  * add the frontier map logic
  * add the explicit fallback to raw `.ID()`
* `core/telemetry.go`
  * this file should remain mostly unaware of the internal redesign
  * the important change is upstream: callers should supply `IDForCaller(...)` before crossing the around-func boundary
* `dagql/server.go`
* `dagql/objects.go`
  * these are the two current wiring points for the around-func / telemetry callback
  * both need to be updated so that user-facing telemetry is rooted in `IDForCaller(...)`, not raw `.ID()`
* `core/container.go`
  * child selection synthesis paths around `UpdatedRootFS(...)` / related helpers no longer need a stored `OpID`
  * the current preferred direction is to use `dagql.CurrentID(ctx)` directly at the `Updated*` use site
  * add an explicit warning comment there that this relies on those helpers never being invoked from a later lazy callback
* `core/schema/container.go`
* `core/schema/host.go`
* `core/builtincontainer.go`
* `core/modfunc.go`
  * these were the main places that used to set or carry `Container.OpID`
  * the new hard cut is to stop storing `OpID` on `Container` entirely and rely on `dagql.CurrentID(ctx)` at the child-selection synthesis site
* `core/sdk/utils.go`
  * once frames exist, SDK scoping should no longer be treated as just ID surgery
  * this phase is where runtime caller-facing reconstruction for those scoped values becomes meaningful
* `core/schema/module.go`
* `core/schema/modulesource.go`
  * `_sourceContentScoped` and related module/source synthetic values need to be represented honestly through frames

What `IDForCaller(...)` should concretely do in this phase:

* live on existing `Result` / `ObjectResult`
* seed reconstruction from the wrapper’s current public `.ID()`
* maintain a caller-local `frontier map[sharedResultID]*call.ID`
* walk `resultCallFrame`s recursively only when needed
* use raw `.ID()` as the only fallback

Important explicit behavior for this phase:

* we are **not** doing cache-wide client/session-specific best-ID selection yet
* v1 uses the current wrapper/boundary binding plus the frame graph
* if that is insufficient, later phases can refine the selection policy

Phase-2 test focus:

* direct unit tests for `IDForCaller(...)`
  * simple projected child selection
  * nth / deref cases
  * synthetic scope node cases
* telemetry-focused tests where the around-func path sees frame-derived IDs rather than raw historical IDs
* targeted reproductions for:
  * cached container child selection
  * function-cache returns lazy container, then later forcing shows inner op structure under the current caller boundary

Phase-2 review boundary:

* after this phase, runtime caller-facing reconstruction should be frame-based in the selected high-signal paths
* persistence should still still be using the old `canonical_id` storage at this point

Phase-2 progress so far:

* `Result` / `ObjectResult` now have `IDForCaller(ctx)`
* `resultCallFrame` can now be turned back into caller-facing `call.ID`s using:
  * caller-local frontier seeding from the wrapper's current raw ID
  * recursive frame/literal reconstruction
  * reattaching all known labeled extra digests from the result's output eq class via `eqClassExtraDigests`
  * raw `.ID()` fallback when reconstruction is unavailable
* the cache-hit telemetry callback shape now accepts the hit result at begin-time, which lets the `LoadType(...)` cache-hit path pass `callerFacingID(...)` instead of blindly using the raw historical ID
* `ObjectResult.preselect(...)`, `Server.loadNthValue(...)`, and `Server.promoteDerivedObjectResult(...)` now use caller-facing IDs instead of raw stored IDs
* `Container.OpID` has been removed
  * `UpdatedRootFS(...)` / `updatedDirMount(...)` / `updatedFileMount(...)` now derive their presentation base directly from `dagql.CurrentID(ctx)`
  * the code now carries an explicit warning comment that this relies on those helpers not being invoked from a later lazy callback
* the schema-visible `_sourceContentScoped` module result now stamps an explicit synthetic frame node instead of relying only on raw ID surgery
* with that, the selected high-signal runtime presentation paths for this phase are now frame-based

Phase-2 tripwire:

* if any future caller tries to invoke the `UpdatedRootFS(...)` / mount update helpers outside a real dagql call context, the direct `dagql.CurrentID(ctx)` assumption will fail and that is a tripwire
* do **not** reintroduce stored presentation state on `Container` as an ad hoc workaround

### Phase 3: Hard-cut module-object and semantic ID-valued data to internal result refs

Purpose:

* remove the biggest remaining “stored absolute recipe DAG as data” smell
* make module-object / function-cache object values use internal result refs in memory
* make those refs retain their targets explicitly

Primary files / seams:

* `core/object.go`
  * this is the center of the cut
  * `persistedModuleObjectValueKindResultRef` is the direction
  * `persistedModuleObjectValueKindCallID` is the smell
  * module-object field storage and persistence logic need to stop decoding semantic refs back to raw public `call.ID`s
* `core/interface.go`
  * interface/module-object bridging currently synthesizes module objects and still leans on raw IDs in some paths
* `core/typedef.go`
  * `DynamicID` and object/interface conversion assumptions still treat caller-facing IDs as the semantic representation
* `core/modtypes.go`
  * `CollectedContent` currently mines raw IDs out of module-object data and even raw strings that decode as IDs
* `dagql/cache.go`
  * module-object semantic result refs should become explicit retained deps
  * this likely means either:
    * extending owned-result attachment/normalization to module objects
    * or adding a dedicated normalization/retention pass for module-object semantic refs
* `dagql/types.go`
  * may need extension if `HasOwnedResults` is broadened or a sibling interface is needed for module-object semantic refs

Concrete cut in this phase:

* for module-object fields that semantically represent dagql object/value references:
  * store internal result refs in memory
  * persist them as `result_id`
  * do **not** decode them back into raw canonical `call.ID`s
* these refs must retain their targets as explicit deps

Important nuance preserved from the design:

* a very narrow exceptional raw-`call.ID` data case may still exist
* but it is explicitly **not** the main model
* if keeping that exception starts to complicate implementation materially, stop and escalate

Important explicit consequence:

* this phase should also tighten the SDK/type-conversion boundary so that we are no longer casually treating semantic stored refs as raw historical public IDs

Phase-3 test focus:

* module-object round-trip tests:
  * store semantic refs
  * persist/import
  * surface them back through caller-facing APIs via `IDForCaller(...)`
* function-cache tests involving module-object fields that previously serialized raw IDs
* retention tests showing that module-object stored result refs keep their targets alive

Phase-3 review boundary:

* after this phase, the worst remaining “absolute recipe as stored data” path should be gone
* persistence can still still be using `canonical_id` on `results` for everything else

Phase-3 progress so far:

* module-object field normalization is now result-centric
  * `ModuleObjectType.ConvertFromSDKResult(...)` no longer just preserves field maps blindly
  * known object/interface/list/nullable fields are normalized so semantic refs become live dagql results in memory instead of raw recipe strings
* module-object and interface-annotated values now participate in owned-result attachment
  * both implement `dagql.HasOwnedResults`
  * nested stored result refs are recursively attached and returned as explicit deps
  * this means cached module-object values now retain their semantic child results through the normal dagql dependency machinery
* module-object persistence now treats semantic refs as `result_id`
  * persisted `result_id` values decode back to live dagql results, not raw `call.ID`s
  * the old `call_id` path is now just the narrow exceptional raw-ID escape hatch rather than the main model
* module-object export back to SDK input is now current-boundary-driven
  * known fields are re-serialized from the current field path instead of leaking stale stored child wrapper IDs
  * nested module-object structures recurse through the same field-aware conversion path
  * private/unknown fields keep their best-effort raw-ID behavior as the intentional narrow exception
* `CollectedContent.CollectUnknown(...)` now treats stored dagql results and idables as IDs instead of opaque garbage
  * this keeps private-field best-effort content collection working after the in-memory model change
* focused unit coverage now exists for:
  * recursive owned-result attachment in module-object fields
  * decoding persisted module-object `result_id` values back to live results
  * converting module-object fields back to SDK input using the current field path instead of a stale stored ID
  * persisted module-object round-trips preserving semantic refs as `result_id`
  * `CollectedContent` best-effort handling of unknown stored results

Phase-3 tripwire:

* if there are widespread truly opaque private/internal `call.ID` payloads that turn out not to be rare exceptions, stop and escalate
* that would mean our “module-object semantic refs vs narrow raw-ID exception” split is too optimistic

### Phase 4: Hard-cut persistence/import from `canonical_id` to persisted `resultCallFrame`

Purpose:

* make the database reflect the new model fully
* stop persisting full caller-facing absolute IDs per result
* make import reconstruct internal frame/ref state directly

Primary files / seams:

* `dagql/persistdb/schema.sql`
  * remove `results.canonical_id`
  * add `results.call_frame_json` (or equivalent)
* `dagql/persistdb/mirror_state.go`
  * `MirrorResult.CanonicalID` becomes frame payload
  * update insert/select SQL
* `dagql/cache.go`
  * remove `resultCanonicalIDs`
  * keep frame state on `sharedResult`
  * bump `cachePersistenceSchemaVersion`
* `dagql/cache_egraph.go`
  * stop lifecycle/reset logic from managing `resultCanonicalIDs`
  * ensure result lifecycle preserves/deletes frames appropriately
* `dagql/cache_persistence_contracts.go`
  * `persistResultSnapshot` should carry frame data rather than canonical ID
* `dagql/cache_persistence_worker.go`
  * snapshot export should read frames from results, not `resultCanonicalIDs`
  * persisted row write should store frame JSON
* `dagql/cache_persistence_import.go`
  * import should load results shell-first
  * decode/store frames after result shells exist
  * wire internal frame refs by `sharedResultID`
* `dagql/cache_persistence_resolver.go`
  * persisted lookup by result ID must stop assuming there is a stored canonical full ID
  * result reconstruction should be frame-backed instead
* `dagql/cache_persistence_self.go`
  * persisted self envelopes still currently store and decode full IDs
  * this phase should stop treating envelope-level IDs as authoritative
* `core/persisted_object.go`
  * persisted object helpers that currently ask for `PersistedCallIDByResultID` need to move to result/frame-based reconstruction

Important import strategy:

* do **not** topo sort
* do shell-first import:
  * create all `sharedResult` shells by result ID first
  * import terms/eq classes/deps/snapshot links as today
  * then decode and wire `resultCallFrame`s by result ID
* this works because the targets of frame refs already exist as shells by the time we wire them

Important persistence rule preserved from the design:

* `resultCallFrame` is not a liveness graph
* frame refs do **not** become dep rows automatically
* only explicit dep edges / snapshot links / existing retention mechanisms should drive liveness

Important likely follow-on simplification in this phase:

* once persistence is frame-backed, persisted object/data fields that now use internal result refs get more uniform:
  * internal graph edges use result IDs
  * caller-facing IDs are reconstructed on demand

Phase-4 test focus:

* persistence worker snapshot tests
* import shell-first reconstruction tests
* persisted object decode/load tests by result ID
* full `TestEngine/TestDiskPersistenceAcrossRestart` rerun
* focused function-cache and cache-volume restart repros via subagents

Phase-4 review boundary:

* after this phase, the persistence model should match the current design directly
* `canonical_id` should be gone from the mirrored DB and in-memory persistence bookkeeping

Phase-4 tripwire:

* if some persisted object decode path genuinely still requires a stored full public `call.ID` rather than a result/frame-based reconstruction, stop and escalate
* at that point we need to decide whether the object decoder is modeling a true public recipe handle or whether the decode API still needs redesign

## Cross-phase notes / guardrails

### Pruning

Current explicit plan:

* do **not** redesign pruning as part of this work
* only add new explicit dep/retention behavior where the design already clearly requires it
  * example: module-object stored result refs

### Telemetry rollout scope

We are intentionally **not** fully locking the exact first rollout scope yet.

What the plan assumes for now:

* phase 2 should definitely cover the around-func / telemetry boundary and the synthetic/projection paths that are already known to be problematic
* a broader “replace every user-facing raw `.ID()` callsite” audit can happen after the first targeted rollout if needed

### Hard cut only

This entire plan assumes:

* no backward compatibility
* no support for old persisted states with no frame
* no attempt to preserve the old `canonical_id` model as a fallback storage layer

If we hit a situation where implementation seems to “need” compatibility glue, that is a tripwire and we should stop.
