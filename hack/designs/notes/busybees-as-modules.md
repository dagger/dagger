# busybees as Dagger modules

Angle 1 of 3: how much of [kpenfound/busybees](https://github.com/kpenfound/busybees)
(`bees`, a GitHub-driven software factory) could be rebuilt as Dagger modules
composed into `dagger agent`, in the style of
[vito/agents](https://github.com/vito/agents). Grounded in busybees@635ab62,
vito/agents@7e66557, and this checkout's `core/schema/agent.go`,
`core/schema/llm.go` and `hack/designs/{async-agents,agent-messaging,workspace-agents,trace-native-agent-resume}.md`.

**Short answer.** The *agentic* half of busybees — roles, the
developer↔reviewer loop, best-of-N with an assembler, the review pipeline,
role-scoped tools, inter-role messages, sandboxing, worktrees — maps onto the
Agent runtime and the `staff`/`delegate`/`committer`/`contributor` modules
almost one-to-one, and several busybees subsystems simply disappear (worktree
management, `bees kill`, PID files, three sandbox modes, per-backend MCP
plumbing). The *operational* half — a poll loop that runs for weeks, durable
bookkeeping, cost budgets with teeth, crash recovery, a multi-project daemon —
is where the current API runs out. The binding constraint is one sentence in
async-agents.md §3: the agent runtime table is **session-scoped**.

## 0. What busybees is, structurally

- **Scheduler** (`internal/scheduler/scheduler.go:313` `Run`): tick loop;
  *full pass* = `gh issue/pr list` → deliver human comments as mail → merge
  state → `reconcile` labels (`:1030`) → pauses (budget, rate limit, manual)
  → `dispatchDevelopers` (`:1207`) → `dispatchSingletons` (`:1405`); *local
  pass* re-runs reconcile/dispatch on a cached poll whenever a session ends
  (docs/architecture.md "Waking up"). Deterministic Go, not an LLM.
- **Developer worker** (`internal/scheduler/developer.go:52` `workIssue`): one
  goroutine per issue; a persisted stage machine
  `develop → (checks) → review → … → approved`, stage written to
  `issues/work-<hash>.json` before each stage so a restart resumes
  (`resumeStage`, `:701`); per-issue budget checked between stages (`:206`);
  best-of-N/MoE fan-out then an assembler session (`bestofn.go`).
- **Sessions**: every role run is a *fresh* headless CLI process
  (claude/codex/opencode/pi) in a temporary git worktree, with a role system
  prompt (`internal/prompts/system/*.md`) and the built-in `bees` MCP server.
  "A role remembers nothing between sessions beyond its notes file and what is
  visible on GitHub" (docs/roles.md).
- **Mailbox** (`internal/mail/mail.go`): JSON files under
  `<state_dir>/mail/<to-role>/`, **addressed to a role, not a session**,
  tagged with a work key; unread mail is rendered into the *next* session's
  task prompt and marked read when it ends.
- **MCP tools** (`internal/mcpserver/`): 11 common (`mail_send`, `mail_list`,
  `issue_create`, `issue_link`, `issue_view`, `pr_view`, `comment`,
  `report_factory_error`, `notes_read`, `notes_write`, `done`) + role-scoped
  policy tools (`issue_set_state` only moves out of triage, `submit_review`,
  `file_bug` with duplicate check, `release_ship`, …). `done(status)`
  (`done.go`) is the structured outcome the scheduler branches on.
- **State dir**: mail, notes, sessions, per-issue bookkeeping, `ledger.jsonl`
  (one line per session: `cost_usd`, turns, outcome), `status.json`.

## 1. Feature-by-feature mapping

Legend: **(a)** exists today in vito/agents or the core Agent runtime;
**(b)** implementable as a Dang module on the current API; **(c)** needs a new
engine primitive; **(d)** host concern, not a module.

| busybees capability | | Dagger equivalent / gap |
|---|---|---|
| Role session = fresh headless agent with role prompt | a | `llm.withWorkspace(ws).compose(expertise).withSystemPrompt(p).spawn(name)` — exactly `Staff.workerBase` + `spawn` (staff/main.dang:70,115) |
| Per-role model / profile | a | `staff.spawn(model:)` → `LLM.withModel` |
| claude / codex backends | a* | `claude`/`codex` modules' `base.withHarness(kind: CLAUDE\|CODEX)`; *engine side is draft PR #14196 (`withHarness` is not in this checkout)* |
| opencode / pi backends | c | `LLMHarnessKind` has only CLAUDE/CODEX; new harness kinds needed — or drop them, since native `llm` covers the providers |
| Fallback profile on usage limit | b | chief/factory sees the FAILED event (`Agent.notify`), `reseed`s or respawns with another `model`; error text classification in Dang |
| Git worktree per session | a | each worker gets its own RW Workspace copy (staff spawn); per-branch work via `GitRef.asWorkspace` (contributor `checkout`, contributor/main.dang:899); no cleanup, no `git worktree prune` |
| Push branch / open PR | a | committer `push` (committer/main.dang:646), contributor `gh pr create` |
| Sandbox modes (host confine, container, sbx) | a | every tool already runs in an engine container; credentials enter only as `Secret`s (contributor `withSecretVariable`) |
| Built-in `bees` MCP server | a/b | object tools via `withTools` replace MCP; or *as a stopgap* `LLM.withMCPServer(name: "bees", service:)` (core/schema/llm.go:222) to reuse `bees mcp serve` unchanged |
| Common tools: `issue_view`, `pr_view`, `issue_link` | a | contributor `gh` |
| `comment` with `<!-- bees:<role> -->` marker | b | role-parameterized wrapper over contributor's `comment` (which enforces a `:robot:` prefix instead) |
| Role-scoped policy tools (`issue_set_state`, `issue_question`, `file_bug`, `submit_review`, `release_ship`) | b | one Dang type per role, bound with `withTools` — the `PullTools`/`ChiefLine` narrowing pattern (staff/main.dang:971,1078). Requires *not* handing roles contributor's raw `gh` (see §2.3) |
| `done(status, note)` structured outcome | b | role tool that `boss.send`s a tagged outcome line to the orchestrator agent (askChief pattern) and/or records it in the role object's state; IDLE event already carries the final reply |
| Mailbox between roles | a/b | live agents: `Agent.send(message, replyTo:)` with engine-stamped attribution (agent-messaging.md §4.1–4.2). Role-addressed mail for roles with *no live agent*: a small inbox map in the factory's state (§2.2) |
| Human comments → mail to the right role | b | factory poll diffs comments since a clock, `send`s to the issue's live agent as a `human`-tagged message |
| Wake on session end (local pass) | a | `Agent.notify(subscriber:, on: [IDLE, FAILED])` — events, not polling |
| Wake on a timer (poll interval, QA interval, PM interval) | **c** | nothing wakes an idle agent or a module on a clock. Primitive: timer events (§3.1) |
| Label state machine / reconcile | b | deterministic Dang over `gh issue list --json` (contributor's toolbox container); pure function of the poll |
| Dispatch order (priority, size, blockers, stacked PRs, `max_large_in_flight`) | b | Dang sort/filter; pool = `members` count in factory state |
| Developer stage machine develop→review→checks | b | a per-issue `Worker` record in factory state; transitions driven by outcome events; stage re-derivable from GitHub (labels + PR + checks) |
| Pre-review checks / checks mode | b | `gh pr checks --json` in a container; wait = re-check on next tick (or a blocking exec with timeout) |
| Best-of-N / mixture of experts + assembler | a | N `staff.spawn`s on the same task (MoE: different prompt/model each); assembler = a worker that `pull`/`pullConflicted`s candidates (PullTools) — strictly nicer than branch juggling; no attempt branches to delete |
| Review pipeline brief → angles → judge | b | `delegate`-style read-only sub-agents; judge is deterministic code (docs/roles.md: "the judge, deterministic code and not a session") → a Dang reducer (§2.5) |
| `bees review <pr>` standalone + triage TUI | b/d | pipeline is (b); the interactive select/dismiss/defer screen (`internal/reviewtui`) is (d), or a chief prompt flow |
| Notes per role (`notes_read/write`, consolidation every N sessions) | b | a `Notes` object over a dedicated git branch (§2.6); consolidation trigger = session counter in state |
| Cost ledger | b (partial) | `agent.snapshot.tokenUsage` gives input/output/cache-read/cache-write tokens (core/llm.go:243); **no USD** — module carries a price table keyed by `snapshot.model` |
| Daily / per-issue budget (checked before dispatch / between stages) | b | factory sums its ledger before spawning; per-issue between stages |
| Per-session budget / timeout (hard stop mid-turn) | **c** | `LLM.loop(maxSteps:, maxTokens:)` exists, but `spawn` has no caps and nothing fires *during* a turn. Primitive: spend/step caps on the agent entry (§3.4) |
| Claude account session-limit pause | c/d | provider rate-limit state is engine/host knowledge; a FAILED-with-rate-limit event classification is (b), a factory-wide pause flag is (b), knowing the *reset time* needs the provider error surfaced structurally (c) |
| GitHub App identity + scoped installation tokens | b | Dang: JWT from a `Secret` private key in a container, mint token, pass as `Secret` to role tools |
| `report_factory_error` → filed upstream | b | a tool that appends to factory state; tick files it with `gh` |
| Durable bookkeeping (`issues/work-*.json`, clocks, `<role>.json`) | **c**/b | module state dies with the session unless restored from trace; GitHub-derivable fields are (b); the rest needs durable storage (§3.2) |
| Crash recovery (`bees kill`, orphan PIDs/containers, interrupted markers) | a/c | orphans cannot exist — the engine owns every container and the session owns every agent. *Resuming* work is (c): trace restore deliberately doesn't continue execution (trace-native-agent-resume.md §3.1–3.2) |
| Live status / roster | a | the TUI multi-agent roster (async-agents.md §5.1), `staff.status/read` |
| Queues / Needs-human / Approved-PRs panels | b/d | a `status` tool rendering factory state is (b); bespoke panels are (d) |
| Multi-project daemon, `-d`, SIGHUP reload, shared developer pool | d | process management |
| Work hours / off-hours polling | b (given 3.1) | arithmetic over the timer |
| Evals (`bees eval` with in-memory fake GitHub) | b | `@check` functions running the factory module against a fixture repo + a fake-`gh` `Service`; grading = the fixture's test in a container |
| `bees.toml` config, versioned migrations | b/d | module constructor args + `dagger.toml` settings (contributor's `githubToken`/`remote` pattern); migrations largely vanish |
| State-dir schema migration | d (moot) | no state dir |

Tally over 40 rows: 10 (a), 18 (b), 3 strictly (c) plus 3 partly (c)
(rate-limit reset, durable bookkeeping, resume), 2 (d), the rest mixed.

## 2. Module designs

### 2.1 `factory` — the scheduler as a module

The scheduler must be **code, not a chief prompt**: busybees' reconcile and
dispatch are deterministic, and paying a model to read label lists every five
minutes is waste. So `factory` is a Dang type whose state holds the live
roster and bookkeeping, and whose `tick` is a pure-ish function of (state,
GitHub poll):

```dang
type Factory {
  let workers: Map[Agent!]! = [:]           # "dev/42", "pm", "qa", "review/pr-57"
  let issues:  Map[IssueState!]! = [:]      # stage, round, branch, clocks, spend
  let inbox:   Map[[Mail!]!]! = [:]         # role-addressed mail for roles with no live agent
  let ledger:  [LedgerEntry!]! = []
  let paused:  String! = ""                 # "", "budget", "rate-limit", "manual"
  let compositionSource: Workspace = null

  new(githubToken: Secret!, repo: String!, maxDevelopers: Int! = 1,
      maxCostPerDay: Float = null, models: RoleModels = null) { ... }

  agent(base: LLM!): LLM! @agent   # optional: a human-facing "foreman" chief
  tick(source: Workspace!, full: Boolean! = true): Factory! @cache(policy: Never)
  status: String! @cache(policy: Never)
  withWorker(key: String!, worker: Agent!): Factory!   # plumbing, as in staff
}
```

`tick` = poll (`gh issue list/pr list --json` in contributor's toolbox
container, nonce-busted) → reconcile labels → settle finished workers (read
`state`, `snapshot.lastReply`, `snapshot.tokenUsage`) → ledger + budgets →
dispatch: for each candidate, `spawn` a role agent and store it with
`currentNode.{{... on Dagger.Factory!}}.withWorker(key, agent)`. That
`withWorker` split is load-bearing: staff/main.dang:169–178 explains that
storing via a separate pure call is what keeps a replayed ID from re-spawning
every agent (the 33-agent incident in `notes/recommendation.md`). Every
side-effecting function is `@cache(policy: Never)`, and container execs carry
a `Random.string` nonce (contributor/main.dang:294).

Who calls `tick`? Three options, in order of what works today:

1. **A long-running driver session**: a thin host program (Go SDK) or
   `dagger call factory run` that loops `f = f.tick(...)` and sleeps. All
   spawned agents live in that one session, so they persist between ticks.
   Local passes come free by polling `Agent.state` (no GitHub cost) on a
   short interval and doing a full poll every `pollInterval`. This is
   busybees' own full/local split.
2. **A foreman chief** in `dagger agent` holding `Factory` as its tool object,
   with workers' lifecycle events subscribed to *the chief*
   (`worker.notify(subscriber: chief)`, staff/main.dang:165) — each event
   wakes the chief, which calls `tick(full: false)`. Event-driven, but every
   wake costs a model turn, and it still cannot wake on a clock (§3.1).
3. **Per-tick sessions** (cron/CI calls `factory tick`): only viable if
   agents and bookkeeping outlive the session — they don't (§3.2).

Recommended: (1) for the factory, with (2) as an optional human-facing layer
for steering ("pause", "what's dev/42 doing?", "re-run QA").

### 2.2 `mailbox` — mostly unnecessary

Agent.send already provides what busybees built by hand, and more: ordered,
never-dropped delivery; engine-stamped attribution headers
(`[message #N from agent …]`); `replyTo` pairing; steering a running turn
rather than waiting for the next session (agent-messaging.md §4.1–4.3,
core/schema/agent.go:74). busybees' `InReplyTo` and `From`/`To` fields are the
same idea, minus delivery.

The one semantic gap: busybees mail is **role-addressed and outlives the
session** — the PM's question to "project_manager" is read by whichever PM
session runs next. With Agent.send you need a live instance. Two policies:

- **Long-lived role agents** (one PM, one QA, one dev agent per issue for the
  issue's life). Mail becomes `send`. This also recovers what busybees
  explicitly lacks — memory across rounds — but conversations grow; use
  `reseed` (core/schema/agent.go:150) with a compacted conversation, or
  `context`-style blocks, at round boundaries.
- **Ephemeral role agents** (busybees' model: fresh per session). Keep a
  `Map[[Mail!]!]` inbox in factory state; a role's tools get `mailSend(to,
  issue, body)` that either `send`s to the live recipient or parks it; the
  next spawn renders parked mail into its task, exactly as busybees does.

Recommendation: long-lived per-issue developer agents (the review loop is one
conversation; busybees already resumes the developer's CLI session across
rounds, developer.go:127–136), ephemeral singletons with the parked inbox. The
inbox is ~40 lines of Dang, not a module.

### 2.3 `roles` — prompts + narrowed toolsets

Each role = (system prompt, model, tool objects). busybees' prompts port
nearly verbatim (they're `text/template`; Dang string interpolation covers
it). The hard requirement is **tool narrowing**: busybees refuses tools a
role isn't offered and tells it not to fall back to `gh`. Staff's `workerBase`
composes *every* workspace Expertise except staff (`withoutUri(uri:
"dag://staff/**")`, staff/main.dang:78–81), which would hand a QA bee
contributor's unrestricted `gh`. A role factory instead whitelists:
`origin.artifacts.filterTypes(["Expertise"]).filterUri(...)` (both exist,
core/schema/artifacts.go:74,79) — e.g. editor + committer only — then binds
role objects:

```dang
type ProjectManagerTools {
  let gh: Secret!  let repo: String!  let boss: Agent!
  issueView(number: Int!): String! @cache(policy: Never)
  issueCreate(title: String!, body: String!, parent: Int = null, size: String!): String! @cache(policy: Never)
  issueSetState(number: Int!, to: String!): String! @cache(policy: Never)   # refuses unless currently bees:triage
  comment(number: Int!, body: String!): String! @cache(policy: Never)       # appends <!-- bees:project_manager -->
  mailSend(to: String!, issue: Int!, body: String!): String! @cache(policy: Never)
  done(status: String!, note: String! = ""): String! @cache(policy: Never)  # boss.send("[outcome] …")
}
```

Bound as `base.withTools(Dagger.factory.pmTools(...), except: ["boss","gh"])`
— the `ChiefLine` trick (a real self-call so the object has engine identity;
private handles stay out of the schema, staff/main.dang:150–155). One type
per role; shared helpers as top-level Dang functions.

### 2.4 Developer worker and best-of-N

Per issue, the factory spawns `dev/<n>` with a workspace from
`GitRef.asWorkspace` on `bees/issue-<n>` (or the stack predecessor's branch),
committer + editor + checks as tools, and `DeveloperTools` (`done`,
`mailSend`, `comment`). Its outcome event (`pr-opened`) moves the
`IssueState` to `review`; the factory spawns or messages `review/<n>`; a
`changes-requested` outcome is forwarded with `dev.send(findings)` — the
developer keeps its conversation, no re-reading the issue.

Best-of-N is where Dagger is clearly better than worktrees: spawn
`dev/<n>/a1..aN` on the same task; on all-IDLE, spawn `dev/<n>` as the
assembler with a PullTools roster over the attempts — `logOf`/`diffOf` to
compare, `pull` to take one, `pullConflicted` to synthesize. No attempt
branches pushed, no stray PRs to close, slots are just roster entries. The
staff roster snapshot rule ("hire producers before consumers",
staff/main.dang:877–882) is exactly the right order here.

### 2.5 `review` — brief / angles / judge

```dang
type Review {
  review(source: Workspace!, pr: Int!, size: String = null): String! @cache(policy: Never)
}
```

1. **Brief**: one read-only sub-agent — `llm.withWorkspace(prTree)
   .withTools(ReadOnlyFiles(...)).withPrompt(briefPrompt).loop.lastReply` —
   in delegate's shape (delegate/main.dang:66–79) but with *no* composed
   expertise (busybees' brief/angle sessions get no MCP, no writes, no
   network, roles.md "Review pipeline").
2. **Angles**: `spawn` one agent per angle for the size, `send` each its
   task, then `wait` each. Spawned agents run concurrently; the module waits
   sequentially. This is a blocking wait inside a tool call — legal (no cycle,
   agent-messaging.md §4.5) but it holds the calling turn. Ask for findings in
   a fixed line format.
3. **Judge**: deterministic Dang: parse, dedupe (word-overlap like
   `internal/duplicates`), sort by severity, and filter by reviewer notes.
4. Post with `gh api …/reviews` (one review, inline comments), or return the
   list to a reviewer agent that decides the verdict, as busybees' judge
   *session* does.

Every step's `tokenUsage` goes into the ledger under the round.

### 2.6 `notes` — per-role memory

Options: (i) `context` blocks — session-only, and "State survives
COMPACTION, not RECOMPOSITION" (context/main.dang:38); (ii) a file in the
product repo — busybees deliberately keeps notes out of the product tree;
(iii) **an orphan git branch** (`bees-notes`) holding `<role>.md`, read via
`GitRef.tree`, written via `Workspace.withCommit` + `push` (committer). (iii)
is durable, diffable, human-editable on GitHub (busybees' "edit the notes file
to steer a role" works as a web edit), and reuses GitHub as the store. A
`Notes` object exposes `notesRead`/`notesWrite(text)` to every role; the
factory counts sessions per role for the consolidation prompt.

### 2.7 `ledger` and budgets

`LLM.tokenUsage` is cumulative over the conversation (core/schema/llm.go:308),
so a session's spend is `snapshot.tokenUsage` at settle minus at spawn (or
total, for ephemeral agents). Multiply by a per-model price table in the
module — the API exposes no USD. Daily and per-issue budgets work as in
busybees (checked before dispatch and between stages). Per-session hard caps
don't (§3.4). Open: whether `withHarness` conversations (claude/codex CLIs)
report `tokenUsage` at all; busybees reads `cost_usd` straight from claude's
result JSON, and the harness path would need to forward it.

## 3. The hard parts

### 3.1 Time: nothing wakes on a clock

Agent events come only from agent lifecycle transitions
(`Agent.notify`, core/schema/agent.go:129). busybees needs four clocks:
GitHub poll (5m), PM interval (1h), QA interval (30m), backoff timers. Inside a
module there is no sleep but a nonce-busted `sleep` exec, and no `select`
over "timer or agent settled" — the combinator for non-model orchestrator
code is still open (async-agents.md §7, agent-messaging.md §4.3). A driver
loop (§2.1 option 1) gets around both by polling `state`.

**Primitive (c): timer events.** E.g. `Agent.schedule(message: String!,
after: String!, every: String = null): ID!` — the engine enqueues an
event-origin message on the agent's clock, through the same mailbox path as
`notify`. That makes option 2 (a pure `dagger agent` foreman) viable without
a host loop. GitHub webhooks would be the second event source (a `Service`
receiving webhooks that `send`s to the foreman), but that is (b) once
services can hold agent handles, and polling stays the fallback.

### 3.2 Lifetime: the factory outlives any session; agents don't

busybees runs for days under `bees run -d`. Here, agents, their mailboxes and
the factory's module state live in a **session-scoped runtime table**
(async-agents.md §3; §4.1 "Lifetime" is listed as an unsettled boundary).
Trace-native restore (`dagger agent -r <trace>`) brings back conversations,
workspaces and notification subscriptions, but by contract "does not
automatically spend model tokens or continue interrupted execution",
"crash-complete recovery… [is] deliberately not claimed", and pending mailbox
messages are outside the guarantee (trace-native-agent-resume.md §3.1–3.2).
That is right for an interactive chief and insufficient for a factory.

Two primitives would close it:

- **(c) Detached sessions** — a session (or an agent set) that stays up after
  the launching client detaches, reattachable by id: `dagger agent --detach`
  / `dagger attach <session>`. The engine already runs the loops on detached
  contexts; what's missing is the session not ending with its client. This is
  the `bees run -d` equivalent.
- **(c) Durable module state** — a keyed store that outlives sessions for
  small records (ledger lines, clocks, inbox), e.g. `Query.store(namespace:)`
  with get/put/append. `CacheVolume` is the closest existing thing but is
  prunable cache, not state. Without it, push everything that must survive to
  GitHub (labels, a hidden marker comment per issue, the notes branch).

### 3.3 Git worktrees vs Workspace copies

This is the strongest fit. Every worker's Workspace is a private copy
(staff spawn) or a fresh `GitRef.asWorkspace` checkout; edits are overlays
until `withCommit`/`push`. busybees' `internal/workspace`, `core/vcs`,
`ws.Prune`, attempt-branch deletion and the `bees kill` worktree sweep all
disappear. Two caveats: (1) a staff-style harvest reads the worker's *last
committed step* (staff/main.dang:713–729, the `sync` pinning); orchestration
must act on IDLE events, not mid-turn. (2) The branch on GitHub, not the
Workspace, is the durable artifact — a dev agent must push before reporting
`pr-opened`, which busybees' prompt already requires.

### 3.4 Budgets with teeth

Between-turn budgets are module code. busybees' `max_cost_per_session` and
role `timeout` stop a session *mid-run*; the factory can only observe an
agent at settle time unless it polls `snapshot.tokenUsage` (DoNotCache) and
calls `pause(interrupt: true)` — polling again. **Primitive (c):** caps on
the agent entry — `spawn(maxSteps:, maxTokens:, deadline:)` — enforced at
step boundaries, settling the agent as FAILED with a typed reason that the
`notify` event carries. `LLM.loop(maxSteps:)` (core/schema/llm.go:247)
already has the step-count half for synchronous loops.

### 3.5 GitHub as the state machine vs module state

Keep busybees' rule: **GitHub is the source of truth** for anything a person
sees or edits (labels, PRs, sub-issues, milestones). Module state is a
cache plus soft bookkeeping. This matters more here than in busybees because
module state is less durable than busybees' state dir. Concretely:
`stage` is re-derived from labels + PR + checks on every full poll
(busybees' `resumeStage` already does this, developer.go:701, with the state
file as a tiebreaker); `human_seen_at` clocks can be the newest
bee-marked comment's timestamp; per-issue spend can live in a hidden marker
comment. What's left in module state is reconstructible: a cold start costs
some redundant work, not correctness.

### 3.6 Crash recovery

The engine removes busybees' entire orphan problem (PID files, process
groups, container labels, sbx, host MCP servers — architecture.md "Crash
recovery"): a session's containers and agents end with it. What remains is
*resuming*: with §3.5's GitHub-derived stages, a restarted factory re-spawns
`dev/<n>` for every `bees:in-progress` issue on its pushed branch with an
"interrupted session" note — exactly busybees' `takeInterrupted` path
(developer.go:142), minus the bookkeeping. Optionally restore the
conversation from the trace (`spawn(handle:)` restore, core/schema/llm.go:268)
to keep context. A one-shot restart is fine; it's the unattended
*auto*-restart that needs §3.2.

## 4. Staging and estimate

1. **`review` module** (2.5) — biggest, cheapest win. Self-contained (one
   PR in, one review out), no lifetime problem (a `dagger call review
   --pr 57` or a tool in any `dagger agent`), exercises spawn/send/wait and
   deterministic reduction, and replaces `bees review` + `core/review`.
   ~300 lines of Dang + angle prompts.
2. **`roles` + per-role tool types** (2.3), including notes on a branch
   (2.6) and the marker-aware `comment`. Usable immediately by a *human chief*
   with `staff` — "spawn a developer on #42" — even before any scheduler.
3. **Developer loop + best-of-N on staff** (2.4), still chief-driven:
   proves the dev↔reviewer conversation, outcome events, PullTools assembly.
4. **`factory` tick** (2.1) with a thin host driver loop: labels,
   reconcile, dispatch, ledger, daily/per-issue budgets. This is the point it
   *is* busybees.
5. Engine work, in order of payoff: timer events (3.1) → agent caps (3.4) →
   detached sessions (3.2) → durable store (3.2). With the first two the
   driver loop shrinks to "keep a session open"; with the third it goes away.

**Estimate.** By busybees *capability*: ~60% is buildable as modules today
(the whole agentic core plus reconcile/dispatch/ledger, run from a
long-lived driver session); ~80% with timer events and agent caps; ~90% with
detached sessions and a durable store. The remaining ~10% is host-side
by nature (multi-project daemon, SIGHUP reload, custom TUI panels,
opencode/pi backends) and mostly not worth porting. By busybees *code*, the
fraction that needs rewriting is far smaller than 60%: sandboxing, worktrees,
session runners, harness adapters, MCP hosting, crash cleanup and state
migration — a large share of `core/` and `internal/` — are absorbed by the
engine rather than ported.

Open questions for angles 2–3: does `tokenUsage` cover harness-run
conversations; how long-lived developer conversations stay within context
(reseed + compaction policy per round); and whether the foreman should be
code-only or have an optional LLM chief on top.
