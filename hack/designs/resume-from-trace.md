# Resume from trace

`dagger agent --trace <TRACE_ID>` — restore everything a past session did,
from the trace it published: its agents, their conversations, and the whole
TUI view of the run, into the session in front of you.

This is `hack/designs/async-agents.md` §4.1 ("Resume from trace — the next
thread") made concrete. It supersedes nothing: §4.1 states the *why* and the
constraints, `hack/designs/notes/recommendation.md` §3.1/§6 states the
*restore verb* and the save-file format, and this document states the
*implementation* — what to fetch, what the trace is missing, what to add to
the engine to close those gaps, and what the client does with the result.

Status: proposal, mostly built. §11's slices 1–6 (the engine facts, the
`rehydrate` verb, the client projection, the import, the fetch and the CLI)
have landed, so `dagger agent --trace <id>` works end to end; §13 records what
implementing them ratified and what it changed. Slice 7 (continuity) is
RESOLVED without being built as designed: `Agent.reseed` fixes the live-case
split at the source, superseding the `continues:` edge — §6 records how. Of
slice 8 (polish), the in-place swap landed as that same general verb; the
rest is unbuilt. Facts about the current code are cited by file; claims
that have NOT been measured are marked as such.

## 1. What it does, and what it does not

```
dagger agent --trace 2f123ba77bf7bd2d4db2f70ed20613e8
```

1. Streams the trace's spans, logs and metrics from Dagger Cloud — the whole
   trace, no filtering — into the **live frontend's** `dagui.DB`.
2. Projects a **restore plan** from that DB: one entry per agent instance the
   trace ever published — its instance ID, display name, lifecycle state, and
   the call digest of its last committed conversation.
3. Rebuilds each entry's conversation ID from the call payloads in the DB and
   **re-hydrates** the instance in the live session: same instance ID, same
   history, same state.
4. Attaches the client's prompt to the session's own agent (the "chief"), and
   leaves every other restored agent addressable through the roster.

The promise is deliberately wider than "the conversation continues". Because
the whole trace lands in the live DB, the restored session is **the old
session's TUI plus a live prompt**: scroll back through the previous run,
expand a tool call from an hour ago and read its full result, open its logs,
branch from an old message. That is a feature of importing everything rather
than a private reconstruction (§5.1), and it is why the fetch is unfiltered.

What it does not promise:

- **Not a hand-off of a live loop.** The runtime registry is per session
  (`engine/server/session.go:532`); nothing here changes that (§8). Resuming
  a trace whose agents are still running *forks* them: the restored instances
  carry the same IDs but are new runtimes in a new session, publishing into a
  new trace. This is accepted, not guarded.
- **Not machine independence.** The trace carries recipes, not snapshots
  (async-agents §4): `currentWorkspace` is `NotReplayable`
  (`core/schema/workspace.go:35-40`), so a restored agent gets *today's* tree
  with its recorded overlay replayed on top. Resume where the checkout is, or
  accept conflict markers (`conflictMarkerCue`, `internal/cmd/dagger/llm.go`).
- **Not sync `llm.loop` conversations.** A module-driven `LLM.loop` is not an
  agent and publishes no identity (async-agents item 5), so it restores as
  *view* (its spans and logs are all there) but not as a resumable
  conversation. Only agents re-hydrate.
- **Not the mailbox.** Messages enqueued but never consumed are not in the
  trace, and this design accepts that loss (§7).

## 2. What the trace already carries

Measured, or cited from code:

- **Agent identity, per loop span.** `dagger.io/agent`, `.id`, `.name`,
  `.call.digest`, stamped at span start (`core/agent_telemetry.go:39`).
- **Agent state, as log records.** `dagger.io/agent.state` (+
  `.waiting_on`), emitted on every change of the projected state
  (`EmitAgentState`, `core/agent.go:509-523`). Mutable facts cannot ride span
  attributes — a live span is exported as a snapshot taken at span start —
  which is why this split exists (async-agents §8).
- **The loop's error**, as the loop span's status description
  (`span.SetStatus(codes.Error, loopErr.Error())`, `core/agent.go:856-858`),
  which survives into `dagui.Span.Status` (`dagql/dagui/spans.go:263`).
- **Call payloads for the whole conversation.** Every dagql call span carries
  its own `dagger.io/dag.call` (`core/telemetry.go:94-107`), and the
  transitive closure of each call's ID rides the log channel for the frames
  that structurally never get a span (`recordCallPayloads`,
  `core/dag_call_telemetry.go`; consumer `dagql/dagui/callpayloads.go`). This
  is the fix that made in-session roster addressing work at all
  (async-agents §10.2, "Mode A: RESOLVED").
- **The consumer.** `dagui.DB` folds all of it: `DB.Agents()`
  (`dagql/dagui/agents.go`) is the roster; `Span.CallID()`
  (`dagql/dagui/spans.go:155`) rebuilds an ID from payloads and reports the
  referring frame when one is missing.
- **The harness.** `agentTraceSink` (`core/integration/agent_runtime_test.go:1001`)
  already stands up an OTLP endpoint, folds a real session's telemetry into a
  `dagui.DB`, and turns a roster entry back into a working handle. Resume is
  that path with a different transport and one more digest.

What reaches Cloud is exactly what reaches the client's own frontend DB: the
CLI wires the Cloud exporters alongside the frontend's
(`internal/cmd/dagger/engine.go:336-361`). So Cloud's copy has the same
completeness the live client has — which, post Mode-A, is enough to rebuild
handles — and Cloud stores attribute values in full, including the large
`dagger.io/dag.call` blobs (confirmed).

## 3. The two questions

### 3.1 Which agents?

Start from `DB.Agents()` — every agent the trace published a loop span for.
An agent that was spawned but never started has no loop span and no history;
there is nothing to restore, and the chief's recipe re-derives it anyway.

**Restore all of them, in the state they were in.** Three rules make that
computable.

**(a) A clean exit stops everything, and that is not a dismissal.** Session
close calls `AgentRuntimes.KillAll` (`engine/server/session.go:639`), which
`Stop`s every entry. So after a normal `exit`, *every* agent's last state
record says `STOPPED`, exactly like a worker the chief dismissed on purpose.
Restoring only non-STOPPED agents would therefore restore **nothing** from a
cleanly closed session — and restoring every explicitly stopped agent as live
would silently erase the fact that the user wound it down. Explicit stops
therefore restore dormant as `STOPPED`; they relaunch only when a later `send`
or `resume` deliberately addresses that same instance.

The two cases are indistinguishable in the trace today, which is what makes
"the state it was in" unknowable rather than merely unpublished. Fix it at
the source: carry the reason on the terminal record (§4.4). Then:

| final record | restores as |
|---|---|
| `STOPPED`, reason `EXPLICIT` | dormant tombstone: snapshot readable; `send` or `resume` relaunches it |
| `STOPPED`, reason `SESSION` | the state held *before* teardown |
| `FAILED` | `FAILED`, error preserved; `resume` retries |
| `PAUSED` | `PAUSED` |
| `IDLE` | `IDLE` |
| `RUNNING` (or no terminal record at all — the client crashed) | `IDLE`, with the interrupted turn's input still pending on the snapshot |

`RUNNING` is the one deliberate deviation from "exactly as it was", and it is
forced: the loop died with the session, so a roster that redisplays it as
running is lying in the way async-agents §3.4 forbids (recommendation §6.1
says the same). What survives is the last *committed* step; a partially
executed step is discarded, and the pending input re-steps when the agent is
next prompted. Nothing auto-continues: a restored agent spends no tokens
until the user says so.

Reading "the state before teardown" needs the record *history*, not just the
latest: `ingestAgentState` keeps latest-wins (`dagql/dagui/agents.go:142`), so
the consumer keeps one extra field — the last state that was not a
session-teardown stop.

Restoring explicitly-stopped agents as **tombstones rather than skipping
them** is what makes a dismissed worker's WIP reachable: `modules/staff`'s
harvest family resolves through `tombstones[name].snapshot.workspace`, and a
tombstone that was never re-hydrated projects IDLE-from-absence with the
*seed* as its snapshot — async-agents item 12's "silently returns nothing
new". Re-hydration fixes that case as a side effect.

**(b) Restore is all-or-nothing, and eager.** The chief's recorded chain binds
its workers by ID (`withTools(object: staff!withWorker(name, …llm!agent(id:…)))`
— the pure form recommendation §3.2 landed, `modules/staff/main.dang:102`).
If a worker is not re-hydrated *before* the chief's first tool dispatch, the
dispatch resolves the handle against an empty registry and — today — `Send`'s
`GetOrCreate` mints an amnesiac twin from the seed (async-agents §10.2, item
13). §5.3 orders it; §4.2 makes the ordering violation loud.

**(c) Focus the agent with no agent above it.** A worker's loop span is
started under its chief's tool-call span, so nesting is readable from the DB:
walk `span.ParentSpan` and ask whether any ancestor is a loop span
(`span.Agent`). Agents with no agent ancestor are top-level; the CLI's own
conversation (`interactiveAgentName = "interactive"`,
`internal/cmd/dagger/llm.go:154`) is one. If exactly one is top-level, focus
it. If several, focus the most recently active and say so; `--agent
<name|id>` overrides.

### 3.2 What is their latest snapshot?

The runtime knows: `AgentRuntime.last`, the last committed conversation
(`core/agent.go:444`), updated after every step (`core/agent.go:929-936`) and
after every mailbox drain (each drained message is a `withPrompt` Select).
Nothing publishes it. This section proposes publishing it (§4.3) — but the
"just look at the spans" instinct is right about the structure, so here is
exactly how far it gets, since the answer decides whether the record is worth
its ten lines.

**The structure does hold.** `Step` selects through the dagql server on a
context descended from the loop span (`core/agent.go:922`), so the
`LLM.withResponse` / `LLM.withToolResult` / `LLM.withPrompt` call spans really
are descendants of the agent's loop span, in order. "The newest LLM call span
beneath the loop spans" is a real candidate for the tip, and it needs no
engine change at all.

**Three ways it is wrong, in rough order of how much they hurt:**

1. **A span can be ahead of the commit.** `step()` materializes the assistant
   response *before* dispatching tool calls (`core/llm.go:1730-1741`), so a
   step that fails or is interrupted between the two leaves a `withResponse`
   span for a state the runtime deliberately did not commit (it returns the
   pre-step `inst` on that path, `core/llm.go:1818-1821`). Resuming from that
   span hands the model an assistant `tool_use` with no `tool_result` — a
   conversation no provider will accept. The heuristic's failure mode is a
   *malformed* conversation, not a stale one, and it fires exactly on the
   sessions people most want to resume: the ones that were interrupted.
2. **Not every commit has a span.** Span emission dedupes per session by call
   digest (`ShouldEmitTelemetry`, `core/telemetry.go:83`), so an identical
   chain — two agents with identical seeds and identical replies, routine
   under the `replay/` provider — suppresses the second span. The payload is
   still there via the closure log channel, so the ID rebuilds; there is just
   nothing to *sort*.
3. **Not every LLM span beneath a loop span is that loop's conversation.** A
   tool can run its own LLM work (`modules/delegate`, an off-the-record
   `snapshot.withPrompt` branch, a compaction), and those spans nest under the
   tool-call span — beneath the loop span. Filtering them out means checking
   that a candidate's receiver chain descends from the previous tip, which is
   most of a real implementation and still an inference.

So: the record states a fact the runtime holds; the scan infers it from spans
emitted for another purpose, and guesses wrong precisely when a step was
interrupted. Publish the record, and do not keep the scan as a fallback —
this is unreleased territory in every direction, and a fallback that silently
produces an unusable conversation is worse than a trace that says it is too
old to resume. (The other candidate fallback, `dagger.io/llm.call.digest`
(`core/llm.go:1676-1687`), is the value *entering* the step, so it is always
one turn short.) A trace with no snapshot records fails the restore, naming
the agent.

With the record, a restore entry is:

```go
type AgentRestore struct {
    ID             string // dagger.io/agent.id — the instance
    Name           string // display label
    State          string // IDLE | PAUSED | FAILED | STOPPED (already mapped per §3.1)
    Error          string // loop error, for FAILED
    SnapshotDigest string // dagger.io/agent.snapshot.digest
    ParentAgentID  string // enclosing agent, for focus + display
}
```

and restore is one pure chain per entry:

```graphql
loadLLMFromID(<snapshot>) { agent(id: <ID>, name: <Name>) { rehydrate(state: <State>, error: <Error>) } }
```

## 4. Engine changes

Five, each small. Three are shared with recommendation.md and pay for
themselves there too.

### 4.1 `Agent.rehydrate` (recommendation §3.1, verbatim)

```graphql
type Agent {
  """
  Recreate this instance's runtime entry from a persisted conversation,
  without starting its loop. The receiver's snapshot becomes the entry's
  committed history, so prompting it continues where it left off.

  Errors if the instance already has a runtime entry in this session:
  re-hydration must happen before anything else can address the instance.
  """
  rehydrate(state: AgentState = IDLE, error: String): ID!
    @expectedType(name: "Agent")
}
```

It works because `GetOrCreate` reads the seed **only when the entry is
created** (`core/agent.go:274-303`): hand it a handle whose receiver is the
*final* snapshot instead of the initial seed and the entry starts life
holding the whole history. `spawn` is mint-create-pin; `rehydrate` is
adopt-create-pin.

`state` sets the facts, never a stored state (async-agents §3.4 — the
projection stays a projection): `PAUSED` sets `paused`; `FAILED` sets `done` +
`loopErr` from `error`; `STOPPED` sets `done` + `sealed` until a later send or
resume clears the tombstone facts and relaunches from the restored snapshot;
`IDLE` sets nothing.
The existence check is the guard that makes a late restore loud instead of a
silent no-op (recommendation §6.2's seed race).

### 4.2 `Send` stops being generative (recommendation §3.1)

`AgentRuntimes.Send` routes through `GetOrCreate` (`core/agent.go:331`), so a
registry miss *creates* — that is what booted amnesiac twins in §10.2 and
item 13. With re-hydration explicit, `Send` uses `Get` and errors on a miss
("agent %q has no runtime in this session"). Signal-with-start survives: it
starts an entry that exists.

This matters more here than in recommendation.md, because importing the trace
into the live DB (§5.1) puts *every* agent of the old session on the roster —
including any this session failed to restore. Focusing one and typing at it
must say "no runtime in this session", not quietly start a second loop with
no history.

### 4.3 Snapshot digest records

`engine/telemetryattrs/attrs.go`, in the `dagger.io/agent.*` block:

```go
// AgentSnapshotDigestAttr carries the recipe digest of the agent's last
// committed conversation. Latest record wins. This is the resume anchor:
// a client rebuilds the conversation's ID from the call payloads it has
// ingested and re-hydrates the instance from it.
AgentSnapshotDigestAttr = "dagger.io/agent.snapshot.digest"
```

Producer: `EmitAgentSnapshot(ctx, digest)` beside `EmitAgentState`
(`core/agent_telemetry.go`), called from a new `rt.commitLast(ctx, next)`
helper that replaces the two places `rt.last` is assigned (the step commit at
`core/agent.go:935`, and the drain in `drainMailbox`). Digest via
`next.RecipeDigest(ctx)` — the same derivation `step()` and `agentSpanAttrs`
already use, and the reason it must be the *recipe* digest is that a
post-evaluation `Result.ID()` is an engine-local handle that dies with its
session (async-agents §4.1).

It cannot be folded into `publishStateLocked`, which is edge-triggered on the
projected state: most steps do not change the state, and every step changes
the snapshot. Emit the seed's digest once at loop start too, so an agent that
never stepped still restores without the consumer special-casing absence.

Consumer: `ingestAgentSnapshot` beside `ingestAgentState`
(`dagql/dagui/agents.go:142`), folding onto `Span.AgentSnapshotDigest`,
surfaced on `AgentNode`. Same rules: consumed as data, never rendered as log
text, bumps `db.mutations`.

### 4.4 Stop reason on the terminal record

```go
// AgentStopReasonAttr distinguishes a stop somebody asked for from a stop
// the session's teardown performed: EXPLICIT | SESSION. Without it every
// agent in a cleanly closed session looks dismissed.
AgentStopReasonAttr = "dagger.io/agent.stop.reason"
```

`AgentRuntime.Stop` gains a reason parameter (a fact on the entry, read by
the publisher); `KillAll` (`core/agent.go:397`) passes `SESSION`, every other
caller `EXPLICIT`. A `STOPPED` record with no reason fails the restore rather
than guessing: guessing `EXPLICIT` loses a whole session, guessing `SESSION`
resurrects dismissals.

One honesty note: the teardown record is emitted at session close, and its
export races process exit. A *missing* terminal record reads as a crash
(§3.1), which is the correct reading either way.

### 4.5 `rehydrate` publishes identity

A re-hydrated agent that is not started has no loop span *in the new trace*,
so once the old trace's spans age out of the view it would vanish from the
roster — telemetry is the directory (async-agents §3.3). `rehydrate`
therefore opens and immediately ends a span carrying `agentSpanAttrs`
(identity + the call digest that makes it addressable), publishes one state
record on it, and retains its `spanCtx` — which `AgentRuntime` already does
deliberately past a span's end (`core/agent.go:466-479`). A later `start`
opens the real loop span; all of them carry the same `dagger.io/agent.id`,
and `DB.Agents()` unions them by construction (`dagql/dagui/agents.go:87-110`)
— which, with the imported trace in the same DB, is what merges an agent's
old life and its new one into a single roster entry and a single transcript.

Rejected: starting every restored agent so it publishes a loop span. It costs
a goroutine per agent, and for a crash-restored agent with pending input it
would silently continue its turn — the unattended token spend §3.1 rules out.

## 5. Client changes

### 5.1 The fetch, into the live DB

A new otlpjson-over-SSE client, modelled on the reference implementation
(`cmd/dagger/trace.go` at `1492469b`): `GET {DAGGER_CLOUD_URL}/v1/traces/{id}`,
`/v1/logs/{id}`, `/v1/metrics/{id}`, each an SSE stream whose events are
protojson-encoded OTLP export requests, decoded with `protojson.Unmarshal`
into `coltracepb`/`collogspb`/`colmetricspb` and re-exported through
`telemetry.SpansFromPB` / `telemetry.ReexportLogsFromPB` /
`enginetel.ReexportMetricsFromPB`. Auth is `auth.GetCloudAuth` +
`auth.GetDaggerCloudAuth` for Basic tokens, exactly as the reference does.
`github.com/vito/go-sse` is already a direct dependency (`go.mod:161`).

It lands in `internal/cloud` (say `internal/cloud/otlp.go`) rather than in the
CLI, next to the existing GraphQL-SSE trace client
(`internal/cloud/trace.go`), because `dagger trace` is a plausible second
consumer later. It does not replace that client now: `dagger trace`'s
incremental/lazy loading answers a different requirement (render a huge trace
cheaply) from this one (rebuild a complete DAG *and* show everything).

**The sink is the live frontend's own exporters** — `Frontend.SpanExporter()`,
`LogExporter()`, `MetricExporter()` — not a private DB. That is what buys the
full-TUI restore of §1: one DB holds both sessions, so an agent's old and new
loop spans merge into one roster entry (keyed on the instance ID,
`dagql/dagui/agents.go:87-110`), its transcript spans both lives
(`SurfacedConversationForAgent` iterates every loop span,
`dagql/dagui/conversation.go:167`), and every imported tool-call span keeps
its logs and its `dagger.io/dag.call` payload — so expanding an old tool call
shows its real result, and branch-from-message works on pre-restart history.

Five consequences that have to be handled, all measured against the code:

1. **Do not touch the primary span; mark the imported root passthrough.**
   `db.RootSpan` is the first parentless span received and `PrimarySpan`
   defaults to it (`dagql/dagui/db.go:939-949`); both are already set to the
   live CLI root by the time the fetch runs, and the imported root simply
   becomes a second parentless span. Unlike the reference client, resume must
   **not** call `Frontend.SetPrimary`. It should stamp
   `dagger.io/ui.passthrough` on the imported root, though: a passthrough span
   is skipped by the walk and its children rendered in its place
   (`dagql/dagui/types.go:193-202`), so any view that walks all spans shows
   the old session's contents rather than a stale `dagger agent …` row
   wrapping them. Cheapest as an attribute added to the protobuf at import.
   Do **not** mark it `Encapsulate` or `Boundary` — those contain, and would
   suppress the imported conversation everywhere it needs to surface.
2. **Seal the imported trace's unfinished spans.** The DB cancels
   still-running spans only when *its own* root ends (`db.go:951-971`), so a
   crashed session's never-ended spans would render as live work forever —
   spinners for a dead run, and `AgentNode.Live()` reporting true for an agent
   that is not. At stream end, re-emit any imported span with no end time,
   sealed to the imported root's end time (or the newest timestamp seen) and
   marked `Canceled`/`LeftRunning`, the same shape `db.go:951-963` produces.
   Cheap because the fix is on the protobuf before conversion: buffer the
   unfinished ones, stamp `EndTimeUnixNano`, re-export.
3. **Whole-trace conversation surfacing has to become genuinely
   whole-trace.** The expectation that a restored conversation surfaces to the
   top regardless of which root it hangs off is right, and it is not what the
   code does: surfacing is defined as "what was said beneath THIS span"
   (`dagql/dagui/db.go:561-572`), `SurfacedConversation()` resolves its nil
   root to `db.RootSpan` — the *live* root — and a message span whose ancestor
   chain ends at the imported root never reaches it, so
   `buildSurfacedConversation` files it as contained and drops it
   (`dagql/dagui/conversation.go:107-109`).

   The fix is one line with a real argument behind it: stop resolving nil to
   `db.RootSpan` for conversations, so nil means "every message span in the
   DB that no Boundary/Encapsulate contains" — which is what
   `HasConversationForSpan(nil)` already means (`underSurfaceRoot`,
   `dagql/dagui/db.go:582-595`), so the two stop disagreeing. Scoped surfacing
   (a zoomed report, a per-agent view) passes an explicit root and is
   untouched, keeping the fixture-containment rule it exists for.

   That is strictly better than the flag this section previously proposed
   (`restoredFromTrace`, forcing agent-scoped promotion): the flag only fixed
   the promotion path, leaving every other whole-trace consumer blind to the
   imported half, and it made "which trace is this span from" a rendering
   input, which is exactly the coupling §5.1 is trying to remove.
4. **`LLM.Replay` becomes unnecessary, and undesirable.** Today's `-r` replays
   the saved conversation to fake up scrollback, with no call digests, so
   replayed messages cannot be branched from (`core/llm.go:2389-2393`). The
   imported spans are the real ones. Restored agents therefore skip Replay
   entirely — a simplification and a strict upgrade.
5. **Render cost is not a new problem.** `buildAgents` and
   `buildSurfacedConversation` walk every span in the DB, memoized on
   `db.mutations`, which a live turn bumps constantly — but that is exactly
   the cost the *original* session was paying by the time it ended, since its
   DB held the same spans. A restored session starts where the old one left
   off rather than paying anything new, so this is a note, not a risk: if it
   is slow it was already slow, and the fix (incremental indexes at ingest for
   spans with `LLMRole` / `Agent`) belongs to the renderer, not to resume.

**Reading the DB back.** The restore plan and the ID rebuild are reads *of*
the frontend's DB, which the frontend owns single-threaded. Follow the
existing pattern (`idtui.TraceFrontend`) with a small optional interface the
CLI type-asserts:

```go
type AgentRestorer interface {
    AgentRestorePlan() []dagui.AgentRestore              // under the frontend's lock
    EncodedIDForCallDigest(digest string) (string, error)
}
```

### 5.2 The projection

In `dagql/dagui/agents.go`, beside `Agents()`:

```go
// RestorePlan projects the trace's agents into what a resuming client needs
// to re-hydrate them. Deliberately a projection, not a query: async-agents
// §3.3 renounced Query.agents because telemetry is the directory, and resume
// keeps that property — an agent whose spans a client cannot see stays
// unreachable to it.
func (db *DB) RestorePlan() []AgentRestore
```

with the §3.1 state mapping applied there (one place, testable without an
engine). It must ignore agents whose only spans came from the *live* session,
so re-running a restore is a no-op rather than a duplicate.

**One shared fix falls out.** Rebuilding an ID from a digest currently
requires a *span* carrying that digest: `encodedIDForCallDigest` scans
`db.Spans.Map` for `s.CallDigest == digest`
(`dagql/idtui/frontend_pretty.go:5737`). But `DB.Call` resolves payloads from
`db.CallPayloads` (`dagql/dagui/db.go:1201-1208`), which the log channel also
fills for frames that never got a span — precisely the case the log channel
exists for, and precisely §3.2's failure mode 2. Factor the walk onto the DB:

```go
func (db *DB) CallIDForDigest(digest string) (*call.ID, error) // Span.CallID's body, span-free
```

`Span.CallID()` and `encodedIDForCallDigest` both become one-liners over it,
keeping branch-from-message, roster addressing and resume on the one proven
path (async-agents §10.2 insists on that, and it has been the difference
between a loud failure and a wrong handle).

### 5.3 Executing the plan

Order and atomicity are load-bearing (recommendation §6.2's seed race):

1. Fetch the trace, under a span so the wait is visible, before the
   interactive loop starts — the same place `LoadSession` runs today
   (`internal/cmd/dagger/functions.go:1118-1127`).
2. Re-hydrate **every** entry, before anything can dispatch a tool or bind an
   LLM.
3. Any entry whose snapshot ID does not rebuild — `CallIDForDigest` reports
   the referring frame (`dagql/dagui/extract.go`) — **fails the command**,
   naming the agent and the frame. It does not degrade to a partial restore:
   a missing worker is exactly the hole a later tool dispatch falls into, and
   with §4.2 that dispatch is an error rather than an amnesiac twin, but the
   error would arrive minutes later with none of this context. `--partial`
   opts into best-effort.
4. Then attach: `LLMSession.Attach(instanceID, name, encodedAgentID)`
   (`internal/cmd/dagger/llm.go:235`) already adopts an agent the session did
   not spawn, rooted on its snapshot — which is exactly a restored agent. The
   encoded handle is the ID `rehydrate` returned.
5. `SetTarget` the focused agent (§3.1c). No Replay (§5.1.4).

Restored agents are marked **owned**: the session that resumed them is their
only driver, so `.clear` stopping them is right. (Contrast with roster
attach, which is deliberately not owned —
`internal/cmd/dagger/session_agent.go:144-149`.)

### 5.4 CLI surface

```
dagger agent --trace <TRACE_ID>              # restore all, focus the top-level agent
dagger agent --trace <TRACE_ID> --agent scout1
```

`--trace` conflicts with `-r/--resume` (two stores, one conversation) and
with positional agent names (composition comes from the trace, not from
`currentWorkspace.agents`). Wiring is one more branch in
`startInteractivePromptModeWithResume` (`internal/cmd/dagger/functions.go:1077`).

**Where the trace ID comes from.** Both from the Cloud URL the session printed
(`URLForTrace`, `engine/telemetry/url.go`) and from the save file: add
`trace_id` to `sessionMetadata` (`internal/cmd/dagger/llm.go:394-401`),
written by `AutoSaveSession` from the root span's trace ID. The picker
(`resumeSessionInteractive`, `internal/cmd/dagger/shell_commands.go:340`) then
has it for free, and the direction recommendation §6 sketched — the save file
as a pointer, the trace as the store — becomes available without deciding it
now.

## 6. Continuity: one conversation, many loops

The seam a restore leaves ("it should look like you just continued") and the
roster split people already see in live sessions — `interactive` going
stopped/idle while a second `interactive` appears beside it — are the same
defect seen from two sides. The roster and the transcript are keyed on the
runtime *instance*, but what the user is looking at is a *conversation*, and
a conversation routinely outlives several instances.

**The restore case is already solved by construction.** A re-hydrated agent
keeps its instance ID, so its old loop spans and its new ones fold into one
`AgentNode` (`dagql/dagui/agents.go:87-110`) and one transcript
(`SurfacedConversationForAgent` iterates every loop span,
`dagql/dagui/conversation.go:167-189` — it was written for the resume-retry
relaunch, and a cross-session restore is the same shape). With §5.1.3 there
is nothing to mark: the imported turns and the new ones promote into one
list, ordered by start time.

**The live case was a real bug, and here was its mechanism.** `updateLLM`
(`internal/cmd/dagger/session_agent.go`) called `dropAgent`, which stops the
runtime; the next submit spawned a fresh instance from the new value, under
the same display name. Every wholesale replacement went through it:
auto-compaction and `/compact`, attaching an `@`-reference, `.model`,
reasoning effort, workspace rebind, ctrl+s export, ctrl+u reset, `.clear`.
Auto-compaction was the everyday trigger, which is why this showed up in
long sessions and looked spontaneous.

**Do not merge by name.** Dismiss-and-rehire under one name is deliberately
two agents — async-agents §8 spent the whole spawn pivot establishing that "a
new incarnation is not the same actor" — so name-merging would rejoin exactly
the case that separation exists for. Continuity has to be *asserted by
whoever performs the replacement*, and that is the client.

**RESOLVED — at the source, not with an edge.** The continuation edge this
section originally proposed (`continues:` on the pinned chain, a
`dagger.io/agent.continues` span attribute, a roster fold in `buildAgents`,
a predecessor→node alias map) is superseded, unbuilt: what the follow-up
paragraph below called "`rehydrate`'s sibling, minus the create" landed as
the general verb, and with it there are no successor instances left to
fold. `Agent.reseed(conversation:)` (core/agent.go) replaces an existing
entry's committed conversation in place — identity, mailbox, and lifecycle
facts untouched; queued mail drains onto the new conversation; a FAILED
tombstone keeps its error so reseed and resume compose — and the CLI's
`updateLLM` now routes every wholesale replacement through it, dropping the
runtime only as a fallback (an attached runtime, which is somebody else's
agent to reseed; a suspended turn; an engine older than the verb). The
guard the follow-up asked for is exactly the one built: idle or paused,
never mid-step — a step in flight would silently overwrite the swap with a
commit derived from the old conversation — and a suspended turn is refused
even though it projects PAUSED, because its consumed messages' input is
pending on the old snapshot.

Two knock-on decisions worth recording. The verb is ARGUMENT-shaped
(`agent.reseed(conversation:)`), not receiver-shaped like `rehydrate`: a
runtime verb's receiver names an instance and never redefines one — the
async-agents §10.2 invariant that makes telemetry-rebuilt handles safe to
hold — while creation verbs (`spawn`, `rehydrate`) read the receiver as the
seed, because creation is the one moment a chain defines an entry.
`rehydrate` errors when the entry exists, `reseed` errors when it does not;
the opposite guards are both load-bearing (a late restore must be loud, and
so must a reseed whose bookkeeping is wrong), which is why they stay two
verbs rather than an upsert. And the TUI's stopped-agent filter — the
roster strip hiding STOPPED entries — was reverted: it papered over the
successor tombstones, and with replacements no longer minting them, a
STOPPED entry means something real (a dismissed worker, a deliberate stop)
that must not silently vanish from the strip. `TestReseed`
(core/integration/agent_runtime_test.go) pins the verb's semantics,
`TestReseedKeepsTheInstance` (internal/cmd/dagger/session_agent_test.go)
the CLI's reseed-or-drop policy, and `TestRosterShowsStoppedAgents`
(dagql/idtui/agent_step_test.go) the roster.

**The follow-up that removed the commonest split at the source** (kept as
originally written, since it is what got built — generalized from
compaction to every replacement): compaction is not a new conversation; it
is the same conversation with a shorter history. The registry keys on the
instance ID and `rt.last` is just a field, so a verb that swaps the
committed conversation while keeping the entry — `rehydrate`'s sibling,
minus the create — lets the client compact in place and never drop the
runtime at all. It needs a guard (idle or paused, never mid-step, or the
swap races a step's commit).

## 7. What is deliberately lost

- **Live-ness.** §1: resume forks; it does not adopt a running loop.
- **Unconsumed mail.** `send`'s one promise is never to drop a message
  (async-agents §8), and a message enqueued but never drained is not in the
  trace at all. Accepted rather than fixed: publishing enqueue records would
  close it, but the common case is already safe, since a message a turn
  *consumed* is on the snapshot as a `withPrompt` selector. Document it in
  `--trace`'s help.
- **Awaiters.** `AgentMessage.await` is idempotent against the entry, so a
  caller re-awaits after reconnecting (async-agents §3.2) — but nothing
  restores an await across sessions, and message records are not re-created.
- **Engine-side workspace metadata** that is not part of a recipe — e.g. a
  staged commit's `origin` provenance (async-agents item 12).

## 8. Dependencies and interactions

- **`Staff.dismiss` must lose its effectful recorded form** (recommendation
  §3.2's `withDismissed`, the half not yet landed —
  `modules/staff/main.dang:686-701` still mutates self directly). Otherwise
  loading a restored chief's chain re-executes `dismiss`, which stops a
  worker this design just re-hydrated, and drags the per-call-nonce cascade
  (recommendation §1) along with it. `spawn`'s half is already pure
  (`withWorker`, `main.dang:102`), which is what makes worker restore work at
  all.
- **`@cache(Never)` module functions returning their own type** elsewhere
  (recommendation §3.3) re-execute on a restored chain: `engineLab.start`,
  `tuiQa.start`, `mcpLab.start`. Not a blocker for agents, but a resumed
  session will re-run them; the sweep in recommendation §3.3 is the fix.
- **Session-independent registry** (async-agents item 13's recommended fix)
  is *not* this, and fork-on-resume (§1) is the decision that says so. If it
  ever lands, resume-from-trace becomes reattach for same-engine traces and
  stays reconstruction for everything else; the plan projection is unchanged
  either way, which is a good reason to keep it a pure projection.
- **Chained resume must work**: a resumed session's own trace has to carry the
  restored chains, or resuming *it* would fail. It should, in principle —
  loading an ID never re-selects the calls behind it (async-agents §10.2), but
  the first call selected on a restored chain emits the transitive closure of
  its ID over the log channel. Unverified; §10 covers it.

## 9. Failure modes

| symptom | cause | behaviour |
|---|---|---|
| `cannot rebuild ID for "agent": call <digest> never reached this client, referenced as …` | a frame's payload is absent from the trace | fail the restore, naming the frame (`dagql/dagui/extract.go` already produces this) |
| an agent has no snapshot digest, or a `STOPPED` record has no reason | the trace predates §4.3/§4.4 | fail the restore, saying the trace is too old — no guessing (§3.2, §4.4) |
| restored worker answers with no history | a tool dispatched before its `rehydrate` (seed race) | §4.1's existence check makes the late `rehydrate` error; §4.2 makes the early dispatch error |
| imported work renders as still running | crashed session's spans never ended | sealed at import (§5.1.2) |
| restored transcript missing from the tree | whole-trace surfacing resolved to the live root | fixed by §5.1.3; regression-tested in §10 |
| roster shows two entries for one conversation | a wholesale LLM replacement re-spawned under the same name | fixed at the source: the CLI reseeds the one instance in place (§6, `Agent.reseed`) |
| conflict markers in workspace files | recorded overlay patches no longer fit | existing `conflictMarkerCue` path, unchanged |

## 10. Testing

- **Projection, no engine.** `dagql/dagui/agents_test.go`: synthesize loop
  spans + state/snapshot records and assert `RestorePlan()` — the state
  mapping, the teardown-vs-dismiss split, the pre-teardown state lookup, a
  crashed agent with no terminal record, an agent with two loop spans (a
  resume retry), an agent with no snapshot digest.
- **Span-free rebuild.** Extend `dagql/dagui/callpayloads_test.go`:
  `CallIDForDigest` resolves a digest that arrived *only* over the log
  channel — the case `encodedIDForCallDigest` cannot serve today, and §3.2's
  failure mode 2.
- **Import behaviour, no engine.** `dagql/idtui`: import a canned foreign
  trace beside a live one and assert the primary span is untouched, unfinished
  imported spans are sealed, the imported root renders passthrough rather than
  as a row, the roster merges old and new loop spans into one entry, and the
  focused agent's promoted transcript includes the imported turns — with a
  single agent on the roster, which is the case §5.1.3 fixes.
- **Continuity.** SUPERSEDED with slice 7 (§6): there is no `continues`
  edge to fold, so the planned `agents_test.go` coverage has nothing to
  test. What replaced it: `TestReseed` (core/integration) for the verb,
  `TestReseedKeepsTheInstance` (internal/cmd/dagger) for the CLI's
  reseed-or-drop policy, `TestRosterShowsStoppedAgents` (dagql/idtui) for
  the roster. The dismiss-and-rehire case §6 must not break needs no test
  here: two spawns are two instance IDs, which `TestSpawnInstances`
  already pins.
- **End to end, replay provider.** Extend the `agentTraceSink` harness
  (`core/integration/agent_runtime_test.go:1001`): run a chief + two workers
  against the keyless `replay/` provider, dismiss one, capture the OTLP, then
  **serve that capture back** through a fake SSE server speaking the §5.1
  endpoints, run the restore, and assert against a *new* session: the chief's
  transcript contains a marker only the pre-restore session produced; a `send`
  continues that conversation rather than opening an empty one; the live
  worker's state and snapshot survive; the dismissed worker is a tombstone
  whose snapshot is its real conversation (not the seed) and whose `send`
  errors.
- **Chained resume.** Resume the resumed session's own trace; assert the
  chains still rebuild (§8).
- **CLI wiring.** `internal/cmd/dagger`: flag conflicts, plan execution and
  focus selection against a fake runtime, in the style of
  `session_agent_test.go`.

Fail-first is the rule this area has paid for twice (async-agents §10.1: "a
green test run against the wrong branch is worth exactly nothing"). Every
test above should be seen failing before the change that fixes it.

## 11. Build order

1. **Engine facts**: §4.3 snapshot records, §4.4 stop reason. Additive
   telemetry, no consumer yet; verifiable with a unit test on the record
   shape (`core/agent_telemetry_test.go` has the pattern).
2. **Engine verb**: §4.1 `rehydrate` + §4.2 non-generative `send` + §4.5
   publication. Independently useful: it is also what recommendation §6's
   save-file roster needs.
3. **Client projection**: §5.2 `RestorePlan` + `CallIDForDigest`.
4. **Import**: §5.1's sealing, passthrough root, whole-trace surfacing fix,
   driven by a canned trace — no Cloud, no engine.
5. **Fetch**: the SSE client, exercised by the fake server.
6. **CLI**: §5.4 `--trace`, restore execution, attach/focus.
7. **Continuity**: SUPERSEDED — §6's `continues` edge and the roster fold
   were never built; `Agent.reseed` resolved the live-case split at the
   source instead (§6 records the resolution and the decisions it forced).
8. **Polish**: `trace_id` in the save file, span links from a restored
   agent back to its source-trace loop span. (In-place compaction landed
   as the general `reseed` verb, per §6.)

Slices 1–4 are testable without Cloud at all, which matters while the
endpoints are undeployed: the fake SSE server in slice 5 is the only thing
that depends on their shape.

Slices 1–6 have landed (§13.1–§13.6), slice 7 dissolved (§6: `Agent.reseed`
removed the split it existed to fold), and of slice 8 only the save-file
`trace_id` and the span links remain — neither of which resume needs to
work.

## 12. Decisions taken, and what is still open

Settled (with the reasoning above): the endpoints are
`/v1/{traces,logs,metrics}/{trace-id}`, unfiltered, whole-trace, into the live
DB — full TUI restore is a goal, not a side effect; Cloud keeps attributes in
full; a still-running source trace forks rather than being adopted; a
crash-restored `RUNNING` agent parks until prompted; every agent restores,
tombstones included, in the state it held; unconsumed mail is a documented
loss; the save file grows a `trace_id`; imported metrics count, so cost and
token totals accumulate across resumes rather than restarting at zero; there
are no compatibility fallbacks — a trace the engine of the day did not
instrument fails the restore instead of guessing; and there is no seam to
mark, because a restored agent keeps its instance ID and therefore renders as
one conversation (§6).

Open:

- **The two Cloud-side assumptions are ANSWERED** (§13.5's measurement, against
  the live endpoints): attribute-only, empty-bodied log records survive the
  round trip, and span attributes come back byte-identical. The agent-specific
  record shapes (`dagger.io/agent.state`, `.snapshot.digest`, `.stop.reason`)
  are answered too, but one step short of Cloud: §13.6's end-to-end test round
  -trips a real agent session's records through protojson-over-SSE and restores
  from them, so the SHAPES are proven on the wire — what nobody has run is that
  same trace through api.dagger.cloud, for want of an agent trace and a
  credential in the environment where the work was done. A metrics stream with
  data in it is likewise still unproven against Cloud, and proven against the
  fake (§13.5's canned gauge).
- **Chained resume** (§8) is MEASURED, and it works: §13.6's test asserts that
  every restored agent's anchor rebuilds from the resumed session's own trace,
  which is the property resuming that trace in turn depends on. What is not
  measured is a second full round trip (restore a restore); the anchors being
  rebuildable is the part that was in doubt.
- **Whether whole-trace surfacing wants a trace filter after all.** §5.1.3
  makes nil mean "every message span in the DB". That is right for one
  imported trace beside one live one; if a client ever holds several
  unrelated traces (a future `dagger trace` that also prompts, say), it will
  want "these traces" rather than "all of them". Not a problem to solve now,
  but the signature should not make it hard to add.
- **The restored session's own conversation is an empty extra.** `dagger agent
  --trace` still composes the workspace's agents into a conversation of its
  own, focuses a RESTORED one, and leaves that composed conversation unused.
  It is invisible (an unspawned conversation publishes no telemetry, so it has
  no roster entry) and it is what `.clear` resets a restored conversation to,
  which is why it is composed at all — but "the session's own conversation" and
  "the restored top-level agent" being two objects is a seam §6's continuity
  work could remove.

## 13. As-built ratifications

What implementing the slices settled, in the style of async-agents §8: the
design above states the intent, this states what the code does and why it
differs where it does.

### 13.1 Slice 1 — the engine facts (§4.3, §4.4)

- **Both facts ride log records, and the split is forced.** The snapshot
  digest (`dagger.io/agent.snapshot.digest`) on a record of its own, the stop
  reason (`dagger.io/agent.stop.reason`) on the terminal state record. Neither
  could be a span attribute: a live span is exported as a snapshot taken at
  start and heartbeated unchanged (async-agents §8), so a fact learned
  mid-loop reaches nobody. The snapshot digest additionally cannot ride the
  state record — `publishStateLocked` is edge-triggered on the projection, and
  most commits leave the projection alone while every commit moves the
  snapshot.
- **The stop reason DOES ride the state record**, rather than a terminal
  record of its own: it is meaningless without the state it qualifies, and a
  second record would be one more thing to correlate for a consumer that
  already folds the first latest-wins. `EmitAgentState` therefore took a
  `stopReason` parameter, and — like `waitingOn` — publishes it as an explicit
  empty string on every record it does not apply to, so a FAILED agent that a
  resume retried past never keeps reporting the reason of a stop that no
  longer applies.
- **The reason is a fact on the entry, set before the loop winds down.**
  `AgentRuntime.Stop` gained a `reason` parameter (`KillAll` passes `SESSION`,
  the `stop` resolver `EXPLICIT` — the only two callers) and records it on
  `rt.stopReason` in the same transition that sets `stopRequested`. That
  ordering is load-bearing for a running agent: the record that says STOPPED
  is published by the loop's own defer, minutes later and on another
  goroutine, and it reads the fact rather than being handed one.
- **`commitLast` runs INSIDE the transition, not around it.** The design's
  `rt.commitLast(ctx, next)` is a locked helper called from within
  `transitionLocked`'s mutation, because both assignment sites commit
  alongside other facts (the step's `stepping = false`, the drain's
  `turnOpen`/`consumed`). Splitting them into two transitions would let a
  reader observe a landed step whose snapshot had not moved yet — a state
  that has never been observable and should not become so for telemetry's
  sake. Emitting under the entry mutex is the same call `publishStateLocked`
  already makes, and for the same reason: serializing publication with the
  transition that caused it is what keeps the published sequence faithful.
- **Emission dedupes on the last published digest** (`emittedSnapshot`,
  mirroring `emittedState`). Two routine paths re-commit an unchanged value —
  a resume-retry relaunch republishes the conversation it picks back up, and a
  step that fails before recording anything commits the value it started from
  (`Step` returns the pre-step instance on that path) — and latest-wins makes
  duplicates harmless but not free. With the guard the record stream is the
  agent's commit history rather than a sampling of its loop iterations.
- **Digest derivation is best-effort.** `RecipeDigest` failing skips the
  record instead of failing the commit, exactly as `agentSpanAttrs` treats the
  same failure: an agent whose digest cannot be derived stays observable and
  addressable, it just cannot be resumed from — and §3.2 already says a trace
  with no anchor fails the restore rather than guessing.
- **The consumer keeps latest-wins and grows two fields.**
  `AgentNode.SnapshotDigest` and `AgentNode.StopReason`, folded in
  `buildAgents` from the newest loop span, with `ingestAgentSnapshot` as a
  second ingest branch beside `ingestAgentState` (both consumed as data, both
  bumping `db.mutations` so the memoized roster cannot go stale). §3.1's
  "the state held before teardown" needs record HISTORY and is deliberately
  left to slice 3's `RestorePlan`, which is where the state mapping lives.
- **Measured, not assumed: the anchor names the committed conversation.**
  `TestResumeAnchorRecords` (core/integration/agent_runtime_test.go) does not
  assert that a digest was published — it rebuilds the conversation from the
  digest through the client's own call payloads and reads its transcript,
  finding the message the agent drained. That is the property the record
  exists for, and the one §3.2's span-scan alternative gets wrong precisely
  when a turn was interrupted. Both halves were seen failing first, with the
  producers disabled one at a time.
- **`SESSION` has no integration coverage, on purpose.** Session teardown's
  record races process exit (§4.4's honesty note), so the reason is pinned by
  a unit test on `KillAll` (`TestKillAllStopsWithSessionReason`) instead of a
  live session. The record's absence already reads as a crash, which is the
  correct reading either way.

### 13.2 Slice 2 — the restore verb (§4.1, §4.2, §4.5)

- **`rehydrate` landed with §4.1's schema verbatim**, ID-returning like every
  other imperative verb, and the existence check is what makes an out-of-order
  restore loud. Three refusals were added, all of the same kind — the engine
  will not be told something only a mis-projection could say:
  - **RUNNING and WAITING_INPUT are refused.** §3.1 maps a crashed RUNNING
    agent to IDLE in the CLIENT's projection; the engine now also refuses the
    unmapped value, so a client that skips the mapping fails at the restore
    rather than creating an entry whose state is a lie about a loop that does
    not exist.
  - **An `error` without `FAILED` is refused**, since the only way to produce
    one is to have projected the state wrong.
  - **`FAILED` with no error text synthesizes one.** FAILED projects from
    `done` + a non-nil `loopErr`, so an empty error would quietly project
    STOPPED — foreclosing the very resume-retry the restore was asking for.
    An error nobody recorded is still an error that happened.
- **A restored `STOPPED` tombstone records `EXPLICIT`.** Only an explicitly
  stopped agent is ever restored as STOPPED — §3.1 restores a teardown stop as
  the state held before it — so stamping the reason keeps chained resume
  idempotent: the resumed session's own trace says exactly what the source
  trace said, rather than degrading to a reasonless record the next restore
  would refuse.
- **§4.2 forced a change §4.1 implied but did not state: `spawn` creates the
  registry entry.** "spawn is mint-create-pin" was true of the design and not
  of the code — the entry was created lazily by whichever verb touched the
  instance first, and that verb was overwhelmingly `send`. Making a miss an
  error without that would have broken every ordinary spawn-then-send. So
  entry creation is now exactly the two verbs that create an instance
  (`spawn`, `rehydrate`); `pause`/`resume`/`interrupt`/`waitFor`/`stop` keep
  their lazy `GetOrCreate`, because none of them can boot a loop from a stale
  seed the way `send` did — they act on an entry, they do not feed one.
- **The defect §4.2 closes was observed live, not argued.** The fail-first run
  of `TestSendRequiresRuntime` shows the trace doing exactly what §10.2 of
  async-agents described: `Agent.send` on an instance the session never minted
  opened a loop span (`agent: ghost`) and delivered the user's message into
  it. One consequence for tests: `TestMessageIdentity`'s "agent with no
  runtime entry" case had to move from a freshly spawned agent to a bare
  `agent(id:, name:)` handle — which is the shape a client actually holds for
  an unrestored instance, so the coverage improved by moving.
- **The identity span is opened on a detached context.** `AgentRuntime.spanCtx`
  is retained past the call (that is the whole point — later transitions still
  publish), so it is started from `context.WithoutCancel(ctx)`: records
  emitted on the request's context once the request has returned would be
  publishing into a canceled context.
- **`rehydrate` publishes the snapshot digest too**, not only the state record
  §4.5 specifies. A restored agent with no anchor in the NEW trace would make
  the resumed session unresumable in turn, which §8 requires to work; it costs
  one call, and `TestRehydratePublishesIdentity` asserts the whole loop a
  client walks — roster entry, addressable call digest, rebuilt handle, and
  through it the restored conversation.
- **Generated clients: Rust is stale and stays stale.** The Go, Python,
  TypeScript, PHP and Elixir clients plus the GraphQL schema doc were
  regenerated. `rust-client:apiclient` fails on `AgentMessage.await`
  (`pub async fn await` is not valid Rust) — a pre-existing break from the
  async-agents slice, unrelated to this one, and `sdk/rust` was left untouched
  rather than papered over.

### 13.3 Slice 3 — the client projection (§5.2)

- **`RestorePlan` landed with §3.2's struct, plus one field: `Err`.** The
  refusals §3.1 and §4.4 require (a `STOPPED` record with no reason, an agent
  with no snapshot digest) are reported ON the entry rather than by failing the
  whole projection, because the projection is the only place that knows *which*
  agent is unrestorable and why. The caller fails the command on the first one
  (§5.3.3), and `--partial` skips exactly those entries — which a plan that
  refused wholesale could not express. A refused entry still carries every fact
  that did resolve, so the failure can name the agent, its ID and its anchor.
- **Unknown state tokens are refused too.** A trace from a newer engine that
  publishes a state this client does not know is not guessed at, for the same
  reason the other two refusals exist: every reading is a guess, and the wrong
  one either resurrects an agent or buries it. §12's "no compatibility
  fallbacks" cuts both ways, not just backwards.
- **`WAITING_INPUT` maps to `IDLE`, alongside `RUNNING`.** §3.1's table does
  not list it, and the engine does not publish it today (the projection has no
  such case — `core/agent.go:684-716`), but the token exists in the enum and
  slice 2's `rehydrate` explicitly refuses it. The mapping is forced by the
  same argument `RUNNING` is: nothing is left to answer the question, so a
  roster redisplaying the agent as parked on one would be lying.
- **The pre-teardown state is one field on the SPAN, folded at ingest.**
  `ingestAgentState` records the state on every record that is not a
  session-teardown stop, beside its latest-wins fold; `buildAgents` surfaces it
  as `AgentNode.PreTeardownState`. Two consequences worth stating: an EXPLICIT
  stop IS recorded as a pre-teardown state (only the SESSION one is skipped),
  which is why `restorableState` accepts `STOPPED` — a `STOPPED` pre-teardown
  state can only have come from a dismissal. And the fold across an agent's
  loop spans takes the newest one that EXISTS rather than the newest span's, so
  a relaunched loop whose only record is the teardown stop cannot erase what
  the previous loop left behind.
- **§5.2's "ignore agents whose only spans came from the live session" had to
  become "ignore agents with ANY live-session span".** Slice 2's §4.5
  publication is what changes it: a re-hydrated agent republishes identity,
  state and snapshot into the new trace, so after a restore its roster entry
  holds spans from BOTH traces and looks exactly like a source-trace one. Under
  the original wording it would be re-hydrated again on the next projection —
  which `rehydrate`'s existence check refuses, loudly, having already been
  restored correctly. The sharper rule states the real property: an agent with
  a span in the live trace has a runtime entry in this session already, whether
  spawned here or restored here, and that is precisely the condition
  `rehydrate` refuses.
- **The live/imported discriminator is the TRACE ID**, taken from the primary
  span (falling back to the root). It needs no cooperation from the import,
  which is what keeps §5.1's "do not make the source trace a rendering input"
  intact — provenance is a restore-plan input, not a rendering one. It also
  leaves §12's open question open in the right direction: the rule is "not the
  live trace" rather than "trace X", so holding several foreign traces widens
  the plan rather than breaking it.
- **The loop error is read from the newest loop span carrying one**, not from
  the newest span outright: a resume-retry relaunch that succeeded must not
  restore the failure it recovered from, and only the mapped state decides
  whether an error is carried at all (§4.1 refuses an error without `FAILED`).
- **`CallIDForDigest` landed as §5.2 describes, and dropped one behaviour.**
  `encodedIDForCallDigest`'s scan tried EVERY span carrying the digest and
  returned the first that rebuilt; resolving through `db.CallPayloads` there is
  exactly one answer per digest, so the fallback has nothing to fall back to.
  `Span.CallID` keeps its own "no call for span" guard for a span with no
  digest at all, and both callers are now one line over the shared walk.
- **Fail-first, including the negative cases.** Every assertion was seen
  failing against a stubbed `RestorePlan`/`CallIDForDigest`. The two tests that
  a stub cannot fail — the ones asserting an EMPTY plan for live-session agents
  — were checked by disabling the live-trace filter instead, and both reported
  the agent the filter is there to leave out.

### 13.4 Slice 4 — the import (§5.1.1, §5.1.2, §5.1.3)

- **The import shim is `enginetel.TraceImporter`
  (`engine/telemetry/traceimport.go`), and the sinks are an argument.** §5.1
  puts the sealing and the passthrough stamp on the protobuf, before
  `SpansFromPB` / `ReexportLogsFromPB` / `ReexportMetricsFromPB`, so the shim
  belongs beside those helpers rather than in the CLI or in `internal/cloud`:
  it needs OTLP and the exporters and nothing else, which is what lets slice 5's
  SSE client feed it live requests while slice 4's tests feed a canned capture
  through the identical path. It takes `TraceImportSinks{Spans, Logs, Metrics}`
  rather than a `Frontend`, so `dagql/idtui`'s tests can point it at a bare
  `dagui.DB` — the frontend's exporters are wrappers around exactly those calls
  — and so nothing in `engine/telemetry` has to import the TUI.
- **`Seal` is a verb, not a stream event.** The unfinished spans cannot be
  sealed as they arrive: a live span is exported at START and again at end
  (`LiveSpanProcessor`), so "no end time" is a claim only the END of the stream
  can settle. The importer buffers the running ones by span ID, drops any that
  a later update ends, and re-exports the remainder when the caller says the
  stream is done. That makes the seal one export of the leftovers instead of a
  rewrite of the capture, and it is what slice 5 calls when the three SSE
  streams close.
- **The seal re-exports a COPY.** The sink was handed the original protobuf and
  a frontend consumes its exports asynchronously (`prettySpanExporter.dispatch`),
  so stamping an end time onto the span already in flight would be a data race
  on a value the TUI is reading. `proto.Clone` is the whole fix; the clone also
  carries the passthrough stamp, so a sealed root stays passthrough.
- **`LeftRunning` needed wire vocabulary, and got
  `dagger.io/dag.left_running`.** dagui has the field but has only ever DERIVED
  it — `db.go:951-963` sets it when the DB's own root ends — and a fact derived
  locally cannot be stated by an importer working on the protobuf. It qualifies
  `dagger.io/dag.canceled` rather than replacing it, which is exactly the
  difference the UI renders: "left running after the root span completed"
  instead of "says it is canceled". Defined in `engine/telemetryattrs` for the
  usual reason (the canonical home would be the external `otel-go`).
- **Every parentless span is stamped passthrough, not "the root".** A capture
  can carry more than one — a partial fetch, or a trace whose real root never
  reached Cloud — and each is a second parentless span as far as the live DB is
  concerned, which is the only property §5.1.1 is about.
- **§5.1.1's "those contain" is measured.** Stamping `Encapsulate` on the
  imported root alongside the passthrough was probed, and the focused agent's
  transcript went back to holding only the live turn — WITH §5.1.3 already in
  place. So the two halves are independent: whole-trace surfacing does not
  rescue a contained subtree, and containment would have quietly undone the fix.
- **The seal's end time is the imported root's, falling back to the newest
  timestamp the capture carried**, and the fallback is the common case: a
  session that crashed hard enough to leave its loop spans open usually left its
  root open too. Neither value is the truth about when that work stopped —
  nothing recorded it — but both say "no later than this", which is the reading
  the DB's own sweep already takes for its own root. A span is never sealed
  before its start.
- **§5.1.3 landed as one line, and cost one behaviour change.**
  `SurfacedConversationForSpan` no longer calls `db.surfaceRoot`, so nil means
  the whole DB; `buildSurfacedConversation` already handled a nil root (its
  containment walk only needs `root != nil` for the reached-root test), so
  nothing below changed. The behaviour that changed is the SEVERED chain: a
  message whose ancestor chain ends at an unreceived placeholder was contained
  under the old rule and now surfaces for the whole-DB question.
  `TestSurfacedConversationHidesContainedMessages` was rewritten to say so, and
  to keep the old expectation for an explicit root. It is the same case
  generalized rather than a regression: an imported message is severed from
  `db.RootSpan` by construction, and "can't be proven to be under the root" is
  not a question the whole-DB query asks — only "does a Boundary or Encapsulate
  contain it" is, which is what `HasConversationForSpan(nil)` has always asked.
  The rewritten test now pins the two agreeing.
- **The whole-trace conversation is now the only surfacing family that reads
  nil as "the DB".** `SurfacedChecks`/`Generators`/`Services` keep
  `db.surfaceRoot`, deliberately: their fixture-containment rule has tests, and
  they have no second trace to miss. Both `db.surfaceRoot` and
  `frontendPretty.surfaceRoot` say so in a comment, because the divergence is
  the kind that gets "tidied up" by someone who has not read §5.1.3.
- **Two nil dereferences on the shared re-export path had to be defended
  against, and they are not hypothetical.** `otel.ResourceFromPB` reads
  `pb.Attributes` on a nil `Resource`, and `otel.LogValueFromPB` reads `v.Value`
  on a nil `Body`; both panicked on the first run of the canned capture. A
  resource-less payload and an attribute-only record are legal OTLP, and the
  empty-bodied record is precisely what agent state, snapshot digests and call
  payloads ride — §12's open question is whether Cloud returns that body at all.
  Each `Import` method fills the field in rather than let a missing one take the
  CLI down mid-restore. The fix belongs at the decode boundary, though, and
  landed there as dagger/otel-go#17 (which found a third, `attrValue`, on the
  span-attribute side — reachable because `AttributesFromProto` skips nil
  elements but passes a nil `Value` through); the guards here are a stopgap to
  delete with the otel-go bump, and the file says so. Worth noting the panic was
  never resume-specific: the engine's own `POST /v1/logs` handler
  (`engine/server/telemetry.go:141`) feeds `ReexportLogsFromPB` directly, so a
  body-less record from any client would have panicked that handler too. Every
  in-repo producer sets an explicit empty body, which is why nobody had hit it.
- **Fail-first, including the negative cases.** The three behaviours were seen
  failing against a plumbing-only importer that re-exported the capture
  unchanged: unfinished spans still running, the imported root unstamped, and
  the promoted transcript holding only the live turn — §5.1.3's defect,
  reproduced. The two assertions a stub cannot fail were checked by breaking
  what makes them pass: the primary-span one by repointing the primary at the
  imported root, and separately by routing the LIVE trace through the importer
  too (which seals it, failing both halves of that test); the roster merge by
  giving the imported chief a different instance ID, which split it into two
  entries.

### 13.5 Slice 5 — the fetch (§5.1)

- **`cloud.OTLPClient` (`internal/cloud/otlp.go`) is transport and nothing
  else.** It GETs the three §5.1 endpoints, reads each as SSE, `protojson`s
  each event into the matching export request, and hands the request over
  UNCONVERTED. §5.1's "re-export through `SpansFromPB` /
  `ReexportLogsFromPB` / `ReexportMetricsFromPB`" is what the reference
  implementation does and what §13.4 superseded: those calls now live inside
  `enginetel.TraceImporter`, wrapped in the passthrough stamp and the seal,
  both applied to the protobuf before conversion. Converting here would have
  looked like it worked and silently skipped both fixes.
- **The sink is an interface (`cloud.TraceImportSink`), not
  `*enginetel.TraceImporter`.** Four methods, satisfied by the importer as it
  stands. It keeps `internal/cloud` free of any opinion about sealing or
  rendering — the package's whole non-test dependency footprint is still
  `engine/slog` plus `internal/cloud/auth` — and it is what lets a test wrap
  the real importer to record the call sequence.
- **The three streams run SEQUENTIALLY, spans first, and the reason is the
  SINK, not the importer.** `TraceImporter` is concurrency-safe, so the
  question was live. But a sink is an OTel exporter, and those are documented
  as not needing to be: `SpanExporter.ExportSpans` is "called synchronously,
  so there is no concurrency safety requirement", and `sdklog.Exporter.Export`
  "should never be called concurrently with other Export calls". §13.4
  deliberately made the sinks an argument precisely so a bare `dagui.DB` can
  be one, and a bare `dagui.DB` has no lock at all; the frontend's exporters
  only serialize because they dispatch onto the UI goroutine, which is their
  business and not a promise the interface makes. Fanning out would impose a
  requirement on every sink anyone ever passes. This is measured rather than
  argued: the fail-first run of the fan-out shape trips the race detector five
  ways inside `dagui.DB`, one of them `ingestAgentState`'s `initSpan` racing
  `recordOTelSpan` on the same span — a log record and its span arriving
  together.
- **Spans first buys two more things, and the cost is one startup fetch.**
  The seal becomes trivially "after the span stream ended", with no window for
  a late span to land already-sealed and keep a stale end time. And every span
  a log record names is real by the time the record arrives, so the record
  folds onto it instead of minting the placeholder `dagui.DB` allocates for an
  unknown span ID — a parentless, never-ended span that the importer never saw
  and therefore never seals (`db.newSpan`'s own TODO says it "fools things into
  thinking they're a root span"). What it costs is the sum of three round trips
  instead of the max, once, in a place §5.3 already runs under a span.
- **`Seal` fires between the span stream and the log stream, not at the end.**
  Nothing in the log or metric streams can move the seal's bound — the importer
  computes it from span timestamps alone — so sealing at the earliest correct
  moment keeps the span half self-consistent even if a later stream fails.
- **A payload that will not decode FAILS the fetch.** The reference client
  warns and carries on, which is right for a renderer and wrong here: an
  undecodable event is a lost fact — an agent's state record, a call payload, a
  whole subtree — and §12 settled that a trace which cannot be rebuilt fails
  the restore rather than degrading. For the same reason `protojson` stays
  STRICT rather than gaining `DiscardUnknown`: with the endpoints undeployed,
  the first thing that will happen is somebody pointing this at a URL that
  returns something else, and unknown-field tolerance would turn that into a
  silent empty import instead of an error naming the stream.
- **A canceled context is not an end of stream.** The reference treats
  `context.Canceled` as EOF; here it returns `ctx.Err()`, because "the user hit
  ctrl-C" and "that was the whole trace" must not be the same outcome when a
  restore is about to be built on the result.
- **The event NAME is ignored.** Cloud's event vocabulary for these endpoints
  is unverified (§12), so the client reads payloads and treats end-of-stream as
  end-of-trace, exactly as the reference does. `TestFetchIgnoresTheSSEEventName`
  exists to stop someone "tightening" this to a name nobody has promised, which
  is the one change that would turn a whole trace into silence.
- **Two small deviations from the reference, both bugs there.** Auth: the
  reference's default branch sends `Authorization: OIDC <token>`, because
  `oauth2.Token.Type()` echoes an unrecognized type back verbatim — while
  `cloud.NewClient` translates the same credential to a Bearer token
  (`client.go`). The OIDC case is now spelled out, so the two clients cannot
  disagree about one auth mode. URL: the reference assigns `u.Path`, which
  silently drops any path prefix on `DAGGER_CLOUD_URL`; this uses `JoinPath`,
  like the GraphQL client.
- **The `--debug` stats bucket comes along for free.** The fetch feeds the
  package's existing `clientStats`, so `OTLPClient.StatsSummary()` reports
  requests/events/bytes/records per stream — the same affordance
  `dagger trace --debug` has, on what will be the largest fetch in the product.
- **dagger/otel-go#17 has NOT merged**, so §13.4's three stopgap nil guards
  stay exactly where they are, and the canned capture keeps its body-less
  record as the thing that will prove the bump carries the fix. The pin is
  still `v1.43.1-0.20260515012101-af7cd0684887`.
- **The endpoints were deployed while the slice was in flight, and §12's two
  assumptions are now MEASURED rather than owed.**
  `TestLiveCloudFetchRoundTrip` and `TestLiveCloudPreservesCallAttributes`
  (`dagql/idtui/trace_live_test.go`, skipped unless `DAGGER_CLOUD_TRACE` names
  a trace) run the real client against `api.dagger.cloud`, and one CI trace
  (426 spans, 486 log records) answers both:
  - **Attribute-only, empty-bodied records survive.** 386 of 486 records came
    back attribute-only, and 177 call payloads reached `db.CallPayloads` — the
    span-free rebuild channel §5.2 and §3.2's failure mode 2 depend on. Cloud
    returns the body as an explicit empty string, never absent: `noBody` was 0.
    So the guard §13.4 added for a nil `Body` is not what stands between the
    Cloud path and a crash; it stays for the shape the engine's own
    `POST /v1/logs` handler can still be handed, and for dagger/otel-go#17.
  - **Span attributes come back byte-identical.** 106 of those payloads
    rebuilt into IDs, and every one re-digested to exactly the digest that
    keyed it — 0 mismatches. That is the property stated where it bites: a
    payload names itself by digest, so any re-encoding would surface as a
    rebuilt ID digesting to something else, and with it every handle the
    restore rebuilds.
- **The wire shape was ratified, with one correction to the fixture.** The real
  stream opens with a named, data-less `connected` event and then sends
  payloads as UNNAMED events — so ignoring the event name was right, the fake's
  default (empty name) was right, and the client's "an event with no data is
  not a payload" branch is load-bearing rather than defensive, since it is the
  FIRST thing Cloud sends. The fake now emits the preamble too. protojson
  carries IDs base64 and 64-bit ints as strings, both of which
  `protojson.Unmarshal` handles without help.
- **The remaining gap is an AGENT trace.** The probe trace is CI: no agents, so
  no `dagger.io/agent.state`, `.snapshot.digest` or `.stop.reason` records, and
  its metrics stream returned 0 records — the endpoint answers, but "imported
  metrics count" (§12) is still unproven with data in it. §10's end-to-end test
  closes this, and it is slice 6's.
- **One number slice 6 should not be surprised by: 71 of the 177 call payloads
  did not rebuild**, failing exactly as §9's first row describes — `cannot
  rebuild ID for "withMountedFile" (Container): call xxh3:19c85d5bfb5ce96c
  never reached this client, referenced as argument "source"`. That is an
  ARGUMENT frame whose payload was never published, not a Cloud round-trip
  defect, and on this trace it is routine rather than exceptional. §5.3.3 fails
  the restore on exactly this error, so whether an agent's SNAPSHOT chain is
  affected — a different chain, mostly string arguments — is a question the
  end-to-end test has to answer rather than assume.
- **The probe ran unauthenticated, against a PUBLIC trace, and that is by
  design.** Traces from public repositories are readable without a credential —
  the probe trace is a `vito/dang` PR build — while presenting an INVALID one
  is still rejected (`401 {"message":"invalid API key"}`). No credential was
  available where the measurement was taken, so the numbers above describe the
  unauthenticated response: the same handler and the same trace, but worth
  stating exactly rather than implying a token was used. Nothing about the
  client changes — it always sends the header, which is what a private trace
  needs, and its 401 path surfaces the server's message verbatim.
- **The test drives the real fetch into a real `dagui.DB`** — one live session
  already publishing, a `httptest` server speaking the three endpoints, and
  slice 4's importer between them. It reuses `trace_import_test.go`'s canned
  capture verbatim (same crashed session, same never-ended spans, same
  empty-bodied state record) and adds the metric stream slice 4 had no reason
  to exercise: an LLM token gauge with no `Resource`, which is both §12's
  "imported metrics count" and a live run through the third stopgap guard. A
  gauge rather than a sum because otel-go's `metricFromPB` converts only
  gauges today.
- **Fail-first, against the reference implementation's own shape.** The stub
  was §5.1-as-written: three streams fanned out across an errgroup, no seal,
  decode errors warned past. Four tests failed, each on its own defect — the
  request order nondeterministic, the sink's call log reading
  `[logs metrics spans]` with no `seal` in it, an undecodable payload
  swallowed, and a 503 on the span stream still importing logs — plus the race
  detector firing on the DB. The assertions a stub cannot fail were checked by
  breaking what makes them pass: "the fetch sealed the live trace too" by
  serving the LIVE trace through the fake Cloud as well (which seals it, and
  moved the seal's bound while it was at it — the "newest timestamp seen" bound
  visibly failing for an unbounded fetch); the auth header by blanking it; the
  event-name one by skipping named events, which is exactly the tightening that
  test exists to prevent.

### 13.6 Slice 6 — the CLI (§5.3, §5.4)

- **`AgentRestorer` landed with §5.1's two methods, verbatim**
  (`dagql/idtui/frontend.go`), implemented by the pretty frontend with the
  blocking-dispatch idiom `SurfacedFailedCheckSpans` already uses for the same
  reason. `encodedIDForCallDigest` stayed a package-level func and gained a
  method wrapper rather than moving: it has three in-package callers that are
  already on the event loop, and exporting it would have made "read the DB
  without the lock" the easy path. A frontend that does not implement the
  interface fails `--trace` with its type in the message — a plain/dots
  frontend holds no span DB, so restoring nothing quietly would be the worst
  outcome available.
- **The plan grew one field, `LastActivity`, and the reason is §3.1c's second
  sentence.** "Several top-level agents means focus the most recently active"
  is not answerable from the plan's ORDER: `Agents()` orders by when each agent
  first appeared, and the two disagree in the ordinary case — a session's own
  conversation is the first agent to appear and usually the last to speak. It
  is the newest timestamp across the agent's loop spans, falling back to a
  span's START when it never ended, because an imported trace's unfinished
  spans are all sealed to one shared bound (§5.1.2) and ends alone would tie
  every agent of a crashed session together.
- **Execution is three phases, not one loop, and the split is load-bearing.**
  Every anchor is rebuilt BEFORE anything is re-hydrated, so a refusal leaves
  the engine untouched — §3.1b's "all-or-nothing" is otherwise only true of the
  command's exit code, not of the session it leaves behind. Then every entry is
  re-hydrated, then every one is attached. The fail-first run against the naive
  interleaved shape failed four ways: the call order, both refusals creating
  entries before failing, and the "nothing restorable" guard missing entirely
  (it panicked).
- **The restore verb is spelled `node(id:) { ... on LLM { … } }`**, not §3.2's
  `loadLLMFromID`. Same call — that is what the generated clients' `Ref` builds
  — and it is the form `core/integration`'s `rehydrateAgent` already used, so
  the CLI and the tests exercise one string.
- **`--partial` skips exactly the refused entries, and still fails when that
  leaves nothing.** A best-effort restore that restores nothing is an empty
  session that looks like it worked, which is the outcome §12 rules out.
- **Focus by `--agent` resolves instance IDs before names, and refuses an
  ambiguous name.** A name is a display label two agents may legitimately share
  (async-agents §8 spent the spawn pivot establishing that); an ID never is.
- **Restored conversations are owned** (`AttachRestored`, a second entry point
  onto the same `attach`), because the session that published them is gone and
  nothing else can drive them. They also inherit the composed agent group as
  their `.clear` target, so clearing a restored conversation returns to
  `dagger agent`'s agents rather than a bare workspace-bound LLM.
- **§8's blocker was cleared, not worked around: `Staff.dismiss` lost its
  effectful recorded form.** The stop stays (it must really happen); the
  bookkeeping moved to a pure `withDismissed`, symmetric with the `withWorker`
  that `spawn` already had. Without it, loading a restored chief's chain
  re-executes `dismiss` and stops a worker the restore just re-hydrated.
- **A real defect fell out of the end-to-end test on its first run: `DB.Call`
  could not terminate on a missing payload.** Its last resort is the creator
  walk, keyed on a span's OUTPUT digest and answering with that span's CALL
  digest — routinely the same value. With a payload present the payload branch
  returns first; with one MISSING the walk recurred on the digest it started
  from and blew the stack. A missing payload is precisely what a resume must
  REPORT (§9's first row), so this was on the failure path the whole design
  leans on. Fixed with a visited set, and pinned by a unit test.
- **§13.5's 71-of-177 number does not describe agent chains.** The worry was
  argument frames whose payloads were never published; the end-to-end test
  restores an agent whose chain carries exactly that shape — `llm.withTools(
  object: <Directory ID>)`, an ID literal, which is what makes a chief a chief
  — and it rebuilds. So a snapshot chain is not the shape that fails, and
  §5.3.3's failure path is a real guard rather than a routine one.
- **Manufacturing that failure took more than withholding the log records.**
  The first attempt served the spans and dropped the call-payload records, and
  the anchor rebuilt anyway: a frame that gets a span of its own carries its
  payload ON the span (`dagger.io/dag.call`), and the log channel is the
  fallback for frames that structurally never get one. In a session this small
  every frame gets a span. The test now strips both channels, which is what an
  incomplete trace actually looks like to a client.
- **The end-to-end test is three sibling agents, not a chief with two workers
  spawned through `modules/staff`.** Every §10 assertion is about restoring
  three agents of which one was dismissed, and none of them needs the
  chief→worker NESTING; the projection's `ParentAgentID` and the CLI's focus
  rule have their own unit coverage. Going through the staff module would have
  built the slice's only end-to-end test on `staff_test.go`'s single test,
  which is skipped as known-broken (async-agents §11 thread 15).
- **What settles "a send continues the conversation" is the replay provider,
  not an assertion.** It diverges on any history but the recorded one, so the
  restored chief's second turn resolving at all is the proof. Seen failing by
  re-hydrating the chief from its SEED instead of its anchor — the amnesiac
  twin — which produced an empty transcript.
- **Chained resume (§8) is measured here too**, since the test's second session
  publishes into a sink of its own: every restored agent's anchor rebuilds from
  the RESUMED session's own trace, which is the property resuming that trace in
  turn depends on.
- **`cloud.OTLPClient` gained `WithBaseURL`.** The end-to-end test serves the
  §5.1 endpoints itself, and `core/integration` runs parallel, where setting
  `DAGGER_CLOUD_URL` is a process-wide mutation reaching every other Cloud
  client. The credential is likewise constructed rather than read from the
  environment; a Basic token renders its header with no network of its own.
- **The live Cloud test was NOT run against an agent trace**, and the task
  asked for it. It skips unless `DAGGER_CLOUD_TRACE` names a trace and
  `auth.GetCloudAuth` yields a credential, and this work had neither an agent
  trace ID nor a token — §13.5's numbers were taken against a public CI trace.
  What replaces it is weaker in one specific way and stronger in another: the
  end-to-end test round-trips a REAL agent session's records (state, snapshot
  digest, stop reason, call payloads) through protojson-over-SSE and restores
  working agents from them, so the shapes are proven on the wire; what stays
  unproven is Cloud's own storage of those particular records. §12 says so.
- **dagger/otel-go#17 has still NOT merged** (checked at
  `48b87b1`; the pin is still `v1.43.1-0.20260515012101-af7cd0684887`), so
  §13.4's three stopgap nil guards stay exactly where they are.
- **The `golang:lint-all` debt from earlier slices was swept, in its own
  commit**: trailing blank lines gofmt strips in `core/agent_telemetry.go` and
  `core/integration/agent_runtime_test.go`, and five `//nolint:gosec`
  directives with nothing left to suppress (G115 is excluded repo-wide, and
  gosec is excluded from test files entirely). `lint-all` is still red, but on
  16 issues that predate this feature entirely — `dupl` in `core/schema`,
  `core/workspace.go` and `dagql/dagui/checks.go`/`generators.go`, three
  `gocyclo`, two `unparam`, two `gocritic`, and a `nolintlint` pair in
  `core/schema/directory.go`. Nothing this slice touched contributes to it.
