package telemetry

import (
	"context"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func normValueless(key string) *commonpb.KeyValue { return &commonpb.KeyValue{Key: key} }

func normString(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}

func attrByKey(t *testing.T, attrs []attribute.KeyValue, key string) attribute.Value {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value
		}
	}
	t.Fatalf("attribute %q not found in %v", key, attrs)
	return attribute.Value{}
}

// Every optional field otel-go's span conversion dereferences, left out:
// after normalization the spans convert and read back without panicking.
func TestNormalizeTraceRequest(t *testing.T) {
	req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{
		nil,
		{
			// No Resource.
			ScopeSpans: []*tracepb.ScopeSpans{nil, {
				Scope: &commonpb.InstrumentationScope{Name: "scope", Attributes: []*commonpb.KeyValue{normValueless("scope.valueless")}},
				Spans: []*tracepb.Span{nil, {
					TraceId: []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
					SpanId:  []byte{1, 0, 0, 0, 0, 0, 0, 0},
					Name:    "span",
					Attributes: []*commonpb.KeyValue{
						nil,
						normValueless("valueless"),
						{Key: "unset", Value: &commonpb.AnyValue{}},
						{Key: "array", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{
							ArrayValue: &commonpb.ArrayValue{Values: []*commonpb.AnyValue{normString("a"), nil}},
						}}},
					},
					Events: []*tracepb.Span_Event{nil, {Name: "event", Attributes: []*commonpb.KeyValue{normValueless("event.valueless")}}},
					Links:  []*tracepb.Span_Link{nil, {Attributes: []*commonpb.KeyValue{normValueless("link.valueless")}}},
				}},
			}},
		},
		{
			Resource:   nil,
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "bare"}}}},
		},
	}}

	NormalizeTraceRequest(req)
	spans := telemetry.SpansFromPB(req.GetResourceSpans())
	require.Len(t, spans, 2)

	span := spans[0]
	require.Equal(t, "span", span.Name())
	require.NotNil(t, span.Resource())
	require.Zero(t, span.Resource().Len())
	attrs := span.Attributes()
	require.Len(t, attrs, 3)
	require.Equal(t, "", attrByKey(t, attrs, "valueless").AsString())
	require.Equal(t, "", attrByKey(t, attrs, "unset").AsString())
	require.Equal(t, []string{"a", ""}, attrByKey(t, attrs, "array").AsStringSlice())
	require.Len(t, span.Events(), 1)
	require.Equal(t, "", attrByKey(t, span.Events()[0].Attributes, "event.valueless").AsString())
	require.Len(t, span.Links(), 1)
	require.Equal(t, "", attrByKey(t, span.Links()[0].Attributes, "link.valueless").AsString())

	// A converted span survives a round trip through the exporter path too.
	require.Equal(t, "bare", spans[1].Name())
	require.Len(t, telemetry.SpansToPB(spans), 1, "both spans share the empty resource")

	NormalizeTraceRequest(nil)
}

type normLogCapture struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (c *normLogCapture) Export(_ context.Context, records []sdklog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rec := range records {
		c.records = append(c.records, rec.Clone())
	}
	return nil
}
func (*normLogCapture) ForceFlush(context.Context) error { return nil }
func (*normLogCapture) Shutdown(context.Context) error   { return nil }

// A resourceless, bodiless record with valueless attributes is re-exported,
// not dropped and not panicked on.
func TestNormalizeLogsRequest(t *testing.T) {
	req := &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{
		nil,
		{
			// No Resource.
			ScopeLogs: []*logspb.ScopeLogs{nil, {
				Scope: &commonpb.InstrumentationScope{Name: "scope", Attributes: []*commonpb.KeyValue{normValueless("scope.valueless")}},
				LogRecords: []*logspb.LogRecord{
					nil,
					{
						// No Body.
						Attributes: []*commonpb.KeyValue{nil, normValueless("valueless")},
					},
					{
						Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{
							Values: []*commonpb.KeyValue{nil, normValueless("nested")},
						}}},
					},
					{
						Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{
							Values: []*commonpb.AnyValue{nil},
						}}},
					},
				},
			}},
		},
	}}

	NormalizeLogsRequest(req)
	capture := &normLogCapture{}
	require.NoError(t, telemetry.ReexportLogsFromPB(t.Context(), capture, req))
	require.Len(t, capture.records, 3)

	bodiless := capture.records[0]
	require.Equal(t, otellog.KindString, bodiless.Body().Kind())
	require.Equal(t, "", bodiless.Body().AsString())
	var attrs []otellog.KeyValue
	bodiless.WalkAttributes(func(kv otellog.KeyValue) bool {
		attrs = append(attrs, kv)
		return true
	})
	require.Len(t, attrs, 1)
	require.True(t, attrs[0].Equal(otellog.String("valueless", "")), "got %v", attrs[0])
	require.Zero(t, bodiless.Resource().Len())

	nested := capture.records[1].Body().AsMap()
	require.Len(t, nested, 1)
	require.True(t, nested[0].Equal(otellog.String("nested", "")), "got %v", nested[0])
	elems := capture.records[2].Body().AsSlice()
	require.Len(t, elems, 1)
	require.True(t, elems[0].Equal(otellog.StringValue("")), "got %v", elems[0])

	NormalizeLogsRequest(nil)
}

// A resourceless metric, an empty gauge and valueless data point attributes
// convert without panicking.
func TestNormalizeMetricsRequest(t *testing.T) {
	req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{
		nil,
		{
			// No Resource.
			ScopeMetrics: []*metricspb.ScopeMetrics{nil, {Metrics: []*metricspb.Metric{
				nil,
				{Name: "empty", Data: &metricspb.Metric_Gauge{}},
				{Name: "gauge", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{
					nil,
					{
						Attributes: []*commonpb.KeyValue{normValueless("valueless")},
						Exemplars:  []*metricspb.Exemplar{nil, {FilteredAttributes: []*commonpb.KeyValue{normValueless("filtered")}, Value: &metricspb.Exemplar_AsInt{AsInt: 1}}},
						Value:      &metricspb.NumberDataPoint_AsInt{AsInt: 1},
					},
				}}}},
			}}},
		},
	}}

	NormalizeMetricsRequest(req)
	require.Len(t, req.ResourceMetrics, 1)
	rm, err := telemetry.ResourceMetricsFromPB(req.ResourceMetrics[0])
	require.NoError(t, err)
	require.Zero(t, rm.Resource.Len())
	require.Len(t, rm.ScopeMetrics, 1)
	require.Len(t, rm.ScopeMetrics[0].Metrics, 2)

	NormalizeMetricsRequest(nil)
}

// The trace importer normalizes too: a resourceless request with a nil span
// entry imports, and its parentless span is still stamped passthrough.
func TestTraceImporterToleratesMalformedRequests(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	logs := &normLogCapture{}
	imp := NewTraceImporter(TraceImportSinks{Spans: spans, Logs: logs})

	require.NoError(t, imp.ImportSpans(t.Context(), &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{nil, {
			TraceId:           []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			SpanId:            []byte{1, 0, 0, 0, 0, 0, 0, 0},
			Name:              "root",
			StartTimeUnixNano: 1,
			EndTimeUnixNano:   2,
			Attributes:        []*commonpb.KeyValue{normValueless("valueless")},
		}}}},
	}}}))
	got := spans.GetSpans()
	require.Len(t, got, 1)
	require.True(t, attrByKey(t, got[0].Attributes, telemetry.UIPassthroughAttr).AsBool())

	require.NoError(t, imp.ImportLogs(t.Context(), &collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{}}}},
	}}}))
	require.Len(t, logs.records, 1)
}
