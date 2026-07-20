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

// TestGenerateReportPersistsSkippedModules covers the whole point of the
// dedicated section: a `dagger generate` that SUCCEEDS (root span exits 0) at
// DEFAULT verbosity still surfaces the modules it skipped. Without the
// HasGenerateReport gate the final render would collapse the live reveal row on
// success and the user would never learn what was left out.
func TestGenerateReportPersistsSkippedModules(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	skipID := prettyTestSpanID(2)
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
			ID:              skipID,
			TraceID:         prettyTestTraceID(),
			Name:            "bad",
			StartTime:       start.Add(time.Second),
			EndTime:         start.Add(2 * time.Second),
			ParentID:        rootID,
			GenerateSkipped: true,
			Status:          sdktrace.Status{Code: codes.Error, Description: `loading module "modules/bad": no match found`},
			Final:           true,
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
	if !strings.Contains(got, "SKIPPED MODULES") {
		t.Fatalf("final render missing SKIPPED MODULES section at default verbosity:\n%s", got)
	}
	if !strings.Contains(got, "bad") || !strings.Contains(got, "modules/bad") {
		t.Fatalf("final render missing skipped module name/error:\n%s", got)
	}
}
