# Fulltext Search for idtui

## Status

### Done

- **Core search infrastructure** (`search.go`): `/` enters search mode with
  a TextInput prompt, `Enter` confirms, `n`/`N` navigate matches (wrapping),
  `Esc` clears. Searches span names + vterm log content (case-insensitive
  substring). Match state auto-refreshes as new spans/logs arrive.

- **Match navigation**: Navigating to a span-name match focuses the row and
  expands ancestors. Navigating to a log match also expands the span and
  calls `Vterm.ScrollToRow` to center the matched line.

- **Keymap bar integration**: Shows `/ search`, `n next (M/N)`, `N prev`,
  `esc clear search` bindings when a search is active.

- **Title-line highlighting** (`highlight.go`): ANSI-aware `highlightANSI()`
  that skips escape sequences and matches on visible text. Replays all prior
  ANSI state after highlight end to preserve faint/bold/color formatting.
  Applied to span title lines in `SpanTreeView.Render`.

- **Separate title vs log rendering**: `renderRowContent` split into
  `renderStep` (title) + `renderRowContentRest` (logs/errors/debug) so
  SpanTreeView can highlight only the title, avoiding double-highlighting
  with the vterm's own search overlay.

- **Vterm search state**: `SetSearchHighlight(query, currentRow)` on Vterm,
  with `SearchQuery`/`SearchCurrentRow` fields. `syncSearchState()` propagates
  to all vterms and calls `.Update()` on affected SpanTreeViews.

- **Repaint correctness**: `dirtySearchTrees()` diffs previous vs current
  match span sets, calling `.Update()` on all affected components.

### Broken: Vterm inline highlighting

The ANSI post-processing approach in `Vterm.Render` (calling `highlightANSI`
on rendered line output) has fundamental issues:

- **Byte vs rune mismatch**: `highlightANSI` works on UTF-8 bytes, but
  midterm's content is `[]rune` and format regions count **columns** (runes).
  Multi-byte characters cause position drift between the query match and the
  ANSI output, producing highlights that start/end at wrong positions.

- **Fragile ANSI parsing**: Post-processing rendered output requires
  perfectly parsing all ANSI sequences (CSI, OSC, etc.) to separate visible
  text from formatting. Edge cases abound.

- **Format state restoration**: After ending a highlight, we must restore the
  full cumulative ANSI state. The current approach (replay all prior
  sequences) is verbose and may interact poorly with complex formatting.

## Next: Native midterm search support

The right fix is to move search highlighting into midterm itself, where it
operates on the structured **rune content + format canvas** rather than
post-processing ANSI byte streams.

### Design

Midterm already has the perfect layering:

- `Screen.Content [][]rune` — the character data
- `Screen.Format *Canvas` — linked-list `Region`s per row with `Format` structs
- `renderLine()` — iterates regions and emits ANSI sequences

Add a **search overlay** that `renderLine` composites on top of the format
canvas during rendering. This operates at the rune/column level, completely
sidestepping ANSI parsing issues.

### New midterm API

```go
// SearchHighlight represents a highlighted range on a single row.
type SearchHighlight struct {
    Col, End int    // column range [Col, End)
    Current  bool   // true = current match (distinct style)
}

// Search state on Terminal
type Terminal struct {
    // ...existing fields...

    // SearchHighlights holds per-row highlight ranges, keyed by row index.
    // Set by the caller; consulted by renderLine during rendering.
    SearchHighlights map[int][]SearchHighlight
    // SearchMatchStyle is the Format override for non-current matches.
    SearchMatchStyle Format
    // SearchCurrentStyle is the Format override for the current match.
    SearchCurrentStyle Format
}
```

### renderLine integration

In `renderLine`, after determining the format for a character at `(row, col)`,
check if any `SearchHighlights[row]` range covers `col`. If so, override
`Bg` and `Fg` from the match/current style. This is O(highlights-per-row)
per character, but search highlights are sparse.

Alternatively, for better perf, pre-split format regions against highlight
ranges at the start of `renderLine`.

### Search + step-through API

```go
// Search finds all occurrences of query (case-insensitive) in Content
// and populates SearchHighlights. Returns the total match count.
func (vt *Terminal) Search(query string) int

// SearchClear removes all search highlights.
func (vt *Terminal) SearchClear()

// SearchSetCurrent marks the match at the given index as "current"
// (receives CurrentStyle). Returns the (row, col) of that match.
func (vt *Terminal) SearchSetCurrent(idx int) (row, col int)
```

The `Search` method iterates `Content[0:UsedHeight()]`, finds all
case-insensitive substring matches (rune-level), and builds the
`SearchHighlights` map. Each match is stored in an ordered list for
`SearchSetCurrent` to index into.

### idtui integration changes

Once midterm has native search:

1. **Remove `highlightANSI` from Vterm.Render** — no more ANSI post-processing.
   Delete `SearchQuery`/`SearchCurrentRow` fields from Vterm. Delete
   `highlight.go` (or keep for title-line highlighting only).

2. **`Vterm.SetSearchHighlight` calls `vt.Search(query)`** and
   `vt.SearchSetCurrent(idx)` on the underlying midterm terminal.

3. **`searchVtermRows` uses midterm's search results** instead of
   independently scanning Content.

4. **Title-line highlighting stays in idtui** — span names aren't in a
   midterm terminal, so `highlightANSI` (or a simplified version) remains
   for the 1-line title. This is reliable since title lines are short and
   simple.

### Implementation order

1. Add `SearchHighlight` struct and fields to `midterm.Terminal`
2. Implement `Search()`, `SearchClear()`, `SearchSetCurrent()` on Terminal
3. Modify `renderLine()` to composite search highlights over format regions
4. Add tests in midterm
5. Bump midterm dependency in dagger, wire up Vterm to use native search
6. Remove ANSI post-processing from Vterm.Render
7. Keep `highlightANSI` for title-line highlighting only (or simplify it)

## File inventory

| File | Role |
|------|------|
| `~/src/midterm/terminal.go` | Search state fields, `Search`/`SearchClear`/`SearchSetCurrent` |
| `~/src/midterm/render.go` | `renderLine` overlay compositing |
| `~/src/midterm/search.go` (new) | Search algorithm, match storage |
| `dagql/idtui/search.go` | Match navigation, `buildSearchMatches`, `syncSearchState` |
| `dagql/idtui/vterm.go` | `SetSearchHighlight` → delegates to midterm |
| `dagql/idtui/highlight.go` | Title-line ANSI highlighting (kept, simplified) |
| `dagql/idtui/frontend_pretty.go` | Key bindings, search bar, SpanTreeView title highlighting |
