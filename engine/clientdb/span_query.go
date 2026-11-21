package clientdb

import (
	"context"
	"fmt"
)

// SpanQuery selects spans of a live store for a frontend that holds only
// part of the trace (see QuerySpans).
type SpanQuery struct {
	// Spans are the span IDs (hex) the query is about.
	Spans []string `json:"spans"`
	// Children adds the spans one edge beneath each of Spans: their
	// children, and the spans cause-linking them (ChildSpanIDs).
	Children bool `json:"children,omitempty"`
	// Ancestors adds the ancestor chain of every span selected
	// (AncestorClosure).
	Ancestors bool `json:"ancestors,omitempty"`
}

// SpanQueryResult is what QuerySpans answers: the newest snapshot of each
// selected span the store has, in append order, with what a frontend
// holding only part of the trace needs to know of the spans beneath each.
type SpanQueryResult struct {
	Spans []Span
	// ChildCounts holds, by span ID, how many spans sit one edge beneath
	// it (ChildSpanIDs), for the spans with any.
	ChildCounts map[string]int
	// HasLogs holds the spans with display text.
	HasLogs map[string]bool
	// Through is the store's span high-water mark (a row ID) when the query
	// ran: every snapshot up to it is reflected.
	Through int64
}

// QuerySpans answers q from the store's in-memory indexes plus one indexed
// read of the rows selected: its cost is proportional to the result, not to
// the trace. It's the live counterpart of a partial archive read, for a
// frontend that folds what it doesn't render and reads it back on demand.
func (s *DB) QuerySpans(ctx context.Context, q SpanQuery) (SpanQueryResult, error) {
	through := s.HighWater().Spans
	ids := make(map[string]struct{}, len(q.Spans))
	for _, id := range q.Spans {
		if id == "" {
			continue
		}
		ids[id] = struct{}{}
		if q.Children {
			for kid := range s.lookup.directChildren(id) {
				ids[kid] = struct{}{}
			}
		}
	}
	if q.Ancestors {
		ids = s.lookup.ancestorClosure(ids)
	}
	rows, err := s.SelectSpansLatest(ctx, ids)
	if err != nil {
		return SpanQueryResult{}, fmt.Errorf("select spans: %w", err)
	}
	result := SpanQueryResult{
		Spans:       rows,
		ChildCounts: make(map[string]int),
		HasLogs:     make(map[string]bool),
		Through:     through,
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.SpanID] {
			continue
		}
		seen[row.SpanID] = true
		if n := s.lookup.directChildCount(row.SpanID); n > 0 {
			result.ChildCounts[row.SpanID] = n
		}
		if s.logIdx.hasDisplay(row.SpanID) {
			result.HasLogs[row.SpanID] = true
		}
	}
	return result, nil
}

// directChildCount is len(directChildren(spanID)), without building it.
func (l *spanLookup) directChildCount(spanID string) int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n, ok := l.lookupLocked(spanID)
	if !ok {
		return 0
	}
	var count int
	var causal map[uint32]struct{}
	l.eachChildLocked(n, func(kid uint32) bool {
		if kid != n {
			count++
		}
		return true
	})
	l.causalChildrenLocked(n, func(kid uint32) bool {
		if kid == n || l.isChildLocked(n, kid) {
			// a span both beneath and cause-linking counts once
			return true
		}
		if causal == nil {
			causal = map[uint32]struct{}{}
		}
		if _, dup := causal[kid]; !dup {
			causal[kid] = struct{}{}
			count++
		}
		return true
	})
	return count
}
