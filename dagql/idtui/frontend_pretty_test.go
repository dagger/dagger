package idtui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/trace"
)

func TestSortErrorOriginsUsesCurrentSpanData(t *testing.T) {
	spanID := func(id byte) dagui.SpanID {
		return dagui.SpanID{SpanID: trace.SpanID{id}}
	}
	start := time.Unix(100, 0)
	firstParent := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(1), Name: "sub-thing 1"}}
	first := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(2), Name: "withExec first", StartTime: start}, ParentSpan: firstParent}
	secondParent := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(3), Name: "sub-thing 2"}}
	second := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(4), Name: "withExec second", StartTime: start.Add(time.Second)}, ParentSpan: secondParent}

	origins := []*dagui.Span{second, first}
	sortErrorOrigins(origins)
	if origins[0] != first || origins[1] != second {
		t.Fatalf("origins sorted by current start time = %q, %q; want first, second", origins[0].Name, origins[1].Name)
	}

	first.StartTime = time.Time{}
	second.StartTime = time.Time{}
	origins = []*dagui.Span{second, first}
	sortErrorOrigins(origins)
	if origins[0] != first || origins[1] != second {
		t.Fatalf("origins sorted by path tie-breaker = %q, %q; want first, second", origins[0].Name, origins[1].Name)
	}
}

func TestDoQuitInvalidatesCachedRender(t *testing.T) {
	db := dagui.NewDB()
	spanID := dagui.SpanID{SpanID: trace.SpanID{1}}
	db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID:        spanID,
		Name:      "cached-live-row",
		StartTime: time.Now(),
		EndTime:   time.Now(),
		Final:     true,
	}})
	db.SetPrimarySpan(spanID)

	fe := NewWithDB(io.Discard, db)
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()

	before := fe.tui.RenderLines()
	if got := strings.Join(before, "\n"); !strings.Contains(got, "cached-live-row") {
		t.Fatalf("initial render = %q, want live row", got)
	}

	fe.quit = make(chan struct{})
	fe.quitting = true
	fe.doQuit()

	if after := fe.tui.RenderLines(); len(after) != 0 {
		t.Fatalf("render after quit = %q, want no live rows", strings.Join(after, "\n"))
	}
}
