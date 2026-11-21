package dagui

import (
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

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
