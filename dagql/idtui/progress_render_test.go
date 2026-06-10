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

	// dots per cell = ceil(current*8/total), clamped to [1,8]; an unknown
	// total renders a single dot
	if want := "⣿⣿⡇⡀⡀⡀ 30 MB/50 MB"; !strings.Contains(got, want) {
		t.Errorf("render missing partial fills %q:\n%s", want, got)
	}
	// items past the cap fold into +N; all-complete items omit the total
	if want := strings.Repeat("⣿", 40) + "+10 51 kB"; !strings.Contains(got, want) {
		t.Errorf("render missing overflow bar %q:\n%s", want, got)
	}
}
