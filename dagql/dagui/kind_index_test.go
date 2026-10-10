package dagui

import (
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// TestKindIndexMatchesScan checks that the kind index holds exactly the spans
// a scan of the whole DB finds, in the same order, as spans arrive and as a
// later snapshot of a span adds or drops its kind.
func TestKindIndexMatchesScan(t *testing.T) {
	db := NewDB()
	traceID := TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	snap := func(n int, edit func(*SpanSnapshot)) SpanSnapshot {
		s := SpanSnapshot{
			ID:        walkTestSpanID(n),
			TraceID:   traceID,
			Name:      "span",
			StartTime: start.Add(time.Duration((n*7)%41) * time.Second),
			EndTime:   start.Add(time.Hour),
		}
		if n > 1 {
			s.ParentID = walkTestSpanID(1)
		}
		if edit != nil {
			edit(&s)
		}
		return s
	}
	kinds := []func(*SpanSnapshot){
		nil,
		func(s *SpanSnapshot) { s.LLMRole = "user" },
		func(s *SpanSnapshot) { s.Agent = true },
		func(s *SpanSnapshot) { s.CheckName = "check" },
		func(s *SpanSnapshot) { s.GeneratorName = "gen" },
		func(s *SpanSnapshot) { s.TestCaseName = "case" },
		func(s *SpanSnapshot) { s.TestSuiteName = "suite" },
		func(s *SpanSnapshot) { s.Service = true; s.Internal = true },
		func(s *SpanSnapshot) { s.ServiceName = "svc" },
		func(s *SpanSnapshot) { s.AgentRewindFrom, s.AgentRewindTo = "a", "b" },
		func(s *SpanSnapshot) { s.GenerateSkipped = true },
		func(s *SpanSnapshot) { s.GenerateRegenerated = true },
	}
	check := func() {
		t.Helper()
		var want []*Span
		for _, span := range db.Spans.Order {
			if isIndexedKind(span) {
				want = append(want, span)
			}
		}
		if got := slices.Collect(db.kindSpanIter()); !slices.Equal(got, want) {
			t.Fatalf("kind index = %v, want %v", got, want)
		}
	}
	var snaps []SpanSnapshot
	for n := 1; n <= 40; n++ {
		snaps = append(snaps, snap(n, kinds[n%len(kinds)]))
	}
	db.ImportSnapshots(snaps)
	check()
	// Later snapshots move spans in and out of the index.
	db.ImportSnapshots([]SpanSnapshot{
		snap(2, nil),
		snap(3, kinds[1]),
		snap(13, kinds[0]),
		snap(24, kinds[4]),
	})
	check()
}
