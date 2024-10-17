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
	ioStatFile     = "io.stat"
	ioPressureFile = "io.pressure"
)

const (
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

	s.readBytes, err = meter.Int64Gauge(telemetry.IOStatDiskReadBytes, metric.WithUnit(telemetry.ByteUnitName))
	if err != nil {
		return nil, fmt.Errorf("failed to create readBytes metric: %w", err)
	}

	s.writeBytes, err = meter.Int64Gauge(telemetry.IOStatDiskWriteBytes, metric.WithUnit(telemetry.ByteUnitName))
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

	// Format here: https://docs.kernel.org/admin-guide/cgroup-v2.html#io-interface-files
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

	s.someTotal, err = meter.Int64Gauge(telemetry.IOStatPressureSomeTotal, metric.WithUnit(telemetry.MicrosecondUnitName))
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

	// Format here: https://docs.kernel.org/accounting/psi.html#psi
	lines := strings.Split(string(fileBytes), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			continue
		}
		pressureKind := fields[0]
		if pressureKind != "some" {
			continue
		}
		k, v := parseKeyValue(fields[4])
		if k != "total" {
			continue
		}
		sample.someTotal.add(v)
	}

	sample.someTotal.record(ctx)

	return nil
}

func parseKeyValue(kv string) (string, int64) {
	key, valueStr, ok := strings.Cut(kv, "=")
	if !ok {
		return "", 0
	}
	value, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return "", 0
	}
	return key, value
}
