package resources

import (
	"context"
	"errors"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// CommandSampler uses the same CPU and memory metrics as withExec, without
// requiring a network namespace or an eBPF network sampler.
type CommandSampler struct {
	cpu     *cpuStatSampler
	current *memoryCurrentSampler
	peak    *memoryPeakSampler
}

func NewCommandSampler(cgroupPath string, meter metric.Meter, attrs attribute.Set) (*CommandSampler, error) {
	cpu, err := newCPUStatSampler(cgroupPath, meter, attrs)
	if err != nil {
		return nil, err
	}
	current, err := newMemoryCurrentSampler(cgroupPath, meter, attrs)
	if err != nil {
		return nil, err
	}
	peak, err := newMemoryPeakSampler(cgroupPath, meter, attrs)
	if err != nil {
		return nil, err
	}
	return &CommandSampler{cpu: cpu, current: current, peak: peak}, nil
}

// Sample must run before the command's cgroup is removed, including after exit
// so short-lived commands retain their final CPU usage and memory peak.
func (s *CommandSampler) Sample(ctx context.Context) error {
	// Helpers have span metrics, but no executor identity in the workload
	// export. Do not emit anonymous availability readings into that stream.
	ctx = enginetel.WithoutWorkloadReadings(ctx)
	return errors.Join(s.cpu.sample(ctx), s.current.sample(ctx), s.peak.sample(ctx))
}
