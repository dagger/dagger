package idtui

import (
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/vito/midterm"
)

// searchMatch represents a single search hit: either a span name match or a
// specific log row inside a span's Vterm output.
type searchMatch struct {
	spanID dagui.SpanID
	// logRow is the row index inside the Vterm, or -1 when the span name
	// itself matched.
	logRow int
}

// searchVtermRows returns the row indices within vt's content that contain
// query (case-insensitive). Only rows up to UsedHeight are examined.
func searchVtermRows(vt *midterm.Terminal, query string) []int {
	used := vt.UsedHeight()
	if used == 0 {
		return nil
	}
	var rows []int
	for i, line := range vt.Content {
		if i >= used {
			break
		}
		if strings.Contains(strings.ToLower(strings.TrimRight(string(line), " ")), query) {
			rows = append(rows, i)
		}
	}
	return rows
}

// buildSearchMatches walks the visible rows and populates fe.searchMatches
// with every hit for the current searchQuery. It also rebuilds the fast-lookup
// set searchMatchSpans.
func (fe *frontendPretty) buildSearchMatches() {
	fe.searchMatches = fe.searchMatches[:0]
	fe.searchMatchSpans = make(map[dagui.SpanID]bool)

	if fe.searchQuery == "" || fe.rows == nil {
		return
	}

	query := strings.ToLower(fe.searchQuery)

	for _, row := range fe.rows.Order {
		span := row.Span
		// 1. Span name match
		if strings.Contains(strings.ToLower(span.Name), query) {
			fe.searchMatches = append(fe.searchMatches, searchMatch{
				spanID: span.ID,
				logRow: -1,
			})
			fe.searchMatchSpans[span.ID] = true
		}

		// 2. Vterm log content matches
		if logs, ok := fe.logs.Logs[span.ID]; ok {
			vt := logs.Term()
			matchRows := searchVtermRows(vt, query)
			for _, r := range matchRows {
				fe.searchMatches = append(fe.searchMatches, searchMatch{
					spanID: span.ID,
					logRow: r,
				})
				fe.searchMatchSpans[span.ID] = true
			}
		}
	}
}

// searchNext moves to the next match after the current one (wrapping).
// Returns true if a match was navigated to.
func (fe *frontendPretty) searchNext() bool {
	if len(fe.searchMatches) == 0 {
		return false
	}
	fe.searchIdx++
	if fe.searchIdx >= len(fe.searchMatches) {
		fe.searchIdx = 0 // wrap
	}
	fe.goToSearchMatch(fe.searchIdx)
	return true
}

// searchPrev moves to the previous match before the current one (wrapping).
// Returns true if a match was navigated to.
func (fe *frontendPretty) searchPrev() bool {
	if len(fe.searchMatches) == 0 {
		return false
	}
	fe.searchIdx--
	if fe.searchIdx < 0 {
		fe.searchIdx = len(fe.searchMatches) - 1 // wrap
	}
	fe.goToSearchMatch(fe.searchIdx)
	return true
}

// searchFirstForward finds the first match at or after the currently focused
// span and navigates to it.
func (fe *frontendPretty) searchFirstForward() {
	if len(fe.searchMatches) == 0 {
		return
	}
	// Find the first match whose span is at or after the focused row index.
	for i, m := range fe.searchMatches {
		row := fe.rows.BySpan[m.spanID]
		if row != nil && row.Index >= fe.focusedIdx {
			fe.searchIdx = i
			fe.goToSearchMatch(i)
			return
		}
	}
	// Nothing after focus — wrap to first match.
	fe.searchIdx = 0
	fe.goToSearchMatch(0)
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
	fe.searchMatchSpans = nil
	fe.searchIdx = -1
}
