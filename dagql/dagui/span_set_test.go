package dagui

import (
	"encoding/binary"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func testSetSpan(i int) *Span {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(i)+1)
	return &Span{SpanSnapshot: SpanSnapshot{
		ID:        SpanID{SpanID: id},
		StartTime: time.Unix(int64(i), 0),
	}}
}

// A fresh span's relations are nil, and every read treats a nil set as
// empty. The first add gives the relation a set of the span's own; emptying
// it drops the set again.
func TestSpanSetNilRelations(t *testing.T) {
	db := NewDB()
	a := db.newSpan(testSetSpan(1).ID)
	b := db.newSpan(testSetSpan(2).ID)
	child := testSetSpan(3)

	for _, set := range []*SpanSet{
		a.ChildSpans, a.RunningSpans, a.FailedLinks, a.CanceledLinks,
		a.RevealedSpans, a.ErrorOrigins, a.ProgressSpans,
		a.causesViaLinks, a.effectsViaLinks,
	} {
		if set != nil {
			t.Fatal("a fresh span's relations should be nil")
		}
	}
	if a.ChildSpans.Len() != 0 || a.ChildSpans.Spans() != nil || a.ChildSpans.Has(child.ID) {
		t.Fatal("a nil set reads as empty")
	}
	if _, ok := a.ChildSpans.Get(child.ID); ok {
		t.Fatal("a nil set has nothing to get")
	}
	for range a.ChildSpans.Iter() {
		t.Fatal("a nil set iterates over nothing")
	}
	if a.IsFailedOrCausedFailure() || a.IsCanceled() || a.Errors().Len() != 0 {
		t.Fatal("a span without relations has no derived status")
	}

	if !a.AddRevealedSpan(child) {
		t.Fatal("first add should add")
	}
	if a.AddRevealedSpan(child) {
		t.Fatal("second add of the same span should not add")
	}
	if !a.RevealedSpans.Has(child.ID) || a.RevealedSpans.Len() != 1 {
		t.Fatal("add should give the relation its own set")
	}
	if b.RevealedSpans != nil {
		t.Fatal("other spans' relations should be unaffected")
	}

	if b.RemoveRevealedSpan(child) {
		t.Fatal("removing from a nil set should report false")
	}
	if !a.RemoveRevealedSpan(child) {
		t.Fatal("remove should remove")
	}
	if a.RevealedSpans != nil {
		t.Fatal("an emptied relation should go back to nil")
	}
}

// A set searches linearly until it outgrows smallSpanSet, then indexes
// itself; membership and start-time order are the same either way.
func TestSpanSetGrowsIndex(t *testing.T) {
	var set *SpanSet
	const n = 3 * smallSpanSet
	spans := make([]*Span, n)
	for i := range spans {
		// added in reverse start order, so each insert lands at the front
		spans[i] = testSetSpan(n - i)
	}
	for i, span := range spans {
		if !addToSpanSet(&set, span) {
			t.Fatalf("add %d: not added", i)
		}
		if addToSpanSet(&set, span) {
			t.Fatalf("add %d: duplicate added", i)
		}
		if got := set.Len(); got != i+1 {
			t.Fatalf("add %d: len = %d", i, got)
		}
		if (set.index != nil) != (i+1 > smallSpanSet) {
			t.Fatalf("add %d: index allocated = %v", i, set.index != nil)
		}
		for _, added := range spans[:i+1] {
			if got, ok := set.Get(added.ID); !ok || got != added {
				t.Fatalf("add %d: lost %v", i, added.ID)
			}
		}
		if set.Has(testSetSpan(0).ID) {
			t.Fatalf("add %d: has a span never added", i)
		}
	}
	ordered := set.Spans()
	for i := 1; i < n; i++ {
		if !ordered[i-1].StartTime.Before(ordered[i].StartTime) {
			t.Fatalf("order not sorted by start time at %d", i)
		}
	}
	for _, span := range spans {
		if !removeFromSpanSet(&set, span) {
			t.Fatalf("remove %v: not removed", span.ID)
		}
		if set.Has(span.ID) {
			t.Fatalf("remove %v: still present", span.ID)
		}
	}
	if set != nil {
		t.Fatal("an emptied set should be dropped")
	}
}

// Spans that start together keep the order they were added in.
func TestSpanSetStableForEqualStarts(t *testing.T) {
	first, second, third := testSetSpan(1), testSetSpan(2), testSetSpan(3)
	second.StartTime = first.StartTime
	set := NewSpanSet(first, third, second)
	got := set.Spans()
	if len(got) != 3 || got[0] != first || got[1] != second || got[2] != third {
		t.Fatalf("order = %v", got)
	}
}
