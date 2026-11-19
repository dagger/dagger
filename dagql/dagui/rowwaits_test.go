package dagui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// rowSpan describes one span of a test trace for the waiting below rows.
type rowSpan struct {
	id, parent byte
	name       string
	// end below zero: still running
	start, end float64
	links      []SpanLink
	// kind is the span's wcprof op kind (call_exec, exec)
	kind     string
	plumbing bool
	// children is one more than the child count the trace's stream reports
	// for the span; zero reports none
	children int
}

func (s rowSpan) snapshot() SpanSnapshot {
	snap := SpanSnapshot{
		ID: testID(s.id), Name: s.name, StartTime: at(s.start),
		Links: s.links, FunctionPlumbing: s.plumbing,
	}
	if s.end >= 0 {
		snap.EndTime = at(s.end)
	}
	if s.parent != 0 {
		snap.ParentID = testID(s.parent)
	}
	if s.kind != "" {
		snap.ProcessAttribute(telemetryattrs.WcprofOpKindAttr, s.kind)
	}
	if s.children != 0 {
		snap.ProcessAttribute(telemetryattrs.UIChildCountAttr, int64(s.children-1))
	}
	return snap
}

func importRowSpans(db *DB, spans ...rowSpan) {
	snaps := make([]SpanSnapshot, len(spans))
	for i, s := range spans {
		snaps[i] = s.snapshot()
	}
	db.ImportSnapshots(snaps)
}

// A module function's step waits on a step another module runs:
//
//	1 query [0,100]
//	├── 2 "apt-get install" [10,70]           (another module's step)
//	└── 3 "Project.build" [5,80]              (the function call)
//	    ├── 4 "Container.from" [5,10]
//	    └── 5 "Container.exitCode" [10,75]    (waits on 2 for [10,70])
var waitBelowTrace = []rowSpan{
	{id: 1, name: "query", start: 0, end: 100},
	{id: 2, parent: 1, name: "apt-get install", start: 10, end: 70},
	{id: 3, parent: 1, name: "Project.build", start: 5, end: 80},
	{id: 4, parent: 3, name: "Container.from", start: 5, end: 10},
	{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: 75,
		links: []SpanLink{waitLink(testID(2), "lazy", at(10), at(70))}},
}

// updateRowWaits brings the waiting below rows up to date in one go.
func updateRowWaits(db *DB, now time.Time) []*Span {
	changed, more := db.UpdateRowWaits(now, 0)
	if more {
		panic("an update without a budget should finish")
	}
	return changed
}

func rowWaitsDB(spans ...rowSpan) *DB {
	db := NewDB()
	importRowSpans(db, spans...)
	return db
}

func checkWaiting(t *testing.T, hb *TimeBreakdown, self time.Duration, want ...TimeSegment) {
	t.Helper()
	segs := waitingSegments(hb)
	if hb.Self != self || len(segs) != len(want) {
		t.Fatalf("got own work %v and waiting %+v, want %v and %d stretches", hb.Self, segs, self, len(want))
	}
	for i, w := range want {
		got := segs[i]
		if !got.Start.Equal(w.Start) || !got.End.Equal(w.End) || got.Target != w.Target || got.Label != w.Label || got.Via != w.Via {
			t.Fatalf("stretch %d: got %+v, want %+v", i, got, w)
		}
	}
}

func waitingOn(from, to float64, target byte, label, via string) TimeSegment {
	return TimeSegment{Start: at(from), End: at(to), Target: testID(target), Label: label, Via: via}
}

// The function call reads as working until the DB computes the waiting below
// rows; then it waits while its step waited and nothing else under it worked.
func TestRowWaitsBelow(t *testing.T) {
	db := rowWaitsDB(waitBelowTrace...)
	row := db.Spans.Map[testID(3)]
	checkWaiting(t, row.TimeBreakdown(at(100)), 75*time.Second)

	if len(updateRowWaits(db, at(100))) == 0 {
		t.Fatal("the update should report a change")
	}
	hb := row.TimeBreakdown(at(100))
	checkWaiting(t, hb, 15*time.Second, waitingOn(10, 70, 2, "apt-get install", "Container.exitCode"))
	if !hb.Material || hb.DominantTarget != testID(2) {
		t.Fatalf("the waiting should be drawn, on the other module's step: %+v", hb)
	}
	// the step that waited reads as it always did
	checkWaiting(t, db.Spans.Map[testID(5)].TimeBreakdown(at(100)), 5*time.Second,
		waitingOn(10, 70, 2, "apt-get install", ""))
	if len(updateRowWaits(db, at(100))) != 0 {
		t.Fatal("an update with nothing new should change nothing")
	}
}

// While another step under the row works, the row is working.
func TestRowWaitsConcurrentWorkWins(t *testing.T) {
	db := rowWaitsDB(append(waitBelowTrace,
		rowSpan{id: 6, parent: 3, name: "Container.withExec", start: 20, end: 30})...)
	updateRowWaits(db, at(100))
	checkWaiting(t, db.Spans.Map[testID(3)].TimeBreakdown(at(100)), 25*time.Second,
		waitingOn(10, 20, 2, "apt-get install", "Container.exitCode"),
		waitingOn(30, 70, 2, "apt-get install", "Container.exitCode"))
}

// A wait rises to just below the lowest row holding both the step that waited
// and the step it waited on.
func TestRowWaitsRiseBelowCommonRow(t *testing.T) {
	spans := []rowSpan{
		{id: 1, name: "query", start: 0, end: 100},
		{id: 7, parent: 1, name: "App.publish", start: 0, end: 90},
	}
	for _, s := range waitBelowTrace[1:] {
		if s.parent == 1 {
			s.parent = 7
		}
		spans = append(spans, s)
	}
	db := rowWaitsDB(spans...)
	updateRowWaits(db, at(100))
	checkWaiting(t, db.Spans.Map[testID(3)].TimeBreakdown(at(100)), 15*time.Second,
		waitingOn(10, 70, 2, "apt-get install", "Container.exitCode"))
	checkWaiting(t, db.Spans.Map[testID(7)].TimeBreakdown(at(100)), 90*time.Second)
}

// A lazy evaluation of the other step, run beneath the step that forced it, is
// the other step's work, not the row's.
func TestRowWaitsLazyEvaluationIsNotWorkWhereItRuns(t *testing.T) {
	spans := append([]rowSpan{}, waitBelowTrace[:4]...)
	spans = append(spans,
		rowSpan{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: 75},
		rowSpan{id: 8, parent: 5, name: "resume withExec", start: 10, end: 70,
			links: []SpanLink{evalLink(testID(2))}},
	)
	db := rowWaitsDB(spans...)
	updateRowWaits(db, at(100))
	segs := waitingSegments(db.Spans.Map[testID(3)].TimeBreakdown(at(100)))
	if len(segs) != 1 || segs[0].Target != testID(2) || segs[0].Duration() != 60*time.Second {
		t.Fatalf("the row should wait 60s on the evaluated step: %+v", segs)
	}
}

// A module function's process runs for the whole call; it is not the row's
// work. What the entrypoint does before starting it is.
func TestRowWaitsFunctionProcessIsNotWork(t *testing.T) {
	spans := append([]rowSpan{}, waitBelowTrace...)
	spans[2].start = 0
	spans = append(spans,
		rowSpan{id: 9, parent: 3, name: "Project.build", start: 0, end: 80, kind: "call_exec"},
		rowSpan{id: 10, parent: 9, name: "entrypoint", start: 0, end: 80, plumbing: true},
		rowSpan{id: 11, parent: 10, name: "pull image", start: 0, end: 2},
		rowSpan{id: 12, parent: 10, name: "exec python -m dagger.mod call", start: 2, end: 79, kind: "exec"},
	)
	db := rowWaitsDB(spans...)
	updateRowWaits(db, at(100))
	checkWaiting(t, db.Spans.Map[testID(3)].TimeBreakdown(at(100)), 20*time.Second,
		waitingOn(10, 70, 2, "apt-get install", "Container.exitCode"))
}

// A wait on a step whose place in the tree isn't known stays where it was
// recorded until the step's ancestors arrive.
func TestRowWaitsHeldBackUntilPlaced(t *testing.T) {
	spans := append([]rowSpan{}, waitBelowTrace...)
	spans[1].parent = 13 // another module's call, not yet arrived
	db := rowWaitsDB(spans...)
	updateRowWaits(db, at(100))
	row := db.Spans.Map[testID(3)]
	checkWaiting(t, row.TimeBreakdown(at(100)), 75*time.Second)

	importRowSpans(db, rowSpan{id: 13, parent: 1, name: "Other.image", start: 9, end: 71})
	updateRowWaits(db, at(100))
	checkWaiting(t, row.TimeBreakdown(at(100)), 15*time.Second,
		waitingOn(10, 70, 2, "apt-get install", "Container.exitCode"))
}

// A wait on another trace rises.
func TestRowWaitsOnAnotherTrace(t *testing.T) {
	link := waitLink(testID(2), "lazy", at(10), at(70))
	link.SpanContext.TraceID = TraceID{TraceID: [16]byte{1}}
	spans := append([]rowSpan{}, waitBelowTrace...)
	spans = append(spans[:1], spans[2:]...)
	spans[3].links = []SpanLink{link}
	db := rowWaitsDB(spans...)
	updateRowWaits(db, at(100))
	segs := waitingSegments(db.Spans.Map[testID(3)].TimeBreakdown(at(100)))
	if len(segs) != 1 || segs[0].Label != "an operation outside this trace" || segs[0].Duration() != 60*time.Second {
		t.Fatalf("the row should wait 60s outside this trace: %+v", segs)
	}
}

// A request that joined a call another function was running waits on what
// that call waited on: the wait chain continues through the waits recorded
// below the call.
//
//	1 query [0,100]
//	├── 2 "apt-get install" [10,70]
//	├── 3 "Other.image" [10,72]               (the call, run by another function)
//	│   └── 5 "Container.exitCode" [10,71]    (waits on 2 for [10,70])
//	└── 6 "Project.build" [8,80]
//	    └── 7 "Other.image" [10,72]           (joined 3: waits on it [10,72])
func TestRowWaitsChainContinuesBelowJoinedCall(t *testing.T) {
	db := rowWaitsDB(
		rowSpan{id: 1, name: "query", start: 0, end: 100},
		rowSpan{id: 2, parent: 1, name: "apt-get install", start: 10, end: 70},
		rowSpan{id: 3, parent: 1, name: "Other.image", start: 10, end: 72},
		rowSpan{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: 71,
			links: []SpanLink{waitLink(testID(2), "lazy", at(10), at(70))}},
		rowSpan{id: 6, parent: 1, name: "Project.build", start: 8, end: 80},
		rowSpan{id: 7, parent: 6, name: "Other.image", start: 10, end: 72,
			links: []SpanLink{waitLink(testID(3), "singleflight", at(10), at(72))}},
	)
	joined := db.Spans.Map[testID(7)]
	checkWaiting(t, joined.TimeBreakdown(at(100)), 0, waitingOn(10, 72, 3, "Other.image", ""))

	updateRowWaits(db, at(100))
	segs := waitingSegments(joined.TimeBreakdown(at(100)))
	if len(segs) != 2 || segs[0].Target != testID(2) || segs[0].Duration() != 60*time.Second ||
		segs[1].Target != testID(3) || segs[1].Duration() != 2*time.Second {
		t.Fatalf("the joined request should wait on what the call waited on: %+v", segs)
	}
	segs = waitingSegments(db.Spans.Map[testID(6)].TimeBreakdown(at(100)))
	if len(segs) != 2 || segs[0].Target != testID(2) || segs[0].Via != "Other.image" {
		t.Fatalf("the function call should wait on it too: %+v", segs)
	}
}

// A view holding part of a trace leaves a row's reading as it was until its
// whole subtree is loaded.
func TestRowWaitsPartialTrace(t *testing.T) {
	counted := map[byte]int{1: 3, 2: 1, 3: 3, 4: 1, 5: 1}
	var spans []rowSpan
	for _, s := range waitBelowTrace {
		s.children = counted[s.id]
		spans = append(spans, s)
	}
	db := NewDB()
	db.PartialTrace = true
	importRowSpans(db, spans[0], spans[1], spans[2], spans[4])
	updateRowWaits(db, at(100))
	row := db.Spans.Map[testID(3)]
	checkWaiting(t, row.TimeBreakdown(at(100)), 75*time.Second)

	importRowSpans(db, spans[3])
	updateRowWaits(db, at(100))
	checkWaiting(t, row.TimeBreakdown(at(100)), 15*time.Second,
		waitingOn(10, 70, 2, "apt-get install", "Container.exitCode"))

	// a stream reading the span by ID doesn't count its children
	uncounted := spans[2]
	uncounted.children = 0
	importRowSpans(db, uncounted)
	updateRowWaits(db, at(100))
	if got := waitingSegments(row.TimeBreakdown(at(100))); len(got) != 1 {
		t.Fatalf("the row's subtree is still loaded: %+v", got)
	}
}

// A row still running waits until the next update when it last waited.
func TestRowWaitsLive(t *testing.T) {
	db := rowWaitsDB(
		rowSpan{id: 1, name: "query", start: 0, end: -1},
		rowSpan{id: 2, parent: 1, name: "apt-get install", start: 10, end: -1},
		rowSpan{id: 3, parent: 1, name: "Project.build", start: 5, end: -1},
		rowSpan{id: 4, parent: 3, name: "Container.from", start: 5, end: 10},
		rowSpan{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: -1},
		rowSpan{id: 8, parent: 5, name: "resume withExec", start: 10, end: -1,
			links: []SpanLink{evalLink(testID(2))}},
	)
	updateRowWaits(db, at(30))
	row := db.Spans.Map[testID(3)]
	checkWaiting(t, row.TimeBreakdown(at(30.5)), 5*time.Second,
		waitingOn(10, 30.5, 2, "apt-get install", "Container.exitCode"))
	if blocker, ok := row.WaitingNow(at(30.5)); !ok || blocker.ID != testID(2) {
		t.Fatalf("the row should be waiting now on the other step: %v %v", blocker, ok)
	}
	if len(updateRowWaits(db, at(31))) == 0 {
		t.Fatal("a running row's waiting changes as time passes")
	}
}

// A remote frontend reads a row's waiting from its snapshot, without
// computing it, and keeps it when a raw snapshot of the span arrives.
func TestRowWaitsInSnapshots(t *testing.T) {
	server := rowWaitsDB(waitBelowTrace...)
	updateRowWaits(server, at(100))
	var snaps []SpanSnapshot
	for _, span := range server.Spans.Order {
		snaps = append(snaps, span.Snapshot())
	}
	encoded, err := json.Marshal(snaps)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []SpanSnapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	browser := NewDB()
	browser.ImportSnapshots(decoded)
	row := browser.Spans.Map[testID(3)]
	want := waitingOn(10, 70, 2, "apt-get install", "Container.exitCode")
	checkWaiting(t, row.TimeBreakdown(at(100)), 15*time.Second, want)

	browser.ImportSnapshots([]SpanSnapshot{waitBelowTrace[2].snapshot()})
	checkWaiting(t, row.TimeBreakdown(at(100)), 15*time.Second, want)
}

// Keeping the waiting up to date as spans arrive gives what computing it from
// scratch does, whatever order they arrive in.
//
//	1 query [0,100]
//	└── 20 "App.publish" [0,95]
//	    ├── 24 "Base.image" [9,72]
//	    │   ├── 2 "apt-get install" [10,40]
//	    │   └── 22 "bash -c install" [40,70]
//	    ├── 21 "Other.image" [9,71]
//	    │   └── 23 "Container.exitCode" [9,71]    (waits on 2, then on 22)
//	    ├── 3 "Project.build" [5,80]
//	    │   ├── 4 "Container.from" [5,10]
//	    │   ├── 5 "Container.exitCode" [10,75]    (joined 21 [10,71])
//	    │   └── 6 "Container.withExec" [20,30]
//	    └── 7 "Uv.builder" [12,78]
//	        └── 8 "Container.exitCode" [12,77]
//	            └── 9 "resume withExec" [12,70]   (evaluates 22)
func TestRowWaitsUpdatesMatchFreshComputation(t *testing.T) {
	trace := []rowSpan{
		{id: 1, name: "query", start: 0, end: 100},
		{id: 20, parent: 1, name: "App.publish", start: 0, end: 95},
		{id: 24, parent: 20, name: "Base.image", start: 9, end: 72},
		{id: 2, parent: 24, name: "apt-get install", start: 10, end: 40},
		{id: 22, parent: 24, name: "bash -c install", start: 40, end: 70},
		{id: 21, parent: 20, name: "Other.image", start: 9, end: 71},
		{id: 23, parent: 21, name: "Container.exitCode", start: 9, end: 71,
			links: []SpanLink{waitLink(testID(2), "lazy", at(10), at(40)), waitLink(testID(22), "lazy", at(40.5), at(70))}},
		{id: 3, parent: 20, name: "Project.build", start: 5, end: 80},
		{id: 4, parent: 3, name: "Container.from", start: 5, end: 10},
		{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: 75,
			links: []SpanLink{waitLink(testID(21), "singleflight", at(10), at(71))}},
		{id: 6, parent: 3, name: "Container.withExec", start: 20, end: 30},
		{id: 7, parent: 20, name: "Uv.builder", start: 12, end: 78},
		{id: 8, parent: 7, name: "Container.exitCode", start: 12, end: 77},
		{id: 9, parent: 8, name: "resume withExec", start: 12, end: 70,
			links: []SpanLink{evalLink(testID(22))}},
	}
	byID := map[byte]rowSpan{}
	for _, s := range trace {
		byID[s.id] = s
	}
	orders := map[string][][]byte{
		"parents first":  {{1, 20, 24, 2, 22, 21, 23}, {3, 4, 5, 6}, {7, 8, 9}},
		"children first": {{9, 8, 5, 23}, {6, 4, 22, 2}, {7, 3, 21, 24}, {20, 1}},
		"one at a time":  {{23}, {5}, {9}, {1}, {3}, {21}, {2}, {8}, {7}, {20}, {22}, {4}, {6}, {24}},
	}
	for name, batches := range orders {
		t.Run(name, func(t *testing.T) {
			db := NewDB()
			var seen []rowSpan
			for i, batch := range batches {
				var spans []rowSpan
				for _, id := range batch {
					spans = append(spans, byID[id])
				}
				importRowSpans(db, spans...)
				seen = append(seen, spans...)
				updateRowWaits(db, at(100))

				fresh := rowWaitsDB(seen...)
				updateRowWaits(fresh, at(100))
				for _, s := range seen {
					got := describeRowWaits(db.Spans.Map[testID(s.id)].TimeBreakdown(at(100)))
					want := describeRowWaits(fresh.Spans.Map[testID(s.id)].TimeBreakdown(at(100)))
					if got != want {
						t.Fatalf("after batch %d, span %d: kept up to date\n%s\nfrom scratch\n%s", i, s.id, got, want)
					}
				}
			}
			// The function call waited on what the call its step joined
			// waited on, except while its own step ran.
			var targets []SpanID
			for _, seg := range waitingSegments(db.Spans.Map[testID(3)].TimeBreakdown(at(100))) {
				targets = append(targets, seg.Target)
			}
			if fmt.Sprint(targets) != fmt.Sprint([]SpanID{testID(2), testID(2), testID(22), testID(21)}) {
				t.Fatalf("the function call should wait on the steps of the other module: %v", describeRowWaits(db.Spans.Map[testID(3)].TimeBreakdown(at(100))))
			}
			checkWaiting(t, db.Spans.Map[testID(7)].TimeBreakdown(at(100)), 8*time.Second,
				waitingOn(12, 70, 22, "bash -c install", "Container.exitCode"))
			checkWaiting(t, db.Spans.Map[testID(20)].TimeBreakdown(at(100)), 95*time.Second)
		})
	}
}

func describeRowWaits(hb *TimeBreakdown) string {
	s := fmt.Sprintf("own %v", hb.Self)
	for _, seg := range waitingSegments(hb) {
		s += fmt.Sprintf("\n  %v-%v %v %q via %q %v", seg.Start.Sub(testEpoch), seg.End.Sub(testEpoch), seg.Target, seg.Label, seg.Via, seg.Blockers)
	}
	return s
}

// The terminal reads a trace loaded in parts as OTLP spans, marked partial
// and with their child counts, rather than as snapshots.
func TestRowWaitsPartialTraceOverOTLP(t *testing.T) {
	counted := map[byte]int64{1: 2, 2: 0, 3: 2, 4: 0, 5: 0}
	otel := func(s rowSpan) sdktrace.ReadOnlySpan {
		ctx := func(id byte) trace.SpanContext {
			return trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: testID(id).SpanID})
		}
		stub := tracetest.SpanStub{
			Name: s.name, SpanContext: ctx(s.id), StartTime: at(s.start), EndTime: at(s.end),
			Attributes: []attribute.KeyValue{
				attribute.Bool(telemetryattrs.UIPartialAttr, true),
				attribute.Int64(telemetryattrs.UIChildCountAttr, counted[s.id]),
			},
		}
		if s.parent != 0 {
			stub.Parent = ctx(s.parent)
		}
		for _, link := range s.links {
			stub.Links = append(stub.Links, sdktrace.Link{
				SpanContext: ctx(link.SpanContext.SpanID.SpanID[0]),
				Attributes: []attribute.KeyValue{
					attribute.String(telemetry.LinkPurposeAttr, link.Purpose),
					attribute.String(telemetryattrs.WcprofWaitReasonAttr, link.WaitReason),
					attribute.String(telemetryattrs.WcprofWaitStartUnixNanoAttr, strconv.FormatInt(link.WaitStart.UnixNano(), 10)),
					attribute.String(telemetryattrs.WcprofWaitEndUnixNanoAttr, strconv.FormatInt(link.WaitEnd.UnixNano(), 10)),
				},
			})
		}
		return stub.Snapshot()
	}
	export := func(db *DB, spans ...rowSpan) {
		var otelSpans []sdktrace.ReadOnlySpan
		for _, s := range spans {
			otelSpans = append(otelSpans, otel(s))
		}
		if err := db.ExportSpans(t.Context(), otelSpans); err != nil {
			t.Fatal(err)
		}
	}
	db := NewDB()
	export(db, waitBelowTrace[0], waitBelowTrace[1], waitBelowTrace[2], waitBelowTrace[4])
	if !db.PartialTrace {
		t.Fatal("spans from a partial stream should mark the DB partial")
	}
	updateRowWaits(db, at(100))
	row := db.Spans.Map[testID(3)]
	checkWaiting(t, row.TimeBreakdown(at(100)), 75*time.Second)

	export(db, waitBelowTrace[3])
	updateRowWaits(db, at(100))
	checkWaiting(t, row.TimeBreakdown(at(100)), 15*time.Second,
		waitingOn(10, 70, 2, "apt-get install", "Container.exitCode"))
}

// An update with a budget stops once the budget is spent, and later calls
// carry on to the result a single update gives.
func TestRowWaitsInSlices(t *testing.T) {
	whole := rowWaitsDB(waitBelowTrace...)
	updateRowWaits(whole, at(100))

	db := rowWaitsDB(waitBelowTrace...)
	calls := 0
	for more := true; more; calls++ {
		_, more = db.UpdateRowWaits(at(100), time.Nanosecond)
		if calls > len(waitBelowTrace) {
			t.Fatal("sliced updates should finish")
		}
		if calls == 0 && !more {
			t.Fatal("a spent budget should leave work for the next call")
		}
	}
	for _, s := range waitBelowTrace {
		got := describeRowWaits(db.Spans.Map[testID(s.id)].TimeBreakdown(at(100)))
		want := describeRowWaits(whole.Spans.Map[testID(s.id)].TimeBreakdown(at(100)))
		if got != want {
			t.Fatalf("span %d: in slices\n%s\nat once\n%s", s.id, got, want)
		}
	}
}

// A wait on a span of this trace not loaded doesn't rise as a wait outside
// the trace, though merging adjoining waits names the longer one after it.
func TestRowWaitsUnloadedTargetBesideOneOutside(t *testing.T) {
	outsideLink := waitLink(testID(9), "lazy", at(30), at(70))
	outsideLink.SpanContext.TraceID = TraceID{TraceID: [16]byte{1}}
	db := rowWaitsDB(
		rowSpan{id: 1, name: "query", start: 0, end: 100},
		rowSpan{id: 3, parent: 1, name: "Project.build", start: 5, end: 80},
		rowSpan{id: 4, parent: 3, name: "Container.from", start: 5, end: 10},
		rowSpan{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: 75,
			links: []SpanLink{waitLink(testID(8), "lazy", at(10), at(30)), outsideLink}},
	)
	updateRowWaits(db, at(100))
	for _, seg := range waitingSegments(db.Spans.Map[testID(3)].TimeBreakdown(at(100))) {
		if seg.Start.Before(at(30)) && seg.End.After(at(10)) {
			t.Fatalf("the wait on a span not loaded should not rise: %+v", seg)
		}
	}
}

// A view drawing a row's waiting below it needs the steps it names, placed.
func TestRowWaitsNameTheirBlockersToLoad(t *testing.T) {
	spans := append([]rowSpan{}, waitBelowTrace...)
	spans[1].parent = 13
	spans = append(spans, rowSpan{id: 13, parent: 1, name: "Base.image", start: 9, end: 71})
	db := rowWaitsDB(spans...)
	updateRowWaits(db, at(100))
	visited := visitedBreakdownSpans(db.Spans.Map[testID(3)])
	for _, id := range []byte{2, 13, 1} {
		if !visited[testID(id)] {
			t.Fatalf("the row's breakdown should draw on %d: %v", id, visited)
		}
	}
}

// A call arriving after the waiting was computed renames the step it names.
func TestRowWaitsLateCallRenamesBlocker(t *testing.T) {
	spans := append([]rowSpan{}, waitBelowTrace...)
	db := NewDB()
	snaps := make([]SpanSnapshot, len(spans))
	for i, s := range spans {
		snaps[i] = s.snapshot()
	}
	snaps[1].Name = "Container.withExec"
	snaps[1].CallDigest = "sha256:late"
	db.ImportSnapshots(snaps)
	updateRowWaits(db, at(100))
	row := db.Spans.Map[testID(3)]
	if segs := waitingSegments(row.TimeBreakdown(at(100))); len(segs) != 1 || segs[0].Label != "Container.withExec" {
		t.Fatalf("before its call arrives, the step reads by its name: %+v", segs)
	}
	db.addCall("sha256:late", &callpbv1.Call{
		Field: "withExec",
		Type:  &callpbv1.Type{NamedType: "Container"},
		Args: []*callpbv1.Argument{{
			Name: "args",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{
				Values: []*callpbv1.Literal{
					{Value: &callpbv1.Literal_String_{String_: "make"}},
					{Value: &callpbv1.Literal_String_{String_: "build"}},
				},
			}}},
		}},
		Digest: "sha256:late",
	})
	updateRowWaits(db, at(100))
	if segs := waitingSegments(row.TimeBreakdown(at(100))); len(segs) != 1 || segs[0].Label != "make build" {
		t.Fatalf("once its call arrives, the step reads by its command: %+v", segs)
	}
}

// liveWaitTrace is TestRowWaitsLive's trace: a function call whose step is
// forcing another module's step, still running.
var liveWaitTrace = []rowSpan{
	{id: 1, name: "query", start: 0, end: -1},
	{id: 2, parent: 1, name: "apt-get install", start: 10, end: -1},
	{id: 3, parent: 1, name: "Project.build", start: 5, end: -1},
	{id: 4, parent: 3, name: "Container.from", start: 5, end: 10},
	{id: 5, parent: 3, name: "Container.exitCode", start: 10, end: -1},
	{id: 8, parent: 5, name: "resume withExec", start: 10, end: -1,
		links: []SpanLink{evalLink(testID(2))}},
}

// sliceUpdates brings the waiting below rows up to date a span at a time,
// the clock moving on by step between calls, as the live terminal does, and
// returns the clock after the last.
func sliceUpdates(t *testing.T, db *DB, now time.Time, step time.Duration, between func(int)) time.Time {
	t.Helper()
	for i, more := 0, true; more; i++ {
		if i > 50 {
			t.Fatal("sliced updates should finish")
		}
		_, more = db.UpdateRowWaits(now, time.Nanosecond)
		now = now.Add(step)
		if between != nil {
			between(i)
		}
	}
	return now
}

// A row whose step is still waiting stays waiting now, though its step's
// summary and its own were computed by calls given different times.
func TestRowWaitsInSlicesKeepAWaitGoing(t *testing.T) {
	db := rowWaitsDB(liveWaitTrace...)
	now := sliceUpdates(t, db, at(30), 20*time.Millisecond, nil)
	row := db.Spans.Map[testID(3)]
	later := now.Add(100 * time.Millisecond)
	if hb := row.TimeBreakdown(later); hb.Self != 5*time.Second {
		t.Fatalf("the row's own work stays the 5s before it waited: %+v", hb)
	}
	if blocker, ok := row.WaitingNow(later); !ok || blocker.ID != testID(2) {
		t.Fatalf("the row should still be waiting on the other step: %v %v", blocker, ok)
	}
}

// A wait that ends while an update is under way ends where it ended.
func TestRowWaitsInSlicesEndAWaitThatEnded(t *testing.T) {
	db := rowWaitsDB(liveWaitTrace...)
	now := sliceUpdates(t, db, at(30), 20*time.Millisecond, func(i int) {
		if i == 0 {
			// the other step, and the evaluation forcing it, finish
			importRowSpans(db,
				rowSpan{id: 2, parent: 1, name: "apt-get install", start: 10, end: 30.01},
				rowSpan{id: 8, parent: 5, name: "resume withExec", start: 10, end: 30.01,
					links: []SpanLink{evalLink(testID(2))}})
		}
	})
	sliceUpdates(t, db, now, 20*time.Millisecond, nil)
	segs := waitingSegments(db.Spans.Map[testID(3)].TimeBreakdown(at(40)))
	if len(segs) != 1 || !segs[0].End.Equal(at(30.01)) {
		t.Fatalf("the row's wait should end where the other step did: %+v", segs)
	}
}
