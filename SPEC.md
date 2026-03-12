# Fulltext Search for idtui

## Overview

Add Vim-style `/` search to the pretty TUI frontend. Searches span names and
vterm log content. Highlights matches, navigates between them with `n`/`N`,
and scrolls matched log lines into view within nested vterms.

## UX (Vim-inspired)

| Mode | Key | Action |
|------|-----|--------|
| Nav | `/` | Enter search mode — show search input bar at bottom |
| Search input | `Enter` | Confirm search, jump to first match forward from focus |
| Search input | `Esc` | Cancel search, clear query |
| Nav (with active query) | `n` | Next match (forward, wrapping) |
| Nav (with active query) | `N` | Previous match (backward, wrapping) |
| Nav (with active query) | `Esc` | Clear search (then second Esc unzooms as usual) |

When a search is active, the keymap bar shows: `n next · N prev · esc clear`

## What gets searched

1. **Span names** — `span.Name` (the label shown in the tree)
2. **Vterm log content** — the plaintext extracted from each span's `Vterm`
   (the `Content [][]rune` rows of the underlying `midterm.Terminal`)

Matching is **case-insensitive substring**.

## Data model additions to `frontendPretty`

```go
// search state
searchActive   bool        // search mode active (query bar shown)
searchQuery    string      // current confirmed search string
searchInput    *tuist.TextInput // the search bar input component
searchMatches  []searchMatch    // ordered list of all matches
searchIdx      int              // index into searchMatches of current match (-1 = none)
```

```go
type searchMatch struct {
    spanID  dagui.SpanID
    // for log matches, the row within the vterm
    logRow  int  // -1 means the span name itself matched
}
```

## Key implementation pieces

### 1. Search bar component

When `/` is pressed in nav mode:
- Create a `tuist.TextInput` with prompt `"/"`, insert it before the keymap bar
  (same pattern as the shell's `textInput`).
- Set `searchActive = true`, focus the search input.
- On `Enter`: set `searchQuery`, call `buildSearchMatches()`, jump to first
  match, remove the search text input, return to nav mode.
- On `Esc`: cancel, clear `searchActive`, remove input, return to nav mode.

### 2. `buildSearchMatches()`

Walk `fe.rows.Order` (the flattened visible rows). For each row:
1. Check `strings.Contains(strings.ToLower(span.Name), query)` → add match
   with `logRow = -1`.
2. If the span has a `Vterm` in `fe.logs.Logs[spanID]`:
   - Access `vterm.Term().Content` (the `[][]rune` from `midterm.Terminal`).
   - For each row, convert `[]rune` to `string`, check
     `strings.Contains(strings.ToLower(line), query)`.
   - Add match with the row index.

Rebuild matches whenever `searchQuery` changes or the view is recalculated
(new spans/logs arrive).

### 3. Navigating to a match

**Span-name match (`logRow == -1`):**
- Call `fe.focus(row)` to move the cursor to that span.
- Expand parents as needed so the span is visible (same as `goErrorOrigin`).

**Log match (`logRow >= 0`):**
- Focus the span (expand parents if needed).
- Expand the span itself so logs are shown.
- **Scroll the Vterm** so the matched line is visible: set
  `vterm.Offset = max(0, logRow - vterm.Height/2)` to center the match.

### 4. Midterm: search helper (new interface)

Add a `Search` method to `midterm.Terminal` (or as a free function in a new
`midterm/search` package if we prefer not to touch the struct). Since midterm
is a dependency at `~/go/pkg/mod`, we have two options:

**Option A (preferred): Keep search logic in idtui.** No midterm changes needed.
The `midterm.Terminal.Content` field (`[][]rune`) is public. We can iterate it
directly from idtui:

```go
func searchVterm(vt *midterm.Terminal, query string) []int {
    query = strings.ToLower(query)
    var rows []int
    for i, line := range vt.Content {
        if i >= vt.UsedHeight() {
            break
        }
        if strings.Contains(strings.ToLower(string(line)), query) {
            rows = append(rows, i)
        }
    }
    return rows
}
```

This avoids needing to fork/modify midterm.

**Option B: Add `Search(string) []int` to midterm.** Cleaner API but requires
a midterm release. We can do Option A first and refactor later.

→ **Go with Option A.**

### 5. Vterm scroll-into-view

The `Vterm` wrapper in `vterm.go` already has `Offset` and `Height`. To scroll
a search result into view:

```go
func (term *Vterm) ScrollToRow(row int) {
    term.mu.Lock()
    defer term.mu.Unlock()
    // Center the target row in the viewport
    term.Offset = max(0, row - term.Height/2)
    // Clamp to valid range
    maxOffset := max(0, term.vt.UsedHeight() - term.Height)
    if term.Offset > maxOffset {
        term.Offset = maxOffset
    }
    term.needsRedraw = true
}
```

### 6. Visual highlighting of matches

Two levels:

**a) Tree row highlight:** When rendering a span tree row whose span is a
search match, apply a subtle background/style to make it visually distinct.
Check `searchMatchSpans` (a `map[dagui.SpanID]bool` built alongside
`searchMatches`) in `renderStep`/`renderToggler`.

**b) Log line highlight (stretch goal):** In `Vterm.Render`, when search is
active, apply a highlight (reverse video or background color) to the matched
substring in the rendered line. This requires modifying `Vterm.Render` to
accept an optional highlight config:

```go
type VtermHighlight struct {
    Query string
    Row   int  // -1 for all rows
}
```

For v1, we can skip in-line text highlighting and just scroll to + focus the
matching span/line. Tree-level indicators (e.g., a colored marker) are enough.

### 7. Integration with view recalculation

In `recalculateViewLocked()`, after rebuilding `fe.rows`:
- If `searchQuery != ""`, call `buildSearchMatches()` to refresh the match
  list (spans may have appeared/disappeared).
- Preserve `searchIdx` if the current match still exists, otherwise reset to 0.

### 8. Keymap bar updates

In `fe.keys()`, when `searchQuery != ""` and not in editline mode:
- Add bindings for `n` (next match), `N` (prev match).
- Change `esc` help to "clear search" (first press clears search, not unzoom).

## File changes summary

| File | Changes |
|------|---------|
| `dagql/idtui/frontend_pretty.go` | Add search state fields, `/` key handler, `n`/`N`/`Esc` search nav, `buildSearchMatches()`, search bar lifecycle, integrate with `recalculateViewLocked`, update `keys()` |
| `dagql/idtui/vterm.go` | Add `ScrollToRow(row int)` method |
| `dagql/idtui/search.go` (new) | `searchMatch` type, `searchVterm()` helper, `buildSearchMatches()`, match navigation logic |
| `dagql/idtui/keymap.go` | No structural changes; search bindings added via `keys()` |

## No midterm changes needed

All search is done by reading the public `Content [][]rune` field of
`midterm.Terminal` via `Vterm.Term()`. No new interfaces or methods are
needed in midterm for v1.

## Implementation order

1. Add search state fields to `frontendPretty`
2. Create `search.go` with `searchMatch`, `searchVterm()`, `buildSearchMatches()`
3. Add `ScrollToRow` to `Vterm`
4. Wire up `/` key → search bar (TextInput lifecycle)
5. Wire up `Enter` → confirm + jump to first match
6. Wire up `n`/`N` → next/prev match navigation
7. Wire up `Esc` → clear search (when query active)
8. Add search match indicators in span tree rendering
9. Integrate `buildSearchMatches` into `recalculateViewLocked`
10. Update keymap bar to show search bindings
