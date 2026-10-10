package dagui

import "iter"

// isIndexedKind reports whether a span is one the DB's whole-trace scans
// look for: surfaced kinds (LLM messages, agents, checks, generators, tests,
// services and service displays; see Span.IsSurfacedKind), rewind markers,
// and generate's skipped and regenerated module spans.
//
// It's a superset of what each scan keeps: it leaves Internal out, which
// some of them filter on, so a span changing only that stays indexed.
func isIndexedKind(span *Span) bool {
	return span.LLMRole != "" ||
		span.Agent ||
		span.CheckName != "" ||
		span.GeneratorName != "" ||
		span.TestCaseName != "" ||
		span.TestSuiteName != "" ||
		span.Service ||
		span.ServiceName != "" ||
		span.AgentRewindMarker() ||
		span.GenerateSkipped ||
		span.GenerateRegenerated
}

// indexKindSpan brings the kind index up to date with a span whose fields
// were just (re)set.
func (db *DB) indexKindSpan(span *Span) {
	if db.Spans.Map[span.ID] != span {
		// Not the span the DB holds for this ID.
		return
	}
	if isIndexedKind(span) {
		addToSpanSet(&db.kindSpans, span)
	} else if db.kindSpans.Has(span.ID) {
		removeFromSpanSet(&db.kindSpans, span)
	}
}

// kindSpanIter iterates, in start order, the spans isIndexedKind holds for:
// callers still filter for the kind they want.
//
// Spans that start at the same time keep the order they joined the index in,
// which only differs from DB.Spans' order for a span that got its kind from
// a later snapshot.
func (db *DB) kindSpanIter() iter.Seq[*Span] {
	return db.kindSpans.Iter()
}
