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
