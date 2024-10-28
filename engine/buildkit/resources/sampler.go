package resources

import (
	"context"
	"fmt"
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
}

func NewSampler(
	cgroupNSSubpath string,
	meter metric.Meter,
	commonAttrs attribute.Set,
) (*Sampler, error) {
	s := &Sampler{
		cgroupPath:  filepath.Join(defaultMountpoint, cgroupNSSubpath),
		commonAttrs: commonAttrs,
	}
	var err error

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

	return s, nil
}

func (s *Sampler) Sample(ctx context.Context) error {
	var eg errgroup.Group

	eg.Go(func() error {
		return s.ioStat.sample(ctx)
	})

	eg.Go(func() error {
		return s.ioPressure.sample(ctx)
	})

	eg.Go(func() error {
		return s.cpuStat.sample(ctx)
	})

	eg.Go(func() error {
		return s.cpuPressure.sample(ctx)
	})

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
