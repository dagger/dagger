package telemetry

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// SharedMetricExporter wraps a metric exporter owned elsewhere, for example
// by a session while each client owns a periodic reader over it: a reader's
// shutdown shuts its exporter down, so Shutdown is a no-op here and the owner
// shuts the underlying exporter down itself.
type SharedMetricExporter struct {
	sdkmetric.Exporter
}

func (SharedMetricExporter) Shutdown(context.Context) error { return nil }
