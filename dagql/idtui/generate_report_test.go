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

// TestGenerateReportKeepsMultiLineLoadError covers a skipped module whose load
// error carries its detail on further lines -- the engine inlines the SDK
// runtime's compiler output under the message (see describeLoadFailure) so the
// user learns WHY the module was skipped, not just that it was. Every line must
// survive into the persisted section, aligned under the "! " marker.
func TestGenerateReportKeepsMultiLineLoadError(t *testing.T) {
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
			Name:            "broken-build",
			StartTime:       start.Add(time.Second),
			EndTime:         start.Add(2 * time.Second),
			ParentID:        rootID,
			GenerateSkipped: true,
			Status: sdktrace.Status{
				Code: codes.Error,
				Description: "loading module \"modules/broken-build\": call constructor: exit code: 1\n" +
					"# dagger/broken-build\n" +
					"./main.go:8:9: undefined: intentionallyUndefinedSymbol",
			},
			Final: true,
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
	for _, want := range []string{
		"  ! loading module \"modules/broken-build\": call constructor: exit code: 1\n",
		"    # dagger/broken-build\n",
		"    ./main.go:8:9: undefined: intentionallyUndefinedSymbol\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("final render missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateReportShowsRegeneratedModules covers the outcome a run can only
// settle after generating: the engine re-loads each skipped module whose
// directory the changes touched, with the changes applied, under a
// "regenerated" span. That span's status supersedes the pre-generation load
// error in the report -- a module that now loads gets a REGENERATED row and its
// old compiler output is NOT repeated; one that still fails shows the
// post-generation error instead of the stale one. An untouched skipped module
// keeps its original error.
func TestGenerateReportShowsRegeneratedModules(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	staleID := prettyTestSpanID(2)
	brokenID := prettyTestSpanID(3)
	regenID := prettyTestSpanID(4)
	stillID := prettyTestSpanID(5)
	stillRegenID := prettyTestSpanID(6)
	start := time.Unix(100, 0)
	skipStatus := func(name string) sdktrace.Status {
		return sdktrace.Status{
			Code:        codes.Error,
			Description: "loading module \"modules/" + name + "\": call constructor: exit code: 1\n./main.go:8:9: undefined: " + name,
		}
	}
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "generate",
			StartTime: start,
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
		{
			ID:              staleID,
			TraceID:         prettyTestTraceID(),
			Name:            "stale",
			StartTime:       start.Add(time.Second),
			EndTime:         start.Add(time.Second),
			ParentID:        rootID,
			GenerateSkipped: true,
			Status:          skipStatus("stale"),
			Final:           true,
		},
		{
			ID:              brokenID,
			TraceID:         prettyTestTraceID(),
			Name:            "broken",
			StartTime:       start.Add(time.Second),
			EndTime:         start.Add(time.Second),
			ParentID:        rootID,
			GenerateSkipped: true,
			Status:          skipStatus("broken"),
			Final:           true,
		},
		{
			ID:                  regenID,
			TraceID:             prettyTestTraceID(),
			Name:                "stale",
			StartTime:           start.Add(2 * time.Second),
			EndTime:             start.Add(3 * time.Second),
			ParentID:            rootID,
			GenerateRegenerated: true,
			Final:               true,
		},
		{
			ID:              stillID,
			TraceID:         prettyTestTraceID(),
			Name:            "still",
			StartTime:       start.Add(time.Second),
			EndTime:         start.Add(time.Second),
			ParentID:        rootID,
			GenerateSkipped: true,
			Status:          skipStatus("still"),
			Final:           true,
		},
		{
			ID:                  stillRegenID,
			TraceID:             prettyTestTraceID(),
			Name:                "still",
			StartTime:           start.Add(2 * time.Second),
			EndTime:             start.Add(3 * time.Second),
			ParentID:            rootID,
			GenerateRegenerated: true,
			Status: sdktrace.Status{
				Code:        codes.Error,
				Description: "still fails to load with this run's changes: call constructor: exit code: 1\n./main.go:9:9: undefined: stillAfter",
			},
			Final: true,
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
	for _, want := range []string{
		"✔ stale 1.0s REGENERATED\n",
		"  could not load before this run's changes; loads with them applied\n",
		"✘ broken 0.0s ERROR\n",
		"    ./main.go:8:9: undefined: broken\n",
		"✘ still 1.0s ERROR\n",
		"  ! still fails to load with this run's changes: call constructor: exit code: 1\n",
		"    ./main.go:9:9: undefined: stillAfter\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("final render missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"✘ stale",
		"undefined: stale",
		// the pre-generation error is superseded by the post-generation one
		"undefined: still\n",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("final render repeats a superseded load error (%q):\n%s", unwanted, got)
		}
	}
}
