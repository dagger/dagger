package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

// These tests lock in the self-time rendering: material rows report self
// time as THE duration, a row blocked right now says what it is waiting
// on, and completed rows carry no waiting decoration.

func waitLink(target dagui.SpanID, from, to time.Time) dagui.SpanLink {
	return dagui.SpanLink{
		SpanContext: dagui.SpanContext{TraceID: prettyTestTraceID(), SpanID: target},
		Purpose:     "wait",
		WaitReason:  "lazy",
		WaitStart:   from,
		WaitEnd:     to,
	}
}

func causeLink(target dagui.SpanID) dagui.SpanLink {
	return dagui.SpanLink{
		SpanContext: dagui.SpanContext{TraceID: prettyTestTraceID(), SpanID: target},
		Purpose:     "cause",
	}
}

// evalLink is a lazy evaluation's link to the call it evaluates, which the
// engine emits with no purpose.
func evalLink(target dagui.SpanID) dagui.SpanLink {
	return dagui.SpanLink{
		SpanContext: dagui.SpanContext{TraceID: prettyTestTraceID(), SpanID: target},
	}
}

func renderedLines(db *dagui.DB) string {
	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()
	return strings.Join(fe.tui.RenderLines(), "\n")
}

// TestRenderSelfTimeCompleted: the forcing op's row reports ~0 own work
// instead of the 13s it spent blocked, with no waiting decoration once done;
// the row that hosted the real work reports its true execution time.
func TestRenderSelfTimeCompleted(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	root := prettyTestSpanID(1)
	forced := prettyTestSpanID(2)
	stdout := prettyTestSpanID(3)
	twin := prettyTestSpanID(4)
	resume := prettyTestSpanID(5)

	// Anchor in the past so nothing renders as running.
	base := time.Now().Add(-time.Hour)
	at := func(sec float64) time.Time {
		return base.Add(time.Duration(sec * float64(time.Second)))
	}

	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: root, TraceID: prettyTestTraceID(), Name: "root",
			StartTime: at(0), EndTime: at(16), Final: true},
		{ID: forced, TraceID: prettyTestTraceID(), ParentID: root, Name: "the-forced-op",
			StartTime: at(2), EndTime: at(2.5), Final: true},
		{ID: stdout, TraceID: prettyTestTraceID(), ParentID: root, Name: "the-forcing-op",
			StartTime: at(3), EndTime: at(16), Final: true},
		{ID: twin, TraceID: prettyTestTraceID(), ParentID: stdout, Name: "the-forcing-op",
			StartTime: at(3), EndTime: at(16), Final: true,
			Links: []dagui.SpanLink{waitLink(resume, at(3), at(16))}},
		{ID: resume, TraceID: prettyTestTraceID(), ParentID: twin, Name: "resume withExec",
			StartTime: at(3), EndTime: at(16), Final: true,
			Links: []dagui.SpanLink{evalLink(forced)}},
	})
	db.SetPrimarySpan(root)

	got := renderedLines(db)

	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "the-forcing-op") {
			continue
		}
		if strings.Contains(line, "13s") {
			t.Errorf("forcing op must not report blocked time as its own:\n%s", line)
		}
		if !strings.Contains(line, "0.0s") {
			t.Errorf("forcing op should report ~0 own work:\n%s", line)
		}
		if strings.Contains(line, "waiting on") {
			t.Errorf("completed rows must carry no waiting decoration:\n%s", line)
		}
	}
	// The forced op hosted the deferred eval: sync 0.5s + resumed 13s.
	if !strings.Contains(got, "the-forced-op 13.5s") {
		t.Errorf("forced op should report its true execution time:\n%s", got)
	}
}

// TestRenderLiveWaitingSuffix: a row blocked right now names its live blocker
// and its own-work number stays at the sync part instead of ticking up.
func TestRenderLiveWaitingSuffix(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	root := prettyTestSpanID(1)
	forced := prettyTestSpanID(2)
	stdout := prettyTestSpanID(3)
	twin := prettyTestSpanID(4)
	resume := prettyTestSpanID(5)

	// The scenario is mid-run: spans started seconds ago and are still
	// running (no EndTime); the resume marker is the only live signal.
	now := time.Now()
	at := func(secAgo float64) time.Time {
		return now.Add(-time.Duration(secAgo * float64(time.Second)))
	}

	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: root, TraceID: prettyTestTraceID(), Name: "root", StartTime: at(10)},
		{ID: forced, TraceID: prettyTestTraceID(), ParentID: root, Name: "the-forced-op",
			StartTime: at(9), EndTime: at(8.5), Final: true},
		{ID: stdout, TraceID: prettyTestTraceID(), ParentID: root, Name: "the-forcing-op",
			StartTime: at(8)},
		{ID: twin, TraceID: prettyTestTraceID(), ParentID: stdout, Name: "the-forcing-op",
			StartTime: at(8)},
		{ID: resume, TraceID: prettyTestTraceID(), ParentID: twin, Name: "resume withExec",
			StartTime: at(8),
			Links:     []dagui.SpanLink{evalLink(forced)}},
	})
	db.SetPrimarySpan(root)

	got := renderedLines(db)

	var forcingLine string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "the-forcing-op") && !strings.Contains(line, "resume") {
			forcingLine = line
			break
		}
	}
	if forcingLine == "" {
		t.Fatalf("no forcing-op row rendered:\n%s", got)
	}
	if !strings.Contains(forcingLine, "waiting on the-forced-op") {
		t.Errorf("blocked row should name its live blocker:\n%s", forcingLine)
	}
	if !strings.Contains(forcingLine, "0.0s") {
		t.Errorf("blocked row's own work should stay ~0 while blocked:\n%s", forcingLine)
	}
	if strings.Contains(forcingLine, "8s") || strings.Contains(forcingLine, "8.") {
		t.Errorf("blocked row must not tick up wall-clock while blocked:\n%s", forcingLine)
	}
}

// A frontend over a trace loaded in parts asks its support provider for what
// its rows waited on that it hasn't loaded, once, and the row then reads as
// the waiting it was.
func TestRequestBreakdownSpans(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	root := prettyTestSpanID(1)
	row := prettyTestSpanID(2)
	other := prettyTestSpanID(3)
	base := time.Now().Add(-time.Hour)
	at := func(sec float64) time.Time {
		return base.Add(time.Duration(sec * float64(time.Second)))
	}
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: root, TraceID: prettyTestTraceID(), Name: "root", StartTime: at(0), EndTime: at(20)},
		{ID: row, TraceID: prettyTestTraceID(), ParentID: root, Name: "the-waiting-op",
			StartTime: at(1), EndTime: at(11),
			Links: []dagui.SpanLink{waitLink(other, at(1), at(11))}},
	})
	db.SetPrimarySpan(root)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	var requested [][]dagui.SpanID
	fe.supportProvider = func(ids []dagui.SpanID) { requested = append(requested, ids) }

	fe.recalculateViewLocked()
	if len(requested) != 1 || len(requested[0]) != 1 || requested[0][0] != other {
		t.Fatalf("the row's unloaded blocker should be requested once: %v", requested)
	}
	if got := strings.Join(fe.tui.RenderLines(), "\n"); !strings.Contains(got, "the-waiting-op 10.0s") {
		t.Errorf("until its blocker loads, the row reads as working the whole time:\n%s", got)
	}
	fe.recalculateViewLocked()
	if len(requested) != 1 {
		t.Fatalf("a span is requested once: %v", requested)
	}

	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: other, TraceID: prettyTestTraceID(), ParentID: root, Name: "the-other-op",
			StartTime: at(1), EndTime: at(11)},
	})
	fe.recalculateViewLocked()
	if got := strings.Join(fe.tui.RenderLines(), "\n"); !strings.Contains(got, "the-waiting-op 0.0s") {
		t.Errorf("once its blocker loads, the row reads as waiting:\n%s", got)
	}
}

// Report mode renders once, so 'dagger trace' asks it through the public
// method, round by round, for the spans its rows' breakdowns need.
func TestRequestBreakdownSpansInReport(t *testing.T) {
	tf, ok := NewReporter(io.Discard).(TraceFrontend)
	if !ok {
		t.Fatal("the reporter should be a trace frontend")
	}
	if tf.RequestBreakdownSpans() {
		t.Error("an empty view should request nothing")
	}
}
