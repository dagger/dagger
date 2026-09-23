package telemetry

import (
	"context"
	"sync"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud/auth"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var (
	configuredCloudSpanExporter    sdktrace.SpanExporter
	configuredCloudLogsExporter    sdklog.Exporter
	configuredCloudMetricsExporter sdkmetric.Exporter
	configuredCloudTelemetry       bool
	configuredCloudExportersOnce   sync.Once
)

// ConfiguredCloudExporters returns this process's Dagger Cloud exporters,
// built once by NewCloudExporters from the process's Cloud credential:
// DAGGER_CLOUD_TOKEN, or the `dagger login` token with its current
// organization, refreshed and persisted as it expires.
func ConfiguredCloudExporters(ctx context.Context) (sdktrace.SpanExporter, sdklog.Exporter, sdkmetric.Exporter, bool) {
	configuredCloudExportersOnce.Do(func() {
		cloudAuth, err := auth.GetCloudAuth(ctx)
		if err != nil {
			slog.Warn("failed to get cloud auth", "error", err)
			return
		}
		if cloudAuth == nil || cloudAuth.Token == nil {
			return
		}
		if !cloudAuthHasStaticHeader(cloudAuth) && cloudAuth.Org == nil {
			// A user's login token names no organization by itself.
			return
		}

		spans, logs, metrics, err := NewCloudExporters(ctx, cloudAuth, auth.Token, "")
		if err != nil {
			slog.Warn("failed to configure cloud exporters", "error", err)
			return
		}
		configuredCloudSpanExporter = spans
		configuredCloudLogsExporter = logs
		configuredCloudMetricsExporter = metrics
		configuredCloudTelemetry = true
	})

	return configuredCloudSpanExporter,
		configuredCloudLogsExporter,
		configuredCloudMetricsExporter,
		configuredCloudTelemetry
}
