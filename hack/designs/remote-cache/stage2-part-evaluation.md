# Stage 2 — Per-Part Evaluation

Design for stage 2 of the remote-cache engine foundations. Written against
commit `de3bb48fe9`, the head of the remediation stack. Sits under the
guiding requirements and principles document and the engine-foundations
design; where this document diverges from the engine-foundations sketch,
the divergence is named and justified in the decisions ledger (section 11).

Status markers used below: **verified** (checked against code at this
commit, with file references), **inference** (follows from verified facts),
**judgment** (a call this design makes), **recommendation** (a call the
implementer or Erik can still reverse cheaply).

Contents:

1. Goal and non-goals
2. Baseline facts this design builds on
3. Part taxonomy and mapping to existing machinery
4. The declaration and delegation mechanism
5. Per-group attempt lifecycle in the cache
6. Mixed-local semantics
7. Session resources (F8)
8. Persistence (non-regression argument)
9. Initial refined operation set and expected wins
10. Model plan and budget
11. Decisions ledger
12. Implementation plan (sequenced commits, cut line)
13. Risks and items flagged for Erik

---

## 1. Goal and non-goals

### 1.1 Goal

Today evaluation is result-wide. Demanding any piece of a lazy container
runs the whole chain: every exec runs, every snapshot materializes
(verified: `Cache.Evaluate` has no narrower entry point, `dagql/cache.go:3636`;
every container metadata resolver evaluates the whole parent, for example
`workdir` at `core/schema/container.go:2090`).

Stage 2 makes evaluation per **part**:

- Reading container metadata (workdir, env, entrypoint, labels, ports,
  platform, mounts list) does not run execs and does not materialize
  snapshots.
- Reading one piece of a container (one mount, the rootfs, the exec
  metadata) does not materialize the other pieces.
- An operation that touches one part does not force the parts it does not
  touch.

This is a local laziness improvement, valuable with no remote cache at all
(requirement F10's deliberate exception). It is also the structural
foundation stages 3–6 build on. Stage 2 designs none of those stages; where
a stage-2 shape exists to serve them, it is a seam, not an implementation,
and is labeled as such.

### 1.2 Non-goals

- **Per-part persistence.** Persisted forms stay exactly as today. A result
  with any pending part persists in today's lazy form (section 8). Stage 3.
- **Recipe retention** (stage 4), **snapshot chains and pull** (stage 5),
  **anything foreign, learned, or described** (stages 6–7),
  **from(image) metadata without layers** (stage 8). In stage 2,
  `from(image)` remains a single unrefined operation: demanding any part of
  it pulls and unpacks the image, exactly as today.
- **No Cloud, service, or channel work of any kind.**
- **No change** to lookup, hit selection, e-graph identity, session-resource
  enforcement, release, prune, or shutdown machinery beyond what section 5
  states explicitly.

---

## 2. Baseline facts this design builds on

All verified at commit `de3bb48fe9`.

**Cache-side lazy machinery** (`dagql/cache.go`):

- `sharedResult` carries one lazy-state block under `lazyMu`: `lazyEval`
  (the stored callback), `lazyEvalComplete`, `lazyEvalAttempt` (the
  published attempt), `lazySyncPending` (body succeeded, bookkeeping did
  not) — lines 2081–2091.
- `evaluateOne` (line 3685) is a retry loop: consult the published attempt
  first (join it, wait, retry on foreign cancellation); with no attempt,
  trust object-side state (re-read the value's callback via
  `lazyEvalFuncOfResult`); a nil callback latches completion; otherwise
  publish a fresh attempt and run body-then-bookkeeping in a goroutine.
- One attempt = one `lazyEvalAttempt` record (line 1894): done channel,
  cancel func, waiter count, latched outcome, retry flag, telemetry
  targets. Waiters retain the record across retirement; the last waiter to
  abandon cancels the callback context; a healthy waiter that receives the
  attempt's own cancellation retries.
- The bookkeeping stage is `syncResultSnapshotLeases` (line 1516): a
  read-diff-write over desired snapshot-owner links derived from the
  payload by `Peek`.
- Each attempt takes its own session operation token (`attemptOp`, line
  3774); recursion is detected by a context stack of `sharedResultID`s
  (line 3672); the callback runs with the result's authoritative
  `ResultCall` restored and telemetry resumed.
- `registerLazyEvaluation` (line 3516) re-arms the stored callback at
  publication and at persisted-hit load, only when no attempt, no stored
  callback, no completion, and no pending bookkeeping.
- `HasPendingLazyEvaluation` (line 1031) is the pending test used by
  telemetry (`core/telemetry.go:357,392`) and the schema eager/lazy fork
  (`core/schema/container.go:3391`).

**Object-side machinery** (`core`):

- Exactly three types implement `HasLazyEvaluation`: `Container`,
  `Directory`, `File` (verified by grep; `core/container.go:1087`,
  `core/directory.go:128`, `core/file.go:116`).
- A container's snapshot-bearing state lives behind `LazyAccessor` fields:
  `FS`, `MetaSnapshot`, and per-mount `DirectorySource`/`FileSource`
  (`core/container.go:74,88,455–457`). Metadata (Config, Platform, the
  mounts list shape, Ports, Annotations, Secrets, Sockets, Services,
  VolatileEnv, ImageRef, …) is plain fields.
- Every container mutation body starts with
  `materializeContainerStateFromParent` (`core/container.go:1041`): fully
  evaluate the parent, then copy all metadata and clone all accessors.
- `WithExec` (`core/container_exec.go:1181`) installs the exec recipe and
  resets `FS`, `MetaSnapshot`, and each writable mount's accessor to empty
  accessors; read-only mounts keep the accessors cloned at construction.
  The exec body fills all outputs in one run via output bindings.
- Schema mutations fork on `HasPendingLazyEvaluation(parent)`
  (`cloneContainerForSchemaChild`, `core/schema/container.go:3390`): an
  evaluated parent gets the mutation applied eagerly with no lazy op; a
  pending parent gets a lazy op installed. So lazy chains exist exactly
  when the base is pending, and `withExec` always defers.
- `ContainerMounts.With` (`core/container.go:5022`) removes any existing
  mount at the same target (or under it) before appending. Mount targets
  are therefore unique within a container's mount list.
- Persisted snapshot-link roles for mounts are positional
  (`mount_dir:%d`, `core/container.go:1247`).

**Model** (`dagql/tla/CacheLifecycle.tla`):

- Lazy evaluation is modeled per result: `lazyCb`, `lazyComplete`,
  `lazyPhase` (idle/running/syncing), `lazyCancel`, `lazySyncPending`,
  `lazyWaiters`, `lazyRunning`, `lazyTokenSession`. Evaluators
  (`EvalSpawn` … `EvalOperationExit`) target a result, not a part.
- The lazy invariants: `LazyMutualExclusion`, `EvalDoneComplete`,
  `LazyCompleteSettled`, `LazySuccessPermanent`,
  `LazyAttemptDefersCollection`, `NoStaleCancelError`, plus the liveness
  properties.
- Callback bodies are opaque: the model does not represent a body
  evaluating another result (today's real bodies do exactly that via
  `materializeContainerStateFromParent`).

---

## 3. Part taxonomy and mapping to existing machinery

### 3.1 Parts

A part is one separately evaluable piece of a result's value. Stage 2
defines parts only where the machinery will exercise them.

**Container** (the refined type):

| Part | Backing state |
|---|---|
| `metadata` | All plain fields: `Config`, `Platform`, the mount list shape (targets, readonly flags, kinds), `Ports`, `Annotations`, `Secrets`, `Sockets`, `Services`, `VolatileEnv`, `EnabledGPUs`, `ImageRef`, `DefaultTerminalCmd`, `SystemEnvNames`, `DefaultArgs` |
| `fs` | The `FS` accessor (root filesystem) |
| `execMeta` | The `MetaSnapshot` accessor (stdout, stderr, exit code) |
| `mount:<target>` | The mount source accessor of the mount at `<target>` |

**Directory** and **File**: the taxonomy names their parts
(path, snapshot) for continuity with the engine-foundations document, but
in stage 2 both types keep a single evaluation unit covering everything.
Judgment: their deferred work is already one snapshot's worth; splitting it
buys no local laziness (a directory op's body needs the parent snapshot to
produce either part, and paths are pre-seeded at construction where cheap).
Splitting them later is additive, not a rework.

**Everything else** (all other lazy-capable values, and every value that is
not lazy): one implicit whole-result part. No code change.

### 3.2 Mount part identity is the target path, not the index

The engine-foundations sketch said mount parts are positional. This design
keys them by target path instead.

- Verified: targets are unique within a mount list
  (`ContainerMounts.With`).
- Inference: target-keyed identity is stable under add, remove, and
  replace, so delegation from a child's mount part to the parent's mount
  part is a lookup by target, with no index-translation table per
  operation.
- Verified: persisted lease roles stay positional (`mount_dir:%d`). They
  can stay positional: under the metadata-first rule (3.4) the mount list —
  and therefore every index — is settled before any mount part
  materializes, so roles remain stable per result. No lease or persistence
  format change.

### 3.3 Groups, and the collapse of the two-level attempt structure

The engine-foundations document sketched attempts per (result, part) plus
group attempts that exclude member part attempts, and called that two-level
structure the likeliest place for a race (its section 5.2).

This design removes the second level. The unit of evaluation is the
**group**. Every part of a result maps to exactly one group. An attempt is
per (result, group). A part is complete exactly when its group is complete.
There are no part attempts, so there is nothing to exclude.

Why this is sound for stage 2 (judgment, with the reasoning spelled out):

- Locally, a part is only ever produced by one body: the body of the
  operation that writes it (or the delegation body that copies it from the
  parent). There is no second producer to race, because pull does not exist
  until stage 5/6.
- The exec's joint output ("running the process fills fs, execMeta, and
  every writable mount at once") is expressed as one group containing all
  those parts. Asking for any member joins the group's single attempt.
  That is exactly the existing single-attempt machinery, keyed by group.

Why this does not foreclose stage 6 (the seam): group resolution is
per-Evaluate-call (section 4.2). A learned result in stage 6 can map a
snapshot part to a single-part pull group while the pull is viable and remap
it to the run group when the pull is exhausted. The pull-versus-run
exclusion then becomes a question about *remapping*, which stage 6 must
model before building — as the engine-foundations document already
requires. Stage 2 asserts, in code and model, the invariant stage 6 will
relax deliberately: a part's group mapping is deterministic and stable once
the result's metadata is settled.

### 3.4 The metadata-first rule

Rule: **evaluating or resolving any part of a refined container first
settles the metadata part.**

Causality:

1. Mount parts are keyed by target; which mounts exist is metadata.
2. Some operations' write target depends on metadata (`withDirectory` into
   a path that may be under a mount — see 4.5).
3. Therefore part-to-group resolution can require metadata.
4. Metadata evaluation is cheap by construction: a metadata group body
   copies the parent's metadata and applies a field edit. It never touches
   a snapshot. Its delegation chain bottoms out at the first ancestor whose
   metadata is settled (or at an unrefined operation, which evaluates fully
   — today's behavior, see 4.6).

So the invariant costs one cheap chain walk and buys a world where every
positional and target-dependent question has an answer before any snapshot
work starts. This is the local analogue of "metadata may be eager;
snapshots are never eager" (F11).

---

## 4. The declaration and delegation mechanism

### 4.1 dagql layer: the part-aware contract

New identifiers in `dagql` (names final unless review objects):

```go
// PartKey identifies one separately evaluable piece of a result's value.
// Keys are defined by the value's package; dagql treats them as opaque.
type PartKey string

// LazyGroupKey identifies one evaluation group of a result. Every part
// maps to exactly one group; a group's single body fills all its parts.
type LazyGroupKey string

// LazyGroupWhole is the implicit group of values that do not split their
// deferred work. It fills every part.
const LazyGroupWhole LazyGroupKey = "whole"

// HasLazyEvaluationParts is implemented by values whose deferred work is
// split into independently evaluable groups. Values that implement only
// HasLazyEvaluation have exactly one group, LazyGroupWhole.
type HasLazyEvaluationParts interface {
    HasLazyEvaluation

    // ResolveLazyEvalGroups maps the requested parts to the groups that
    // fill them, in the order they should be evaluated. nil parts means
    // "every group that currently has deferred work". self is the
    // attached result wrapping this value. Resolution may evaluate the
    // value's own metadata part (via the cache) to settle positional
    // parts; it must never evaluate snapshot content. The mapping must be
    // deterministic and stable once metadata is settled.
    ResolveLazyEvalGroups(ctx context.Context, self AnyResult, parts []PartKey) ([]LazyGroupKey, error)

    // LazyEvalFuncForGroup returns the group's remaining deferred work,
    // nil when none remains. Same consumption contract as LazyEvalFunc,
    // per group: a successful body run consumes the group's work, and the
    // cache independently guarantees the body never runs twice.
    LazyEvalFuncForGroup(LazyGroupKey) LazyEvalFunc
}
```

The existing `HasLazyEvaluation.LazyEvalFunc` contract is unchanged and
remains the "is anything still deferred" signal: for a parts value it must
return non-nil while any group has work (for `Container` this already
holds: it returns non-nil exactly while `container.Lazy != nil`, and
`Lazy` is now cleared only when the last group is consumed — see 4.4).

New cache API:

```go
// Evaluate is unchanged: it forces every part of each result.
func (c *Cache) Evaluate(ctx context.Context, results ...AnyResult) error

// EvaluateParts forces only the named parts of one result. On a value
// that does not implement HasLazyEvaluationParts every part is filled by
// the whole-result group, so this degenerates to Evaluate.
func (c *Cache) EvaluateParts(ctx context.Context, res AnyResult, parts ...PartKey) error
```

Callers that need everything keep calling `Evaluate` and are untouched.

### 4.2 Cache flow

`EvaluateParts(ctx, res, parts...)`:

1. Begin exactly as `beginEvaluateOne` does today: operation token,
   attached-result check.
2. Fast path under `lazyMu`: if the result-level completion latch is set,
   return.
3. `UnwrapAs[HasLazyEvaluationParts](res)`. Not implemented → evaluate the
   whole-result group (today's `evaluateOne` loop). Implemented → call
   `ResolveLazyEvalGroups(ctx, res, parts)` **outside** `lazyMu` (it may
   re-enter the cache to settle metadata).
4. For each returned group, in order, run the per-group generalization of
   today's `evaluateOne` loop (section 5).

`Evaluate` on a parts value resolves with `parts == nil` (all groups) and
runs them **sequentially, metadata group first, then the remaining groups
in declaration order**. Judgment: sequential matches today's one-body
execution most closely, keeps failure attribution simple, and the joint
exec group dominates cost anyway; parallelizing groups within one result is
an optimization nobody has asked for.

### 4.3 core layer: part keys and the container contract

```go
// core
const (
    ContainerPartMetadata dagql.PartKey = "metadata"
    ContainerPartFS       dagql.PartKey = "fs"
    ContainerPartExecMeta dagql.PartKey = "execMeta"
)

func ContainerPartMount(target string) dagql.PartKey {
    return dagql.PartKey("mount:" + target)
}

// LazyContainerParts is implemented by refined container lazy ops.
// Unrefined ops implement only Lazy[*Container] and keep whole-result
// behavior.
type LazyContainerParts interface {
    Lazy[*Container]

    // ContainerLazyGroups maps parts to groups. The owning result's
    // metadata part is already settled when this is called with
    // positional (mount) parts.
    ContainerLazyGroups(ctx context.Context, ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error)

    // EvaluateContainerGroup runs one group's body against ctr. It must
    // write only the parts of that group and must be idempotent per
    // group (per-group run-once latch inside LazyState).
    EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error
}
```

`Container` implements `dagql.HasLazyEvaluationParts` by routing:

- `container.Lazy == nil` → no groups (nothing deferred).
- `container.Lazy` implements `LazyContainerParts` → settle own metadata
  part first (one `EvaluateParts(self, ContainerPartMetadata)` — cheap,
  latched), then delegate the mapping to the op.
- Otherwise → `[LazyGroupWhole]`, and `LazyEvalFuncForGroup(LazyGroupWhole)`
  is today's `LazyEvalFunc()`. **This is the default: an undeclared
  operation behaves byte-for-byte as today.**

Group keys for refined container ops:

- `"metadata"` — the metadata part.
- Delegation groups — key equals the part key (`"fs"`, `"execMeta"`,
  `"mount:<target>"`); each delegated part is its own group so demanding
  one never forces another.
- `"execOutputs"` — the exec's joint group: `fs`, `execMeta`, and every
  writable mount's part.
- `"write"` — the single written part of a target-resolved writer
  (4.5); which part it covers is decided by `ContainerLazyGroups` from
  settled metadata.

### 4.4 Object-side state: per-group latching, and when `Lazy` clears

`LazyState` (`core/lazy_state.go`) grows per-group latching in place:

```go
type LazyState struct {
    LazyMu           *sync.Mutex
    LazyInitComplete bool                          // the whole-op latch, as today
    doneGroups       map[dagql.LazyGroupKey]bool   // per-group latches, nil until first use
}

// EvaluateGroup is the per-group analogue of Evaluate: return immediately
// if the group latched; otherwise run once under LazyMu and latch on
// success.
func (lazy *LazyState) EvaluateGroup(ctx context.Context, typeName string, g dagql.LazyGroupKey, run func(context.Context) error) error
```

Rules:

- Each group's body writes **only** its own parts. The metadata group
  writes plain fields; a delegation group writes one accessor; the exec's
  joint group writes its output accessors. No body rewrites a part another
  group owns (see section 6).
- `container.Lazy` is cleared only when **all** groups are consumed. This
  keeps two existing signals truthful: `LazyEvalFunc() != nil` means
  "something is still deferred" (feeds `HasPendingLazyEvaluation` and the
  schema eager/lazy fork), and persistence's form selection (`Lazy == nil`
  → ready form) keeps meaning "fully materialized" (section 8).
- The direct object-side evaluation paths (`Container.Evaluate`,
  `container.Sync`, `metaFileContents`'s internal force at
  `core/container_exec.go:2333`) continue to work: they run all remaining
  groups through the same per-group latches. They remain exactly as
  coordinated as today — serialized by `LazyMu`, made idempotent by the
  latches, uncoordinated with cache attempts in the same way today's
  direct calls are. Sites whose refinement is a stage-2 win (the exec-meta
  readers) move their forcing to the resolver via `EvaluateParts`
  (section 9).

### 4.5 Delegation and the two templates

Delegation helper (replaces the relevant uses of
`materializeContainerStateFromParent`):

```go
// materializeContainerMetadataFromParent evaluates only the parent's
// metadata part and copies the plain fields onto dst. The split-out
// metadata half of materializeContainerStateFromParent.
func materializeContainerMetadataFromParent(ctx context.Context, dst *Container, parent dagql.ObjectResult[*Container]) error

// delegateContainerPart evaluates the parent's part and copies its
// value into dst's accessor for the same part (detached clone for
// Directory/File values, same reference for snapshot refs — the same
// cloning the existing CloneContainer* helpers do).
func delegateContainerPart(ctx context.Context, dst *Container, parent dagql.ObjectResult[*Container], part dagql.PartKey) error
```

**Template A — metadata-only mutation** (covers ~28 ops, section 9):

- `ContainerLazyGroups`: `metadata` → `"metadata"`; every snapshot part →
  a delegation group named by the part.
- `EvaluateContainerGroup("metadata")`:
  `materializeContainerMetadataFromParent`, then apply the op's field edit
  (the code that today sits after `materializeContainerStateFromParent` in
  the op's `Evaluate`).
- `EvaluateContainerGroup(part)`: `delegateContainerPart`.
- Delegation chains collapse naturally: the parent's own delegation group
  copies from *its* parent, recursively, bottoming at the nearest writer.
  Each level is one latched copy; no chain-walking index is built or
  needed. (The engine-foundations phrase "delegation chains collapse to
  the nearest ancestor" is satisfied behaviorally; materialized
  intermediate copies are cheap accessor writes.)

**Template B — snapshot writer**:

- Declares written parts. For the exec they are static (`execOutputs`).
  For target-resolved writers (`withDirectory`, `withFile`,
  `withNewFile`, `withoutPaths`, …) the written part is `fs` or
  `mount:<t>` depending on where the target path lands; that is computed
  from settled metadata (`locatePath` reads only the mount list —
  verified, `core/container.go` mount resolution takes targets, not
  content). `ContainerLazyGroups` maps the resolved written part to
  `"write"` and everything else to delegation groups.
- `EvaluateContainerGroup("write")`: evaluate the parent parts and source
  results the body actually reads (for `withDirectory` into the rootfs:
  parent `fs` plus the source directory), run the existing helper, set the
  written accessor.
- The engine-foundations line "withDirectory writes fs only" was
  incomplete: the write target is path-dependent. This design handles that
  with per-instance resolution rather than a conservative joint group,
  because a joint group over all snapshot parts would force unrelated
  mounts — exactly the over-evaluation stage 2 exists to remove.

**The exec** (template B, static):

- `metadata` → delegation. The exec does not change Config, Platform,
  Ports, or the mount list.
- Each read-only mount part → delegation.
- `fs`, `execMeta`, every writable mount part → `"execOutputs"`. The group
  body: settle own metadata (needed for args/env expansion), evaluate the
  parent parts the run mounts (parent `fs` and **all** parent mount
  parts — the run needs them mounted; this is unavoidable and correct),
  run the process, fill the output accessors via the existing output
  bindings. It does **not** copy parent state wholesale:
  `materializeContainerStateFromParent` is not used by refined bodies.
- Two body impurities exist today and must be resolved
  (verified):
  - `state.ExecMD` is written mid-body (`core/container_exec.go:1312`).
    That is recipe-local state, not a part; unchanged.
  - `container.VolatileEnv` is rewritten from the session's volatile-var
    table (`core/container_exec.go:1262–1268`). Under the "a body writes
    only its group's parts" rule this write is illegal (VolatileEnv is
    metadata). Recommendation: keep the resolved list local to the run
    (it feeds `metaSpec.Env`, line 352, inside the same body) and stop
    rewriting the child's field; the child's VolatileEnv then delegates
    from the parent. Downstream consumers read names, not values
    (next exec re-resolves by name, `expandEnvVar` walks names —
    verified), so this is expected to be behavior-preserving; the
    implementer must verify every `VolatileEnv` reader
    (`grep -rn VolatileEnv core/`) before landing, and this is flagged to
    Erik (section 13).

### 4.6 Composition with unrefined operations

- Refined child, unrefined parent: the child's delegation group calls
  `EvaluateParts(parent, part)`; the parent maps every part to
  `LazyGroupWhole`; the parent fully evaluates. Conservative, correct,
  today's cost.
- Unrefined child, refined parent: the child's whole-result body calls
  `Evaluate(parent)`; the parent evaluates all groups. Same.
- Chains refine incrementally with no flag days.

---

## 5. Per-group attempt lifecycle in the cache

### 5.1 State

The existing per-result lazy block becomes per-group state plus a
result-level latch:

```go
// one evaluation group's cache-side state; the fields and their meaning
// are exactly today's sharedResult lazy fields, per group
type lazyGroupState struct {
    eval        LazyEvalFunc
    complete    bool
    attempt     *lazyEvalAttempt
    syncPending bool
}

// on sharedResult, all guarded by lazyMu:
lazyMu           sync.Mutex
lazyWhole        lazyGroupState                       // the implicit whole-result group, inline
lazyPartGroups   map[LazyGroupKey]*lazyGroupState     // named groups; nil until a parts value uses one
lazyEvalComplete bool                                 // result-level: everything settled (fast path)

// serializes syncResultSnapshotLeases per result (see 5.5)
leaseSyncMu sync.Mutex
```

Memory: results that never use named groups (every plain value, every
unrefined op) allocate nothing new — `lazyWhole` replaces the four existing
scalar fields, and the map stays nil. The hot fast path ("evaluate a
completed result") is unchanged in shape and cost: one `lazyMu` lock, one
bool check. That meets the bar F10 sets; no path gets slower for unusers.

Per value there is exactly one regime: a parts value never uses
`lazyWhole`, a non-parts value never uses named groups. `Evaluate` on a
parts value resolves to all named groups, so the two regimes cannot mix on
one result.

### 5.2 The attempt loop, per group

`evaluateGroup(ctx, res, shared, g)` is today's `evaluateOne` for-loop with
every read/write of the lazy block going through `groupState(g)`:

1. Under `lazyMu`: group complete → return.
2. Published attempt for `g` → increment its waiters, wait on it
   (`waitForLazyEvaluation`, unchanged), retry the loop on foreign
   cancellation, return its outcome otherwise.
3. No attempt for `g`: if `g.syncPending`, lead a bookkeeping-only
   attempt. Else re-read `LazyEvalFuncForGroup(g)` from the value; nil →
   latch `g.complete`, return. (Object-side per-group state is trustworthy
   exactly when no attempt for that group is published — the same ordering
   argument as today, per group, because attempt retirement for `g`
   happens under `lazyMu` after `g`'s body returned.)
4. Publish a fresh `lazyEvalAttempt` on `g`, mint telemetry targets under
   `lazyMu`, take the callback operation token, start the goroutine: body
   (if armed), then `syncResultSnapshotLeases`, then latch outcome, retire
   the attempt, close done. On body-success-then-bookkeeping-failure set
   `g.syncPending` — identical to today, per group.

Unchanged, per group, by construction (the code is the same code):

- Singleflight per group; joiners never read a successor attempt's state.
- Waiter-count cancellation: the last waiter to abandon a running group
  attempt cancels that group's callback context; a healthy waiter that
  receives the attempt's own cancellation retries.
- Retry-after-bookkeeping-failure via `syncPending`.
- One operation token per attempt; the release-deferral tail
  (`exitingLazy`) counts callback goroutines exactly as before — there can
  now be several concurrently for one result, which the saturating
  session-level flag already tolerates (it counts sessions' tails, not
  results').
- Call-context restoration and telemetry resumption per attempt. Resume
  spans now exist per group; the span name gains the group key so traces
  distinguish "resume withExec (metadata)" from "resume withExec
  (execOutputs)".

New:

- **Two callers wanting different parts of one result do not serialize.**
  Their groups' attempts are independent; only `lazyMu`'s short critical
  sections are shared. (Model reachability probe, section 10.)
- **Recursion stack keyed by (result, group).** A group body may evaluate
  a *different* group of the same result (the joint exec body settles own
  metadata). Re-entering the *same* (result, group) is refused with
  today's "recursive lazy evaluation detected" error. Group dependencies
  within a result are acyclic by construction: only snapshot groups demand
  the metadata group, never the reverse.
- `HasPendingLazyEvaluation`: complete-latch → false; any group with an
  attempt, pending bookkeeping, or an armed stored callback → true;
  otherwise fall back to the value's `LazyEvalFunc() != nil`, unchanged.
- `registerLazyEvaluation`: unchanged in role; for parts values it does
  not pre-store per-group callbacks (they are re-read at attempt start,
  which the loop already does today), it only keeps the whole-group path
  working for non-parts values. Its guard clauses generalize to "no
  attempt on any group, nothing stored, not complete, no pending
  bookkeeping".

### 5.3 Failure, retry, cancellation semantics (stated per part)

- A group body failure leaves that group retryable and other groups
  untouched. A caller evaluating parts [metadata, fs] where fs's body
  fails gets the fs error; metadata stays complete.
- Success is permanent per group.
- A caller's own cancellation ends only that caller's wait, per group, as
  today per result.
- Whole-result `Evaluate` over groups runs them sequentially and returns
  the first error; already-completed groups cost one mutex check each.

### 5.4 What "fully evaluated" means

`lazyEvalComplete` (result level) latches when an evaluate-everything pass
observes every resolved group complete, or immediately for non-parts
values as today. It exists only as the fast path; correctness derives from
per-group latches.

### 5.5 Bookkeeping concurrency

`syncResultSnapshotLeases` is a read-diff-write
(`loadSnapshotOwnerLinks` … `storeSnapshotOwnerLinks`) with only per-step
locking. Today two concurrent syncs per result are rare (public API plus
decode install); per-group attempts make them routine. Interleaved syncs
cannot remove a fresh lease (removal only targets roles the syncer itself
observed), but the stored link set can transiently omit a link another
sync attached — bookkeeping drift that a later sync repairs. Judgment:
don't rely on eventual repair; serialize the function per result with
`leaseSyncMu`. It does I/O, so it must not run under `lazyMu`. Desired
links derive from `Peek`, and accessor sets only grow, so incremental
syncs only add links; roles never disappear mid-lifecycle.

---

## 6. Mixed-local semantics (the simplified local shadow of D11)

The rule, in three sentences:

1. **Every part of a result is written exactly once, by its own group's
   body, and never rewritten.**
2. A delegation body copies from the parent's already-latched part, so the
   value a part gets is independent of the order in which sibling groups
   run.
3. Consequently one result's parts may materialize at different times but
   always compose into the single state today's whole-body evaluation
   would have produced — there is only ever one production per group, so
   "mixing productions" cannot arise locally.

D11 proper (a learned result mixing a pulled part with a locally-run
group) does not bite in stage 2, exactly as the engine-foundations
document predicted, because rule 1 has no second producer. Stage 6 will
have to weaken rule 1 deliberately and model the weakening.

Enforcement is structural, not runtime-checked: refined bodies write only
their declared accessors/fields (the `materializeContainerStateFromParent`
wholesale copy is not used by refined bodies), and the two known body
impurities (VolatileEnv, ExecMD) are resolved in section 4.5. The model
asserts the per-part single-production invariant abstractly (section 10).

---

## 7. Session resources (F8)

Per-part evaluation creates no new way to serve a result:

- `EvaluateParts` requires an attached result the caller already holds.
  How callers obtain results — lookup, load, select — is untouched, and
  those paths keep the existing enforcement: the stored-requirement subset
  filter at selection and the generation-checked serve-time re-validation
  (one atomic load in the common case). No stage-2 code touches
  `requiredSessionResources`, its generation counter, or any serve path.
- Requirements are derived from dependency edges recorded at attachment
  time, which happens at publication regardless of evaluation state
  (verified: `AttachDependencies` runs at attach, not at evaluate —
  `internal-docs/lazy_evaluation.md`, and stage-2 changes nothing there).
  A partially evaluated result therefore carries exactly the same
  requirement set as a fully evaluated one.
- Delegation bodies evaluate parent results from inside a callback. Bodies
  already do exactly this today (`materializeContainerStateFromParent` →
  `cache.Evaluate(parent)`). Per-part evaluation only narrows the set of
  ancestors evaluated; it never widens it. Execution-time resource use
  (secret plaintext, socket mounts) still resolves against the current
  session's bindings and still fails for a session that lacks them.

---

## 8. Persistence (non-regression argument)

Stage 2 changes no persisted format and no persistence code path.

- Form selection is object-side and keeps its meaning: `Container`
  encodes the ready form only when `Lazy == nil`, which now means "every
  group consumed", which implies every accessor is set — the ready form's
  precondition, unchanged. Any pending group leaves `Lazy != nil`, so a
  partially evaluated result persists in today's **lazy form**: the
  recipe, exactly as an unevaluated result does. Partial progress is
  discarded across restart and re-derived on demand; the discarded work is
  metadata copies and delegated accessor clones, cheap by construction,
  while expensive work (an exec that ran) was latched in the *parent's*
  own row, which persists independently.
- A lazy-form result holding some snapshot links already exists today
  (an exec on an evaluated parent holds its read-only mounts' refs before
  its own body runs — engine-foundations 2.5, verified against
  `WithExec`), and persistence collects links regardless of form. Stage 2
  makes that shape more common, not new.
- Lease roles stay stable (3.2). Decode, import, re-arm after restart:
  unchanged; a decoded lazy form re-arms with all groups pending, which is
  exactly what its recipe encodes.
- `ErrPersistStateNotReady` behavior for Directory/File: unchanged (they
  stay single-group).

Inference: nothing in stage 2 can make a result persist that would not
persist today, or fail to persist one that would. Stage 3 replaces this
section wholesale.

---

## 9. Initial refined operation set and expected wins (D13)

The stage-1 audit page the engine-foundations document planned does not
exist in the stage-1 worktree (verified by search), so this section is the
classification, made against the code directly. The implementer re-verifies
each line as they convert it; a wrong classification fails loudly in tests,
not silently, because delegation bodies copy real values.

### Wave 1 (tonight's slice)

**Refined operations:**

- Template A, metadata-only (~29 ops in `core/container.go` /
  `core/container_exec.go`): `WithEntrypoint`, `WithoutEntrypoint`,
  `WithDefaultArgs`, `WithoutDefaultArgs`, `WithUser`, `WithoutUser`,
  `WithWorkdir`, `WithoutWorkdir`, `WithEnvVariable`,
  `WithEnvFileVariables`, `WithSystemEnvVariable`, `WithVolatileVariable`,
  `WithoutEnvVariable`, `WithoutVolatileVariable`, `WithLabel`,
  `WithoutLabel`, `WithImageConfigMetadata`, `WithHealthcheck`,
  `WithoutHealthcheck`, `SetGPUs`, `WithAnnotation`, `WithoutAnnotation`,
  `WithSecretVariable`, `WithoutSecretVariable`, `WithServiceBinding`,
  `WithExposedPort`, `WithoutExposedPort`, `WithDefaultTerminalCmd`,
  `ContainerVolatileExecCacheHitLazy`. (Ops touching only Secrets,
  Sockets, Services lists are metadata: the resources are dependency
  results, not parts.)
- Template B, static: `ContainerExecLazy` (the centerpiece),
  `ContainerWithRootFSLazy` (writes `fs`, delegates the rest).
- Selectors, refined on the read side: `ContainerRootFSLazy` evaluates
  parent `fs` only; `ContainerDirectoryLazy` / `ContainerFileLazy`
  evaluate parent `metadata`, locate the path, then evaluate only the
  located part (their bodies' `Peek`s of mount sources become
  evaluate-that-part-then-`Peek`).

**Part selection at Evaluate sites** (`core/schema/container.go`):

- Metadata readers → `EvaluateParts(parent, ContainerPartMetadata)`:
  `entrypoint`, `defaultArgs`, `user`, `workdir`, `envVariables`,
  `envVariable`, `labels`, `label`, `mounts`, `healthcheck`, `platform`,
  `exposedPorts` (12 resolvers, lines listed in section 2's survey).
- Exec-meta readers → `EvaluateParts(parent, ContainerPartExecMeta)`:
  `stdout`, `stderr`, `combinedOutput`, `exitCode` — moving the force out
  of `metaFileContents` into the resolvers (4.4).

**Everything else stays unrefined** and therefore byte-for-byte today's
behavior: `from(image)` (stage 8), mount-adding mutations,
`withDirectory`-family writers, dockerBuild, git/host sources, export and
service and terminal paths (they need everything or nearly so), all
Directory and File ops.

### Wave 2 (in-stage follow-ups; explicitly allowed to slip past tonight)

- Mount mutations (`WithMountedDirectory`, `WithMountedFile`,
  `WithMountedCache`, `WithMountedVolume`, `WithMountedTemp`,
  `WithMountedPathDockerfileCompat`, `WithoutMount`, `WithUnixSocket`,
  `WithoutUnixSocket`): metadata writes the mount list; the new mount's
  part evaluates its source on demand.
- Target-resolved rootfs/mount writers (`WithDirectory`, `WithFile`,
  `WithFiles`, `WithNewFile`, `WithoutPaths`, symlink ops): template B
  with metadata-resolved write target.
- Export-family read narrowing (`publish`, `export`, `asTarball`,
  `manifest`, `layer` → metadata + `fs`), only after verifying no export
  path reads mounts or exec meta.

### Expected real-world wins (wave 1)

- **Metadata reads run nothing.** Any pipeline that inspects config on a
  built-but-not-yet-needed container (module SDK code paths, dockerfile
  compat reading env/entrypoint, users branching on `envVariable`) stops
  running the exec chain to answer. Concretely: today
  `from(x).withExec(e).withEnvVariable(k,v).workdir()` runs `e`; after
  stage 2 it runs nothing once the image is present.
- **Exec-meta reads don't wait on unrelated delegation.** `stdout` on an
  exec forces the exec (necessary — the run produces the meta) but no
  longer forces materialization of chains hanging off untouched read-only
  mounts above it.
- **Selectors materialize one part.** `ctr.directory("/out")` on a
  container with several mounted inputs evaluates metadata plus the part
  `/out` lives in; today it materializes every mount and the rootfs. This
  is the largest byte-count win: untouched `host.directory` /
  `git` mount sources are never uploaded or unpacked.
- **Foundation:** stages 3 (persist per part), 5/6 (pull per part) attach
  to group bodies and part state without re-plumbing evaluation.

Non-wins to keep expectations honest: a local exec chain demanded at the
tip still runs every exec (their `fs` outputs are genuinely needed); the
per-exec skip of cousin work only moves bytes when mounts/sources are
expensive. The chain-of-fifty-warm-hits headline belongs to stage 6.

---

## 10. Model plan and budget

Per G30 the model moves first (commit 1 in section 12), and every modeled
claim below lands with a re-break run and a reachability probe.

### 10.1 Spec changes (`dagql/tla/CacheLifecycle.tla`)

New constants:

- `LazyGroups` — the set of group identifiers for the run (existing
  configs: a singleton, `{g1}`).
- `LazyParts` — the part identifiers; `PartGroupOf` — the (static, per
  configuration) part→group table. In the code the mapping is
  per-operation and metadata-resolved; a configuration fixes one shape,
  which is sufficient because every invariant below is per-group/per-part
  and shape-independent.
- `GroupNeeds` — a per-configuration acyclic relation over `LazyGroups`:
  group g's body demands group h of the same result before it can
  succeed (models the joint body settling own metadata).
- `ModelPartDelegation` — enables the cross-result body-demand action
  (models a delegation body evaluating the parent's part).

State: `res[r]`'s eight lazy fields (`lazyCb`, `lazyComplete`,
`lazyPhase`, `lazyCancel`, `lazySyncPending`, `lazyWaiters`,
`lazyRunning`, `lazyTokenSession`) become functions
`[LazyGroups -> ...]`. Eval records gain a `part` field; demand targets
`PartGroupOf[part]`.

Actions: `EvalNoWork`, `EvalStartAttempt(Refused)`, `EvalJoin`,
`EvalBodyFinish`, `EvalSyncFinish`, `EvalAbandon`, `EvalCallbackClose`,
`EvalWake`, `EvalOperationExit`, `ImportedLazyArm` are indexed by group;
their bodies are otherwise unchanged (that is the point: the design reuses
the machinery). `ImportedLazyArm` arms all groups together (a decoded lazy
form re-arms whole). Two new actions:

- `EvalBodyDemand(r, g)` — a running body for (r, g) spawns an internal
  evaluator for (r, h) where `<<g,h>> ∈ GroupNeeds`; `EvalBodyFinish(r,g)`
  with outcome bodyOk requires every needed group complete.
- `EvalDelegateDemand(r, g)` — with `ModelPartDelegation`, a running body
  for (r, g) spawns an internal evaluator for (parent, g) where parent is
  a dependency of r. This models what real bodies do today and the model
  has never covered; stage 2's delegation makes it load-bearing.

Internal evaluators take an operation token like any Evaluate caller
(matching `beginContextOperation` inside a callback) and do not require a
session edge on the target (matching Go: `Evaluate` needs an attached
result, not an edge; today's `EvalSpawn` edge requirement stays for
client-origin evaluators only).

Invariants, generalized per group with the existing names:
`LazyMutualExclusion` (≤1 body per (result, group)), `EvalDoneComplete`
(an evaluator's demanded part's group is complete when it returns done),
`LazyCompleteSettled`, `LazySuccessPermanent`, `NoStaleCancelError`,
`LazyAttemptDefersCollection` (token per (result, group) defers its
session). New: `PartServedByItsGroup` (an evaluator for part p only ever
waits on group `PartGroupOf[p]` — the stable-mapping invariant stage 6
will deliberately relax). Liveness: `EvalEventuallyTerminal` re-proved
with `GroupNeeds` and delegation demands enabled (the new wedge risks are
exactly there). Reachability probes: two groups of one result running
bodies concurrently (new behavior, must be reachable); a group failing
while a sibling group is complete.

### 10.2 Configurations

Every existing configuration gains the new constants with inert values
(`LazyGroups = {g1}`, one part, empty `GroupNeeds`, delegation off).
Acceptance: the `lazy` configuration must reproduce exactly
**6,398,997 distinct states** (measured on this worktree's jar,
2026-08-31, 56s wall) — the singleton-group re-encoding is an isomorphism,
so any count drift means the refactor changed behavior.

New configurations, each with a budget estimate and a hard ceiling of 20
minutes on the dev box:

- `lazy_parts` — one session, one call, `MaxInvocations=1`, `MaxEvals=3`,
  `LazyGroups={gMeta,gOut}`, parts `{pMeta,pFS,pXMeta}` with
  `pFS,pXMeta→gOut`, `LazyCanFail=TRUE`. Checks all per-group invariants
  plus joint-group completion and the concurrent-groups probe. Estimate:
  well under the current `lazy` config (one producing invocation instead
  of two).
- `lazy_parts_prereq` — `lazy_parts` plus `GroupNeeds = {<<gOut,gMeta>>}`
  and `EvalBodyDemand`. Safety.
- `lazy_parts_liveness` — the prereq shape under `LiveSpec`,
  `EvalEventuallyTerminal`. Small bounds (liveness forbids symmetry).
- `lazy_parts_delegate` — two results (parent a dependency of child),
  `ModelPartDelegation`, `AllowRelease=TRUE`: a delegation body mid-flight
  while the session releases; the parent is pinned by the dependency
  edge. Checks `OwnershipExact`, `LazyAttemptDefersCollection`,
  `EvalDoneComplete`.
- `lazy_parts_release` — `lazy_release`'s question (per-group attempts
  versus release refusal) with two groups.

Each new config enters `expectedOutcome`; the cheap ones enter
`quickConfigs`. Each lands with its re-break (weaken the per-group latch:
let `EvalNoWork` trust a consumed callback while a sibling group's
bookkeeping is pending → `EvalDoneComplete` must trip) and its
reachability probes.

### 10.3 The budget ruling and the two-hour configuration

Erik's ruling: no configuration may take hours; target every configuration
under ~20 minutes; re-bound, split, or retire as needed. Evidence gathered
for this design (all runs on this worktree's pinned jar, 2026-08-31):

- `attach_release_reader` as configured (`MaxInvocations=3`): ~863M
  distinct states, ~2 hours (README figure).
- Re-bounded to `MaxInvocations=2`: 666,999 distinct states, **11
  seconds** — but the re-break (attachment-barrier error classification
  removed, spec line 1473 forced to FALSE) also **passes** at that bound,
  with the identical state count. The mechanism under test is unreachable
  at two invocations. Naive re-bounding is therefore exactly the
  green-over-unreachable-states cheat G30 forbids, and is ruled out.

The invocation bound is load-bearing (the scenario needs a dependency
producer, the publisher, and the parked cross-session reader). The
reduction must come from scenario scope, not the invocation count.
Directed plan, part of stage 2's model commit:

1. Add two scenario-scoping constants in the existing configuration style
   ("configurations select scenarios, never implementations"):
   `ReleaseSessions ⊆ Sessions` (which sessions may release; today
   effectively "all when AllowRelease") and `PersistableIntent ∈ BOOLEAN`
   (whether Spawn may choose persistable=TRUE). Defaults preserve every
   existing configuration's space (verified by distinct-state counts).
2. Re-scope `attach_release_reader` to `ReleaseSessions={s1}` (only the
   publisher's release is part of the question) and
   `PersistableIntent=FALSE` (persisted edges play no role in the
   scenario). Re-verify both re-break arms trip at the reduced scope: the
   classification drop must violate `NoSpuriousErrors`, and the
   claim-pinning probe must stay reachable.
3. If still over 20 minutes, split the configuration: the
   reader-conversion regression check and the pinned-target
   unreachability probe become separate configurations, each with the
   narrowest switches that keep its own re-break red.
4. Whatever bound finally ships, record the one-time exhaustive result in
   the configuration comment — the convention `lazy.cfg` already uses for
   its retired three-caller run — so the coverage decision is visible,
   not silent.
5. Apply the same procedure to any other configuration the dev box
   measures over 20 minutes after the stage-2 model lands
   (`resources_gated_growth` at ~114M distinct states is the likely
   second candidate; at this box's measured ~4M distinct states/minute it
   sits near 28 minutes).

The full-suite-before-push rule from `dagql/tla/README.md` stands.

---

## 11. Decisions ledger

Each entry: the decision, then the rationale. All are working positions
(G32).

- **S2-1. Attempts are per (result, group); parts map to exactly one
  group; the two-level part-plus-group exclusion structure is removed.**
  Locally a part has exactly one producer, so there is nothing to
  exclude; the exec's joint output is one group; the existing
  singleflight machinery applies verbatim per group. Stage 6 reintroduces
  a second producer (pull) as a deliberate remapping of parts between
  groups, modeled then. This contradicts engine-foundations 5.2's sketch
  and is flagged to Erik.
- **S2-2. Undeclared operations get one whole-result group — today's
  behavior byte-for-byte.** The default is the identity; refinement is
  opt-in per operation.
- **S2-3. Container parts are `metadata`, `fs`, `execMeta`,
  `mount:<target>`. Directory and File stay single-group in stage 2.**
  Their split buys no local laziness; adding it later is additive.
- **S2-4. Mount parts are keyed by target path, not index.** Targets are
  unique (`ContainerMounts.With`); target keys survive add/remove/replace
  with no index translation. Lease roles stay positional and stable
  because metadata settles before any mount part materializes.
  Contradicts engine-foundations 5.1 ("mount parts are positional");
  flagged.
- **S2-5. Metadata-first rule.** Any part evaluation or resolution
  settles the metadata part first. Positional and target-dependent
  questions get answers before snapshot work; metadata chains never touch
  snapshots, so the rule is cheap.
- **S2-6. One production per part.** A part is written once, by its
  group's body; delegation copies the parent's latched value; no body
  rewrites another group's part. This is the entire local answer to D11.
  Requires the exec-body VolatileEnv restructure (4.5, flagged).
- **S2-7. API: `Evaluate` unchanged (means everything);
  `EvaluateParts(res, parts...)` added.** Existing callers untouched;
  narrowing is incremental.
- **S2-8. Whole-result evaluation of a refined value runs groups
  sequentially, metadata first.** Closest to today's one-body execution;
  parallelism within one result solves no observed problem.
- **S2-9. Partially evaluated results persist in today's lazy form; no
  format change.** Partial progress is cheap to re-derive; expensive work
  is latched in ancestor rows. Stage 3 owns per-part persistence.
- **S2-10. Joiner/cancellation/retry/token machinery carries over
  verbatim per group; `lazySyncPending` becomes per-group.** The
  remediation-stack guarantees are extended by indexing, not re-derived.
- **S2-11. `syncResultSnapshotLeases` is serialized per result
  (`leaseSyncMu`).** Concurrent group attempts make concurrent syncs
  routine; the diff-based sync can transiently drop stored links under
  interleaving; a small mutex removes the case instead of arguing about
  eventual repair.
- **S2-12. Initial refined set (D13): wave 1 = metadata-only template
  (~29 ops), exec, withRootfs, the three selectors, 12 metadata readers,
  4 exec-meta readers. Wave 2 = mount mutations, target-resolved writers,
  export narrowing.** Wave 1 maximizes real wins (metadata reads,
  selector narrowing) for minimal surface; wave 2 is mechanical extension
  behind the same templates.
- **S2-13. Recursion stack keyed by (result, group).** A body may demand
  a sibling group of its own result (needed for metadata-first); genuine
  cycles are still refused.
- **S2-14. Model: per-group generalization with singleton-group
  isomorphism as the regression anchor (6,398,997 distinct states for
  `lazy`), new parts configurations, delegation-demand coverage, and the
  scenario-scoping budget procedure for `attach_release_reader`
  (evidence in 10.3).** Naive invocation re-bounding is ruled out by the
  measured re-break pass at bound 2.
- **S2-15. Session resources: no change; the argument that no new serve
  path exists is section 7.**
- **S2-16. `from(image)` and every source operation stay unrefined.**
  Their refinement is stage 8's job (`from` metadata) or has no local
  value now.

---

## 12. Implementation plan

Ordered small commits; behavior-preserving refactors first; each step
independently testable. Model before cache code (G30). Commit trailer:
`Signed-off-by: Erik Sipsma <erik@sipsma.dev>`.

1. **Model: per-group lazy section.** Spec generalization (10.1), inert
   constants added to all existing configurations, the `lazy`
   distinct-state isomorphism check (must equal 6,398,997), new
   configurations `lazy_parts`, `lazy_parts_prereq`,
   `lazy_parts_liveness`, `lazy_parts_delegate`, `lazy_parts_release`
   with re-breaks and probes; `expectedOutcome`/`quickConfigs` updates.
   *Test: full TLA suite green; recorded state counts.*
2. **Model: budget work.** Scenario-scoping constants; re-scope
   `attach_release_reader` per 10.3 with re-break verification; measure
   the suite; apply the procedure to anything else over 20 minutes.
   *This commit can land independently of 1 and may be reordered.*
3. **dagql refactor, no behavior change.** Introduce `lazyGroupState`;
   `sharedResult` holds `lazyWhole` inline; `evaluateOne`,
   `HasPendingLazyEvaluation`, `registerLazyEvaluation` operate through a
   group-state accessor that today always returns `&lazyWhole`.
   *Test: existing `dagql` suite unchanged.*
4. **dagql per-group machinery.** `PartKey`, `LazyGroupKey`,
   `HasLazyEvaluationParts`, `EvaluateParts`, the named-group map, the
   (result, group) recursion stack, per-group `HasPendingLazyEvaluation`
   and registration, `leaseSyncMu`. Unit tests in `dagql/cache_test.go`
   with a fake parts value mirroring the model scenarios: joint group
   fills all members once; two groups evaluate concurrently without
   serializing; per-group failure leaves siblings complete and the failed
   group retryable; foreign-cancellation retry per group; body-success
   sync-failure retries only bookkeeping per group; whole-result
   `Evaluate` over groups; recursion refusal for same (result, group) and
   allowance for sibling groups. *Model and code reconciled here.*
5. **core scaffolding, no behavior change.** Container part keys;
   `LazyState.EvaluateGroup`; `LazyContainerParts`; `Container`
   implements `HasLazyEvaluationParts` routing every existing op to
   `LazyGroupWhole`; `materializeContainerMetadataFromParent` and
   `delegateContainerPart` split out. *Test: engine container suites
   unchanged.*
6. **Template A conversion.** The ~29 metadata-only ops (mechanical; one
   or two commits). *Test: a pending metadata chain answers `workdir`
   with every snapshot group still pending (assert via accessor state /
   `HasPendingLazyEvaluation`).*
7. **Exec refinement.** Joint `execOutputs` group; metadata and read-only
   mount delegation; the VolatileEnv restructure with its consumer audit.
   *Tests: `from(img).withExec(["/bin/false"]).workdir()` succeeds while
   `.sync()` fails (the user-visible sharp edge, deliberately asserted);
   a read-only mount whose source is pending stays pending across a
   `workdir` read; `stdout` still runs the exec and returns output;
   failed exec group retries.*
8. **Selectors and readers.** `ContainerRootFSLazy` → parent `fs` only;
   `ContainerDirectoryLazy`/`ContainerFileLazy` → metadata + located
   part; the 12 metadata resolvers and 4 exec-meta resolvers switch to
   `EvaluateParts`; `metaFileContents` forcing moves to resolvers.
   *Tests: `ctr.directory("/a")` leaves an unrelated mount's pending
   source untouched; `stdout` leaves unrelated read-only-mount chains
   untouched.*
9. **Suite sweep.** Run the engine test suites; fix tests that relied on
   metadata reads forcing evaluation (switch them to `sync`). Update
   `internal-docs/lazy_evaluation.md` for the per-part contract.
10. **Wave 2 (may slip past tonight):** mount mutations; target-resolved
    writers; export-family narrowing. Each op lands with its own
    delegation test.

**Cut line for tonight:** commits 1–9. If the night shrinks further, cut
after 7 — the exec refinement plus template A is the minimum that makes
the headline behavior true; 8 is small and high-value, so prefer cutting
into 10 first, then 8's selector half before its reader half.

PR shaping (per engine-foundations stage-2 split guidance): commits 1–4 as
one PR (model + cache machinery), 5–9 as a second (core + schema).

---

## 13. Risks, and the items genuinely worth flagging to Erik

Risks the design accepts and manages:

- **User-visible timing change.** A failing exec no longer surfaces at a
  metadata read; it surfaces when a part that needs it is demanded, or
  never. This is the existing laziness contract applied consistently (an
  undemanded exec never ran before stage 2 either), and F10 names the
  per-part improvement a wanted change — but users and tests observe it.
  Managed by the commit-7 test asserting the new behavior on purpose and
  the commit-9 suite sweep.
- **Wrong write classification of a converted op.** Managed by the
  one-production rule (a missed write shows up as a missing accessor
  value, which `GetOrEval` reports as an explicit error, not silence) and
  per-op tests.
- **Body impurities beyond the two found.** The conversion of each op is
  a review of its body against "writes only its group's parts"; the
  VolatileEnv and ExecMD findings show the check is tractable per op.
- **Telemetry volume.** More (smaller) resume spans, named per group. No
  contract change; `HasPendingLazyEvaluation` semantics preserved.
- **Model budget estimates are estimates.** Managed by the measured
  anchors in 10.3, hard per-config measurement on landing, and the
  documented fallback (split, then record the exhaustive bound).

Items for Erik specifically (each one sentence of what changed and why):

1. **The two-level attempt structure from engine-foundations 5.2 is
   collapsed to single-level group attempts** (S2-1); the pull/run
   two-producer case that motivated it cannot occur before stage 6, and
   stage 6 gets it back as an explicitly modeled part-to-group remapping.
2. **Mount parts are target-keyed, not positional** (S2-4), against the
   engine-foundations sketch, because targets are verified unique and
   survive list edits.
3. **`withExec(...).workdir()` stops failing when the exec fails**; the
   error moves to the first demand of an exec output. This is the one
   behavior change a user can notice without reading code.
4. **The exec body's VolatileEnv rewrite is being removed** (kept local
   to the run) to satisfy the one-production rule; consumer audit is a
   named implementation step, and if the audit finds a reader of resolved
   values outside the run, the fallback is declaring VolatileEnv an exec
   output written by the joint group — which would make metadata reads on
   exec chains run the exec again, so the audit result matters.
5. **`attach_release_reader` cannot be honestly re-bounded by invocation
   count** (measured: the re-break passes at bound 2); the budget fix is
   scenario scoping (release restricted to the publisher session,
   persistable intent off) with re-break verification, splitting the
   config if that is not enough.
6. **Found in passing, not fixed (G24):** an evaluated exec child's
   `ImageRef` ends up equal to its parent's — `WithExec` clears it on the
   shell (`core/container_exec.go:1198`) but the body's parent-state copy
   restores it (`core/container.go:1073`). Per-part delegation preserves
   the evaluated-state behavior. If the shell-state behavior was the
   intended one, that is a pre-existing bug to rule on separately.
