package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestLazyContentPreferredDigest(t *testing.T) {
	sr, rootCtx, root := newLazyRecordingRoot("root")
	defer root.End()
	ctx := cacheTestContext(rootCtx)
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	defer cacheTestReleaseSession(t, c, ctx)
	srv := cacheTestServer(t)
	frame := &ResultCall{Field: "lateContent", Type: NewResultCallType((&cacheTestObject{}).Type())}
	content := digest.FromString("lazy-output")
	var res AnyResult
	res, err = c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
			lazyEval: func(ctx context.Context) error { return c.TeachContentDigest(ctx, res, content) },
		}), nil
	})
	require.NoError(t, err)
	require.True(t, HasPendingLazyEvaluation(res))
	require.NoError(t, c.Evaluate(ctx, res))
	var found bool
	for _, span := range sr.Ended() {
		if span.Name() != "resume lateContent" {
			continue
		}
		for _, kv := range span.Attributes() {
			if string(kv.Key) == telemetryattrs.DagContentPreferredDigestAttr {
				require.Equal(t, content.String(), kv.Value.AsString())
				found = true
			}
		}
	}
	require.True(t, found, "lazy completion must export content learned after API completion")
}

func TestRecordOutputContentDigest(t *testing.T) {
	ctx := t.Context()
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)

	newFrame := func(field string) *ResultCall {
		return &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType(Int(0).Type()), Field: field}
	}
	frame := newFrame("getter")
	recipe, err := frame.RecipeDigest(ctx)
	require.NoError(t, err)
	content := digest.FromString("output-content")
	plain, err := NewResultForCall(Int(1), newFrame("value"))
	require.NoError(t, err)
	withContent, err := plain.WithContentDigestAny(ctx, content)
	require.NoError(t, err)

	record := func(fn func(trace.Span)) (string, bool) {
		t.Helper()
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		defer tp.Shutdown(t.Context())
		_, span := tp.Tracer("digest-test").Start(ctx, "span")
		fn(span)
		span.End()
		require.Len(t, sr.Ended(), 1)
		for _, kv := range sr.Ended()[0].Attributes() {
			if kv.Key == attribute.Key(telemetryattrs.DagContentPreferredDigestAttr) {
				return kv.Value.AsString(), true
			}
		}
		return "", false
	}

	// Without output content, nothing is derived from the recipe.
	for _, res := range []AnyResult{nil, plain} {
		_, ok := record(func(span trace.Span) { RecordOutputContentDigest(span, res) })
		require.False(t, ok)
	}
	got, ok := record(func(span trace.Span) { RecordOutputContentDigest(span, withContent) })
	require.True(t, ok)
	require.Equal(t, content.String(), got)

	// RecordContentPreferredDigest still falls back to the recipe.
	got, ok = record(func(span trace.Span) { RecordContentPreferredDigest(ctx, span, frame, plain) })
	require.True(t, ok)
	require.Equal(t, recipe.String(), got)
	got, ok = record(func(span trace.Span) { RecordContentPreferredDigest(ctx, span, frame, withContent) })
	require.True(t, ok)
	require.Equal(t, content.String(), got)
}
