package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	cpuStatFile     = "cpu.stat"
	cpuPressureFile = "cpu.pressure"

	cpuUsageKey  = "usage_usec"
	cpuUserKey   = "user_usec"
	cpuSystemKey = "system_usec"
)

type cpuStatSampler struct {
	cpuStatFilePath string
	commonAttrs     attribute.Set

	cpuUsage  metric.Int64Gauge
	cpuUser   metric.Int64Gauge
	cpuSystem metric.Int64Gauge
}

func newCPUStatSampler(cgroupPath string, meter metric.Meter, commonAttrs attribute.Set) (*cpuStatSampler, error) {
	s := &cpuStatSampler{
		cpuStatFilePath: filepath.Join(cgroupPath, cpuStatFile),
		commonAttrs:     commonAttrs,
	}
	var err error

	s.cpuUsage, err = meter.Int64Gauge(telemetry.CPUStatUsage,
		metric.WithUnit(telemetry.MicrosecondUnitName),
		metric.WithDescription("The total CPU time used by all tasks in the container"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create cpuUsage metric: %w", err)
	}

	s.cpuUser, err = meter.Int64Gauge(telemetry.CPUStatUser,
		metric.WithUnit(telemetry.MicrosecondUnitName),
		metric.WithDescription("The total CPU time spent in user mode by all tasks in the container"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create cpuUser metric: %w", err)
	}

	s.cpuSystem, err = meter.Int64Gauge(telemetry.CPUStatSystem,
		metric.WithUnit(telemetry.MicrosecondUnitName),
		metric.WithDescription("The total CPU time spent in kernel mode by all tasks in the container"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create cpuSystem metric: %w", err)
	}

	return s, nil
}

type cpuStatSample struct {
	cpuUsage  int64GaugeSample
	cpuUser   int64GaugeSample
	cpuSystem int64GaugeSample
}

func (s *cpuStatSampler) sample(ctx context.Context) error {
	sample := cpuStatSample{
		cpuUsage:  newInt64GaugeSample(s.cpuUsage, s.commonAttrs),
		cpuUser:   newInt64GaugeSample(s.cpuUser, s.commonAttrs),
		cpuSystem: newInt64GaugeSample(s.cpuSystem, s.commonAttrs),
	}

	bs, err := os.ReadFile(s.cpuStatFilePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.cpuStatFilePath, err)
	}

	for key, value := range flatKeyValuesInt64(bs) {
		switch key {
		case cpuUsageKey:
			sample.cpuUsage.add(value)
		case cpuUserKey:
			sample.cpuUser.add(value)
		case cpuSystemKey:
			sample.cpuSystem.add(value)
		}
	}

	sample.cpuUsage.record(ctx)
	sample.cpuUser.record(ctx)
	sample.cpuSystem.record(ctx)

	return nil
}

type cpuPressureSampler struct {
	cpuPressureFilePath string
	commonAttrs         attribute.Set

	someTotal metric.Int64Gauge
	fullTotal metric.Int64Gauge
}

func newCPUPressureSampler(cgroupPath string, meter metric.Meter, commonAttrs attribute.Set) (*cpuPressureSampler, error) {
	s := &cpuPressureSampler{
		cpuPressureFilePath: filepath.Join(cgroupPath, cpuPressureFile),
		commonAttrs:         commonAttrs,
	}
	var err error

	s.someTotal, err = meter.Int64Gauge(telemetry.CPUStatPressureSomeTotal,
		metric.WithUnit(telemetry.MicrosecondUnitName),
		metric.WithDescription("The total time that any task in the container has been throttled due to CPU pressure"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create someTotal metric: %w", err)
	}

	s.fullTotal, err = meter.Int64Gauge(telemetry.CPUStatPressureFullTotal,
		metric.WithUnit(telemetry.MicrosecondUnitName),
		metric.WithDescription("The total time that all tasks in the container have simultaneously been throttled due to CPU pressure"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create fullTotal metric: %w", err)
	}

	return s, nil
}

type cpuPressureSample struct {
	someTotal int64GaugeSample
	fullTotal int64GaugeSample
}

func (s *cpuPressureSampler) sample(ctx context.Context) error {
	sample := cpuPressureSample{
		someTotal: newInt64GaugeSample(s.someTotal, s.commonAttrs),
		fullTotal: newInt64GaugeSample(s.fullTotal, s.commonAttrs),
	}

	bs, err := os.ReadFile(s.cpuPressureFilePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.cpuPressureFilePath, err)
	}

	p := parsePressure(bs)
	sample.someTotal.add(p.someTotal)
	sample.fullTotal.add(p.fullTotal)

	sample.someTotal.record(ctx)
	sample.fullTotal.record(ctx)

	return nil
}
