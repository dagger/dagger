package dagui

import (
	"slices"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/internal/spanfootprint"
)

// withoutLinks strips a span's links.
func withoutLinks(s sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	stub := tracetest.SpanStubFromReadOnlySpan(s)
	stub.Links = nil
	return stub.Snapshot()
}

// treeRollUp counts the categories of span's descendants in the span tree,
// from scratch.
func treeRollUp(span *Span) RollUpState {
	var st RollUpState
	var walk func(*Span)
	walk = func(s *Span) {
		for _, c := range s.ChildSpans.Spans() {
			if c.ParentSpan != s {
				continue
			}
			st.incrementCategory(c.currentStateCategory())
			walk(c)
		}
	}
	walk(span)
	return st
}

func rollUpCounts(span *Span) RollUpState {
	if span.rollUpState == nil {
		return RollUpState{}
	}
	return *span.rollUpState
}

// A subtree that arrives in an earlier batch than its root's parent link
// (an import read children first) must be counted on the ancestors the
// late link makes reachable, as if it had arrived in order.
func TestLateParentRollUpCounts(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	for _, links := range []bool{false, true} {
		name := "tree"
		if links {
			name = "with links"
		}
		t.Run(name, func(t *testing.T) {
			spans := make([]sdktrace.ReadOnlySpan, n)
			for i := range n {
				spans[i] = tr.Stub(i, true)
				if !links {
					spans[i] = withoutLinks(spans[i])
				}
			}
			slices.Reverse(spans)

			db := NewDB()
			for chunk := range slices.Chunk(spans, 512) {
				if err := db.ExportSpans(t.Context(), chunk); err != nil {
					t.Fatal(err)
				}
			}

			// Whatever order spans arrive in, a span's counts are the
			// categories last propagated by every span that reaches it.
			// (With causal links, a cause's category can go stale when an
			// effect fails after it propagated, so the closure is the
			// invariant, not a from-scratch count.)
			want := map[*Span]*RollUpState{}
			for _, span := range db.Spans.Order {
				if span.lastRollUpCategory == stateUnknown {
					continue
				}
				span.rollUpAncestors(nil, func(a *Span) {
					if want[a] == nil {
						want[a] = &RollUpState{}
					}
					want[a].incrementCategory(span.lastRollUpCategory)
				})
			}
			var wrong int
			for _, span := range db.Spans.Order {
				var expected RollUpState
				if w := want[span]; w != nil {
					expected = *w
				}
				if !links {
					if truth := treeRollUp(span); expected != truth {
						t.Fatalf("span %s: closure counts %+v, tree counts %+v", span.ID, expected, truth)
					}
				}
				if got := rollUpCounts(span); got != expected {
					if wrong < 5 {
						t.Errorf("span %s (%s): counts %+v, want %+v", span.ID, span.Name, got, expected)
					}
					wrong++
				}
			}
			if wrong > 0 {
				t.Errorf("%d of %d spans have wrong roll-up counts", wrong, n)
			}
		})
	}
}

// lateTestSpan is one span of TestLateParentRunningAndRevealed's trace.
func lateTestSpan(id, parent byte, running bool, attrs ...attribute.KeyValue) sdktrace.ReadOnlySpan {
	ctx := func(id byte) trace.SpanContext {
		return trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: testID(id).SpanID})
	}
	stub := tracetest.SpanStub{
		Name:        string(rune('a' + id)),
		SpanContext: ctx(id),
		StartTime:   time.Unix(0, int64(id)),
		EndTime:     time.Unix(100, 0),
		Attributes:  attrs,
	}
	if running {
		stub.EndTime = time.Time{}
	}
	if parent != 0 {
		stub.Parent = ctx(parent)
	}
	return stub.Snapshot()
}

// The running and revealed spans beneath a span that joins its parent late
// reach the ancestors the link makes reachable too, and remote frontends
// hear about them.
func TestLateParentRunningAndRevealed(t *testing.T) {
	// 1 root
	// └── 2 group
	//     └── 3 work
	//         ├── 4 running
	//         └── 5 revealed
	root := lateTestSpan(1, 0, true)
	group := lateTestSpan(2, 1, true)
	work := lateTestSpan(3, 2, true)
	running := lateTestSpan(4, 3, true)
	revealed := lateTestSpan(5, 3, false, attribute.Bool(telemetry.UIRevealAttr, true))

	db := NewDB()
	for _, batch := range [][]sdktrace.ReadOnlySpan{
		{running, revealed},
		{work},
		{root},
		{group},
	} {
		db.UpdatedSnapshots(nil) // drain, so the last batch's updates stand alone
		if err := db.ExportSpans(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	updated := map[SpanID]bool{}
	for _, snap := range db.UpdatedSnapshots(nil) {
		updated[snap.ID] = true
	}
	for _, id := range []byte{1, 2} {
		span := db.Spans.Map[testID(id)]
		if _, ok := span.RunningSpans.Get(testID(4)); !ok {
			t.Errorf("span %d should hold the running span beneath it", id)
		}
		if _, ok := span.RevealedSpans.Get(testID(5)); !ok {
			t.Errorf("span %d should reveal the revealed span beneath it", id)
		}
		if got := rollUpCounts(span); got.RunningCount < 2 || got.SuccessCount != 1 {
			t.Errorf("span %d roll-up counts %+v", id, got)
		}
	}
	if !updated[testID(1)] {
		t.Error("the root's new aggregates should be sent to remote frontends")
	}
}
