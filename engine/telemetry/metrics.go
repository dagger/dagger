package telemetry

import (
	"context"
	"fmt"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"dagger.io/dagger/telemetry"
)

func ReexportMetricsFromPB(ctx context.Context, exp sdkmetric.Exporter, req *colmetricspb.ExportMetricsServiceRequest) error {
	for _, reqResourceMetrics := range req.GetResourceMetrics() {
		resourceMetrics, err := telemetry.ResourceMetricsFromPB(reqResourceMetrics)
		if err != nil {
			return fmt.Errorf("failed to unmarshal resource metrics: %w", err)
		}
		if err := exp.Export(ctx, resourceMetrics); err != nil {
			return fmt.Errorf("failed to export resource metrics: %w", err)
		}
	}

	return nil
}
