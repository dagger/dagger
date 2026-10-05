package networkmetrics

import (
	"context"
	"math"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	BytesName     = "dagger.engine.network.io"
	AvailableName = "dagger.engine.network.available"
)

type sample struct {
	internalRX uint64
	internalTX uint64
	externalRX uint64
	externalTX uint64
}

type sampler func() (sample, error)

type instruments struct {
	bytes     metric.Int64ObservableCounter
	available metric.Int64ObservableGauge
}

func register(meter metric.Meter, sampleNetwork sampler) (metric.Registration, error) {
	bytes, err := meter.Int64ObservableCounter(BytesName,
		metric.WithUnit("By"),
		metric.WithDescription("Cumulative network-layer bytes for the engine process and its subprocess operations, excluding executor workloads."),
	)
	if err != nil {
		return nil, err
	}
	available, err := meter.Int64ObservableGauge(AvailableName,
		metric.WithUnit("1"),
		metric.WithDescription("1 when exact engine cgroup network accounting is available; otherwise 0."),
	)
	if err != nil {
		return nil, err
	}
	inst := instruments{bytes: bytes, available: available}
	return meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		snapshot, err := sampleNetwork()
		if err != nil {
			observer.ObserveInt64(inst.available, 0)
			return nil //nolint:nilerr // unavailability is reported by the gauge
		}
		observer.ObserveInt64(inst.available, 1)
		for _, value := range []struct {
			scope     string
			direction string
			bytes     uint64
		}{
			{"internal", "receive", snapshot.internalRX},
			{"internal", "transmit", snapshot.internalTX},
			{"external", "receive", snapshot.externalRX},
			{"external", "transmit", snapshot.externalTX},
		} {
			observer.ObserveInt64(inst.bytes, clamp(value.bytes), metric.WithAttributes(
				attribute.String("network.scope", value.scope),
				attribute.String("network.io.direction", value.direction),
			))
		}
		return nil
	}, inst.bytes, inst.available)
}

func clamp(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}
