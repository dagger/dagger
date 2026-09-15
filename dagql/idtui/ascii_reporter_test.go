package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestASCIIReporterRendersPlainText covers the report-only ASCII frontend used
// to embed a rendered final report in an API result (e.g. an LLM tool result):
// it must render the CHECKS section without any ANSI escape sequences, no
// matter what color profile the ambient environment would select.
func TestASCIIReporterRendersPlainText(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	checkID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "check",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:        checkID,
			TraceID:   prettyTestTraceID(),
			Name:      "viztest:lint",
			CheckName: "viztest:lint",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewASCIIReporterWithDB(io.Discard, db)
	fe.Verbosity = dagui.ShowCompletedVerbosity

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "CHECKS") {
		t.Fatalf("final render missing CHECKS section:\n%s", got)
	}
	if !strings.Contains(got, "viztest:lint") {
		t.Fatalf("final render missing check row:\n%s", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("final render contains ANSI escape sequences:\n%q", got)
	}
}

// TestASCIIReporterRerenderIsStable covers reusing one reporter (and its DB)
// across renders, which is what core's per-session trace report cache does to
// avoid rebuilding the whole DB on every render. The reporter is stateful, so
// pin down that a second render of the same scope is byte-identical, and that
// re-scoping between renders takes effect.
func TestASCIIReporterRerenderIsStable(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	checkID := prettyTestSpanID(2)
	otherID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "check",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:        checkID,
			TraceID:   prettyTestTraceID(),
			Name:      "viztest:lint",
			CheckName: "viztest:lint",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Final:     true,
		},
		{
			ID:        otherID,
			TraceID:   prettyTestTraceID(),
			Name:      "some other work",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			ParentID:  rootID,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewASCIIReporterWithDB(io.Discard, db)
	fe.Verbosity = dagui.ShowCompletedVerbosity
	fe.ExpandCompleted = true

	render := func() string {
		t.Helper()
		var buf bytes.Buffer
		if err := fe.FinalRender(&buf); err != nil {
			t.Fatalf("FinalRender: %v", err)
		}
		return buf.String()
	}

	// renderFresh is the baseline the cache has to match: what a
	// never-rendered reporter over the same DB produces right now.
	renderFresh := func() string {
		t.Helper()
		one := NewASCIIReporterWithDB(io.Discard, db)
		one.Verbosity = dagui.ShowCompletedVerbosity
		one.ExpandCompleted = true
		var buf bytes.Buffer
		if err := one.FinalRender(&buf); err != nil {
			t.Fatalf("FinalRender: %v", err)
		}
		return buf.String()
	}

	first := render()
	second := render()
	if first != second {
		t.Fatalf("re-render of the same scope differs:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if fresh := renderFresh(); first != fresh {
		t.Fatalf("reused reporter differs from a fresh one:\nreused:\n%s\nfresh:\n%s", first, fresh)
	}

	// Re-scoping between renders must behave exactly like a fresh reporter
	// over the re-scoped DB -- that's what the per-session cache relies on,
	// since consecutive reports scope to different spans.
	db.SetPrimarySpan(otherID)
	scoped := render()
	if fresh := renderFresh(); scoped != fresh {
		t.Fatalf("re-scoped reuse differs from a fresh reporter:\nreused:\n%s\nfresh:\n%s", scoped, fresh)
	}
	if scoped != render() {
		t.Fatal("re-render of the re-scoped report differs")
	}

	// ...and scoping back must reproduce the original render exactly.
	db.SetPrimarySpan(rootID)
	if back := render(); back != first {
		t.Fatalf("restoring the scope did not reproduce the first render:\nfirst:\n%s\nback:\n%s", first, back)
	}
}
