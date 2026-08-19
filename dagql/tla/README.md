# TLA+ models of the dagql cache

Formal models of the dagql cache's result lifecycle and concurrency kernel,
checked with TLC. One spec (`CacheLifecycle.tla`), built in increments:

1. the live kernel — GetOrInitCall, publication, ownership/release, the
   read barrier;
2. lazy evaluation — the per-result singleflight in `Cache.Evaluate`
   (gated behind the `ModelLazy` constant; increment-1 configurations are
   unaffected);
3. import/flush — imported initial states with undecoded payloads, the
   persisted-decode arm of the read barrier, and one graceful
   flush+restart cycle (gated behind `ModelPersistence`; earlier
   configurations are unaffected).

Granularity: one atomic action per Go critical section. Verified against
main @ `f3cc3eb3f2` (2026-08-18); every action names the Go function and
lines it abstracts.

The spec is self-contained: its header documents the modeling rules and
every action names the Go function and lines it abstracts. Headline
abstractions:

- Equivalence is a static partition of call identities; lookup returns any
  live result in the request's class, and may spuriously miss (covers the
  accepted duplicate-execution window, `cache.go:3879-3883`).
- Model axiom, never verified: equal digests mean interchangeable values.
- `ReleaseSession` fires at any time — the server-side `dagqlInFlight`
  drain is deliberately not modeled; the cache must be safe without it.
- Fairness for liveness checking is stated explicitly at the bottom of the
  spec (`LiveSpec`): weak fairness on system/waiter progress only; none on
  cancellation, failure injection, release, or prune.

## Running

Requires Java 17+ and `tla2tools.jar` (v1.7.4 used here). `tla2tools.jar` alone suffices; no community modules are used.

```sh
java -XX:+UseParallelGC -cp tla2tools.jar tlc2.TLC \
    -workers auto -deadlock \
    -config CacheLifecycle_<name>.cfg CacheLifecycle.tla
```

`-deadlock` is required: the model quiesces by design (bounded spawn
budget), and TLC would otherwise report quiescence as a deadlock.
For `CacheLifecycle_liveness.cfg` drop nothing else — it selects `LiveSpec`
and the `EventuallyTerminal` property itself. Symmetry (`Symm`) is used
only in safety configs; TLC symmetry is unsound for liveness.

## Configurations

Bug toggles select between as-is code shape and fixed/guarded shape; each
config isolates one question. Expected outcomes:

| Config | Question | Expected |
|---|---|---|
| `fixed` | all fixes on, no external races: every safety property | green |
| `asis` | as-is code, release + prune enabled: graph-integrity core (P1, underflow, no-resurrection) | green |
| `bug_canonical` | pre-fix canonical-adoption shape (hold late, no guard): P2/P3 | **violated** — the historical use-after-release race of `cache_canonical_race_test.go` |
| `bug_persistable` | as-is joiner `isPersistable` stamp (OQ19, open): persistable request honored | **violated** — joiner's flag read early by publication, edge never lands |
| `bug_once_gap` | as-is `sync.Once` error-read TODO (`cache.go:4222`, open): returned results session-owned (P10) | **violated** — waiter returns mid-publication with no session edge |
| `bug_lost_cancel` | as-is lease-error branch (OQ19, open): cancels discharged | **violated** — `cancel` from `cache.go:3887` never called |
| `no_barrier` | attach barrier removed: no half-attached reads (P4) | **violated** — credibility check that the barrier is load-bearing |
| `release_inflight` | release during in-flight calls: returned results live (P2) | **violated** — cache-level finding; currently prevented only by the server's drain |
| `orphan_edges` | release during in-flight calls: no orphaned retention at quiescence | **violated** — post-release session-edge claims retain results forever |
| `finding_rollback` | hit-path rollback with a pre-existing edge (`alreadyTracked`) + attach failure: ownership exact (P1) | **violated** — candidate real bug, see below |
| `finding_poisoned` | persistable call whose attachment fails: attach-failed results not retained | **violated** — the persisted edge (added at `cache.go:4601`, before attachment at `:4618`) survives the failure; see below |
| `lazy_asis` | increment 2, as-is lazy coordination with callback failures injected: mutual exclusion, success-permanence, ownership exact | green |
| `lazy_no_singleflight` | per-result singleflight removed: at most one callback per result | **violated** — mechanism-removal check that the singleflight is load-bearing |
| `finding_lazy_stale_cancel` | fresh Evaluate joining a fully-abandoned attempt: no stale cancellation errors | **violated** — candidate real bug, see below |
| `lazy_liveness` | as-is lazy shape, fair scheduling: every Evaluate caller terminates | green |
| `liveness` | fixed shape, fair scheduling: every call terminates (P8) | green |
| `persist_asis` | increment 3, starting from imported graphs with undecoded payloads: graph-integrity core + decode mutual exclusion | green |
| `flush_roundtrip` | drained shutdown → flush → restart → new work: ownership re-established, flush clean, nothing laundered | green |
| `persist_liveness` | decode (with failures) under fairness: every caller terminates | green |
| `finding_rollback_decode` | the F3 rollback through the persisted-decode failure arm — its wide door | **violated** — reproduction evidence for F3 |
| `finding_flush_inflight` | flush racing an unfinished publication: snapshot captures only clean state | **violated** — finding F6, see below |
| `finding_poisoned_restart` | poisoned (attach-failed) retained entry across flush+restart: never served | **violated** — F4's restart consequence made formal |

## Candidate real findings

Numbered by their canonical ledger IDs; `FINDINGS.md` in this directory
is the single source of truth for each finding's full description and
status.

- **F1 — release during in-flight calls hands a caller a collected
  result** (`release_inflight`): if `ReleaseSession` runs while the
  session still has calls in flight, a caller can be handed a result whose
  OnRelease already ran. Today the server's `dagqlInFlight` drain
  (`engine/server/session.go:600-608`) prevents this; the cache has no
  defense of its own. Under independent investigation.
- **F2 — post-release session-edge claims leak retention permanently**
  (`orphan_edges`): `trackSessionResult`'s second critical section
  (`cache.go:284-291`) increments based on the `acquired` flag captured in
  its first section, never re-checking the session map. Under independent investigation.
- **F3 — hit-path rollback desyncs ownership** (`finding_rollback`,
  `finding_rollback_decode`): the error arm of `lookupCacheForRequest`
  (`cache_egraph.go:1002-1022`) deletes the session-map record
  unconditionally but decrements only when `!alreadyTracked`. Increment 3
  confirmed the realistic trigger: the persisted-decode failure arm
  reaches the same rollback, and two same-session hits on an imported
  encoded entry plus one decode failure suffice. Under independent investigation; the decode
  config is reproduction evidence.
- **F4 — a persistable attach failure leaves a poisoned, retained entry**
  (`finding_poisoned`, `finding_poisoned_restart`): the persisted edge is
  upserted inside the publication critical section (`cache.go:4601`)
  before attachment runs (`:4618`); the attach-error path drops only the
  handoff hold. Every future equivalent call fails on the entry (error,
  not miss). `attachDepsErr` has no cleanup path and lives only in
  memory, so flush+restart launders the entry into a clean-looking,
  servable one whose attachment never completed — increment 3 demonstrates
  the full cycle. Maintainer decision pending.
- **F5 — a fresh Evaluate can fail with another caller's cancellation**
  (`finding_lazy_stale_cancel`): a new Evaluate joining a fully-abandoned
  lazy attempt inherits its cancellation error — spurious failure on a
  healthy, retryable result. Narrow window; severity a judgment call.
  Maintainer decision pending.
- **F6 — an undrained flush captures mid-publication state**
  (`finding_flush_inflight`): `Cache.Close`'s snapshot is atomic against
  critical sections but not against a publication split across them; with
  sessions released but calls still in flight, the snapshot can contain a
  result whose attach barrier is still open, and the restarted engine
  imports it as clean. Same drain-dependency family as F1/F2. Maintainer
  decision pending.

One acyclicity note: dependency cycles would make results permanently
uncollectable, silently. The Go cache does not prevent them
(`addExplicitDependencyLocked` rejects only self-edges) — acyclicity holds
because structural deps point from newer IDs to older ones and explicit
attachment edges mirror object graphs that core producers build as DAGs.
The spec bakes that in as a stated assumption (the `NoCycle` guard in
`PubAttachAddDep`) so collection-liveness properties stay meaningful.

## The CI check, and what to do when it fails

`dagger check tla-check:cache-lifecycle` (module source:
`.dagger/modules/tla-check`) runs every configuration in this directory and
verifies each outcome against its expectation — the greens above must pass,
and each red must violate *exactly* its designated invariant. The red
configurations are self-verifying reproduction artifacts: they prove the
model still reproduces the bug or finding they document.

You do not need to know TLA+ to act on a failure. The check's message names
the configuration and the violated-vs-expected property; the three cases
are:

- **A green configuration reported a violation.** Someone changed the spec
  (or a config) in a way that breaks a verified property of the modeled
  cache behavior. Reproduce locally with the command below, read the trace
  bottom-up (the last state is the violation), and either fix the spec
  change or — if the spec is right and the model was wrong before —
  update the expectation *with* a FINDINGS.md entry explaining the trace.
- **A red configuration came up clean.** The model or config no longer
  reproduces its documented bug/finding. Usually this means a spec edit
  accidentally "fixed" the seeded behavior; restore it, or if the
  underlying bug was genuinely fixed on main, move the toggle's fixed
  shape to the default and update the findings ledger.
- **A red configuration violated a different invariant.** The config
  drifted; diff it against the table above.

Reproduce any single configuration locally (same jar, same invocation the
check runs):

```sh
java -XX:+UseParallelGC -cp tla2tools.jar tlc2.TLC \
    -workers auto -deadlock \
    -config CacheLifecycle_<name>.cfg CacheLifecycle.tla
```

## Files

- `CacheLifecycle.tla` — the spec. Constants documented in the header.
- `CacheLifecycle_*.cfg` — one TLC configuration per question above.

Note on lazy-evaluation configs: they keep `AllowRelease` and
`AllowPruneCut` off. What happens when a result is collected while its
lazy callback runs belongs to the release-during-in-flight family under
separate investigation.

The v1 modeling scope (live kernel, lazy evaluation, import/flush) is
fully built. Deliberately deferred beyond v1, each awaiting a declared
property before it earns model state: session resources (the handle
gating on cache hits), TTL/expiry as a nondeterministic entry state,
DoNotCache, e-graph internals (dynamic equivalence merging), recipe
replay, and lazy callbacks on imported entries (persisted lazy forms
decode into values with deferred work; coupling the lazy and import
machineries had no declared property in v1).
