# Markdown Rendering Handoff

Status of an investigation into "janky" Markdown rendering in `dagger agent`.
Written to hand off to a fresh session. Nothing here is urgent; it's a catalog
of findings + a reproduction harness.

## TL;DR

- The originally-reported symptom ("Markdown given a width that's too wide, not
  accounting for leading indentation, worse with sub-agent nesting") led to a
  **real but separate** bug in the final conversation report, which is **already
  fixed** (see below). The reporter suspects there's *another* quirk lurking, and
  the demo harness confirms several.
- A scratch demo test (`TestMarkdownRenderDemo`) renders many Markdown
  constructs through the exact agent path and surfaces 5 distinct quirks. **None
  are fixed yet** — the reporter said "fix 'em all" and then paused to write this
  handoff, so start there.

## What's in the working tree right now

Run `git status` / review the diff. Pending changes:

- `dagql/idtui/conversation_report.go` — **the fix** (keep it). Adds
  `indentMessageNodeLogs` and calls it from `renderMessageNode`.
- `dagql/idtui/conversation_report_test.go` — regression test
  `TestConversationReportWrapsMarkdownForDepth` for the fix (keep it).
- `dagql/idtui/markdown_demo_test.go` — **scratch demo** `TestMarkdownRenderDemo`
  (not an assertion; it just `t.Log`s rendered output with width flags). Decide
  whether to keep it as an iteration aid or delete before committing. It's the
  fastest way to eyeball the remaining quirks.

Note: two pre-existing failures in `dagql/idtui` are unrelated to this work and
fail on the untouched tree too:
- `TestConversationReportNestsSubAgent` — supplies message spans with no
  logs/`Message`, so their names never render (fails on original code).
- `TestTelemetry/TestGolden` — `git: not found` in the sandbox.

## The already-fixed bug (for context)

The final `--full` conversation report (`renderMessageNode` in
`conversation_report.go`) rendered each surfaced message at the full content
width, then prepended `"  " * depth` to every line afterward. So a nested
sub-agent's wrapped Markdown overflowed the viewport by `2 × depth` columns.

Fix: shrink the wrap width handed to that node's log vterms by the depth indent
*before* rendering (`indentMessageNodeLogs`, which resizes the message span's
vterm and, for a tool call, its exec-output vterm). No-ops when
`contentWidth <= 0` (headless report mode, where wrapping is intentionally off).
Only runs under `fe.finalRender`, so mutating vterm widths there is safe.

This mirrors how `checks_report.go` already does it correctly — it folds the
indent into the vterm *prefix* (`logs.SetPrefix(indent + pipe + " ")`) so
wrapping accounts for it. The conversation report was the odd one out.

## The reproduction harness

`dagql/idtui/markdown_demo_test.go` → `TestMarkdownRenderDemo`. Run:

```
go test ./dagql/idtui/ -run TestMarkdownRenderDemo -v -count=1
```

It renders each construct through `Vterm.WriteMarkdown` → glamour (the exact
agent path) at two indents — a top-level 2-space gutter and an 8-space
(~3-levels-of-sub-agent) gutter — and prints each line's display width, flagging
overflow with `>>>OVER`. Adjust `width` / the prefixes to explore.

## The 5 quirks found (NONE fixed yet — this is the work)

All ultimately trace to `redraw()` in `dagql/idtui/vterm.go`:

```go
glamour.WithWordWrap(term.Width - lipgloss.Width(term.Prefix))
```

glamour wraps *text* to that width but then adds block decoration (list/quote/
code indent, the `│` quote gutter) on top — and does **not** re-apply that
decoration to soft-wrapped continuation lines, nor reserve width for it.

1. **Code blocks: soft-wrapped lines lose the code indent.** A wrapped line in a
   fenced block jumps back to the base gutter instead of staying at the code
   indent, so the block's left edge is unstable whenever a line wraps.

2. **Blockquotes: a wrapped word escapes the `│` gutter.** A continuation line
   can render with no `│`, orphaned outside the quote. It's wrap-position
   dependent (renders clean at some prefixes/widths, broken at others), so it
   flickers in and out with pane width — the classic "sometimes looks broken."

3. **`WithPreservedNewLines()` keeps source hard-wraps instead of reflowing.**
   The renderer is built with this option, so if the model hard-wraps its prose
   to ~80 cols, those exact short lines show regardless of the actual pane width
   (ragged right edge, unused width). Plain prose that *isn't* source-wrapped
   reflows fine. **Prime suspect for the everyday "feels ragged/off" sensation.**
   Decide whether preserving newlines is actually desired for agent replies.

4. **Indented-block-inside-indented-block overflows, compounding with nesting.**
   A code block inside a list item, at the nested 8-space prefix, actually ran
   over the width (`>>>OVER by 1`) and its wrapped tail dropped both indents.
   glamour's wrap width doesn't reserve room for the *combined* block
   indentation. This is the "not accounting for leading indentation, worse with
   nesting" intuition — but it lives inside glamour's block layout, not the
   report's depth indent.

5. **Cosmetic:** `#` H1 is styled (marker stripped) but `##`/`###` render their
   literal `## `/`### ` markers; list continuation lines hang-indent to the
   bullet column rather than the text column.

## Suggested starting points for the fix session

- Reproduce with `TestMarkdownRenderDemo` first; watch the `>>>OVER` flags and
  the left-edge instability on wrapped lines.
- #3 (preserved newlines) is a one-line policy question in `vterm.go`
  (`glamour.WithPreservedNewLines()` in both `redraw()` and the `Markdown.View()`
  path) — cheap to toggle and A/B.
- #1/#2/#4 are the same underlying glamour limitation (decoration not carried to
  soft-wrapped lines / width not reserved for block indent). Investigate whether
  the installed glamour version has a wrap option that reserves the block indent,
  or whether we need to post-process rendered output (re-apply the gutter/indent
  to continuation lines) or reduce the word-wrap width to leave headroom.
- The two Markdown code paths to keep in sync: `Vterm.redraw()` and
  `Markdown.View()` in `dagql/idtui/vterm.go`. Prefix width is subtracted in
  both; block indentation is not accounted for in either.
- Relevant existing tests to not regress: `TestReproMarkdownWrapIndent`,
  `TestUserPromptLeadingGutterShaded`, `TestConversationReport*`.
