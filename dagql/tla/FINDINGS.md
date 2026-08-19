# Findings Ledger

Every finding the cache TLA+ modeling produced, in one place. Each entry
carries a plain-English description, the TLC configuration that reproduces
it mechanically, code references, and current status. Traces are explained
in the configurations' comments and in `README.md`; the spec is
`CacheLifecycle.tla`.

## A. Model-produced findings

Output of the model checker, not seeded. Each reproduces mechanically from
its configuration.

### F1 — Release during in-flight calls hands a caller a collected result

If `ReleaseSession` runs while the session still has calls in flight, an
in-flight caller can be returned a result whose OnRelease already ran.
Today only the server-side drain (`dagqlInFlight`) prevents the ordering;
the cache has no defense of its own.

- Repro: `release_inflight` (violates `ReturnedLive`)
- Code: `cache.go:759-833` (ReleaseSession); `engine/server/session.go:600-608`
  (the drain)
- Status: under independent investigation

### F2 — Post-release session-edge claims leak retention permanently

`trackSessionResult`'s second critical section increments ownership based
on the `acquired` flag captured in its first section — never re-checking
that the session's map entry still exists. A release landing between the
two sections leaves an increment with no releasable record; a concurrent
re-claim can land two increments against one entry. Affected results are
retained forever.

- Repro: `orphan_edges` (violates `NoOrphanEdgesAtQuiescence`)
- Code: `cache.go:259-291`, esp. `:284-291`
- Status: under independent investigation

### F3 — Hit-path error rollback desyncs ownership accounting

The error arm of `lookupCacheForRequest` deletes the session-map record
unconditionally but decrements only when this call created it
(`!alreadyTracked`): a second same-session hit rolling back removes the
record for an increment that stays counted. The trigger via attach failure
is narrow; the persisted-decode failure arm reaches the same rollback and
is the wide door, confirmed by the model: two same-session hits on an
imported encoded entry plus one decode failure suffice.

- Repro: `finding_rollback` (attach-failure trigger);
  `finding_rollback_decode` (decode-failure trigger). Both violate
  `OwnershipExact`.
- Code: `cache_egraph.go:1002-1022`; decode arm
  `cache_persistence_import.go:573-701`
- Status: under independent investigation

### F4 — A persistable attach failure leaves a poisoned, retained, lookup-visible entry

The persisted edge is upserted inside the publication critical section,
before attachment; the attach-error path drops only the handoff hold, so
the edge survives. Every future equivalent call hits the entry and fails
(error, not miss — no fall-through to execution). `attachDepsErr` has no
cleanup path anywhere; the error lives only in memory, so shutdown flushes
the entry normally and restart re-imports it without the error, serving a
payload whose attachment never completed. Recovery today: prune or store
wipe.

- Repro: `finding_poisoned` (violates `NoRetainedPoisonedEntry`);
  `finding_poisoned_restart` (the flush/restart laundering made formal —
  violates `NoLaunderedServe`)
- Code: `cache.go:4601` (edge upsert), `:4618` (attach), `:4618-4638`
  (error path); `cache_persistence_import.go:545-571` (read barrier)
- Status: maintainer decision pending

### F5 — A fresh Evaluate can fail with another caller's cancellation error

After the last waiter abandons a lazy evaluation (which cancels it), the
attempt's wait channel stays published until the dying callback exits. A
new `Evaluate` in that window joins the dying attempt — the join check is
only "a wait channel exists" — and is handed the abandoners' cancellation
error: a spurious, caller-visible failure on a healthy, retryable result.
A second route: a last waiter in the finished-but-undrained state can
leave via its own ctx.Done select arm, which clears nothing, leaving the
latched error for the next joiner to drain. Narrow windows; severity a
judgment call.

- Repro: `finding_lazy_stale_cancel` (violates `NoStaleCancelError`)
- Code: `cache.go:2990` (evaluateOne join arm ~`:3038-3053`),
  `:2934-2944` (abandon arm clears nothing)
- Status: maintainer decision pending

### F6 — An undrained flush captures mid-publication state

`Cache.Close`'s snapshot copies whatever is registered under one
`egraphMu` hold — atomic against critical sections, but publication is
deliberately split across several: a result can be indexed
(lookup-visible, snapshot-visible) while its dependency attachment is
still running outside the lock. A shutdown that has released sessions but
not waited for their in-flight calls can flush a result whose attach
barrier is still open, retained partly by a transient handoff hold; the
restarted engine imports it as a clean, servable entry. *Read this as
exposure-if-drain-incomplete, not a reachable-today bug:* through the
graceful path, `GracefulStop`'s per-session removal drains `dagqlInFlight`
before each release, so the triggering ordering should be unreachable
today. The finding is that the flush-time invariant — "the snapshot never
captures an open barrier or a transient hold" — lives entirely in that
server-side politeness, not in the cache or the flush itself. Same
drain-dependency family as F1/F2.

- Repro: `finding_flush_inflight` (violates `FlushCleanCapture`)
- Code: `cache_persistence_worker.go:27` (snapshotPersistState),
  `engine/server/server.go:771` (GracefulStop ordering),
  `session.go:600-608` (the drain)
- Status: maintainer decision pending

## B. Known bugs used as model validation targets

Pre-existing bugs (fixed or open) that the model was required to
rediscover, proving it finds this class mechanically.

### B1 — Canonical-adoption use-after-release

Pre-fix, the canonical-equivalent pick and the handoff hold were separate
critical sections; a session release in the gap collected the adopted
sibling, and publication resurrected the corpse. The Go regression test
choreographs this with mutex starvation and admits it can misfire; TLC
exhibits it unconditionally.

- Repro: `bug_canonical` (violates `NoResurrection`)
- Code: fix at `cache.go:4319-4329` (hold inside the pick's section) +
  `cache_egraph.go:1548` (resurrection guard); test:
  `dagql/cache_canonical_race_test.go`
- Status: fixed on main

### B2 — A joiner's IsPersistable request is silently lost

A joiner stamps `oc.isPersistable` under `callsMu`; publication reads the
flag in its own `egraphMu` section with no `callsMu`, and the entry leaves
`ongoingCalls` only later — so a persistable joiner landing after the read
gets no persisted edge. Reachable at critical-section granularity, not
just a memory-model technicality. (The spec's `FixJoinerPersistable` is a
hypothetical fix for the green-matrix run; no fix exists on main. A
revised protocol is in progress.)

- Repro: `bug_persistable` (violates `PersistableHonored`)
- Code: `cache.go:3861-3862` (stamp), `:4600-4601` (lockless read)
- Status: open on main

### B3 — Waiter passes the sync.Once before the publication error is written

Acknowledged TODO in code: a second waiter skips the Once mid-publication,
reads the unwritten error as nil, and returns without a session edge (and
possibly with an unindexed result).

- Repro: `bug_once_gap` (violates `ReturnedOwned`)
- Code: `cache.go:4222` (the TODO)
- Status: open on main

### B4 — Lost cancel on the lookup-miss path

The `withOperationLease` error branch returns without calling the
`WithCancelCause` cancel created just above. Impact: an undischarged
cancel (the parent is a WithoutCancel context) — a leak, not corruption.

- Repro: `bug_lost_cancel` (violates `NoLostCancels`)
- Code: `cache.go:3887` (cancel created), `:3907-3915` (branch that drops
  it)
- Status: open on main

Two further red configurations are *mechanism-removal checks*, not bugs:
`no_barrier` (delete the dependency-attachment barrier → readers observe
half-attached results) and `lazy_no_singleflight` (delete the per-result
singleflight → two callbacks run concurrently). They exist to prove those
mechanisms are load-bearing.

## C. Documentation discrepancies found during the modeling work

### D1 — Stale file reference in egraph docs

`internal-docs/egraph.md` references `dagql/call/id_content.go`, which no
longer exists; content-preferred digest derivation lives in
`dagql/result_call_frame.go`.

- Status: fix in progress

### D2 — Wrong default concurrency key in cache docs

`internal-docs/cachebasics.md` says the default concurrency key is the
client ID; the code uses the session ID (`dagql/objects.go:607`).

- Status: fix in progress

## D. Stated model assumptions (not findings, on record)

- **Equal digests mean interchangeable values.** Taken as an axiom; the
  model never verifies it. Stated in the spec header.
- **Dependency edges never form a cycle.** True today by producer
  convention (structural deps are acyclic by ID ordering; explicit edges
  mirror DAG-shaped object graphs) but unenforced by the cache —
  `addExplicitDependencyLocked` checks only self-edges. A cycle would make
  its members silently, permanently uncollectable. Stated in the `NoCycle`
  guard comment in `PubAttachAddDep`.
