package idtui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vito/tuist"
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

// TestGeneratorsReportShowsChanges covers generators that produced changes:
// each reads CHANGED, with the files it changed beneath it, and the tally
// counts it as changed rather than passed. One with nothing to change stays
// OK, and a failed one stays an error whatever it recorded.
func TestGeneratorsReportShowsChanges(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	rootID := prettyTestSpanID(10)
	start := time.Unix(100, 0)
	gen := func(id byte, name string, changes *dagui.GeneratorChanges, failed bool) dagui.SpanSnapshot {
		span := dagui.SpanSnapshot{
			ID: prettyTestSpanID(id), TraceID: prettyTestTraceID(), Name: name, ParentID: rootID,
			StartTime: start, EndTime: start.Add(time.Second), Final: true,
			GeneratorName: name, GeneratorChanges: changes,
		}
		if failed {
			span.Status = sdktrace.Status{Code: codes.Error, Description: "boom"}
		}
		return span
	}
	many := &dagui.GeneratorChanges{Files: 60, Added: 60}
	for i := range dagui.MaxGeneratorChangeEntries {
		many.Entries = append(many.Entries, dagui.GeneratorChange{Path: fmt.Sprintf("gen/%02d.go", i), Kind: "ADDED", Added: 1})
	}
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "generate", StartTime: start, EndTime: start.Add(2 * time.Second), Final: true},
		gen(1, "dag://?name=docs", &dagui.GeneratorChanges{Files: 2, Added: 3, Removed: 1, Entries: []dagui.GeneratorChange{
			{Path: "docs/new.md", Kind: "ADDED", Added: 2},
			{Path: "docs/old.md", Kind: "MODIFIED", Added: 1, Removed: 1},
		}}, false),
		gen(2, "dag://?name=sdk", many, false),
		gen(3, "dag://?name=tidy", nil, false),
	})
	db.SetPrimarySpan(rootID)
	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	report := strings.Join(fe.generatorsReport(tuist.Context{Width: 120}, r, false), "\n")
	for _, want := range []string{
		"GENERATORS  ✔ 2 changed  ✔ 1 passed",
		"✔ docs 1.0s CHANGED\n  docs/new.md +2\n  docs/old.md +1 -1\n",
		"✔ sdk 1.0s CHANGED\n  gen/00.go +1\n",
		"  … and 10 more files\n",
		"✔ tidy 1.0s OK",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("missing %q in:\n%s", want, report)
		}
	}

	// A failed generator is an error, not a change.
	db = dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "generate", StartTime: start, EndTime: start.Add(2 * time.Second), Final: true},
		gen(1, "dag://?name=docs", &dagui.GeneratorChanges{Files: 1, Entries: []dagui.GeneratorChange{{Path: "docs/new.md", Kind: "ADDED"}}}, true),
	})
	db.SetPrimarySpan(rootID)
	fe = NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()
	r = newRenderer(fe.db, 0, fe.FrontendOpts, true)
	report = strings.Join(fe.generatorsReport(tuist.Context{Width: 120}, r, false), "\n")
	if !strings.Contains(report, "✘ docs 1.0s ERROR") || strings.Contains(report, "CHANGED") || strings.Contains(report, "docs/new.md") {
		t.Errorf("a failed generator must report its failure only:\n%s", report)
	}
}
