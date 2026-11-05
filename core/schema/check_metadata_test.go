package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCheckCollectionMetadata(t *testing.T) {
	ctx, srv := newNestingTestServer(t, "v1.0.0")
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx, root := provider.Tracer("test").Start(ctx, "check metadata")
	defer root.End()
	dagql.Fields[*core.Query]{dagql.Func("sampleCheck", func(context.Context, *core.Query, struct{}) (*core.Check, error) {
		return &core.Check{Failure: "expected failure"}, nil
	})}.Install(srv)

	// The same underlying check is addressed both ways and read repeatedly.
	// Its cached result must not replace the address's collection identity.
	for _, item := range []bool{true, false, true, false} {
		var result dagql.ObjectResult[*core.Check]
		err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{Field: "sampleCheck"},
			dagql.Selector{Field: "__withAddress", Args: []dagql.NamedInput{
				{Name: "address", Value: dagql.String("same display name")},
				{Name: "collectionItem", Value: dagql.Boolean(item)},
			}},
			dagql.Selector{Field: "sync"},
		)
		require.NoError(t, err)
		require.True(t, result.Self().Error.Valid)
	}

	var classified []bool
	for _, span := range recorder.Ended() {
		isCheck := false
		var item *bool
		for _, attr := range span.Attributes() {
			if string(attr.Key) == telemetry.CheckNameAttr {
				isCheck = true
			}
			if string(attr.Key) == telemetryattrs.CheckCollectionItemAttr {
				value := attr.Value.AsBool()
				item = &value
			}
		}
		if isCheck {
			require.NotNil(t, item)
			classified = append(classified, *item)
		}
	}
	require.Equal(t, []bool{true, false, true, false}, classified)
}
