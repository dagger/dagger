package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	telemetry "github.com/dagger/otel-go"
)

// lateSpan describes one span of a test trace, for importing it either as a
// snapshot or as an OTLP span.
type lateSpan struct {
	id, parent         byte
	name               string
	start, end         float64
	internal, passthru bool
	// evaluates and installs are a lazy evaluation's links: the call it
	// evaluates (no purpose) and a site its result was installed at (cause).
	evaluates, installs byte
}

// A deferred evaluation of an internal call is the work of the user's call
// around it, as in TestTimeBreakdownLazyEvaluationOfInternalCall:
//
//	1 query [0,20]
//	├── 2 "Container.from" [1,1.5]
//	│   └── 3 "Container.from" (passthrough) [1,1.5]
//	│       └── 4 "Container.from" (internal) [1.1,1.4]
//	└── 5 "Container.withExec" [2,12]
//	    └── 6 "resume from" [2,12]  (evaluates 4; installed at 2)
var internalEvaluation = map[byte]lateSpan{
	1: {id: 1, name: "query", start: 0, end: 20},
	2: {id: 2, parent: 1, name: "Container.from", start: 1, end: 1.5},
	3: {id: 3, parent: 2, name: "Container.from", start: 1, end: 1.5, passthru: true},
	4: {id: 4, parent: 3, name: "Container.from", start: 1.1, end: 1.4, internal: true},
	5: {id: 5, parent: 1, name: "Container.withExec", start: 2, end: 12},
	6: {id: 6, parent: 5, name: "resume from", start: 2, end: 12, evaluates: 4, installs: 2},
}

func (s lateSpan) snapshot() SpanSnapshot {
	snap := SpanSnapshot{
		ID: testID(s.id), Name: s.name, StartTime: at(s.start), EndTime: at(s.end),
		Internal: s.internal, Passthrough: s.passthru,
	}
	if s.parent != 0 {
		snap.ParentID = testID(s.parent)
	}
	if s.evaluates != 0 {
		snap.Links = append(snap.Links, evalLink(testID(s.evaluates)))
	}
	if s.installs != 0 {
		snap.Links = append(snap.Links, causeLink(testID(s.installs)))
	}
	return snap
}

func (s lateSpan) otel() sdktrace.ReadOnlySpan {
	ctx := func(id byte) trace.SpanContext {
		return trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: testID(id).SpanID})
	}
	stub := tracetest.SpanStub{Name: s.name, SpanContext: ctx(s.id), StartTime: at(s.start), EndTime: at(s.end)}
	if s.parent != 0 {
		stub.Parent = ctx(s.parent)
	}
	if s.internal {
		stub.Attributes = append(stub.Attributes, attribute.Bool(telemetry.UIInternalAttr, true))
	}
	if s.passthru {
		stub.Attributes = append(stub.Attributes, attribute.Bool(telemetry.UIPassthroughAttr, true))
	}
	if s.evaluates != 0 {
		stub.Links = append(stub.Links, sdktrace.Link{SpanContext: ctx(s.evaluates)})
	}
	if s.installs != 0 {
		stub.Links = append(stub.Links, sdktrace.Link{
			SpanContext: ctx(s.installs),
			Attributes:  []attribute.KeyValue{attribute.String(telemetry.LinkPurposeAttr, telemetry.LinkPurposeCause)},
		})
	}
	return stub.Snapshot()
}

// importLate imports spans of trace in batches, as snapshots (the web view's
// path) or as OTLP spans (the terminal's).
func importLate(t *testing.T, otlp bool, spans map[byte]lateSpan, batches [][]byte) *DB {
	t.Helper()
	db := NewDB()
	for _, batch := range batches {
		if otlp {
			var otelSpans []sdktrace.ReadOnlySpan
			for _, id := range batch {
				otelSpans = append(otelSpans, spans[id].otel())
			}
			if err := db.ExportSpans(t.Context(), otelSpans); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var snaps []SpanSnapshot
		for _, id := range batch {
			snaps = append(snaps, spans[id].snapshot())
		}
		db.ImportSnapshots(snaps)
	}
	return db
}

// The user's call gets the evaluation's time whatever order the spans
// arrive in: an internal call, or its passthrough parent, arriving after
// the evaluation joins its ancestors only then.
func TestLateAncestorsGetEvaluationTime(t *testing.T) {
	for name, batches := range map[string][][]byte{
		"all at once":                     {{1, 2, 3, 4, 5, 6}},
		"evaluated call later":            {{1, 2, 3, 5, 6}, {4}},
		"evaluated call, then its parent": {{1, 2, 5, 6}, {4}, {3}},
	} {
		for _, otlp := range []bool{false, true} {
			ingest := "snapshots"
			if otlp {
				ingest = "otlp"
			}
			t.Run(name+", "+ingest, func(t *testing.T) {
				db := importLate(t, otlp, internalEvaluation, batches)
				user := db.Spans.Map[testID(2)]
				want := 10*time.Second + 500*time.Millisecond
				if got := user.Activity.Duration(at(20)); got != want {
					t.Fatalf("the user's call should be credited the evaluation: got %v, want %v", got, want)
				}
				if hb := user.TimeBreakdown(at(20)); hb.Material || hb.Self != want {
					t.Fatalf("the user's call's own work should be %v: %+v", want, hb)
				}
			})
		}
	}
}

// Loading a trace in parts by span ID brings no ancestors along. A row that
// forced the evaluation of an internal call reads as working until the
// call's ancestry up to the user's call is loaded, naming each next span to
// load; then it waits on the user's call, by name.
func TestTimeBreakdownHiddenBlockerAncestryLoadsInRounds(t *testing.T) {
	db := importLate(t, false, internalEvaluation, [][]byte{{1, 5, 6}})
	row := db.Spans.Map[testID(5)]
	for _, round := range []byte{4, 3, 2} {
		hb := row.TimeBreakdown(at(20))
		if hb.Material || hb.Waiting != 0 || hb.Self != 10*time.Second {
			t.Fatalf("before %d loads, the row should read as working: %+v", round, hb)
		}
		if !visitedBreakdownSpans(row)[testID(round)] {
			t.Fatalf("the breakdown should name %d to load", round)
		}
		db.ImportSnapshots([]SpanSnapshot{internalEvaluation[round].snapshot()})
	}
	segs := waitingSegments(row.TimeBreakdown(at(20)))
	if len(segs) != 1 || segs[0].Target != testID(2) || segs[0].Label != "Container.from" || segs[0].Duration() != 10*time.Second {
		t.Fatalf("once loaded, the row should wait on the user's call: %+v", segs)
	}
}
