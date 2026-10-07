package networkmetrics

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/ebpf/nettracer"
	"github.com/dagger/dagger/engine/engineutil/resources"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	daggerotel "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// PrepareCommandNetwork attributes CPU, memory, and kernel network counters to
// a dedicated child span per command. Call before starting cmd and finish after Wait. For daemons,
// keep the accounting alive until the daemon is released, not just its parent.
// Finish records a snapshot and schedules bounded cleanup without waiting for
// surviving helpers. The span ends at finish; late samples keep its ID and
// require the session's telemetry to remain open.
// Unsupported environments run normally and report availability=0.
func PrepareCommandNetwork(ctx context.Context, cmd *exec.Cmd) func() {
	return prepareCommandNetwork(ctx, cmd, func(cmd *exec.Cmd) (commandNetworkCounters, error) {
		return nettracer.PrepareCommand(cmd)
	})
}

type commandNetworkCounters interface {
	CgroupPath() string
	Sample() (nettracer.Sample, error)
	WaitEmpty(context.Context) error
	Close() error
}

func prepareCommandNetwork(ctx context.Context, cmd *exec.Cmd, prepare func(*exec.Cmd) (commandNetworkCounters, error)) func() {
	endSpan := func() {}
	if trace.SpanContextFromContext(ctx).IsValid() {
		var commandSpan trace.Span
		ctx, commandSpan = daggerotel.Tracer(ctx, "dagger.io/network").Start(ctx,
			filepath.Base(cmd.Path)+" resources")
		endSpan = func() { commandSpan.End() }
	}
	command, prepareErr := prepare(cmd)
	// Cgroup placement also isolates CPU and memory. It does not depend on
	// having a span or a working network sampler.
	cleanup := sync.OnceFunc(endSpan)
	if prepareErr == nil {
		cleanup = sync.OnceFunc(func() {
			endSpan()
			go func() {
				waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				defer cancel()
				_ = command.WaitEmpty(waitCtx)
				if err := command.Close(); err != nil {
					slog.DebugContext(ctx, "release subprocess accounting", "error", err)
				}
			}()
		})
	}
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() {
		return cleanup
	}
	ctx = context.WithoutCancel(ctx)
	meter := daggerotel.Meter(ctx, "dagger.io/network")
	attrs := attribute.NewSet(
		attribute.String(daggerotel.MetricsTraceIDAttr, span.TraceID().String()),
		attribute.String(daggerotel.MetricsSpanIDAttr, span.SpanID().String()),
	)
	opts := metric.WithAttributeSet(attrs)
	available, err := meter.Int64Gauge(telemetryattrs.NetworkAvailable, metric.WithUnit("1"))
	if err != nil {
		return cleanup
	}
	names := []string{
		telemetryattrs.NetworkRxBytes, telemetryattrs.NetworkTxBytes,
		telemetryattrs.NetworkInternalRxBytes, telemetryattrs.NetworkInternalTxBytes,
		telemetryattrs.NetworkExternalRxBytes, telemetryattrs.NetworkExternalTxBytes,
	}
	gauges := make([]metric.Int64Gauge, len(names))
	for i, name := range names {
		gauges[i], err = meter.Int64Gauge(name, metric.WithUnit("bytes"))
		if err != nil {
			available.Record(ctx, 0, opts)
			return cleanup
		}
	}
	if prepareErr != nil {
		available.Record(ctx, 0, opts)
		slog.DebugContext(ctx, "subprocess network accounting unavailable", "error", prepareErr)
		return cleanup
	}
	resourceSampler, err := resources.NewCommandSampler(command.CgroupPath(), meter, attrs)
	if err != nil {
		slog.DebugContext(ctx, "subprocess resource accounting unavailable", "error", err)
	}
	var sampleMu sync.Mutex
	sample := func() {
		sampleMu.Lock()
		defer sampleMu.Unlock()
		// Resource accounting remains useful when eBPF is disabled or fails.
		if resourceSampler != nil {
			if err := resourceSampler.Sample(ctx); err != nil {
				slog.DebugContext(ctx, "sample subprocess resources", "error", err)
			}
		}
		value, err := command.Sample()
		if err != nil {
			available.Record(ctx, 0, opts)
			return
		}
		available.Record(ctx, 1, opts)
		for i, n := range []uint64{
			value.InternalRX + value.ExternalRX, value.InternalTX + value.ExternalTX,
			value.InternalRX, value.InternalTX, value.ExternalRX, value.ExternalTX,
		} {
			gauges[i].Record(ctx, int64(min(n, math.MaxInt64)), opts)
		}
	}
	sample()
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				sample()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return sync.OnceFunc(func() {
		// Publish current traffic before the caller can close session telemetry.
		sample()
		// Session teardown does not wait for cgroup cleanup. End the span now
		// so its completion is included in the caller's telemetry flush.
		endSpan()
		go func() {
			// Lazy unmount can leave SSHFS alive. Do not hold up the operation
			// or CLI while waiting for it, but bound cleanup for stuck helpers.
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			waitErr := command.WaitEmpty(waitCtx)
			close(done)
			<-stopped
			if waitErr != nil {
				// A live helper can make the totals incomplete, but it does
				// not invalidate the samples already collected.
				slog.DebugContext(ctx, "subprocess network accounting incomplete", "error", waitErr)
			}
			if err := command.Close(); err != nil {
				slog.DebugContext(ctx, "release subprocess network accounting", "error", err)
			}
		}()
	})
}
