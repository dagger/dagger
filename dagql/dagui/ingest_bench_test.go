package dagui

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// benchTrace builds a synthetic trace of n completed spans shaped roughly like
// a large agent session: a root, a handful of wide parents (agent loops with
// many direct children) and otherwise deep, narrow call chains. Span i starts
// at i microseconds, so start order is index order and every parent starts
// before its children.
func benchTrace(n int) []sdktrace.ReadOnlySpan {
	rng := rand.New(rand.NewSource(1))
	traceID := trace.TraceID{1}
	spanID := func(i int) trace.SpanID {
		var id trace.SpanID
		binary.BigEndian.PutUint64(id[:], uint64(i)+1)
		return id
	}
	epoch := time.Unix(1_700_000_000, 0)
	spans := make([]sdktrace.ReadOnlySpan, n)
	for i := range n {
		var parent trace.SpanContext
		if i > 0 {
			var p int
			switch r := rng.Intn(100); {
			case r < 2:
				p = 0 // the root
			case r < 5:
				p = min(i-1, 1+rng.Intn(min(i, 8))) // a few wide "loop" spans
			default:
				p = max(0, i-1-rng.Intn(64)) // a recent span: deep, narrow chains
			}
			parent = trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID(p),
			})
		}
		start := epoch.Add(time.Duration(i) * time.Microsecond)
		spans[i] = tracetest.SpanStub{
			Name: fmt.Sprintf("span %d", i),
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID(i),
			}),
			Parent:    parent,
			StartTime: start,
			EndTime:   start.Add(time.Duration(1+rng.Intn(1000)) * time.Microsecond),
		}.Snapshot()
	}
	return spans
}

// BenchmarkDBIngest feeds a large trace through the real ingestion path in
// export-sized batches, in the delivery orders a trace import sees: start
// order, shuffled (Cloud does not deliver in start order), and children
// before parents (which creates a zero-start-time placeholder per parent).
func BenchmarkDBIngest(b *testing.B) {
	const batch = 512
	for _, n := range []int{50_000, 200_000} {
		spans := benchTrace(n)
		shuffled := slices.Clone(spans)
		rand.New(rand.NewSource(2)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		reversed := slices.Clone(spans)
		slices.Reverse(reversed)
		for _, order := range []struct {
			name  string
			spans []sdktrace.ReadOnlySpan
		}{
			{"inorder", spans},
			{"shuffled", shuffled},
			{"reversed", reversed},
		} {
			b.Run(fmt.Sprintf("%s/%d", order.name, n), func(b *testing.B) {
				ctx := context.Background()
				for b.Loop() {
					db := NewDB()
					for chunk := range slices.Chunk(order.spans, batch) {
						if err := db.ExportSpans(ctx, chunk); err != nil {
							b.Fatal(err)
						}
					}
					if len(db.Spans.Order) != n {
						b.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
					}
				}
			})
		}
	}
}
