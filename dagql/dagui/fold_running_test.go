package dagui

import (
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// Only the DB's root span ending cancels what still runs: not another
// trace's root, ended already (e.g. a resumed session's archived trace).
func TestFoldOtherRootEnded(t *testing.T) {
	other := tracetest.SpanStubFromReadOnlySpan(lateTestSpan(9, 0, false))
	other.SpanContext = trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{2}, SpanID: testID(9).SpanID})
	spans := []sdktrace.ReadOnlySpan{
		lateTestSpan(1, 0, true),
		lateTestSpan(2, 1, true),
		lateTestSpan(3, 2, true),
	}
	later := []sdktrace.ReadOnlySpan{other.Snapshot(), lateTestSpan(4, 2, true)}
	full := NewDB()
	folded, store := newFoldedDB(nil)
	for _, batch := range [][]sdktrace.ReadOnlySpan{spans, later} {
		if err := full.ExportSpans(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
		if err := store.ExportSpans(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if !folded.Fold().IsFolded(testID(3)) {
		t.Fatal("span 3 should be folded")
	}
	opts := FrontendOpts{ZoomedSpan: testID(1)}
	want := full.RowsView(opts).Rows(opts).BySpan[testID(2)]
	got := folded.RowsView(opts).Rows(opts).BySpan[testID(2)]
	if want == nil || got == nil {
		t.Fatal("row 2 missing")
	}
	if !want.IsRunningOrChildRunning || got.IsRunningOrChildRunning != want.IsRunningOrChildRunning {
		t.Errorf("row 2 running %v, want %v", got.IsRunningOrChildRunning, want.IsRunningOrChildRunning)
	}
	if w, g := full.Spans.Map[testID(2)].rollUpState, folded.Spans.Map[testID(2)].rollUpState; w == nil || g == nil || *w != *g {
		t.Errorf("row 2 roll-up %+v, want %+v", g, w)
	}
}

// Progress arriving ahead of its span is kept when the span is retained on
// arrival, as a full DB keeps it on a placeholder.
func TestFoldProgressAheadOfSpan(t *testing.T) {
	full := NewDB()
	folded, store := newFoldedDB(nil)
	for _, db := range []*struct {
		db  *DB
		exp sdktrace.SpanExporter
	}{{full, full}, {folded, store}} {
		if err := db.exp.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{lateTestSpan(1, 0, true)}); err != nil {
			t.Fatal(err)
		}
		db.db.IngestProgress(testID(2), "layer", 5, 10, "bytes")
		if err := db.exp.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{lateTestSpan(2, 1, true)}); err != nil {
			t.Fatal(err)
		}
	}
	want, got := full.Spans.Map[testID(2)], folded.Spans.Map[testID(2)]
	if want == nil || got == nil || !want.HasProgress() {
		t.Fatal("span 2 should be loaded with progress in the full DB")
	}
	if !got.HasProgress() {
		t.Error("the folding DB dropped the progress that arrived ahead of span 2")
	}
	if want.ParentSpan.ProgressSpans.Len() != got.ParentSpan.ProgressSpans.Len() {
		t.Errorf("root progress spans %d, want %d", got.ParentSpan.ProgressSpans.Len(), want.ParentSpan.ProgressSpans.Len())
	}
}

// A row that ended while something beneath it still runs shows running when
// the view would place what runs, and not when it hides it, whether what
// runs is folded or not.
func TestFoldRunningBeneathEndedRows(t *testing.T) {
	internal := attribute.Bool(telemetry.UIInternalAttr, true)
	encapsulate := attribute.Bool(telemetry.UIEncapsulateAttr, true)
	spans := []sdktrace.ReadOnlySpan{
		lateTestSpan(1, 0, true),
		// a visible span running beneath an ended row
		lateTestSpan(2, 1, false),
		lateTestSpan(3, 2, true),
		// an internal one
		lateTestSpan(4, 1, false),
		lateTestSpan(5, 4, true, internal),
		// one an ended row encapsulates
		lateTestSpan(6, 1, false, encapsulate),
		lateTestSpan(7, 6, true),
		// one beneath an internal span
		lateTestSpan(8, 1, false),
		lateTestSpan(9, 8, false, internal),
		lateTestSpan(10, 9, true),
		// deeper: a visible running span two levels down
		lateTestSpan(11, 1, false),
		lateTestSpan(12, 11, false),
		lateTestSpan(13, 12, true),
	}
	full := NewDB()
	if err := full.ExportSpans(t.Context(), spans); err != nil {
		t.Fatal(err)
	}
	folded, store := newFoldedDB(nil)
	if err := store.ExportSpans(t.Context(), spans); err != nil {
		t.Fatal(err)
	}
	if !folded.Fold().IsFolded(testID(3)) || !folded.Fold().IsFolded(testID(13)) {
		t.Fatal("the running spans should be folded")
	}
	opts := FrontendOpts{ZoomedSpan: testID(1)}
	want := full.RowsView(opts).Rows(opts)
	got := folded.RowsView(opts).Rows(opts)
	for _, id := range []byte{2, 4, 6, 8, 11} {
		w, g := want.BySpan[testID(id)], got.BySpan[testID(id)]
		if w == nil || g == nil {
			t.Fatalf("row %d missing: full %v, folded %v", id, w != nil, g != nil)
		}
		if w.IsRunningOrChildRunning != g.IsRunningOrChildRunning {
			t.Errorf("row %d: running %v, want %v", id, g.IsRunningOrChildRunning, w.IsRunningOrChildRunning)
		}
	}
	if !want.BySpan[testID(2)].IsRunningOrChildRunning || want.BySpan[testID(4)].IsRunningOrChildRunning {
		t.Fatal("the full DB should show only visible running work running")
	}
}
