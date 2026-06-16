package server

import (
	"context"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type capturedLogs struct {
	records []sdklog.Record
}

func (c *capturedLogs) Export(_ context.Context, records []sdklog.Record) error {
	c.records = append(c.records, records...)
	return nil
}

func (c *capturedLogs) ForceFlush(context.Context) error { return nil }
func (c *capturedLogs) Shutdown(context.Context) error   { return nil }

func spanStub(spanID byte, internal bool, status codes.Code) tracetest.SpanStub {
	stub := tracetest.SpanStub{
		Name: "test-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1},
			SpanID:  trace.SpanID{spanID},
		}),
		Status: sdktrace.Status{Code: status},
	}
	if internal {
		stub.Attributes = []attribute.KeyValue{
			attribute.Bool(telemetry.UIInternalAttr, true),
		}
	}
	return stub
}

func logRecord(spanID byte, verbose bool) sdklog.Record {
	var rec sdklog.Record
	rec.SetSpanID(trace.SpanID{spanID})
	rec.SetBody(otellog.StringValue("hello"))
	if verbose {
		rec.SetAttributes(otellog.Bool(telemetry.LogsVerboseAttr, true))
	}
	return rec
}

func TestScaleOutTelemetryFilterSpans(t *testing.T) {
	ctx := context.Background()
	filter := newScaleOutTelemetryFilter()
	spanSink := tracetest.NewInMemoryExporter()
	spans := filter.Spans(spanSink)

	require.NoError(t, spans.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		spanStub(1, false, codes.Unset).Snapshot(), // ordinary span: kept
		spanStub(2, true, codes.Unset).Snapshot(),  // internal span: dropped
		spanStub(3, true, codes.Error).Snapshot(),  // internal but errored: kept
	}))

	kept := spanSink.GetSpans()
	require.Len(t, kept, 2)
	require.Equal(t, trace.SpanID{1}, kept[0].SpanContext.SpanID())
	require.Equal(t, trace.SpanID{3}, kept[1].SpanContext.SpanID())
}

func TestScaleOutTelemetryFilterLogs(t *testing.T) {
	ctx := context.Background()
	filter := newScaleOutTelemetryFilter()
	spanSink := tracetest.NewInMemoryExporter()
	logSink := &capturedLogs{}
	spans := filter.Spans(spanSink)
	logs := filter.Logs(logSink)

	require.NoError(t, spans.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		spanStub(1, false, codes.Unset).Snapshot(),
		spanStub(2, true, codes.Unset).Snapshot(),
	}))

	require.NoError(t, logs.Export(ctx, []sdklog.Record{
		logRecord(1, false), // log for kept span: kept
		logRecord(2, false), // log for dropped span: dropped
		logRecord(1, true),  // verbose log: dropped even for kept span
	}))

	require.Len(t, logSink.records, 1)
	require.Equal(t, trace.SpanID{1}, logSink.records[0].SpanID())
}

func TestScaleOutTelemetryFilterUndropsErroredSpans(t *testing.T) {
	ctx := context.Background()
	filter := newScaleOutTelemetryFilter()
	spanSink := tracetest.NewInMemoryExporter()
	logSink := &capturedLogs{}
	spans := filter.Spans(spanSink)
	logs := filter.Logs(logSink)

	// live telemetry exports spans in snapshots: first while running...
	require.NoError(t, spans.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		spanStub(2, true, codes.Unset).Snapshot(),
	}))
	require.Empty(t, spanSink.GetSpans())

	// ...then a final snapshot, here with an error: the span must come back
	require.NoError(t, spans.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		spanStub(2, true, codes.Error).Snapshot(),
	}))
	require.Len(t, spanSink.GetSpans(), 1)

	// and its logs must flow again too
	require.NoError(t, logs.Export(ctx, []sdklog.Record{logRecord(2, false)}))
	require.Len(t, logSink.records, 1)
}
