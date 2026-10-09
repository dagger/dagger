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

// A fresh span's relations all share emptySpanSet: reads see an empty set,
// the first SpanSetAdd gives the relation a set of its own, and emptying it
// again hands it back.
func TestSpanSetSharedEmpty(t *testing.T) {
	db := NewDB()
	a := db.newSpan(testSetSpan(1).ID)
	b := db.newSpan(testSetSpan(2).ID)
	child := testSetSpan(3)

	if a.ChildSpans != emptySpanSet || len(a.ChildSpans.Order) != 0 || a.ChildSpans.Has(child.ID) {
		t.Fatal("fresh span should share the empty set")
	}
	if !SpanSetAdd(&a.ChildSpans, child) {
		t.Fatal("first add should add")
	}
	if SpanSetAdd(&a.ChildSpans, child) {
		t.Fatal("second add of the same span should not add")
	}
	if a.ChildSpans == emptySpanSet || !a.ChildSpans.Has(child.ID) {
		t.Fatal("add should give the relation its own set")
	}
	if len(emptySpanSet.Order) != 0 || emptySpanSet.Map != nil {
		t.Fatal("shared empty set was mutated")
	}
	if b.ChildSpans != emptySpanSet || len(b.ChildSpans.Order) != 0 {
		t.Fatal("other spans' relations should be unaffected")
	}

	if SpanSetRemove(&b.ChildSpans, child) {
		t.Fatal("removing from the empty set should report false")
	}
	if !SpanSetRemove(&a.ChildSpans, child) {
		t.Fatal("remove should remove")
	}
	if a.ChildSpans != emptySpanSet {
		t.Fatal("an emptied relation should go back to the shared empty set")
	}

	defer func() {
		if recover() == nil {
			t.Fatal("adding to the shared empty set directly should panic")
		}
		if len(emptySpanSet.Order) != 0 {
			t.Fatal("shared empty set was mutated")
		}
	}()
	b.ChildSpans.Add(child)
}

// A small set searches linearly until it outgrows smallSetMax, then indexes
// itself; membership and start-time order are the same either way.
func TestSmallSpanSet(t *testing.T) {
	var set SpanSet = emptySpanSet
	const n = 3 * smallSetMax
	spans := make([]*Span, n)
	for i := range spans {
		// added in reverse start order, so each insert lands at the front
		spans[i] = testSetSpan(n - i)
	}
	for i, span := range spans {
		if !SpanSetAdd(&set, span) {
			t.Fatalf("add %d: not added", i)
		}
		if SpanSetAdd(&set, span) {
			t.Fatalf("add %d: duplicate added", i)
		}
		if got := set.Len(); got != i+1 {
			t.Fatalf("add %d: len = %d", i, got)
		}
		if (set.Map != nil) != (i+1 > smallSetMax) {
			t.Fatalf("add %d: map allocated = %v", i, set.Map != nil)
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
	for i := 1; i < n; i++ {
		if !set.Order[i-1].StartTime.Before(set.Order[i].StartTime) {
			t.Fatalf("order not sorted by start time at %d", i)
		}
	}
	for _, span := range spans {
		if !SpanSetRemove(&set, span) {
			t.Fatalf("remove %v: not removed", span.ID)
		}
		if set.Has(span.ID) {
			t.Fatalf("remove %v: still present", span.ID)
		}
	}
	if set != emptySpanSet {
		t.Fatal("emptied set should be the shared empty set")
	}

	var nilSet SpanSet
	if nilSet.Has(spans[0].ID) || nilSet.Len() != 0 {
		t.Fatal("a nil set is empty")
	}
}
