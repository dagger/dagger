package idtui

import (
	"context"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
)

// remoteLogRow marks a match a source's search found in a span's logs that
// the frontend does not hold (yet): navigating to it reads them.
const remoteLogRow = -2

// LogSearchFunc searches a source's display text for pattern, a
// case-insensitive substring, and returns the records that match, by the
// trace, span and digest the frontend routes them by.
type LogSearchFunc func(ctx context.Context, pattern string) ([]LogTarget, error)

// logSearchTimeout bounds a source's search.
const logSearchTimeout = 30 * time.Second

func (fe *frontendPretty) SetLogSearch(traceID dagui.TraceID, search LogSearchFunc) {
	fe.dispatch(func() {
		if fe.logSearches == nil {
			fe.logSearches = make(map[dagui.TraceID]LogSearchFunc)
		}
		fe.logSearches[traceID] = search
	})
}

// searchLoadedOnly reports whether a search covers only the logs loaded:
// some provider reads logs on demand from a source with no search (Cloud).
func (fe *frontendPretty) searchLoadedOnly() bool {
	if fe.logProvider != nil && fe.logSearches[dagui.TraceID{}] == nil {
		return true
	}
	for traceID := range fe.traceLogProviders {
		if fe.logSearches[traceID] == nil {
			return true
		}
	}
	return false
}

// startLogSearch searches the sources of the logs read on demand for query,
// so that matches cover the spans not loaded. Results land as remote
// matches (remoteLogRow) while the query is still the current one.
func (fe *frontendPretty) startLogSearch(query string) {
	fe.searchGen++
	gen := fe.searchGen
	fe.searchRemote = nil
	for _, search := range fe.logSearches {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), logSearchTimeout)
			defer cancel()
			targets, err := search(ctx, query)
			if err != nil {
				slog.Warn("error searching logs", "err", err)
				return
			}
			fe.dispatch(func() {
				if fe.searchGen != gen || fe.searchQuery != query {
					return
				}
				for _, t := range targets {
					if id := fe.db.LogTarget(t.Trace, t.Span, t.Digest); id.IsValid() {
						if fe.searchRemote == nil {
							fe.searchRemote = make(map[dagui.SpanID]bool)
						}
						fe.searchRemote[id] = true
					}
				}
				fe.refreshSearchMatches()
				if fe.searchIdx < 0 && len(fe.searchMatches) > 0 {
					fe.searchFirstForward()
					fe.syncSearchState()
				}
				fe.Update()
			})
		}()
	}
}

// searchMatch represents a single search hit: either a span name match or a
// specific log row inside a span's Vterm output.
type searchMatch struct {
	spanID dagui.SpanID
	// logRow is the row index inside the Vterm, or -1 when the span name
	// itself matched.
	logRow   int
	toolArgs bool
}

// buildSearchMatches finds every span in the view whose name or logs match
// the current searchQuery, not just those on visible rows, and populates
// fe.searchMatches with every hit. Reads midterm's cached search results
// (populated by syncVtermSearchHighlights). Results are ordered by tree
// position so n/N navigation follows a logical sequence.
//
// The view only builds the trees its rows show, so rather than walk a tree of
// every span, it looks for matching spans in the DB and builds just the trees
// on the way down to each (RowsView.HomeTree).
func (fe *frontendPretty) buildSearchMatches() {
	fe.searchMatches = fe.searchMatches[:0]
	fe.searchMatchSpans = make(map[dagui.SpanID]bool)

	if fe.searchQuery == "" || fe.rowsView == nil {
		return
	}

	query := strings.ToLower(fe.searchQuery)

	// Find the home tree of every span with a match.
	matched := map[*dagui.TraceTree]bool{}
	add := func(span *dagui.Span) {
		if tree := fe.rowsView.HomeTree(span); tree != nil {
			matched[tree] = true
		}
	}
	for _, span := range fe.searchNameMatches(query) {
		add(span)
	}
	for id := range fe.searchRemote {
		if span := fe.db.Spans.Map[id]; span != nil {
			add(span)
		}
	}
	for toolArgs, logs := range map[bool]map[dagui.SpanID]*Vterm{false: fe.logs.Logs, true: fe.logs.ToolArgs} {
		for spanID, vt := range logs {
			if len(vt.Term().SearchMatchRows()) == 0 {
				continue
			}
			span := fe.db.Spans.Map[spanID]
			if span == nil || (toolArgs && fe.spanVerbosity(span) < toolArgsVerbosity) {
				continue
			}
			add(span)
		}
	}
	if len(matched) == 0 {
		return
	}

	// Visit them in depth-first order. Every match's tree is built, and so
	// is the path down to it.
	var walkTree func(trees []*dagui.TraceTree)
	walkTree = func(trees []*dagui.TraceTree) {
		for _, tree := range trees {
			spanID := tree.Span.ID

			if matched[tree] {
				// 1. Span name match.
				if strings.Contains(strings.ToLower(tree.Span.Name), query) {
					fe.searchMatches = append(fe.searchMatches, searchMatch{
						spanID: spanID,
						logRow: -1,
					})
					fe.searchMatchSpans[spanID] = true
				}

				// 2. Vterm log content matches — reads midterm's cached results.
				appendLogMatches := func(logs *Vterm, toolArgs bool) {
					if logs == nil {
						return
					}
					for _, r := range logs.Term().SearchMatchRows() {
						fe.searchMatches = append(fe.searchMatches, searchMatch{
							spanID:   spanID,
							logRow:   r,
							toolArgs: toolArgs,
						})
						fe.searchMatchSpans[spanID] = true
					}
				}
				before := len(fe.searchMatches)
				appendLogMatches(fe.logs.Logs[spanID], false)
				if fe.spanVerbosity(tree.Span) >= toolArgsVerbosity {
					appendLogMatches(fe.logs.ToolArgs[spanID], true)
				}
				// 3. A source's search found it in logs not loaded.
				if fe.searchRemote[spanID] && len(fe.searchMatches) == before {
					fe.searchMatches = append(fe.searchMatches, searchMatch{
						spanID: spanID,
						logRow: remoteLogRow,
					})
					fe.searchMatchSpans[spanID] = true
				}
			}

			// Only what's built: the rest holds no match.
			walkTree(tree.Children)
		}
	}
	walkTree(fe.rowsView.Body)
}

// searchNameMatches returns the spans whose name contains query (already
// lowercased). It only scans the DB again once its spans have changed.
func (fe *frontendPretty) searchNameMatches(query string) []*dagui.Span {
	at := fe.db.MutationCount()
	if fe.searchNames.query == query && fe.searchNames.at == at && fe.searchNames.db == fe.db {
		return fe.searchNames.spans
	}
	// Span names repeat heavily (a call span is named for its Type.field),
	// so match each distinct name once.
	byName := map[string]bool{}
	var spans []*dagui.Span
	for _, span := range fe.db.Spans.Order {
		match, seen := byName[span.Name]
		if !seen {
			match = strings.Contains(strings.ToLower(span.Name), query)
			byName[span.Name] = match
		}
		if match {
			spans = append(spans, span)
		}
	}
	fe.searchNames.query = query
	fe.searchNames.at = at
	fe.searchNames.db = fe.db
	fe.searchNames.spans = spans
	return spans
}

// searchNext moves to the next match after the current one (wrapping).
// If no match is selected (searchIdx == -1), seeks forward from focus.
// Returns true if a match was navigated to.
func (fe *frontendPretty) searchNext() bool {
	if len(fe.searchMatches) == 0 {
		return false
	}
	if fe.searchIdx < 0 {
		fe.searchFirstForward()
		return fe.searchIdx >= 0
	}
	fe.searchIdx++
	if fe.searchIdx >= len(fe.searchMatches) {
		fe.searchIdx = 0 // wrap
	}
	fe.goToSearchMatch(fe.searchIdx)
	fe.syncSearchState()
	return true
}

// searchPrev moves to the previous match before the current one (wrapping).
// If no match is selected (searchIdx == -1), seeks backward from focus.
// Returns true if a match was navigated to.
func (fe *frontendPretty) searchPrev() bool {
	if len(fe.searchMatches) == 0 {
		return false
	}
	if fe.searchIdx < 0 {
		fe.searchFirstBackward()
		return fe.searchIdx >= 0
	}
	fe.searchIdx--
	if fe.searchIdx < 0 {
		fe.searchIdx = len(fe.searchMatches) - 1 // wrap
	}
	fe.goToSearchMatch(fe.searchIdx)
	fe.syncSearchState()
	return true
}

// syncSearchState pushes the search query to all vterms (incrementally
// updating midterm's search results), then marks affected SpanTreeViews
// dirty so they repaint with the new highlight state.
func (fe *frontendPretty) syncSearchState() {
	fe.syncVtermSearchHighlights()
	fe.dirtySearchTrees()
}

// refreshSearchMatches is called every frame when a search is active.
// It incrementally updates midterm's search results (only re-scanning
// rows that changed), rebuilds the match list, and preserves the
// current match selection.
func (fe *frontendPretty) refreshSearchMatches() {
	oldMatch := searchMatch{}
	if fe.searchIdx >= 0 && fe.searchIdx < len(fe.searchMatches) {
		oldMatch = fe.searchMatches[fe.searchIdx]
	}

	fe.syncVtermSearchHighlights()
	fe.buildSearchMatches()

	// Try to preserve the current match position. A remote match whose logs
	// arrived gives way to the first of the span's lines that match.
	if oldMatch != (searchMatch{}) {
		fe.searchIdx = 0
		for i, m := range fe.searchMatches {
			if m == oldMatch {
				fe.searchIdx = i
				break
			}
		}
		if oldMatch.logRow == remoteLogRow && len(fe.searchMatches) > 0 && fe.searchMatches[fe.searchIdx] != oldMatch {
			for i, m := range fe.searchMatches {
				if m.spanID == oldMatch.spanID && m.logRow >= 0 {
					fe.searchIdx = i
					fe.scrollToSearchMatch(m)
					break
				}
			}
		}
	}
	if len(fe.searchMatches) == 0 {
		fe.searchIdx = -1
	}
	fe.dirtySearchTrees()
	fe.refreshKeymap()
}

// matchRowIndex returns the row index for a search match. If the match's
// span is visible, its row index is returned directly. If it's hidden
// (inside a collapsed subtree), the nearest visible ancestor's index is
// returned. Returns -1 if no ancestor is visible.
func (fe *frontendPretty) matchRowIndex(m searchMatch) int {
	if row := fe.rows.BySpan[m.spanID]; row != nil {
		return row.Index
	}
	// Walk up the span tree to find the nearest visible ancestor.
	for id := m.spanID; id.IsValid(); {
		span := fe.db.Spans.Map[id]
		if span == nil {
			break
		}
		if !span.ParentID.IsValid() {
			break
		}
		if row := fe.rows.BySpan[span.ParentID]; row != nil {
			return row.Index
		}
		id = span.ParentID
	}
	return -1
}

// searchFirstForward finds the first match at or after the currently focused
// span and navigates to it. Matches in collapsed spans are included — they
// will be revealed when navigated to.
func (fe *frontendPretty) searchFirstForward() {
	if len(fe.searchMatches) == 0 {
		return
	}
	curIdx := fe.focusedIndex()
	for i, m := range fe.searchMatches {
		if fe.matchRowIndex(m) >= curIdx {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
	}
	fe.searchIdx = 0
	fe.goToSearchMatch(0)
}

// searchFirstBackward finds the last match at or before the currently
// focused span and navigates to it.
func (fe *frontendPretty) searchFirstBackward() {
	if len(fe.searchMatches) == 0 {
		return
	}
	curIdx := fe.focusedIndex()
	for i := len(fe.searchMatches) - 1; i >= 0; i-- {
		m := fe.searchMatches[i]
		if fe.matchRowIndex(m) <= curIdx {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
	}
	fe.searchIdx = len(fe.searchMatches) - 1
	fe.goToSearchMatch(fe.searchIdx)
}

// goToSearchMatch navigates to the match at the given index in searchMatches.
func (fe *frontendPretty) goToSearchMatch(idx int) {
	if idx < 0 || idx >= len(fe.searchMatches) {
		return
	}
	m := fe.searchMatches[idx]
	fe.autoFocus = false

	// Ensure the span is visible: expand all ancestors.
	fe.expandToSpan(m.spanID)

	row := fe.rows.BySpan[m.spanID]
	if row == nil {
		fe.recalculateViewLocked()
		row = fe.rows.BySpan[m.spanID]
		if row == nil {
			return
		}
	}
	fe.focus(row)

	// For log matches, expand the span and scroll the vterm. A remote
	// match's logs are read as the span expands.
	if m.logRow >= 0 || m.logRow == remoteLogRow {
		fe.setExpanded(m.spanID, true)
		fe.syncAfterExpandToggle(m.spanID)
		fe.scrollToSearchMatch(m)
	}
}

// scrollToSearchMatch scrolls a log match's vterm to its row.
func (fe *frontendPretty) scrollToSearchMatch(m searchMatch) {
	if m.logRow < 0 {
		return
	}
	logs := fe.logs.Logs[m.spanID]
	if m.toolArgs {
		logs = fe.logs.ToolArgs[m.spanID]
	}
	if logs != nil {
		logs.ScrollToRow(m.logRow)
	}
}

// expandToSpan expands every tree above spanID's in the view, so that it
// becomes visible in the flat row list.
func (fe *frontendPretty) expandToSpan(spanID dagui.SpanID) {
	if span := fe.db.Spans.Map[spanID]; span != nil {
		if tree := fe.rowsView.HomeTree(span); tree != nil {
			for parent := tree.Parent; parent != nil; parent = parent.Parent {
				fe.setExpanded(parent.Span.ID, true)
			}
		}
	}
	fe.recalculateViewLocked()
}

// clearSearch removes the active search query and all match state.
func (fe *frontendPretty) clearSearch() {
	fe.searchQuery = ""
	fe.searchMatches = nil
	fe.searchIdx = -1
	fe.searchGen++
	fe.searchRemote = nil
	for _, logs := range []map[dagui.SpanID]*Vterm{fe.logs.Logs, fe.logs.ToolArgs} {
		for _, vt := range logs {
			vt.SetSearchHighlight("", -1)
		}
	}
	fe.dirtySearchTrees()
	fe.searchMatchSpans = nil
}

// dirtySearchTrees calls Update() on every SpanTreeView that has (or had)
// a search match so tuist will repaint them with the new highlight state.
func (fe *frontendPretty) dirtySearchTrees() {
	for spanID := range fe.searchMatchSpans {
		if st, ok := fe.spanTrees[spanID]; ok {
			st.Update()
		}
		for key, lv := range fe.logsViews {
			if key.spanID == spanID {
				lv.Update()
			}
		}
	}
	for spanID := range fe.prevSearchMatchSpans {
		if !fe.searchMatchSpans[spanID] {
			if st, ok := fe.spanTrees[spanID]; ok {
				st.Update()
			}
			for key, lv := range fe.logsViews {
				if key.spanID == spanID {
					lv.Update()
				}
			}
		}
	}
	fe.prevSearchMatchSpans = make(map[dagui.SpanID]bool, len(fe.searchMatchSpans))
	for id := range fe.searchMatchSpans {
		fe.prevSearchMatchSpans[id] = true
	}
}

// syncVtermSearchHighlights propagates the current search state to all
// vterms. Each vterm's SetSearchHighlight calls midterm's Search() which
// is incremental — only re-scanning rows that changed since last call.
func (fe *frontendPretty) syncVtermSearchHighlights() {
	var currentSpan dagui.SpanID
	currentRow := -1
	currentToolArgs := false
	if fe.searchIdx >= 0 && fe.searchIdx < len(fe.searchMatches) {
		m := fe.searchMatches[fe.searchIdx]
		if m.logRow >= 0 {
			currentSpan = m.spanID
			currentRow = m.logRow
			currentToolArgs = m.toolArgs
		}
	}

	for toolArgs, logs := range map[bool]map[dagui.SpanID]*Vterm{false: fe.logs.Logs, true: fe.logs.ToolArgs} {
		for spanID, vt := range logs {
			if fe.searchQuery == "" {
				vt.SetSearchHighlight("", -1)
			} else {
				cr := -1
				if spanID == currentSpan && toolArgs == currentToolArgs {
					cr = currentRow
				}
				vt.SetSearchHighlight(fe.searchQuery, cr)
			}
		}
	}
}
