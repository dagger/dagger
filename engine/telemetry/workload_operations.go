package telemetry

import (
	"context"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const WorkloadScope = "dagger.io/workload"

type workloadTracerKey struct{}
type workloadTracer struct{ tracer trace.Tracer }
type workloadParentKey struct{}
type workloadReferenceKey struct{}
type workloadRootKey struct{}

type workloadParent struct {
	owner        trace.SpanContext
	presentation trace.SpanContext
}

// OperationTracerProvider exports only workload operations. It shares the
// engine queue, not the session's CLI or Cloud trace destinations.
func (d *WorkloadExport) OperationTracerProvider(sessionID string) *sdktrace.TracerProvider {
	processor := d.SpanProcessor(sessionID)
	if processor == nil {
		return nil
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(processor),
	)
}

func WithWorkloadTracer(ctx context.Context, tracer trace.Tracer) context.Context {
	if tracer == nil {
		return ctx
	}
	return context.WithValue(ctx, workloadTracerKey{}, &workloadTracer{tracer: tracer})
}

// WithoutWorkloadSpans disables operation records. Resource readings remain enabled.
func WithoutWorkloadSpans(ctx context.Context) context.Context {
	return context.WithValue(ctx, workloadTracerKey{}, (*workloadTracer)(nil))
}

func HasWorkloadSpans(ctx context.Context) bool {
	tracer, _ := ctx.Value(workloadTracerKey{}).(*workloadTracer)
	return tracer != nil
}

// WithWorkloadReference records cost evidence without a consumer use.
func WithWorkloadReference(ctx context.Context) context.Context {
	return context.WithValue(ctx, workloadReferenceKey{}, true)
}

func WorkloadReferenceOnly(ctx context.Context) bool {
	reference, _ := ctx.Value(workloadReferenceKey{}).(bool)
	return reference
}

// WithWorkloadRoot detaches only the next workload copy from its parent.
func WithWorkloadRoot(ctx context.Context) context.Context {
	return context.WithValue(ctx, workloadRootKey{}, true)
}

// ActiveWorkloadSpan identifies the operation responsible for nested work.
// Engine-owned module metadata can carry it across an API request boundary.
func ActiveWorkloadSpan(ctx context.Context) trace.SpanContext {
	if !HasWorkloadSpans(ctx) {
		return trace.SpanContext{}
	}
	parent, _ := ctx.Value(workloadParentKey{}).(workloadParent)
	return parent.owner
}

// StartWorkloadSpan keeps ordinary tracing on its current span. Only the
// workload export sees this operation and its consumption relationship.
func StartWorkloadSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) trace.Span {
	tracer, _ := ctx.Value(workloadTracerKey{}).(*workloadTracer)
	if tracer == nil {
		return nil
	}
	if WorkloadReferenceOnly(ctx) {
		opts = append(opts, trace.WithAttributes(attribute.Bool(telemetryattrs.WorkloadReferenceAttr, true)))
	}
	parent := trace.SpanContextFromContext(ctx)
	if override, ok := workloadParentFromContext(ctx); ok {
		parent = override
	}
	_, span := tracer.tracer.Start(trace.ContextWithSpanContext(ctx, parent), name, opts...)
	return span
}

// WithWorkloadParent binds only direct children of the current presentation
// span to owner in the workload export. Descendants retain their own parent;
// the user-facing trace remains unchanged.
func WithWorkloadParent(ctx context.Context, owner trace.SpanContext) context.Context {
	if !owner.IsValid() {
		return ctx
	}
	return context.WithValue(ctx, workloadParentKey{}, workloadParent{
		owner: owner, presentation: trace.SpanContextFromContext(ctx),
	})
}

func workloadParentFromContext(ctx context.Context) (trace.SpanContext, bool) {
	parent, ok := ctx.Value(workloadParentKey{}).(workloadParent)
	current := trace.SpanContextFromContext(ctx)
	if !ok || current.TraceID() != parent.presentation.TraceID() ||
		current.SpanID() != parent.presentation.SpanID() ||
		current.TraceID() != parent.owner.TraceID() {
		return trace.SpanContext{}, false
	}
	return parent.owner, true
}
