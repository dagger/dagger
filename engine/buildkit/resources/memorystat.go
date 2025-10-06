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
	memoryCurrentFile = "memory.current"
	memoryPeakFile    = "memory.peak"
)

type memoryCurrentSampler struct {
	memoryCurrentFilePath string
	commonAttrs           attribute.Set

	memoryCurrent metric.Int64Gauge
}

func newMemoryCurrentSampler(cgroupPath string, meter metric.Meter, commonAttrs attribute.Set) (*memoryCurrentSampler, error) {
	memoryCurrent, err := meter.Int64Gauge(
		telemetry.MemoryCurrentBytes,
		metric.WithDescription("Current memory usage"),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		return nil, err
	}

	return &memoryCurrentSampler{
		memoryCurrentFilePath: filepath.Join(cgroupPath, memoryCurrentFile),
		commonAttrs:           commonAttrs,
		memoryCurrent:         memoryCurrent,
	}, nil
}

func (s *memoryCurrentSampler) sample(ctx context.Context) error {
	sample := newInt64GaugeSample(s.memoryCurrent, s.commonAttrs)
	bs, err := os.ReadFile(s.memoryCurrentFilePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.memoryCurrentFilePath, err)
	}

	value, err := singleValue(bs)
	if err != nil {
		return fmt.Errorf("error converting value to int64: %w", err)
	}

	sample.add(value)
	sample.record(ctx)

	return nil
}

type memoryPeakSampler struct {
	memoryPeakFilePath string
	commonAttrs        attribute.Set

	memoryPeak metric.Int64Gauge
}

func newMemoryPeakSampler(cgroupPath string, meter metric.Meter, commonAttrs attribute.Set) (*memoryPeakSampler, error) {
	memoryPeak, err := meter.Int64Gauge(
		telemetry.MemoryPeakBytes,
		metric.WithDescription("Peak memory usage"),
		metric.WithUnit("bytes"),
	)
	if err != nil {
		return nil, err
	}

	return &memoryPeakSampler{
		memoryPeakFilePath: filepath.Join(cgroupPath, memoryPeakFile),
		commonAttrs:        commonAttrs,
		memoryPeak:         memoryPeak,
	}, nil
}

func (s *memoryPeakSampler) sample(ctx context.Context) error {
	sample := newInt64GaugeSample(s.memoryPeak, s.commonAttrs)
	bs, err := os.ReadFile(s.memoryPeakFilePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.memoryPeakFilePath, err)
	}

	value, err := singleValue(bs)
	if err != nil {
		return fmt.Errorf("error converting value to int64: %w", err)
	}

	sample.add(value)
	sample.record(ctx)

	return nil
}
