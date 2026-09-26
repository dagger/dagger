package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/errgroup"
)

const (
	defaultMountpoint = "/sys/fs/cgroup"
)

type Sampler struct {
	cgroupPath  string
	commonAttrs attribute.Set

	ioStat     *ioStatSampler
	ioPressure *ioPressureSampler

	cpuStat     *cpuStatSampler
	cpuPressure *cpuPressureSampler

	memoryCurrent *memoryCurrentSampler
	memoryPeak    *memoryPeakSampler

	netNS     *netNSSampler
	available metric.Int64Gauge
}

func NewSampler(
	cgroupNSSubpath string,
	netNS BKNetworkSampler,
	meter metric.Meter,
	commonAttrs attribute.Set,
) (*Sampler, error) {
	s := &Sampler{
		cgroupPath:  filepath.Join(defaultMountpoint, cgroupNSSubpath),
		commonAttrs: commonAttrs,
	}
	var err error
	s.available, err = meter.Int64Gauge("dagger.resource.sample.available",
		metric.WithUnit("1"),
		metric.WithDescription("1 when a resource source was sampled; 0 when it was unavailable or invalid."))
	if err != nil {
		return nil, err
	}

	s.ioStat, err = newIOStatSampler(s.cgroupPath, meter, s.commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create ioStat sampler: %w", err)
	}

	s.ioPressure, err = newIOPressureSampler(s.cgroupPath, meter, s.commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create ioPressure sampler: %w", err)
	}

	s.cpuStat, err = newCPUStatSampler(s.cgroupPath, meter, s.commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create cpuStat sampler: %w", err)
	}

	s.cpuPressure, err = newCPUPressureSampler(s.cgroupPath, meter, s.commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create cpuPressure sampler: %w", err)
	}

	s.memoryCurrent, err = newMemoryCurrentSampler(s.cgroupPath, meter, s.commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create memoryCurrentSampler sampler: %w", err)
	}

	s.memoryPeak, err = newMemoryPeakSampler(s.cgroupPath, meter, s.commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create memoryCurrentSampler sampler: %w", err)
	}

	s.netNS, err = newNetNSSampler(netNS, meter, commonAttrs)
	if err != nil {
		return nil, fmt.Errorf("failed to create netNS sampler: %w", err)
	}

	return s, nil
}

func (s *Sampler) Sample(ctx context.Context) error {
	var eg errgroup.Group

	for _, source := range []struct {
		name   string
		sample func(context.Context) error
	}{
		{ioStatFile, s.ioStat.sample},
		{ioPressureFile, s.ioPressure.sample},
		{cpuStatFile, s.cpuStat.sample},
		{cpuPressureFile, s.cpuPressure.sample},
		{memoryCurrentFile, s.memoryCurrent.sample},
		{memoryPeakFile, s.memoryPeak.sample},
		{"network", s.netNS.sample},
	} {
		eg.Go(func() error {
			err := source.sample(ctx)
			available := int64(1)
			if err != nil {
				available = 0
			}
			s.available.Record(ctx, available,
				metric.WithAttributeSet(s.commonAttrs), metric.WithAttributes(attribute.String("source", source.name)))
			// A cgroup may not exist yet at startup, or the network provider
			// may not support measurements. Export absence, not a false zero.
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errNetworkUnavailable) {
				return nil
			}
			return err
		})
	}

	return eg.Wait()
}

type int64GaugeSample struct {
	gauge metric.Int64Gauge
	attrs attribute.Set
	value *int64
}

func (s *int64GaugeSample) add(value int64) {
	if s.value == nil {
		s.value = new(int64)
	}
	*s.value += value
}

func (s *int64GaugeSample) record(ctx context.Context) {
	if s.value == nil {
		return
	}
	s.gauge.Record(ctx, *s.value, metric.WithAttributeSet(s.attrs))
}

func newInt64GaugeSample(gauge metric.Int64Gauge, attrs attribute.Set) int64GaugeSample {
	return int64GaugeSample{
		gauge: gauge,
		attrs: attrs,
	}
}
