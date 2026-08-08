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
(approximately) spawning an anonymous agent, starting it, waiting for
`IDLE`, and reading the snapshot. This closes the worst coordination gap for
free: module-driven loops stop being opaque, because any running evaluation
is an addressable agent.

An `Agent` is a **spawned instance**: `llm.spawn` mints a unique instance ID
per call and pins it into the returned handle's chain via the pure
`agent(id:)` lookup — Agent joins `AgentMessage` in the minted-and-pinned
identity family (§9). The value itself stays a pure, content-addressed dagql
value (seed conversation, minted instance ID, display name), and starting it
registers a runtime entry — mailbox, loop goroutine on a detached context,
computed state — in a session-scoped runtime table keyed by the value's
digest. Because the digest contains the minted ID, every spawn gets a fresh
entry: two spawns of one composition are two agents, and a stopped
instance's slot can never be resolved to by a later spawn. `start` is
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

  """Display label — telemetry and error messages; carries no identity."""
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
  send(message: String!): ID! @expectedType(name: "AgentMessage")

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
  Spawn the conversation as an agent: a startable, addressable evaluation
  loop seeded with this conversation's state, tools, and workspace. Every
  spawn mints a unique agent instance; the returned ID is pinned to it.
  """
  spawn(name: String): ID! @expectedType(name: "Agent")

  """
  Rehydrate a spawned agent's handle from its instance ID: the pure lookup
  spawn pins instance identity through. Never creates an instance.
  """
  agent(id: String!, name: String!): Agent!
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
label only — not an address, and not an identity: uniqueness is minted by
`spawn`, so two spawns under one name are simply two agents sharing a label
(agents need no `fork(label:)`-style discriminator).

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
      .spawn(name)
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
  snapshot readable for the rest of the session, in the spirit of
  `ExitedService` (services.go:180). One deliberate divergence: Services
  *free* a running entry's registry key on exit (`delete(ss.running, key)`,
  services.go:1116–1137; tombstones go to a capped side list) precisely
  because their keys are reusable composition digests. An agent's key
  contains its spawn-minted instance ID — born unique, never reusable — so
  the tombstone keeps the keyed slot harmlessly forever, and terminal stop
  is the honest semantics of an instance (nobody asks to restart a k8s
  UID).
- **`FAILED`** holds the completed prefix in `snapshot`; `resume` re-enters
  the loop from it — supervision-lite.
- **Instances, not dedupe**: `LLM` *values* dedupe — identical chains are
  one cached conversation, with `fork(label:)` as the divergence knob — but
  agents are spawned *instances*: `spawn` is `DoNotCache` and mints fresh
  identity per call, so two evaluations of one composition are two runtimes.
  The one load-bearing dedupe survives above the spawn: identical `llm.loop`
  chains still cache-hit at loop's own call ID, so a second evaluation never
  reaches the inner spawn.

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
`spawn` + `start`. Notes:

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
`dagger agent` spawns a real Agent and attaches to it; a second terminal (or
Cloud) attaching to the same session addresses the same agent **by held ID**
— its telemetry-carried ID, never by re-deriving the composition, which
would simply spawn a fresh agent (attach-by-rederivation is renounced; §9).

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

Middlewares *define* agents; `compose(...).spawn(name)` *instantiates* one.

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

## 9. As-built ratifications

Semantics settled during implementation (core/agent.go, core/schema/agent.go,
core/integration/agent_runtime_test.go), ratified here:

- **State projection order.** `state` is a pure ordering of runtime facts:
  done-with-error and not sealed → `FAILED`; done → `STOPPED`; stepping →
  `RUNNING` (a pause/interrupt requested mid-step shows `RUNNING` until the
  step lands); paused → `PAUSED` (even with a suspended turn or non-empty
  mailbox); turn-open or mail pending → `RUNNING`; else `IDLE`. The
  turn-open-or-mail-pending clause means `send + waitFor(IDLE)` can never
  observe a lying `IDLE` in the enqueue→drain window.
- **Pause freezes the turn frontier.** Turn-end resolution re-checks the
  mailbox before resolving, so a message racing in during the final step
  joins that turn — keeping its `STEERED` evidence truthful. Under pause the
  mailbox must not drain, so resolution is deferred too: a turn that finishes
  its last step just as pause lands stays open, and awaiters get the reply on
  resume. `PAUSED` is a full freeze of the observable frontier.
- **Delivery evidence is computed at enqueue**, serialized (under the entry
  mutex) with the turn-end path, which is what makes it truthful: idle/new
  turn → `STARTED`; turn open or stepping → `STEERED` (the boundary drain
  guarantees absorption); paused → `QUEUED`.
- **`send` to `FAILED` enqueues (`QUEUED`)** rather than erroring — send's
  one promise is never to drop; a later `resume` drains it, and awaiting such
  a message meanwhile projects the failure error without resolving the
  record. `send` to `STOPPED` errors.
- **`stop` on a `FAILED` tombstone seals it**: once stop forecloses the
  retry, queued mail is settled with stop errors in the same transition that
  flips the projection to `STOPPED` — no awaiter ever observes `STOPPED`
  with mail apparently in flight. The loop error is preserved as a fact; the
  seal overrides it in the projection.
- **Interrupt is a per-step context cancel with a sentinel cause**,
  distinguishing it from stop/session-teardown cancellation: interrupted →
  commit the returned prefix, keep the turn and its consumed messages
  pending, park as `PAUSED`; loop-context cancel → `STOPPED`; anything else
  → `FAILED`. Resume after an interrupt continues the suspended turn (the
  pending input is still on the snapshot), so awaits resolve normally.
- **Message identity by re-exec pinning.** `send` is `DoNotCache`, and
  detached results have no addressable ID — which would break the
  cancel-and-re-await contract across requests. The fix follows the schema's
  established pattern for delicate runtime identity (the same trick `step()`
  uses to materialize state as selectors): after enqueueing, `send` re-execs
  through a lookup field — `Agent.message(id: String!): AgentMessage!` —
  via a real Select, so the returned handle's ID is an honest, replayable
  chain (`…agent(id:…)!message(id:"…")`) pinned to the generated message ID
  and addressable from any request in the session.
- **Agent identity is minted at spawn and pinned by re-exec.** Ratified
  after live QA surfaced the failure the original model guaranteed: with
  identity derived from the composition chain (name included), a stopped
  agent's tombstone occupied the registry slot every identical
  re-derivation resolved to — so dismiss-and-rehire against an unchanged
  workspace addressed the predecessor's corpse (the task rides `send`, not
  the chain, making this the common case, not an edge). Prior art is
  unanimous — Temporal workflowID/runID + reuse policies, Erlang name/Pid,
  Akka path/ref-with-UID ("a new incarnation … is not the same actor"),
  k8s name/UID + generateName — a reusable name is never the instance ID,
  and uniqueness is minted where instances are born, never by callers. So
  the pure constructor `asAgent(name)` became the effectful verb
  `spawn(name)`: it mints the instance ID in the resolver and pins it
  through the pure `LLM.agent(id:, name:)` lookup — extending the
  message-identity pattern (previous bullet) one level up — and, being
  imperative, returns `ID!` with `@expectedType` like every other verb.
  The registry is untouched: keys are still content digests, they just
  never collide now, so send-to-STOPPED-errors, seal ordering,
  signal-with-start, resume-retries-FAILED, message pinning, tombstone
  readability (now per-instance), and no-namespace all hold verbatim.
  Renounced with it: attach-by-rederivation — two evaluations of the same
  composition are two agents, and observing a running agent requires
  holding its ID (§3.3's addressing model, now strengthened: IDs are
  unforgeable, since composition knowledge alone no longer derives a live
  handle). Dissolved with it: the entire reuse-policy question — terminal
  stop is the honest end of an instance, and name reuse is trivially safe.
- **Imperative verbs are ID-returning, sync-style.** `spawn`, `start`,
  `send`, `interrupt`, `pause`, `resume`, `waitFor`, and `stop` return
  `ID!` with `@expectedType`, exactly like `Service.start`/`stop` and the
  `sync` fields. Lazy clients (Dang) force scalar-returning fields at the
  call site and re-hydrate the ID into an object via the annotation, so the
  side effect executes exactly once — eliminating the duplicate-send
  hazard of re-forcing a lazy `DoNotCache` chain. Reads stay
  object-returning: `agent(id:)` and `message(id:)` (pure lookups), and
  `snapshot`. For `spawn` and `send` the returned ID is the pinned lookup
  chain (previous bullets), so re-hydrating it replays the lookup, not the
  mint/enqueue.
- **Self-await hazard.** A tool holding its own calling agent's handle (via
  `Agent!` injection) can `send` to it — the message joins the in-flight
  turn as `STEERED` — but awaiting it from inside that same turn's tool call
  is a deadlock (the turn cannot end until the tool returns). Fire-and-forget
  sends to self are legal steering; awaits belong on *other* agents.

## 10. Alternatives considered


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

## 11. Implementation status

What is BUILT (see also §9 for ratified semantics):

- **Core runtime**: `core/agent.go` (Agent value with spawn-minted
  `InstanceID`, `AgentRuntimes` session registry keyed by content digest —
  collision-free by construction, loop with mailbox drained at step
  boundaries, tombstones), `core/schema/agent.go` (fields: `name`, `state`,
  `snapshot`, `start`, `send`, `message`, `waitFor`, `pause`, `resume`,
  `interrupt`, `stop`; `AgentMessage.{delivery,await}`; `AgentState`,
  `AgentMessageDelivery`). Registry wiring in `engine/server/session.go`
  alongside `Services`.
- **Spawned instance identity**: `LLM.spawn(name)` mints a unique instance
  per call and pins it through the pure `LLM.agent(id:, name:)` lookup
  (§9), in `core/schema/llm.go`; name is display-only. `asAgent` is gone.
- **Message identity**: re-exec pinning via `Agent.message(id:)` (§9) —
  handles are honest chains, cancel-and-re-await works across requests.
- **ID-returning verbs**: the imperative fields (`spawn`, `start`, `send`,
  `interrupt`, `pause`, `resume`, `waitFor`, `stop`) return `ID!` with
  `@expectedType`, `Service.start`-style (§9); reads (`agent(id:)`,
  `snapshot`, `message(id:)`) stay object-returning. Typed SDKs
  re-hydrate self-returning verbs natively; `spawn`'s agent ID and
  `send`'s message ID re-hydrate via `node(id:)` (`dagger.Ref` in the Go
  SDK).
- **`Agent!` injection**: `core/agent_context.go` + `core/modfunc.go`
  (`FunctionArg.IsAgentHandle`, distinct from the middleware `IsAgent`
  flag); hidden from tool schemas via `core/llm_object_tools.go`. Works
  from agent loops; a sync `loop` errors ("synchronous loop support is
  planned").
- **Namespace**: descriptor types renamed `AgentMiddleware` /
  `AgentMiddlewareGroup` (§6).
- **CLI prompt mode** (`internal/cmd/dagger/llm.go`, `shell.go`,
  `dagql/idtui/frontend_pretty.go`): submit = send + resume + await,
  re-rooting on `snapshot` at turn end; interjections send immediately
  (STEERED); Ctrl-C → `interrupt` (PAUSED, prefix kept), next submit
  resumes; wholesale LLM replacement stops the stale runtime and the next
  submit spawns afresh — instance uniqueness comes from `spawn` itself,
  so the session's old entropy naming is gone.
- **Async orchestration module** (§3.3 Team sketch, realized):
  `modules/staff` — spawn/sendTo/ask/status/read/collect/interruptWorker/
  dismiss over module-held `[Agent!]` state (the `modules/editor`
  pattern), with each worker given an `askChief` line home whose answers
  ride the chief's own record. Deliberately NOT registered in the repo
  dagger.toml (load with `dagger -m ./modules/staff`). Side-effecting and
  live-reading tools carry `@cache(policy: FunctionCachePolicy.Never)` —
  load-bearing: dagql otherwise replays identical-arg calls, so a
  zero-arg `status` could never observe a state transition. Windowed
  reads (`read_agent`-style) are the module-side `read` projection over
  `snapshot.messages`, as predicted — no core work needed.
- **Tests**: `core/integration/agent_runtime_test.go` +
  `agent_injection_test.go` (fixture
  `testdata/modules/go/agent-poker`) + `staff_test.go` (E2E over the
  served `modules/staff`: spawn → askChief steering into the chief's open
  turn → collect), all against the keyless `replay/` provider, including
  genuinely mid-turn STEERED and mid-step interrupt via a slow-tool
  recording synchronized on a cache-volume marker. Spawn semantics are
  locked in by `TestSpawnInstances` (two spawns of an identical
  composition are distinct, concurrently running agents) and
  `TestSpawnAfterStop` (dismiss-and-rehire works; the predecessor's
  tombstone stays readable by held ID).

What is NOT built — threads to pull, each self-contained:

1. **Telemetry directory** (§3.3, §5): the loop span carries no agent-ID
   attribute yet, so the TUI cannot offer "send to this agent" for agents
   it renders; generalize the `llmCallDigest` branch-from-message
   machinery. Start at the loop span in `core/agent.go` and
   `dagql/dagui/spans.go`.
2. **Enqueue guards** (§3.3): depth limiting, self-send rejection, cycle
   detection — none exist. Central point: the enqueue path in
   `AgentRuntimes` (`Send`). Until then `modules/staff` documents the
   ask/askChief deadlock and steers the chief around it by prompt.
3. **Provenance stamping** (§3.3): drained messages record plain
   `withPrompt` selectors with no sender identity; no "via X" in history
   or telemetry.
4. **`WAITING_INPUT` / `waitingOn`** (§3.4): enum value exists but is
   unreachable; needs the user-ask parking path (the non-modal
   resurrection of the dead `LLM.Interject`).
5. **Loop-as-sugar** (§7): `LLM.loop`/`step` remain an independent code
   path; consequently sync loops cannot satisfy `Agent!` args. Note the
   spawn pivot constrains the eventual sugar pleasantly: `loop` stays a
   cached pure field whose resolver would spawn internally, and identical
   `llm.loop` chains still dedupe at loop's own call ID — the second
   evaluation cache-hits `loop` and never reaches the inner spawn.
6. **`awaitAny`/`awaitAll`** (§7): absent; orchestrators poll
   `state`/`waitFor` per agent (staff's `collect` is a per-worker
   `waitFor(IDLE)`).
7. **CLI follow-ups**: re-enabling undo/fork in prompt mode (the
   "interrupts lose progress" rationale is retired — server-side
   interrupt is prefix-preserving); `startInteractivePromptMode`
   pre-initializes a default LLM that demands provider config even when
   the entrypoint supplies its own (pre-existing wart); confirm the
   `TestGolden` TUI snapshots in CI (they replay non-prompt traces and
   should be unaffected by the prompt-flow rewire).
8. **Module-call cache staleness** (open investigation): in one live QA
   session, after a second module reload, identical-arg staff calls
   (`status`, `read`) replayed stale results DESPITE their
   `@cache(Never)` annotations — which had verifiably worked right after
   the first reload. Fresh arg-tuples always read live. Suspects: the
   module-function cache policy path (`core/modfunc.go`,
   `derivedCachePolicy`) or reload/re-serve interplay with cached
   function metadata. Until root-caused, treat repeated same-arg module
   reads in long reload-heavy sessions with suspicion.

