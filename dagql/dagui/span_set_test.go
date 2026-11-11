package dagui

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

// A span leaves the set even when it shares its start time with others, and
// even when its start time changed after it joined, which puts it out of
// start-time order.
func TestSpanSetRemoveFindsSpan(t *testing.T) {
	const n = 4 * smallSpanSet
	var set *SpanSet
	spans := make([]*Span, n)
	for i := range spans {
		spans[i] = testSetSpan(i / 4) // four spans per start time
		binary.BigEndian.PutUint64(spans[i].ID.SpanID[:], uint64(i)+1)
		addToSpanSet(&set, spans[i])
	}
	moved := spans[5]
	moved.StartTime = time.Unix(1_000, 0)
	for _, i := range []int{5, 0, n - 1, 6, 7, 4} {
		if !removeFromSpanSet(&set, spans[i]) {
			t.Fatalf("remove %d: not removed", i)
		}
		if set.Has(spans[i].ID) || slices.Contains(set.Spans(), spans[i]) {
			t.Fatalf("remove %d: still present", i)
		}
		if removeFromSpanSet(&set, spans[i]) {
			t.Fatalf("remove %d again: removed", i)
		}
	}
	if got := set.Len(); got != n-6 {
		t.Fatalf("len = %d, want %d", got, n-6)
	}
}

// Most spans arrive once, already ended, and each tries to leave the
// RunningSpans of every ancestor it never joined. That must not cost a scan
// of those sets, or a run with many spans running at once ingests each span
// in time proportional to how many are running, and the CLI falls behind the
// engine's telemetry until its shutdown deadline drops the end of the run.
func TestIngestCostDoesNotGrowWithRunningSpans(t *testing.T) {
	const n, depth = 5_000, 20
	type liveRun struct{ running, ended [][]sdktrace.ReadOnlySpan }
	export := func(db *DB, batches [][]sdktrace.ReadOnlySpan) {
		for _, batch := range batches {
			if err := db.ExportSpans(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	// ingest times only the ended spans: starting the running spans costs
	// time in proportion to how many there are, and isn't what's measured.
	ingest := func(run liveRun) time.Duration {
		db := NewDB()
		export(db, run.running)
		start := time.Now()
		export(db, run.ended)
		return time.Since(start)
	}
	var few, many liveRun
	few.running, few.ended = concurrentLiveTrace(n, 100, depth)
	many.running, many.ended = concurrentLiveTrace(n, 5_000, depth)
	// Alternate the two, so that other work on a shared machine slows both
	// alike, and keep the fastest of each.
	fastFew, fastMany := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range 5 {
		fastFew = min(fastFew, ingest(few))
		fastMany = min(fastMany, ingest(many))
	}
	// Scanning made this about 14x, and 47x with the race detector; without
	// the scan it's about 1.1x, and under 2x on a busy machine.
	if ratio := float64(fastMany) / float64(fastFew); ratio > 5 {
		t.Fatalf("ingesting %d ended spans took %s with 100 spans running and %s with 5000 (%.1fx)", n, fastFew, fastMany, ratio)
	}
}

// A live session receives most spans twice: as they start, joining the
// RunningSpans of every ancestor, and as they end, leaving them. Leaving must
// not scan those sets either.
func TestLiveIngestCostDoesNotGrowWithRunningSpans(t *testing.T) {
	const n, depth = 5_000, 20
	type liveRun struct{ running, spans [][]sdktrace.ReadOnlySpan }
	export := func(db *DB, batches [][]sdktrace.ReadOnlySpan) {
		for _, batch := range batches {
			if err := db.ExportSpans(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
		}
	}
	ingest := func(run liveRun) time.Duration {
		db := NewDB()
		export(db, run.running)
		start := time.Now()
		export(db, run.spans)
		return time.Since(start)
	}
	live := func(width int) liveRun {
		running, ended := concurrentLiveTrace(n, width, depth)
		return liveRun{running, startThenEnd(ended)}
	}
	few, many := live(100), live(5_000)
	fastFew, fastMany := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range 5 {
		fastFew = min(fastFew, ingest(few))
		fastMany = min(fastMany, ingest(many))
	}
	if ratio := float64(fastMany) / float64(fastFew); ratio > 5 {
		t.Fatalf("ingesting %d spans as they started and ended took %s with 100 spans running and %s with 5000 (%.1fx)", n, fastFew, fastMany, ratio)
	}
}

// Every span that ends adds its interval to the Activity of each span it
// links as a cause, and of that cause's ancestors. Spans with gaps between
// them leave those with as many intervals as spans, so adding one must not
// scan or re-merge them all, or ingesting a run costs the square of its spans.
func TestIngestCostGrowsLinearlyWithLinkedSpans(t *testing.T) {
	const depth, perCause = 20, 50
	traceID := trace.TraceID{3}
	ctx := func(i int) trace.SpanContext {
		var id trace.SpanID
		binary.BigEndian.PutUint64(id[:], uint64(i)+1)
		return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: id})
	}
	epoch := time.Unix(1_700_000_000, 0)
	// A running chain of depth ancestors; beneath it, a call span for every
	// perCause short spans, which link it as their cause, as a resumed lazy
	// evaluation links the call that returned its result. Each short span
	// lasts a microsecond, with a microsecond's gap before the next.
	build := func(n int) (running, ended [][]sdktrace.ReadOnlySpan) {
		for i := range depth {
			stub := tracetest.SpanStub{Name: "ancestor", SpanContext: ctx(i), StartTime: epoch}
			if i > 0 {
				stub.Parent = ctx(i - 1)
			}
			running = append(running, []sdktrace.ReadOnlySpan{stub.Snapshot()})
		}
		var batch []sdktrace.ReadOnlySpan
		for j := range n {
			start := epoch.Add(time.Duration(2*j+1) * time.Microsecond)
			cause := depth + n + j/perCause
			if j%perCause == 0 {
				batch = append(batch, tracetest.SpanStub{
					Name: "call", SpanContext: ctx(cause), Parent: ctx(depth - 1),
					StartTime: start, EndTime: start,
				}.Snapshot())
			}
			batch = append(batch, tracetest.SpanStub{
				Name: "resume", SpanContext: ctx(depth + j), Parent: ctx(depth - 1),
				Links:     []sdktrace.Link{{SpanContext: ctx(cause)}},
				StartTime: start, EndTime: start.Add(time.Microsecond),
			}.Snapshot())
			if len(batch) >= 64 {
				ended = append(ended, batch)
				batch = nil
			}
		}
		return running, append(ended, batch)
	}
	ingest := func(n int) time.Duration {
		running, ended := build(n)
		db := NewDB()
		for _, batch := range running {
			if err := db.ExportSpans(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		for _, batch := range ended {
			if err := db.ExportSpans(t.Context(), batch); err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(start)
	}
	const small, large = 2_000, 8_000
	fastSmall, fastLarge := time.Duration(math.MaxInt64), time.Duration(math.MaxInt64)
	for range 3 {
		fastSmall = min(fastSmall, ingest(small))
		fastLarge = min(fastLarge, ingest(large))
	}
	// Four times the spans: about 4x when linear, 16x when quadratic.
	if ratio := float64(fastLarge) / float64(fastSmall); ratio > 8 {
		t.Fatalf("ingesting %d linked spans took %s, and %d took %s (%.1fx)", small, fastSmall, large, fastLarge, ratio)
	}
}
