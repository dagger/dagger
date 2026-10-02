//go:build !linux

package networkmetrics

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func Register(_ context.Context, _ metric.Meter) (metric.Registration, error) {
	return noop.Registration{}, nil
}
