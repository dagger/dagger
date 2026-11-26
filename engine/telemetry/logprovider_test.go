package telemetry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type shutdownCountingFailingLogExporter struct {
	failingLogExporter
	shutdowns atomic.Int32
}

func (e *shutdownCountingFailingLogExporter) Shutdown(context.Context) error {
	e.shutdowns.Add(1)
	return nil
}

// An emit waiting on a full BlockingLogProcessor holds the provider's
// shutdown past its context, so the provider never shuts the processor down.
// ShutdownLoggerProvider does: the emit returns, and the processor's worker
// stops and shuts its exporter down.
func TestShutdownLoggerProviderShutsProcessorsDownPastDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	exporter := &shutdownCountingFailingLogExporter{}
	processor := NewBlockingLogProcessor(exporter, 1, 1, 0)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(processor))
	logger := provider.Logger("test")
	emitted := make(chan struct{})
	go func() {
		defer close(emitted)
		// The first record is retried forever, the second fills the queue,
		// and the third waits for space.
		for i := range 3 {
			var rec log.Record
			rec.SetBody(attribute.IntValue(i))
			logger.Emit(ctx, rec)
		}
	}()
	require.Eventually(t, func() bool {
		processor.mu.Lock()
		defer processor.mu.Unlock()
		return len(processor.queue) == processor.limit
	}, 10*time.Second, time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer shutdownCancel()
	require.ErrorIs(t, ShutdownLoggerProvider(shutdownCtx, provider, processor), context.DeadlineExceeded)
	select {
	case <-emitted:
	case <-ctx.Done():
		t.Fatal("the waiting emit was not released")
	}
	require.Eventually(t, func() bool { return exporter.shutdowns.Load() == 1 }, 10*time.Second, time.Millisecond,
		"the processor's exporter was not shut down")
}
