package dagui

import (
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Generate's skipped and regenerated module spans are retained wherever they
// sit, as the report lists them (DB.SkippedModuleSpans): beneath an
// encapsulated preparation span too.
func TestFoldGenerateModuleSpansRetained(t *testing.T) {
	// 1 root
	// └── 2 prepare artifacts (encapsulate)
	//     └── 3 load modules
	//         ├── 4 skipped module (failed)
	//         └── 5 regenerated module
	folded, store := newFoldedDB(nil)
	if err := store.ExportSpans(t.Context(), []sdktrace.ReadOnlySpan{
		lateTestSpan(1, 0, true),
		lateTestSpan(2, 1, false, attribute.Bool(telemetry.UIEncapsulateAttr, true)),
		lateTestSpan(3, 2, false),
		lateTestSpan(4, 3, false, attribute.Bool(telemetryattrs.GenerateSkippedAttr, true)),
		lateTestSpan(5, 3, false, attribute.Bool(telemetryattrs.GenerateRegeneratedAttr, true)),
	}); err != nil {
		t.Fatal(err)
	}
	if got := folded.SkippedModuleSpans(); len(got) != 1 || got[0].ID != testID(4) {
		t.Errorf("skipped module spans = %v, want span 4", got)
	}
	if got := folded.RegeneratedModuleSpans(); len(got) != 1 {
		t.Errorf("regenerated module spans = %v, want span 5", got)
	}
}
