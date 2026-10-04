# A busybees-style factory TUI on Tuist

Investigation note (angle 2 of 3). Question: what would it take to give a
`dagger agent`-native multi-agent factory an operator view like
[busybees](https://github.com/kpenfound/busybees)' `bees run`, built on Tuist
(the component framework the pretty frontend uses), and could a Dagger
*module* extend it?

Everything below cites code read at this checkout; busybees paths are
relative to its repo (`internal/tui/…`), Tuist paths to `vito/tuist@v0.0.12`
(the version `go.mod:165` pins).

**TL;DR.** Dagger already has the hard half: a live, revisioned agent roster
folded from telemetry, per-agent conversation views that follow focus,
per-agent send/interrupt, span trees with logs, serialized forms, and a
command-screen slot that lets a command own the screen body. What it lacks is
the *operator* half: a dense multi-row table of agents (cost, model, elapsed,
parent, outcome), a "needs a human" queue, domain queues that are not agents
at all (issues, PRs, labels), and any way for a module to put data on screen
other than spans and `print`. Recommendation: build an in-tree board screen
over the existing `CommandView` slot fed only by agent telemetry first (no
protocol), then add one narrow, revisioned **board record** on the log stream
— modelled exactly on `engine/agentcontrol` — so a factory module can publish
queue/needs-human tables. Do not ship module-compiled components, and defer a
declarative `View` type until the record protocol has a second consumer.

---

## 1. What busybees' TUI shows, and why an operator wants it

`bees run` is an alt-screen Bubble Tea program (`internal/tui/tui.go:49-51`,
`:96-126`) fed by the scheduler's in-process event stream plus re-reads of
`status.json` and the mailbox (`model.go:26-92`). It never polls GitHub and the
scheduler never waits for it (`tui.go:23-25`). The rendered layout is
documented in `docs/cli.md:716-758`:

| Panel | Rows / columns | Source | Operator question it answers |
|---|---|---|---|
| **Now** | role, issue, PR, stage+round, elapsed, turns, cost, sandbox, model (`(fallback)` marker) — `model.go:1341-1404` | session-start events; turns re-counted from each `transcript.jsonl` every 5 s (`model.go:382-413`) | *What is burning money right now, on what, and is anything stuck?* Elapsed + turns + stage spot a looping session; the fallback marker spots a degraded model. |
| **Recent** | role, issue, PR, outcome, took, cost, note; newest first, capped at 8 (`panels.go:13-62`) | session-ended events only | *What just happened and did it work?* Coloured by outcome: green/yellow/red. |
| **Needs human** | issue, waiting-since, title, why (`panels.go:73-114`) | `status.json` escalations | *What has the factory given up on, and why?* Title/border turn warn-coloured only when non-empty (`model.go:1320-1333`). |
| **Approved PRs** | pr, issue, age, title | `status.json` | *What is waiting on me to merge?* |
| **Queues** | label-state counts in workflow order, unread mail per role, countdown to next poll (`model.go:1483-1569`) | `status.json`, mailbox | *Is the pipeline flowing, and where is it backing up?* |

Second screen — the **session view** (`session.go:13-58`): tails one session's
`transcript.jsonl` every 300 ms (`session.go:109-117`), follows the tail,
scrolls with j/k/pgup/g/G, and `m` composes a message that is queued *for the
next session on that work item* — a headless session cannot be told anything
once started (`model.go:58-68`).

Keys (`model.go:511-602`): one flat cursor across every panel's rows (↑/↓),
`enter` watch, `o` open issue/PR on GitHub, `k` kill with a two-press
confirmation that pins its target (`model.go:725-743`), `p` pause dispatch,
`r` reload config off-goroutine, `q`/ctrl-c graded stop (drain → hard stop →
leave). A daemon adds a project selector on ←/→ and a project column
(`project.go:16-49`). A short terminal drops panels bottom-up
(`tui.go:17-18`); the footer wraps notices up to 6 lines (`model.go:1241-1273`).
Colour is redundant with text and uses ANSI 0-15 only (`theme.go:9-19`).

`internal/reviewtui` is a separate full-screen triage tool: one finding at a
time beside the PR diff, single-key decisions (select/edit/dismiss/defer/ask),
tab between panes, every decision persisted before the next is shown
(`reviewtui/reviewtui.go:1-19`, `model.go:162-212`).

Design traits worth keeping: **read-only projection of an event stream**, a
**single selection model over heterogeneous rows**, **actions addressed to a
pinned target**, and **"nothing needs you" looks ordinary**.

## 2. What dagger's frontend already provides

### 2.1 Host, event loop, focus

- `frontendPretty` is a single Tuist component added to the root
  (`dagql/idtui/frontend_pretty.go:1115-1143`); the prompt chrome — error
  label, queued message, `PromptFrame`, `StatusLine` (with roster), keymap —
  are *root siblings* added by `startShell` (`:1502-1548`).
- All mutation is marshalled onto the Tuist loop via `fe.dispatch`
  (`:1042`); Tuist exposes `Dispatch` (`tui.go:767`).
- Focus is Tuist-owned with scoped restore: `Focused`/`IsFocused`/`PushFocus`
  (`tuist/tui.go:555-575`), and the frontend holds `*tuist.FocusHandle`s for
  search, tests view, log pager, diff viewer (`frontend_pretty.go:371-419`).
  `hack/designs/tuist-focus-ownership.md` §§5-6 is the contract: focus moves
  only on keypress or explicit present/dismiss; a turn finishing never moves it.

### 2.2 Command screens (`hack/designs/composable-command-tui.md`)

- Contract: `CommandFrontend{SetView, Live}`, `ViewFactory`,
  `CommandView{tuist.Component; Update(); SetFinal(bool)}`,
  `ViewContext{SpanList(root, include)}`, `ViewHandle{Update(func())}`
  (`dagql/idtui/frontend.go:128-157`).
- Host: `SetView` builds the view on the loop and focuses it if
  `Interactive` (`frontend_pretty.go:466-485`); `Render` hands the *whole body*
  to it and returns before the trace tree (`:3563-3566`); final render calls
  `SetFinal(true)` (`:2436-2438`); DB changes invalidate mounted `SpanListView`s
  and the view (`:3071-3075`, `:3160-3164`).
- Reusable trace component: `SpanListView` renders chosen children of a span as
  normal interactive span trees (`frontend_span_list.go:11-70`).
- **Reality check:** the design names `dagger setup`'s `SetupView` as the
  consumer, but `setup` is now a deprecated hint-printer
  (`internal/cmd/dagger/setup.go:43-60`) and nothing under `internal/` or
  `cmd/` calls `SetView` — the only callers are
  `frontend_command_view_test.go:570-640`. The slot works and is tested, but has
  **zero production consumers**; a factory board would be the first.
- Because prompt chrome lives in root siblings, a command view and the agent
  prompt/roster *can* coexist on screen — but `ViewContext` exposes no reserved
  height (`fe.keymapHeight()`/`statusLineHeight()`/`editlineHeight()` are
  private, `:3625-3633`), so a view cannot size itself around them today.

### 2.3 Agent roster, focus and attention (async-agents §5.1)

- **Data:** engine publishes each runtime as a loop span (`span.Agent`,
  `AgentID`, `AgentName`, `AgentCallDigest`; parsed at
  `dagql/dagui/spans.go:551-561`) plus revisioned control **log records**
  (`engine/agentcontrol/otlp.go:13-25`, emitted by `core/agent_control.go:87-90`).
  `agentcontrol.Agent` carries `Name, Parent, State, StopReason, Failure,
  Activity, Digest` (`engine/agentcontrol/control.go:32-45`); states are
  `IDLE RUNNING WAITING_INPUT PAUSED FAILED STOPPED` (`:68-75`). The DB ingests
  them latest-revision-wins (`dagql/dagui/agent_control.go:36-50`) and folds
  spans+records into `[]*AgentNode` (`dagql/dagui/agents.go:23-102`) —
  deliberately flat and unfiltered by containment so deep workers surface.
- **View:** `AgentRoster` is a one-line tab strip at the left of the status
  line, `N name state-glyph` per entry (`agent_roster.go:31-58`, glyphs
  `:252-272`; `WAITING_INPUT` renders "needs you"). Sourced at
  `frontend_pretty.go:4664-4689`, independent of zoom.
- **Focus:** ctrl+1…9 / alt+l / alt+[ ] from the prompt, 1…9 / \` / [ ] in nav
  (`:3508-3515`, `:4722-4877`); `focusAgent` saves/restores per-agent drafts
  and retargets the shell handler, rebuilding a handle from the trace when
  needed (`:4907-…`). Ctrl-C interrupts the focused agent only
  (`agent_focus_test.go:255`).
- **Live console**: `/agents` and `/transcript` on the TUI console
  (`frontend_console.go:307-308`) serve the roster and decoded checkpoints,
  live or from a downloaded trace (`frontend_console_trace.go:30-101`). That is
  what backs the tui-qa module's `agents`/`transcript` tools
  (`.dagger/modules/tui-qa/main.dang:390-460`).

### 2.4 Conversation view

- `DB.SurfacedConversationForSpan` / `ForAgent` build the message tree
  (`dagql/dagui/conversation.go:10-60`, `:163`); `promoteConversationLocked`
  swaps the promoted transcript when focus changes, so **the tree follows the
  focused agent** (`frontend_pretty.go:5291-5341`;
  `agent_conversation_focus_test.go:99-130`).
- Prompt mode is **flowing**: output scrolls into native scrollback
  REPL-style, with the prompt pinned below (`frontend_pretty.go:4350-4371`).
  That is the opposite of busybees' fixed alt-screen dashboard.
- Agent-to-agent messages render sender-attributed via
  `dagger.io/llm.origin.*` (async-agents.md §10 item 3; parsed
  `dagui/spans.go:590-601`); events collapse to one-liners (`:443-449`).

### 2.5 Other surfaces

- **HUD/sidebar bubbles** (`SetSidebarContent`, `frontend_pretty.go:1146-1204`;
  `SidebarSection` with `ContentFunc`, `KeyMap`, `Diffs`, `Agent`,
  `frontend.go:305-323`): top-right overlay, used for "Changes"
  (`internal/cmd/dagger/session_agent.go:1072-1118`) and "References".
- **Fullscreen modes**: tests view (`T`, `frontend_tests.go:1722`), log pager
  (`L`, `frontend_log_pager.go:326`), diff viewer (`frontend_diffs.go:875`).
- **Forms**: `HandleForm` queues huh forms FIFO, each wrapped in
  `teav1.Wrap` with its own `FocusHandle` (`frontend_pretty.go:1831-1894`).
  Engine→client prompts arrive over a session attachable:
  `PromptBool/String/Select` (`engine/session/prompt/prompt.go:38-156`).
  `PromptSelect` is the one declarative one — IDs and labels cross the RPC,
  the client builds the form — used by workspace checkpoints
  (`core/schema/workspace_checkpoint.go:845`).
- **Status line**: tokens/cost/context — but `LLMTokenMetrics` is aggregated
  **per model, session-wide** (`dagql/dagui/db.go:35-56`), not per agent.
- **Agent API** an operator view can drive: `send`, `message`, `pause`,
  `resume`, `wait`, `stop`, `reseed`, `snapshot` (`core/schema/agent.go:74-150`).

### 2.6 Tuist's actual vocabulary

Tuist v0.0.12 is deliberately small: `Component` (must embed `Compo`),
`Interactive`, `Pasteable`, `MouseEnabled`, `Focusable`, `Mounter`/`Dismounter`
(`component.go:628-760`); `Container`, `Slot` (`:815-891`); `TextInput`,
`CompletionMenu`, `Spinner`; overlays with anchors (`overlay.go`,
`tui.go:751`); `HeadlessTerminal` (`headless.go:30`); `RenderChildResult`
returns a child's lines (`component.go:438`) — the hook for side-by-side
layout. There is **no Table, List, Box, split or scroll-view**; idtui draws
those by hand with lipgloss/termenv. `teav1.Wrap` hosts any Bubble Tea v1
model as a Tuist component (`teav1/bubbletea.go:42-70`) — that is how huh
forms embed, and it means busybees-style `tea.Model`s *could* be hosted
verbatim.

## 3. Mapping and gaps

| busybees | dagger today | Gap |
|---|---|---|
| Now: role/issue/stage/elapsed/turns/cost/model per session | Roster strip: name + state glyph, one line, flat | **G1** No multi-column agent table. **G2** No per-agent cost/tokens/model (`LLMTokenMetrics` is per-model, session-wide). **G3** No parent/child (chief→worker) nesting, though `agentcontrol.Agent.Parent` already carries it (async-agents §10 item 1 calls entries "flat"). **G4** No elapsed/turn count per agent (derivable: loop span start, message count). |
| Recent: outcome, took, cost, note | STOPPED/IDLE/FAILED entries stay on roster | **G5** No "outcome" or final-note field; `Failure` and the last reply exist, a summary does not. |
| Needs human | `WAITING_INPUT` → "needs you" label | **G6** `WAITING_INPUT` is registered (`core/agent.go:97`) but unreachable; `waitingOn` is never rendered (async-agents §10 item 4). |
| Approved PRs, Queues (labels, mail, next poll) | nothing — not agent state | **G7** No way for a module to publish domain tables (work items, queue counts). `ProcessAttribute` is a closed switch (`dagui/spans.go:459-601`): unknown attributes are dropped. |
| Session view (tail transcript, message next session) | Focus switch promotes that agent's conversation; prompt sends *live* | Mostly covered and stronger (live `send`, interrupt). **G8** no "watch without focusing" read-only pane; focus is the only way to see a transcript. |
| k kill, p pause, r reload, o open | Ctrl-C interrupts focused agent | **G9** No TUI verbs for `Agent.stop`/`pause`/`resume`; no row-level actions; no "open URL". |
| Project selector (daemon) | One session per CLI; `dagger agent -r --agent` focuses a restored agent (`internal/cmd/dagger/agent.go:231-232`) | **G10** Multi-session/multi-workspace view is out of scope for now. |
| Alt-screen dashboard, panels drop on short terminals | Flowing REPL in prompt mode; command view replaces the body | **G11** No fixed-layout board mode alongside the prompt; `ViewContext` lacks reserved-height, roster, conversation and focus helpers. |
| reviewtui (diff + findings) | Diff viewer over HUD `Diffs` | Diff side exists; finding-triage queue does not (out of scope). |

## 4. Architecture options

All options share an in-tree core: a **board** of agent rows derived from
`db.Agents()` + `AgentControl()`, rendered by a new `AgentTableView` Tuist
component (hand-rolled columns, `Interactive` for a row cursor, `enter` →
`focusAgent`, `s` → stop, `p` → pause/resume, two-press confirm pinned to the
agent *handle*, not the row index — busybees' `model.go:731-739` lesson). It
answers G1/G3/G4/G9 with no new protocol. G2 needs per-agent metrics: either
tag LLM token metrics with the agent handle in `core/` (preferred; one
attribute on the existing gauges) or sum per-message usage under each agent's
loop span client-side. The options differ in how **module-defined** panels
(G7) get on screen.

### Option A — in-tree `dagger agent --board` screen + revisioned board records (recommended)

*Screen:* a first real `CommandView` consumer, toggled from prompt mode (say
`B`, a fullscreen mode like `T` tests) or started with `--board`. Layout top to
bottom: Agents table, Needs-you list, module panels, then the existing root
siblings (prompt, roster, keymap). Extend `ViewContext` with
`ReservedHeight()`, `Agents()`, `Conversation(agentID)`, `FocusAgent(id)`, and a
`PanelsFor(root)` accessor — all read-only projections of the host's DB, in the
spirit of `composable-command-tui.md` §"View Context".

*Extensibility (attribute/record-driven, i.e. option (a)):* a module publishes
**board records** on the log stream, exactly the agentcontrol pattern —
complete, revisioned, latest-wins projections keyed by `(namespace, panel,
row)`, validated, ingested by `dagui` into a materialized index that bumps
`db.mutations`:

```text
dagger.io/board.kind      = "panel" | "row"
dagger.io/board.panel     = "needs-human"          # stable key
dagger.io/board.revision  = 7
dagger.io/board.title     = "Needs human"          # panel only
dagger.io/board.columns   = ["issue","waiting","title","why"]  # panel only
dagger.io/board.attention = true                   # panel: warn styling when non-empty
dagger.io/board.row       = "issue-44"             # row only; empty cells list = tombstone
dagger.io/board.cells     = ["#44","2d","Parser drops…","Checks still fail…"]
dagger.io/board.agent     = "<agent handle>"       # optional: enter focuses it
dagger.io/board.url       = "https://github.com/…" # optional: o opens it
dagger.io/board.status    = "ok"|"warn"|"fail"     # optional: colour, never the only signal
```

Why records, not span attributes: rows outlive spans, must be replaceable
in place and deletable (tombstones, like `Subscription` with empty `States`,
`agentcontrol/control.go:53-59`), and must replay identically from an imported
trace — the agentcontrol index already proves all three for agents, including
Cloud import (`frontend_console_trace.go:52-61`). The board stays a pure
projection of telemetry: works on `dagger trace`, in Cloud, and after `-r`
restore; no new RPC; the engine never waits on the UI (busybees' `tui.go:23`
property for free).

*Module surface:* Go/TS/Python modules can emit the records with the OTel
logger they already have (`dagql/idtui/viztest/main.go:14-18` shows a module
setting `dagger.io/ui.*` attributes directly). Dang has no telemetry API — only
`print` — so add a small core API, e.g. `Query.board(panel:).withRow(...)` or
`Board.publish`, implemented in `core/` by emitting the records (as
`core/agent_control.go` does). That keeps Dang factories first-class.

*Actions:* rows are read-only except the generic `enter` (focus agent),
`o` (open URL) and agent verbs on agent-linked rows. Module-defined actions are
deferred; if needed later, they should be a *declared* action ID returned to
the module through `PromptSelect`-style IDs-not-code, never a callback.

*Pros:* smallest new concept; replayable; Cloud can render the same records;
no module code runs in the CLI. *Cons:* tables only (no custom drawing);
schema-on-attributes needs careful validation (fail closed like
`agentcontrol.Index.ApplyAgent`).

### Option B — declarative view model returned by a module

A core `Panel`/`View` object type (title, columns, rows, row links to `Agent`
or URL) that a module function returns, and which the CLI renders with Tuist.
The board would call, say, every `@board`-annotated function on the
workspace's modules on a timer, or subscribe to a returned `Agent`-like live
object.

*Precedent:* `PromptSelect` already crosses a declarative UI description over
the session (`prompt.go:134-156`); `SidebarSection` is an in-process analogue
(`frontend.go:305-323`).

*Pros:* typed, discoverable in the schema, can carry richer structure (nested
groups, actions as enum IDs). *Cons:* pull-based — someone must poll, and each
poll is a module call (cache policy, cost, session lifetime); the result is not
in the trace, so Cloud/`dagger trace`/restore see nothing unless also
mirrored to telemetry; a public API commits early to a widget vocabulary.
Reasonable as a *later* typed façade that emits Option A's records.

### Option C — module-provided Tuist component compiled into the CLI

A Go package implementing `tuist.Component`/`CommandView`, linked into the
`dagger` binary (build tag or registry), selected by name.

*Pros:* unlimited fidelity; could even host busybees' existing `tea.Model`
via `teav1.Wrap`. *Cons:* not a module at all — no runtime extensibility,
version-locks third parties to idtui internals (`frontendPretty` is ~9.7k
lines of private state), and runs arbitrary code in the CLI. **Reject** as an
extension mechanism; it is only the right shape for components *we* own.

### Option D — separate `dagger factory` command with its own screen

A new top-level command whose screen is the board, launching/attaching to a
factory module's chief agent.

*Pros:* clean alt-screen layout like `bees run`; no interference with prompt
mode's flowing render. *Cons:* duplicates `dagger agent`'s session, restore
(`-r`), focus and prompt wiring; splits operators between two UIs; still needs
Option A or B for module panels. Better expressed as `dagger agent --board`
(Option A) sharing all of that.

### Recommendation

Option A, staged:

1. **Agent board, no protocol** — `AgentTableView` over `db.Agents()` +
   control records: name, parent-indented, state, elapsed (loop span),
   turns (message count), model; `enter` focus, `s`/`p` stop/pause via the
   shell handler (new duck-typed methods like the existing `FocusAgent`).
   Hosted as a fullscreen toggle in prompt mode (pattern: tests view) so it
   needs no new focus model.
2. **Per-agent cost** — stamp the agent handle on LLM token metrics in `core/`
   and keep a per-agent rollup next to `LLMTokenMetrics`. Also fixes the
   status-line scope lie async-agents §5.1 calls out.
3. **Attention** — wire `WAITING_INPUT`/`waitingOn` (async-agents §10 item 4);
   render a Needs-you section that is plain when empty.
4. **Board records** — `engine/boardcontrol` mirroring `agentcontrol`
   (types, `Record()`, `Decode`, `Index.Apply*`), `dagui` ingestion,
   a generic `BoardPanelView`, and a core API so Dang modules can publish.
5. Make the board a proper `CommandView` (extend `ViewContext`), giving the
   slot its first production consumer and validating
   `composable-command-tui.md`'s unchecked boxes (host split, focus/final tests).

Keep busybees' discipline: the view reads projections and never blocks
producers; colour is redundant with text; confirmations pin their target by
identity.

## 5. Rough sizing

Assuming one engineer familiar with idtui; tests via `tuist.NewHeadlessTerminal`
and the existing frontend test driver, plus tui-qa for live checks.

| Slice | Where | Size |
|---|---|---|
| 1. `AgentTableView` + toggle + row actions (focus/stop/pause) | `dagql/idtui/agent_board.go` (new), keymap, shell-handler duck types in `internal/cmd/dagger/session_agent.go` | ~600-900 LOC incl. tests; 3-5 days |
| 2. Per-agent token/cost rollup | `core/llm*.go` metric attrs, `dagql/dagui/db.go`, status line | ~200-400 LOC; 2-3 days (touches metric emission, needs engine test) |
| 3. WAITING_INPUT + waitingOn rendering | `core/agent*.go` parking path is the long pole (async-agents §3.4); UI part ~150 LOC | UI 1 day; engine side is its own project |
| 4. Board records protocol + core API + Dang support | `engine/boardcontrol/` (new, ~300 LOC like agentcontrol), `dagql/dagui/board.go`, `core/schema/board.go`, SDK regen, `BoardPanelView` | ~1200-1800 LOC; 1.5-2 weeks incl. schema review and integration tests |
| 5. Promote to `CommandView`, extend `ViewContext`, final-render | `frontend.go`, `frontend_pretty.go`, `frontend_span_list.go` | ~300-500 LOC; 3-4 days |
| Option B façade (later) | core `Panel` type emitting records | ~1 week |

Slices 1-2 alone deliver most of busybees' **Now**/**Recent** value for a
staff-of-agents factory; slice 4 is what makes **Needs human / Approved PRs /
Queues** expressible by a factory module rather than hard-coded in the CLI.

## Open questions

- Board-record namespace: per session like agentcontrol, or per workspace so a
  long-running factory's board survives client restarts?
- Should Cloud render board records? (Cheap if the schema is fixed early.)
- Does the board replace the roster strip when shown, or sit above it? The
  strip is the prompt's state indicator (`agent_roster.go:38-40`), so keeping
  both is likely right.
- Polling cadence for anything not event-driven (busybees refreshes every 5 s,
  `model.go:315-319`): with records, the *module* decides when to publish, which
  is the right owner.
