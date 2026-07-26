---
name: tui-surfacing
description: How the Dagger pretty TUI surfaces deep spans (checks, tests, LLM conversations) to the top level — the reveal-independent SurfacedX computation, live promotion via passthrough, and the report-vs-live render split. Use when working in dagql/idtui or dagql/dagui: adding a new top-level "surfaced" section, debugging why a span does or doesn't appear at the top level, or reasoning about reveal / passthrough / Boundary / Encapsulate / rollup.
---

# TUI Surfacing

"Surfacing" is how the pretty frontend promotes spans buried deep in the trace
tree up to the **top level** — so `dagger trace` on a CI run leads with its
checks (sub-checks and tests rolled up beneath), and `dagger shell --model`
leads with the LLM conversation, instead of the raw connect/load/exec tree.

There are **two independent layers**. Get this distinction first — most
confusion comes from conflating them:

1. **Final report — reveal-independent.** `DB.SurfacedX()` walks *all* spans by a
   marker attribute and builds a tree from scratch, ignoring `reveal`. Rendered
   as a dedicated section in `renderFinalReport`. This is what `dagger trace`
   and the end-of-run report use.
2. **Live tree — reveal-driven.** Spans set `reveal=true`; the frontend
   *promotes* them to the top by marking the root span `passthrough`. Reuses the
   normal row/tree machinery. This is what the interactive TUI shows mid-run.

The flag that switches render paths is `fe.finalRender` (in `frontendPretty.Render`,
`dagql/idtui/frontend_pretty.go`).

## Marker attributes

Emitters tag spans with OTel attributes (constants in `github.com/dagger/otel-go`
plus `engine/telemetryattrs`). `ProcessAttribute` in `dagql/dagui/spans.go` maps
them onto `SpanSnapshot` fields. The load-bearing ones:

- `reveal` (`UIRevealAttr`) → `Reveal` — bubble this span up toward the root (live).
- `passthrough` (`UIPassthroughAttr`) → `Passthrough` — hide this span; show its
  revealed spans as the top-level rows instead of its children.
- `encapsulate` / `boundary` (`UIEncapsulateAttr` / `UIBoundaryAttr`) →
  `Encapsulate` / `Boundary` — containment walls; stop reveal bubbling and
  reveal-independent surfacing from crossing them (used by test fixtures so a
  check/LLM run they drive on purpose doesn't surface to the outer trace).
- `internal` (`UIInternalAttr`) → `Internal` — machinery; hidden by default.
- `rollup.logs` / `rollup.spans` (`UIRollUpLogsAttr` / `UIRollUpSpansAttr`) →
  `RollUpLogs` / `RollUpSpans` — pull descendant logs/spans into this row.
- `CheckNameAttr` → `CheckName`, `CheckPassedAttr` → the pass flag — makes a span
  a **check**.
- `LLMRoleAttr` → `LLMRole` (`LLMToolAttr` → `LLMTool`, `UIMessageAttr`,
  `UIActorEmojiAttr`) — makes a span an LLM **message**.

Where they're emitted:

- Checks: `core/modtree.go` (`CheckName` + `Reveal()` + rollup + `CheckPassed`).
- LLM messages: `core/llm.go` (`emitMessageSpan` / `emitUserMessageSpan` /
  `emitAssistantMessageSpan`: `LLMRole` + `Reveal()` + `UIMessage`/emoji; the
  system prompt is marked `Internal`) and `core/llm_display.go` (`displayPhases`
  streams the live per-block spans with the same attrs).

## Reveal bubbling (live)

When a span has `Reveal`, it adds itself to each ancestor's `RevealedSpans` set,
walking up until it hits a `Boundary`, `Encapsulate`, or another `Reveal`
ancestor (`dagql/dagui/spans.go`). So a top-level check/turn reaches the root's
`RevealedSpans`; a nested one (e.g. a sub-agent's turn under a tool-call span,
itself revealed) stops at that parent and nests under it.

## Reveal-independent SurfacedX (report)

The pattern lives in `dagql/dagui/checks.go` (`SurfacedChecks` / `CheckNode`) and
`dagql/dagui/conversation.go` (`SurfacedConversation` / `MessageNode`). Both:

- Walk every span; keep those with the marker (`CheckName != ""` /
  `LLMRole != ""`).
- **Containment**: a span surfaces only if its ancestor chain reaches
  `db.RootSpan` with no `Boundary`/`Encapsulate` in between. A chain **severed**
  before the root (an unreceived placeholder, or a reparenting seam the
  incremental fetch never loaded) can't be proven boundary-free, so it's treated
  as contained too — that's why fixture checks stay hidden even when their
  `Boundary` span wasn't loaded.
- **Nest** each node under its nearest surfaced ancestor of the same kind.
- **Cache** per `db.mutations` (rebuilt only when span data changes; a render
  frame reads it many times).

They differ where the domain differs:

| | checks | conversation |
|---|---|---|
| dedup | by `CheckName` | none — each span is a node |
| order | failed-first, then name | start time (a sequence) |
| extra | — | skips `Internal` (system prompt) |

`HasChecks()` / `HasConversation()` (in `types.go` / `conversation.go`) are the
cheap "did any surface" checks used by live promotion.

## Render paths

`frontendPretty.Render` (`dagql/idtui/frontend_pretty.go`):

- **`fe.finalRender`** → `renderFinalReport`, which calls `checksReport` then
  `conversationReport` (`dagql/idtui/checks_report.go`,
  `conversation_report.go`). Each returns `nil` when zoomed and falls back to the
  raw progress tree when nothing surfaces.
- **live** → `renderProgressLines` (the reveal-driven tree) + chrome. Collapsed
  check rows show an inline rollup via `renderInlineChecks` /
  `shouldRenderInlineChecks`.

## Live promotion

`promoteChecksLocked` / `promoteConversationLocked` (called from
`recalculateViewLocked`, `frontend_pretty.go`): when `HasChecks()` /
`HasConversation()` and the root isn't itself that kind, set
`db.RootSpan.Passthrough = true` and default the zoom to the primary (root) span.
Then the passthrough branch of `DB.RowsView` (`dagql/dagui/types.go`) iterates
`RootSpan.RevealedSpans` **instead of** its children — so the revealed
checks/turns become the top-level rows and the connect/load noise disappears.
This is the mechanism that replaced `dagger shell`'s old manual `SetPrimary` zoom.
`zoomKind` (`dagql/idtui/frontend_trace_policy.go`) distinguishes
zoomRoot/zoomCheck/zoomTest/zoomSpan for the zoomed views.

## Recipe: add a new surfaced kind

1. **Emit**: tag the spans with a marker (a dedicated attr, or key on an existing
   `SpanSnapshot` field the way checks key on `CheckName`) plus `telemetry.Reveal()`
   and rollup attrs. Confirm the field lands via `ProcessAttribute`.
2. **dagui**: add `DB.SurfacedX() []*XNode` + `HasX()` mirroring
   `conversation.go`; add `surfacedX*` cache fields to `dagql/dagui/db.go`.
3. **idtui**: add `xReport` / `renderXSection` / `renderXNode` mirroring
   `conversation_report.go`; wire it into `renderFinalReport`, and suppress the
   progress-tree fallback when it surfaces.
4. **Live**: add `promoteXLocked` mirroring `promoteConversationLocked`; call it
   from `recalculateViewLocked`.
5. **Test**: unit the tree (order / nest / containment / cache) like
   `dagql/dagui/conversation_test.go`; render + promote like
   `dagql/idtui/conversation_report_test.go` (`ImportSnapshots` →
   `recalculateViewLocked` → call the report fn / assert `RootSpan.Passthrough`
   and top-level rows).

## Gotchas

- **Report and live can legitimately disagree.** The report is reveal-independent
  (walks all spans by marker); the live tree needs reveal to actually reach the
  root. On a stored / incrementally-fetched trace the bubbling may be severed, so
  the report still surfaces things the live tree wouldn't — that asymmetry is the
  whole reason the report layer exists.
- **Surfaced rows render their content from span *logs*, and the report must
  pre-fetch them.** A message/tool-call's text, arguments, and output live in the
  span's logs (`emitMessageSpan` / `displayPhases` write them via `SpanStdio`),
  not attributes. `renderFinalReport` renders once, so `recalculateViewLocked`
  eagerly fetches each surfaced span's logs first — in **both** report and
  interactive modes, since the final report also renders on interactive exit.
  Fetch **`descendants=false`** (own logs only): a tool call's execution output
  is a nested exec span (`LLMTool`, no `LLMRole`) whose logs Cloud's `RollUpLogs`
  descendant roll-up returns empty for, so fetch that child's own logs directly
  (`toolCallExecSpan`). Do it **before** the report-only failure fetch, or a
  failed row's `descendants=true` roll-up wins the `requestLogs` dedup and the
  args vanish.
- Report sections return `nil` when zoomed; the zoom views render themselves.
- Section headings render `== X ==` under an agent, bold for humans
  (`reportHeadingLine` / `RunningInAgent`).
- Build filter for this repo: `go build ./... 2>&1 | grep -v my-module`.
- Handle-form IDs panic on `.Digest()`/`Field()` — guard before reading call
  digests off a span (see `stableIDDigest` in `core/mcp.go`).
