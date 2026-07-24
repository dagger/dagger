# Agent v2: The Focus-Anchored Conversation UI

A plan for a dramatically different terminal UI paradigm for `dagger agent`:
the input prompt is no longer pinned to the bottom of the screen — it is
**anchored to the focused message**. Everything else (status line, context
gauge, keymap, pause, interjection) re-anchors with it. Because focus can
land on *any* message — including a sub-agent's message running inside an
outer conversation's tool call — the prompt becomes a portal into that
specific conversation: submit interjects into it, Ctrl+C pauses *its* loop
(decide, steer, resume), and the context bar shows *its* fill level at that
moment in time.

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
  - [6. The conversation tree (branching from the anchor)](#6-the-conversation-tree-branching-from-the-anchor)
  - [7. Top-level loop unification (option)](#7-top-level-loop-unification-option)
- [Phases](#phases)
- [Risks & Edge Cases](#risks--edge-cases)
- [Resolved Decisions](#resolved-decisions)
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
  branch point if it's history.
- Ctrl+C **pauses** *the focused conversation's* loop — it does not kill it.
  While paused you decide: interject (type and submit — the message lands and
  the loop resumes), just resume (empty submit — no dumb "carry on" message
  required), or escalate (Ctrl+C again pauses/cancels the parent, walking up
  the hierarchy). Focus on a sub-agent → only that loop pauses; the parent
  turn is simply blocked on its tool call, losing nothing.
- The anchored status line time-travels: context gauge, step cost, tokens, and
  model *as of the focused message*, so scanning up the transcript is also
  scanning the context-usage history.
- The conversation is a **tree, not a line** — every branch ever taken is
  preserved, visible at its branch point, and switchable in place.

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
      ⏎ interject triage · ctrl+c pause triage · esc back
… 23 lines below …
```

- `⏎` queues the message into the *triage* loop; it's consumed at its next
  step boundary and renders inline in the nested transcript. No pause needed
  for a drive-by steer.
- `ctrl+c` pauses only the triage loop (see Scenario 4). The outer turn is
  simply blocked on its `delegate` tool call — nothing is cancelled.
- The breadcrumb (`agent ▸ delegate ▸ triage`) always names the target, so
  there is never ambiguity about who you're talking to or what Ctrl+C pauses.

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

### Scenario 4: pause, decide, resume

While the triage sub-agent is focused and running, `ctrl+c` pauses it: the
in-flight step is cancelled and the loop holds, waiting for you. The paused
state is unmistakable at the anchor:

```
  ▶ 🤖 Three failures, all in quote handling. Investigating the lexer…
    ┌───────────────────────────────────────────────────────────┐
    │ ❯ █                                                       │
    └───────────────────────────────────────────────────────────┘
      ⛬ agent ▸ delegate ▸ triage  ⏸ paused   [██▁▁▁▁▁▁▁▁] 18%/200k
      ⏎ resume · type+⏎ interject & resume · ctrl+c pause parent
```

Three ways out, none of them awkward:

- **Type and `⏎`** → the message is appended to the triage conversation and
  the loop resumes with it. Steering, not restarting.
- **Empty `⏎`** → resume as if nothing happened. No "carry on" filler message
  pollutes the transcript.
- **`ctrl+c` again** → escalate: the *parent* conversation pauses too,
  walking up the hierarchy one level per press. At the root, a further
  `ctrl+c` aborts the turn — so at top level, `ctrl+c ctrl+c` lands close to
  today's muscle memory. The keymap bar always names what the next press
  will do.

Focus at top level: `ctrl+c` pauses the top-level turn the same way (between
steps, purely client-side). Nothing is lost for users who never navigate.

### Scenario 5: the conversation tree

Every message you ever branched from shows its branch point inline. Focus a
past user message, recall it into the editline (`alt+↑`), reword, submit —
that's a new branch. Old branches stay alive and switchable:

```
🧑 Refactor the parser and get the tests green        ⑂ 3
    ┌───────────────────────────────────────────────────────────┐
    │ ❯ Refactor the parser using the new grammar module█       │
    └───────────────────────────────────────────────────────────┘
      ⛬ agent · branch 2/3 · [ ] switch branch · ⏎ new branch from here
```

`[` / `]` flip the transcript below the branch point between siblings —
the rest of the tree re-renders in place. `⏎` creates branch 4. The session
file stores the whole tree, so `-r` resumes any of it.

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

Give every LLM conversation a stable identity so the UI can address it.

- Sub-agent loops mint a conversation ID per `Loop` execution; the top-level
  conversation carries a session-stable ID per branch (minted by the CLI,
  stable across turns), so the breadcrumb root doesn't churn and the tree UI
  (§6) can correlate live spans with session branches. Every message span
  the conversation emits is stamped:

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

A session-scoped registry of running conversations, with a small state
machine per loop: `running ⇄ paused → done`.

**Registry.** The per-session server (the state shared by the main client and
all nested module clients) gains an `LLMLoopRegistry`. Each `Loop` execution
registers `{conversationID, label, parentID, state, cancelStep, mailbox}` on
entry and deregisters on exit. Nested module calls share the session server,
so a sub-agent loop deep inside module sandboxes is still reachable from the
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
  state: LLMConversationState!

  """
  Pause the loop: cancel the in-flight provider call (that step's partial
  output is discarded; nothing is lost from history) and hold before the
  next step, awaiting resume/interject/abort. The caller's tool call simply
  blocks; nothing upstream is cancelled.
  """
  pause: Void

  """
  Resume a paused loop. The held step re-runs from history — resuming
  without interjecting is a clean no-op on the transcript.
  """
  resume: Void

  """
  Append a user message to the conversation. On a paused loop it lands
  immediately and implies resume. On a running loop it queues and is
  consumed at the next step boundary (drive-by steering; no pause needed).
  """
  interject(prompt: String!): Void

  """
  End the loop now, returning its accumulated state to its caller.
  """
  abort: Void
}

enum LLMConversationState {
  RUNNING
  PAUSED
}
```

**Loop changes** (`core/llm.go:1756`):

- Each iteration steps under a per-loop `context.WithCancelCause` derived
  from the tool ctx; `pause` cancels just that step-scoped ctx. Because the
  loop resends from message history, a cancelled step is safely re-runnable
  on `resume` — the pre-step state is untouched (the same property today's
  interrupt-is-not-an-error branch at `core/llm.go:1785-1790` relies on).
  Cost of a pause: the cancelled step's tokens are re-spent on resume; in
  exchange, pause is *instant*, which is what makes it feel like pressing
  pause.
- **Safe-point refinement**: a step is provider-call → tool batch, and the
  assistant message is only committed at step end (`core/llm.go:1594`).
  Pause cancels immediately during provider *streaming* (nothing but tokens
  lost), but during the *tool batch* it holds at the batch's end instead of
  cancelling — tools are where side effects live, and dagql caching only
  softens, not eliminates, re-runs. The UI shows `⏸ pausing…` until the
  hold lands.
- Before each step (and while paused), drain the mailbox — interjected
  prompts append as user messages, rendering as `🧑` spans inside the nested
  transcript exactly like top-level prompts.
- While paused, the loop blocks on a resume/interject/abort signal (or
  ancestor ctx cancellation — a paused loop still dies cleanly if the whole
  turn or session goes away). The tool-call span stays running; a
  `dagger.io/llm.conversation.paused` span event (or status attr) lets the
  TUI mark the pause without polling.
- `abort` returns accumulated state to the caller — the tool call gets the
  child's last reply so far.

**Invisible steering.** Interjections and pauses leave *no annotation* on
the tool result the parent sees. The child simply obeys and its final answer
reflects the guidance; the parent never knows a human was in the room. (The
interjected message is still visible to *you* in the child's transcript, and
persists in the child's history for resume — it is only the parent that is
kept unaware.)

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
- **Input-vs-nav collapse (jump-to-type)**: with the prompt attached to
  focus, the two modes partially merge — plain typing in nav mode routes
  into the anchored editline, and `↑`/`↓` on an *empty* editline move focus
  between messages instead of recalling input history. "Navigate + talk"
  becomes one fluid motion. Input-history recall goes away entirely: the
  transcript *is* the history — focus a past user message and press `alt+↑`
  to recall its text into the editline for rewording (today's queued-message
  recall binding, generalized). Combined with §6 this is the
  edit-and-resubmit flow. Care needed: multi-line editing (`↑` inside a
  multi-line draft must move within the draft, only escaping at the top
  edge), and `hjkl` stay nav-mode-only.
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
  produced it, and the running/paused state of the target conversation.
- **Global footer** (kept, one line, pinned at the bottom): session-wide
  totals — cumulative cost across all models and sub-agents, token rollup,
  `● working` — today's `StatusLine` data minus the context gauge. Overall
  consumption is a session property and stays put; **context occupancy is a
  message property and moves with focus.** The two never show the same
  number twice.

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
displays the target's breadcrumb and state; every conversation-verb key in
the keymap bar names its target (`ctrl+c pause triage`).

| Key | Target running | Target paused | Target is history |
|---|---|---|---|
| `⏎` (text) | interject: queue into that loop, consumed at next step boundary (top level: today's queue) | interject & resume | branch from here (§6, with confirm) |
| `⏎` (empty) | — | **resume** | — |
| `ctrl+c` | **pause** that loop (top level: pause turn, client-side) | **escalate: pause parent** (at root: abort turn) | clear input (today) |
| `alt+↑` | recall queued message (today) | recall queued message | recall focused message text for rewording |
| `[` / `]` | — | — | switch sibling branch at branch point |
| `esc` / `i` | nav ↔ input toggle, unchanged | unchanged | unchanged |

Notes:

- **Pause is the only destructive-ish gesture and it destroys nothing** — it
  cancels an in-flight provider call whose step re-runs on resume. `abort`
  has no dedicated key in the MVP; it is reached by escalation (root abort)
  or a slash command (`/abort`), keeping accidental kills hard.
- Escalation is state-based, not timing-based: `ctrl+c` on an
  already-paused target walks one level up the `conversation.parent` chain.
  No escalation window to race against; the keymap bar always shows what
  the *next* press will do.
- Global quit stays `ctrl+d` on empty input
  (`frontend_pretty.go:3914-3918`); never Ctrl+C, unchanged from today.

### 6. The conversation tree (branching from the anchor)

Prior art exists: nav-mode `b` branches from a message via
`ShellHandler.BranchFromID` + `dagger.io/llm.call.digest`, and branch
summaries already work. v2 promotes branching from an escape hatch to a
structural feature: **the session is a tree, every branch is preserved, and
the tree is always visible in the UI.**

**Creating a branch.** The anchored prompt makes it spatial: focus a past
top-level message, type (or `alt+↑` to recall and reword the message
itself), submit → confirm (`⏎ again to branch from here`) → a new branch
seeded at that point. No mode, no command.

**Seeing and switching branches.** A message with more than one child branch
gets an inline `⑂ N` marker; the anchored status shows `branch 2/3` when
focus is inside a multi-branch region; `[` / `]` swap the transcript below
the branch point between siblings, re-rendering in place. Switching is
non-destructive — it just changes which lineage is "checked out" for the
live end. (A future tree overlay — a compact `git log --graph`-style map of
the session — is a natural add-on, but inline markers + in-place switching
are the MVP of "beautiful".)

**Session format v2.** Today a session file stores a single `LLMID` plus an
optional `Branch` label (`sessionMetadata`, `internal/cmd/dagger/llm.go:707`;
branching saves a *separate file*). v2 stores the tree in one file:

```json
{
  "name": "refactor the parser",
  "created_at": "…",
  "branches": [
    {
      "id": "b1", "parent": "", "branch_point": "",
      "llm_id": "<portable recipe ID of this branch's head>",
      "model": "…", "created_at": "…"
    },
    {
      "id": "b2", "parent": "b1",
      "branch_point": "<llm.call.digest of the message branched from>",
      "llm_id": "…", "model": "…", "created_at": "…"
    }
  ],
  "head": "b2"
}
```

Portable recipe IDs already share structure (a child branch's ID embeds the
lineage up to the branch point), so storing one ID per branch is naturally
prefix-deduplicated at the recipe level; explicit delta/dedupe encoding of
the JSON is overkill and a non-goal. Resume (`-r`) loads the whole tree,
replays the `head` branch, and keeps the others switchable; the picker shows
one entry per session (not per branch). Migration: v1 files (single
`llm_id`) load as a one-branch tree; sibling v1 branch files with matching
lineage can be adopted lazily.

**Scope limits.** Sub-agent history is non-branchable in the MVP (the
sub-conversation belongs to the module's execution, not the session) — the
prompt anchored there is informational once that loop has ended. Branch
switching while the live end is running pauses first (you're moving the
head out from under a loop).

### 7. Top-level loop unification (option)

Today the top-level turn is a *client-driven* loop
(`LLMSession.WithPrompt`) while sub-agents are *engine* `Loop`s. Two paths:

- **A (MVP): keep both.** The client loop keeps its queued-message /
  auto-compact / auto-save behavior; only the *UX* is unified (submit at top
  level uses the existing queue, submit on a child uses `interject`). Least
  churn, some duplicated semantics. Notably, **pause/resume of the top-level
  turn needs no engine work at all**: the client loop cancels its in-flight
  `Step().Sync(ctx)` (completed steps are already preserved and auto-saved)
  and holds before issuing the next step; resume just steps again, since
  `HasPending` is still true.
- **B (later): top level becomes an engine Loop too.** The CLI starts the
  turn as `loop` and controls it exclusively via the control plane; queued
  messages, and eventually auto-compact, move engine-side. One mechanism
  everywhere, and `dagger agent` turns become pausable/steerable by
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

**Phase 1 — Focus-anchored prompt + top-level pause.**
Prompt block spliced at the focus crop point; anchored breadcrumb + status;
bottom chrome reduced to the global footer; queued message rendered inline.
Ctrl+C at top level becomes **pause** (client-side: cancel in-flight step,
hold, empty-`⏎` resume, `ctrl+c` again aborts) — the pause paradigm lands
here, before any engine changes beyond Phase 0. This is the visual and
muscle-memory shift.

**Phase 2 — Context archaeology.**
Anchored context gauge time-travel using Phase 0 attrs (with
`MetricsBySpan` fallback); per-step growth display; context-blame inline
markers.

**Phase 3 — Loop control plane + child targeting.**
Engine registry; `pause`/`resume`/`interject`/`abort` API with the
safe-point rules; loop mailbox drain; CLI wires submit/Ctrl+C to the focused
conversation with state-based escalation. **This is the magic trick.**

**Phase 4 — The conversation tree.**
Branch-from-anchor with confirm; reword-recall (`alt+↑` on history); `⑂ N`
markers; `[`/`]` in-place branch switching; session format v2 (tree file,
v1 migration); resume loads the whole tree.

**Phase 5 — Polish & unification.**
Jump-to-type mode merge (typing in nav mode routes to the anchored
editline); branch tree overlay; top-level loop unification (7B);
plain-frontend/report parity notes; Cloud trace rendering of conversation
attrs.

## Risks & Edge Cases

- **Race: verb vs loop exit.** The loop may finish between focus and
  pause/interject. The verb returns a "conversation ended" error → the UI
  shows the conversation finished and offers to requeue the message to the
  parent (or the top level) rather than dropping it.
- **Paused-loop liveness.** A paused child blocks its parent's tool call
  indefinitely — that is the feature, but the UI must make paused loops
  loud (anchored `⏸ paused` badge + a global-footer indicator listing any
  paused conversations, so one is never forgotten off-screen). Paused loops
  still die cleanly with ancestor cancellation and session close. Provider
  calls are not held open across a pause (the step is cancelled), so
  nothing upstream times out.
- **Parallel sub-agents.** Multiple loops run concurrently under one turn
  (parallel tool calls). Focus disambiguates naturally; the registry lists
  all; the breadcrumb prevents mis-sends.
- **Deep nesting.** Sub-agents of sub-agents work by construction
  (parent-chain attrs + registry entries per loop); breadcrumbs truncate in
  the middle (`agent ▸ … ▸ triage`) at narrow widths.
- **Pause re-run cost & side effects.** A pause during provider streaming
  re-spends that step's tokens on resume (accepted: instant pause is worth
  it). Tool side effects are protected by the safe-point rule (§2): the
  tool batch completes before the hold, so tools never re-run due to pause.
- **Trust boundary.** Control verbs must be main-client-only; module code
  must not steer, pause, or kill sibling conversations. Same enforcement
  pattern as `terminal`/interactive.
- **Renderer regressions.** The splice must keep the flowing-mode contract
  (no mouse handlers, over-tall frames, cursor math via
  `RenderResult.Cursor`). Golden tests exist for the pretty frontend
  (`frontend_pretty_test.go`, `golden_test.go`) and the TUI console
  (`DAGGER_TUI_CONSOLE`) enables scripted QA of nav/anchor/pause flows.
- **Session resume.** Interjections are ordinary `withPrompt`s in the child's
  history; the *parent's* history only contains the tool result, so a resumed
  session replays exactly what the models saw. The tree format (§6) is the
  only persistence change, with v1 migration.
- **Old engine / plain frontend.** Version-skew degradation per §2;
  `frontend_plain` and the final report ignore anchoring entirely (the
  conversation report already renders nested transcripts).

## Resolved Decisions

Answers folded into the sections above, recorded here for traceability:

1. **Pause-steer-resume over interrupt-and-return** — Ctrl+C pauses the
   focused conversation; empty submit resumes; no filler messages. (§2, §5)
2. **State-based escalation** — Ctrl+C on an already-paused target walks up
   the hierarchy; at the root it aborts the turn. (§5)
3. **Branch from history, with a first-class UI** — submit on a past message
   branches; branches get inline markers and in-place switching. (§6)
4. **Invisible steering** — no annotations on tool results; the parent never
   learns a human intervened. (§2)
5. **Both status scopes** — session-wide footer for consumption; per-message
   anchored context meter. (§4)
6. **Scope: `dagger agent`** — shared plumbing may benefit `llm`/`shell` for
   free, but they are not design targets.
7. **Jump-to-type, and focus replaces input history** — `↑`/`↓` on empty
   editline move focus; `alt+↑` recalls the focused message for rewording;
   editline history recall is dropped. (§3)
8. **The full conversation tree is preserved and always visible** — session
   format v2 stores the tree; recipe-ID prefix sharing is dedupe enough;
   explicit delta encoding is a non-goal. (§6)

## Open Questions

Smaller, next-round questions; resolutions get folded into the sections
above.

1. **Pause safe-point rule.** Proposed: cancel instantly during provider
   streaming, hold after the in-flight tool batch otherwise (§2). OK, or
   should pause also rip through running tools (accepting re-runs)?
2. **Abort reachability.** Escalation-at-root plus a `/abort` command, with
   no dedicated abort key — hard enough to hit accidentally, easy enough
   when needed?
3. **Branch-switch keys.** `[` / `]` for sibling switching (free in both
   modes today) — any conflict with planned bindings?
4. **Jump-to-type timing.** Phase 5 as planned, or pull into Phase 1 so the
   paradigm launches with the merged mode from day one?
5. **Paused-loop guardrail.** Should a paused sub-agent left unattended
   (e.g. > N minutes) surface a reminder, auto-resume, or just sit there
   forever by design?

## Status

Draft v2 — first-round decisions folded in; open questions are refinements,
not blockers. No implementation started.
