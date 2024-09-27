package resources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	ioStatFile = "io.stat"

	// TODO: not read yet, saving for later
	ioPressureFile = "io.pressure"
)

const (
	ioReadBytes  = "rbytes"
	ioWriteBytes = "wbytes"

	// TODO: not read yet, saving for later
	ioDiscardBytes = "dbytes"
	ioReadIOs      = "rios"
	ioWriteIOs     = "wios"
	ioDiscardIOs   = "dios"
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

	s.readBytes, err = meter.Int64Gauge(telemetry.IOStatDiskReadBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create readBytes metric: %w", err)
	}

	s.writeBytes, err = meter.Int64Gauge(telemetry.IOStatDiskWriteBytes)
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

	lines := strings.Split(string(fileBytes), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		for _, part := range parts[1:] {
			key, value := parseKeyValue(part)
			if key == "" {
				continue
			}

			switch key {
			case ioReadBytes:
				sample.readBytes.add(value)
			case ioWriteBytes:
				sample.writeBytes.add(value)
			}
		}
	}

	sample.readBytes.record(ctx)
	sample.writeBytes.record(ctx)

	return nil
}

func parseKeyValue(kv string) (key string, value int64) {
	parts := strings.SplitN(kv, "=", 2)
	if len(parts) != 2 {
		return "", 0
	}
	key = parts[0]
	value, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0
	}
	return key, value
}
