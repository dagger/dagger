# Agent v2: The Focus-Anchored Conversation UI

A plan for a dramatically different terminal UI paradigm for `dagger agent`:
the input prompt is no longer pinned to the bottom of the screen — it is
**anchored to the focused message**. Everything else (status line, context
gauge, keymap, interrupt, interjection) re-anchors with it. Because focus can
land on *any* message — including a sub-agent's message running inside an
outer conversation's tool call — the prompt becomes a portal into that
specific conversation: submit interjects into it, Ctrl+C interrupts *its*
loop, and the context bar shows *its* fill level at that moment in time.

The end state: you can guide every step of the way in a hierarchy of agents.

## Table of Contents

- [Problem](#problem)
- [Vision](#vision)
- [UX Walkthrough](#ux-walkthrough)
- [Current Architecture](#current-architecture)
- [Design](#design)
  - [1. Conversation identity (telemetry)](#1-conversation-identity-telemetry)
  - [2. Loop control plane (engine)](#2-loop-control-plane-engine)
  - [3. Focus-anchored prompt (frontend)](#3-focus-anchored-prompt-frontend)
  - [4. Focus-anchored status (frontend + telemetry)](#4-focus-anchored-status-frontend--telemetry)
  - [5. Targeting, keybindings, and escalation](#5-targeting-keybindings-and-escalation)
  - [6. Branching from the anchor](#6-branching-from-the-anchor)
  - [7. Top-level loop unification (option)](#7-top-level-loop-unification-option)
- [Phases](#phases)
- [Risks & Edge Cases](#risks--edge-cases)
- [Open Questions](#open-questions)

## Problem

1. **The prompt is context-blind** — it is pinned to the screen bottom
   regardless of what you're looking at. Navigating the conversation and
   talking to the agent are two disconnected activities.
2. **Interrupt is all-or-nothing** — Ctrl+C cancels the entire turn's context,
   including every sub-agent transitively
   (`frontend_pretty.go` routes Ctrl+C to `shellInterrupt`, the cancel of the
   single per-turn `context.WithCancelCause`). There is no way to interrupt
   *just* a child loop.
3. **Interjection is top-level only and between-turns only** — the
   queued-message mechanism (`shellCallHandler.QueueMessage` /
   `LLMSession.WithPrompt`'s `DequeueMessage` check) is purely client-side and
   only feeds the *top-level* conversation between *top-level* steps. A
   sub-agent mid-loop is unreachable.
4. **Context usage is a mystery** — the status line shows *current* context
   occupancy; there's no way to look at a message and know how full the
   context was at that point, so you can't see where usage blew up (the
   per-step data exists in telemetry, but only `--debug` surfaces a coarse
   version as spans).
5. **Sub-agents are opaque** — nested conversations render (they roll up under
   the tool-call span via `dagui.MessageNode`), but they're read-only
   scenery. You can watch a sub-agent go down the wrong path and do nothing
   about it except kill everything.

## Vision

One principle, applied everywhere: **the focused message is the cursor of the
whole UI, and the prompt is its caret.**

- The editline renders *directly beneath the focused message*, framed and
  labeled with the conversation it targets.
- Submitting interjects into that conversation — the live end if it's the
  newest message, a mid-loop injection if that conversation is running, a
  branch point if it's history (opt-in).
- Ctrl+C interrupts *the focused conversation's* loop. Focus on a sub-agent
  message → the sub-agent stops at its next step boundary and the parent turn
  keeps running. Focus at top level → today's whole-turn interrupt. Repeat to
  escalate.
- The anchored status line time-travels: context gauge, step cost, tokens, and
  model *as of the focused message*, so scanning up the transcript is also
  scanning the context-usage history.

When nothing is focused manually (auto-follow, the default), focus rides the
newest message — so the prompt hugs the bottom of the live conversation and
the UI degrades gracefully to (approximately) today's layout. The paradigm
only *reveals* itself when you navigate.

## UX Walkthrough

### Scenario 1: idle, auto-follow (looks familiar)

Focus rides the newest message; the anchored prompt is effectively
bottom-anchored, but visibly *attached to the transcript* rather than the
terminal:

```
🧑 Refactor the parser and get the tests green

🤖 Done. All 47 tests pass. Summary of changes: …

  ┌───────────────────────────────────────────────────────────────┐
  │ ❯ █                                                           │
  └───────────────────────────────────────────────────────────────┘
    [███▁▁▁▁▁▁▁] 31%/200k · ↑6.3k ↓30k · $3.61 · claude-sonnet-4-5
    ⏎ send · esc nav · tab complete
```

### Scenario 2: steering a running sub-agent

The outer agent delegated to a `triage` sub-agent (an LLM loop running inside
the `delegate` tool call). You press `esc`, arrow up into the nested
conversation, and the prompt re-anchors there:

```
🧑 Refactor the parser and get the tests green

🤖 I'll delegate test triage first.

• delegate "triage failing parser tests"
    🧑 Triage the failing parser tests …
    🤖 Running the suite to see what fails…
    • go test ./parser/... ✔ 12s
  ▶ 🤖 Three failures, all in quote handling. Investigating the lexer…
    ┌───────────────────────────────────────────────────────────┐
    │ ❯ focus on the multiline case first; skip the perf test█  │
    └───────────────────────────────────────────────────────────┘
      ⛬ agent ▸ delegate ▸ triage        [██▁▁▁▁▁▁▁▁] 18%/200k
      ⏎ interject triage · ctrl+c interrupt triage · esc back
… 23 lines below …
```

- `⏎` queues the message into the *triage* loop; it's consumed at its next
  step boundary and renders inline in the nested transcript.
- `ctrl+c` interrupts only the triage loop. The outer turn keeps running; the
  `delegate` tool call returns the sub-agent's accumulated result annotated as
  interrupted.
- The breadcrumb (`agent ▸ delegate ▸ triage`) always names the target, so
  there is never ambiguity about who you're talking to or what Ctrl+C kills.

### Scenario 3: context archaeology

Navigate up the transcript; the anchored gauge replays context history:

```
  ▶ • read_file "vendor/parser/grammar.y" ✔ 0.8s   +41.2k tokens
      ⛬ agent                              [███████▁▁▁] 74%/200k ▲ +41.2k
```

The message that blew up the context is instantly visible: the anchored bar
jumps between two adjacent messages, and per-step growth (`▲ +41.2k`) is
right there. (Optionally: a faint per-message heat indicator in the gutter for
steps whose growth exceeds a threshold, so the spike is findable without
stepping through.)

### Scenario 4: interrupt escalation

While the triage sub-agent is focused and running:

- `ctrl+c` → interrupt **triage** (graceful: stop at step boundary).
- `ctrl+c` again (while triage is winding down / already interrupted) →
  escalate to the parent conversation — ultimately the whole turn, exactly
  today's semantics.

Focus at top level: `ctrl+c` is exactly today's whole-turn interrupt. Nothing
is lost for users who never navigate.

## Current Architecture

Grounding for the design; all paths relative to repo root.

### Frontend (dagql/idtui)

- `frontendPretty` (`frontend_pretty.go:68`) is a `tuist` component tree. The
  prompt chrome is a stack of **siblings appended after the trace body** in
  `startShell` (`frontend_pretty.go:886`, order at `:917-923`):
  body → `ErrorLabel` → `QueuedMessageLabel` → `PromptFrame` (wrapping
  `tuist.TextInput`) → `StatusLine` → `KeymapBar`. Bottom-anchoring is purely
  child ordering; the body is cropped to the remaining height
  (`reserved` calc at `:2413-2421`).
- **Scrollback**: `flowingMode()` (`:3034`) lets over-tall frames spill into
  native terminal scrollback. When the user navigates up (`!autoFocus`), the
  frame is already cropped *below the focused item* (`flowingCropEnd`,
  `:3676`) with a `… N lines below …` hint (`:3696`) — i.e. the focused row
  already gravitates toward the bottom of the visible frame. This is the hook
  the anchored prompt slots into.
- **Focus**: `FocusedSpan dagui.SpanID` is the nav cursor;
  `editlineFocused bool` (`:123`) toggles input vs nav mode (`esc` / `i`);
  `autoFocus` (`:156`) follows the newest row. Per-row highlight lives in
  `SpanTreeView.focused` (`:342`).
- **Interrupt**: Ctrl+C in either mode calls `fe.shellInterrupt(...)`
  (editline: `:3920-3926`; nav: `:4060-4068`) — the cancel of the per-turn
  `context.WithCancelCause(fe.shellCtx)` created in `startShellHandle`
  (`:4295-4297`). One turn = one cancel = everything under it dies.
- **Queued messages**: submit-while-running →
  `fe.setQueuedMessage` (`:4254-4273`) → `shellCallHandler.QueueMessage`
  (`internal/cmd/dagger/shell.go:159`); drained between top-level steps in
  `LLMSession.WithPrompt` (`internal/cmd/dagger/llm.go:243-268`) or at turn
  end (`handleShellDone`, `frontend_pretty.go:4333-4335`).
- **Status line**: `StatusLine` (`statusline.go:57`) renders
  `StatusLineData` seeded per-step by `LLMSession.updateStatusLine`
  (`internal/cmd/dagger/llm.go:386`) and overridden at render time by the
  live metric rollup `fe.llmLiveStats()` (`frontend_pretty.go:856`) over
  `dagui.LLMTokenMetrics` (`dagql/dagui/db.go:31`). Context gauge:
  `renderContextBar` (`statusline.go:202`).

### Conversation model (dagql/dagui)

- `MessageNode` (`conversation.go:10`) — spans with `LLMRole` grouped by
  ancestry: a message anchors under the nearest ancestor message span; a
  tool-call span is a `Boundary`, so a sub-agent's turns roll up under the
  tool call that spawned them (`buildSurfacedConversation`,
  `conversation.go:46-119`). **Sub-agent conversations are only inferable
  structurally — there is no conversation-identity attribute today.**
- Message attributes consumed into `Span` (`spans.go:466-488`):
  `telemetry.LLMRoleAttr`, `LLMToolAttr`, `dagger.io/llm.call.digest`
  (`engine/telemetryattrs/attrs.go:24`), `dagger.io/llm.tool.result_tokens`
  (`attrs.go:32`).
- **Per-step token metrics are span-scoped**: providers record
  `dagger.io/metrics.llm.input.tokens` (+ output / cache reads / writes)
  tagged with `dagger.io/metrics.span` = the LLM HTTP call span (e.g.
  `core/llm_anthropic.go:320-330`), landing in
  `db.MetricsBySpan[spanID][name]` (`dagql/dagui/db.go:476-495`). The data
  for a per-message context gauge already flows; it's just not stamped on
  the *message* spans.

### Engine LLM loop (core)

- `LLM.Step` (`core/llm.go:1446`): one provider round-trip + tool batch;
  emits message spans (`emitMessageSpan`, `:1915`). `HasPending`
  (`:1866`): last message is `user`. `Loop` (`:1756`): steps until
  `!HasPending`; **already treats ctx cancellation as a non-error and returns
  accumulated state** (`:1785-1790`) — the exact semantics a graceful child
  interrupt needs.
- The interactive CLI drives its own client-side loop
  (`LLMSession.WithPrompt`, one `Step().Sync(ctx)` per iteration). Engine-side
  `Loop` is what agent-module tool functions use — i.e. **sub-agents run
  `Loop` inside a tool call's ctx**, reachable only via ancestor-ctx
  cancellation today.
- `LLM.Interject` (`core/llm.go:1797-1853`) is unwired dead code (blocking
  `PromptHumanHelp` flow) — prior art that the engine can block on client
  input mid-run, but not the async mechanism we need.

## Design

Four layers, separable and independently shippable: telemetry identity →
engine control plane → anchored prompt → anchored status.

### 1. Conversation identity (telemetry)

Give every LLM loop/conversation a stable identity so the UI can address it.

- On LLM instantiation of a message-history lineage (practically: generated
  once per `Loop`/turn execution and threaded through `Step`), mint a
  conversation ID and stamp every message span it emits:

  ```
  dagger.io/llm.conversation.id     = <uuid>       # this conversation
  dagger.io/llm.conversation.parent = <uuid|"">    # spawning conversation, if nested
  dagger.io/llm.conversation.label  = "triage"     # human name: agent/tool name
  ```

  Emitted from `emitMessageSpan` (`core/llm.go:1915`) and the live display
  path (`core/llm_display.go`); consumed into `dagui.Span` alongside the
  existing `LLM*` fields (`dagql/dagui/spans.go:466-488`).

- `dagui` maps `FocusedSpan` → owning `MessageNode` → conversation ID +
  breadcrumb (walk `conversation.parent` chain for `agent ▸ delegate ▸
  triage`). Fallback for old engines: today's structural inference
  (ancestry + `Boundary`) still works for *display*; only *control* needs the
  ID.

### 2. Loop control plane (engine)

A session-scoped registry of running conversations, with three verbs.

**Registry.** The per-session server (the state shared by the main client and
all nested module clients) gains an `LLMLoopRegistry`. Each `Loop` execution
registers `{conversationID, label, parentID, cancelStep, mailbox}` on entry
and deregisters on exit. Nested module calls share the session server, so a
sub-agent loop deep inside module sandboxes is still reachable from the
top-level CLI client.

**API.** Impure, uncached fields, restricted to the session's main client
(same trust posture as `terminal`):

```graphql
extend type Query {
  """Conversations (LLM loops) currently running in this session."""
  llmConversations: [LLMConversation!]!
}

"""A handle to a running LLM conversation loop."""
type LLMConversation {
  conversationId: String!
  label: String!
  parentId: String

  """
  Queue a user message into the conversation. Consumed at the next step
  boundary (before the next provider call). Does not cancel anything.
  """
  interject(prompt: String!): Void

  """
  Interrupt the conversation. GRACEFUL stops at the next step boundary,
  keeping the in-flight step; HARD cancels the in-flight provider call too
  (that step's partial output is lost, matching today's turn interrupt).
  The loop returns its accumulated state to its caller.
  """
  interrupt(kind: LLMInterruptKind! = GRACEFUL): Void
}

enum LLMInterruptKind {
  GRACEFUL
  HARD
}
```

**Loop changes** (`core/llm.go:1756`): before each step, drain the mailbox —
interjected prompts append as user messages (rendering as `🧑` spans inside
the nested transcript, exactly like top-level prompts); check the interrupt
flag — if set, return accumulated state (the existing
ctx-cancel-is-not-an-error branch at `:1785-1790` already defines the return
path; GRACEFUL reuses it without cancelling, HARD cancels a per-loop derived
`context.WithCancelCause` first).

**What the parent sees.** The tool call returns whatever the sub-agent's loop
returned — for GRACEFUL, its last reply. The tool result is annotated (a
system-visible note, e.g. appended to the tool result) with
`"[user interrupted this sub-agent]"` and/or `"[user interjected during this
call]"` so the parent LLM can react sensibly rather than trusting a
half-finished answer.

**Held state (later phase).** An optional third verb: `interrupt(kind: HOLD)`
pauses the loop after cancelling the in-flight step and *blocks* awaiting
`interject` / `resume` / `interrupt`. The tool-call span stays running and
the TUI shows "held — waiting for you". This is the full
pause-inspect-steer-resume flow; it needs liveness care (held loops die with
the session) and is deliberately not in the MVP.

**Version skew.** CLI probes for `Query.llmConversations`; absent → the
anchored prompt still renders, but child-targeted verbs degrade (interject →
top-level queue, Ctrl+C → whole-turn interrupt) with a status-line notice.

### 3. Focus-anchored prompt (frontend)

Move the editline from a bottom sibling into the body, spliced beneath the
focused message.

- **Anchor point** = the focused `MessageNode`'s last rendered line (after
  its logs/children per current expansion). Non-message spans focused (a
  plain tool span, a check) anchor to their *owning* message node; if there
  is none (pure pipeline runs, `dagger call`), fall back to today's bottom
  chrome — this paradigm is scoped to conversation UIs.
- **Mechanics**: the prompt block (frame + anchored status + keymap; see §4)
  becomes a component rendered by the body. Two candidate implementations:
  1. *Splice at crop time*: `renderProgressLines` (`frontend_pretty.go:3520`)
     already computes `findFocusLine` (`:3723`) and crops below the focused
     item when navigated (`flowingCropEnd`, `:3676`). Insert the prompt
     block's rendered lines at the crop point, and translate the editline's
     reported cursor position (tuist `RenderResult.Cursor`) by the focus-line
     offset. Low-risk: the tree components don't change; the prompt is
     composited where the crop hint goes today.
  2. *Mount into the tree*: make the prompt a child component of the focused
     `SpanTreeView`. Cleaner conceptually, but churns per-span components,
     focus routing (`applyTuistFocus`, `:3309`), and render caching.
  Recommendation: (1) first; revisit (2) if compositing gets fiddly.
- **Height accounting**: the bottom `reserved` calc (`:2413-2421`) shrinks to
  keymap-only (or nothing); editline/status heights move into the body's crop
  math. `PromptFrame`, `StatusLine`, `QueuedMessageLabel` are reused as-is,
  just rendered by the new anchored block instead of as bottom siblings.
- **Auto-follow**: `autoFocus` keeps focus on the newest message, so the
  anchored prompt naturally sits at the transcript's live end; completed
  lines above still spill into native scrollback via flowing mode. Navigating
  up sets `!autoFocus` → the prompt travels; content below the anchor is
  cropped with the existing `… N lines below …` hint. **The flowing-mode
  contract is preserved** — we never need to redraw lines already pushed to
  native scrollback, because the anchor block always renders within the live
  frame. (Messages that have fully scrolled into native scrollback can still
  be *focused* — the frame re-renders from the tree, effectively scrolling
  the view back; same behavior nav-up has today.)
- **Input-vs-nav collapse (creative option)**: with the prompt attached to
  focus, the two modes can partially merge — plain typing in nav mode could
  route into the anchored editline (jump-to-type), with `↑`/`↓` on an empty
  editline moving focus between messages instead of history. This makes
  "navigate + talk" one fluid motion. Needs care with history navigation and
  key conflicts (`hjkl`); proposed as a later polish phase, default off.
- **Queued/interjected rendering**: a message queued for conversation X
  renders inline, dimmed with `⏳`, *at the end of X's transcript* (not in
  bottom chrome), becoming a real `🧑` message span when consumed. The
  existing `QueuedMessageLabel` (`queued_message.go`) is repurposed for this
  inline slot; `alt+↑` still recalls it for editing.

### 4. Focus-anchored status (frontend + telemetry)

The status line splits into two:

- **Anchored status** (part of the prompt block): everything scoped to the
  focused message and its conversation —
  breadcrumb (`⛬ agent ▸ delegate ▸ triage`), context gauge *as of that
  message*, that step's growth (`▲ +41.2k`), step cost, the model that
  produced it, and running/held/interrupted state of the target conversation.
- **Global footer** (optional, one line, may be merged into the anchored
  line when focus is at the live end): session totals — cumulative cost, all
  models, `● working` — today's `StatusLine` data.

**Per-message context data.** Two sources, both used:

1. *Engine stamps (preferred)*: at step end, stamp the assistant reply span
   with

   ```
   dagger.io/llm.context.tokens = <prompt tokens this step: input + cache reads + writes>
   dagger.io/llm.context.window = <model window>
   ```

   Trivial for the engine (it has the usage in hand in `Step`), robust across
   providers/models/sub-agents, works in Cloud traces too.
2. *Client-side fallback* (old engines): correlate via
   `db.MetricsBySpan` — find the `LLM HTTP` call span under the step and sum
   `dagger.io/metrics.llm.input.tokens` (+ cache metrics)
   (`dagql/dagui/db.go:476-495`).

**Context blame**: with per-message context sizes, the frontend can compute
per-step *growth* for every message and (a) show it inline on tool-call rows
above a threshold (like `renderToolResultTokens` does for tool results today,
`frontend_pretty.go:5679`), (b) tint the anchored gauge segment that this
step contributed. Finding "where context blew up" becomes a two-second scan.

### 5. Targeting, keybindings, and escalation

The **target conversation** is derived from focus: focused message → owning
conversation ID (walking up for non-message spans). The prompt frame always
displays the target's breadcrumb; every conversation-verb key in the keymap
bar names its target (`ctrl+c interrupt triage`).

| Key | Focus on live end (top level) | Focus on running sub-agent | Focus on history |
|---|---|---|---|
| `⏎` submit | prompt (idle) / queue (running) — today's behavior, now via the same interject verb | **interject into that loop** | branch prompt (§6) or no-op + hint |
| `ctrl+c` | interrupt whole turn (today) | **interrupt that loop (graceful)** | clear input (today) |
| `ctrl+c` ×2 | (unchanged) | **escalate: interrupt parent / whole turn** | — |
| `alt+c` (TBD) | — | hard interrupt (cancel in-flight step) | — |
| `esc` / `i` | nav ↔ input toggle, unchanged | unchanged | unchanged |

Escalation rule: a second `ctrl+c` within the escalation window (or while the
target is already interrupting) walks one level up the conversation.parent
chain. The keymap bar always shows what the *next* press will do — no hidden
state.

Global quit stays `ctrl+d` on empty input (`frontend_pretty.go:3914-3918`);
never Ctrl+C, unchanged from today.

### 6. Branching from the anchor

Prior art exists: nav-mode `b` branches from a message via
`ShellHandler.BranchFromID` + `dagger.io/llm.call.digest`. The anchored
prompt makes branching *spatial*: focus a past top-level message, type, and
submit → confirm (`⏎ again to branch from here`) → new branch seeded at that
point with the typed prompt, using the existing branch machinery (branch
summaries, session save with `Branch` metadata). Sub-agent history is
non-branchable in the MVP (the sub-conversation belongs to the module's
execution, not the session) — the prompt anchored there is
informational/status-only once that loop has ended.

### 7. Top-level loop unification (option)

Today the top-level turn is a *client-driven* loop
(`LLMSession.WithPrompt`) while sub-agents are *engine* `Loop`s. Two paths:

- **A (MVP): keep both.** The client loop keeps its queued-message /
  auto-compact / auto-save behavior; only the *UX* is unified (submit at top
  level uses the existing queue, submit on a child uses `interject`). Least
  churn, some duplicated semantics.
- **B (later): top level becomes an engine Loop too.** The CLI starts the
  turn as `loop` and controls it exclusively via the control plane; queued
  messages, and eventually auto-compact, move engine-side. One mechanism
  everywhere, and `dagger agent` turns become interruptible/steerable by
  *other* tooling (Cloud, IDEs) via the same API. Requires solving per-step
  client callbacks (auto-save via `onStep`, status refresh) — likely via the
  existing step-wise telemetry plus a `PortableID` checkpoint per step.

Recommendation: A for the MVP, B as a follow-up once the control plane is
proven on sub-agents.

## Phases

Each phase ships independently and is useful on its own.

**Phase 0 — Telemetry identity & per-message context.**
Conversation ID/parent/label attrs on message spans; context tokens/window
stamped on assistant reply spans. Pure emission; no behavior change.
(`core/llm.go` `emitMessageSpan`/`Step`, `core/llm_display.go`,
`engine/telemetryattrs`, consumed in `dagql/dagui/spans.go`.)

**Phase 1 — Focus-anchored prompt, top-level only.**
Prompt block spliced at the focus crop point; anchored breadcrumb + status;
bottom chrome reduced; queued message rendered inline. Submit/Ctrl+C
semantics unchanged (top-level targets only). This is the visual paradigm
shift, shippable without any engine changes beyond Phase 0.

**Phase 2 — Context archaeology.**
Anchored context gauge time-travel using Phase 0 attrs (with
`MetricsBySpan` fallback); per-step growth display; context-blame inline
markers.

**Phase 3 — Loop control plane + child targeting.**
Engine registry, `interject`/`interrupt(GRACEFUL|HARD)` API, loop mailbox
drain, tool-result annotations; CLI wires submit/Ctrl+C to the focused
conversation with escalation. **This is the magic trick.**

**Phase 4 — Polish & unification.**
Branch-from-anchor; HOLD (pause/resume) verb; jump-to-type mode merge;
top-level loop unification (7B); plain-frontend/report parity notes; Cloud
trace rendering of conversation attrs.

## Risks & Edge Cases

- **Race: interject vs loop exit.** The loop may finish between focus and
  submit. `interject` returns a "conversation ended" error → CLI offers to
  requeue the message to the parent (or the top level) rather than dropping
  it.
- **Parallel sub-agents.** Multiple loops run concurrently under one turn
  (parallel tool calls). Focus disambiguates naturally; the registry lists
  all; the breadcrumb prevents mis-sends.
- **Deep nesting.** Sub-agents of sub-agents work by construction
  (parent-chain attrs + registry entries per loop); breadcrumbs truncate in
  the middle (`agent ▸ … ▸ triage`) at narrow widths.
- **Interrupted-step loss.** HARD interrupt loses the in-flight step's
  partial output — same as today's turn interrupt. GRACEFUL (the default)
  never loses anything, at the cost of waiting out the current provider call.
- **Trust boundary.** Control verbs must be main-client-only; module code
  must not steer or kill sibling conversations. Same enforcement pattern as
  `terminal`/interactive.
- **Renderer regressions.** The splice must keep the flowing-mode contract
  (no mouse handlers, over-tall frames, cursor math via
  `RenderResult.Cursor`). Golden tests exist for the pretty frontend
  (`frontend_pretty_test.go`, `golden_test.go`) and the TUI console
  (`DAGGER_TUI_CONSOLE`) enables scripted QA of nav/anchor/interrupt flows.
- **Session resume.** Interjections are ordinary `withPrompt`s in the child's
  history; the *parent's* history only contains the tool result, so a resumed
  session replays exactly what the models saw. No new persistence.
- **Old engine / plain frontend.** Version-skew degradation per §2;
  `frontend_plain` and the final report ignore anchoring entirely (the
  conversation report already renders nested transcripts).

## Open Questions

Tracked here; resolutions get folded back into the sections above.

1. **Submit-vs-interrupt division of labor.** Proposal: `⏎` = interject
   (never cancels), `ctrl+c` = interrupt (never sends). Is that the right
   split, or should Ctrl+C on a child open a "held" state that waits for an
   interjection (pause-steer-resume) even in the MVP?
2. **Escalation gesture.** Double-`ctrl+c` to walk up the hierarchy vs. a
   dedicated "interrupt everything" key. How fast should the escalation
   window be, and should the keymap bar count it down?
3. **History semantics.** Should submit on a *past* top-level message branch
   (with confirm), or should the anchored prompt on history be status-only in
   the MVP?
4. **What the parent is told.** Annotate tool results on interject and on
   interrupt? Wording? Should interjections be *invisible* steering instead
   (child obeys, parent never knows)?
5. **Global footer.** Keep a one-line global status (session cost, working
   indicator) pinned at the bottom in addition to the anchored status, or go
   all-in on anchoring?
6. **Scope.** `dagger agent` only, or also `dagger llm` / `dagger shell`
   prompt mode (they share `startInteractivePromptMode`)?
7. **Mode merge.** Pursue jump-to-type (typing in nav mode routes to the
   anchored editline) in Phase 4, or keep the strict esc/i modal split?
8. **Engine attr shape.** One conversation ID per `Loop` execution vs. per
   LLM lineage (surviving across turns)? Affects whether the top-level
   conversation has one ID for the whole session or one per turn.

## Status

Draft for review — questions above are open; no implementation started.
