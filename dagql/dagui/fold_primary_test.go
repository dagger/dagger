package dagui

import (
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// A nested CLI's primary span has a parent it never receives (the exec it
// runs in), so it's no root: its children are top-level rows all the same,
// and are retained, those folded before it became primary read back.
func TestFoldPrimarySpanChildrenRetained(t *testing.T) {
	// 1 the exec the CLI runs in, never received
	// └── 2 the CLI's command span, primary
	//     ├── 3 a step that ended before it became primary
	//     └── 4 a step after
	//         └── 5 its work
	folded, store := newFoldedDB(nil)
	export := func(spans ...sdktrace.ReadOnlySpan) {
		t.Helper()
		if err := store.ExportSpans(t.Context(), spans); err != nil {
			t.Fatal(err)
		}
	}
	export(lateTestSpan(3, 2, false))
	if !folded.Fold().IsFolded(testID(3)) {
		t.Fatal("span 3 should fold before span 2 is primary")
	}
	folded.SetPrimarySpan(testID(2))
	export(lateTestSpan(4, 2, false), lateTestSpan(5, 4, false))
	export(lateTestSpan(2, 1, true))
	for _, id := range []byte{2, 3, 4} {
		if span := folded.Spans.Map[testID(id)]; span == nil || !span.Received {
			t.Errorf("span %d is not retained", id)
		}
	}
	if !folded.Fold().IsFolded(testID(5)) {
		t.Error("span 5 should stay folded beneath its retained parent")
	}
}
