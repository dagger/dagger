package idtui

import (
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
)

// searchMatch represents a single search hit: either a span name match or a
// specific log row inside a span's Vterm output.
type searchMatch struct {
	spanID dagui.SpanID
	// logRow is the row index inside the Vterm, or -1 when the span name
	// itself matched.
	logRow int
}

// buildSearchMatches walks ALL spans in the trace tree (not just visible
// rows) and populates fe.searchMatches with every hit for the current
// searchQuery. Reads midterm's cached search results (populated by
// syncVtermSearchHighlights). Results are ordered by tree position so
// n/N navigation follows a logical sequence.
func (fe *frontendPretty) buildSearchMatches() {
	fe.searchMatches = fe.searchMatches[:0]
	fe.searchMatchSpans = make(map[dagui.SpanID]bool)

	if fe.searchQuery == "" || fe.rowsView == nil {
		return
	}

	query := strings.ToLower(fe.searchQuery)

	// Walk the full tree in depth-first order.
	var walkTree func(trees []*dagui.TraceTree)
	walkTree = func(trees []*dagui.TraceTree) {
		for _, tree := range trees {
			spanID := tree.Span.ID

			// 1. Span name match.
			if strings.Contains(strings.ToLower(tree.Span.Name), query) {
				fe.searchMatches = append(fe.searchMatches, searchMatch{
					spanID: spanID,
					logRow: -1,
				})
				fe.searchMatchSpans[spanID] = true
			}

			// 2. Vterm log content matches — reads midterm's cached results.
			if logs, ok := fe.logs.Logs[spanID]; ok {
				for _, r := range logs.Term().SearchMatchRows() {
					fe.searchMatches = append(fe.searchMatches, searchMatch{
						spanID: spanID,
						logRow: r,
					})
					fe.searchMatchSpans[spanID] = true
				}
			}

			walkTree(tree.Children)
		}
	}
	walkTree(fe.rowsView.Body)
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

	// Try to preserve the current match position.
	if oldMatch != (searchMatch{}) {
		fe.searchIdx = 0
		for i, m := range fe.searchMatches {
			if m == oldMatch {
				fe.searchIdx = i
				break
			}
		}
	}
	if len(fe.searchMatches) == 0 {
		fe.searchIdx = -1
	}
	fe.dirtySearchTrees()
	fe.keymapBar.Update()
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

	// For log matches, expand the span and scroll the vterm.
	if m.logRow >= 0 {
		fe.setExpanded(m.spanID, true)
		fe.syncAfterExpandToggle(m.spanID)
		if logs, ok := fe.logs.Logs[m.spanID]; ok {
			logs.ScrollToRow(m.logRow)
		}
	}
}

// expandToSpan expands all ancestor spans so that spanID becomes visible
// in the flat row list.
func (fe *frontendPretty) expandToSpan(spanID dagui.SpanID) {
	for id := spanID; id.IsValid(); {
		span := fe.db.Spans.Map[id]
		if span == nil {
			break
		}
		if !span.ParentID.IsValid() {
			break
		}
		fe.setExpanded(span.ParentID, true)
		id = span.ParentID
	}
	fe.recalculateViewLocked()
}

// clearSearch removes the active search query and all match state.
func (fe *frontendPretty) clearSearch() {
	fe.searchQuery = ""
	fe.searchMatches = nil
	fe.searchIdx = -1
	for _, vt := range fe.logs.Logs {
		vt.SetSearchHighlight("", -1)
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
	}
	for spanID := range fe.prevSearchMatchSpans {
		if !fe.searchMatchSpans[spanID] {
			if st, ok := fe.spanTrees[spanID]; ok {
				st.Update()
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
	if fe.searchIdx >= 0 && fe.searchIdx < len(fe.searchMatches) {
		m := fe.searchMatches[fe.searchIdx]
		if m.logRow >= 0 {
			currentSpan = m.spanID
			currentRow = m.logRow
		}
	}

	for spanID, vt := range fe.logs.Logs {
		if fe.searchQuery == "" {
			vt.SetSearchHighlight("", -1)
		} else {
			cr := -1
			if spanID == currentSpan {
				cr = currentRow
			}
			vt.SetSearchHighlight(fe.searchQuery, cr)
		}
	}
}
