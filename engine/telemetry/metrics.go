package telemetry

import (
	"context"
	"fmt"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger/telemetry"
)

func ReexportMetricsFromPB(ctx context.Context, exps []sdkmetric.Exporter, req *colmetricspb.ExportMetricsServiceRequest) error {
	for _, reqResourceMetrics := range req.GetResourceMetrics() {
		var eg errgroup.Group
		for _, exp := range exps {
			eg.Go(func() error {
				resourceMetrics, err := telemetry.ResourceMetricsFromPB(reqResourceMetrics)
				if err != nil {
					return fmt.Errorf("failed to unmarshal resource metrics: %w", err)
				}
				err = exp.Export(ctx, resourceMetrics)
				if err != nil {
					return fmt.Errorf("failed to export resource metrics: %w", err)
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return err
		}
	}

	return nil
}
