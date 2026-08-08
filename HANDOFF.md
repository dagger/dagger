# HANDOFF: async agents — ID-returning verbs + modules/staff

Continuation notes for the async-agents work. Orientation: read
`hack/designs/async-agents.md` first — §1-§8 design, §9 ratified semantics,
§11 implementation status (what's built, with file pointers, and the open
threads). Everything below is the IN-FLIGHT work: two tasks fully planned but
not yet written, plus session-specific tricks that are not in the design doc.

## State of the branch

Staged commits (oldest→newest): design doc (`882f98d`), ratifications
(`c270c45`), status section (`8a4697f`), then implementation:
`AgentMiddleware` rename (`8e0142b`), Agent runtime (`a84e6a6`), mailbox/send
(`cea0fb0`), pause/resume/interrupt (`a72c266`), integration tests
(`5a1e820`), message pinning (`e536ea5`), `Agent!` injection (`f663eca`), CLI
prompt-mode rewire (`70eec8f`). All green: `TestAgentRuntime` (14) +
`TestAgents` (16) via `engineLab_engineTest pkg ./core/integration`.

## Task A: make the imperative Agent verbs ID-returning (DO THIS FIRST)

Decision (from the user): `Agent.start`, `send`, `interrupt`, `pause`,
`resume`, `waitFor`, `stop` must return `ID!` with `@expectedType`, exactly
like `Service.start`/`Service.stop`/`sync`. Rationale: lazy clients (Dang)
force scalar-returning fields at the call site and re-hydrate the ID into an
object via the annotation — which eliminates the duplicate-send hazard of
re-forcing a lazy `DoNotCache` chain. This applies to ALL imperative APIs;
reads stay object-returning: `asAgent` (pure constructor), `snapshot`,
`message(id:)` (pure lookup).

- **Pattern**: core/schema/service.go:473-487 (`stop`) — resolver returns
  `dagql.Result[core.ServiceID]`; body ends with
  `id := dagql.NewID[*core.Service](parentID)` +
  `dagql.NewResultForCurrentCall(ctx, id)`.
- **Where**: core/schema/agent.go — resolvers `start` (:163), `send` (:174),
  `interrupt` (:224), `pause` (:241), `resume` (:258), `waitFor` (:287),
  `stop` (:306). For the self-returning six, return the parent's ID. For
  `send`, return the ID of the *pinned* result (the re-exec Select through
  `message(id:)` already in the resolver): `res.ID()` →
  `dagql.NewID[*core.AgentMessage](...)`. That ID replays the lookup, not the
  send — the pinning (design §9) is what makes ID-returning `send` safe.
- **`core.AgentMessageID`**: check core/ids.go; `AgentID` exists, the message
  alias may not.
- **Ripples**:
  - core/integration/agent_runtime_test.go: `agentHandle` (:62-182) selects
    sub-fields off verbs (`start { state }`, `send(...) { delivery await }`,
    `waitFor(state: X) { state }`) — all become two-step: verb returns an ID
    string. Prefer rewriting these helpers onto the generated Go SDK, which
    handles the ID-returning verbs natively (like `Service.Start`). Where raw
    GraphQL is unavoidable, re-hydrate with the standard `node(id:)` field —
    NOT `loadAgentFromID`/`loadAgentMessageFromID`, which are hard-deprecated
    (or keep reading `state` as a separate selection in the same query —
    `state` is still a normal field). The idempotency test's aliased awaits
    move onto the loaded message — still valid thanks to pinning.
  - core/integration/agent_injection_test.go: uses the same helpers.
  - core/integration/testdata/modules/go/agent-poker/main.go: SDK `Send`
    becomes a forcing `(ctx) (*AgentMessage, error)`-style call after
    codegen (mirror how generated `Service.Start` looks).
  - internal/cmd/dagger/llm.go: the prompt-mode rewire calls
    `Send/Resume/Interrupt/Stop/WaitFor` via the typed SDK; signatures change
    (mostly get simpler — they become forcing calls).
  - Codegen: `generate` with ["go-client:generate"], then ["docs:references"].
- **Aftercare**: add a §9 ratification bullet (imperative APIs are
  ID-returning, sync-style, so lazy clients force at call time) and touch up
  §3.1/§11 where the object-returning spelling appears.

## Task B: modules/staff (write directly; do NOT register it)

A tailcall-style async orchestration module, the async sibling of
`modules/delegate`. The previous attempt failed because a sub-agent
REGISTERED a syntactically broken module, poisoning workspace load — so:
**do not add staff to dagger.toml** (registration there is the only discovery
mechanism; nothing auto-loads). Load it explicitly: `dagger -m ./modules/staff`.

Files: `modules/staff/main.dang` + `modules/staff/dagger.json` (copy
delegate's: name staff, engineVersion v1.0.0-0, sdk dang). Syntax: mimic
`modules/delegate/main.dang` and `modules/editor/main.dang`; the
`dang-language` skill (ReadSkill) has the reference.

Design (all points confirmed with the user):

- `type Staff` holds `let members: [Agent!]! = []`; state evolves via tools
  returning `Staff!` (`self.members += [worker]; self` — the editor pattern).
  Engine-object handles in Dang state "just work" (serialized as IDs).
- Chief tools (bound via `agent(base: LLM!): LLM! @agent { base
  .withTools(currentNode).withSystemPrompt(chiefPrompt) }`):
  - `spawn(chief: Agent!, name: String!, task: String!, source: Workspace!,
    model: String = null): Staff!` — `chief` and `source` auto-inject and are
    hidden from the tool schema. Compose the worker:
    `source.agents.compose(base: llm(model: model).withWorkspace(source))`
    when model set, else bare `source.agents.compose` (confirmed correct).
    Then neuter: `.withTools(Dagger.staff, except: [<all orchestration
    tools>])` — the delegate self-reference trick; confirmed it should work
    even when staff isn't installed in the workspace (harmless extra binding
    then). Then the ask channel (below), then workerPrompt, then
    `.asAgent(name)` and a fire-and-forget `send(task)`. Reject duplicate
    names.
  - `sendTo(name, message): String!` — resume-first (see below), send, return
    delivery evidence.
  - `ask(name, message): String!` — resume-first, `send(message).await`.
  - `status: String!` — one line per member: name + `toString(state)`
    (`toString` on enums works; `AgentState.IDLE` is the arg syntax).
  - `read(name, last: Int! = 10): String!` — bounded projection over
    `snapshot.messages.{{role, content.{{kind, text}}}}`: TEXT blocks only
    (tool results are TOOL_RESULT-kind, so they drop out naturally), tagged
    by role, `takeLast(last)`.
  - `collect(name): String!` —
    `waitFor(state: AgentState.IDLE).snapshot.lastReply`. Docstring warning:
    a FAILED worker never goes idle on its own; check status first.
  - `interruptWorker(name): String!` — parks PAUSED, prefix kept; result text
    should say sendTo/ask resumes it.
  - `dismiss(name): Staff!` — stop, remove from members.
  - Private `member(name): Agent!` helper: filter, raise listing current
    member names on miss.
- **askChief (child→parent)**: separate `type ChiefLine { boss: Agent!
  askChief(question: String!): String! { boss.send(question).await } }`.
  CRITICAL: `withTools(ChiefLine(chief))` does NOT work — locally constructed
  Dang objects have no engine identity. Mint it via a **self-call**: give
  Staff a method `chiefLine(boss: Agent!): ChiefLine!` and bind
  `.withTools(Dagger.staff.chiefLine(boss: chief), except: ["boss"])` — the
  result of a real module call has a dagql ID. (`currentNode` exists too if
  a receiver-side handle helps.)
  askChief does NOT auto-resume the chief: a paused chief was paused
  deliberately; the question queues (QUEUED) and is answered on resume.
- **Resume-first idiom** in sendTo/ask: `member(name).resume` before the
  send, so steering a worker parked by interruptWorker actually drains.
  After Task A, `resume` is scalar-under-the-hood: a bare statement forces
  it (confirmed: scalar-returning fields force at the call site; that same
  property is why Task A exists).
- **Prompts** (mirror delegate's structure/tone):
  - Chief: tool guidance; workers' questions arrive as ordinary messages in
    YOUR conversation — your turn-end reply is what the asking worker
    receives; DEADLOCK warning — engine-side guards don't exist yet (design
    §11 thread 2), so a blocking `ask` of a worker that might `askChief` can
    cycle-deadlock; prefer sendTo + collect for question-prone workers.
  - Worker: autonomy; askChief blocks you until the chief's turn ends and
    puts your question on the chief's record — sparingly, and never when the
    chief is likely blocked waiting on you; supersede any chief-prompt text
    picked up via workspace compose (same prompt-leak delegate has — its
    header TODO documents it; live with it).

## Task C: staff E2E integration test

New suite in core/integration (StaffSuite / staff_test.go), reusing the
existing machinery in agent_runtime_test.go:

- `cannedReplayModel` builds recordings through the LLM API
  (WithPrompt/WithResponse/WithToolResult) and exports to
  `replay/<base64>`. KEY ENABLER: **the replayer excludes tool results from
  history matching** (agent_injection_test.go:69-72 — record placeholder
  `WithToolResult("call_1", "", false)`; the live tool's real result flows
  through). Matching compares stabilized TextContent + role per message and
  skips the leading system prompt (core/llm_replay.go:81-100).
- Serving the module: modules/staff is OUTSIDE testdata;
  `copyTestdataFixture` (core/integration/module_helpers_test.go:167) wraps
  `fscopy.Copy` from `testDataPath` — write a sibling helper that copies from
  the repo-root `../../modules/staff`, then `c.ModuleSource(dir).AsModule()
  .Serve(ctx)` and query `{ staff { id } }` for the withTools ID (the
  servePokerModule shape, agent_injection_test.go:36-49).
- **De-race for askChief (user-approved plan)**: make the chief's spawn turn
  dwell in a slow tool AFTER the spawn call, so the worker's question
  deterministically STEERS into the chief's open turn, and the worker's
  await resolves with that turn's closing reply (tailcall's absorbed-steer).
  Slow-tool + cache-volume marker pattern: agent_runtime_test.go:190-210.
  - Chief recording: prompt → TOOL_CALL spawn{name:"w1", task, model:
    <worker replay model>} → placeholder result → TOOL_CALL stdout (slow,
    6s) → placeholder → the worker's question as a drained user prompt →
    closing reply containing the answer.
  - Worker recording: task prompt → TOOL_CALL askChief{question} →
    placeholder → final reply referencing the answer.
  - Then a second chief turn: prompt → TOOL_CALL collect{name:"w1"} →
    placeholder → closing reply; assert the worker's final reply appears in
    the chief's transcript (transcript includes tool results — the
    injection test asserts `delivery: STEERED` from it the same way).
- Cheap gates before the E2E: `dagger -m /src/modules/staff functions` via
  engineLab (parse gate — this is what the failed sub-agent never passed);
  direct `call status` (no injection needed → works, "(none)"); direct
  `call spawn` (errors with "function requires the calling agent" — the
  negative check).

## Tooling notes for the fresh session

- `engineLab_start` boots the from-source engine; its client container
  mounts the workspace at /src (`engineLab_dagger` for CLI commands,
  `engineLab_query` for raw GraphQL). `engineLab_engineTest pkg
  ./core/integration run TestAgentRuntime` for the suites. Keyless model:
  `replay/<base64-of-messages-JSON>`.
- Multi-statement stateful scenarios need ONE session (the runtime registry
  is per-session): `dagger shell -c` scripts work; separate
  `engineLab_query` calls do not share agents.
- Commit style: scoped commits, one logical change each. If delegateEdits is
  ever used again: it has repeatedly leaked empty `go/pkg/mod/` dirs into the
  changeset (`rm go` before committing), and docs .mdx `sidebar_position`
  churn from docs:references is normal.
- After both tasks land: update design-doc §9 (ID-returning ratification) and
  §11 (mark thread 7, the orchestration module, done; note the staff test).
- Delete this HANDOFF.md in the wrap-up commit.
