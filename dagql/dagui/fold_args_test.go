package dagui

import (
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// callAttrs are the attributes of a span carrying call, and producing output
// if it's set.
func callAttrs(t *testing.T, call *callpbv1.Call, output string) []attribute.KeyValue {
	t.Helper()
	payload, err := call.Encode()
	if err != nil {
		t.Fatal(err)
	}
	attrs := []attribute.KeyValue{
		attribute.String(telemetry.DagDigestAttr, call.Digest),
		attribute.String(telemetry.DagCallAttr, payload),
	}
	if output != "" {
		attrs = append(attrs, attribute.String(telemetry.DagOutputAttr, output))
	}
	return attrs
}

// The spans a retained row's call renders its ID arguments through -- the
// span running an argument, the span producing it -- are retained, as a full
// DB renders them: whether they arrived before the row or after.
func TestFoldArgumentSpansRetained(t *testing.T) {
	// 1 root
	// ├── 2 setup
	// │   └── 3 runs the argument dir (call D), folded before 6 arrives
	// │       └── 4 produces its file (output F)
	// ├── 5 setup
	// │   └── 7 produces dir too (output D), after 6
	// └── 6 the row: Query.use(dir: D)
	file := &callpbv1.Call{Field: "file", Digest: "xxh3:F", Type: &callpbv1.Type{NamedType: "File"}}
	dir := &callpbv1.Call{Field: "directory", Digest: "xxh3:D", Type: &callpbv1.Type{NamedType: "Directory"},
		Args: []*callpbv1.Argument{{Name: "file", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: file.Digest}}}}}
	use := &callpbv1.Call{Field: "use", Digest: "xxh3:U", Type: &callpbv1.Type{NamedType: "Void"},
		Args: []*callpbv1.Argument{{Name: "dir", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: dir.Digest}}}}}
	other := &callpbv1.Call{Field: "other", Digest: "xxh3:O", Type: &callpbv1.Type{NamedType: "Directory"}}

	folded, store := newFoldedDB(nil)
	export := func(spans ...sdktrace.ReadOnlySpan) {
		t.Helper()
		if err := store.ExportSpans(t.Context(), spans); err != nil {
			t.Fatal(err)
		}
	}
	export(
		lateTestSpan(1, 0, true),
		lateTestSpan(2, 1, false),
		lateTestSpan(3, 2, false, callAttrs(t, dir, "")...),
		lateTestSpan(4, 3, false, callAttrs(t, other, file.Digest)...),
		lateTestSpan(5, 1, false),
	)
	for _, id := range []byte{3, 4} {
		if !folded.Fold().IsFolded(testID(id)) {
			t.Fatalf("span %d should fold before the row arrives", id)
		}
	}
	export(lateTestSpan(6, 1, false, callAttrs(t, use, "")...))
	export(lateTestSpan(7, 5, false, callAttrs(t, other, dir.Digest)...))
	for _, id := range []byte{3, 4, 7} {
		if span := folded.Spans.Map[testID(id)]; span == nil || !span.Received {
			t.Errorf("span %d, behind the row's argument, is not retained", id)
		}
	}
	if got := folded.MostInterestingSpan(dir.Digest); got == nil || got.ID != testID(3) {
		t.Errorf("most interesting span of the argument = %v, want span 3", got)
	}
	if got := folded.Simplify(dir, false); got.Digest != other.Digest {
		t.Errorf("argument simplifies to %s, want its creator's call %s", got.Digest, other.Digest)
	}
}
