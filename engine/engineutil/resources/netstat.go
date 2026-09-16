package resources

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/engine/telemetryattrs"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	telemetry "github.com/dagger/otel-go"
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
	rxBytes         metric.Int64Gauge
	networkRxBytes  metric.Int64Gauge
	rxPackets       metric.Int64Gauge
	rxDropped       metric.Int64Gauge
	txBytes         metric.Int64Gauge
	networkTxBytes  metric.Int64Gauge
	txPackets       metric.Int64Gauge
	txDropped       metric.Int64Gauge
	internalRxBytes metric.Int64Gauge
	internalTxBytes metric.Int64Gauge
	externalRxBytes metric.Int64Gauge
	externalTxBytes metric.Int64Gauge
}

type netNSSample struct {
	rxBytes         int64GaugeSample
	networkRxBytes  int64GaugeSample
	rxDropped       int64GaugeSample
	rxPackets       int64GaugeSample
	txBytes         int64GaugeSample
	networkTxBytes  int64GaugeSample
	txDropped       int64GaugeSample
	txPackets       int64GaugeSample
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
	if s.rxBytes, err = meter.Int64Gauge(
		telemetry.NetstatRxBytes,
		metric.WithDescription("Total number of bytes received over the network"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create rx bytes gauge: %w", err)
	}
	if s.networkRxBytes, err = meter.Int64Gauge(
		telemetryattrs.NetworkRxBytes,
		metric.WithDescription("Total number of bytes received by the operation"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network rx bytes gauge: %w", err)
	}
	if s.rxDropped, err = meter.Int64Gauge(
		telemetry.NetstatRxDropped,
		metric.WithDescription("Total number of received packets dropped"),
		metric.WithUnit("packets"),
	); err != nil {
		return nil, fmt.Errorf("failed to create rx dropped gauge: %w", err)
	}
	if s.txBytes, err = meter.Int64Gauge(
		telemetry.NetstatTxBytes,
		metric.WithDescription("Total number of bytes transmitted over the network"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create tx bytes gauge: %w", err)
	}
	if s.networkTxBytes, err = meter.Int64Gauge(
		telemetryattrs.NetworkTxBytes,
		metric.WithDescription("Total number of bytes transmitted by the operation"),
		metric.WithUnit("bytes"),
	); err != nil {
		return nil, fmt.Errorf("failed to create network tx bytes gauge: %w", err)
	}
	if s.txDropped, err = meter.Int64Gauge(
		telemetry.NetstatTxDropped,
		metric.WithDescription("Total number of transmitted packets dropped"),
		metric.WithUnit("packets"),
	); err != nil {
		return nil, fmt.Errorf("failed to create tx dropped gauge: %w", err)
	}
	if s.rxPackets, err = meter.Int64Gauge(
		telemetry.NetstatRxPackets,
		metric.WithDescription("Total number of packets received over the network"),
		metric.WithUnit("packets"),
	); err != nil {
		return nil, fmt.Errorf("failed to create rx packets gauge: %w", err)
	}
	if s.txPackets, err = meter.Int64Gauge(
		telemetry.NetstatTxPackets,
		metric.WithDescription("Total number of packets transmitted over the network"),
		metric.WithUnit("packets"),
	); err != nil {
		return nil, fmt.Errorf("failed to create tx packets gauge: %w", err)
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
		rxBytes:         newInt64GaugeSample(s.rxBytes, s.commonAttrs),
		networkRxBytes:  newInt64GaugeSample(s.networkRxBytes, s.commonAttrs),
		rxPackets:       newInt64GaugeSample(s.rxPackets, s.commonAttrs),
		rxDropped:       newInt64GaugeSample(s.rxDropped, s.commonAttrs),
		txBytes:         newInt64GaugeSample(s.txBytes, s.commonAttrs),
		networkTxBytes:  newInt64GaugeSample(s.networkTxBytes, s.commonAttrs),
		txPackets:       newInt64GaugeSample(s.txPackets, s.commonAttrs),
		txDropped:       newInt64GaugeSample(s.txDropped, s.commonAttrs),
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
	sample.rxBytes.add(hostRXBytes)
	// The sampled interface is the host side of the container veth, so its
	// direction is opposite to the operation's point of view used by the
	// canonical network metrics.
	sample.networkRxBytes.add(hostTXBytes)
	sample.rxPackets.add(bkSample.RxPackets - s.baselineSample.RxPackets)
	sample.rxDropped.add(bkSample.RxDropped - s.baselineSample.RxDropped)
	sample.txBytes.add(hostTXBytes)
	sample.networkTxBytes.add(hostRXBytes)
	sample.txPackets.add(bkSample.TxPackets - s.baselineSample.TxPackets)
	sample.txDropped.add(bkSample.TxDropped - s.baselineSample.TxDropped)
	if bkSample.ScopeSupported {
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

	sample.rxBytes.record(ctx)
	sample.networkRxBytes.record(ctx)
	sample.rxDropped.record(ctx)
	sample.txBytes.record(ctx)
	sample.networkTxBytes.record(ctx)
	sample.txDropped.record(ctx)
	sample.rxPackets.record(ctx)
	sample.txPackets.record(ctx)
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
