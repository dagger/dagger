# Recommendation: pure identity for spawned agents

Settles the defect behind `hack/designs/async-agents.md` §10.1 item 13, and
supersedes the exploratory notes that produced it (`forensics.md`, `api.md`,
`persist.md`, since deleted). Scope: what to change, why, and what is
deliberately left alone.

## 1. What actually happened

A chief hired 3 workers, the client was restarted, the user said "check on
them", and **33 agents started running**. Two defects compose.

**The recipe contains imperative calls.** `AutoSaveSession` persists
`LLM.portableID` (internal/cmd/dagger/llm.go), and `recipeSelectors`
(core/llm.go:2538-2550) bakes the chief's bound module object in as
`withTools(object: staff.spawn(…).spawn(…).spawn(…))`. Each `Staff.spawn`
is `@cache(policy: FunctionCachePolicy.Never)` — a call that composes,
starts and messages a live agent runtime.

**That recorded chain never converges.** `Query.currentWorkspace` is
`NotReplayable` + `PerSessionInput` (core/schema/workspace.go:35-40); on a
cross-session load the taint propagates upward (dagql/server.go:1439-1467)
and skips both cache lookups (`:1532-1539`, `:1602-1608`). That alone would
be a one-shot cost — a taint-forced execution *is* cached, and later loads
hit it. The multiplier is the **per-call nonce**: `@cache(Never)` appends
only `dagql.PerCallInput` (core/modfunc.go:132-133), and a taint-forced
vertex falls through to `preselect` → `resolveImplicitInputCallArgs`
(dagql/objects.go:566), which *re-resolves* implicit inputs instead of
replaying the recorded ones, minting a fresh `identity.NewID()` every time
(dagql/cache_inputs.go:72-77). Load *k* files its result under a key load
*k+1* cannot construct. Measured in `dagql/recipe_replay_test.go`: 5 loads
of one recorded ID → 5 executions, 5 distinct nonces, none the recorded one
— and every ordinary cacheable call recorded *above* the nonce re-executes
too, because its receiver differs each time.

**The load happens once per step, not once per resume.** `boundToolObject`
(core/llm_object_tools.go:123-170) memoizes the loaded object into the
step's transient MCP clone, while `step()` continues the lineage from the
pre-step value and only re-records a binding whose *ID* changed
(core/llm.go:1789-1808). A lazy load sets `.object`, never `.id`, so the
memo is discarded at every step boundary. Read-only tools return `String!`
and never rebind, so the stale pre-restart ID stayed bound for the whole
"check on them" phase: 7 dispatching steps, 14 tool calls, 11 loads × 3
spawns = 33. The mechanism is deterministic; 33 is a sample (concurrent
read-only calls race at `:145-150`, where the lock is released before
`srv.Load`).

Two claims in item 13 are measured false and should be corrected there: "each
resume adds one more loop" (off by an order of magnitude), and "the replayed
call ID is byte-identical, nonce included" (re-executions carry *fresh*
nonces).

## 2. The principle

> **Decouple creation from access.** An effectful call may *do* anything and
> *name* nothing: what gets recorded is the pure chain that reconstructs the
> state it produced, never the call that produced it.

This is not a new mechanism. `LLM.spawn` already does it by hand
(core/schema/llm.go:487-544): it mints an instance ID, then re-Selects the
pure `agent(id:, name:)` lookup so the *returned* ID is
`…llm!agent(id:"…", name:"…")` rather than `…llm!spawn(…)`. The bug is that
this mitigation stops at the core boundary — a module function wrapping
`spawn` has no equivalent, and gets the nonce *and* the multiplier with
nothing to protect it.

The fix is to make the same property hold one level up, using machinery that
already exists. **No new directive, no new engine mechanism, no loader
change.**

## 3. The change

### 3.1 Core: keep `spawn`, add its inverse

`LLM.spawn` already *is* the pattern, inside core: it mints an instance ID,
creates the agent, and returns a handle whose ID is the pure
`agent(id:, name:)` lookup (core/schema/llm.go:487-544). Nothing about it
needs to change, and the alternatives considered — exposing a `mint` verb, or
having the caller supply a UUID — are both worse: they hand out identity
before there is anything to identify, and neither buys anything the pin does
not already give.

What core lacks is the **inverse**: a way to take a persisted snapshot and
re-hydrate the instance it belongs to.

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

Called as `loadLLMFromID(<saved snapshot>).agent(id: <saved id>, name: <saved
name>).rehydrate(state: <saved state>)`. The two verbs are the same shape in
opposite directions: `spawn` is mint-create-pin, `rehydrate` is
adopt-create-pin.

This works because `AgentRuntimes.GetOrCreate` seeds `last` from the handle's
`Seed` and reads it **only when the entry is created** (core/agent.go:279-303)
— the property §10.2 ratified as load-bearing. Hand it a handle whose receiver
is the *final* snapshot rather than the *initial* seed and the entry starts
life holding the whole history.

**One further change, same principle.** `Send` routes through `GetOrCreate`
(core/agent.go:330-350), which makes a registry miss *generative*: §10.2
recorded a rebuilt handle booting a second loop from the seed on first `send`.
With re-hydration explicit, `Send` should use `Get` and error on a miss
("agent %q has no runtime in this session"). Signal-with-start survives — it
starts an entry that *exists* — and the one way to conjure an agent from a
seed becomes a verb someone actually typed.


### 3.2 Module: return a pure state setter, reached through `currentNode`

`Query.currentNode` returns the object that *received* the current module
function call — `fnCall.ParentTyped()`, threaded in core/modfunc.go:958-964,
resolved at core/schema/module.go:260-292. It is the receiver's own dagql ID
and **does not include the active call**. A lazy inline fragment narrows the
universal `Node!` to the module's own type, so the setter is reached through
a real API call whose identity is independent of the effectful call that
issued it:

```dang
let plumbing = ["chiefLine", "workerPrompt", "withMember", "withDismissed"]

# Pure state transitions. Public because they must be reachable through the
# API; excluded from the toolset because they are plumbing, not tools.
withMember(name: String!, agent: Agent!): Staff! {
  self.tombstones = tombstones.without(name)
  self.members = members.with(name, agent)
  self
}

withDismissed(name: String!): Staff! {
  self.tombstones = tombstones.with(name, member(name))
  self.members = members.without(name)
  self
}

spawn(chief: Agent!, source: Workspace!, name: String!, task: String!):
  Dagger.Staff! @cache(policy: FunctionCachePolicy.Never) {
  if (members.has(name)) { raise `staff member "${name}" already exists …` }
  let worker = source.agents(exclude: ["staff"]).compose
    .withTools(Dagger.staff.chiefLine(boss: chief), except: ["boss"])
    .withSystemPrompt(workerPrompt)
    .agent(id: UUID.string, name: name)
    .start
  worker.send(task)
  currentNode.{{... on Dagger.Staff!}}.withMember(name: name, agent: worker)
}

dismiss(name: String!): Dagger.Staff!
  @cache(policy: FunctionCachePolicy.Never) {
  member(name).stop
  currentNode.{{... on Dagger.Staff!}}.withDismissed(name: name)
}
```

The recorded binding becomes:

```text
staff!withMember(name: "scout1", agent: …llm!agent(id: "…", name: "scout1"))
     !withMember(name: "scout2", agent: …llm!agent(id: "…", name: "scout2"))
     !withMember(name: "scout3", agent: …llm!agent(id: "…", name: "scout3"))
```

Every frame is pure, nonce-free and cacheable. Re-loading it rebuilds the
`members` map with the same three handles and **creates nothing**. The
`@cache(Never)` nonce stays where it belongs — on an invocation nobody
records — so §1's cascade has nothing to attach to.

### 3.3 The same shape exists elsewhere; fix it as a pattern, not a one-off

`modules/staff` is not special. Every `@cache(Never)` module function
returning its own type has this defect, and several are bound into ordinary
sessions:

| module | function | what a replay re-runs |
|---|---|---|
| `modules/staff` | `spawn`, `dismiss` | hires and stops workers |
| `modules/engine-lab`, `.dagger/modules/engine-lab` | `start`, `restart` | builds and starts engines |
| `.dagger/modules/tui-qa` | `start` | builds a CLI, starts a TUI service |
| `.dagger/modules/mcp-lab` | `start` | starts an MCP session |

`engineLab.start` is the alarming one: a resumed session re-runs it per
dispatching step. Note that `tui-qa` and `engine-lab` already annotate
`stop: Dagger.TuiQa!` / `Dagger.EngineLab!` and return a self-call result,
so the `Dagger.<T>!` idiom is established in this repo — those returns just
reset to a fresh instance rather than preserving state.

## 4. Enforcement (optional, recommended)

The rule above is a convention, and conventions get forgotten once per
module. One narrow check makes it enforceable without adding a mechanism:
**at the single point where a recipe is persisted** (`recipeSelectors` /
`LLM.portableID`), refuse — or at minimum warn loudly — when a bound-object
chain contains a call whose field carries a per-call nonce. That is the
design doc's "honest stopgap" repurposed as a permanent invariant, at one
site rather than as a general capability. It converts the next occurrence
from silent duplicate work into a save-time failure naming the offending
field.

## 5. Deliberately not doing

**The loader.** Making a taint-forced execution identity-stable — replay the
recorded `cachePerCall` inputs, or memoize the forced execution per loading
session — is roughly a one-function change in `recipeLoadState`
(dagql/server.go). It is *mildly* interesting on its own terms: any
`@cache(Never)` module call under a cross-session recipe load re-executes per
load and invalidates every recorded call above it, which is a real cost even
where the call is harmless. But it is not needed here, and shipping it alone
would be worse than shipping nothing: it turns 33 loud duplicates into 3
quiet ones — three agents wearing the user's workers' names with none of
their history, which is precisely the failure item 13 records being
misdiagnosed as a cache defect for several sessions. One caveat if it is ever
picked up: `cachePerCall` may be replayed, `cachePerSession` must **not** be
— replaying a foreign session stamp is exactly what the taint exists to
prevent.

`dagql/recipe_replay_test.go` characterizes the current behaviour so the cost
stays measured and visible; it should be updated, not deleted, if the loader
ever changes.

## 6. Persistence: the roster goes in the save file

Not deferred to "load from trace". The save file grows an explicit roster
alongside `llm_id`, and restore re-hydrates it before anything else runs.

```jsonc
{
  "version": 2,                    // absent = v1, roster-less
  "name": "…", "model": "…", "created_at": "…",
  "llm_id": "…",                   // the chief's recipe, unchanged
  "agents": [
    {
      "instance_id": "m39gowtw3zfw4e71g5ta490jp",
      "name": "scout1",
      "state": "IDLE",             // as projected at save time
      "error": "",                 // loop error, when state was FAILED
      "snapshot": "…",             // portable recipe of rt.last
      "mailbox": ["…"]             // enqueued, not yet consumed
    }
  ]
}
```

Restore is `loadLLMFromID(snapshot).agent(id:, name:).rehydrate(state:,
error:)` per entry, then re-enqueue `mailbox`. This needs no schema beyond
§3.1's `rehydrate`.

### 6.1 Mixed states, and whether prompting wakes them

| saved state | restores as | prompt to wake? |
|---|---|---|
| `IDLE` | inert entry holding the full history | **yes** — a normal new turn |
| `RUNNING` | inert entry; the interrupted turn's input is still pending on the snapshot | **yes** — the send drains, then the loop re-steps the pending input and finishes the turn |
| `PAUSED` | inert entry, paused | **yes** via `staff.sendTo`, which resumes first (modules/staff/main.dang:130-133); a raw `Agent.send` only queues |
| `FAILED` | tombstone projecting FAILED, error preserved | **yes** — same resume-first path retries the loop from the committed prefix (§3.5 supervision-lite) |
| `STOPPED` | tombstone: snapshot readable, `send` errors | **no, deliberately** |

`RUNNING` works because the loop's own interrupt semantics already cover it:
`rt.last` is the last *committed* step, a partially-executed step is discarded,
and the loop re-steps pending input on resume (core/agent.go:902-906). That is
the honest shape — but note it means **`RUNNING` does not mean "kept
running"**. The loop died with the session; work the in-flight step had done
but not committed is gone. The roster must not redisplay a restored agent as
`RUNNING`, or it is lying in exactly the way §3.4 forbids.

`STOPPED` is the one place this design and "restore everything" disagree, and
it is worth choosing deliberately. §3.5 made stop terminal on purpose ("nobody
asks to restart a k8s UID"), and `modules/staff.dismiss` stops the worker — so
every dismissed worker is `STOPPED`, and resurrecting them on resume would
silently reverse a decision the user made. Restoring them as readable
tombstones is also what the harvest tools want: a dismissed worker's snapshot
and workspace stay reachable for a late `pullPending`. If continuing a stopped
worker's work is wanted, the honest spelling is a **re-hire** — a new instance
seeded from the tombstone's snapshot — not an un-stop.

### 6.2 Four hazards to design against

**The seed race, which is the dangerous one.** `GetOrCreate` reads the seed
only at creation. If *anything* addresses an instance before its `rehydrate`
runs — a staff tool dispatch loading the chief's binding, the roster UI, a
stray `send` — the entry is created from *that* handle's seed, which is the
agent's **initial** conversation. The saved snapshot is then silently
discarded and the worker comes back with no history: the amnesiac-twin failure
of §10.2, relocated rather than fixed. Two mitigations, both needed:
re-hydrate eagerly at session load, before the LLM is bound or any tool can
dispatch; and make `rehydrate` **error** when an entry already exists, so a
late restore is loud instead of a no-op.

**The workspace is re-derived, not restored.** A snapshot recipe carries
`withWorkspace(currentWorkspace…withNewFile…)`, and `currentWorkspace` is
`NotReplayable`, so on reload the base re-evaluates to the *new* session's
workspace with the worker's overlay replayed on top. A restored worker gets
"today's tree plus its old edits", not a byte-identical copy of what it had.
Usually what you want; occasionally surprising if the checkout moved
underneath. This belongs in the format's stated contract, not in a footnote.

**Size.** `testdata/after.json` is 300 KB for one conversation. A roster
multiplies that by the number of agents, with no structural sharing between
JSON fields, so a busy staff session could produce a multi-megabyte save file.
Measure before shipping; the obvious levers are persisting only agents the
chief still holds, and dropping tombstones older than some bound.

**Mailboxes.** Unconsumed messages and unresolved `AgentMessage` awaits die
with the session. Since "never drop a message" is the one thing `send`
promises (§8), the roster should carry pending mailbox entries rather than
document the loss — hence the `mailbox` field above. Awaiters cannot be
restored and do not need to be: `await` is idempotent against the entry, so a
caller re-awaits after reconnecting.

### 6.3 This does not replace the API fix

The roster restores the *agents*. It does nothing about the chief's recipe,
which still contains `withTools(object: staff.spawn(…)…)` and still
re-executes it on every dispatch. Without §3.2 a resumed session re-hydrates
three workers correctly and then spawns thirty-three more alongside them. The
two changes are independent and both required.

Nor does the roster foreclose "load from trace" — it front-runs it. The fields
worth saving are the same ones a trace-derived roster would carry, so the
format is the durable part and the store can change underneath it later.


## 7. Landing it safely

`modules/staff` provides the staff tooling used to drive sessions like this
one, so the edit can lock out its own author. Notes:

- A module is loaded at session start, so editing it does **not** affect a
  running session (§10.1). The change lands for the *next* session — verify
  with a fresh `dagger -m ./modules/staff` before trusting it, and expect to
  dogfood one version behind.
- The return annotation must be `Dagger.Staff!`, not `Staff!`. The self-call
  rule is the #1 Dang gotcha: annotating with the bare local type hands the
  runtime a raw ID string.
- `withMember` / `withDismissed` must be public — a `let` function is not in
  the schema, so `currentNode.{{… on Dagger.Staff!}}.withMember(…)` would not
  resolve — and must be added to `plumbing`, or they appear as chief tools.
- `@cache(Never)` **stays** on `spawn` and `dismiss`: the effects must still
  run on every invocation. Only the recorded identity changes.
- `withDismissed` should resolve the handle through `member(name)` so that
  dismissing an unknown name fails exactly as it does today.

Suggested order: §3.1 (core `rehydrate` + non-generative `send`) and §3.2
(staff setters) together, since both are needed before a resumed session is
trustworthy; then §6 (the roster in the save file), which depends on
`rehydrate`; then §3.3 as a sweep; then §4 as the guard that keeps it true.

