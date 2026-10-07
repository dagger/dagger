package engineutil

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/engine/engineutil/resources"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	resourcestypes "github.com/dagger/dagger/internal/buildkit/executor/resources/types"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

func execResourceTelemetry(ctx context.Context, state *execState) (context.Context, metric.Meter, attribute.Set) {
	hasCallDigest := state.execMD != nil && state.execMD.CallDigest != ""
	meter := telemetry.Meter(ctx, InstrumentationLibrary)
	if !hasCallDigest {
		// Preserve the ordinary path's exclusion of unassociated execs.
		meter = noop.NewMeterProvider().Meter(InstrumentationLibrary)
	}
	meter = enginetel.WorkloadReadingMeter(ctx, meter, InstrumentationLibrary)
	var attrs []attribute.KeyValue
	if hasCallDigest {
		attrs = append(attrs, attribute.String(telemetry.DagDigestAttr, string(state.execMD.CallDigest)))
	}
	span := trace.SpanContextFromContext(ctx)
	if span.HasSpanID() {
		attrs = append(attrs, attribute.String(telemetry.MetricsSpanIDAttr, span.SpanID().String()))
	}
	if span.HasTraceID() {
		attrs = append(attrs, attribute.String(telemetry.MetricsTraceIDAttr, span.TraceID().String()))
	}
	if enginetel.HasWorkloadReadings(ctx) {
		interval := min(enginetel.WorkloadReadingInterval(ctx, cgroupSampleInterval), cgroupSampleInterval)
		workloadAttrs := append(slices.Clone(attrs),
			attribute.String(enginetel.ExecutionIDAttr, state.id),
			attribute.Bool(enginetel.ExecutionInternalAttr, state.execMD != nil && state.execMD.Internal),
			attribute.Int64(enginetel.SampleIntervalAttr, interval.Milliseconds()),
		)
		ctx = enginetel.WithWorkloadReadingAttributes(ctx, attribute.NewSet(workloadAttrs...))
	}
	return ctx, meter, attribute.NewSet(attrs...)
}

// Setup can fail after SSHFS has already done work but before runContainer
// starts the regular sampler. Mount cleanup has stopped the daemons here;
// their parent cgroup and network counters remain alive until the next cleanup.
func sampleFailedExecMountResources(ctx context.Context, state *execState) error {
	if state.mountResources == nil || !state.mountResources.Active() {
		return nil
	}
	hasCallDigest := state.execMD != nil && state.execMD.CallDigest != ""
	if !hasCallDigest && !enginetel.HasWorkloadReadings(ctx) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalCgroupSampleTimeout)
	defer cancel()
	ctx, meter, attrs := execResourceTelemetry(ctx, state)
	// No container ran, so there is no container network delta to include. In
	// particular, do not baseline away the mount traffic already incurred.
	sampler, err := resources.NewSampler(state.resourceCgroupPath, setupNetworkSample{}, meter, attrs)
	if err != nil {
		return fmt.Errorf("create failed exec mount sampler: %w", err)
	}
	sampler.SetMountNetwork(state.mountResources)
	if !state.mountResources.Available() {
		sampler.DisableCgroupSamples()
	}
	return sampler.Sample(enginetel.WithSamplePhase(ctx, "final"))
}

type setupNetworkSample struct{}

func (setupNetworkSample) Sample() (*resourcestypes.NetworkSample, error) {
	return &resourcestypes.NetworkSample{ScopeSupported: true}, nil
}
