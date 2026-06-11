package idtui

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func checkSpanStub(spanID byte, checkName string, start time.Time, end time.Time, status codes.Code) tracetest.SpanStub {
	return tracetest.SpanStub{
		Name: checkName,
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1},
			SpanID:  trace.SpanID{spanID},
		}),
		StartTime: start,
		EndTime:   end,
		Status:    sdktrace.Status{Code: status},
		Attributes: []attribute.KeyValue{
			attribute.String(telemetry.CheckNameAttr, checkName),
		},
	}
}

func TestReportHeartbeatLine(t *testing.T) {
	ctx := context.Background()
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.reportOnly = true

	now := time.Now()
	exp := fe.SpanExporter()
	require.NoError(t, exp.ExportSpans(ctx, []sdktrace.ReadOnlySpan{
		// still running: no end time
		checkSpanStub(1, "go:test", now.Add(-90*time.Second), time.Time{}, codes.Unset).Snapshot(),
		// passed
		checkSpanStub(2, "go:lint", now.Add(-2*time.Minute), now.Add(-time.Minute), codes.Unset).Snapshot(),
		// failed
		checkSpanStub(3, "docs:build", now.Add(-2*time.Minute), now.Add(-time.Minute), codes.Error).Snapshot(),
	}))

	line := fe.reportHeartbeatLine(2 * time.Minute)
	require.Contains(t, line, "2m0s elapsed")
	require.Contains(t, line, "checks: 2/3 done")
	require.Contains(t, line, "(1 failed)")
	require.Contains(t, line, "go:test")
}

func TestReportHeartbeatLineNoChecks(t *testing.T) {
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.reportOnly = true

	line := fe.reportHeartbeatLine(30 * time.Second)
	require.Contains(t, line, "30.0s elapsed")
	require.NotContains(t, line, "checks:")
}
