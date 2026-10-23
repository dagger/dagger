package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dagger/dagger/engine/clientdb"
	telemetry "github.com/dagger/otel-go"
)

// Rows share the encoding of a resource or scope that several spans carry.
// Each row must still hold exactly its own span's resource and scope.
func TestSpanRowsEncodeEachSpansOwnResourceAndScope(t *testing.T) {
	resA := resource.NewSchemaless(attribute.String("service.name", "a"))
	resB := resource.NewWithAttributes("https://example.com/schema", attribute.String("service.name", "b"))
	scopeA := instrumentation.Scope{Name: "a"}
	scopeB := instrumentation.Scope{Name: "b", Version: "1"}
	stub := func(id byte, res *resource.Resource, scope instrumentation.Scope) sdktrace.ReadOnlySpan {
		return tracetest.SpanStub{
			Name: "span",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: trace.TraceID{1}, SpanID: trace.SpanID{id},
			}),
			StartTime:            time.Unix(1, 0),
			EndTime:              time.Unix(2, 0),
			Resource:             res,
			InstrumentationScope: scope,
		}.Snapshot()
	}
	spans := []sdktrace.ReadOnlySpan{
		stub(1, resA, scopeA),
		stub(2, resB, scopeA),
		stub(3, resA, scopeB),
		stub(4, nil, instrumentation.Scope{}),
		stub(5, resB, scopeB),
		// Equal attributes in a separate resource value.
		stub(6, resource.NewSchemaless(attribute.String("service.name", "a")), scopeA),
	}

	rows := spanRows(spans)
	require.Len(t, rows, len(spans))
	for i, span := range spans {
		require.True(t, rows[i].ok)
		require.Equal(t, span.SpanContext().SpanID().String(), rows[i].row.SpanID)
		wantRes, err := protojson.Marshal(telemetry.ResourcePtrToPB(span.Resource()))
		require.NoError(t, err)
		require.Equal(t, string(wantRes), string(rows[i].row.Resource), "span %d resource", i)
		wantScope, err := protojson.Marshal(telemetry.InstrumentationScopeToPB(span.InstrumentationScope()))
		require.NoError(t, err)
		require.Equal(t, string(wantScope), string(rows[i].row.InstrumentationScope), "span %d scope", i)
	}
}

// appendTestSpans writes spans straight to one client's DB, as the session
// span exporter does for each target on a span's route.
func appendTestSpans(ctx context.Context, ps *PubSub, clientID string, spans []sdktrace.ReadOnlySpan) error {
	var rows []clientdb.Span
	for _, r := range spanRows(spans) {
		if r.ok {
			rows = append(rows, r.row)
		}
	}
	return ps.appendSpans(ctx, clientID, rows)
}
