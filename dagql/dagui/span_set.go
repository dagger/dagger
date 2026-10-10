package dagui

import (
	"iter"
	"slices"
)

// SpanSet is a set of spans ordered by start time, spans that start
// together staying in the order they were added. It holds a span's
// relations (Span.ChildSpans, RunningSpans, ErrorOrigins, ...) and other
// small groups of spans the DB keeps (DB.CreatorSpans).
//
// A nil *SpanSet is an empty set, and every method accepts one: most of a
// span's relations stay empty forever, so they stay nil, costing a pointer
// rather than a set apiece. Its storage is unexported, and changing a span's
// relations goes through Span methods (AddRevealedSpan, AddErrorOrigin,
// AddProgressSpan), which allocate the set on the first add.
type SpanSet struct {
	order []*Span

	// index maps IDs to spans once the set outgrows smallSpanSet; smaller
	// sets search order, which costs about as much as hashing one key.
	index map[SpanID]*Span
}

// smallSpanSet is the most spans a SpanSet holds before indexing them.
const smallSpanSet = 8

// NewSpanSet returns a set of the given spans.
func NewSpanSet(spans ...*Span) *SpanSet {
	set := &SpanSet{}
	for _, span := range spans {
		set.add(span)
	}
	return set
}

// Len returns the number of spans in the set.
func (set *SpanSet) Len() int {
	if set == nil {
		return 0
	}
	return len(set.order)
}

// Spans returns the spans in order. The slice belongs to the set: don't
// modify it.
func (set *SpanSet) Spans() []*Span {
	if set == nil {
		return nil
	}
	return set.order
}

// Iter iterates over the spans in order.
func (set *SpanSet) Iter() iter.Seq[*Span] {
	return func(yield func(*Span) bool) {
		for _, span := range set.Spans() {
			if !yield(span) {
				return
			}
		}
	}
}

// Get returns the span with the given ID, if the set holds it.
func (set *SpanSet) Get(id SpanID) (*Span, bool) {
	if set == nil {
		return nil, false
	}
	if set.index != nil {
		span, ok := set.index[id]
		return span, ok
	}
	for _, span := range set.order {
		if span.ID == id {
			return span, true
		}
	}
	return nil, false
}

// Has reports whether the set holds the span with the given ID.
func (set *SpanSet) Has(id SpanID) bool {
	_, ok := set.Get(id)
	return ok
}

// add adds span to a non-nil set, reporting whether it was added.
func (set *SpanSet) add(span *Span) bool {
	if set.Has(span.ID) {
		return false
	}
	if set.index != nil || len(set.order) >= smallSpanSet {
		if set.index == nil {
			set.index = make(map[SpanID]*Span, len(set.order)+1)
			for _, existing := range set.order {
				set.index[existing.ID] = existing
			}
		}
		set.index[span.ID] = span
	}
	set.order = insert(set.order, span, byStartTime)
	return true
}

// remove removes span from the set, reporting whether it was there.
func (set *SpanSet) remove(span *Span) bool {
	if set == nil {
		return false
	}
	i := slices.IndexFunc(set.order, func(s *Span) bool { return s.ID == span.ID })
	if i < 0 {
		return false
	}
	set.order = slices.Delete(set.order, i, i+1)
	delete(set.index, span.ID)
	return true
}

// addToSpanSet adds span to *set, allocating the set if it's nil. It reports
// whether span was added.
func addToSpanSet(set **SpanSet, span *Span) bool {
	if *set == nil {
		*set = &SpanSet{}
	}
	return (*set).add(span)
}

// removeFromSpanSet removes span from *set, dropping the set once it's empty
// so that its storage, sized for everything it ever held, can be collected
// (an ancestor's RunningSpans would otherwise pin room for every span that
// ever ran beneath it). It reports whether span was removed.
func removeFromSpanSet(set **SpanSet, span *Span) bool {
	if !(*set).remove(span) {
		return false
	}
	if (*set).Len() == 0 {
		*set = nil
	}
	return true
}

// AddRevealedSpan adds a span to RevealedSpans, reporting whether it was
// added.
func (span *Span) AddRevealedSpan(revealed *Span) bool {
	if !addToSpanSet(&span.RevealedSpans, revealed) {
		return false
	}
	if span.db != nil {
		span.db.noteRevealed(span, revealed, true)
	}
	return true
}

// RemoveRevealedSpan removes a span from RevealedSpans, reporting whether it
// was there.
func (span *Span) RemoveRevealedSpan(revealed *Span) bool {
	if !removeFromSpanSet(&span.RevealedSpans, revealed) {
		return false
	}
	if span.db != nil {
		span.db.noteRevealed(span, revealed, false)
	}
	return true
}

// AddErrorOrigin adds a span to ErrorOrigins, reporting whether it was
// added.
func (span *Span) AddErrorOrigin(origin *Span) bool {
	return addToSpanSet(&span.ErrorOrigins, origin)
}

// AddProgressSpan adds a span to ProgressSpans, reporting whether it was
// added.
func (span *Span) AddProgressSpan(src *Span) bool {
	return addToSpanSet(&span.ProgressSpans, src)
}
