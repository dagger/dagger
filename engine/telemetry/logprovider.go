package telemetry

import (
	"context"
	"errors"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// ShutdownLoggerProvider shuts provider down within ctx, and shuts its
// processors down itself when the provider could not.
//
// OpenTelemetry's LoggerProvider.Shutdown waits for every emit in flight
// before it shuts its processors down, and once ctx has ended it returns
// without shutting them down at all. A processor whose emits can wait, such
// as a BlockingLogProcessor with its queue full, would keep its worker and
// its exporter for good. Shut down here, a processor releases its waiting
// emits and stops its worker even with ctx ended. processors are the ones
// the provider was made with; each must allow a second Shutdown, as the
// provider may have shut some of them down before ctx ended.
func ShutdownLoggerProvider(ctx context.Context, provider *sdklog.LoggerProvider, processors ...sdklog.Processor) error {
	err := provider.Shutdown(ctx)
	if err == nil || ctx.Err() == nil {
		return err
	}
	for _, processor := range processors {
		err = errors.Join(err, processor.Shutdown(ctx))
	}
	return err
}
