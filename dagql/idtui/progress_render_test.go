package idtui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestRenderProgressBarFills locks in the per-cell braille fill math. The
// golden tests can't cover it: scrub.Stabilize collapses every braille run
// to one canonical sequence because roll-up dots are nondeterministic.
func TestRenderProgressBarFills(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	transferID := prettyTestSpanID(2)
	overflowID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        transferID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "transfer",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        overflowID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "overflow",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	transfer := db.Spans.Map[transferID]
	transfer.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "complete", Current: 10_000_000, Total: 10_000_000, Unit: "bytes"},
		{Name: "almost", Current: 9_000_000, Total: 10_000_000, Unit: "bytes"},
		{Name: "half", Current: 5_000_000, Total: 10_000_000, Unit: "bytes"},
		{Name: "started", Current: 1_000_000, Total: 10_000_000, Unit: "bytes"},
		{Name: "untouched", Current: 0, Total: 10_000_000, Unit: "bytes"},
		{Name: "indeterminate", Current: 5_000_000, Total: 0, Unit: "bytes"},
	}}

	overflowItems := make([]*dagui.ProgressItem, 50)
	for i := range overflowItems {
		overflowItems[i] = &dagui.ProgressItem{
			Name:    fmt.Sprintf("item-%d", i),
			Current: 1024,
			Total:   1024,
			Unit:    "bytes",
		}
	}
	db.Spans.Map[overflowID].Progress = &dagui.SpanProgress{Order: overflowItems}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	got := strings.Join(fe.tui.RenderLines(), "\n")

	// fill level per cell = ceil(current*8/total), clamped to [1,8]; an
	// unknown total renders the lowest level
	if want := "██▄▁▁▁ 30 MB/50 MB"; !strings.Contains(got, want) {
		t.Errorf("render missing partial fills %q:\n%s", want, got)
	}
	// items past the cap fold into +N; all-complete items omit the total
	if want := strings.Repeat("█", 40) + "+10 51 kB"; !strings.Contains(got, want) {
		t.Errorf("render missing overflow bar %q:\n%s", want, got)
	}
}

// TestRenderProgressTrack covers the single-item (1-D) form: a fixed-width
// track filling left-to-right, visually distinct from the per-item cells.
func TestRenderProgressTrack(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	fetchID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        fetchID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "fetching modules.tar.gz",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	db.Spans.Map[fetchID].Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "modules.tar.gz", Current: 9_000_000, Total: 12_000_000, Unit: "bytes"},
	}}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	got := strings.Join(fe.tui.RenderLines(), "\n")
	if want := "fetching modules.tar.gz 1.0s █████████░░░ 9.0 MB/12 MB"; !strings.Contains(got, want) {
		t.Errorf("render missing 1-D track %q:\n%s", want, got)
	}
}

// TestRenderProgressIndeterminate covers a single item with an unknown
// total (e.g. a filesync's streaming diff): no bar, just a climbing count.
func TestRenderProgressIndeterminate(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	syncID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        syncID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "uploading /src",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	db.Spans.Map[syncID].Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "bytes", Current: 400_000_000, Total: 0, Unit: "bytes"},
	}}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	got := strings.Join(fe.tui.RenderLines(), "\n")
	if want := "uploading /src 1.0s 400 MB"; !strings.Contains(got, want) {
		t.Errorf("render missing indeterminate count %q:\n%s", want, got)
	}
	if strings.Contains(got, "█") || strings.Contains(got, "▁") || strings.Contains(got, "░") {
		t.Errorf("indeterminate progress should not render bar glyphs:\n%s", got)
	}
}

// TestRenderProgressObjectUnit covers non-byte units (e.g. a git fetch
// counting objects): the summary shows raw counts with the unit name
// instead of humanized byte sizes.
func TestRenderProgressObjectUnit(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	fetchID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        fetchID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "fetching github.com/dagger/dagger",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	db.Spans.Map[fetchID].Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "objects", Current: 1234, Total: 2900, Unit: "objects"},
	}}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	got := strings.Join(fe.tui.RenderLines(), "\n")
	if want := "fetching github.com/dagger/dagger 1.0s █████░░░░░░░ 1234/2900 objects"; !strings.Contains(got, want) {
		t.Errorf("render missing object-unit track %q:\n%s", want, got)
	}
}

// TestRenderProgressSpanRows covers encapsulated descendants that report
// progress: each renders as its own labeled bar-first row — revealed in the
// tree when its ancestors are expanded, rolled up beneath the nearest
// visible collapsed ancestor otherwise. Bars are never merged into the
// ancestor's own title row.
func TestRenderProgressSpanRows(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	fromID := prettyTestSpanID(2)
	pullingID := prettyTestSpanID(3)
	unpackingID := prettyTestSpanID(4)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        fromID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "Container.from",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:           pullingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "pulling nginx",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
		},
		{
			ID:           unpackingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "unpacking nginx",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
		},
	})
	db.SetPrimarySpan(rootID)

	pulling := db.Spans.Map[pullingID]
	pulling.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "layer-1", Current: 10_000_000, Total: 10_000_000, Unit: "bytes"},
		{Name: "layer-2", Current: 10_000_000, Total: 10_000_000, Unit: "bytes"},
	}}
	unpacking := db.Spans.Map[unpackingID]
	unpacking.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "layer-1", Current: 10_000_000, Total: 10_000_000, Unit: "bytes"},
		{Name: "layer-2", Current: 5_000_000, Total: 10_000_000, Unit: "bytes"},
	}}
	from := db.Spans.Map[fromID]
	from.ProgressSpans.Add(pulling)
	from.ProgressSpans.Add(unpacking)
	root := db.Spans.Map[rootID]
	root.ProgressSpans.Add(pulling)
	root.ProgressSpans.Add(unpacking)

	render := func(expand bool) string {
		fe := NewWithDB(io.Discard, db)
		fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
		fe.FrontendOpts.ExpandCompleted = expand
		fe.FrontendOpts.GCThreshold = time.Hour
		fe.recalculateViewLocked()
		return strings.Join(fe.tui.RenderLines(), "\n")
	}

	// expanded: carrying progress reveals the encapsulated spans, and each
	// renders its labeled bar in its natural tree position
	expanded := render(true)
	for _, want := range []string{
		"pulling nginx 1.0s ██ 20 MB",
		"unpacking nginx 1.0s █▄ 15 MB/20 MB",
	} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded render missing progress row %q:\n%s", want, expanded)
		}
	}
	for _, line := range strings.Split(expanded, "\n") {
		if strings.Contains(line, "Container.from") && strings.Contains(line, "█") {
			t.Errorf("bars should not merge into the parent's title row:\n%s", expanded)
		}
	}

	// collapsed: the progress spans still surface in the final render,
	// rolled up beneath the nearest visible collapsed ancestor (the live
	// roll-up only carries in-flight transfers; see
	// TestRenderProgressSpanRowsAutoHide)
	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	var buf strings.Builder
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	collapsed := buf.String()
	for _, want := range []string{
		"pulling nginx 1.0s ██ 20 MB",
		"unpacking nginx 1.0s █▄ 15 MB/20 MB",
	} {
		if !strings.Contains(collapsed, want) {
			t.Errorf("collapsed final render missing rolled-up progress row %q:\n%s", want, collapsed)
		}
	}
}

// TestRenderProgressSpanRowsAutoHide covers the roll-up's auto-hide: live
// rendering only rolls up transfers that are still in flight, so completed
// ones stop piercing their collapsed ancestors (they'd otherwise accumulate
// without bound on large traces). The final render keeps them as the run's
// transfer summary.
func TestRenderProgressSpanRowsAutoHide(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	fromID := prettyTestSpanID(2)
	pullingID := prettyTestSpanID(3)
	unpackingID := prettyTestSpanID(4)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			// still running so the trace stays live
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
		},
		{
			// completed, collapsed, still shown at default verbosity:
			// hosts the roll-up
			ID:        fromID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "Container.from",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			// finished: hidden from the live roll-up
			ID:           pullingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "pulling nginx",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
		},
		{
			// still transferring: stays in the live roll-up
			ID:           unpackingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "unpacking nginx",
			Encapsulated: true,
			StartTime:    start,
		},
	})
	db.SetPrimarySpan(rootID)

	pulling := db.Spans.Map[pullingID]
	pulling.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "layer-1", Current: 10_000_000, Total: 10_000_000, Unit: "bytes"},
	}}
	unpacking := db.Spans.Map[unpackingID]
	unpacking.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
		{Name: "layer-1", Current: 5_000_000, Total: 10_000_000, Unit: "bytes"},
	}}
	from := db.Spans.Map[fromID]
	from.ProgressSpans.Add(pulling)
	from.ProgressSpans.Add(unpacking)
	root := db.Spans.Map[rootID]
	root.ProgressSpans.Add(pulling)
	root.ProgressSpans.Add(unpacking)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity // the TUI default
	fe.recalculateViewLocked()

	live := strings.Join(fe.tui.RenderLines(), "\n")
	if want := "unpacking nginx"; !strings.Contains(live, want) {
		t.Errorf("live render missing in-flight rolled-up progress row %q:\n%s", want, live)
	}
	if dontWant := "pulling nginx"; strings.Contains(live, dontWant) {
		t.Errorf("live render should hide completed rolled-up progress row %q:\n%s", dontWant, live)
	}

	// the final render keeps completed transfers as the run's summary
	var buf strings.Builder
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	final := buf.String()
	for _, want := range []string{"pulling nginx", "unpacking nginx"} {
		if !strings.Contains(final, want) {
			t.Errorf("final render missing rolled-up progress row %q:\n%s", want, final)
		}
	}
}
