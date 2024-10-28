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
	ioStatFile     = "io.stat"
	ioPressureFile = "io.pressure"

	ioReadBytes  = "rbytes"
	ioWriteBytes = "wbytes"
)

type ioStatSampler struct {
	ioStatFilePath string
	commonAttrs    attribute.Set

	readBytes  metric.Int64Gauge
	writeBytes metric.Int64Gauge
}

func newIOStatSampler(cgroupPath string, meter metric.Meter, commonAttrs attribute.Set) (*ioStatSampler, error) {
	s := &ioStatSampler{
		ioStatFilePath: filepath.Join(cgroupPath, ioStatFile),
		commonAttrs:    commonAttrs,
	}
	var err error

	s.readBytes, err = meter.Int64Gauge(telemetry.IOStatDiskReadBytes,
		metric.WithUnit(telemetry.ByteUnitName),
		metric.WithDescription("The total number of bytes read from the disk by all tasks in the container (not including disk read cache)"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create readBytes metric: %w", err)
	}

	s.writeBytes, err = meter.Int64Gauge(telemetry.IOStatDiskWriteBytes,
		metric.WithUnit(telemetry.ByteUnitName),
		metric.WithDescription("The total number of bytes written to the disk by all tasks in the container (not including disk write cache)"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create writeBytes metric: %w", err)
	}

	return s, nil
}

type ioStatSample struct {
	readBytes  int64GaugeSample
	writeBytes int64GaugeSample
}

func (s *ioStatSampler) sample(ctx context.Context) error {
	sample := ioStatSample{
		readBytes:  newInt64GaugeSample(s.readBytes, s.commonAttrs),
		writeBytes: newInt64GaugeSample(s.writeBytes, s.commonAttrs),
	}

	fileBytes, err := os.ReadFile(s.ioStatFilePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.ioStatFilePath, err)
	}

	for _, kvs := range nestedKeyValuesInt64(fileBytes) {
		for k, v := range kvs {
			switch k {
			case ioReadBytes:
				sample.readBytes.add(v)
			case ioWriteBytes:
				sample.writeBytes.add(v)
			}
		}
	}

	sample.readBytes.record(ctx)
	sample.writeBytes.record(ctx)

	return nil
}

type ioPressureSampler struct {
	ioPressureFilePath string
	commonAttrs        attribute.Set

	someTotal metric.Int64Gauge
}

func newIOPressureSampler(cgroupPath string, meter metric.Meter, commonAttrs attribute.Set) (*ioPressureSampler, error) {
	s := &ioPressureSampler{
		ioPressureFilePath: filepath.Join(cgroupPath, ioPressureFile),
		commonAttrs:        commonAttrs,
	}
	var err error

	s.someTotal, err = meter.Int64Gauge(telemetry.IOStatPressureSomeTotal,
		metric.WithUnit(telemetry.MicrosecondUnitName),
		metric.WithDescription("The total time in microseconds that tasks in the container were throttled due to I/O pressure"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create some total metric: %w", err)
	}

	return s, nil
}

type ioPressureSample struct {
	someTotal int64GaugeSample
}

func (s *ioPressureSampler) sample(ctx context.Context) error {
	sample := ioPressureSample{
		someTotal: newInt64GaugeSample(s.someTotal, s.commonAttrs),
	}

	fileBytes, err := os.ReadFile(s.ioPressureFilePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.ioPressureFilePath, err)
	}

	p := parsePressure(fileBytes)
	sample.someTotal.add(p.someTotal)
	sample.someTotal.record(ctx)

	return nil
}
