# Agent messaging: turns are the unit of waiting

Proposal. Companion to hack/designs/async-agents.md: it extends that design's
messaging model (§3), corrects one of its claims (§3.4), and consolidates its
open agent-to-agent threads — §7's `awaitAny` question and §10 items 2
(enqueue guards), 3 (provenance), 6 (combinators), 11 (idle notification) —
into one design, motivated by several days of dogfooding `modules/staff`.

The one-sentence version: **the mailbox was the right kernel and the blocking
awaits punched holes in it — every cross-agent interaction becomes a
provenance-stamped message, an agent waits only by ending its turn, and the
blocking verbs survive solely for callers that are not agents.**

## 1. Evidence: six pitfalls from dogfooding

Numbered for reference throughout; each maps to where async-agents.md already
tracked it (or didn't).

- **P1 — Deadlocks everywhere.** Three shapes observed or derivable, mechanics
  in §2. Tracked as item 2 (cycle detection, "none exist") and §8's self-await
  hazard, but the doc treated guards as hardening; dogfooding shows the
  blocking verbs are the defect.
- **P2 — Messages are indistinguishable.** A worker's `askChief` question, a
  peer's message, and the user's prompt all land as bare `withPrompt` text.
  The CHIEF'S MODEL cannot tell who is asking — it answers a worker as if the
  user spoke — and neither can any client. Tracked as item 3 (provenance
  stamping, unbuilt).
- **P3 — Agent messages render as user messages.** The influence⇔append rule
  (async-agents §3.2) is right that a consumed message must enter the
  history; the pitfall is that with no recorded origin, every frontend MUST
  render it as if the user typed it. The history entry is correct; its
  anonymity is the bug. Item 3's client-facing half.
- **P4 — Waiting is single-target.** `collect` blocks on ONE worker's
  `waitFor(IDLE)` (modules/staff/main.dang:253); every other worker's
  completion during that wait is silently missed and must be re-discovered by
  polling `status`. Tracked twice: item 6 / §7 (`awaitAny`/`awaitAll`) and
  item 11 (idle notification) — which are competing answers to the same
  need, and this design picks one.
- **P5 — `collect` hangs forever on FAILED.** `waitFor(state: IDLE)`
  (core/agent.go:1998) waits for exactly one state, and a FAILED loop never
  reaches it. Documented as a WARNING on the tool and in the chief prompt —
  i.e. worked around by prompting, owned by nobody.
- **P6 — `ask` holds the callee hostage.** `AgentMessage.await` resolves with
  the final reply of the consuming TURN (core/agent.go:705), so answering an
  `askChief` requires the chief to END ITS TURN, and the answer is the same
  text addressed to the user — two conversations conflated in one reply.
  Meanwhile the asker sits blocked inside a tool call, unable to do anything
  else. No existing thread; §3.4's own parked-question reasoning covers it
  and was never applied to agents.

## 2. Diagnosis: receiving is not progressing

async-agents §3.4 argued that agent-on-agent asks may block freely because
"an agent blocked in an `ask` tool call is still *receiving*, so asks landing
meanwhile queue or steer rather than deadlock." That is true of ENQUEUE and
false of everything after it. `enqueue` (core/agent.go:1084) accepts the
message and stamps STEERED delivery evidence — but consumption happens only
when the loop calls `drainMailbox` at a step boundary (core/agent.go:1267),
and reply resolution only at turn end. A turn blocked inside a tool call
reaches neither. The claim conflated three distinct events — receipt,
consumption, resolution — of which a blocked agent performs only the first.

The three deadlock shapes, precisely:

- **`ask` ↔ `askChief`** (mutual await). Chief's turn is inside
  `worker.send(m).await`; worker's turn is inside `chief.send(q).await`. Each
  await resolves at the other's turn END; each turn is blocked in the very
  tool call that prevents its end. Both messages are duly enqueued, hinted
  STEERED, and never consumed.
- **`collect` ↔ `askChief`** (await against wait-for-state). Chief blocks in
  `waitFor(IDLE)`; the worker's turn is open (blocked in `askChief`), so it
  is never IDLE; the chief's turn never ends, so `askChief` never resolves.
- **`sendTo` ↔ `askChief`** (the wedge — not a classical cycle, and the
  subtlest). The worker asks; the chief, mid-turn, answers the natural way:
  `sendTo(worker, answer)`. The answer lands in the worker's MAILBOX — but
  the worker is blocked inside `askChief`, not parked at a step boundary, so
  it cannot drain it. `askChief` resolves only at the chief's turn end, which
  a busy chief may not reach for many minutes. The chief answered; the worker
  never heard it; nothing is technically deadlocked and everything is stuck.
  This is the smoking gun that the two channels — turn-boundary messages and
  turn-progress awaits — DO NOT COMPOSE: a reply delivered as a message
  cannot unblock a waiter that is blocked as a tool call.

Even acyclic, non-wedged blocking is bad: an `askChief` under a long chief
turn starves the worker for the turn's whole duration (P6), and any blocked
wait hides a potential cycle the model issuing it cannot see.

async-agents §2 named the kernel correctly: "mailbox + blocking receive is
the minimal complete kernel (actors)." In the actor model an actor blocks
ONLY in receive. The agent runtime has exactly that — an IDLE loop blocked in
receive, woken by `enqueue`'s poke (core/agent.go:1134) — and then `await`,
`waitFor`, and the tools built on them (staff's `ask`, `collect`,
`askChief`) reintroduced blocking in the middle of turns, where the actor
model forbids it. §3.4 got the human case right for exactly this reason (the
human is not a blockable callee; park the question as data) and then failed
to apply its own argument to agents.

## 3. The rule

**An agent's turn never blocks on another agent's progress. An agent waits by
ending its turn: IDLE is the wait state, and the mailbox is the waker.**

Corollaries:

- Replies are messages, not blocked returns (§4.2).
- Lifecycle changes are messages, not polled or awaited states (§4.3).
- The blocking verbs (`AgentMessage.await`, `Agent.waitFor`) remain — for
  callers that are NOT agent turns: the CLI prompt (submit = send + resume +
  await), module code driving agents imperatively, tests. A human caller can
  Ctrl-C; a turn cannot.
- The engine enforces the rule where it can and detects violations where it
  cannot (§4.5), instead of documenting deadlocks in prompts.

Nothing about the value model changes: LLM stays immutable, influence⇔append
stands (consumed messages enter history as honest selectors), and the
off-the-record branch (`snapshot.withPrompt(q).loop`) remains the
consultation-without-influence route.

## 4. Design

### 4.1 Provenance: every message knows its sender (P2, P3)

All delivery already flows through one enqueue path (`AgentRuntimes.Send`,
core/agent.go:629), and every tool dispatch inside an agent turn already
carries the calling agent's handle in context (`AgentFromContext`,
core/agent_context.go). So sender identity needs no new argument for the
common case: `Send` resolves an origin at enqueue —

- **agent**: `AgentFromContext` present → instance ID + display name of the
  sending agent;
- **user**: no agent in context → the sending client (main client vs. a
  nested/attached one, from client metadata);
- **event**: synthesized by the engine itself (§4.3).

The origin is recorded HONESTLY: `drainMailbox` currently records a bare
`withPrompt(prompt:)` (core/agent.go:1295); it grows an `origin` argument (a
small input object: kind, agent ID, agent name, message ID, replyTo — exact
spelling at implementation time), so the conversation chain itself says who
said what and replay is byte-stable. Three consumers:

- **The model.** The provider render path prepends a deterministic
  attribution header to non-user messages (e.g. `[message from scout
  (#m7f3)]`, `[reply from chief to #m7f3]`, `[event: scout is idle]`). This
  is what fixes P2 at the root: the chief's model stops answering workers as
  if the user spoke. Rendered at request-build time from the recorded
  origin, never baked into the stored prompt text.
- **Clients.** `LLM.messages` exposes the origin; role stays USER at the wire
  (provider constraint) but no SDK or frontend needs to confuse the two ever
  again.
- **Telemetry.** The origin rides the existing message/state record
  vocabulary (engine/telemetryattrs), which is what lets the TUI render
  attribution for agents it merely observes — the "via X" §3.3 promised.

TUI treatment (P3): agent-origin messages render with the sender's name and a
distinct style from the user's own prompts; event messages render more
compactly still (a one-liner, not a prompt bubble). The history placement is
unchanged — it is the STRUCTURE that stops lying, not the record.

### 4.2 Replies are messages: `replyTo` (P6, deadlock shape 1)

`send` gains an optional `replyTo: String` naming a prior message ID. That is
the entire core mechanism; the rest is convention carried by provenance:

- A question is a normal send whose rendered attribution invites a reply and
  carries its message ID.
- An answer is a normal send with `replyTo` set; the recipient's rendered
  form pairs it with the question ("reply from chief to #m7f3").
- `AgentMessage.await` on a message that HAS received a reply resolves with
  that reply rather than waiting for turn end — so external (non-agent)
  callers get ask semantics that no longer conflate the answer with the
  callee's turn-ending reply. (Turn-end resolution remains the fallback for
  messages nothing ever explicitly answers — the CLI prompt's contract is
  unchanged.)

`askChief` becomes non-blocking (§7): it sends and returns immediately with
the message ID and the standing instruction — *keep working, or end your turn
to wait; the answer will arrive as a message*. The chief answers mid-turn
with an ordinary `sendTo(name, answer, replyTo: id)` — which now WORKS,
because the worker is either mid-turn (the reply steers its next step
boundary) or IDLE (the reply wakes it). The wedge in §2 is impossible by
construction: there is no tool-call-blocked state for the reply to miss.

The chief's turn shape is undisturbed: no forced turn end, no answer
addressed to two audiences (P6 resolved). The failure mode this trades into —
a chief that forgets to reply — is visible rather than wedged (the question
sits in its history, attributed) and is mitigated in prompting; a structural
backstop (an unanswered-question ledger on the runtime, shared with item 4's
parked user questions) is sketched in §9.

### 4.3 Lifecycle is messages: subscriptions (P4, P5, item 11)

New runtime verb: `Agent.notify(subscriber: AgentID!, on: [AgentState!] =
[IDLE, FAILED])`. Capability-based like everything else — you must hold BOTH
handles — and argument-shaped per the §3.5 rule (the receiver names the
watched instance; nothing is redefined). On each projected transition into a
subscribed state, the engine enqueues an event-origin message to the
subscriber:

- IDLE: "scout is idle" + the turn's final reply (the payload `collect`
  exists to fetch — truncation knob open, §9);
- FAILED: "scout FAILED" + the loop error;
- WAITING (later, once item 4 makes it reachable): "scout is waiting on you"
  + the parked question.

Delivery rules, two of them load-bearing:

- **Events never relaunch.** `enqueue`'s STOPPED branch reopens the entry
  (core/agent.go:1091) — signal-with-start is right for sends and would be
  resurrection-by-notification here. Events deliver only to live entries and
  are dropped otherwise.
- **Events ride the ordinary mailbox** — provenance-stamped, drained at step
  boundaries, appended to history (they influence, so they append; §3.2
  applies to the engine's own messages too). An IDLE chief is woken by a
  worker's completion exactly as by a user prompt.

This dissolves the P4 combinator question: the chief never waits on N
workers, it keeps working (or goes IDLE) and completions arrive as they
happen, in mailbox order, none missed. `awaitAny`/`awaitAll` stop being
needed for agent loops at all — the residual case (non-model orchestrator
CODE that wants to block on a set) is demoted to §9.

Subscription is `spawn`-time policy in modules (staff subscribes the chief to
every worker it hires), not an engine default: an agent that wants silence
simply doesn't subscribe.

### 4.4 Settled waits for the callers that may block (P5)

For non-agent callers the blocking verbs stay — but `collect`'s hang is a
missing verb, not a policy error. `Agent.waitSettled: ID!` blocks until the
projection reaches ANY settled state (IDLE, FAILED, STOPPED) and returns the
agent for inspection. `waitFor(state:)` remains for the exact-state cases
(tests, mostly). staff's `collect` moves onto `waitSettled` and reports a
FAILED worker's error as an answer instead of hanging on a state that will
never come (§7).

### 4.5 The backstop: waits-for edges and cycle refusal (P1, item 2)

The rule in §3 removes blocking from the shipped tools, but nothing stops the
next module author from building a blocking channel again. The engine gets
the guard async-agents item 2 reserved space for, at the two blocking
primitives (`AwaitMessage`, `WaitFor`/`waitSettled`):

- If `AgentFromContext` yields a caller, register a waits-for edge
  caller → target in a session-scoped table for the duration of the wait.
- A wait whose edge closes a cycle is REFUSED immediately, with the named
  path: `would deadlock: chief → scout (awaiting message #m7f3) → chief`.
  The refusal lands as the tool result of the newest edge — a loud, teachable
  error at the moment of cycle formation, in the conversation of the agent
  that can act on it.
- A self-edge is refused the same way, turning §8's documented self-await
  hazard into an enforcement.
- Non-agent callers register no edge: a human-held await cannot cycle and
  can always be canceled.

Deliberately NOT attempted: starvation detection (a long callee turn under an
acyclic wait). No cheap signal distinguishes it from work; the §3 rule is the
real fix, and the cycle guard is only the floor.

Item 2's remaining half — depth/rate limiting against message storms — stays
open (§9); it is orthogonal to blocking.

## 5. Schema sketch

```graphql
type Agent {
  """
  Enqueue a message. Provenance (sending agent or client) is resolved at
  enqueue and recorded on the consuming turn's history entry. replyTo names
  a prior message this answers; the recipient sees them paired, and awaiters
  of that message resolve with this reply.
  """
  send(message: String!, replyTo: String): ID!
    @expectedType(name: "AgentMessage")

  """
  Subscribe another agent to this agent's lifecycle: each transition into
  one of the given states enqueues an event message to the subscriber.
  Events never relaunch a stopped subscriber. Idempotent per (subscriber,
  states).
  """
  notify(subscriber: AgentID!, on: [AgentState!] = [IDLE, FAILED]): ID!
    @expectedType(name: "Agent")

  """
  Block until the agent settles: IDLE, FAILED, or STOPPED. The safe wait for
  supervisors — never hangs on a failed loop. Refused (with the cycle) when
  called from inside an agent turn whose wait would deadlock.
  """
  waitSettled: ID! @expectedType(name: "Agent")
}

"""Recorded origin of a consumed message; also rendered to the model."""
type AgentMessageOrigin {
  kind: AgentMessageOriginKind!  # USER | AGENT | EVENT
  agentId: String                # sending agent's instance ID, when AGENT
  agentName: String
  messageId: String!
  replyTo: String
}
```

`LLM.messages` entries expose their origin; the telemetry vocabulary
(engine/telemetryattrs) gains the matching attributes on message records.
Everything else — `await`, `waitFor`, delivery evidence, the state
projection — is unchanged.

## 6. What lands where

The split the dogfooding question asked for directly:

| Concern | Layer | Why |
|---|---|---|
| Origin resolution + recording, model-facing attribution | core | the one enqueue path and the honest chain both live there |
| `replyTo` correlation, await-resolves-on-reply | core | message records are runtime state |
| Subscriptions + event enqueue | core | only the runtime sees projected transitions |
| `waitSettled` | core | missing verb on the runtime |
| Waits-for edges + cycle refusal | core | only the engine sees the whole graph; prompts provably don't work |
| Origin in `LLM.messages` + telemetry attrs | core | clients need it without bespoke plumbing |
| Rendering: attribution styles, event one-liners | TUI | presentation of recorded facts |
| Which tools exist, their names and prompts | modules/staff | policy — the engine ships mechanism only |
| Chief subscribes to hires; collect-on-settled; non-blocking askChief | modules/staff | orchestration policy over the new verbs |

The current pain is mostly prompt-documented workarounds for missing core
mechanism (deadlock WARNINGS, "check status before collect"). After this,
staff's prompts describe a working system instead of routing around a broken
one — the reliable tell for the split being right.

## 7. modules/staff, rebuilt on the kernel

- **`ask`: deleted.** The verb is the trap (P1, P6); `sendTo` with an
  expect-reply rendering replaces it. Anything that truly wants blocking ask
  semantics from OUTSIDE an agent still has `send(...).await` + §4.2.
- **`sendTo(name, message, replyTo?)`**: unchanged mechanics, plus reply
  correlation. Resume-first behavior stays.
- **`askChief(question)`**: sends with expect-reply, returns immediately:
  "question #m7f3 delivered; the answer will arrive as a message — keep
  working, or end your turn to wait for it."
- **`collect(name)`**: `waitSettled`, then: IDLE → final reply; FAILED → the
  error plus the retry/dismiss options; STOPPED → says so. The WARNING
  paragraph dies.
- **`spawn`**: after `worker.send(task)`, `worker.notify(subscriber: chief,
  on: [IDLE, FAILED])`. The chief hears every completion and failure with no
  polling and no blocking.
- **Prompts**: rewritten around the event loop — "spawn workers; their
  completions, failures, and questions arrive as attributed messages; answer
  questions with sendTo replyTo; you rarely need to wait, and collect is
  safe when you do." Both DEADLOCK WARNING blocks are deleted, because the
  engine now refuses the cycles they warned about.
- Harvest family, tombstones, pull semantics: untouched.

## 8. Build order and tests

Each step lands alone and pays for itself:

1. **Provenance** (§4.1) + `LLM.messages` origin + TUI attribution. Fixes
   P2/P3 with zero behavioral risk. Test: origin recorded on the chain,
   visible in messages, attribution in the provider request; existing
   replay recordings unaffected (origin renders only for non-user origins).
2. **`waitSettled`** (§4.4) + staff `collect` onto it. Kills P5. Test: FAILED
   worker → collect returns the error promptly.
3. **Cycle refusal** (§4.5). Turns silent deadlock into loud error. Tests:
   mutual-await cycle refused with named path; self-await refused; acyclic
   await from a turn still works; client awaits register nothing.
4. **`replyTo`** (§4.2) + non-blocking `askChief` + chief prompt. Kills the
   wedge and P6. Test: worker asks and goes idle; chief replies mid-turn via
   sendTo; the reply wakes the worker; `await` on the question resolves with
   the reply, not the chief's turn-end text.
5. **Subscriptions** (§4.3) + staff spawn wiring + event rendering. Kills P4.
   Tests: idle event lands in chief history with event origin and wakes an
   IDLE chief; events do not relaunch a STOPPED subscriber; FAILED event
   carries the loop error.

`TestStaff/TestAskChiefAndCollect` (async-agents item 15, skipped): the flow
it records is replaced by step 4's, so it gets re-recorded to the new shape —
but item 15's seed-divergence question (`message history diverges at index
0`) is independent of this design and must be answered on its own before any
re-recording is trusted.

## 9. Open questions

- **Unanswered-question ledger.** A question with expect-reply could register
  on the askee's runtime until a `replyTo` names it — giving `status` and the
  roster an "N unanswered questions" surface, and unifying with item 4's
  parked USER questions (`WAITING_INPUT`), which need the same ledger. Sketch
  only; step 4 works without it.
- **Turn-end auto-reply.** Renounced here (it re-creates P6's conflation),
  but if models reliably forget to `sendTo replyTo`, a fallback that routes
  the consuming turn's final reply to unanswered askers could return.
  Measure first.
- **Event payload size.** IDLE events carry the final reply verbatim; a
  worker ending with a huge report bloats the chief's context. Truncate with
  a "read(name) for the rest" tail, or not at all — decide on observed cost.
- **Message storms.** Depth/rate limiting at enqueue (item 2's other half):
  two agents auto-replying, or a subscription fan-in flooding a chief.
  Nothing in this design creates unbounded loops by itself (events fire on
  transitions, replies need a model's decision), so this stays open rather
  than blocking.
- **Combinators for code.** `awaitAny`/`awaitAll` for non-model orchestrator
  code (a module function supervising N agents imperatively). Demoted by
  §4.3; revisit on demand, per async-agents §7's original instinct.
- **Naming.** `notify` vs `watch` (receiver-shaped); `waitSettled` vs
  `settle`. Bikeshed at implementation.
