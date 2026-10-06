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
	netNS            BKNetworkSampler
	meter            metric.Meter
	commonAttrs      attribute.Set
	baselineSample   *resourcestypes.NetworkSample
	networkRxBytes   metric.Int64Gauge
	networkTxBytes   metric.Int64Gauge
	internalRxBytes  metric.Int64Gauge
	internalTxBytes  metric.Int64Gauge
	externalRxBytes  metric.Int64Gauge
	externalTxBytes  metric.Int64Gauge
	networkAvailable metric.Int64Gauge
}

type netNSSample struct {
	networkRxBytes   int64GaugeSample
	networkTxBytes   int64GaugeSample
	internalRxBytes  int64GaugeSample
	internalTxBytes  int64GaugeSample
	externalRxBytes  int64GaugeSample
	externalTxBytes  int64GaugeSample
	networkAvailable int64GaugeSample
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
		metric.WithDescription("Network-layer bytes received by this operation"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network rx bytes gauge: %w", err)
	}
	if s.networkTxBytes, err = meter.Int64Gauge(
		telemetryattrs.NetworkTxBytes,
		metric.WithDescription("Network-layer bytes transmitted by this operation"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network tx bytes gauge: %w", err)
	}
	for _, scoped := range []struct {
		name        string
		description string
		dst         *metric.Int64Gauge
	}{
		{telemetryattrs.NetworkInternalRxBytes, "Network-layer bytes received from Dagger-managed networks", &s.internalRxBytes},
		{telemetryattrs.NetworkInternalTxBytes, "Network-layer bytes transmitted to Dagger-managed networks", &s.internalTxBytes},
		{telemetryattrs.NetworkExternalRxBytes, "Network-layer bytes received from outside Dagger-managed networks", &s.externalRxBytes},
		{telemetryattrs.NetworkExternalTxBytes, "Network-layer bytes transmitted outside Dagger-managed networks", &s.externalTxBytes},
	} {
		*scoped.dst, err = meter.Int64Gauge(scoped.name, metric.WithDescription(scoped.description), metric.WithUnit("bytes"))
		if err != nil {
			return nil, fmt.Errorf("failed to create %s gauge: %w", scoped.name, err)
		}
	}
	if s.networkAvailable, err = meter.Int64Gauge(
		telemetryattrs.NetworkAvailable,
		metric.WithDescription("Whether attributed eBPF network accounting is available"),
		metric.WithUnit("1"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network availability gauge: %w", err)
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
		networkRxBytes:   newInt64GaugeSample(s.networkRxBytes, s.commonAttrs),
		networkTxBytes:   newInt64GaugeSample(s.networkTxBytes, s.commonAttrs),
		internalRxBytes:  newInt64GaugeSample(s.internalRxBytes, s.commonAttrs),
		internalTxBytes:  newInt64GaugeSample(s.internalTxBytes, s.commonAttrs),
		externalRxBytes:  newInt64GaugeSample(s.externalRxBytes, s.commonAttrs),
		externalTxBytes:  newInt64GaugeSample(s.externalTxBytes, s.commonAttrs),
		networkAvailable: newInt64GaugeSample(s.networkAvailable, s.commonAttrs),
	}

	bkSample, err := s.netNS.Sample()
	if err != nil {
		return fmt.Errorf("failed to sample bk netNS: %w", err)
	}
	bkSample = normalizeNetworkSample(bkSample)

	if s.baselineSample.ScopeSupported && bkSample.ScopeSupported {
		sample.networkAvailable.add(1)
		internalRX := bkSample.InternalRxBytes - s.baselineSample.InternalRxBytes
		internalTX := bkSample.InternalTxBytes - s.baselineSample.InternalTxBytes
		externalRX := bkSample.ExternalRxBytes - s.baselineSample.ExternalRxBytes
		externalTX := bkSample.ExternalTxBytes - s.baselineSample.ExternalTxBytes
		sample.networkRxBytes.add(internalRX + externalRX)
		sample.networkTxBytes.add(internalTX + externalTX)
		sample.internalRxBytes.add(internalRX)
		sample.internalTxBytes.add(internalTX)
		sample.externalRxBytes.add(externalRX)
		sample.externalTxBytes.add(externalTX)
	} else {
		sample.networkAvailable.add(0)
	}

	sample.networkRxBytes.record(ctx)
	sample.networkTxBytes.record(ctx)
	sample.internalRxBytes.record(ctx)
	sample.internalTxBytes.record(ctx)
	sample.externalRxBytes.record(ctx)
	sample.externalTxBytes.record(ctx)
	sample.networkAvailable.record(ctx)

	return nil
}

// Some providers (e.g. none/host) report network stats as nil rather than zero values.
func normalizeNetworkSample(sample *resourcestypes.NetworkSample) *resourcestypes.NetworkSample {
	if sample == nil {
		return &resourcestypes.NetworkSample{}
	}
	return sample
}
