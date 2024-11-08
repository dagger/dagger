package resources

import (
	"context"
	"fmt"

	"dagger.io/dagger/telemetry"
	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type BKNetworkSampler interface {
	Sample() (*resourcestypes.NetworkSample, error)
}

type netNSSampler struct {
	netNS       BKNetworkSampler
	meter       metric.Meter
	commonAttrs attribute.Set
	rxBytes     metric.Int64Gauge
	rxPackets   metric.Int64Gauge
	rxDropped   metric.Int64Gauge
	txBytes     metric.Int64Gauge
	txPackets   metric.Int64Gauge
	txDropped   metric.Int64Gauge
}

type netNSSample struct {
	rxBytes   int64GaugeSample
	rxDropped int64GaugeSample
	rxPackets int64GaugeSample
	txBytes   int64GaugeSample
	txDropped int64GaugeSample
	txPackets int64GaugeSample
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

	return s, nil
}

func (s *netNSSampler) sample(ctx context.Context) error {
	sample := netNSSample{
		rxBytes:   newInt64GaugeSample(s.rxBytes, s.commonAttrs),
		rxPackets: newInt64GaugeSample(s.rxPackets, s.commonAttrs),
		rxDropped: newInt64GaugeSample(s.rxDropped, s.commonAttrs),
		txBytes:   newInt64GaugeSample(s.txBytes, s.commonAttrs),
		txPackets: newInt64GaugeSample(s.txPackets, s.commonAttrs),
		txDropped: newInt64GaugeSample(s.txDropped, s.commonAttrs),
	}

	bkSample, err := s.netNS.Sample()
	if err != nil {
		return fmt.Errorf("failed to sample bk netNS: %w", err)
	}

	sample.rxBytes.add(bkSample.RxBytes)
	sample.rxPackets.add(bkSample.RxPackets)
	sample.rxDropped.add(bkSample.RxDropped)
	sample.txBytes.add(bkSample.TxBytes)
	sample.txPackets.add(bkSample.TxPackets)
	sample.txDropped.add(bkSample.TxDropped)

	sample.rxBytes.record(ctx)
	sample.rxDropped.record(ctx)
	sample.txBytes.record(ctx)
	sample.txDropped.record(ctx)
	sample.rxPackets.record(ctx)
	sample.txPackets.record(ctx)

	return nil
}
