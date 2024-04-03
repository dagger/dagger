package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

type ProxyTraceProvider struct {
	embedded.TracerProvider

	tp       *tracesdk.TracerProvider
	onUpdate func(trace.Span)
}

func NewProxyTraceProvider(tp *tracesdk.TracerProvider, onUpdate func(trace.Span)) *ProxyTraceProvider {
	return &ProxyTraceProvider{
		tp:       tp,
		onUpdate: onUpdate,
	}
}

func (tp *ProxyTraceProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return &ProxyTracer{
		tracer:   tp.tp.Tracer(name, options...),
		onUpdate: tp.onUpdate,
	}
}

func (tp *ProxyTraceProvider) ForceFlush(ctx context.Context) error {
	return tp.tp.ForceFlush(ctx)
}

func (tp *ProxyTraceProvider) Shutdown(ctx context.Context) error {
	return tp.tp.Shutdown(ctx)
}

type ProxyTracer struct {
	embedded.Tracer
	tracer   trace.Tracer
	onUpdate func(trace.Span)
}

func (t ProxyTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, span := t.tracer.Start(ctx, spanName, opts...)
	return ctx, proxySpan{sp: span, onUpdate: t.onUpdate}
}

type proxySpan struct {
	embedded.Span
	sp       trace.Span
	onUpdate func(trace.Span)
}

var _ trace.Span = proxySpan{}

func (s proxySpan) SpanContext() trace.SpanContext { return s.sp.SpanContext() }

func (s proxySpan) IsRecording() bool { return s.sp.IsRecording() }

func (s proxySpan) SetStatus(code codes.Code, message string) {
	s.sp.SetStatus(code, message)
	s.onUpdate(s.sp)
}

// func (s proxySpan) SetError(v bool) { s.sp.SetError(v) }

func (s proxySpan) SetAttributes(attributes ...attribute.KeyValue) {
	s.sp.SetAttributes(attributes...)
	s.onUpdate(s.sp)
}

func (s proxySpan) End(opts ...trace.SpanEndOption) { s.sp.End(opts...) }

func (s proxySpan) RecordError(err error, opts ...trace.EventOption) {
	s.sp.RecordError(err, opts...)
	s.onUpdate(s.sp)
}

func (s proxySpan) AddEvent(event string, opts ...trace.EventOption) {
	s.sp.AddEvent(event, opts...)
	s.onUpdate(s.sp)
}

func (s proxySpan) SetName(name string) {
	s.sp.SetName(name)
	s.onUpdate(s.sp)
}

func (s proxySpan) TracerProvider() trace.TracerProvider { return s.sp.TracerProvider() }
