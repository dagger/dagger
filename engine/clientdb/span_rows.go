package clientdb

import "context"

// LatestSpanRowIDs returns the newest snapshot row ID of every span in ids
// the store knows, ascending: SelectSpansLatest's rows, for a caller that
// reads them in batches (SelectSpanRows) rather than all at once.
func (s *DB) LatestSpanRowIDs(ids map[string]struct{}) []int64 {
	return s.lookup.latestRowIDs(ids)
}

// SelectSpanRows reads the given span rows, which must be ascending (as
// LatestSpanRowIDs returns them), in that order.
func (s *DB) SelectSpanRows(ctx context.Context, rowIDs []int64) ([]Span, error) {
	return readIndexedRows(ctx, s.spans, rowIDs, func(row Span) int64 { return row.ID }, "span")
}

// SpansWithDisplayLogs returns the spans of ids with display text (see
// logFlags.display), answered from the log index alone.
func (s *DB) SpansWithDisplayLogs(ids map[string]struct{}) map[string]struct{} {
	with := map[string]struct{}{}
	for id := range ids {
		if s.logIdx.hasDisplay(id) {
			with[id] = struct{}{}
		}
	}
	return with
}

// hasDisplay reports whether any of spanID's log rows is display text.
func (l *logLookup) hasDisplay(spanID string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	span, ok := l.spans.lookup(spanID)
	if !ok {
		return false
	}
	for _, row := range l.rowsBySpan[span] {
		if i := row - 1; i >= 0 && int(i) < len(l.meta) && l.meta[i].flags().display() {
			return true
		}
	}
	return false
}
