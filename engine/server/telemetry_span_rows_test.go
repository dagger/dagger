package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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

// The trace stream converts stored rows straight to OTLP. It must send what
// converting the rows' ReadOnly views did.
func TestSpanRowsToPBMatchesReadOnlyConversion(t *testing.T) {
	resA := resource.NewSchemaless(attribute.String("service.name", "a"), attribute.Int("pid", 1))
	resB := resource.NewWithAttributes("https://example.com/schema", attribute.String("service.name", "b"))
	ts, err := trace.ParseTraceState("k=v")
	require.NoError(t, err)
	attrs := []attribute.KeyValue{
		attribute.Bool("bool", true),
		attribute.Int64("int", -7),
		attribute.Float64("float", 1.5),
		attribute.String("string", "s"),
		attribute.String("empty", ""),
		attribute.BoolSlice("bools", []bool{true, false}),
		attribute.Int64Slice("ints", []int64{1, 2}),
		attribute.Float64Slice("floats", []float64{0.5}),
		attribute.StringSlice("strings", []string{"x", "y"}),
		attribute.StringSlice("none", nil),
	}
	start := time.Unix(100, 5)
	var spans []sdktrace.ReadOnlySpan
	for i, stub := range []tracetest.SpanStub{
		{Name: "attrs", Attributes: attrs, Resource: resA, InstrumentationScope: instrumentation.Scope{Name: "a"},
			SpanKind: trace.SpanKindInternal, EndTime: start.Add(time.Second)},
		{Name: "events", Resource: resA, SpanKind: trace.SpanKindServer,
			Events: []sdktrace.Event{
				{Name: "e1", Time: start.Add(time.Millisecond), Attributes: attrs[:3], DroppedAttributeCount: 2},
				{Name: "e2", Time: start.Add(2 * time.Millisecond)},
			},
			DroppedEvents: 1, DroppedAttributes: 3, EndTime: start.Add(time.Second)},
		{Name: "links", Resource: resB, InstrumentationScope: instrumentation.Scope{Name: "b", Version: "2"},
			SpanKind: trace.SpanKindClient,
			Links: []sdktrace.Link{
				{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: trace.TraceID{2}, SpanID: trace.SpanID{2}, TraceState: ts, Remote: true,
				}), Attributes: attrs[3:5], DroppedAttributeCount: 4},
				{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{3}, SpanID: trace.SpanID{3}})},
			},
			DroppedLinks: 5, EndTime: start.Add(time.Second)},
		{Name: "unended child", Resource: resB, SpanKind: trace.SpanKindProducer,
			Parent: trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, Remote: true})},
		{Name: "error", Resource: nil, SpanKind: trace.SpanKindConsumer,
			Status: sdktrace.Status{Code: codes.Error, Description: "boom"}, EndTime: start.Add(time.Second)},
		{Name: "ok", Resource: resA, Status: sdktrace.Status{Code: codes.Ok}, EndTime: start.Add(time.Second)},
		{Name: "ends before start", Resource: resA, EndTime: start.Add(-time.Second)},
	} {
		stub.SpanContext = trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{byte(10 + i)}, TraceState: ts,
			TraceFlags: trace.FlagsSampled,
		})
		stub.StartTime = start
		spans = append(spans, stub.Snapshot())
	}

	encoded := spanRows(spans)
	rows := make([]clientdb.Span, 0, len(encoded))
	views := make([]sdktrace.ReadOnlySpan, 0, len(encoded))
	for _, r := range encoded {
		require.True(t, r.ok)
		rows = append(rows, r.row)
	}
	for i := range rows {
		views = append(views, rows[i].ReadOnly())
	}

	byResource := func(rss []*otlptracev1.ResourceSpans) map[string]*otlptracev1.ResourceSpans {
		out := map[string]*otlptracev1.ResourceSpans{}
		for _, rs := range rss {
			key, err := protojson.Marshal(rs.GetResource())
			require.NoError(t, err)
			require.NotContains(t, out, string(key))
			out[string(key)] = rs
		}
		return out
	}
	want := byResource(telemetry.SpansToPB(views))
	got := byResource(clientdb.SpansToPB(rows))
	require.Len(t, got, 3)
	require.Equal(t, len(want), len(got))
	for key, rs := range want {
		require.Contains(t, got, key)
		require.True(t, proto.Equal(rs, got[key]), "resource %s:\nwant %s\ngot  %s", key, protojson.Format(rs), protojson.Format(got[key]))
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
