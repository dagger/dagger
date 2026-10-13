package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	memoryCurrentFile = "memory.current"
	memoryPeakFile    = "memory.peak"
	memoryStatFile    = "memory.stat"

	memoryStatAnonKey         = "anon"
	memoryStatInactiveFileKey = "inactive_file"
)

type memoryCurrentSampler struct {
	memoryCurrentFilePath string
	memoryStatFilePath    string
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
		memoryStatFilePath:    filepath.Join(cgroupPath, memoryStatFile),
		commonAttrs:           commonAttrs,
		memoryCurrent:         memoryCurrent,
	}, nil
}

func (s *memoryCurrentSampler) sample(ctx context.Context) error {
	sample := newInt64GaugeSample(s.memoryCurrent, s.commonAttrs)
	bs, err := os.ReadFile(s.memoryCurrentFilePath)
	ctx = enginetel.WithWorkloadReadingTime(ctx, time.Now())
	defer func() {
		enginetel.RecordResourceAvailability(ctx, memoryCurrentFile, sample.value != nil)
	}()
	// The memory.stat readings get the time of memory.current, so the working
	// set can be computed for each reading.
	statErr := s.sampleWorkloadMemoryStat(ctx)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return statErr
	case err != nil:
		return errors.Join(fmt.Errorf("failed to read %s: %w", s.memoryCurrentFilePath, err), statErr)
	}

	value, err := singleValue(bs)
	if err != nil {
		return errors.Join(fmt.Errorf("error converting value to int64: %w", err), statErr)
	}

	sample.add(value)
	sample.record(ctx)

	return statErr
}

// sampleWorkloadMemoryStat reads memory.stat only for the workload export.
// memory.current includes page cache that the kernel can reclaim; anon and
// inactive_file let billing separate it. No ordinary gauge records them.
func (s *memoryCurrentSampler) sampleWorkloadMemoryStat(ctx context.Context) error {
	if !enginetel.HasWorkloadReadings(ctx) {
		return nil
	}
	var anon, inactiveFile int64
	var hasAnon, hasInactiveFile bool
	bs, err := os.ReadFile(s.memoryStatFilePath)
	defer func() {
		enginetel.RecordResourceAvailability(ctx, memoryStatFile, hasAnon && hasInactiveFile)
	}()
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("failed to read %s: %w", s.memoryStatFilePath, err)
	}

	for key, value := range flatKeyValuesInt64(bs) {
		switch key {
		case memoryStatAnonKey:
			anon, hasAnon = value, true
		case memoryStatInactiveFileKey:
			inactiveFile, hasInactiveFile = value, true
		}
	}
	if hasAnon && hasInactiveFile {
		enginetel.RecordWorkloadMemoryStat(ctx, anon, inactiveFile)
	}
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
