//go:build linux

package networkmetrics

import (
	"context"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	"go.opentelemetry.io/otel/metric"
)

func Register(_ context.Context, meter metric.Meter) (metric.Registration, error) {
	return register(meter, func() (sample, error) {
		snapshot, err := nettracer.SampleEngine()
		return sample{
			internalRX: snapshot.InternalRX,
			internalTX: snapshot.InternalTX,
			externalRX: snapshot.ExternalRX,
			externalTX: snapshot.ExternalTX,
		}, err
	})
}
