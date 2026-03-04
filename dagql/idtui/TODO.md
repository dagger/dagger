# TUI Refactoring TODO

Remaining areas where the TUI fights with tuist's design, ordered by
impact and dependency.

## 1. Remove dual focus tracking

**Why first:** Small, self-contained, reduces confusion for everything else.

We maintain `fe.FocusedSpan` + `fe.focusedIdx` + `st.focused` (synced
manually in `syncSpanTreeState`) AND tuist's `SetFocus`/`SetFocused`.
The `SetFocused` callback redundantly sets `s.focused` and calls
`Update()`, but `syncSpanTreeState` already does the same thing.

**Plan:**
- Remove the `Focusable` interface from `SpanTreeView` (drop `SetFocused`)
- Keep `fe.FocusedSpan` as the single source of truth
- `syncSpanTreeState` already syncs `st.focused` — that's sufficient
- Keep `tui.SetFocus(sr)` for keyboard event routing only, but stop
  relying on the `SetFocused` callback for render state

## ~~2. Remove manual scroll truncation~~ — KEEP

On review, the truncation works WITH tuist's diff model:
- Navigating down appends lines → tuist's append-start fast path
- Navigating up shrinks the tail → tuist's tail-shrink fast path
- Focus highlight change is a single-line diff

The render metadata (`selfLineCount`, `childGapCounts`, etc.) is
lightweight and only read during `renderProgressTree`. No changes
needed.

## ~~3. Remove the indentFunc fallthrough hack~~ — KEEP

On review, the fallthrough is clean: synthetic rows from
`renderErrorCause` have `Parent: nil`, so the original `fancyIndent`
correctly renders 0 ancestor bars. The `bool` return is minimal
overhead. Eliminating it would require either rewriting
`renderErrorCause` to not use `fancyIndent`, or threading depth-based
indentation through a different mechanism — not worth the churn.

## 4. Break frontendPretty.Render() into composed components — DONE

**Phase 1 (done):** Restructured `Render()` to work with `[]string`
lines throughout instead of building one giant string and splitting.
- `renderProgressTree` → `renderProgressLines` (returns `[]string`)
- Extracted `renderLogsLines`, `renderEditlineLines`, `renderFormLines`,
  `renderKeymapLines` as line-returning helpers
- `Render()` assembles lines via `append`, no string builder

**Phase 2 (done):** Sidebar → notification bubble overlays.
- Each `SidebarSection` becomes a `NotificationBubble` component
- Rendered as tuist overlays, anchored bottom-right, stacked upward
- Bordered boxes with title in top border: `╭─ Title ──╮`
- No more sidebar width stealing or lipgloss JoinHorizontal

**Deferred:** Extracting keymap/logs/editline into separate components.
These update frequently enough (every frame for logs, every focus
change for keymap) that caching gains are minimal. The line-returning
helpers already eliminate string building overhead.

## 5. Convert render functions to line-oriented output

**Why last:** Biggest refactor, touches every render function, benefits
most from stable component boundaries established above.

`renderStep`, `renderLogs`, `renderCall`, `renderStepError` etc. all
write to a `strings.Builder` via `fmt.Fprint`. `SpanTreeView.Render()`
then splits the result into lines. This write→split dance happens on
every cache miss.

**Plan:**
- Define a line-oriented output interface (append lines, not write bytes)
- Convert render functions to append to `[]string` directly
- `SpanTreeView.Render()` just collects lines, no string building
- The `prefix` parameter can be applied per-line naturally
- `renderCall`'s multi-line arg indentation becomes line-oriented too
