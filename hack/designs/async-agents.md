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

```text
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
identity family (§8). The value itself stays a pure, content-addressed dagql
value (seed conversation, minted instance ID, display name), and starting it
registers a runtime entry — mailbox, loop goroutine on a detached context,
computed state — in a session-scoped runtime table **keyed by that minted
instance ID**. Every spawn therefore gets a fresh entry: two spawns of one
composition are two agents, and a stopped instance's slot can never be
resolved to by a later spawn. The key is the ID and nothing else, which is
what lets a handle rebuilt from telemetry address the live entry even though
re-executing its chain re-derives the seed (§10.2). `start` is `DoNotCache`
and idempotent; `send` to an unstarted agent starts it (signal-with-start).

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

  """
  Replace this instance's committed conversation, keeping the entry:
  identity, mailbox, and lifecycle state are all untouched. The continuity
  verb — compaction, a workspace rebind, or a model change produce a new
  conversation for the SAME agent. Refused while a turn is in flight or
  suspended, and on STOPPED.
  """
  reseed(conversation: LLMID!): ID! @expectedType(name: "Agent")
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

**Correction, from dogfooding (see hack/designs/agent-messaging.md).** The
claim above that agent-on-agent asks block safely — "asks landing meanwhile
queue or steer rather than deadlock, and ask-cycles degrade to steerable
waits" — conflated receiving with progressing. A blocked agent still
ENQUEUES fine (the STEERED evidence is honest), but consumption happens only
at step boundaries and reply resolution only at turn end, and a turn blocked
inside an ask tool call reaches neither — so ask-cycles wedge hard
(ask↔askChief, collect↔askChief), and even the non-cycle case breaks: a
reply delivered as a *message* cannot unblock a waiter blocked in a *tool
call*, so answering an askChief via sendTo strands the asker until the
answerer's turn ends. The parked-question argument this section makes for
the human turns out to apply to agents too; agent-messaging.md carries the
redesign (turns are the unit of waiting) and this section's human-side
conclusion stands unchanged.

### 3.5 Lifecycle details

- **`interrupt`** promotes `Loop`'s existing cancellation semantics
  (core/llm.go:2016-2021: keep the completed prefix) to a verb, and takes no
  message: in this model `send` to a running agent *is* steering (consumed at
  the next step boundary, with `STEERED` delivery evidence), and tailcall's
  experience says never conflate the two.
- **`stop`** winds down the loop and leaves a dormant tombstone — state and
  final snapshot remain readable, in the spirit of `ExitedService`
  (services.go:180), and a later `send` or `resume` relaunches the SAME
  instance from that snapshot. Services *free* a running entry's registry key
  on exit (`delete(ss.running, key)`, services.go:1116–1137; tombstones go to a
  capped side list) precisely because their keys are reusable composition
  digests. An agent's key IS its spawn-minted instance ID — born unique, never
  reusable — so keeping the keyed slot is what lets a stopped agent retain its
  history and identity across relaunch.
- **`FAILED`** holds the completed prefix in `snapshot`; `resume` re-enters
  the loop from it — supervision-lite.
- **`reseed`** replaces the committed conversation and changes nothing
  else: queued mail drains onto the new conversation (a message is
  addressed to the agent, not to a history), a FAILED tombstone keeps its
  error (reseed and resume compose), and the swap is refused while the
  loop is unparked — a step in flight would silently overwrite it — on a
  suspended turn, and on STOPPED. It is the client-facing form of the
  continuation adoption a tool performs mid-turn (`MCP.adoptLLM`): the
  agent adopts a new conversation without changing who it is. `snapshot`
  reads the committed conversation; reseed is its deliberate write-side
  inverse. This is what lets a client's wholesale LLM replacements
  (compaction, workspace export/reset, model change) keep one agent one
  roster entry instead of minting a STOPPED successor per replacement.
  The shape encodes a rule worth stating: RUNTIME verbs are
  argument-shaped — the receiver names an instance and never redefines
  one, the §10.2 invariant that makes telemetry-rebuilt handles safe to
  hold — while CREATION verbs (`spawn`, `rehydrate`) read the receiver as
  the seed, because creation is the one moment a chain defines an entry.
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

### 4.1 Resume from trace — the next thread

**This is the next piece of work.** Everything above describes resume as a
capability the trace already affords; what exists in practice is the local
save file, and a live QA session (item 13) demonstrated that route failing in
both directions at once — re-animating dismissed workers *and* leaving them
unaddressable. The two failures share a cause: the durable artifact is a
recipe of recorded calls held on one machine, when it should be the trace.

What is already proven, within a session: a client reconstructs a *sendable*
handle from a carried call digest, and the rebuilt handle observes the LIVE
runtime — `TestRosterAddressing`, and `TestRosterAddressingFromModule` for an
agent spawned inside a module call (§8). The mechanism works. What it lacks
is durability across client processes: a restarted client builds a fresh
`dagui.DB`, so every call payload emitted before the restart is simply
absent, `ID.decode` fails with `call digest not found`, and the roster
degrades every pre-restart agent to watch-only. Cloud already receives and
persists the traces (§4), so the store exists; nothing reads a roster back
out of it.

The work, in the order the constraints force:

- **Read the directory from the persisted trace**, not only from the
  in-process DB. This is the part that needs no new schema — §8's
  renunciation of `Query.agents` holds, because the roster stays a projection
  of the trace; the trace just stops being per-process.
- **Resume must not re-execute recorded imperative verbs.** Item 13's
  non-atomic replay finding is the constraint: a chain load that fails
  partway leaves a world that never existed, and the compensating verb is
  the one most likely skipped. Reattach by instance ID (item 13's
  recommended fix) rather than replaying `spawn`.
- **Persist the recipe form, not the handle form.** A post-evaluation
  `Result.ID()` is an engine-local shared-result reference that dies with its
  session (`call.NewEngineResultID`; measured — loading one later on the same
  engine fails with "missing shared result"). §8 measured both encodings:
  ~24 bytes for the handle, ~350 for the recipe of a bare `llm` seed.

Two boundaries to settle before promising cross-machine migration:

- **Lifetime.** §4 dodges "when does an agent outlive every session that can
  see it?" and Cloud makes that urgent rather than optional: once an agent is
  addressable from anywhere, its lifetime is no longer bounded by the session
  that spawned it, and something has to own the answer.
- **Leaves.** The trace carries recipes, not snapshots, so `host.directory(...)`,
  moved git refs, and session resources re-*evaluate*. Migration is clean for
  conversations and for content-addressed leaves; a workspace rooted in
  machine A's host is what bounds "trivially". Worth deciding which half is
  being promised before it ships.

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
would simply spawn a fresh agent (attach-by-rederivation is renounced; §8).

### 5.1 Multi-agent UI: the roster, focus, and attention

One frontend, many live agents: the client folds the `dagger.io/agent.*`
telemetry (§8) into a **roster** of every agent the trace mentions, and the
user alternates prompting between its entries. The roster is a tmux-style
switcher strip immediately above the prompt, not a sidebar section. The
sidebar (`SetSidebarContent`/`SidebarSection`,
dagql/idtui/frontend_pretty.go:846, carrying "Changes" and "References"
today) was considered and rejected as the end state on three counts: it is a
top-right overlay, so it occludes the tree it summarizes; it has no
selection model, so it could only ever duplicate the switcher rather than
*become* it; and its sections queue, so the roster would be least visible
exactly when the session is busiest. Three idioms come straight from tmux —
per-agent status flags, numbered jump targets, and a **last-focused toggle**,
since the two-agent ping-pong is the common case and a next/prev cycle is the
wrong verb for it. `tab` is unavailable for any of them: it is already the
input-mode binding (frontend_pretty.go:2421,:4552) and the completion menu
consumes it.

All three keys are bound in **both input modes**. At the prompt they can only
be modified ones — the digits themselves have to keep typing. Numbered jumps
use `ctrl+1`…`ctrl+9`; tuist enables the Kitty keyboard protocol's
disambiguate mode, under which supporting terminals send these as distinct
`CSI u` sequences. Legacy terminal input cannot represent every Ctrl+digit,
and terminal shortcuts may still consume them before they reach the TUI. Nav
mode is a modal context where unmodified keys are the vocabulary, so it
carries the same jumps on bare `1`…`9` and the last-focused toggle on `` ` ``
(tmux's `l` is nav mode's expand). The prompt bindings stay for everyone they
do reach, since this adds a path that always works rather than replacing one.
It also settles a live false affordance: the strip already printed
`1:chief* 2:scout` in nav mode, where those numbers did nothing at all.

Nav mode additionally binds the `[`/`]` cycle the paragraph above renounced.
That renunciation stands as far as it went — the toggle, not the cycle,
answers the two-agent ping-pong, which is why nav mode binds the toggle too —
but it was an argument about the common case, not about a roster long enough
that finding an agent's number is itself the work, where stepping along the
strip beats counting it. Not `ctrl+[`, the obvious pairing, and not because
it cannot be read: tuist enables the Kitty keyboard protocol's disambiguate
mode unconditionally, under which `esc` arrives as `CSI 27 u` and `ctrl+[` as
`CSI 91;5u` — genuinely distinct. The objection is that it is *ambiguous*:
every terminal without that protocol (Terminal.app, older xterm, tmux/screen,
conhost) sends `ctrl+[` as the bare 0x1B byte, which decodes as plain `esc`,
and nothing here consults `HasKittyKeyboard` to tell the two worlds apart. It
would be a real key for some users and a silent Esc for the rest — and even
where it works it is muscle memory for Esc, so taking it breaks Esc for
exactly the users whose terminal is good enough to give it to us.

The keys **split by verb** on whether they return to the prompt. A digit or
the toggle *names* a destination, so it hands the prompt back: that is the
whole point of the per-agent draft below, which only pays off if the next
keystroke is the message. The cycle *surveys*, and is meant to be tapped until
you land on the one you want, so it stays in nav mode — a cycle key that
switched modes would type its own second press into the input. The strip's `*`
marker is its feedback instead, which is why focus is believed on the keypress
and confirmed afterwards rather than waiting on the round-trip that retargets
the handler. All of them bind only while the strip is on screen: an unmodified
key may not be swallowed to address something invisible.

**Focus is moved only by a keypress, never by an event.** An agent that
needs the user *advertises* attention on its roster entry; the user decides
when to go. Attention taken rather than advertised would drop a message into
whatever was half-typed, and the parked-question model (§3.4) exists
precisely because the human is not a service to be called. A draft is kept
per agent, saved on blur and restored on focus, so an interrupted
composition survives the switch. Ctrl-C interrupts the FOCUSED agent only:
`interrupt` is per-runtime (§3.5), and a key that preempted every live agent
at once would be unusable in exactly the sessions this UI is for.

Focus makes the client's session-scoped affordances **focused-agent scoped**,
and that is accepted deliberately: `/compact`, `/save`, `ctrl+s` and the
status line's context gauge (internal/cmd/dagger/llm.go:596-603) each
describe one conversation, so they follow focus, and session-wide totals move
to the roster header. The alternative is a status line that lies about which
conversation it is describing.

**The tree follows focus too** (built; §10). The strip moved the prompt
between agents while the tree above it kept showing the whole session, so
switching told you nothing about what the agent you switched to was doing —
the roster's own purpose, missing its other half. The scoping rides the
conversation-PROMOTION axis, not the zoom axis, and that is a renunciation
rather than a preference: focus could have been a write to `ZoomedSpan`, the
existing "show me this subtree" mechanism, at a cost of about five lines, but
zoom is navigation the user drives with enter/esc — so `esc` would silently
un-follow the agent the prompt still addresses, and a switch would discard
wherever the user had navigated to. Two cases deliberately keep the whole
trace: no strip on screen, since a single-agent session's one agent already IS
the whole conversation and narrowing it would change what every existing
session renders; and a focused agent that has surfaced no turn yet, since
promoting an empty set onto a Passthrough host is a blank screen rather than
an empty conversation.

Build order is **roster first, attention second**. Slice 1 is the engine's
telemetry publication plus a read-only roster strip — no focus, no change
to routing (both built; §10). Slice 2 is focus, per-agent drafts, and the
`LLMSession` refactor (it held exactly one agent), which had to land
*together with* the send-routing change: bolting focus onto the old
`shellRunning → Interject` latch would silently deliver messages to
whichever agent happened to own the in-flight turn (built; §10). Slice 3 is
the parked-question/attention work (§3.4), which is what makes the roster
worth having — a roster where nobody can ever say "I need you" is only a
progress display.

Four constraints surfaced while building slice 2's routing half, each of
which the plan above understated. **Ctrl-C cannot simply "follow focus":**
interrupt used to be a client-side context cancel of the shell handle's
`WithPrompt` await, so it necessarily hit the agent owning the in-flight
turn — the very inference this section outlaws for messages — and since
`shellLock` serialized handles, an agent that is running but is not the one
blocking the handle could not be interrupted from the client at all. Slice 2
turned Ctrl-C into an explicit `interrupt` on the focused agent's runtime,
not a re-pointed cancel (§10). **Session save/load stays session-wide:**
`initialPrompt`/`sessionUUID` live on the shell handler, so the auto-save
writes one file per SESSION while this section scopes `/save` to a
conversation — with two agents, the last to step wins the file. That needs
a per-conversation save identity, which does not exist yet. **The status
line already mixes scopes:** the frontend's live rollup is deliberately
session-wide (all models and sub-agents) while the per-conversation
snapshot is not, so moving session-wide totals to the roster header is
required for the line to stop lying, not a nicety. And **prompting an
attached agent is not purely additive:** submit is send + resume + await,
and the resume un-pauses a runtime the session does not own. Whether that
is correct ("prompt it exactly like your own") or wants a
resume-only-if-owned rule is undecided; it is the one place attach reaches
past observation.

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
  real). *Superseded by hack/designs/agent-messaging.md §4.3: lifecycle
  events delivered as mailbox messages make the combinator unnecessary for
  agent loops; a combinator for non-model orchestrator code stays open
  there.*
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

## 8. As-built ratifications

Semantics settled during implementation (core/agent.go, core/agent_telemetry.go,
core/schema/agent.go, engine/telemetryattrs/attrs.go,
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
  record. `send` to `STOPPED` reopens the same runtime entry from its last
  committed snapshot and reports `STARTED`; `resume` also relaunches STOPPED
  so resume-first callers such as `modules/staff` work without a special case.
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
  The registry keys on that minted ID directly (originally on the agent
  value's content digest, which contained it; see §10.2 for why the digest
  had to go), so seal ordering, signal-with-start, relaunch-from-STOPPED,
  resume-retries-FAILED, message pinning, tombstone readability (now
  per-instance), and no-namespace all hold verbatim.
  Renounced with it: attach-by-rederivation — two evaluations of the same
  composition are two agents, and observing a running agent requires
  holding its ID (§3.3's addressing model, now strengthened: IDs are
  unforgeable, since composition knowledge alone no longer derives a live
  handle). Stop no longer ends the instance identity: it ends the current
  loop, and the held ID is the capability that may relaunch it.
- **Imperative verbs are ID-returning, sync-style.** `spawn`, `start`,
  `send`, `interrupt`, `pause`, `resume`, `waitFor`, and `stop` return
  `ID!` with `@expectedType`, exactly like `Service.start`/`stop` and the
  `sync` fields. Lazy clients (Dang) force scalar-returning fields at the
  call site and re-hydrate the ID into an object via the annotation, so the
  side effect executes exactly once — eliminating the duplicate-send
  hazard of re-forcing a lazy `DoNotCache` chain. Typed SDKs do the same
  at codegen time: an `ID!` return with `@expectedType` is loaded as that
  object, so `spawn` hands back an `Agent` and `send` an `AgentMessage`
  rather than their IDs. Reads stay
  object-returning: `agent(id:)` and `message(id:)` (pure lookups), and
  `snapshot`. For `spawn` and `send` the returned ID is the pinned lookup
  chain (previous bullets), so re-hydrating it replays the lookup, not the
  mint/enqueue.
  **Scope correction, measured.** That guarantee covers the ID-returning
  verbs and nothing else, and it is the ID RETURN that earns it, not the
  cache policy. Dang forces a lazy chain once per SELECTION off it, not
  once per `let` binding, so `let x = <object-returning call>` followed by
  `x.a`, `x.b`, `x.c` runs the chain three times — and when that call is a
  module function carrying `@cache(policy: Never)`, dagql cannot dedupe it
  (`core/modfunc.go` gives it `PerCallInput`), so every selection re-runs
  its side effects. `TestLazyChainForcing`
  (core/integration/dang_forcing_test.go) measures both halves against a
  from-source engine with `LLM.spawn` as the witness — DoNotCache and a
  fresh instance per evaluation, so distinct handles count executions: two
  selections off an unpinned chain yield 2, the same reads through an
  ID-returning pin yield 1. The by-hand escape hatch, where a type has no
  `sync` of its own, is to route the binding through one that does
  (`modules/staff`'s harvest tools pin the worker's snapshot through
  `LLM.sync`).
- **Self-await hazard.** A tool holding its own calling agent's handle (via
  `Agent!` injection) can `send` to it — the message joins the in-flight
  turn as `STEERED` — but awaiting it from inside that same turn's tool call
  is a deadlock (the turn cannot end until the tool returns). Fire-and-forget
  sends to self are legal steering; awaits belong on *other* agents.
- **Telemetry is the directory, and it needed no new schema.** Discovery is a
  purely client-side derivation from the trace a client already ingests: the
  capability was in every trace before any of this was written. `spawn` pins
  instance identity through an internal `Select` of `agent(id:, name:)`
  (core/schema/llm.go:523-538), and every dagql call span carries the full
  protobuf `Call` (`DagCallAttr`, core/telemetry.go:92-105), so a client
  reconstructs a *sendable* handle from a carried digest exactly as
  `llmCallDigest` → `loadIDFromSpan` (dagql/idtui/frontend_pretty.go:5012) →
  `dagger.Ref` already does for branch-from-message. §3.3's renunciation of
  `Query.agents` therefore holds as an API contract and not merely as advice:
  the roster is a projection of the trace, an agent whose spans a client
  cannot see stays unreachable to it, and the schema gained nothing.
  Verified end to end rather than argued: `TestRosterAddressing`
  (core/integration/agent_runtime_test.go) runs a real OTLP hop into a
  `dagui.DB` and asserts the rebuilt handle observes the *live* runtime —
  facts a re-derived value could not fake, since re-deriving the composition
  yields the same content digest and would land silently on a fresh inert
  entry. Two facts the argument did not predict: the lookup's span is flagged
  internal (nested `Select`) yet still carries its payload, which is exactly
  what the walk needs; and `spawn`'s returned ID is the *handle* form of the
  pinned chain (~24 bytes, an engine-local shared-result reference), while
  the trace-rebuilt one is the *recipe* form (~350 bytes for a bare `llm`
  seed, growing with the composition) — different encodings, same instance.
  The headline case — an agent spawned INSIDE a module call, which the user
  never spawned and holds no handle to — is covered by
  `TestRosterAddressingFromModule` (fixture
  `testdata/modules/go/agent-hirer`), and closes too: a chain mixing client
  calls, calls the module issued from its nested session, and a module
  provenance frame all rebuild, because every dagql call span carries its
  payload wherever it was issued. Two notes for whoever wires the UI. The
  walk's failure mode is loud, not silent — `ID.decode` errors with `call
  digest %q not found` (dagql/call/id.go:767-770), so a roster entry
  degrading to read-only is detectable rather than a wrong handle. And
  module frames are rarer in agent chains than expected: provenance attaches
  to module-DEFINED fields (core/object.go:1107), and a module function
  returning a core object hands back that object's own core recipe, so a
  worker composed inside a module carries no module frame at all unless one
  rides in on an argument — e.g. a module object bound with `withTools`,
  which is how a chief's own conversation gets one.
  **Scope correction, measured later:** "verified end to end" covered what
  those two tests compose and no more. A live session found a staff worker
  whose chain could not be rebuilt in the very session that spawned it
  (item 16): the span channel alone cannot carry every frame, because a
  payload only ever rode the span of its own selection and whole classes of
  frame are never independently spanned. Fixed by giving call payloads a
  second channel (§10.2, "Mode A: RESOLVED"). The guarantee to state today is
  therefore that a rebuilt handle RESOLVES — not that it addresses the live
  runtime, which is Mode B and still open.
- **Identity rides span attributes; state rides log records.** The split is
  forced by the export model, not chosen for tidiness. A live span is
  exported as a *snapshot taken at span start* (`LiveSpanProcessor.OnStart`
  calls `OnEnd(SnapshotSpan(span))`, github.com/dagger/otel-go), and
  `SpanHeartbeater` (engine/telemetry/heartbeat.go:55-67) re-exports that
  same frozen snapshot for as long as the span lives — so an attribute
  written onto a live span later reaches no client at all, even though
  client-side ingest would have merged it fine (`recordOTelSpan`,
  dagql/dagui/db.go:608-652, re-processes attributes on every export). So
  immutable identity facts (`AgentAttr`, `AgentIDAttr`, `AgentNameAttr`,
  `AgentCallDigestAttr`) are stamped at span start (`agentSpanAttrs`), and
  mutable lifecycle state (`AgentStateAttr`, `AgentWaitingOnAttr`) is emitted
  as OTel **log records** attributed to the loop span (`EmitAgentState`) —
  the same channel streaming progress uses (`EmitProgress`,
  engine/snapshots/progress.go:41 → `ingestProgress`,
  dagql/dagui/progress.go:82) and for the same reason. Such records are
  latest-wins and consumed as data: a client folds them into the roster
  entry, and never renders them as log text. Rejected: a child span per state
  interval, which gives per-state durations for free and needs no new ingest
  plumbing, but pollutes the very span tree the user is reading, needs dedupe
  against the projection, and has no live parent to hang the final record
  from once the loop span has ended — which breaks the FAILED-tombstone seal;
  and a `waitForChange(since:)` long-poll per agent, which is correct and
  genuinely not polling (`Agent.waitFor` already blocks server-side) but
  costs a request and a goroutine per agent per client, needs new schema
  right where §3.3 renounced it, and is dead in replay, where the roster must
  still come out of the trace.
- **The roster keys on the agent ID, and the last record outlives the span.**
  The grouping key is the spawn-minted `AgentIDAttr`, never the span ID: a
  `resume` retry relaunches the loop, so one agent owns several loop spans
  over its life. Publication is edge-triggered on the *projection*
  (`publishStateLocked`), because `transitionLocked` fires on every fact
  change and most fact changes do not move the state (the projection order
  above). And `AgentRuntime.spanCtx` is deliberately retained past its span's
  own end — a record carries its span ID whether or not that span is still
  open — so the tombstone-sealing transition in `stop`, which runs after the
  loop has already returned, still reaches a client's roster instead of
  leaving a FAILED agent apparently retryable forever.
- **A handle can be asked which instance it is.** `Agent.instanceID` returns
  the spawn-minted ID — the same value the loop span publishes as
  `dagger.io/agent.id`. It was added for focus (§5.1): a client that
  discovers agents through telemetry keys its roster on that ID, and without
  a way to ask its own handle, it cannot tell a rostered agent apart from one
  it is already driving — so focusing your own agent from the strip would
  attach a second conversation to a runtime you already hold. It grants no
  new reach (you still need the handle to ask), so §3.3's capability model is
  untouched, and clients tolerate its absence: an older engine still spawns
  and prompts, it just cannot be correlated with its roster entry.
- **Routing follows FOCUS, never the busy turn.** The client's rule, forced
  by the same argument as everything else in §5.1: a submitted message goes
  to the focused conversation's own in-flight turn, and opens a new turn if
  it has none — the previous "hand it to whatever turn is running" latch
  delivered the user's words to whichever agent happened to be mid-step.
  Three consequences settled while building it. Prompt turns stopped being
  serialized behind the client's single interpreter (only a shell line or a
  `/command` is), because otherwise a running agent makes every other agent
  unreachable — so the client tracks turns as a count, and only a serial turn
  makes a submission queue. A message typed while a turn is still OPENING
  (reference attachment, auto-compaction, spawn, send: all round trips) is
  buffered and flushed onto the record behind the prompt, rather than opening
  a rival turn — a rival's own compaction or rebinding can replace the LLM
  wholesale and stop the runtime the first turn is running on. And Ctrl-C
  cancels an in-flight client turn, but with none it interrupts the focused
  runtime only if that runtime is actually RUNNING: interrupt on an idle
  agent is equivalent to pause, and the key's commonest use is clearing a
  half-typed line.

### 8.1 Harvesting a worker's work

Every worker gets its own `Workspace`, so anything it edits or commits is
invisible to the chief until it is deliberately taken. `Workspace.commitsFrom`
/ `withCommitsFrom` and the `modules/staff` harvest family
(`logOf`/`diffOf`/`pull`/`pullConflicted`/`pullPending`) close that gap.
Semantics ratified during implementation:

- **Application is patch-based, never whole-file overlay.** `withChanges` is a
  `ReplaceExisting` copy, so applying a worker changeset anchored at spawn
  time would silently clobber the chief's newer edits. Each commit is applied
  as a patch to the receiver's *current* content, so a commit still lands
  cleanly when the receiver has moved on since the worker branched off.
  Patch application is the merge; `withChanges` is only the write.
- **Plan/apply split.** A pure planner (`commitsFrom`) classifies each of the
  source's staged commits — PICKABLE, PICKED, REDUNDANT, or CONFLICT with a
  reason (CONTENT / DIRTY) and the obstructing paths — and a strict apply
  (`withCommitsFrom`) executes. Conflicts are *data*, not errors; but a commit
  the caller explicitly asked for is never silently dropped, so the apply
  raises on any conflict in its set. **Skip-and-continue is the module's
  policy, not the engine's**: `pull` plans, then applies only the pickable
  set, and reports the rest.
- **The fold judges each candidate against the workspace that would exist if
  every prior pickable candidate had been applied**, oldest first. The cascade
  property falls out for free: a skipped commit never folds, so a later commit
  building on it is patched against a tree lacking its pre-image and lands as
  CONFLICT. Every fold step is a real dagql field selection, which is what
  keeps a plan and the apply that follows it on the same cache entries — and
  is why the planner cannot be an in-Go loop (N constructions would collide on
  one call ID).
- **Provenance collapses transitively to the root.** A replayed commit records
  the original as its `origin`; replaying a commit that already carries an
  origin records THAT origin, not the immediate source's hash. So a commit
  pulled A → B → C still names the commit A staged, and a later pull straight
  from A recognises it as already present.
- **Dirty-path refusal.** A candidate touching a path the receiver has
  uncommitted edits on is refused (DIRTY) rather than applied — git
  cherry-pick's rule for a dirty worktree, and the guarantee that the chief's
  WIP is never swept into a commit attributed to a worker. `git.unmanaged`
  joins `git.uncommitted` in the dirty set: those edits are invisible to
  `uncommitted` yet would be clobbered by a whole-file write.
- **The reverse-apply probe is what makes REDUNDANT real.** `git apply`
  refuses an already-applied patch rather than producing an identical tree, so
  "the chief hand-merged the same fix" would otherwise be misreported as a
  content conflict. A patch whose *reverse* applies cleanly is git's own
  definition of already-applied (what `git am`/`rebase` use); it only runs
  after a forward failure, and a partial hand-merge fails both directions and
  stays CONFLICT. Correct.
- **`pullConflicted` is deliberately NOT a mode of `pull`.** A commit must
  never be staged with conflict markers inside it — that would put a broken
  tree in history under the worker's name. So the resolution lands in the
  chief's working tree, the commit that records it is the chief's, and the
  worker's authorship is lost by construction; the original message is printed
  for reuse, and a commit that would have applied cleanly gets an advisory
  NOTE steering back to `pull`.
- **`pullPending` re-anchors by intersecting with tree drift.** Naively
  applying the worker's `uncommitted` patch to the chief's tree fails in the
  COMMON case: the worker inherited the chief's pending edits at spawn, so its
  patch contains hunks the chief already has, and `withPatch` is a plain
  `git apply` with no 3-way. Scoping the patch to the paths where the two
  trees genuinely differ removes whole-file overlap. Partial overlap *inside*
  a file still fails — honestly, pointing at `markers: true`.
- **Tombstones are what make WIP rescue possible.** `dismiss` files the
  worker's handle away instead of dropping it, and the harvest family resolves
  through live members *then* tombstones, so a forgotten `pullPending` is
  still recoverable until the session ends. The steering tools keep resolving
  through `member()` alone, so they can never address a corpse.
- **The harvest reads the source only through already-materialized in-engine
  values** (its recorded changesets), never through its host, so a worker's
  workspace is classified without routing to its client. Only receiver reads
  go through the host. Preserve this: pulling the source's host in would make
  the operation depend on a second live client.
- **The patch-based rule governs changesets moving INTO a receiver, and
  deliberately does not govern a worker's own workspace.** A worker's overlay
  is not a frozen tree: a host-backed overlay resolves as a sparse host read
  at READ time with the changeset applied on top (`resolveHostOverlayRootfs`),
  so its base is live. What is frozen is the changeset, whose diff layer
  carries FULL CONTENT for every touched path and is written with
  `ReplaceExisting`. So the split is per path, not per tree: paths the worker
  has never touched track the host live, and on the paths it HAS touched its
  own content wins over whatever the host holds at read time — no patch, no
  conflict detection, no refusal. Ratified as correct: that is what a pending
  edit IS, and a worker's edits outranking the checkout is the same rule that
  governs the chief's. A worker is not frozen at spawn; it owns the files it
  has its hands on, and rolls forward with the host everywhere else. The
  bounded consequence worth knowing is that a path edited on the host AFTER a
  worker has touched it keeps the worker's version silently — an argument for
  harvesting promptly, not for making a worker's overlay patch-based.
- **Same agent through a wholesale conversation replacement: `reseed`.**
  Ratified against live pain: every wholesale LLM replacement in the CLI
  (auto-compaction the everyday trigger; export, reset, model change,
  branch the deliberate ones) dropped the runtime and spawned a successor,
  so one conversation accumulated identically-named STOPPED tombstones —
  first papered over by hiding stopped agents from the roster strip, which
  swapped the zombie-duplicates problem for agents jarringly vanishing.
  The engine already accepted "same agent, wholesale new conversation"
  from INSIDE a turn (a tool returning an `LLM` acts as a continuation and
  the loop adopts it, core/llm.go); `Agent.reseed(conversation:)` is that
  same adoption exposed as a client-facing verb, and the CLI's `updateLLM`
  now routes every replacement through it, dropping only as a fallback.
  The premise behind the old drop — "a different value digest is a
  different instance by design" — had already died with the InstanceID
  registry pivot (§10.2); the drop was a workaround for the missing verb.
  The verb is argument-shaped where `rehydrate` is receiver-shaped, and
  the split is a rule, not an accident: creation verbs (`spawn`,
  `rehydrate`) read the receiver as the seed because creation is the one
  moment a chain defines an entry; runtime verbs' receivers name an
  instance and never redefine one, which is the §10.2 invariant that keeps
  telemetry-rebuilt handles inert. `rehydrate` errors when the entry
  exists, `reseed` when it does not — opposite guards, both load-bearing,
  so they stay two verbs rather than an upsert. With no successors minted,
  the roster strip shows STOPPED agents again (a stopped entry now means a
  real dismissal, rendered dim and readable until an explicit `send` or
  `resume` relaunches it).
  `TestReseed` (core/integration) pins the semantics: continuation proven
  by the replay provider, queued mail draining onto the new conversation,
  FAILED composing with resume, and refusals for no-runtime, mid-turn,
  suspended-turn, and stopped.

## 9. Alternatives considered

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

## 10. Implementation status

What is BUILT (see also §8 for ratified semantics):

- **Core runtime**: `core/agent.go` (Agent value with spawn-minted
  `InstanceID`, `AgentRuntimes` session registry keyed by that ID —
  collision-free by construction, and stable across the re-execution a
  telemetry-rebuilt handle performs (§10.2) — loop with mailbox drained at
  step boundaries, tombstones), `core/schema/agent.go` (fields: `name`,
  `state`, `snapshot`, `start`, `send`, `message`, `waitFor`, `pause`,
  `resume`, `interrupt`, `stop`; `AgentMessage.{delivery,await}`;
  `AgentState`, `AgentMessageDelivery`). Registry wiring in
  `engine/server/session.go` alongside `Services`.
- **Spawned instance identity**: `LLM.spawn(name)` mints a unique instance
  per call and pins it through the pure `LLM.agent(id:, name:)` lookup
  (§8), in `core/schema/llm.go`; name is display-only. `asAgent` is gone.
- **Message identity**: re-exec pinning via `Agent.message(id:)` (§8) —
  handles are honest chains, cancel-and-re-await works across requests.
- **ID-returning verbs**: the imperative fields (`spawn`, `start`, `send`,
  `interrupt`, `pause`, `resume`, `waitFor`, `stop`) return `ID!` with
  `@expectedType`, `Service.start`-style (§8); reads (`agent(id:)`,
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
- **Agent telemetry publication** (§3.3, §8): the `dagger.io/agent.*`
  vocabulary in `engine/telemetryattrs/attrs.go` (`AgentAttr`, `AgentIDAttr`,
  `AgentNameAttr`, `AgentCallDigestAttr`, `AgentStateAttr`,
  `AgentWaitingOnAttr`), `core/agent_telemetry.go` (`agentSpanAttrs`,
  `EmitAgentState`), and the loop span in `core/agent.go` — anonymous before
  — now stamped with identity at start; `AgentRuntime` gained `spanCtx` and
  `emittedState`, and `transitionLocked` publishes through
  `publishStateLocked` on every change of the projected state. Every live
  agent, module-internal ones included, is now discoverable in the trace.
- **Client-side roster** (§5.1, slice 1): `dagql/dagui/agents.go` folds the
  published directory into `DB.Agents()` — `AgentNode` keyed by
  `AgentIDAttr`, so a resume-retry's second loop span joins the same agent
  rather than forking a phantom one — with the span attributes decoded in
  `spans.go` and the state records consumed in `ingestAgentState` beside
  `ingestProgress`, which keeps them out of the log text and bumps the
  mutation counter the roster memo keys on. Deliberately flat and
  containment-free, unlike `SurfacedServices`: a worker born under its
  chief's tool-call span (a Boundary) is exactly what the roster exists to
  reveal. `dagql/idtui/agent_roster.go` renders it as the strip above the
  prompt, hidden below two agents so single-agent sessions are untouched.
  Tests: `dagql/dagui/agents_test.go`, `dagql/idtui/agent_roster_test.go`.
- **Roster addressing and focus** (§5.1, slice 2): the strip is a switcher.
  `internal/cmd/dagger/session_agent.go` splits the CLI session in two — a
  `sessionAgent` is ONE conversation (LLM value, runtime handle behind the
  `agentRuntime` interface, turn state, model, references, auto-compact,
  context baselines), and `LLMSession` (llm.go) owns the conversations plus
  the session-wide plumbing, resolving routing in exactly one place:
  `Target()`. Ownership is one flag, read where a handle leaves a
  conversation, so `dropAgent` stops only what the session spawned and
  clearing an attached conversation can never kill somebody else's worker.
  Frontend half in `dagql/idtui/frontend_pretty.go`: `ctrl+1…9` jump to a
  roster entry and `alt+l` toggles back to the last (tmux's idioms), with
  nav mode carrying both on unmodified keys — `1`…`9` and `` ` `` — plus a
  `[`/`]` cycle, since modified digits require an enhanced keyboard protocol
  or terminal-specific encoding. The keys split by verb: naming an agent
  returns to the prompt, the cycle stays in nav mode so it can be tapped.
  Focus is believed on the keypress and
  confirmed after (`pendingFocusAgent`), which is what the strip's `*`
  marker and the cycle's next step both read — the handler's target is only
  re-pointed on the shell goroutine, so counting from it would step from
  where focus has been rather than where it is going — and one request is
  allowed out at a time, so a burst of taps attaches where the user landed
  instead of to every agent walked past. Each agent keeps its own draft, and
  an agent the session does not already drive is attached to through a
  handle rebuilt from the trace via `encodedIDForCallDigest` — factored out
  of `llmBranchID`, so branch-from-message and roster addressing share the
  one proven path. A failed rebuild marks the entry read-only rather than
  faking a handle, and the cycle steps over such entries rather than
  reporting one the user never named. Submission asks the target first and
  queues only behind a serial turn; Ctrl-C interrupts the focused runtime
  (§8). `Agent.instanceID` is what correlates a held handle with its roster
  entry. Tests: `internal/cmd/dagger/session_agent_test.go` (routing,
  ownership and interrupt policy against a fake runtime, no engine) and
  `dagql/idtui/agent_focus_test.go` (routing, Ctrl-C, focus keys in both
  input modes, the cycle's consecutive taps and request coalescing, drafts,
  read-only entries, and the trace push that makes the strip re-render).
- **Roster conversation switching** (§5.1): the tree above the strip follows
  focus. `dagql/dagui/conversation.go` gains `SurfacedConversationForAgent`,
  keyed on the AGENT rather than a span — a resume relaunches the loop under
  a fresh span, so an agent owns several loop spans over its life and scoping
  to `AgentNode.Span()` alone would silently drop everything said before the
  last relaunch — with a memo slot of its own, since the whole-trace memo is
  single-entry and keyed by root while both are read on the same render.
  Promotion splits into `PromoteConversationNodesTo` plus a matching
  `DemoteConversationNodesFrom`: promotion is an ADD into a set that outlives
  the render (it mutates the cached, reused DB's spans), so it can express a
  fixed scope but not a CHANGE of scope, and a switch that skipped the
  withdrawal would reveal both agents' transcripts at once rather than
  switching between them. `promoteConversationLocked`
  (`dagql/idtui/frontend_pretty.go`) chooses the scope and withdraws the
  previous one; focus invalidates the view as well as the strip, checked
  against focus alone so the state flags — which fingerprint in the same
  place and change far more often — do not force a recalculation. Tests:
  `dagql/dagui/agent_conversation_test.go` (scoping, the relaunch union, memo
  independence, and withdrawal reaching nested reveals) and
  `dagql/idtui/agent_conversation_focus_test.go` (switching re-scopes and
  retracts, zoom is untouched, an agent with nothing said keeps the session).
  NOT verified: `TestTelemetry/TestGolden` renders through this same
  promotion path, but it needs an engine to warm up and has not been run
  against this change. It is a regression test rather than an iteration
  one, which is what item 7 proposes to make explicit.
- **CLI prompt mode** (`internal/cmd/dagger/session_agent.go`, `shell.go`,
  `dagql/idtui/frontend_pretty.go`): submit = send + resume + await,
  re-rooting on `snapshot` at turn end; mid-turn submissions send
  immediately (STEERED); Ctrl-C → `interrupt` (PAUSED, prefix kept), next
  submit resumes; wholesale LLM replacement stops the stale runtime and the
  next submit spawns afresh — instance uniqueness comes from `spawn` itself,
  so the session's old entropy naming is gone.
- **Async orchestration module** (§3.3 Team sketch, realized):
  `modules/staff` — spawn/sendTo/ask/status/read/collect/interruptWorker/
  dismiss over module-held `[Agent!]` state (the `modules/editor`
  pattern), with each worker given an `askChief` line home whose answers
  ride the chief's own record. Registered in the repo dagger.toml as
  `env.dev.modules.staff`. Side-effecting and
  live-reading tools carry `@cache(policy: FunctionCachePolicy.Never)` —
  load-bearing: dagql otherwise replays identical-arg calls, so a
  zero-arg `status` could never observe a state transition. Windowed
  reads (`read_agent`-style) are the module-side `read` projection over
  `snapshot.messages`, as predicted — no core work needed.
- **Workspace harvesting** (§8.1): the core API is
  `Workspace.commitsFrom` / `withCommitsFrom` (+ the internal
  `__withReplayedCommit`), with `WorkspaceCommitPick` and its
  status/reason enums, `WorkspaceStagedCommit.origin`, and
  `WorkspaceRepoContainsCommits`; the chief-facing tools are
  `modules/staff`'s `logOf`/`diffOf`/`pull`/`pullConflicted`/
  `pullPending`, resolving the worker's workspace through
  `member(name).snapshot.workspace` and a `tombstones` map so a dismissed
  worker's WIP is still reachable. All five are `@cache(Never)` because
  they read the worker's live snapshot, which is not part of the call's
  arguments. Tests: `core/integration/workspace_commit_from_test.go`
  (15 cases, including the drift, dirty-refusal, cascade and hand-merge
  cases), plus a live end-to-end pass of the whole loop against real
  workers.
- **Tests**: `core/integration/agent_runtime_test.go` (fixture
  `testdata/modules/go/agent-hirer`, for the module-internal roster case) +
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

What is NOT built — threads to pull, each self-contained. The numbers are
stable identifiers, cited from code comments and from earlier sessions, so a
thread that has since been resolved or retired keeps its slot and says so
rather than being renumbered away:

1. **Roster follow-ups** (§5.1): addressing itself is BUILT (above), but
   three threads it exposes are not. **Attaching prompts a runtime the
   session does not own**: submit is send + resume + await, and that resume
   un-pauses somebody else's worker — whether that is right ("prompt it
   exactly like your own") or wants a resume-only-if-owned rule is
   undecided. **Save identity is still session-wide**: `initialPrompt`/
   `sessionUUID` live on the shell handler, so with two conversations
   stepping, the last to step wins the auto-save file; a per-conversation
   save identity does not exist yet. **The status line does not follow focus
   for session-wide totals**: the frontend's live rollup covers all models
   and sub-agents while the per-conversation snapshot does not, so those
   totals belong in a roster header rather than on a line that describes one
   conversation. Also unwired: the roster shows only name + state, so an
   agent's `waitingOn` question is carried but never rendered (blocked on
   item 4), and entries are flat — a chief and its workers look alike.
2. **Enqueue guards** (§3.3): depth limiting, self-send rejection, cycle
   detection — none exist. Central point: the enqueue path in
   `AgentRuntimes` (`Send`). Until then `modules/staff` documents the
   ask/askChief deadlock and steers the chief around it by prompt.
   *Designed: hack/designs/agent-messaging.md §4.5 puts waits-for edges and
   cycle refusal at the blocking primitives (self-await refusal included) —
   dogfooding showed prompt-steering does not work (its §1–2). Depth/rate
   limiting stays open there.*
3. **Provenance stamping** (§3.3): drained messages record plain
   `withPrompt` selectors with no sender identity; no "via X" in history
   or telemetry. *Designed: hack/designs/agent-messaging.md §4.1 — origin
   resolved at the enqueue path from `AgentFromContext`, recorded as a
   `withPrompt` argument, rendered to the model as attribution and to
   clients via `LLM.messages` and telemetry.*
4. **`WAITING_INPUT` / `waitingOn`** (§3.4): enum value exists but is
   unreachable; needs the user-ask parking path (the non-modal
   resurrection of the dead `LLM.Interject`). The telemetry half is
   pre-wired: `AgentWaitingOnAttr` rides every state record and is emitted
   empty until something can park a question, so the roster's attention flag
   (§5.1) lights up the moment the state becomes reachable.
5. **Loop-as-sugar** (§7): `LLM.loop`/`step` remain an independent code
   path; consequently sync loops cannot satisfy `Agent!` args. Note the
   spawn pivot constrains the eventual sugar pleasantly: `loop` stays a
   cached pure field whose resolver would spawn internally, and identical
   `llm.loop` chains still dedupe at loop's own call ID — the second
   evaluation cache-hits `loop` and never reaches the inner spawn.
6. **`awaitAny`/`awaitAll`** (§7): absent; orchestrators poll
   `state`/`waitFor` per agent (staff's `collect` is a per-worker
   `waitFor(IDLE)`). *Superseded by hack/designs/agent-messaging.md §4.3
   (see §7's note); its §4.4 also fixes `collect`'s hang on FAILED with a
   settled-state wait.*
7. **CLI follow-ups**: re-enabling undo/fork in prompt mode (the
   "interrupts lose progress" rationale is retired — server-side
   interrupt is prefix-preserving); `startInteractivePromptMode`
   pre-initializes a default LLM that demands provider config even when
   the entrypoint supplies its own (pre-existing wart); and make
   `TestTelemetry/TestGolden` skip unless a flag asks for it. The golden
   snapshots are a regression test, not something local iteration should
   pay for — they need an engine to warm up — so the follow-up is to stop
   running them by default rather than to confirm them by hand. They have
   not been run against the prompt-flow rewire; they replay non-prompt
   traces and should be unaffected by it.
8. **Module-call cache staleness** — RETIRED; it was not a cache defect.
   Filed after a live session where identical-arg staff calls (`status`,
   `read`) appeared to replay stale results despite their `@cache(Never)`
   annotations. They were not stale: the session had been resumed, and
   those calls were honest reads of runtimes that item 13's
   revival-on-receiver-load had just re-minted from `Seed`. Folded into
   item 13, which is where the real defect lives; the guidance this item
   used to carry — treat repeated same-arg module reads with suspicion —
   was pointing at the wrong layer and is withdrawn. One genuine fix came
   out of the area and stands on its own: `Workspace.reloaded` used to mint
   a result for its own (`PerCallInput`) call, stamping a nonce into the
   workspace ID that every later edit inherited, so an agent that reloaded
   once re-declared its modules on every subsequent edit; it now returns
   its parent's own result (`git log -S 'Workspace.reloaded'`).
9. **Chief prompt leak into workers** — FIXED, and where the item predicted
   it belonged: in composition rather than prompting.
   `source.agents(exclude: ["staff"])` (modules/staff/main.dang:87) composes
   the workspace's agents with staff itself excluded, so neither the chief's
   orchestration toolset nor its system prompt reaches a worker — a no-op
   when the module is not installed there. `workerPrompt`'s closing
   paragraph, which used to be the whole mitigation, is now belt and braces.
   `modules/delegate`'s documented leak is the same class and is NOT covered
   by this.
10. **Workers don't know their own staff name**: `name` is display
    metadata on the runtime and never reaches the worker's conversation,
    so a worker cannot refer to itself the way the chief addresses it.
    Cheap fix — interpolate it into `workerPrompt` at spawn — with one
    catch: recordings match `workerPrompt` byte for byte, so the
    interpolation has to become part of the public constant's contract.
11. **Idle notification (chief-side)**: the chief only learns a worker
    went idle by polling `status` or blocking in `collect`. Natural fit:
    on turn-end, push a "worker ⟨name⟩ went idle" message onto the
    chief's queue — the same channel `askChief` already uses, so it
    steers an open turn or wakes the chief, with no polling and none of
    `collect`'s deadlock exposure. Could be a spawn opt-in
    (`notifyOnIdle: true`) or an engine-level watch verb on Agent.
    *Designed: hack/designs/agent-messaging.md §4.3 takes the engine-level
    verb (`Agent.notify(subscriber:, on:)`), generalized to FAILED (and
    later WAITING) transitions, with events that never relaunch a stopped
    subscriber; staff wires it at spawn.*
12. **Harvest limitations worth revisiting** (§8.1): patch scoping
    matches on `DiffStat.path` only, so a renamed file's old path can be
    dropped from a scoped patch, leaving a half-applied rename
    (`modules/review` has the same limitation, but `pullPending`
    *applies* rather than displays, so the consequence is bigger — select
    `oldPath` too). A tombstone re-selected in a NEW session projects
    IDLE-from-absence and its `snapshot` is the SEED conversation, so
    harvest silently returns "nothing new" rather than erroring; harvest
    within the session. **Observed once, and worse than "nothing new":** a
    `logOf` issued around a session restart returned a stack of ONE commit
    carrying the CHIEF's own commit message under a different hash, while the
    worker's actual commit — made minutes earlier, reported in its own final
    message — was absent. So the post-restart harvest view is not merely
    empty, it can be plausible and wrong: a chief that trusted it would pull a
    re-hashed copy of its own work and never notice the worker's real commit
    was gone. Timing was not pinned down (the restart and the call raced), so
    treat the mechanism as unestablished; the practical rule is unchanged and
    now has teeth — harvest inside the spawning session, and do not believe a
    stack you read after a restart.
    And once the chief SAVES a pulled commit and reloads, the origin link is
    gone (it is engine-side metadata), so a re-pull relies on REDUNDANT to
    notice — a durable fix needs the origin in the commit object, as a
    trailer or a git note. Fixed since: the torn-snapshot half, where a
    harvest tool's `let theirs = workerWorkspace(name)` binding re-forced
    onto a different live snapshot on every field read, so `diffOf`'s patch
    could mix trees and the tools' documented contract of reading the
    worker's LAST COMMITTED step was not what they did; they now pin the
    snapshot through `LLM.sync` (§8's scope correction on lazy forcing).
13. **A session restart silently RE-ANIMATES workers** (§4, §3.3): observed
    live — after restarting a client session, a `modules/staff` chief's
    workers reported `RUNNING` again, while their `snapshot` had degraded to
    the SEED conversation (system prompt plus opening task, none of the work
    they had actually done). A second observation narrows it: only agents
    spawned BEFORE the resume are affected — a freshly spawned agent yields
    exactly one loop — and the re-animated loops render beneath the ORIGINAL
    `spawn()` tool-call span, so one worker can appear as several concurrent
    loops under one hire.
    Mechanism, established: **the runtime registry is per-session**
    (`engine/server/session.go:532` allocates a fresh `NewAgentRuntimes()`).
    Keys are stable across sessions — the agent's content digest, minted
    `InstanceID` included — but the table they index is not, so the same key
    resolves to a FRESH entry. Every creating verb routes through
    `GetOrCreate` (core/agent.go:250) and mints from `Seed` rather than
    reattaching; `send` then signal-with-starts it. That is exactly "RUNNING
    again, snapshot back to the seed". The chief's saved recipe is what
    carries the spawn call back: `AutoSaveSession` persists `LLM.portableID`
    (internal/cmd/dagger/llm.go:953) and `recipeSelectors` (core/llm.go:2460)
    emits `withTools(object: …staff!spawn(…))`, in RECIPE form — it must be,
    since a post-evaluation `Result.ID()` is an engine-local handle
    (`call.NewEngineResultID`, dagql/cache.go:2250) that dies with its
    session (measured: loading one later on the same engine fails with
    "missing shared result"). A worker is therefore re-animated iff its
    `spawn` was baked into the restored recipe — i.e. iff it existed at save
    time — and each resume adds one more loop. All of them render under the
    original row because the replayed call ID is byte-identical, recorded
    per-call nonce included, so its `dagger.io/dag.digest` is too.
    Note what §8's pinning does NOT cover: it is `Staff.spawn` that gets
    re-executed, not `Agent.send` — the chief's conversation never records
    one. Pinning makes re-*hydrating a result ID* inert; it says nothing
    about re-*executing a recorded call*.
    **Settled by a later live session, and the answer was neither option.**
    The trigger is **receiver load**, not resume and not the first steer: a
    single `status` call — a `Get`-never-creates read designed precisely not
    to revive anything — brought three workers back, and *the call itself
    failed*. The error's own shape is the evidence, `load bound object of
    type "Staff": load xxh3:…: inputs: load xxh3:…: inputs: …`, i.e. the
    bound object's ID chain being re-hydrated input by input. The revival
    lands while assembling the receiver, one level below the field, so no
    amount of care in the read protects it: any tool call on that object
    revives, including a read built to be safe, and an aborting error does
    not roll it back. Three runtimes booted out of a call that returned
    nothing but a parse failure.
    **It can present as cache staleness.** This was filed separately for a
    while (item 8) as a module-call cache defect: identical-arg `status`/
    `read` calls appeared to replay stale results despite `@cache(Never)`.
    They were not stale — they were honest reads of runtimes this revival had
    just re-minted from `Seed`. On a resumed session, a module read whose
    answer looks frozen at the seed is this bug, not the cache.
    **Dismissal does not survive either**, and the reason generalizes. All
    three workers had been dismissed before the restart. The layers split:
    agent runtimes are core values that revive through `GetOrCreate` on chain
    load, while `dismiss` is module-held state (`modules/staff` moving a
    handle from `members` to `tombstones`, and stopping the runtime) — and in
    that session the module was exactly what failed to parse. The
    layer that knows about dismissal never replayed; the core runtimes booted
    anyway. Hence the general finding, which outlives this crash:
    **replaying a chain of imperative verbs is not atomic.** A failure
    partway leaves a world that never existed — spawned but never dismissed —
    and the compensating verb is the one most likely to be skipped, because
    it comes later in the chain. Any fix that keeps re-executing recorded
    verbs inherits this; it is the constraint §4.1 is built around.
    **Re-animated workers are also unreachable**, which compounds it into
    something worse than duplicate work. The roster rendered all three, each
    marked watch-only, and focusing one gave `agent "skimmer" cannot be
    addressed from this trace`. That is `focusAgent`'s read-only branch,
    reached when `encodedIDForCallDigest` cannot rebuild a handle: addressing
    is a projection of the trace (§8), the payloads were emitted in the
    PREVIOUS session, and a restarted client's `dagui.DB` is fresh, so
    `ID.decode` reports `call digest not found`. §3.3 renounced
    `Query.agents` *because* telemetry is the directory, so there is no
    fallback: the agents run, consume tokens, and can be watched but never
    addressed or stopped from the UI. The blast radius is bounded per session
    — the registry dies with it — but the recipe on disk resurrects them next
    time, so the cause is not bounded at all.
    Fix, recommended: **reattach by instance ID** — make registry lookup
    session-independent, keyed on the spawn-minted `InstanceID` that already
    rides the pinned chain, so a resumed session finds the live entry instead
    of minting one. **Half of this has landed**: the registry now keys on
    `InstanceID` (§10.2, fixing a different defect), so what remains is
    purely the session-independence — the table is still allocated per
    session, so a resumed session misses whatever the key is and
    `GetOrCreate` still mints from `Seed`. It is the only option where resume
    means what a user expects (the worker keeps running, and keeps its
    history), and §8 already made that identity unforgeable and
    collision-free. It needs an owner for the lifetime question §4 dodges:
    when does an agent outlive every session that can see it? Runners-up:
    make imperative verbs in a restored chain resolve to their recorded
    result (smaller, stops the duplicate work, but leaves the chief holding
    corpses); or refuse to serialize a chain containing an imperative verb at
    all — the honest stopgap, turning silent duplicate work into a loud
    save-time failure. Neither landed: this is a hole in §4's cross-session
    identity story rather than a defect in `send`, and every option changes
    engine-wide semantics. Note the receiver-load finding raises the stakes
    on the runner-up: resolving imperative verbs to their recorded results
    also fixes the "a read revived it" case, which reattach-by-instance-ID
    alone does not, since a reattached entry still has to exist somewhere to
    be found.
    Adjacent to item 12's note that a tombstone re-selected in a NEW session
    projects IDLE-from-absence with the seed as its snapshot, but distinct
    and worse: there the re-selection is inert, here it has side effects. The
    failure mode is expensive (silent duplicate work) and confusing (a roster
    full of agents that look busy but have lost their history). A roster
    (item 1) makes it more visible, not less.
    **Not the fix: making `ctrl+s` reach the staff.** Considered live and
    rejected. Workers have their own workspaces by design — the premise
    §8.1 rests on, and why taking their work is a deliberate `pull` — so a
    save that exported N divergent trees over one checkout would be either
    last-writer-wins (item 1's save-identity problem, worse) or a silent
    discard of worker WIP; and a staff-wide reset has `dismiss`'s shape with
    none of its bookkeeping. Nothing replaces it: a worker's overlay
    outranking the checkout on its touched paths is ratified as correct
    (§8.1), so what remains here is the revival defect itself.
14. **A staged commit's delta is diffed against a stale sparse base** —
    filed for six sessions as "changeset replay loses tracked-ness", a
    misnomer that steered the search wrong for most of them. **ROOT-CAUSED
    AND FIXED.**
    **The mechanism.** `stagedCommitChanges` (core/schema/workspace_commit.go)
    derived commit N's own changes by diffing its staged tree against commit
    N-1's staged tree: `before = commits[index-1].Committed.After`. Every
    `WorkspacePendingCommit.Committed` is *cumulative* and anchored on the
    overlay's `Before` **as it stood when that commit was staged**, and for a
    host-backed workspace that base is sparse — `host.directory` including
    only the paths touched so far (`sparseHostBase`). `TouchedPaths` grows
    with every edit, so the earlier tree sits on a strictly narrower base,
    and a path whose FIRST edit falls after the previous commit was staged is
    absent from it altogether. The step then reports that path as a
    whole-file ADD at its full content. Two diff anchors, sized at different
    instants; the older one lies.
    **The tell.** Commit index 0 anchors on its own `Committed.Before` — the
    current base, which does contain the path — so **the first commit of a
    session is always right**, which is the whole reason it looked
    intermittent for eight sightings. The rule is
    `edit X, commit, first-edit Y, commit` ⇒ Y reports `A +<whole file>`,
    while `edit X and Y, commit X, commit Y` ⇒ Y reports `M`. Deterministic,
    5 seconds to reproduce, no module, no worker, no restart, no checkout
    move.
    Worth stating because six hypotheses died on it: the defect is on the
    READ path, in a projection derived at render time that nothing persisted
    and no test covered. The touched set is innocent at every edit-path site
    (`TouchedPaths`, `overlayEdit`, `overlayWorkspaceWithMutation`,
    persistence), each independently audited and cleared.
    **The fix**, in `stagedTreeOver` (core/schema/workspace_commit.go):
    anchor both sides of the diff on one base, by rebuilding the previous
    staged state over THIS commit's base rather than reusing its frozen tree.
    That is the identity rather than an approximation — `withCommit` seeds the
    cumulative record from exactly that expression
    (`overlay.Before.withChanges(staged)` in `workspaceOverlayChanges`) — and
    the helper is called from both sites, because nothing linked the two
    copies of that expression and a later edit to either would silently
    reintroduce the class. The fix is retroactive: the per-commit delta is
    derived on read and never persisted, so stacks already staged in a live
    session render correctly as soon as the engine has it. Reviewed
    adversarially before landing, and the results are worth keeping: it does
    not depend on the sparse base being monotonic (it applies the previous
    changeset rather than assuming containment); it is a semantic no-op for
    value/git/rootless workspaces, whose `Before` is constant; it costs no
    extra host sync, since `withChanges` is lazy and the base is already in
    the after-side's lineage; and the guard must stay
    `index > 0 && commits[index-1].Committed != nil`, which mirrors
    `StagedChanges()` and is what makes the reconstruction exact.
    **The cost it was doing, measured rather than assumed.** The content was
    always innocent: the workspace holds the surgical edit, nothing is left
    pending, and after `export` git itself records `M` — history was never
    corrupt, only its projection. The harvest cost is a refusal, not a
    clobber: `pull` replays the same recorded changeset as a patch,
    `git apply` refuses an add over a file the receiver already has, and
    `withCommitsFrom` then rejects the WHOLE batch — so the receiver takes
    nothing, not even the unrelated commit that would have applied. That is
    why it cost authorship every time.
    **`pullConflicted` is broken for this shape too**, and that outlives the
    fix. It is documented as the recovery for a CONTENT conflict, but git
    cannot leave conflict markers for an add over an existing file; it
    refuses outright. So the escape hatch fails on the one defect it existed
    to work around. Worth fixing on its own terms (§8.1): a refused add whose
    target exists should degrade to a 3-way merge against the receiver's copy
    rather than an error.
    **Provenance.** The anchoring was introduced by `2e8c27fb8468`
    ("cli(agent): show staged commits in Changes", #13835), 25 days after
    `6376ba07d838` (#13600) made the diff base sparse — the sparse base is
    what turned a plausible-looking derivation into a wrong one. #13835 is
    still open, so this never reached `main`.
    **Regression coverage**, and one trap worth stating: the suite's Go entry
    point is `TestWorkspace`, so `-run WorkspaceSuite/...` matches NOTHING and
    reports a green "no tests to run". Use
    `TestWorkspace/TestWorkspaceStagedCommitSequence*`, which covers the five
    failing sequences and a control (`...TrackedEdit`), the saved history read
    back with git (`...SavedHistoryIsIntact`), and the cross-workspace replay
    (`...Harvest`); plus unit-level pins of the anchor arithmetic in
    `core/changeset_test.go` and of the sparse include semantics in
    `core/host_include_filter_test.go`. The confirmation experiment that
    cleared the leading suspect stayed green throughout because
    `workspace_module_edit_test.go` stages exactly ONE commit
    (`require.Len(..., 1)`) and so never reaches the `index > 0` branch —
    the transferable lesson is recorded in §10.1.
    **What this does NOT explain, and remains open.** A worker's workspace
    materializing a source file with git CONFLICT MARKERS in it (observed in
    `engine/telemetryattrs/attrs.go`, markers landing exactly where the
    chief's edit had been inserted, failing the worker's build and aborting
    `spawn`) is NOT accounted for by this defect: the replay was measured to
    refuse rather than merge, and `pullConflicted` cannot produce markers for
    an add either. With the reporting defect gone it should be re-observed
    from scratch rather than assumed to share a cause.
    **Follow-ups this turned up, none blocking.** (a) The structural version
    of the fix: record the per-commit delta at staging time, where both
    operands are already in hand, instead of re-deriving it on read — that
    retires the two-anchors class rather than this instance, at the cost of a
    persisted field and back-compat decode. (b) `withChanges` collapses a
    fully-removed directory to `RemoveAll`, which on a wider base also deletes
    siblings the narrower base never held; pre-existing, inherited by the fix,
    fixable by removing files individually and only rmdir'ing genuinely empty
    directories. (c) A commit staged on a workspace with no overlay renders an
    *empty* summary — same "summaries lie" family, different cause. (d) Latent
    in `sparseHostBase`: a touched path containing glob metacharacters, one
    starting with `!`, or one under a symlinked ancestor is silently missed;
    none occur in this checkout, all now pinned by tests. (e) Unproven
    suspect: `latest.Repo` is a full host read frozen at the first commit of a
    stack while the remainder applied on top is anchored live — same shape,
    bounded by the read epoch, and it does not match anything that was
    actually observed.
15. **`TestStaff/TestAskChiefAndCollect` is broken and SKIPPED**: it arrived
    broken from a session that stopped before resolving it, and it is still
    unknown whether the test or the code is wrong — hence skipped rather
    than deleted or "fixed" by adjusting the assertion until it passes. It
    covers the whole orchestration loop (spawn → askChief steering into the
    chief's open turn → collect), so leaving it red would mask real
    regressions in everything around it. What a run establishes: the worker
    fails at its first step with `message history diverges at index 0`, the
    replayer expecting `SYSTEM` (the `workerPrompt`) where the live history
    has `USER` (the opening task) — i.e. the worker's seed is one message
    short at the front. The compose chain is not obviously at fault: the
    trace shows `withSystemPrompt` applied (with the exact prompt text)
    immediately before `spawn`, in the right order, and real staff workers
    outside the test demonstrably DO carry their system prompt. So suspicion
    falls on how a spawned agent's `Seed` materializes its messages versus
    what the replayer compares against, not on `modules/staff`. Two
    consequences if the code side turns out to be wrong: it would mean an
    agent's recorded history can disagree with what it actually sends, which
    §3.2's influence⇔append rule forbids; and the `replay/` provider's
    byte-for-byte history matching would be the only thing that ever notices.
    Worth pairing with item 10 (workers not knowing their own name), which
    also wants to interpolate into `workerPrompt` and would move the same
    seed boundary. *Note: the askChief flow it records is being redesigned
    (hack/designs/agent-messaging.md §7 makes askChief non-blocking), so the
    eventual re-recording follows that shape — but the seed-divergence
    question here is independent of that design and must be answered on its
    own before any recording is trusted either way.*
16. **A staff-spawned worker could be unaddressable from the roster in the
    SAME session** (§3.3, §8) — distinct from item 13, which needs a restart.
    **RESOLVED.** The full account — root cause, fix, tests, and what is
    still open behind it — is §10.2 "Mode A: RESOLVED", which supersedes this
    entry; what follows is only enough to know whether §10.2 is the section
    you want.
    The symptom was a worker spawned minutes earlier in the same client
    process, fully drivable through the chief's held handle (`sendTo`
    delivered `STEERED`, `collect` and the harvest family worked), rendering
    on the roster with the read-only marker and failing on focus with
    `call digest … not found`. So the runtime was live and correct, and it
    was *addressing* that failed — the one thing §8 claimed was verified end
    to end rather than argued.
    Root cause, in one sentence: a call payload only ever reached a client as
    an attribute on the span emitted for that exact selection, and whole
    classes of frame are never independently spanned — most sharply, loading
    an ID never re-selects the calls behind it, so an ID entering a session
    from OUTSIDE it contributes zero spans and every frame behind it is
    permanently unresolvable to that client. Fixed by giving call payloads a
    second channel, as OTel log records (§10.2).
    Two hypotheses died here and should not be re-derived, both recorded in
    §10.2: that a post-evaluation `Result.ID()` handle form sits in an
    ID-literal argument (impossible by construction — `mustBeRecipe` panics
    on one), and that `Query.host`'s single per-session emission goes missing
    (measured: the client's DB held two spans carrying that digest).
    RESOLVED WITH IT: **Mode B**, where the chain rebuilds completely and the
    rebuilt handle then addresses a different, inert entry — `state` reads
    `IDLE` while `name` and `instanceID` read back correctly, because they
    are literals in the recipe. Fixed by keying the runtime registry on the
    spawn-minted `InstanceID` rather than the agent value's content digest;
    §10.2 carries the reasoning, including why the capability objection to
    that key dissolved on inspection. The live report that forced it also
    corrected the symptom's description: a missed lookup is not inert,
    because `send` creates through `GetOrCreate` — so focusing a worker and
    prompting it started a SECOND loop from the seed, which answered with no
    history, while the original kept running.

### 10.1 Notes for live QA

Hazards learned the expensive way, worth reading before driving a staff
session by hand:

- **On a RESUMED session, do not call a staff tool to "just check".** Item
  13's revival triggers on receiver load, so any call on the bound `Staff`
  object — including `status`, which is built never to create — re-animates
  every worker baked into the restored recipe, and each resume adds another
  round. Establish what you want to know before touching it. If a read's
  answer looks frozen at the seed, that is this, not a stale cache.
- **`staff.read` is the wrong tool for watching a working agent.** It omits
  tool calls by design, so a worker in a long tool-call stretch shows almost
  nothing, and SYSTEM-role padding used to consume the window (fixed).
  `ReadLogs` on the worker's span is what actually shows progress.
- **Pulling a fix to `modules/staff` does not fix the RUNNING session**: the
  loaded module is the one from session start, so a harvested change to the
  staff tools takes effect only after a reload. Expect to dogfood one version
  behind.
- **Harvest inside the session that spawned the worker** (item 12): a
  tombstone re-selected later projects IDLE-from-absence with the seed as its
  snapshot, so harvest silently reports "nothing new" rather than failing.
- **A worker you focus from the roster addresses the live runtime** (§10.2).
  Both failure modes are fixed: the loud one (a read-only `·` after the name,
  "cannot be addressed") by the call-payload log channel, and the silent one
  (an entry that renders normally but reads `IDLE` while the worker is really
  running) by keying the registry on the instance ID. If you see either again
  — especially a focused agent whose state disagrees with `staff.status`, or a
  prompt that lands in a conversation with no history while the original loop
  keeps going — that is a regression worth reporting, not the known condition
  it used to be.
- **On an engine built before item 14's fix, a worker's commit may be
  unpullable.** Any commit after the first in a session records a
  first-touched path as a whole-file ADD, and both `pull` and `pullConflicted`
  then refuse it ("already exists in working directory"); replay the change by
  hand and commit it yourself, at the cost of the worker's authorship. The
  tell is that only the FIRST commit of the session reads correctly. Do NOT
  infer from a whole-file add that the file is untracked — it never meant
  that. That inference was made here, believed, written into this section as
  fact before a human caught it, and cost the investigation several sessions
  by pointing it at tracked-ness instead of at the diff anchor.
- **Do not brief workers as read-only.** Observed live: a chief told its staff
  the workspace was read-only, so the workers spent their turns investigating
  and writing prose patches the chief then had to apply by hand — the exact
  cost the per-worker `Workspace` exists to avoid. Isolation (§8.1) is what
  makes editing SAFE, not a reason to avoid it: a worker's copy is read-write,
  nothing it does escapes until a `pull`/`pullPending`, and a commit is the
  handoff that keeps its authorship. Both prompts now say so outright
  (`chiefPrompt`: never tell a worker to avoid editing; `workerPrompt`: the
  workspace is yours, commit what you finish), and `spawn`'s own doc string
  says it too, since that is what the chief reads at call time. When briefing
  a worker by hand, hand it the change, not a research assignment.
- **A green test run against the wrong branch is worth exactly nothing.**
  Item 14 survived a confirmation experiment for several sessions because the
  fixture staged exactly one commit and so never reached the branch that was
  broken — the run was green and told you nothing. Before believing a test
  that clears a hypothesis, check that it executes the code path the
  hypothesis is about; prefer one you have seen fail first.

### 10.2 Handoff: fixing roster switching

**STATUS.** Both modes are FIXED. MODE A (the loud "never reached this
client") was resolved by the call-payload log channel, explained below. MODE B
(the silent IDLE-from-absence) was resolved by keying the runtime registry on
the spawn-minted `InstanceID` instead of the agent value's content digest —
option (a) below, taken after the security objection to it dissolved on
inspection. A recurrence of either is a bug report, not an expected condition.
Switching to a worker whose seed carries `currentWorkspace` now lands on the
live runtime; `TestRosterAddressingHostWorkspace` covers all three workspace
shapes with no skips.

Written at the end of a session that investigated ONLY this, so the next one
does not re-derive it. Everything below is about **switching between agents in
the TUI failing** — item 16's territory. It is deliberately separate from item
13 (revival on resume), item 14 (the stale diff anchor), and §4.1 (resume from
trace); those are real and pressing but are NOT this, and conflating them cost
time here.

**The one fact that scopes the work.** The live failure happened in a FRESH
session — no restart, no resume, no saved recipe, the agent spawned minutes
earlier in the same client process. So this is not a durability gap and §4.1
is not a prerequisite: the client had every payload it should ever need and
addressing still failed. Fix the implementation, not the persistence story.

**"Switching is broken" is two distinct failures.** Telling them apart is the
first thing to do with any new report, because they need opposite fixes:

- **MODE A, loud.** `Span.CallID` cannot rebuild the chain and fails with
  `cannot rebuild ID for "agent" (Agent): call <digest> never reached this
  client, referenced as …`. The roster marks the entry read-only (`·`).
  **ROOT-CAUSED AND FIXED** — see "Mode A: resolved" below. A recurrence is
  now a bug report, not an expected condition, and the error names the frame
  to chase.
- **MODE B, silent and worse.** The chain rebuilds COMPLETELY, and the handle
  then addresses a different, inert entry: `state` reads `IDLE` while the live
  runtime is something else, and `name`/`instanceID` read back CORRECTLY
  because they are literals in the recipe and never touch the registry. A
  handle that looks healthy and points at nothing. **ROOT-CAUSED AND FIXED**
  — see "Mode B: resolved" below.

**Mode B: the mechanism, settled.** `AgentRuntimes` keyed entries on the
agent VALUE's content digest (`agentKey` → `ContentPreferredDigest`,
core/agent.go). A telemetry-rebuilt ID is the RECIPE form (§8), so using it
RE-EXECUTES the chain — and `Query.currentWorkspace` is `NotReplayable` with
`PerCallInput`/`PerSessionInput` (core/schema/workspace.go:35-40), i.e.
deliberately mints a fresh value every evaluation. Fresh workspace → different
Seed → different digest → different key → lookup miss. The miss is invisible
because `Get` never creates and IDLE-with-seed-snapshot is the honest
projection of a never-started agent, so absence and freshness are
indistinguishable by construction. Measured contrast: the same test PASSES
with `host.directory(…).asWorkspace()`, which is replayable and digest-stable,
and FAILS with a bare `currentWorkspace` carrying NO overlay — so the
sparse-overlay machinery is NOT involved, `currentWorkspace` alone is. This
generalizes: ANY non-replayable or per-session leaf anywhere in a composition
broke rebuild-addressing the same way.

**Mode B is not inert — it MANUFACTURES an agent.** The account above (and
this section's original "addresses a corpse" framing) understated the damage,
and a live report is what corrected it. Reads stop at the miss, but `send`
does not: it goes through `GetOrCreate` (core/agent.go) and then
signal-with-starts what it created. So focusing a worker and typing at it
**booted a second loop from the seed**, which received the message with no
history and answered accordingly, while the original loop kept stepping
untouched. Both loops publish the same `dagger.io/agent.id` — it is a literal
on the chain — so the roster showed ONE entry, and the whole thing read as a
single agent behaving bizarrely rather than as two runtimes. Any future
registry-miss bug should be assumed generative, not merely blind: on this path
a miss is a constructor.

**Mode B: resolved — the registry keys on `InstanceID`.** Option (a) below,
and what settled it was noticing that its stated cost does not exist. The
objection was that the whole-value digest doubles as proof of possession
("you can only address a runtime by presenting the entire composition",
§3.3's capability model), while `InstanceID` is PUBLISHED as
`dagger.io/agent.id`. But the composition is published too, and by the same
channel: §10.2's own Mode A fix emits the TRANSITIVE CLOSURE of every call's
ID as log records precisely so that a client can rebuild the full recipe from
the trace — that IS the roster feature. Both the digest and the ID come out of
the trace, so keying on either draws the capability boundary in exactly the
same place: *can you read this session's telemetry*. The digest was never the
stronger secret; it was only the more fragile one. And the ID is engine-minted
entropy (`identity.NewID()` in `LLM.spawn`), scoped to a session-local
registry, so it is a serviceable capability token on its own terms.

That also **dissolves the question this section said would decide between (a)
and (c)** — whether a module can enumerate the parent session's agent IDs from
the trace. It no longer decides anything: anything that can read the IDs can
read the compositions, so (a) and the status quo have identical exposure. The
question is worth answering for its own sake, but it does not block this.

What the change is, concretely: `agentKey` returns `agent.Self().InstanceID`
rather than `ContentPreferredDigest`, and `AgentRuntimes.entries` /
`AgentRuntime.key` / `AgentMessage.AgentKey` are strings. Two consequences
worth stating because they are now load-bearing:

- **The seed is read only when the entry is CREATED.** A later handle for the
  same instance addresses the entry as it stands; its own re-derived seed is
  never consulted and cannot displace the conversation the loop has been
  building. That is what makes a rebuilt handle safe to *use* rather than
  merely safe to construct — it names an instance, it does not redefine one.
- **An empty instance ID is rejected** rather than silently sharing one key.
  `LLM.agent(id: "")` is the only way to build such a value, and under digest
  keying it was harmless; under ID keying it would be a collision, so
  `agentKey` errors.

Measured fail-first, as §10.1 demands: with the digest key restored,
`TestRosterAddressingHostWorkspace` fails `expected: "FAILED", actual: "IDLE"`
on both `currentWorkspace` cases and passes the `host.directory` one —
reproducing this section's table exactly. With the ID key, all three pass, and
the failing run's trace shows why in one glance: each read off the rebuilt
handle re-executes `LLM.withWorkspace(workspace: currentWorkspace)` afresh
while `LLM.agent(id: "m39gowtw3zfw4e71g5ta490jp", …)` carries the identical ID
literal every time.

**Not fixed by this, deliberately.** Item 13 (a session restart re-animating
workers) is a different defect and survives: the registry is still
per-SESSION, so a resumed session's lookup misses whatever the key is, and
`GetOrCreate` still mints from `Seed`. Item 13's recommended fix was
"reattach by instance ID — make registry lookup session-independent, keyed on
the spawn-minted `InstanceID`"; this lands the keying half only. The
session-independence half is what opens §4's unanswered lifetime question
(when does an agent outlive every session that can see it?), and it still
needs an owner.

**Update: the generative half of item 13 is now closed.**
`hack/designs/resume-from-trace.md` §4.2 (built) makes `AgentRuntimes.Send`
use `Get` and error on a miss, so a resumed session's lookup miss no longer
mints an amnesiac twin from the seed — it says the instance has no runtime
here. Restoring one is now an explicit verb (`Agent.rehydrate`, §4.1), and
entry creation belongs to the two verbs that create an instance: `spawn`
mints and creates, `rehydrate` adopts and creates. The registry is still
per-session, so item 13's session-independence half — and §4's lifetime
question with it — remains open and still needs an owner.

**Do not "keep the digest as a corroborating check".** It was proposed and it
does not work: the rebuilt recipe's digest legitimately differs, which is the
entire defect, so any check strong enough to be security-relevant also
rejects the legitimate rebuild it was added to enable.

**The options as they stood**, kept because the reasoning is reusable:

- **(a) Key on `InstanceID`** — TAKEN. Survives any leaf: the ID is minted at
  spawn, unique by construction, and already rides the pinned chain as a
  literal (which is exactly why `instanceID` read back correctly even while
  broken). Its stated cost dissolved, per above.
- **(b) Key on `InstanceID`, put authority in another layer** — the spawning
  session, or the ownership flag the CLI already carries (§10 slice 2). Still
  available if addressing ever needs to be narrower than "can read the trace";
  (a) does not foreclose it.
- **(c) Make the replay digest-stable instead**, by pinning the workspace into
  the chain rather than re-deriving it. Not pursued: it conflicts with
  `currentWorkspace` being deliberately live, and it fixes only the leaves
  anyone thought to pin, where (a) is indifferent to what the composition
  contains.

**Mode A: what is ruled out, so it is not re-derived.** The hypothesis that
`Query.host`'s single per-session telemetry emission goes missing was
MEASURED AND REFUTED — the client's DB held two spans carrying that digest,
one internal from `sparseHostBase` and one from the client's own chain. It is
a plausible-sounding dead end that costs a day, and the golden-trace hint that
suggested it (`Host.directory` rows with no `Query.host` row) was UI
visibility, not payload absence. More usefully, the same probe established
that the plain client-side workspace chain rebuilds COMPLETELY. So the gap is
not in a client-issued composition, which is most of the search space gone.

**Mode A: RESOLVED.** Root cause, fix and remaining edge all measured in one
session. Read this before touching anything telemetry-shaped.

*The live repro that cracked it.* The walk was first taught to name the
referring frame rather than hand a truncated recipe to `ID.decode` — which
turned a wall of identical `failed to decode receiver Call` wrappers into one
named frame (`dagql/dagui/extract.go`, `extract_test.go`). With that in a
built CLI, focusing a `modules/staff` worker gave:

```text
agent "scout" cannot be addressed: cannot rebuild ID for "agent" (Agent):
call xxh3:47ab2dce6d1d5b1e never reached this client, referenced as
argument "directory" of "withSkills" (LLM)
xxh3:edf3a4032b78d5df(directory: xxh3:47ab2dce6d1d5b1e)
```

Note this is an ARGUMENT gap, where the first occurrence of this failure was
on the receiver spine. Both shapes are the same defect; neither is special.

*The handle-form hypothesis is REFUTED BY CONSTRUCTION, not merely unproven.*
Item 16 spent a long time on "maybe a post-evaluation `Result.ID()` handle
form is embedded as an argument, and no span can ever publish it". It cannot
happen: `NewLiteralID` and `LiteralID.pb()` both call `id.mustBeRecipe(...)`,
which PANICS on a handle-form ID (dagql/call/literal.go:71-77, :110-117;
dagql/call/id.go:110-114). Any ID literal in a recorded call is recipe-form or
the engine crashes. Do not re-derive this.

*The actual mechanism.* A call payload only ever reached a client as an
attribute on the span emitted for that exact selection — ONE `callpbv1.Call`
proto, base64'd into `dagger.io/dag.call` (core/telemetry.go:92-105). So the
walk needed every frame to have been independently spanned, and whole classes
never are: `LiteralID.pb()` flattens an embedded ID's entire DAG to a bare
digest reference; `AroundFunc` returns early for skipped/introspection/isMeta
frames and for digests the per-session span dedupe already spent; array
members are never selected. Sharpest of all, and measured rather than
reasoned: **loading an ID never re-selects the calls behind it** —
`Server.LoadType` serves handle IDs straight from the result cache and
`loadRecipeVertex` short-circuits the subtree on a digest hit
(dagql/server.go:1338, :1530-1539) — so an ID that enters a session from
OUTSIDE it (another client, a handed-over ID, anything already cached)
contributes zero spans, and every frame behind it is permanently unresolvable
to that client. That is what the live `withSkills` failure was.

*The fix, shipped.* Call payloads now also travel as OTel LOG records
(core/dag_call_telemetry.go producer, dagql/dagui/callpayloads.go consumer,
vocabulary in engine/telemetryattrs), modelled on the agent-state records that
already ride that channel. Emission is the TRANSITIVE CLOSURE of a call's ID,
minus digests already claimed from a session-wide seen-set
(`dagql.ShouldEmitCallPayload`) — and the span channel claims into that same
set, so logs only ever fill gaps rather than duplicating spans. The claim
key space is namespaced away from `ShouldEmitTelemetry`'s: sharing it would
suppress the span of every frame in a closure. `dagger.io/dag.call` is
untouched, so this is additive in both directions of version skew. Crucially
`extractIntoDAG` and `Span.CallID` needed NO change — that was the acceptance
criterion, and it held.

*Base64, not bytes, and why it looked tempting.* The log data model has a
Bytes kind, but bytes do not survive the first hop: `telemetry.LogValueToPB`
has no `KindBytes` case and silently encodes such a value as the string
`"INVALID"` (measured). The upstream fix, dagger/otel-go#16, has MERGED, but
this repo still pins `v1.43.1-0.20260515012101-af7cd0684887`, so until that
bump lands base64 remains a requirement rather than a preference.

*Still open, deliberately.* (1) **Array members** — `TestArrayMemberSubSelection`
(core/integration/callid_rebuild_test.go) is SKIPPED, and its comment carries
the measurement and the next move; the closure walk goes through
`ResultCall.RecipeID`, which an array-member receiver appears to defeat.
(2) ~~**Dedupe is session-wide but delivery is per-client**~~ RESOLVED: this
bit for real — a nested `dagger agent` attaching to a long-running session
(the tui-qa harness shape, and any CI nesting) could never rebuild worker
IDs, because shared frames (the bare `Query.llm` root every compose selects)
were claimed by clients whose delivery predated the new client's DB. Payload
claims are now scoped to the emitting client's delivery domain — the client
and its ancestors, exactly PubSub's fan-out set — via
`Query.CallPayloadSeenKeyStore` (engine/server `callPayloadDeliveryStore`),
so a later client's first closure walk re-publishes into its own domain. The
session-wide SPAN dedupe is unchanged. Pinned by
`TestCallPayloadDeliveryStore` (engine/server/session_test.go).
(3) `DB.CallPayloads` still has no pruning path.

*Tests that pin this.* `TestRosterAddressingWithSkills`
(core/integration/agent_roster_skills_test.go) — three cases by who built the
skills directory: client-built and module-built rebuild fine, and
directory-from-another-session is the one that failed and now passes. Note
this REFUTES the "reproduce through a module call" measurement this section
used to recommend: nested-session spans ARE forwarded, so a module-built
chain was never the problem.

**The Mode B regression test.** `TestRosterAddressingHostWorkspace` is
`TestRosterAddressing` plus a host-backed workspace bound via
`spawnOpts.wsID`, with the session pointed at a temp-dir git repo via
`dagger.WithWorkdir`, in three cases:

| case | workspace | before | after |
|---|---|---|---|
| session workspace | `currentWorkspace` | FAIL | pass |
| session workspace overlay | `currentWorkspace.withNewFile(…)` | FAIL | pass |
| host directory workspace | `host.directory(workdir).asWorkspace()` | pass | pass |
| baseline | none (`TestRosterAddressing`) | pass | pass |

The failure was `expected: "FAILED", actual: "IDLE"` on the rebuilt handle's
state — Mode B. `withNewFile` is the workspace edit verb
(core/schema/workspace.go:130). The nested-session skip does NOT trigger under
the engine-dev test container, which sets `_EXPERIMENTAL_DAGGER_RUNNER_HOST`
rather than `DAGGER_SESSION_PORT`, so these tests really run there.

It lives in `core/integration/agent_runtime_test.go`. The `known-broken:`
skips it used to carry are gone, along with the `broken` table column — the
assertions past the rebuild (`state`, the transcript marker, the QUEUED send)
were written as the CORRECT expectations precisely so that fixing the registry
would be a deletion, and it was. Everything before that seam — the rebuild
itself and the literal-derived identity (`name`, `instanceID`) — remains Mode
A coverage and always passed.

**One hazard for whoever picks this up.** Do not open the investigation by
calling a `modules/staff` tool on a resumed session (§10.1, item 13) — it
revives workers on receiver load, including reads. Item 14 used to be a second
hazard, misreporting this section's own commits as whole-file adds and making
a worker's commit unpullable; it is fixed, but an engine built before the fix
still shows it (§10.1).
