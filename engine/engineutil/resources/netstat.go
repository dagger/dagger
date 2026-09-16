package resources

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine/telemetryattrs"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type BKNetworkSampler interface {
	Sample() (*resourcestypes.NetworkSample, error)
}

type netNSSampler struct {
	netNS           BKNetworkSampler
	meter           metric.Meter
	commonAttrs     attribute.Set
	baselineSample  *resourcestypes.NetworkSample
	networkRxBytes  metric.Int64Gauge
	networkTxBytes  metric.Int64Gauge
	internalRxBytes metric.Int64Gauge
	internalTxBytes metric.Int64Gauge
	externalRxBytes metric.Int64Gauge
	externalTxBytes metric.Int64Gauge
}

type netNSSample struct {
	networkRxBytes  int64GaugeSample
	networkTxBytes  int64GaugeSample
	internalRxBytes int64GaugeSample
	internalTxBytes int64GaugeSample
	externalRxBytes int64GaugeSample
	externalTxBytes int64GaugeSample
}

func newNetNSSampler(netNS BKNetworkSampler, meter metric.Meter, commonAttrs attribute.Set) (*netNSSampler, error) {
	s := &netNSSampler{
		netNS:       netNS,
		meter:       meter,
		commonAttrs: commonAttrs,
	}

	var err error
	if s.networkRxBytes, err = meter.Int64Gauge(
		telemetryattrs.NetworkRxBytes,
		metric.WithDescription("Total number of bytes received by the operation"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network rx bytes gauge: %w", err)
	}
	if s.networkTxBytes, err = meter.Int64Gauge(
		telemetryattrs.NetworkTxBytes,
		metric.WithDescription("Total number of bytes transmitted by the operation"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network tx bytes gauge: %w", err)
	}
	for _, scoped := range []struct {
		name        string
		description string
		dst         *metric.Int64Gauge
	}{
		{telemetryattrs.NetworkInternalRxBytes, "Bytes received from Dagger-managed networks", &s.internalRxBytes},
		{telemetryattrs.NetworkInternalTxBytes, "Bytes transmitted to Dagger-managed networks", &s.internalTxBytes},
		{telemetryattrs.NetworkExternalRxBytes, "Bytes received from outside Dagger-managed networks", &s.externalRxBytes},
		{telemetryattrs.NetworkExternalTxBytes, "Bytes transmitted outside Dagger-managed networks", &s.externalTxBytes},
	} {
		*scoped.dst, err = meter.Int64Gauge(scoped.name, metric.WithDescription(scoped.description), metric.WithUnit("bytes"))
		if err != nil {
			return nil, fmt.Errorf("failed to create %s gauge: %w", scoped.name, err)
		}
	}

	s.baselineSample, err = s.netNS.Sample()
	if err != nil {
		return nil, fmt.Errorf("failed to baseline sample bk netNS: %w", err)
	}
	s.baselineSample = normalizeNetworkSample(s.baselineSample)

	return s, nil
}

func (s *netNSSampler) sample(ctx context.Context) error {
	sample := netNSSample{
		networkRxBytes:  newInt64GaugeSample(s.networkRxBytes, s.commonAttrs),
		networkTxBytes:  newInt64GaugeSample(s.networkTxBytes, s.commonAttrs),
		internalRxBytes: newInt64GaugeSample(s.internalRxBytes, s.commonAttrs),
		internalTxBytes: newInt64GaugeSample(s.internalTxBytes, s.commonAttrs),
		externalRxBytes: newInt64GaugeSample(s.externalRxBytes, s.commonAttrs),
		externalTxBytes: newInt64GaugeSample(s.externalTxBytes, s.commonAttrs),
	}

	bkSample, err := s.netNS.Sample()
	if err != nil {
		return fmt.Errorf("failed to sample bk netNS: %w", err)
	}
	bkSample = normalizeNetworkSample(bkSample)

	hostRXBytes := bkSample.RxBytes - s.baselineSample.RxBytes
	hostTXBytes := bkSample.TxBytes - s.baselineSample.TxBytes
	// The sampled interface is the host side of the container veth, so its
	// direction is opposite to the operation's point of view used by the
	// canonical network metrics.
	sample.networkRxBytes.add(hostTXBytes)
	sample.networkTxBytes.add(hostRXBytes)
	if s.baselineSample.ScopeSupported && bkSample.ScopeSupported {
		sample.internalRxBytes.add(bkSample.InternalRxBytes - s.baselineSample.InternalRxBytes)
		sample.internalTxBytes.add(bkSample.InternalTxBytes - s.baselineSample.InternalTxBytes)
		sample.externalRxBytes.add(bkSample.ExternalRxBytes - s.baselineSample.ExternalRxBytes)
		sample.externalTxBytes.add(bkSample.ExternalTxBytes - s.baselineSample.ExternalTxBytes)
	} else {
		// Anything not positively identified as Dagger-internal is external.
		// This also makes TCX load/attach failures fail closed for accounting
		// without reintroducing a classic-TC attachment fallback.
		sample.externalRxBytes.add(hostTXBytes)
		sample.externalTxBytes.add(hostRXBytes)
	}

	sample.networkRxBytes.record(ctx)
	sample.networkTxBytes.record(ctx)
	sample.internalRxBytes.record(ctx)
	sample.internalTxBytes.record(ctx)
	sample.externalRxBytes.record(ctx)
	sample.externalTxBytes.record(ctx)

	return nil
}

// Some providers (e.g. none/host) report network stats as nil rather than zero values.
func normalizeNetworkSample(sample *resourcestypes.NetworkSample) *resourcestypes.NetworkSample {
	if sample == nil {
		return &resourcestypes.NetworkSample{}
	}
	return sample
}
