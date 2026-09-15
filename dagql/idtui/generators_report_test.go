package idtui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestGeneratorsReportSurfacesRuns covers the reveal-independent GENERATORS
// section: a `dagger generate` run's generator spans surface in the final
// report -- failed ones with a red row sorted first -- without any span setting
// the deprecated `reveal` attribute. The successful generator persists too (the
// live tree collapses on exit 0), mirroring the checks section.
func TestGeneratorsReportSurfacesRuns(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	okGenID := prettyTestSpanID(2)
	failGenID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "generate",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:            okGenID,
			TraceID:       prettyTestTraceID(),
			Name:          "viztest:gen-docs",
			GeneratorName: "viztest:gen-docs",
			StartTime:     start.Add(time.Second),
			EndTime:       start.Add(2 * time.Second),
			ParentID:      rootID,
			Final:         true,
		},
		{
			ID:            failGenID,
			TraceID:       prettyTestTraceID(),
			Name:          "viztest:gen-fail",
			GeneratorName: "viztest:gen-fail",
			StartTime:     start.Add(time.Second),
			EndTime:       start.Add(2 * time.Second),
			ParentID:      rootID,
			Status:        sdktrace.Status{Code: codes.Error, Description: "exit code: 3"},
			Final:         true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "GENERATORS") {
		t.Fatalf("final render missing GENERATORS section:\n%s", got)
	}
	if !strings.Contains(got, "viztest:gen-fail") || !strings.Contains(got, "viztest:gen-docs") {
		t.Fatalf("final render missing generator rows:\n%s", got)
	}
	if strings.Index(got, "viztest:gen-fail") > strings.Index(got, "viztest:gen-docs") {
		t.Fatalf("failed generator must sort before passing one:\n%s", got)
	}
}

// TestGeneratorsPromoteLive covers the live-tree half: recalculateViewLocked
// promotes a trace with generator spans by wiring them into the host's
// RevealedSpans and marking it Passthrough, so RowsView leads with the
// generator rows instead of the session/load noise -- the structural
// replacement for the generator spans' old `reveal` attribute.
func TestGeneratorsPromoteLive(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	genID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "generate",
			StartTime: start,
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:            genID,
			TraceID:       prettyTestTraceID(),
			Name:          "viztest:gen",
			GeneratorName: "viztest:gen",
			StartTime:     start.Add(time.Second),
			EndTime:       start.Add(2 * time.Second),
			ParentID:      rootID,
			Final:         true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	host := db.Spans.Map[rootID]
	if host == nil {
		t.Fatal("missing root span")
	}
	if !host.Passthrough {
		t.Fatal("host must be marked Passthrough so RowsView surfaces the generators")
	}
	found := false
	for _, revealed := range host.RevealedSpans.Order {
		if revealed.GeneratorName == "viztest:gen" {
			found = true
		}
	}
	if !found {
		t.Fatalf("generator span missing from host.RevealedSpans: %+v", host.RevealedSpans.Order)
	}
}
