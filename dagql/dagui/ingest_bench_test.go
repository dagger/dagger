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

// concurrentLiveTrace builds the updates a live run delivers: width spans
// that keep running, such as the tests of a large go test run, beneath a
// chain of depth shared ancestors, and n short spans beneath them that arrive
// once, already ended, as most spans do. It returns the batches that start the
// running spans and their ancestors, then the batches of ended spans.
func concurrentLiveTrace(n, width, depth int) (running, ended [][]sdktrace.ReadOnlySpan) {
	traceID := trace.TraceID{2}
	spanID := func(i int) trace.SpanID {
		var id trace.SpanID
		binary.BigEndian.PutUint64(id[:], uint64(i)+1)
		return id
	}
	epoch := time.Unix(1_700_000_000, 0)
	stub := func(i, parent int, ended bool) sdktrace.ReadOnlySpan {
		start := epoch.Add(time.Duration(i) * time.Microsecond)
		s := tracetest.SpanStub{
			Name: fmt.Sprintf("span %d", i),
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID(i),
			}),
			StartTime: start,
		}
		if parent >= 0 {
			s.Parent = trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID,
				SpanID:  spanID(parent),
			})
		}
		if ended {
			s.EndTime = start.Add(time.Microsecond)
		}
		return s.Snapshot()
	}
	for i := range depth {
		running = append(running, []sdktrace.ReadOnlySpan{stub(i, i-1, false)})
	}
	for i := depth; i < depth+width; i++ {
		running = append(running, []sdktrace.ReadOnlySpan{stub(i, depth-1, false)})
	}
	const batch = 64
	for i := 0; i < n; i += batch {
		var spans []sdktrace.ReadOnlySpan
		for j := i; j < min(n, i+batch); j++ {
			id := depth + width + j
			spans = append(spans, stub(id, depth+j%width, true))
		}
		ended = append(ended, spans)
	}
	return running, ended
}

// BenchmarkDBIngestLive feeds a live run through the ingestion path in
// export-sized batches, with more and more spans running at once.
func BenchmarkDBIngestLive(b *testing.B) {
	const n, depth = 20_000, 20
	for _, width := range []int{100, 1_000, 5_000} {
		running, ended := concurrentLiveTrace(n, width, depth)
		batches := slices.Concat(running, ended)
		b.Run(fmt.Sprintf("running=%d", width), func(b *testing.B) {
			ctx := context.Background()
			for b.Loop() {
				db := NewDB()
				for _, batch := range batches {
					if err := db.ExportSpans(ctx, batch); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// startThenEnd returns the batches of a live session for the ended spans:
// each span first as it starts, then, a batch later, as it ends, so every span
// joins and then leaves the running spans of each of its ancestors.
func startThenEnd(ended [][]sdktrace.ReadOnlySpan) [][]sdktrace.ReadOnlySpan {
	var batches [][]sdktrace.ReadOnlySpan
	var prev []sdktrace.ReadOnlySpan
	for _, batch := range ended {
		starts := make([]sdktrace.ReadOnlySpan, len(batch))
		for i, span := range batch {
			starts[i] = tracetest.SpanStub{
				Name:        span.Name(),
				SpanContext: span.SpanContext(),
				Parent:      span.Parent(),
				StartTime:   span.StartTime(),
			}.Snapshot()
		}
		batches = append(batches, slices.Concat(prev, starts))
		prev = batch
	}
	return append(batches, prev)
}

// BenchmarkDBIngestLiveStartEnd is BenchmarkDBIngestLive with each short span
// delivered twice, as a live session receives it: once when it starts and
// once when it ends.
func BenchmarkDBIngestLiveStartEnd(b *testing.B) {
	const n, depth = 20_000, 20
	for _, width := range []int{100, 1_000, 5_000} {
		running, ended := concurrentLiveTrace(n, width, depth)
		batches := slices.Concat(running, startThenEnd(ended))
		b.Run(fmt.Sprintf("running=%d", width), func(b *testing.B) {
			ctx := context.Background()
			for b.Loop() {
				db := NewDB()
				for _, batch := range batches {
					if err := db.ExportSpans(ctx, batch); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
