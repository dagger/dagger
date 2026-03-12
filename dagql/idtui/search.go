package idtui

import (
	"strings"
	"sync"

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

// spanSearchResult collects all matches for a single span, produced in
// parallel and merged afterward.
type spanSearchResult struct {
	spanID  dagui.SpanID
	matches []searchMatch
}

// buildSearchMatches walks ALL spans in the trace tree (not just visible
// rows) and populates fe.searchMatches with every hit for the current
// searchQuery. Vterm searches run in parallel. Results are ordered by
// tree position so n/N navigation follows a logical sequence.
func (fe *frontendPretty) buildSearchMatches() {
	fe.searchMatches = fe.searchMatches[:0]
	fe.searchMatchSpans = make(map[dagui.SpanID]bool)

	if fe.searchQuery == "" || fe.rowsView == nil {
		return
	}

	query := strings.ToLower(fe.searchQuery)

	// Collect all spans from the tree in tree-walk order.
	type spanEntry struct {
		order  int
		spanID dagui.SpanID
		span   *dagui.Span
	}
	var allSpans []spanEntry
	var walkTree func(trees []*dagui.TraceTree)
	walkTree = func(trees []*dagui.TraceTree) {
		for _, tree := range trees {
			allSpans = append(allSpans, spanEntry{
				order:  len(allSpans),
				spanID: tree.Span.ID,
				span:   tree.Span,
			})
			walkTree(tree.Children)
		}
	}
	walkTree(fe.rowsView.Body)

	// Search all spans in parallel.
	results := make([]spanSearchResult, len(allSpans))
	var wg sync.WaitGroup
	for i, entry := range allSpans {
		i, entry := i, entry
		results[i].spanID = entry.spanID

		// Span name match (cheap, no goroutine needed).
		if strings.Contains(strings.ToLower(entry.span.Name), query) {
			results[i].matches = append(results[i].matches, searchMatch{
				spanID: entry.spanID,
				logRow: -1,
			})
		}

		// Vterm log content match (parallel).
		if logs, ok := fe.logs.Logs[entry.spanID]; ok {
			wg.Add(1)
			go func() {
				defer wg.Done()
				vt := logs.Term()
				matchRows := vt.SearchMatchRows()
				for _, r := range matchRows {
					results[i].matches = append(results[i].matches, searchMatch{
						spanID: entry.spanID,
						logRow: r,
					})
				}
			}()
		}
	}
	wg.Wait()

	// Merge results in tree order.
	for _, res := range results {
		if len(res.matches) > 0 {
			fe.searchMatches = append(fe.searchMatches, res.matches...)
			fe.searchMatchSpans[res.spanID] = true
		}
	}
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

// syncSearchState propagates the search query to all vterms (so midterm
// runs/refreshes its search), then rebuilds idtui's match list from
// midterm's results, and marks affected SpanTreeViews dirty.
//
// Call this whenever the search query changes or vterm content may have
// changed. It is safe to call even when no search is active.
func (fe *frontendPretty) syncSearchState() {
	fe.syncVtermSearchHighlights()
	fe.dirtySearchTrees()
}

// searchFirstForward finds the first match at or after the currently focused
// span and navigates to it. Matches in collapsed spans are included — they
// will be revealed when navigated to.
func (fe *frontendPretty) searchFirstForward() {
	if len(fe.searchMatches) == 0 {
		return
	}
	// Find the first match whose tree position is at or after the focused row.
	// For matches in collapsed spans, compare using the tree's order index.
	for i, m := range fe.searchMatches {
		row := fe.rows.BySpan[m.spanID]
		if row != nil && row.Index >= fe.focusedIdx {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
		// If the span isn't visible yet (collapsed), still consider it — the
		// tree walk order is already logical so we just take the first match.
		if row == nil {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
	}
	// Nothing after focus — wrap to first match.
	fe.searchIdx = 0
	fe.goToSearchMatch(0)
}

// searchFirstBackward finds the last match at or before the currently
// focused span and navigates to it.
func (fe *frontendPretty) searchFirstBackward() {
	if len(fe.searchMatches) == 0 {
		return
	}
	// Walk backward to find the last match at or before focus.
	for i := len(fe.searchMatches) - 1; i >= 0; i-- {
		m := fe.searchMatches[i]
		row := fe.rows.BySpan[m.spanID]
		if row != nil && row.Index <= fe.focusedIdx {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
		if row == nil {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
	}
	// Nothing before focus — wrap to last match.
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
		// May need a recalculate after expanding.
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
	// Walk the DB span parents upward, expanding each.
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
	// Clear highlights from all vterms.
	for _, vt := range fe.logs.Logs {
		vt.SetSearchHighlight("", -1)
	}
	// Dirty trees that had matches so they repaint without highlights.
	fe.dirtySearchTrees()
	// Now clear the span sets (after dirtySearchTrees used them for diff).
	fe.searchMatchSpans = nil
}

// dirtySearchTrees calls Update() on every SpanTreeView that has (or had)
// a search match so tuist will repaint them with the new highlight state.
func (fe *frontendPretty) dirtySearchTrees() {
	// Dirty all spans that currently have matches.
	for spanID := range fe.searchMatchSpans {
		if st, ok := fe.spanTrees[spanID]; ok {
			st.Update()
		}
	}
	// Also dirty spans that previously had matches but no longer do
	// (tracked via prevSearchMatchSpans).
	for spanID := range fe.prevSearchMatchSpans {
		if !fe.searchMatchSpans[spanID] {
			if st, ok := fe.spanTrees[spanID]; ok {
				st.Update()
			}
		}
	}
	// Snapshot current set for next diff.
	fe.prevSearchMatchSpans = make(map[dagui.SpanID]bool, len(fe.searchMatchSpans))
	for id := range fe.searchMatchSpans {
		fe.prevSearchMatchSpans[id] = true
	}
}

// syncVtermSearchHighlights propagates the current search state to all
// vterms so they highlight matches during rendering.
func (fe *frontendPretty) syncVtermSearchHighlights() {
	// Determine the current match's vterm row (if any).
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
