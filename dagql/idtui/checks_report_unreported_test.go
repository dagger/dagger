package idtui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/dagql/dagui"
)

// A check whose span never reported its outcome before the trace ended has no
// final state. The CHECKS section must not count or show it as passed.
func TestChecksReportUnreportedCheck(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	failedID := prettyTestSpanID(2)
	unreportedID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "check",
			StartTime: start,
			EndTime:   start.Add(20 * time.Second),
			Final:     true,
		},
		{
			ID:        failedID,
			TraceID:   prettyTestTraceID(),
			Name:      "dag://?check=failed",
			CheckName: "dag://?check=failed",
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(18 * time.Second),
			ParentID:  rootID,
			Status:    sdktrace.Status{Code: codes.Error, Description: "test failed"},
			Final:     true,
		},
		{
			// Started, but its end never arrived: the root's end leaves it running.
			ID:        unreportedID,
			TraceID:   prettyTestTraceID(),
			Name:      "dag://?check=unreported",
			CheckName: "dag://?check=unreported",
			StartTime: start.Add(time.Second),
			ParentID:  rootID,
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
	for _, want := range []string{"1 failed", "1 not reported", "dag://?check=unreported", "NOT REPORTED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("final render missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "passed") || strings.Contains(got, " OK") {
		t.Fatalf("an unreported check must not show as passed:\n%s", got)
	}
}

// A check's final state can arrive after the trace's end marked it canceled.
// Its real outcome then replaces the assumed cancellation.
func TestChecksReportLateFinalState(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID:        rootID,
		TraceID:   prettyTestTraceID(),
		Name:      "check",
		StartTime: start,
		EndTime:   start.Add(20 * time.Second),
		Final:     true,
	}})
	running := checkSpanStub(2, "dag://?check=late", start.Add(time.Second), time.Time{}, codes.Unset)
	running.Parent = running.SpanContext.WithSpanID(rootID.SpanID)
	if err := db.ExportSpans(context.Background(), tracetest.SpanStubs{running}.Snapshots()); err != nil {
		t.Fatal(err)
	}
	final := checkSpanStub(2, "dag://?check=late", start.Add(time.Second), start.Add(21*time.Second), codes.Ok)
	final.Parent = running.Parent
	if err := db.ExportSpans(context.Background(), tracetest.SpanStubs{final}.Snapshots()); err != nil {
		t.Fatal(err)
	}
	db.SetPrimarySpan(rootID)
	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	var buf bytes.Buffer
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "1 passed") || strings.Contains(got, "not reported") {
		t.Fatalf("a check whose final state arrived late must show its outcome:\n%s", got)
	}
}
