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

// The spans a row's call renders its ID arguments through -- the span
// running an argument, the span producing it, at any depth rendering reaches
// -- are retained once rendering asks for them, as a full DB renders them:
// folded ones read back, ones to come on arrival. Spans no rendering asks for
// stay folded.
func TestFoldArgumentSpansRetained(t *testing.T) {
	// 1 root
	// ├── 2 setup
	// │   ├── 3 runs the argument dir (call D), folded before 6 arrives
	// │   │   └── 4 produces its file (output F)
	// │   └── 8 produces something no row renders (output X)
	// ├── 5 setup
	// │   └── 7 produces dir too (output D), after 6
	// └── 6 the row: Query.use(dir: D)
	file := &callpbv1.Call{Field: "file", Digest: "xxh3:000000000000000f", Type: &callpbv1.Type{NamedType: "File"}}
	dir := &callpbv1.Call{Field: "directory", Digest: "xxh3:000000000000000d", Type: &callpbv1.Type{NamedType: "Directory"},
		Args: []*callpbv1.Argument{{Name: "file", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: file.Digest}}}}}
	use := &callpbv1.Call{Field: "use", Digest: "xxh3:0000000000000001", Type: &callpbv1.Type{NamedType: "Void"},
		Args: []*callpbv1.Argument{{Name: "dir", Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: dir.Digest}}}}}
	other := &callpbv1.Call{Field: "other", Digest: "xxh3:0000000000000002", Type: &callpbv1.Type{NamedType: "Directory"}}
	unused := &callpbv1.Call{Field: "unused", Digest: "xxh3:0000000000000003", Type: &callpbv1.Type{NamedType: "Directory"}}

	folded, store := newFoldedDB(nil)
	export := func(spans ...sdktrace.ReadOnlySpan) {
		t.Helper()
		if err := store.ExportSpans(t.Context(), spans); err != nil {
			t.Fatal(err)
		}
	}
	// render the row's arguments as the CLI's renderer does: each through
	// its most interesting span, as its creator's call, and that call's
	// arguments in turn, until what rendering reaches is read back
	var renderArgs func(call *callpbv1.Call, seen map[string]bool)
	renderArgs = func(call *callpbv1.Call, seen map[string]bool) {
		for _, arg := range call.GetArgs() {
			dig := arg.GetValue().GetCallDigest()
			if dig == "" || seen[dig] {
				continue
			}
			seen[dig] = true
			folded.MostInterestingSpan(dig)
			renderArgs(folded.Simplify(folded.MustCall(dig), false), seen)
		}
	}
	render := func() {
		t.Helper()
		for range 8 {
			renderArgs(use, map[string]bool{})
			if !folded.Fold().RetainAsked() {
				return
			}
		}
		t.Fatal("rendering kept reading back")
	}
	retained := func(id byte) bool {
		span := folded.Spans.Map[testID(id)]
		return span != nil && span.Received
	}

	export(
		lateTestSpan(1, 0, true),
		lateTestSpan(2, 1, false),
		lateTestSpan(3, 2, false, callAttrs(t, dir, "")...),
		lateTestSpan(4, 3, false, callAttrs(t, other, file.Digest)...),
		lateTestSpan(8, 2, false, callAttrs(t, unused, "xxh3:00000000000000aa")...),
		lateTestSpan(5, 1, false),
	)
	for _, id := range []byte{3, 4, 8} {
		if !folded.Fold().IsFolded(testID(id)) {
			t.Fatalf("span %d should fold before the row arrives", id)
		}
	}
	export(lateTestSpan(6, 1, false, callAttrs(t, use, "")...))
	render()
	for _, id := range []byte{3, 4} {
		if !retained(id) {
			t.Errorf("span %d, behind the row's argument, is not retained", id)
		}
	}
	// a creator of the argument arriving later is retained on arrival
	export(lateTestSpan(7, 5, false, callAttrs(t, other, dir.Digest)...))
	if !retained(7) {
		t.Error("span 7, producing the row's argument, is not retained on arrival")
	}
	if !folded.Fold().IsFolded(testID(8)) {
		t.Error("span 8, which no rendering asked for, should stay folded")
	}
	if got := folded.MostInterestingSpan(dir.Digest); got == nil || got.ID != testID(3) {
		t.Errorf("most interesting span of the argument = %v, want span 3", got)
	}
	if got := folded.Simplify(dir, false); got.Digest != other.Digest {
		t.Errorf("argument simplifies to %s, want its creator's call %s", got.Digest, other.Digest)
	}
}

// The digest index finds every folded span under its call and its output,
// across growth and collisions.
func TestFoldDigestIndex(t *testing.T) {
	var idx digestIndex
	next := make([]uint32, 5000)
	for i := range uint32(5000) {
		next[i] = chainOf(idx.push(uint64(i%700), i))
	}
	for k := range uint64(700) {
		var n int
		for j := idx.head(k); j != 0 && j != chainEnd; j = next[j-1] {
			if uint64(j-1)%700 != k {
				t.Fatalf("key %d holds span %d", k, j-1)
			}
			n++
		}
		if want := 5000 / 700; n != want && n != want+1 {
			t.Fatalf("key %d holds %d spans", k, n)
		}
	}
	if idx.head(9999) != 0 {
		t.Fatal("an absent key holds spans")
	}
	if digestKey("xxh3:00000000000000ff") != 0xff || digestKey("sha256:abc") == digestKey("sha256:abd") {
		t.Fatal("digest keys")
	}
}
