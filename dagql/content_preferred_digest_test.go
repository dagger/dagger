package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestContentPreferredDigestRecipeFallback(t *testing.T) {
	for _, field := range []string{"plain", "withInputs"} {
		t.Run(field, func(t *testing.T) {
			frame := cacheTestIntCall(field)
			if field == "withInputs" {
				input := cacheTestIntCall("input")
				frame.Receiver = &ResultCallRef{Call: input}
				frame.Module = &ResultCallModule{ResultRef: &ResultCallRef{Call: input}}
				frame.ImplicitInputs = []*ResultCallArg{{Name: "scope", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{Call: input}}}}
			}
			recipe, err := frame.deriveRecipeDigest(nil)
			require.NoError(t, err)
			preferred, err := frame.contentPreferredDigestForTelemetry(nil)
			require.NoError(t, err)
			require.Equal(t, recipe, preferred)
		})
	}
}

func TestContentPreferredDigestLateContent(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	defer cacheTestReleaseSession(t, c, ctx)
	input := cacheTestIntCall("source")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: input}, func(_ctx context.Context) (AnyResult, error) {
		return cacheTestIntResult(input, 1), nil
	})
	require.NoError(t, err)
	for _, sharedFastPath := range []bool{false, true} {
		t.Run(fmt.Sprint(sharedFastPath), func(t *testing.T) {
			ref := &ResultCallRef{ResultID: uint64(res.cacheSharedResult().id)}
			if sharedFastPath {
				ref.shared = res.cacheSharedResult()
			}
			child := cacheTestIntCall("child")
			child.Receiver = ref
			parent := cacheTestIntCall("parent")
			parent.Receiver = &ResultCallRef{Call: child}
			before, err := parent.contentPreferredDigestForTelemetry(c)
			require.NoError(t, err)
			content := digest.FromString(fmt.Sprint("content-", sharedFastPath))
			require.NoError(t, c.TeachContentDigest(ctx, res, content))
			after, err := parent.contentPreferredDigestForTelemetry(c)
			require.NoError(t, err)
			require.NotEqual(t, before, after)
			expectedInput := cacheTestIntCall("different-source", call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: content})
			expectedChild := cacheTestIntCall("child")
			expectedChild.Receiver = &ResultCallRef{Call: expectedInput}
			expected := cacheTestIntCall("parent")
			expected.Receiver = &ResultCallRef{Call: expectedChild}
			expectedDigest, err := expected.contentPreferredDigestForTelemetry(nil)
			require.NoError(t, err)
			require.Equal(t, expectedDigest, after)
		})
	}
}

func TestContentPreferredDigestRecursiveInputs(t *testing.T) {
	// Every edge kind must substitute content recursively, including nested
	// literal refs. Compare to an independently recipe-hashed reference graph.
	paths := map[string]func(*ResultCall, *ResultCallRef){
		"receiver": func(f *ResultCall, r *ResultCallRef) { f.Receiver = r },
		"module":   func(f *ResultCall, r *ResultCallRef) { f.Module = &ResultCallModule{ResultRef: r} },
		"argument": func(f *ResultCall, r *ResultCallRef) {
			f.Args = []*ResultCallArg{{Name: "source", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: r}}}
		},
		"implicit": func(f *ResultCall, r *ResultCallRef) {
			f.ImplicitInputs = []*ResultCallArg{{Name: "scope", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: r}}}
		},
		"nested": func(f *ResultCall, r *ResultCallRef) {
			f.Args = []*ResultCallArg{{Name: "sources", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindList, ListItems: []*ResultCallLiteral{
				{Kind: ResultCallLiteralKindObject, ObjectFields: []*ResultCallArg{{Name: "source", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: r}}}},
			}}}}
		},
	}
	for name, set := range paths {
		t.Run(name, func(t *testing.T) {
			canonical := cacheTestIntCall("canonical")
			content, err := canonical.deriveRecipeDigest(nil)
			require.NoError(t, err)
			parent := func(input *ResultCall) *ResultCall {
				middle := cacheTestIntCall("middle")
				set(middle, &ResultCallRef{Call: input})
				root := cacheTestIntCall("root")
				root.Receiver = &ResultCallRef{Call: middle}
				return root
			}
			expected, err := parent(canonical).deriveRecipeDigest(nil)
			require.NoError(t, err)
			var recipes []digest.Digest
			for _, source := range []string{"filesync-session-a", "filesync-session-b"} {
				input := cacheTestIntCall(source, call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: content})
				frame := parent(input)
				got, err := frame.contentPreferredDigestForTelemetry(nil)
				require.NoError(t, err)
				require.Equal(t, expected, got)
				recipe, err := frame.deriveRecipeDigest(nil)
				require.NoError(t, err)
				recipes = append(recipes, recipe)
				// Same output content does not make different downstream operations equal.
				changed := frame.fork()
				changed.Field = "other-operation"
				other, err := changed.contentPreferredDigestForTelemetry(nil)
				require.NoError(t, err)
				require.NotEqual(t, got, other)
			}
			require.NotEqual(t, recipes[0], recipes[1])
		})
	}
}

func TestContentPreferredDigestLiteralsAndShape(t *testing.T) {
	literals := []*ResultCallLiteral{
		nil, {Kind: ResultCallLiteralKindNull},
		{Kind: ResultCallLiteralKindBool, BoolValue: true},
		{Kind: ResultCallLiteralKindBool, BoolValue: false},
		{Kind: ResultCallLiteralKindEnum, EnumValue: "ENUM"},
		{Kind: ResultCallLiteralKindInt, IntValue: 42},
		{Kind: ResultCallLiteralKindFloat, FloatValue: 1.5},
		{Kind: ResultCallLiteralKindString, StringValue: "value"},
		{Kind: ResultCallLiteralKindBytes, BytesValue: []byte{0, 255}},
		{Kind: ResultCallLiteralKindDigestedString, DigestedStringValue: "display", DigestedStringDigest: digest.FromString("identity")},
		{Kind: ResultCallLiteralKindList, ListItems: []*ResultCallLiteral{{Kind: ResultCallLiteralKindInt, IntValue: 2}}},
		{Kind: ResultCallLiteralKindObject, ObjectFields: []*ResultCallArg{{Name: "value", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindInt, IntValue: 3}}}},
	}
	for i, lit := range literals {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := cacheTestIntCall("shape")
			f.Kind = ResultCallKindSynthetic
			f.SyntheticOp = "synthetic-shape"
			f.Type = &ResultCallType{Elem: NewResultCallType(Int(0).Type())}
			f.Nth = 2
			f.View = call.View("test")
			f.Args = []*ResultCallArg{{Name: "literal", Value: lit}, {Name: "redacted", IsSensitive: true, Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: "secret"}}}
			f.ImplicitInputs = f.Args
			recipe, err := f.deriveRecipeDigest(nil)
			require.NoError(t, err)
			preferred, err := f.contentPreferredDigestForTelemetry(nil)
			require.NoError(t, err)
			require.Equal(t, recipe, preferred)
			if lit == nil {
				return
			}
			id, err := f.recipeID(t.Context(), nil)
			require.NoError(t, err)
			require.Equal(t, id.Digest(), recipe, "portable recipe encoding must remain unchanged")
		})
	}
}

func TestContentPreferredDigestExtraLabels(t *testing.T) {
	for _, label := range []string{"", "alias", call.ExtraDigestLabelContent} {
		t.Run(label, func(t *testing.T) {
			content := digest.FromString("same-extra")
			first := cacheTestIntCall("first", call.ExtraDigest{Label: label, Digest: content})
			second := cacheTestIntCall("second", call.ExtraDigest{Label: label, Digest: content})
			a, err := first.contentPreferredDigestForTelemetry(nil)
			require.NoError(t, err)
			b, err := second.contentPreferredDigestForTelemetry(nil)
			require.NoError(t, err)
			if label == call.ExtraDigestLabelContent {
				require.Equal(t, content, a)
				require.Equal(t, a, b)
			} else {
				recipe, err := first.deriveRecipeDigest(nil)
				require.NoError(t, err)
				require.Equal(t, recipe, a)
				require.NotEqual(t, a, b)
			}
		})
	}
}

func TestContentPreferredDigestErrors(t *testing.T) {
	frame := cacheTestIntCall("cycle")
	frame.Receiver = &ResultCallRef{Call: frame}
	_, err := frame.contentPreferredDigestForTelemetry(nil)
	require.ErrorContains(t, err, "cycle")
	frame.Receiver = &ResultCallRef{ResultID: 123}
	_, err = frame.contentPreferredDigestForTelemetry(nil)
	require.ErrorContains(t, err, "without cache")
	// Failed observations must not poison a subsequent valid traversal.
	frame.Receiver = nil
	_, err = frame.contentPreferredDigestForTelemetry(nil)
	require.NoError(t, err)
	// A recorded content digest needs no recipe traversal.
	frame.Receiver = &ResultCallRef{Call: frame}
	frame.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("known")}}
	got, err := frame.contentPreferredDigestForTelemetry(nil)
	require.NoError(t, err)
	require.Equal(t, frame.ContentDigest(), got)
}

func BenchmarkContentPreferredDigest(b *testing.B) {
	for _, size := range []int{0, 10, 100, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			frame := cacheTestIntCall("leaf")
			if size == 0 {
				frame.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: digest.FromString("content")}}
			}
			for range size {
				next := cacheTestIntCall("next")
				next.Receiver = &ResultCallRef{Call: frame}
				next.Module = &ResultCallModule{ResultRef: &ResultCallRef{Call: frame}}
				frame = next
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := frame.contentPreferredDigestForTelemetry(nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestContentPreferredDigestPreservesRuntimeIdentity(t *testing.T) {
	frame := cacheTestIntCall("plain")
	// Recorded on upstream/main before the telemetry implementation. The
	// historical runtime helper has a different delimiter layout; services use
	// it for hostnames, so adding telemetry must not silently change its output.
	runtimeDigest, err := frame.deriveContentPreferredDigest(nil)
	require.NoError(t, err)
	require.Equal(t, digest.Digest("xxh3:980c8fba10c53c57"), runtimeDigest)
	recipe, err := frame.deriveRecipeDigest(nil)
	require.NoError(t, err)
	require.Equal(t, digest.Digest("xxh3:d8611ec28d7ef2eb"), recipe)
	observed, err := frame.contentPreferredDigestForTelemetry(nil)
	require.NoError(t, err)
	require.Equal(t, recipe, observed)
}

func TestContentPreferredDigestSnapshotAndConcurrentTeaching(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	defer cacheTestReleaseSession(t, c, ctx)
	input := cacheTestIntCall("input")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: input}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(input, 1), nil
	})
	require.NoError(t, err)
	child := cacheTestIntCall("child")
	child.Receiver = &ResultCallRef{ResultID: uint64(res.cacheSharedResult().id), shared: res.cacheSharedResult()}
	// A persisted frame loses the shared-pointer fast path. Both forms must
	// observe new content, without storing traversal state in serialized data.
	encoded, err := json.Marshal(child)
	require.NoError(t, err)
	var restored ResultCall
	require.NoError(t, json.Unmarshal(encoded, &restored))
	before, err := child.contentPreferredDigestForTelemetry(c)
	require.NoError(t, err)
	content := digest.FromString("learned")
	expectedFrame := cacheTestIntCall("child")
	expectedFrame.Receiver = &ResultCallRef{Call: cacheTestIntCall("other-input", call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: content})}
	after, err := expectedFrame.contentPreferredDigestForTelemetry(nil)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		for range 20 {
			if err := c.TeachContentDigest(ctx, res, content); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 20 {
		for _, frame := range []*ResultCall{child, &restored} {
			got, err := frame.contentPreferredDigestForTelemetry(c)
			require.NoError(t, err)
			require.Contains(t, []digest.Digest{before, after}, got)
		}
	}
	require.NoError(t, <-done)
	for _, frame := range []*ResultCall{child, &restored} {
		got, err := frame.contentPreferredDigestForTelemetry(c)
		require.NoError(t, err)
		require.Equal(t, after, got)
	}
}

func TestContentPreferredDigestDoesNotCanonicalizeEquivalence(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	defer cacheTestReleaseSession(t, c, ctx)
	parent := cacheTestIntCall("parent")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: parent}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(parent, 1), nil
	})
	require.NoError(t, err)
	noop := cacheTestIntCall("noop")
	noop.Receiver = &ResultCallRef{ResultID: uint64(res.cacheSharedResult().id)}
	require.NoError(t, c.TeachCallEquivalentToResult(ctx, "test-session", noop, res))
	a, err := parent.contentPreferredDigestForTelemetry(c)
	require.NoError(t, err)
	b, err := noop.contentPreferredDigestForTelemetry(c)
	require.NoError(t, err)
	require.NotEqual(t, a, b, "e-graph equality alone is not recorded content")
}
