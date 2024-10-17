package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	// OTel metric for number of bytes read from disk by a container, as parsed from its cgroup
	IOStatDiskReadBytes = "dagger.io/metrics.iostat.disk.readbytes"

	// OTel metric for number of bytes written to disk by a container, as parsed from its cgroup
	IOStatDiskWriteBytes = "dagger.io/metrics.iostat.disk.writebytes"

	// OTel metric for number of microseconds SOME tasks in a cgroup were stalled on IO
	IOStatPressureSomeTotal = "dagger.io/metrics.iostat.pressure.some.total"

	// OTel metric units should be in UCUM format
	// https://unitsofmeasure.org/ucum

	// Bytes unit for OTel metrics
	ByteUnitName = "byte"

	// Microseconds unit for OTel metrics
	MicrosecondUnitName = "us"
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
