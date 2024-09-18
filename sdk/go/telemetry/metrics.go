package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type meterProviderKey struct{}

func WithMeterProvider(ctx context.Context, provider *sdkmetric.MeterProvider) context.Context {
	return context.WithValue(ctx, meterProviderKey{}, provider)
}

func MeterProvider(ctx context.Context) *sdkmetric.MeterProvider {
	meterProvider := sdkmetric.NewMeterProvider()
	if val := ctx.Value(meterProviderKey{}); val != nil {
		meterProvider = val.(*sdkmetric.MeterProvider)
	}
	return meterProvider
}

func Meter(ctx context.Context, name string) metric.Meter {
	return MeterProvider(ctx).Meter(name)
}
