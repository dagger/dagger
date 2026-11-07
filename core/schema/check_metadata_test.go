package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestCheckCollectionMetadataViews(t *testing.T) {
	for _, tc := range []struct {
		version string
		visible bool
	}{
		{"v1.0.0-beta.25", false},
		{checkCollectionMetadataVersion, true},
		{"v1.0.0", true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			view := call.View(engine.APIViewVersion(tc.version))
			_, srv := newNestingTestServer(t, view)
			schema := srv.SchemaForView(view)
			require.Equal(t, tc.visible, schema.Types["Artifact"].Fields.ForName("__checkGroup") != nil)
			address := schema.Types["Check"].Fields.ForName("__withAddress")
			require.NotNil(t, address)
			require.NotNil(t, address.Arguments.ForName("address"))
			require.Equal(t, tc.visible, address.Arguments.ForName("collectionItem") != nil)
		})
	}
}

func TestCheckCollectionMetadata(t *testing.T) {
	// An older client still gets item metadata from the engine-owned helper.
	ctx, srv := newNestingTestServer(t, "v1.0.0-beta.25")
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx, root := provider.Tracer("test").Start(ctx, "check metadata")
	defer root.End()
	dagql.Fields[*core.Query]{
		dagql.Func("sampleCheck", func(context.Context, *core.Query, struct{}) (*core.Check, error) {
			return &core.Check{Failure: "expected failure"}, nil
		}),
		dagql.NodeFunc("sampleArtifact", func(ctx context.Context, _ dagql.ObjectResult[*core.Query], args struct{ Item bool }) (*core.Artifact, error) {
			var check dagql.ObjectResult[*core.Check]
			if err := srv.Select(ctx, srv.Root(), &check, dagql.Selector{Field: "sampleCheck"}); err != nil {
				return nil, err
			}
			node := &core.ModTreeNode{RootValue: check}
			if args.Item {
				key := "first"
				node.CollectionDimension = &core.ArtifactDimension{Identifier: "cases"}
				node.CollectionKey = &key
			}
			return &core.Artifact{TypeName: "Check", Path: []string{"check"}, Node: node}, nil
		}),
	}.Install(srv)

	// The same underlying check is addressed both ways and read repeatedly.
	// Its cached result must not replace the address's collection identity.
	for _, item := range []bool{true, false, true, false} {
		var result dagql.ObjectResult[*core.Check]
		err := srv.Select(ctx, srv.Root(), &result,
			dagql.Selector{Field: "sampleArtifact", Args: []dagql.NamedInput{{Name: "item", Value: dagql.Boolean(item)}}},
			dagql.Selector{Field: "value", View: srv.View},
		)
		require.NoError(t, err)
		frame, err := result.ResultCall()
		require.NoError(t, err)
		require.Equal(t, "__withAddress", frame.Field)
		require.Equal(t, call.View(checkCollectionMetadataVersion), frame.View)
		err = srv.Select(ctx, result, &result, dagql.Selector{Field: "sync"})
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
