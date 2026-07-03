package idtui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

	// collapsed: the completed transfers fold into one merged summary
	// line beneath the nearest visible collapsed ancestor (see
	// TestRenderProgressSpanRowsAutoHide for the fold policy)
	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	var buf strings.Builder
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	collapsed := buf.String()
	if want := "1 pull, 1 unpack 1.0s"; !strings.Contains(collapsed, want) {
		t.Errorf("collapsed final render missing merged transfer line %q:\n%s", want, collapsed)
	}
}

// TestTransferSummary covers the merged line's kind counting: the engine's
// progress emitters all name their spans "<verb> <subject>", and unknown
// verbs fall back to "transfers".
func TestTransferSummary(t *testing.T) {
	mkSpan := func(name string) *dagui.Span {
		return &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{Name: name}}
	}
	for _, tc := range []struct {
		name  string
		spans []*dagui.Span
		want  string
	}{
		{
			name: "counts by kind in order of first appearance",
			spans: []*dagui.Span{
				mkSpan("fetching https://example.com/pkg-1.apk"),
				mkSpan("pulling alpine:latest"),
				mkSpan("fetching https://example.com/pkg-2.apk"),
				mkSpan("pushing ttl.sh/test:10m"),
			},
			want: "2 fetches, 1 pull, 1 push",
		},
		{
			name: "unknown verbs count as transfers",
			spans: []*dagui.Span{
				mkSpan("transfer-done"),
				mkSpan("syncing /things"),
			},
			want: "2 transfers",
		},
		{
			name: "singular and plural nouns",
			spans: []*dagui.Span{
				mkSpan("pushing ttl.sh/one:10m"),
				mkSpan("pushing ttl.sh/two:10m"),
				mkSpan("downloading /out"),
				mkSpan("unpacking nginx:latest"),
				mkSpan("uploading /src"),
			},
			want: "2 pushes, 1 download, 1 unpack, 1 upload",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := transferSummary(tc.spans); got != tc.want {
				t.Errorf("transferSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderProgressSpanRowsAutoHide covers the roll-up's fold policy:
// in-flight transfers each get their own row immediately, completed ones
// always fold into one merged summary line (they'd otherwise accumulate
// without bound on large traces), and the "p" keybind expands the fold
// back into individual rows.
// TestRenderProgressSpanRowsAutoHideKeepsFailuresSeparate guards the collapsed
// roll-up: failed and canceled transfers must not disappear into the green
// completed-transfer summary.
func TestRenderProgressSpanRowsAutoHideKeepsFailuresSeparate(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	fromID := prettyTestSpanID(2)
	pullingID := prettyTestSpanID(3)
	fetchingID := prettyTestSpanID(4)
	uploadingID := prettyTestSpanID(5)
	downloadingID := prettyTestSpanID(6)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			// still running so the completed child stays collapsed in the live TUI
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
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
			ID:           fetchingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "fetching https://example.com/pkg.apk",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
		},
		{
			ID:           uploadingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "uploading cache",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
			Status:       sdktrace.Status{Code: codes.Error},
		},
		{
			ID:           downloadingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "downloading canceled",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
			Canceled:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	for _, id := range []dagui.SpanID{pullingID, fetchingID, uploadingID, downloadingID} {
		src := db.Spans.Map[id]
		src.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
			{Name: "layer-1", Current: 5_000_000, Total: 10_000_000, Unit: "bytes"},
		}}
		db.Spans.Map[fromID].ProgressSpans.Add(src)
		db.Spans.Map[rootID].ProgressSpans.Add(src)
	}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.recalculateViewLocked()

	live := strings.Join(fe.tui.RenderLines(), "\n")
	if want := "1 pull, 1 fetch 1.0s"; !strings.Contains(live, want) {
		t.Errorf("live render missing success-only merged transfer line %q:\n%s", want, live)
	}
	for _, want := range []string{"uploading cache", "downloading canceled"} {
		if !strings.Contains(live, want) {
			t.Errorf("live render should keep failed/canceled progress row %q separate:\n%s", want, live)
		}
	}
	for _, dontWant := range []string{
		"1 pull, 1 fetch, 1 upload",
		"1 pull, 1 fetch, 1 download",
	} {
		if strings.Contains(live, dontWant) {
			t.Errorf("live render should not merge failed/canceled transfer into summary %q:\n%s", dontWant, live)
		}
	}
}

func TestRenderProgressSpanRowsAutoHide(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	fromID := prettyTestSpanID(2)
	pullingID := prettyTestSpanID(3)
	fetchingID := prettyTestSpanID(4)
	unpackingID := prettyTestSpanID(5)
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
			// finished: folds into the merged line
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
			// finished: folds into the merged line
			ID:           fetchingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "fetching https://example.com/pkg.apk",
			Encapsulated: true,
			StartTime:    start,
			EndTime:      end,
			Final:        true,
		},
		{
			// just started transferring: own row immediately
			ID:           unpackingID,
			TraceID:      prettyTestTraceID(),
			ParentID:     fromID,
			Name:         "unpacking nginx",
			Encapsulated: true,
			StartTime:    time.Now(),
		},
	})
	db.SetPrimarySpan(rootID)

	for _, id := range []dagui.SpanID{pullingID, fetchingID, unpackingID} {
		src := db.Spans.Map[id]
		src.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
			{Name: "layer-1", Current: 5_000_000, Total: 10_000_000, Unit: "bytes"},
		}}
		db.Spans.Map[fromID].ProgressSpans.Add(src)
		db.Spans.Map[rootID].ProgressSpans.Add(src)
	}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity // the TUI default
	fe.recalculateViewLocked()

	live := strings.Join(fe.tui.RenderLines(), "\n")
	if want := "unpacking nginx"; !strings.Contains(live, want) {
		t.Errorf("live render missing in-flight rolled-up progress row %q:\n%s", want, live)
	}
	if want := "1 pull, 1 fetch 1.0s"; !strings.Contains(live, want) {
		t.Errorf("live render missing merged transfer line %q:\n%s", want, live)
	}
	if dontWant := "pulling nginx"; strings.Contains(live, dontWant) {
		t.Errorf("live render should fold completed progress row %q:\n%s", dontWant, live)
	}

	// the "p" toggle expands the fold into individual rows
	fe.progressExpanded = map[dagui.SpanID]bool{fromID: true}
	fe.renderVersion++
	fe.recalculateViewLocked()
	expanded := strings.Join(fe.tui.RenderLines(), "\n")
	for _, want := range []string{"pulling nginx", "fetching https://example.com/pkg.apk"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded fold missing individual progress row %q:\n%s", want, expanded)
		}
	}
	if dontWant := "1 pull, 1 fetch"; strings.Contains(expanded, dontWant) {
		t.Errorf("expanded fold should not render the merged line %q:\n%s", dontWant, expanded)
	}
}

// TestRenderProgressMergedRollup covers the merged line itself: completed
// transfers fold into one summary line counting them by kind with the
// wall-clock union of their activity, so a module fetching dozens of
// packages doesn't drown the view.
func TestRenderProgressMergedRollup(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	installID := prettyTestSpanID(2)
	start := time.Unix(100, 0)
	snapshots := []dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root",
			StartTime: start,
			EndTime:   start.Add(10 * time.Second),
			Final:     true,
		},
		{
			ID:        installID,
			TraceID:   prettyTestTraceID(),
			ParentID:  rootID,
			Name:      "Container.withExec",
			StartTime: start,
			EndTime:   start.Add(10 * time.Second),
			Final:     true,
		},
	}
	// one slow pull and a storm of quick, overlapping fetches plus a
	// quick download; the quick ones span 0.0s-0.9s wall-clock combined
	quicks := []struct {
		name       string
		start, end time.Duration
	}{
		{"fetching https://example.com/pkg-1.apk", 0, 300 * time.Millisecond},
		{"fetching https://example.com/pkg-2.apk", 200 * time.Millisecond, 500 * time.Millisecond},
		{"fetching https://example.com/pkg-3.apk", 400 * time.Millisecond, 700 * time.Millisecond},
		{"downloading /out", 600 * time.Millisecond, 900 * time.Millisecond},
	}
	slowID := prettyTestSpanID(3)
	snapshots = append(snapshots, dagui.SpanSnapshot{
		ID:           slowID,
		TraceID:      prettyTestTraceID(),
		ParentID:     installID,
		Name:         "pulling alpine",
		Encapsulated: true,
		StartTime:    start,
		EndTime:      start.Add(5 * time.Second),
		Final:        true,
	})
	quickIDs := make([]dagui.SpanID, len(quicks))
	for i, q := range quicks {
		quickIDs[i] = prettyTestSpanID(byte(4 + i))
		snapshots = append(snapshots, dagui.SpanSnapshot{
			ID:           quickIDs[i],
			TraceID:      prettyTestTraceID(),
			ParentID:     installID,
			Name:         q.name,
			Encapsulated: true,
			StartTime:    start.Add(q.start),
			EndTime:      start.Add(q.end),
			Final:        true,
		})
	}
	db.ImportSnapshots(snapshots)
	db.SetPrimarySpan(rootID)

	install := db.Spans.Map[installID]
	root := db.Spans.Map[rootID]
	for _, id := range append([]dagui.SpanID{slowID}, quickIDs...) {
		src := db.Spans.Map[id]
		src.Progress = &dagui.SpanProgress{Order: []*dagui.ProgressItem{
			{Name: "blob", Current: 1024, Total: 1024, Unit: "bytes"},
		}}
		install.ProgressSpans.Add(src)
		root.ProgressSpans.Add(src)
	}

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	var buf strings.Builder
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	final := buf.String()
	// merged line: counts by kind, wall-clock union of the 0.0s-5.0s pull
	// and the overlapping quick transfers
	if want := "1 pull, 3 fetches, 1 download 5.0s"; !strings.Contains(final, want) {
		t.Errorf("final render missing merged transfer line %q:\n%s", want, final)
	}
	for _, dontWant := range []string{"pulling alpine", "pkg-1.apk"} {
		if strings.Contains(final, dontWant) {
			t.Errorf("completed transfer %q should fold into the merged line:\n%s", dontWant, final)
		}
	}

	// the "p" toggle expands the fold into individual rows, in the final
	// render too
	fe.progressExpanded = map[dagui.SpanID]bool{installID: true}
	buf.Reset()
	if err := fe.FinalRender(&buf); err != nil {
		t.Fatalf("FinalRender: %v", err)
	}
	expanded := buf.String()
	for _, want := range []string{"pulling alpine", "pkg-1.apk", "downloading /out"} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded fold missing individual progress row %q:\n%s", want, expanded)
		}
	}
}
