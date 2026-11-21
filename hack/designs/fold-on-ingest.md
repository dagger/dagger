# Fold-on-ingest: a live CLI that doesn't keep every span

Status: research + spike (`dagql/dagui/foldoningest_spike_test.go`). Code
references are to `main` at `c1f8e9ae`.

## Problem

The live `dagger` CLI keeps every span of the session in `dagql/dagui`'s
`DB`. A long multi-agent session with ~9.9M spans, most of them from nested
engines (integration tests, benchmarks) under rows nobody expanded, got the
CLI OOM-killed at ~20 GB. `BenchmarkSpanFootprint` puts today's cost at
**~1962 B/span** (1M spans; 1000 B `Span` struct, 18 objects/span), so 10M
spans is ~19.6 GB before logs and metrics — matching the OOM.

The "priority-first live loading" plan has the engine send only priority
spans plus expanded subtrees through a new incremental server-side view, with
roll-ups computed by the engine. Much of its complexity comes from Cloud's
constraint (don't ship all the data to the client). A local CLI doesn't have
that constraint: it already receives the whole live stream (`GET /v1/traces`,
`engine/server/session.go:2757`).

## Idea

**Fold each span into its retained ancestors' aggregates when it arrives, keep
only a stub for it, and load children lazily when the user expands a row.**

- The push stays as it is: the CLI still sees every span event.
- Spans the CLI may render are *retained*: they go into today's `DB` in full.
- Every other span is *folded*: its contribution (roll-up category, running,
  failure, has-logs, child count) is applied to its retained ancestors, and
  all that's kept of it is a ~31-byte stub.
- Expanding a row whose children are folded reads their latest snapshots from
  the engine's live store, which already indexes the tree in memory, and
  retains them from then on.

This is exactly what the CLI already does for **logs**: with
`EngineLogsPresence` the engine pushes one body-less presence record per span
instead of display text (`engine/server/telemetry_logs.go:29-71`), and the
frontend reads display text for the spans it renders, one-shot or following
(`POST /v1/logs/query`, `displayLogFetcher` `:116`; `idtui.LiveLogsFrontend`,
`dagql/idtui/live_logs.go:26`). Fold-on-ingest is the same move for spans.

## Measurements (spike)

`dagql/dagui/foldoningest_spike_test.go` implements the stub table
(`foldIndex`) and one aggregate end to end — the roll-up counts that
`Span.updateRollUpAncestors` (`dagql/dagui/spans.go:932`) maintains today —
over the `spanfootprint` synthetic agent session, exported live (each span
once as it starts, again as it ends). Heap after GC, `runtime.ReadMemStats`:

| | 1M spans | 10M spans |
|---|---|---|
| today (`BenchmarkSpanFootprint`) | 1962 B/span (1.96 GB) | ~19.6 GB (extrapolated) |
| stub alone, per folded span | **32.2 B** | **30.9 B** (309 MB) |
| fold, `top` (2% retained) | 75 B/span | 72 B/span (722 MB) |
| fold, `kinds-nested` (7.3% retained) | 173 B/span | 171 B/span (1.7 GB) |
| fold, `kinds` on the agent shape (31% retained) | 637 B/span | — |

- `top` retains the root and its children only; everything else folded. It
  isolates the stub cost.
- `kinds` also retains surfaced kinds (checks, tests, LLM messages and tool
  calls, agents, services, rewind markers), failed and revealed spans that
  may roll up to the root, and all their ancestors. `spanfootprint`'s
  messages and tests mostly sit outside any boundary, so almost a third of it
  is retained: a retained span still costs today's ~2 KB, so **the win is
  exactly the folded fraction**.
- `kinds-nested` is the same trace with each call tree under an agent loop
  made a boundary, like tool calls running nested engines — the OOM case.
  What runs beneath them stops surfacing, and 93% folds.

The synthetic trace has no logs or metrics, so neither column counts them.

CPU, 200k spans × 2 events, single run, with the generator's ~3.0 µs/span
subtracted: today ~12.5 µs/span; fold-top ~0.6 µs; kinds-nested ~1.2 µs;
kinds ~3.8 µs. The generator builds OTLP-shaped spans, a stand-in for the
decode the CLI still pays for every span event either way, so the CLI's
per-span floor under fold-on-ingest is ~3.5–4 µs, against ~15 µs today. A
folded span is a hash lookup, a few attribute compares and an O(depth) walk.

Run them with:

```
go test ./dagql/dagui -run TestFoldOnIngest -v
go test ./dagql/dagui -run '^$' -bench 'FoldOnIngestFootprint|SpanFootprint' \
  -benchtime 1x -footprint.sizes 1000000
go test ./dagql/dagui -run '^$' -bench FoldOnIngestCPU -benchtime 1x
```

### The stub

Struct-of-arrays, indexed by a dense `uint32` (the engine's `spanLookup` uses
the same layout, `engine/clientdb/span_lookup.go:20-112`):

| field | bytes | why |
|---|---|---|
| span ID | 8 | identity; the ID table compares against it |
| ID table slot | ~5–11 (amortized 4/0.75 at 2× growth) | open addressing over indexes only; a Go `map[SpanID]uint32` would cost ~3× |
| parent | 4 | fold path to retained ancestors; late linking |
| first child, next sibling | 8 | recompute a subtree's aggregate on promotion or late link without an engine round trip |
| state | 2 | roll-up category (3 bits), received, retained, expanded, boundary, encapsulate, UI passthrough, internal, roll-up-logs, has-logs, kind, reveal |

≈31 B/span measured. Dropping the child lists (8 B) is possible if promotion
asks the engine for subtree aggregates instead; adding an interned name index
(4 B) would make `/` name search cover folded spans locally.

### Ordering, as the spike surfaced it

- **Start then end.** A stub keeps its last category; an update moves its
  count from the old category to the new one on every retained ancestor
  (`foldIndex.setCategory`), the same decrement/increment today's
  `updateRollUpAncestors` does via `lastRollUpCategory`.
- **Children before parents.** A child interns its parent as an unreceived
  stub and links to it. When the parent arrives and links to *its* parent,
  the subtree that arrived early must be credited to the newly reachable
  ancestors: `foldIndex.link` sums the subtree over the child lists and adds
  it up the chain. O(subtree) per late link; rare in a live stream (spans are
  exported when they start, `spanfootprint.Trace.Export`), common only in
  reversed imports.
- **Today gets this partly wrong.** `ExportSpans`/`ImportSnapshots` record
  and link a whole batch before `propagateStatus` (`db.go:519-567`), so a
  subtree and its parent in the same batch are fine. But when a parent
  arrives in a *later batch* than its subtree, `updateRollUpAncestors` only
  propagates the late span's own category, never the subtree's: in
  `TestFoldOnIngestLateParents` (each span once, children first, 512-span
  batches) **2394 of 11766** retained spans have wrong counts; the same spans
  in one batch, none. The impact today is small — `RollUpState` is only
  rendered on `ui.rollup.spans` rows (`renderRollUpDots`,
  `dagql/idtui/frontend_pretty.go:9741`), and live streams send parents
  first — and the same late-link gap affects `RunningSpans` and
  `RevealedSpans` on the newly reachable ancestors (`spans.go:862-900`).
  It matters for this design because **promotion makes children-first the
  normal path into the full `DB`** (next bullet). The fold layer's own counts
  match a from-scratch count in both orders (`TestFoldOnIngestRollUp`,
  `TestFoldOnIngestLateParents`).
- **Promotion mid-stream.** Retaining a span retains its ancestors
  (`foldIndex.promote`): each folded ancestor is read back from the store at
  its latest snapshot and its aggregate is recomputed over its stub subtree.
  The `kinds` run read back 5355 of 30000 spans that way; `kinds-nested` 783.
  An unreceived ancestor is marked retained and ingested in full when it
  arrives, and then so are *its* newly linked ancestors (asserted: no
  retained span has a folded parent). The spike reads ancestors back
  synchronously, into the same batch as the span; a real read is a round
  trip, so the span reaches the full `DB` before its ancestors. Either the
  fold layer holds a promoted span until its ancestors' read lands, or the
  `DB` fixes the late-link gap above for counts, running and revealed sets
  (re-running `PropagateStatusToParentsAndLinks` over the late subtree, as
  `recreditAncestors` does for activity, `spans.go:1252`).
- **Retention decided before the ancestry is known.** Containment
  (`spanMayRollUp`, `dagql/dagui/db.go:1032`) walks ancestors' Boundary and
  Encapsulate flags, which a child may not have yet. The spike retains a kind
  conservatively when an ancestor is unknown (11766 retained children-first vs
  8925 in live order); demotion would reclaim them. A row's children are
  retained through an `expanded` bit on the row, not a check at arrival, so a
  root's children arriving before the root are read back when it arrives
  (`foldIndex.expand`; asserted: nothing received beneath an expanded row is
  folded).
- **Stale reads vs the push.** A read of a span's children returns rows as
  of some store cursor while the push keeps delivering. Spans are marked
  retained *when the read is requested*, so push events from then on are
  ingested in full; a read row only fills spans not yet received in full. The
  push delivers every row in order, so the DB converges once the push passes
  the read's cursor. No version vectors needed: span snapshots are cumulative
  (`span_lookup.go:34-39`).

## Derived-data inventory

What the TUI reads from spans it doesn't render, and what fold-on-ingest
keeps per folded span.

| derived data | where today | per folded span | treatment |
|---|---|---|---|
| roll-up counts / bars | `updateRollUpAncestors` `spans.go:932`, `RollUpState` `:35`; counts on **every** ancestor incl. causal | category (3 bits in stub) | aggregate on retained ancestors; recomputed from stubs on promotion. **Prototyped.** Only `ui.rollup.spans` rows render it (`frontend_pretty.go:9741`), so only those strictly need the counts. |
| running indicator | `RunningSpans` set on every ancestor (`spans.go:862-868`); lazy walk's `runningEdges` from `db.runningSpans` (`walk_lazy.go:165`) | category | running count on retained ancestors (= `RollUpState.RunningCount`). Caveat: `runningEdges` only counts running spans the view would *place* (skips hidden ones); a count over stubs must skip `internal`/encapsulated stubs to match. `PendingReason`'s running names degrade to a count. |
| `Activity` (duration bars) | own interval, plus effects' time to causes (`spans.go:870-887`, `Activity.Add` `db.go:1203`) | nothing (direct parents don't take children's activity) | only causal effects contribute: see links row |
| failure | own status; `FailedLinks` via causal links only (`spans.go:832-849`) | category | failed count on retained ancestors; failed spans that may roll up are retained (as Cloud's priority set, `clientdb/selection.go:303`); failures under a boundary/encapsulation only count |
| error origins | `ErrorOrigins` set from links / status (`db.go:1414-1438`); `r` → `goErrorOrigin` (`frontend_pretty.go:8431`) | nothing | origins referenced by a retained failed span are loaded on demand with their ancestor closure (precedent: `unreceivedErrorOrigins` in `core/trace_inspect.go:461`) |
| child counts / expand affordance | `countChildren` flattens UI-passthrough children (`spans.go:267`); `Snapshot` raises `ChildCount` | child-list entry + UI-passthrough bit | `ChildCount` of a retained span from its stub children (flattening UI-passthrough stubs); `requestSpans` already gates lazy expand on `ChildCount` (`frontend_pretty.go:3045`) |
| has-logs | `ingestLogs`/`ingestLogPresence` do `db.initSpan(id).HasLogs = true` (`db.go:852`, `:885`) — **a ~1 KiB placeholder per folded span with logs** | 1 bit | set the stub bit instead of creating a placeholder; the read's rows can also carry `ui.has_logs` (`telemetryattrs.UIHasLogsAttr`) |
| `ui.reveal` | `RevealedSpans` up to the nearest boundary (`spans.go:889-900`) | 1 bit | revealed spans retained (they render in their ancestors' place) |
| check/test/message/service/generator surfacing | `kindSpans` (`kind_index.go:12`) + `spanMayRollUp`/`testMayRollUp` (`tests.go:938`) walks of ancestors' flags | boundary/encapsulate bits | kinds that may roll up are retained; containment computed on stubs. Test boundaries don't contain subtests, so the stub needs a "test" bit for `testSpanHasNode`. Agents are retained regardless of containment (the roster is session-wide, `db.go:278-282`) |
| encapsulation / UI-passthrough flags | read on ancestors by the walk, `countChildren`, `rollUpSpanOf` | 4 bits | in the stub |
| progress bars | `ProgressSpans` on every ancestor (`progress.go:150`) | — | progress-carrying spans retained if they may roll up (they're real pulls, few); otherwise dropped |
| cause/effect links | effect joins the cause's `ChildSpans` (`db.go:1399`); status, failure, running, activity and pending resolution flow effect→cause via `attributedCauses` (`spans.go:1227`), `continuations` (`:1323`), install sites | link targets (side table, only for spans with cause links) | **open choice**: retain both ends of every cause link (simple, and effects render as the cause's children anyway), or fold them and reimplement `attributedCauses`' rules over stubs. The engine's child read already follows cause edges (`ChildSpanIDs`, `telemetry_store.go:653`) |
| wait links / time breakdowns | `LinkPurposeWait` makes a placeholder (`db.go:1405-1413`); `TimeBreakdownSpans` fetch rounds | nothing | waited-on spans loaded on demand, as `dagger trace` does (`frontend_pretty.go:2893`, `trace.go:449-458`) |
| "pending forever" | `IsPending` resolved by continuations (`spans.go:1292`) | — | pending resolution is causal: same choice as cause links |
| rowWaits ("waiting below rows") | per-span `waitSummary` over the **whole** subtree, bottom-up (`rowwaits.go:56-81`, `:340`); 3.2 GB in the OOM | nothing | not foldable incrementally (interval subtraction across wait chains). Compute only over retained subtrees, with the existing `PartialTrace` semantics: a row whose subtree is partly folded reads its own breakdown (`rowwaits.go:39-40`, `:344`). Its memory drops to retained spans only. Caveat: `PartialTrace` counts a subtree loaded only when `childrenReported` says so (`childrenLoaded`, `rowwaits.go:539`), which only a `ui.child_count` attribute sets (`spans.go:750-754`) and the live push never carries. The fold layer must set it on every retained span from the stub child lists and keep it current as children arrive (leaves included), or turning `PartialTrace` on blanks rowWaits everywhere. |
| agent roster, conversation | `agentSpans` index (`agents.go:167`), `SurfacedConversation` (`conversation.go:25`); agent control from logs | — | loop, message and tool-call spans are retained kinds |
| call payloads | `db.Calls` from span attrs (`db.go:1454-1475`) and the log channel (`callpayloads.go:35`); `Intervals`/`CreatorSpans` by digest/output (`db.go:1440`, `:1511`) | nothing | keep payloads only for retained spans' digests; fetch recipe closures on demand through the existing `SetCallProvider`/`requestCalls` (`frontend_pretty.go:2920`) backed by a live call-closure read (`clientdb.CallClosure` is served for archives only, `engine/server/archive.go:637`) |
| log routing by digest | records carrying `dag.digest` (presence records too, e.g. service stdio, `engine/server/telemetry_logs.go:90`) route to the output's creator through `CreatorSpans` (`routeLogTarget`, `db.go:1713`); without a creator they park in `pendingLogsByOutput` (`db.go:844`) | output digest, for folded creators | an output-digest → stub index for folded spans with outputs — not free: most call spans have one, so ~30–40 B more per folded call span — or have the engine resolve the target span before pushing presence (nothing in the CLI). Otherwise those records park forever and has-logs never reaches the stub |
| agent rewinds | marker spans found through `kindSpans` (`AgentRewindMarker`, `kind_index.go:21`); `buildRewinds` walks the LLM state chain through `db.Call` and returns nothing when a payload is missing (`rewind.go:86`, `:159`) | — | markers are retained kinds (the spike's `foldKind` includes them); the calls between `AgentRewindFrom` and `AgentRewindTo` must be kept or fetched (`requestCalls`) before rewinds are built, or abandoned messages silently stop being marked superseded |
| log roll-ups, live follow | `rollUpSpanOf` walks `ParentID` through `DB.Spans.Map` (`frontend_pretty.go:10549`); presence mode reads roll-ups with `descendants` | roll-up-logs/boundary/encapsulate/internal bits | walk stubs instead; text is already read on demand and followed by the engine's `LiveLogScope`, which places late spans itself |
| metrics | `MetricsBySpan`, `MetricsByCall` (`db.go:929`); 1 GB in the OOM | nothing | keep for retained spans/digests only; `LLMTokenMetrics` is global and stays |
| search `/` | name scan of `db.Spans.Order` (`dagql/idtui/search.go:250`); remote log hits placed at the nearest loaded ancestor, else "N in spans not loaded" (`search.go:155-178`) | optionally a 4 B name index | names: local name index, or an engine span search beside `/v1/logs/search`; hits placed by the existing remote-hit path, which loads the ancestor closure to place them |
| tests view `T` | `TestIndex` (`tests.go:419-746`) | test bit | retained tests only, which are the ones it shows (`testMayRollUp`) |
| root end cancels running | scans `db.Spans.Order` (`db.go:1489-1500`) | category | one O(N) pass over the stub states |
| remote frontends (`UpdatedSnapshots`) | `db.go:391` | — | **no production caller**: only `db_test.go`. Meanwhile `db.updatedSpans` keeps a pointer to every span forever (`db.go:186-194`). Not a constraint. |

## What is retained, and how a row moves

Retained:

1. the root and the top-level rows;
2. surfaced kinds that may roll up to the root (checks, tests, generators,
   services, LLM messages and tool calls, agents — the last regardless of
   containment);
3. failed, revealed and progress-carrying spans that may roll up;
4. what the user expanded or zoomed into, and search/error-origin targets;
5. the ancestors of all of the above (`AncestorClosure`).

Expand a row X with folded children:

1. The frontend asks for X's children (`requestSpans`, gated on `ChildCount`
   from the stubs).
2. The fold layer sets X's `expanded` bit and marks its stub children
   retained, so push events for them are ingested in full from now on, and
   **children that arrive later are retained as they come** (a child of an
   expanded row is retained at arrival). Without the bit, a running row
   expanded once would never show new children: `requestSpans` asks once per
   span (`requestedSpans`, `frontend_pretty.go:3070`).
3. A live read returns their latest snapshots, stamped with child counts and
   has-logs; they go through `ImportSnapshots`/`ExportSpans`, filling only
   spans not received in full since step 2.
4. Their aggregates are recomputed from the stubs beneath them.

`TestFoldOnIngestExpand` expands a running agent loop halfway through a
session: 143 folded children are read back, and all 304 it ends up with are
retained.

Collapse: nothing has to happen. The aggregates are maintained over stubs
whatever is retained, so a collapsed running subtree keeps updating like any
retained span. Demotion — clear `expanded`, drop the `*Span`s, keep the stubs
— is an optional LRU, because what the user looked at is bounded by what they
looked at.

### Reusing the existing lazy-load path

The client half is there: `SetSpanProvider`, `requestSpans`/`requestSubtree`
(`frontend_pretty.go:2860`, `:3045`, `:3066`), `ImportSnapshots` (`:3089`),
`PartialTrace` (`db.go:151-156`). The server half used by `dagger trace`
and restore is **not** reusable live:

- the archive endpoints refuse an active archive (`engine/server/archive.go:521-523`);
- `SpanViewAt` builds a topology over every span row, O(N) per cut
  (`clientdb/selection.go:265-282`; 81 ms at 100k), and a live cut moves
  with every batch, so nothing is ever cached.

But the live store already keeps exactly the index a live read needs,
incrementally: `spanLookup`'s parent, children and newest-row-per-span
(`span_lookup.go:40-61`), behind `ChildSpanIDs`, `AncestorClosure`,
`SelectSpansLatest` (`telemetry_store.go:653-690`). `core/trace_inspect.go:449`
does this scoped live load today for the MCP trace tools.

## Engine changes

Small, and none of them computes a view:

1. **`POST /v1/traces/query`**: given span IDs and `children`/`ancestors`/
   `exact`, return the newest snapshot row of `ChildSpanIDs(id)` ∪
   `AncestorClosure(...)`, stamped with `ui.child_count`
   (`spanLookup.directChildren`) and `ui.has_logs` (the log index), plus the
   store cursor. Registered beside `/v1/logs/query`
   (`engine/server/session.go:2750-2762`), advertised as a live feature like
   `LiveFeatureLogQuery` (`engine/telemetry/livestream.go:23`). Cost per read:
   O(result), not O(trace).
2. **Live call closure** (optional): `clientdb.CallClosure` on the live mux,
   as the archive serves it.
3. **Span name search** (optional, or keep a local name index).

The push, the store, Cloud and archives stay as they are.

## Versus priority-first

| | fold-on-ingest | priority-first |
|---|---|---|
| CLI memory, 1M spans | ~31 MB stubs + retained (75–173 MB in the spike) | retained only |
| CLI memory, 10M spans | ~310 MB stubs + retained (0.72–1.7 GB in the spike) | retained only |
| CLI CPU / bandwidth | still receives and decodes every span event (~3 µs decode-equivalent + ~0.6–1.2 µs fold per span) | only priority + expanded |
| engine | one O(result) read endpoint | incremental server-side view per session, engine-computed roll-ups, invalidation |
| where derived logic lives | CLI only, split between stub-level folds and today's full path | split between engine (roll-ups) and CLI (rendering), which must agree |
| ordering problems | in one process, against one stream (above) | across two processes and two streams |
| Cloud / archives | untouched | shared design pressure |

Fold-on-ingest is simpler on the engine and keeps one source of truth for
derived state. Its cost is a ~31 B/span floor that grows with the session
(~310 MB at 10M), and every span still crossing the wire and being decoded.
Priority-first scales better in the limit; fold-on-ingest is likely enough
for the sessions we have, and the engine read it needs is also a building
block priority-first would use.

## Where the complexity lands

Not in the engine. In `dagui`, in four places:

1. **Two ingest paths that must agree.** Every aggregate an ancestor reads
   today from per-span sets (`RunningSpans`, `FailedLinks`, `rollUpState`,
   `ProgressSpans`, `RevealedSpans`, `ErrorOrigins`) needs its folded
   contribution too. Roll-up counts were easy; the rest are sets used for
   *names* in reasons and need degrading to counts or on-demand loads.
2. **Causal links.** `attributedCauses`, install sites, continuations,
   Blocked/Partial resumptions and recredit (`spans.go:1227-1377`) are the
   subtlest code in the DB. Folding them means re-deriving them over stubs.
   Retaining both ends of every cause link avoids that, at a cost that
   depends on how many lazy evaluations a session makes (5% of spans carry
   links in `spanfootprint`).
3. **Visibility-dependent aggregates.** The running indicator follows
   placement (`walk_lazy.go:165-208`); `rowWaits` needs whole subtrees.
   Counts over stubs approximate the first; the second degrades to
   `PartialTrace` behaviour, once `childrenReported` is kept from the stubs.
4. **Children-first into the full `DB`.** Promotion delivers a span before
   the read that brings its ancestors back, the case today's late linking
   handles only partly (roll-up counts, running and revealed sets).

The rest (has-logs bits, digest routing, child counts, log roll-up walks,
metrics, search placement, error-origin loads, rewinds' call chains) is
mechanical.

## Plan

1. **Cheap wins now, independent of the rest**: don't keep `db.updatedSpans`
   when nothing drains it (`db.go:186-194`); credit a span that links to its
   parent in a later batch with its subtree's roll-up counts, running and
   revealed spans (the late-link gap above); route has-logs/presence for
   unknown spans without allocating a placeholder `Span`.
2. **Engine**: `POST /v1/traces/query` over `spanLookup`, with tests, behind a
   live feature flag.
3. **dagui**: the stub table as the `DB`'s front door (`ExportSpans`,
   `ImportSnapshots`, `ingestLogs`): retention policy, full ingest for
   retained spans, stub aggregates for roll-up counts, running, failed,
   has-logs and child counts; `ChildCount` from stubs; routing of
   digest-tagged records to folded creators (open choice 8).
4. **idtui**: wire `SetSpanProvider` for live sessions to the new read,
   `expanded` + mark-retained-before-read, fill-if-not-received import.
5. **Causal links**: start by retaining both ends; measure on real sessions.
6. **rowWaits**: `PartialTrace` semantics for partly folded rows, with
   `childrenReported` kept from the stub child lists.
7. **On-demand loads**: error origins, wait targets, call closures, search
   hits.
8. **Measure** a real long session (memory-cake, an integration-suite run)
   with and without it; then decide on demotion and on making it the
   default.

## Open choices for the user

1. **Retention policy.** Which kinds are retained outside boundaries
   (tests? every LLM message?), whether failures under a boundary stay folded
   (the spike does; in nested engines they're mostly expected failures), and
   whether agents are retained regardless of containment.
2. **Cause links**: retain both ends (simple, costlier) or fold them
   (re-derive `attributedCauses` over stubs).
3. **Demotion** on collapse (LRU) or never.
4. **Name search**: +4 B/span local name index, or an engine span search.
5. **Always on, or above a threshold.** Switching mid-session means folding
   an already full DB, so always-on is simpler; a threshold keeps small
   sessions on today's exact path.
6. **Accept the semantic drift**: running/failed counts over stubs instead of
   over placed rows; rowWaits only for fully loaded subtrees.
7. **Push volume**: keep pushing call payloads (and full attributes) for
   folded spans, or let the engine strip them in a fold mode, as presence does
   for display text.
8. **Digest-routed records** (service stdio and other `dag.digest` logs):
   an output-digest index in the CLI (~30–40 B per folded call span), or the
   engine resolves the creator span before pushing presence.
