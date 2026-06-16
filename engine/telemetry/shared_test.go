package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// A session shares its cloud exporters across every client's providers, each
// of which shuts its processors down when that client goes away. One client
// shutting down must not stop exports for the rest of the session. Only spans
// are exercised; the log and metric wrappers are the same one-line override.
func TestSharedExporterSurvivesConsumerShutdown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	underlying := &recordingSpanExporter{}
	shared := SharedSpanExporter{SpanExporter: underlying}

	require.NoError(t, shared.Shutdown(ctx)) // one client's provider shuts down
	require.NoError(t, shared.ExportSpans(ctx, nil))
	require.False(t, underlying.shutdown, "shared exporter forwarded Shutdown to the underlying exporter")
	require.Equal(t, 1, underlying.exports, "shared exporter stopped exporting after a consumer shutdown")
}

type recordingSpanExporter struct {
	exports  int
	shutdown bool
}

func (r *recordingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	r.exports++
	return nil
}

func (r *recordingSpanExporter) Shutdown(context.Context) error {
	r.shutdown = true
	return nil
}
