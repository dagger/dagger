package idtui

import (
	"context"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// The logs frontend prints an internal span's logs only when internal spans
// are shown (-vvv), or when the span failed, as the other frontends show the
// span itself.
func TestLogsFrontendHidesInternalSpanLogs(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	rootID := prettyTestSpanID(1)
	execID := prettyTestSpanID(2)
	internalID := prettyTestSpanID(3)
	failedID := prettyTestSpanID(4)
	start := time.Unix(100, 0)
	snapshots := []dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "root", StartTime: start, EndTime: start.Add(time.Second)},
		{ID: execID, TraceID: prettyTestTraceID(), ParentID: rootID, Name: "exec", StartTime: start, EndTime: start.Add(time.Second)},
		{ID: internalID, TraceID: prettyTestTraceID(), ParentID: execID, Name: "internal detail", Internal: true, StartTime: start, EndTime: start.Add(time.Second)},
		{
			ID: failedID, TraceID: prettyTestTraceID(), ParentID: execID, Name: "internal failure", Internal: true, StartTime: start, EndTime: start.Add(time.Second),
			Status: sdktrace.Status{Code: codes.Error, Description: "boom"},
		},
	}
	records := []sdklog.Record{
		frontendTestLogRecord(execID.SpanID, otellog.StringValue("exec output\n")),
		frontendTestLogRecord(internalID.SpanID, otellog.StringValue("internal line\n")),
		frontendTestLogRecord(failedID.SpanID, otellog.StringValue("failure line\n")),
	}

	for _, tc := range []struct {
		name      string
		verbosity int
		internal  bool
	}{
		{name: "default", verbosity: dagui.ShowCompletedVerbosity},
		{name: "vvv", verbosity: dagui.ShowInternalVerbosity, internal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output strings.Builder
			fe := NewLogs(&output).(*frontendLogs)
			fe.opts.Verbosity = tc.verbosity
			fe.db.ImportSnapshots(snapshots)
			require.NoError(t, fe.LogExporter().Export(context.Background(), records))

			got := output.String()
			require.Contains(t, got, "exec output")
			require.Contains(t, got, "failure line")
			if tc.internal {
				require.Contains(t, got, "internal detail")
				require.Contains(t, got, "internal line")
			} else {
				require.NotContains(t, got, "internal detail")
				require.NotContains(t, got, "internal line")
			}
		})
	}
}

// The dots frontend prints no dot for an internal span unless internal spans
// are shown.
func TestDotsFrontendHidesInternalSpans(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	start := time.Unix(100, 0)
	traceID := trace.TraceID{1}
	span := func(id, parent byte, name string, attrs ...attribute.KeyValue) sdktrace.ReadOnlySpan {
		stub := tracetest.SpanStub{
			Name:        name,
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{id}}),
			StartTime:   start,
			EndTime:     start.Add(time.Second),
			Attributes:  attrs,
		}
		if parent != 0 {
			stub.Parent = trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: trace.SpanID{parent}})
		}
		return stub.Snapshot()
	}
	spans := []sdktrace.ReadOnlySpan{
		span(1, 0, "root"),
		span(2, 1, "exec"),
		span(3, 2, "internal detail", attribute.Bool(telemetry.UIInternalAttr, true)),
	}

	for _, tc := range []struct {
		name      string
		verbosity int
		dots      int
	}{
		{name: "default", verbosity: dagui.ShowCompletedVerbosity, dots: 2},
		{name: "vvv", verbosity: dagui.ShowInternalVerbosity, dots: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output strings.Builder
			fe := NewDots(&output).(*frontendDots)
			fe.opts.Verbosity = tc.verbosity
			require.NoError(t, fe.SpanExporter().ExportSpans(context.Background(), spans))
			require.Equal(t, tc.dots, strings.Count(stripANSITest(output.String()), "."))
		})
	}
}
