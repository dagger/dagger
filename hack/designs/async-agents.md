# Async Agents: the evaluation loop as an addressable entity

Proposal. Today the `LLM` type is used synchronously: `step` and `loop` send
the pending request (if any) and return the LLM with content appended, blocking
the caller until the turn completes. Letting the user interject — or letting
agents talk to each other — means the whole UI and evaluation stack has to
coordinate around those blocking calls.

This design adds an **asynchronous API** for both user-to-agent and
agent-to-agent communication: the agent's evaluation loop is modeled akin to a
service — a long-lived, addressable entity within the session that can be
paused, have messages enqueued into its mailbox, and resumed. The synchronous
API remains, reinterpreted as sugar over the asynchronous one.

## 1. Current state and constraints

What exists today, and what any redesign must respect:

- **`LLM` is an immutable dagql value.** Every `withX` returns a clone;
  `step()` (core/llm.go:1583) materializes its result *through the API
  itself* — it records `withResponse`, `withToolResult`, and in-step mutations
  (workspace overlays, rebound toolsets) as honest selectors, so the resulting
  ID chain (`llm!withPrompt!withResponse!withToolResult…`) is replayable and
  branchable. TUI branch-from-message (`llmCallDigest`, core/llm.go:1612) and
  the portable-recipe machinery (`PortableRecipe`, core/llm.go:2406) both
  depend on this. **The async design must keep every observable state an
  honest chain.**
- **Caching is per-session** (`PerSessionInput` on `Query.llm`,
  core/schema/llm.go:20): identical chains dedupe; `fork(label:)` is the
  sanctioned divergence knob. No hidden nonces.
- **The loop runs in-request.** `Loop` (core/llm.go:1987) is a `for` loop over
  `Step`; on context cancellation it deliberately returns the last recorded
  state instead of failing (core/llm.go:2016-2021) — interrupting preserves
  the completed prefix. Tool calls fully block the step (`MCP.CallBatch`,
  core/mcp.go:1194).
- **Interjection today is a client-side hack.** The shell drives its own
  step loop over GraphQL and drains a single-slot `queuedMsg` between steps
  (internal/cmd/dagger/shell.go:142-174, llm.go:258-279); a second interjection
  silently overwrites the first, and the `alt+up` recall path is documented as
  racy. Module-driven loops (an SDK `Loop()` inside a function) have **no
  interjection hook at all**. The only engine→user channel is the blocking
  prompt attachable (`PromptBool`/`PromptString`,
  engine/session/prompt/prompt.go), whose LLM caller (`LLM.Interject`,
  core/llm.go:2028) is dead code.
- **The services runtime** (core/services.go) already provides the lifecycle
  substrate: request-outliving processes started under detached contexts
  (services.go:1219), one instance per (digest, session) with refcounted
  bindings (`ServiceKey`, services.go:157), graceful stop, attach-into-running
  (`Service.terminal`), and `ExitedService` tombstones that keep crashed
  services observable post-mortem (services.go:180). It lacks: any message
  queue, structured signaling (only SIGTERM/KILL), and cross-session identity.

## 2. Prior art

Surveyed: Google A2A, OpenAI Assistants/Responses, LangGraph, Temporal, the
classic actor model, and — most instructive, as a working system —
[tailcall](https://gitlab.com/sipsma1/tailcall), a personal orchestrator whose
"chief-of-staff" pattern is one long-lived agent orchestrating workers through
generic MCP tools.

```
System      Enqueue primitive              Pause                           Resume
----------  -----------------------------  ------------------------------  -------------------------------
A2A         SendMessage (contextId/taskId) task -> input-required          follow-up message to taskId
OpenAI      thread append (locked mid-run) run -> requires_action          submit_tool_outputs
LangGraph   re-invoke on thread_id         interrupt() checkpoints         Command(resume=v); node re-runs
Temporal    Signal/Update (+ with-start)   durable await in workflow code  handler mutates awaited state
Actors      Pid ! Msg into mailbox         blocking (selective) receive    arrival of matching message
```

Only actors and Temporal have a true *mailbox* — enqueue without waiting for a
pause point. A2A and OpenAI pause only at agent-chosen points; LangGraph's
resume-as-reexecution leaks idempotency constraints into user code. Lessons
adopted:

- **Mailbox + blocking receive is the minimal complete kernel** (actors).
- **Signal-with-start** avoids lost-message races: sending to an unstarted
  agent starts it (Temporal).
- **Reply correlation must follow the message**, not the clock: the answer to
  a message is the final response of *whichever turn consumed it*, including
  a mid-turn absorption (tailcall: an `inputId` "follows the input to
  whichever execution consumes it, including an absorbed steer").
- **Steering and interrupting are distinct verbs** (tailcall keeps
  `started|steered|queued` delivery evidence strictly separate from hard
  interrupt).
- **Liveness state is computed, never stored** (tailcall §4.9: `running` is a
  projection; storing it is how systems lie after crashes).
- **Per-agent tool binding fails; orchestrate via generic tools.** Tailcall
  tried binding each agent's tools into peers and deleted it, replacing it
  with `create_agent`/`send_agent`/`ask_agent`/`await_agent` addressing agents
  by ID.
- **Don't block on the human.** Tailcall blocks agent-on-agent freely but
  deliberately suppresses Claude's native ask-the-user tool, rendering
  questions as parked, durable approval cards instead — the human is not a
  service.

## 3. The Agent model

**There is always an agent.** Every evaluation loop *is* an agent; the
synchronous API just never looks at the handle. `llm.loop` becomes sugar for
(approximately) `llm.asAgent.start.waitFor(IDLE).snapshot`. This closes the
worst coordination gap for free: module-driven loops stop being opaque,
because any running evaluation is an addressable agent.

An `Agent` is a content-addressed dagql **value** (its seed conversation plus
a name), exactly as `Service` is a value: starting it registers a runtime
entry — mailbox, loop goroutine on a detached context, computed state — in a
runtime table keyed by (value digest, session), one running instance per key,
following the `Services` registry model (services.go:157, :1219). `start` is
`DoNotCache` and idempotent; `send` to an unstarted agent starts it
(signal-with-start).

The loop itself is today's `Loop`, with one change: **at each step boundary it
drains the mailbox**, recording every drained message as `withPrompt`
selectors (with provenance — see §3.3) before continuing; when the turn ends
and the mailbox is empty, instead of returning it **blocks in receive**
(state: `IDLE`). Pausing, inspecting, and resuming are operations on the
runtime entry, never on the value.

### 3.1 Schema

```graphql
"""
A conversation loop running as an addressable, long-lived entity within the
session. Messages enqueue into its mailbox and are drained at step
boundaries; the conversation itself remains observable at any time as an
immutable LLM value.
"""
type Agent implements Node {
  id: ID!

  """Display label and identity discriminator — not a session-wide address."""
  name: String!

  """Computed lifecycle state; never stored."""
  state: AgentState!

  """What the agent is blocked on, when state is WAITING_INPUT."""
  waitingOn: String

  """
  The conversation as of the last committed step: immutable, branchable,
  persistable. Branching from it does not affect the agent — for
  on-the-record interaction, use send.
  """
  snapshot: LLM!

  """Start the loop. No-op if running; send starts implicitly."""
  start: ID! @expectedType(name: "Agent")

  """
  Enqueue a message, on the record: it is consumed at a step boundary,
  appends to the history with provenance, and steers the running turn or
  opens a new one. Never blocks, never drops; concurrent sends queue in
  order.
  """
  send(message: String!): AgentMessage!

  """
  Preempt the in-flight step, keeping all completed steps, and pause. To
  redirect, follow with send: steering and interrupting are separate verbs.
  """
  interrupt: ID! @expectedType(name: "Agent")

  """
  Stop draining the mailbox once the in-flight step completes. Messages
  sent while paused enqueue without being processed until resume.
  """
  pause: ID! @expectedType(name: "Agent")

  """Resume draining the mailbox."""
  resume: ID! @expectedType(name: "Agent")

  """Block until the agent reaches the given state."""
  waitFor(state: AgentState = IDLE): ID! @expectedType(name: "Agent")

  """
  Release the runtime. The tombstone (state, snapshot) stays readable for
  the rest of the session; the trace persists for resume (§6).
  """
  stop(kill: Boolean = false): ID! @expectedType(name: "Agent")
}

"""A message delivered to an agent's mailbox."""
type AgentMessage implements Node {
  id: ID!

  """
  How the message landed: opened a new turn, was absorbed into the running
  turn, or is queued behind it.
  """
  delivery: AgentMessageDelivery!

  """
  Block until the turn that consumed this message ends, and return that
  turn's reply. Idempotent: cancel and re-await freely; concurrent waiters
  share the result.
  """
  await: String!
}

enum AgentState {
  """Mailbox empty, turn complete; blocked in receive."""
  IDLE
  """A model request or tool evaluation is in flight."""
  RUNNING
  """Blocked on input from the user (derived; see waitingOn)."""
  WAITING_INPUT
  """Mailbox accepting but not draining, until resume."""
  PAUSED
  """Runtime released; snapshot remains readable."""
  STOPPED
  """The loop failed; snapshot holds the completed prefix. resume retries."""
  FAILED
}

enum AgentMessageDelivery { STARTED STEERED QUEUED }
```

Constructors and injection:

```graphql
type LLM {
  """
  Package the conversation as an agent: a startable, addressable evaluation
  loop seeded with this conversation's state, tools, and workspace.
  """
  asAgent(name: String): Agent!
}
```

- `LLM.loop` / `LLM.step` become sugar over an implicit (anonymous) agent.
- Tool functions may declare an **`Agent!` argument** to receive a handle to
  the calling loop — the async sibling of `LLM!` injection (which hands a
  tool the conversation *value* for continuation-style handoff, the `Agent!`
  form hands it a live *channel*). This is the child→parent channel: a
  spawned worker holds its spawner's handle and can `parent.send(...).await`.
  Tailcall's equivalent is the per-agent MCP token identity.

There is deliberately **no `ask` field**: `ask(m)` ≡ `send(m).await`, and the
handle form is strictly more expressive (fire-and-forget, deferred await,
shared awaiting). The off-the-record variant needs no verb at all — see §3.2.

### 3.2 On the record vs. off the record: influence ⇔ append

Two ways to put a question to an agent, with opposite semantics, both
first-class:

- **Off the record**: `agent.snapshot.withPrompt(q).loop.lastReply` — a pure
  branch of the value. Cached, parallel-safe, and *invisible to the agent*.
  This is the existing LLM-as-value model doing its job; no new API.
- **On the record**: `agent.send(q)` — enters the mailbox, is consumed by a
  turn, and **appends to the agent's history**.

The rule is forced by the honest-chain constraint, not preference: if a
message *influenced* the agent (the model computed subsequent replies with it
in context), a history omitting it is a lying transcript — replay and
persisted recipes would diverge from what actually happened. Influence ⇔
append. Consultation-without-influence is exactly what snapshot branching is.

Consequently a sub-agent's question to its parent **does** enter the parent's
history (with provenance), and tailcall independently converged on the same
design: a delivered ask *is* a user message in the askee's conversation,
source-agnostic, with per-turn provenance stamped alongside.

Reply correlation rides the handle: `AgentMessage.await` returns the final
reply of *the turn that consumed the message* — under multiple senders,
`send + waitFor(IDLE) + snapshot.lastReply` is racy (idle may follow a turn
that consumed someone else's message), which is why the handle exists. There
are no timeout arguments anywhere: callers cancel the GraphQL request, and
because `await` is idempotent against the runtime entry, cancel-and-re-await
loses nothing. (Tailcall needed 30-minute caps and keepalives precisely
because its MCP clients could not cheaply cancel-and-reattach; session-scoped
GraphQL requests can.)

### 3.3 Addressing is capability-based; telemetry is the directory

There is **no session-wide agent namespace** — no `Query.agent(name)`, no
`Query.agents`. To message an agent you must hold its ID. `name` is a display
label and identity discriminator (two otherwise-identical agents — the
`fork(label:)` role), not an address.

Orchestrators hold agents as **bound-object state**, the pattern
`modules/editor` proves (a `todos` field plus `todoWrite(...): Editor!`
rebinding mutated self; `step()` persists each rebind as a `.withTools`
selector). A team module holds `members: [Agent!]` and resolves its own
member names — module-local, collision-free:

```dang
type Team {
  let members: [Agent!]! = []

  spawn(name: String!, task: String!, source: Workspace!): Team! {
    let worker = source.agents.compose
      .withSystemPrompt(workerPrompt)
      .asAgent(name)
    worker.send(task)
    self.members += [worker]
    self
  }

  askWorker(name: String!, message: String!): String! {
    members.find { m => m.name == name }.send(message).await
  }

  collect(name: String!): String! {
    members.find { m => m.name == name }.waitFor(IDLE).snapshot.lastReply
  }
}
```

This is also tailcall's post-mortem conclusion: per-agent tool binding into
peers was tried and deleted; generic spawn/send/ask/await tools addressing
held handles won. ID-as-capability additionally gives, for free, the access
scoping tailcall built a whole token-identity system for: a module hands out
an agent handle deliberately, or not at all.

What a namespace *would* have provided — the user reaching an agent they
didn't spawn, e.g. one born deep inside a module call — moves to telemetry:
**agent spans carry the agent's ID as an attribute**, so the TUI can offer
"send to this agent" for any agent it renders, including module-internal
loops. This generalizes the existing branch-from-`llmCallDigest` machinery
(core/llm.go:1612, frontend_pretty.go BranchFromID): telemetry is already the
discovery plane; the carried IDs become actionable. The same mechanism serves
cross-client addressing within a session.

All delivery flows through one central enqueue path on the runtime entry —
the place for the guards tailcall enforces centrally: depth limiting,
self-send rejection, cycle detection. Provenance (sending client or calling
agent) is stamped on the recorded `withPrompt` selector and surfaced in
telemetry ("via X"), keeping multi-party transcripts legible.

### 3.4 State is computed; WAITING_INPUT

`AgentState` is always a projection of the runtime entry — never stored
(tailcall's hard-won rule). In particular `WAITING_INPUT` derives from "the
in-flight tool call is an ask targeting the user," with `waitingOn` carrying
the question.

Why a distinguished state at all, when asks could just block? For
agent-on-agent they do — and the mailbox makes that safe: an agent blocked in
an `ask` tool call is still *receiving*, so asks landing meanwhile queue or
steer rather than deadlock, and ask-cycles degrade to steerable waits rather
than wedged threads. The human is the one party that cannot be modeled as a
blocking callee: no availability contract, possibly not connected. A blocked
prompt-RPC is (a) unrenderable — indistinguishable from "model is thinking";
(b) addressed to one client fixed at call time — nobody else (another
attached client, a supervising agent triaging its workers) can answer; and
(c) goroutine state — lost on disconnect. A parked question is data:
renderable by any frontend without bespoke coordination, answerable by
whoever is authorized via ordinary `send`, and durable across absence.
Tailcall is the empirical witness: it blocks agent-on-agent freely and still
suppresses the native ask-the-user tool in favor of parked approval cards.

So: blocking calls are the *verb*; `WAITING_INPUT` is the *view*.

### 3.5 Lifecycle details

- **`interrupt`** promotes `Loop`'s existing cancellation semantics
  (core/llm.go:2016-2021: keep the completed prefix) to a verb, and takes no
  message: in this model `send` to a running agent *is* steering (consumed at
  the next step boundary, with `STEERED` delivery evidence), and tailcall's
  experience says never conflate the two.
- **`stop`** releases the runtime and leaves a tombstone — state and final
  snapshot readable for the rest of the session — following `ExitedService`
  (services.go:180). (That precedent arguably wants generalizing anyway,
  e.g. querying logs of a crashed service after the fact.)
- **`FAILED`** holds the completed prefix in `snapshot`; `resume` re-enters
  the loop from it — supervision-lite.
- **Dedupe**: two evaluations of the same `asAgent` chain in one session
  resolve to the same value digest and thus the same running instance, like
  services; distinct `name`s are the way to run identical twins.

## 4. Persistence: resume from telemetry

No checkpointing machinery. Every API call span already carries the **full
protobuf-encoded Call** (`DagCallAttr`, core/telemetry.go:92-104), literals
included, and `dagui.DB` reconstructs the whole DAG from those payloads —
and because `step()` records everything (responses, tool results, in-step
workspace/toolset mutations) as honest selectors, **the trace contains the
complete, re-Selectable conversation chain, bindings and all**. Traces stream
to and persist in Cloud.

Resume = locate the turn-tip call digest (already findable via
`llmCallDigest` attributes), decode its Call payload, re-Select it, then
`asAgent.start`. Notes:

- Span emission dedupes by call digest per session (`ShouldEmitTelemetry`,
  core/telemetry.go:80-84), so reconstruction must join call payloads across
  the full trace rather than expecting them all under the resume tip.
- **The trace carries recipes, not snapshots.** `withResponse`/
  `withToolResult` are data selectors — resuming replays the conversation
  without re-calling models or re-running tools — but leaves like
  `host.directory(...)`, git refs that have moved, or session resources
  (secrets, sockets) re-*evaluate*, so a cold engine reconstructs faithfully
  only to the extent the leaves still resolve. This is `portableID`'s
  existing contract, unchanged by this design.

Within a session, nothing is needed at all: stopped agents are tombstones in
the runtime table.

## 5. UI and shell impact

The shell's client-driven step loop, its single-slot `queuedMsg`, and the
racy `alt+up` recall all collapse: the prompt line becomes `agent.send`, and
the frontend becomes a pure observer — spans for progress (unchanged),
`state`/`waitingOn` for the ball-in-your-court moment, `AgentMessage.await`
when it wants a turn's reply. Ctrl-C maps to `interrupt` (prefix-preserving)
instead of cancelling the turn context and rolling back client-side state.
`dagger agent` starts a real Agent and attaches to it; a second terminal (or
Cloud) attaching to the same session addresses the same agent via its
telemetry-carried ID.

## 6. Naming: freeing `Agent`

`Agent` and `AgentGroup` are currently the *descriptor* types for `@agent`
middleware functions (core/agents.go; schema.graphqls:99,:116) — name/module
metadata plus the `compose` fold. Rename them **`AgentMiddleware`** and
**`AgentMiddlewareGroup`** (matching the vocabulary of
hack/designs/workspace-agents.md), freeing `Agent` for the runtime entity.
Everything else keeps its name, and converges rather than collides:

| Surface | Fate |
|---|---|
| `type Agent` / `AgentGroup` | renamed to `AgentMiddleware` / `AgentMiddlewareGroup` |
| `Workspace.agents` field | stays (returns the renamed group) |
| `@agent` annotation, `Function.withAgent`, `IsAgent` | stay — "this function defines an agent" still reads right |
| `dagger agent` CLI | stays, and now starts an actual `Agent` |

Middlewares *define* agents; `compose(...).asAgent(name)` *instantiates* one.

## 7. Open questions

- **`awaitAny` / `awaitAll`** over message handles or agents: without a
  combinator, a chief-of-staff monitor loop degenerates to polling `state`.
  The handle type makes combinators possible; whether they land in core or
  wait for demand is open (tailcall's `await_agents` suggests demand is
  real).
- **Windowed reads**: tailcall's most load-bearing chief tool is
  `read_agent` — a bounded, final-responses-only view of a target's
  conversation. Expressible today as a module-side projection over
  `snapshot.messages`; a core convenience may or may not be warranted.
- **Mid-step delivery**: the mailbox drains at step boundaries. Provider
  turn invariants (tool_result-follows-tool_use) make finer granularity
  awkward; `interrupt` + `send` covers "stop now, do this instead." Revisit
  if step-boundary latency proves painful for long tool batches.
- **Loop/step as sugar**: whether to literally reimplement `LLM.loop` over
  the agent runtime immediately, or keep two code paths during transition.
- **Adoption hazard**: object-tool routing adopts returned `LLM`s as
  continuations (`routeObjectMethodResult`), so a tool that returns
  `agent.snapshot` would graft another agent's conversation onto the
  caller's. Orchestration modules should return projections (strings) rather
  than raw `LLM`s; whether the engine should also guard this centrally is
  open.

## 8. Alternatives considered

- **Task ledger (A2A-style)**: no resident runtime; immutable `AgentTask`
  objects under a shared context, `INPUT_REQUIRED` as a stored state.
  Cleaner durability, but a parallel lifecycle subsystem next to Services,
  no live mailbox (steering only at agent-chosen pause points), and weaker
  fit for "model it akin to a service."
- **Sovereign agent container**: the loop runs in a container-as-Service
  speaking A2A/MCP over a health-checked port. Maximal service-layer reuse
  and external interop, but the loop leaves the engine — honest chains and
  cache intimacy must round-trip a nested session — and a container per
  agent is heavy for "let me interject." Remains the natural *interop* layer
  to build atop this design later.
- **Bare mailbox primitive**: a `Mailbox` type plus `llm.withMailbox` —  the
  minimal kernel, but it leaves the loop occupying a request, offers no
  state machine for UIs, and punts the actual pause/enqueue/resume ask; it
  is subsumed by the agent runtime entry.
