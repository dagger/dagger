package daggercmd

import (
	"context"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestArtifactFailureOutputUsesCurrentExecution(t *testing.T) {
	recorder := new(sessionTitleLogRecorder)
	logger := sdklog.NewLoggerProvider(sdklog.WithProcessor(recorder))
	tracer := sdktrace.NewTracerProvider()
	t.Cleanup(func() { require.NoError(t, logger.Shutdown(context.Background())) })
	t.Cleanup(func() { require.NoError(t, tracer.Shutdown(context.Background())) })
	failure := &artifactExecutionError{Message: "exit code: 3", Values: []struct{ Name, Value string }{
		{"stderr", `"boom\n"`},
		{"stdout", `"before failure\n"`},
	}}
	// A cached failure must report its output again on the new execution span.
	for range 2 {
		ctx, span := tracer.Tracer("test").Start(t.Context(), "generator")
		ctx = telemetry.WithLoggerProvider(ctx, logger)
		before := len(recorder.records)
		reportArtifactFailureOutput(ctx, failure)
		span.End()
		var output strings.Builder
		for _, record := range recorder.records[before:] {
			require.Equal(t, span.SpanContext().SpanID(), record.SpanID())
			output.WriteString(record.Body().AsString())
		}
		require.Contains(t, output.String(), "boom")
		require.Contains(t, output.String(), "before failure")
	}
}
