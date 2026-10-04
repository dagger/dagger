package core

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestAroundFuncContentPreferredDigest(t *testing.T) {
	for _, mode := range []string{"recipe", "content", "cached-content", "returned-other-recipe", "error", "late-input"} {
		t.Run(mode, func(t *testing.T) {
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			defer tp.Shutdown(t.Context())
			ctx, root := tp.Tracer("digest-test").Start(t.Context(), "root")
			defer root.End()
			cache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			ctx = dagql.ContextWithCache(ctx, cache)
			frame := evidenceTestFrame("operation")
			content := digest.FromString("output-content")
			var input dagql.AnyResult
			if mode == "late-input" {
				input, err = dagql.NewResultForCall(dagql.Int(1), evidenceTestFrame("source"))
				require.NoError(t, err)
				srv, err := dagql.NewServer(ctx, &Query{})
				require.NoError(t, err)
				input, err = cache.AttachResult(ctx, "digest-test", srv, input)
				require.NoError(t, err)
				id, err := input.ID()
				require.NoError(t, err)
				frame.Receiver = &dagql.ResultCallRef{ResultID: id.EngineResultID()}
			}
			recipe, err := frame.RecipeDigest(ctx)
			require.NoError(t, err)
			expected := recipe
			_, done := AroundFunc(ctx, &dagql.CallRequest{ResultCall: frame, ReceiverTypeName: "Container"})
			var res dagql.AnyResult
			var callErr error
			switch mode {
			case "content", "cached-content":
				res, err = dagql.NewResultForCall(dagql.Int(1), evidenceTestFrame("result"))
				require.NoError(t, err)
				res, err = res.WithContentDigestAny(ctx, content)
				require.NoError(t, err)
				expected = content
			case "returned-other-recipe":
				res, err = dagql.NewResultForCall(dagql.Int(1), evidenceTestFrame("different"))
				require.NoError(t, err)
			case "error":
				callErr = errors.New("execution failed")
			case "late-input":
				require.NoError(t, cache.TeachContentDigest(ctx, input, content))
				expectedFrame := evidenceTestFrame("operation")
				expectedInput := evidenceTestFrame("another-source")
				expectedInput.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: content}}
				expectedFrame.Receiver = &dagql.ResultCallRef{Call: expectedInput}
				expected, err = expectedFrame.ContentPreferredDigestForTelemetry(ctx)
				require.NoError(t, err)
				require.NotEqual(t, recipe, expected)
			}
			done(res, mode == "cached-content", &callErr)
			require.Len(t, sr.Ended(), 1)
			attrs := map[string]attribute.Value{}
			for _, kv := range sr.Ended()[0].Attributes() {
				attrs[string(kv.Key)] = kv.Value
			}
			require.Equal(t, recipe.String(), attrs[telemetry.DagDigestAttr].AsString())
			require.Equal(t, attribute.STRING, attrs[telemetryattrs.DagContentPreferredDigestAttr].Type())
			require.Equal(t, expected.String(), attrs[telemetryattrs.DagContentPreferredDigestAttr].AsString())
			require.Contains(t, attrs, telemetry.DagCallAttr)
		})
	}
}

func TestAroundFuncTrivialFieldContentPreferredDigest(t *testing.T) {
	// Trivial getters skip the recipe walk but still carry output content.
	for _, tc := range []struct {
		name    string
		trivial bool
		content bool
		want    string // "recipe", "content" or "" for no attribute
	}{
		{name: "trivial", trivial: true, want: ""},
		{name: "trivial-content", trivial: true, content: true, want: "content"},
		{name: "non-trivial", want: "recipe"},
		{name: "non-trivial-content", content: true, want: "content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			defer tp.Shutdown(t.Context())
			ctx, root := tp.Tracer("digest-test").Start(t.Context(), "root")
			defer root.End()
			cache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			ctx = dagql.ContextWithCache(ctx, cache)
			if tc.trivial {
				ctx = dagql.ContextWithTrivialField(ctx)
			}
			frame := evidenceTestFrame("getter")
			recipe, err := frame.RecipeDigest(ctx)
			require.NoError(t, err)
			content := digest.FromString("output-content")

			var res dagql.AnyResult
			res, err = dagql.NewResultForCall(dagql.Int(1), evidenceTestFrame("value"))
			require.NoError(t, err)
			if tc.content {
				res, err = res.WithContentDigestAny(ctx, content)
				require.NoError(t, err)
			}

			_, done := AroundFunc(ctx, &dagql.CallRequest{ResultCall: frame, ReceiverTypeName: "DiffStat"})
			var callErr error
			done(res, false, &callErr)
			require.Len(t, sr.Ended(), 1)
			attrs := map[string]attribute.Value{}
			for _, kv := range sr.Ended()[0].Attributes() {
				attrs[string(kv.Key)] = kv.Value
			}
			require.Equal(t, recipe.String(), attrs[telemetry.DagDigestAttr].AsString())
			require.Equal(t, tc.trivial, attrs[telemetry.UIInternalAttr].AsBool())
			got, ok := attrs[telemetryattrs.DagContentPreferredDigestAttr]
			switch tc.want {
			case "":
				require.False(t, ok, "unexpected content-preferred digest %q", got.Emit())
			case "recipe":
				require.True(t, ok)
				require.Equal(t, recipe.String(), got.AsString())
			case "content":
				require.True(t, ok)
				require.Equal(t, content.String(), got.AsString())
			}
		})
	}
}

func TestRecordContentPreferredDigestFailureIsOptional(t *testing.T) {
	sr, span := evidenceTestRecordingSpan(t)
	// A dangling input cannot be derived without a cache. No placeholder or
	// recipe guess should masquerade as a successfully derived identity.
	frame := evidenceTestFrame("unavailable")
	frame.Receiver = &dagql.ResultCallRef{ResultID: 99}
	dagql.RecordContentPreferredDigest(context.Background(), span, frame, nil)
	attrs := evidenceTestEndedAttrs(t, sr, span)
	for _, kv := range attrs {
		require.NotEqual(t, telemetryattrs.DagContentPreferredDigestAttr, string(kv.Key))
	}
}
