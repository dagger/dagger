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

	// OTel metric for number of microseconds SOME tasks in a cgroup were stalled on IO due to resource contention
	IOStatPressureSomeTotal = "dagger.io/metrics.iostat.pressure.some.total"

	// OTel metric for number of microseconds of all CPU usage of a container, as parsed from its cgroup
	CPUStatUsage = "dagger.io/metrics.cpustat.usage"

	// OTel metric for number of microseconds of CPU time spent in user mode by a container, as parsed from its cgroup
	CPUStatUser = "dagger.io/metrics.cpustat.user"

	// OTel metric for number of microseconds of CPU time spent in system mode by a container, as parsed from its cgroup
	CPUStatSystem = "dagger.io/metrics.cpustat.system"

	// OTel metric for number of microseconds SOME tasks in a cgroup were stalled on CPU due to resource contention
	CPUStatPressureSomeTotal = "dagger.io/metrics.cpustat.pressure.some.total"

	// OTel metric for number of microseconds ALL tasks in a cgroup were stalled on CPU due to resource contention
	CPUStatPressureFullTotal = "dagger.io/metrics.cpustat.pressure.full.total"

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
