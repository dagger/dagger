package telemetry

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Encapsulate can be applied to a span to indicate that this span should
// collapse its children by default.
func Encapsulate() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIEncapsulateAttr, true))
}

// Reveal can be applied to a span to indicate that this span should
// collapse its children by default.
func Reveal() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIRevealAttr, true))
}

// Encapsulated can be applied to a child span to indicate that it should be
// collapsed by default.
func Encapsulated() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIEncapsulatedAttr, true))
}

func Resume(ctx context.Context) trace.SpanStartOption {
	return trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(ctx)})
}

// Internal can be applied to a span to indicate that this span should not be
// shown to the user by default.
func Internal() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIInternalAttr, true))
}

// ActorEmoji sets an emoji representing the actor of the span.
func ActorEmoji(emoji string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(UIActorEmojiAttr, emoji))
}

// Passthrough can be applied to a span to cause the UI to skip over it and
// show its children instead.
func Passthrough() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIPassthroughAttr, true))
}

// Boundary indicates that telemetry shouldn't bubble up through this span,
// through Reveal, RollUpLogs, or RollUpSpans.
func Boundary() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIBoundaryAttr, true))
}

// Tracer returns a Tracer for the given library using the provider from
// the current span.
func Tracer(ctx context.Context, lib string) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer(lib)
}

// ExtendedError is an error that can provide extra data in an error response.
type ExtendedError interface {
	error
	Extensions() map[string]any
}

// End is a helper to end a span with an error if the function returns an error.
//
// It is optimized for use as a defer one-liner with a function that has a
// named error return value, conventionally `rerr`.
//
//	defer telemetry.End(span, func() error { return rerr })
//
// Deprecated: use EndWithCause instead.
func End(span trace.Span, fn func() error) {
	err := fn()
	EndWithCause(span, &err)
}

// EndWithCause is a helper for ending a span and tracking the span as the error
// origin if errPtr points to an error that does not already have an origin.
//
// It is optimized for use as a defer one-liner with a function that has a
// named error return value, conventionally `rerr`.
//
//	defer telemetry.EndWithCause(span, &rerr)
func EndWithCause(span trace.Span, errPtr *error) {
	if errPtr != nil && *errPtr != nil {
		err := *errPtr
		var extErr ExtendedError
		if errors.As(err, &extErr) {
			// Look for an error origin embedded in error extensions, and link to it.
			originCtx := trace.SpanContextFromContext(
				Propagator.Extract(
					context.Background(),
					AnyMapCarrier(extErr.Extensions()),
				),
			)
			if originCtx.IsValid() && originCtx.SpanID() != span.SpanContext().SpanID() {
				span.AddLink(trace.Link{
					SpanContext: originCtx,
					Attributes: []attribute.KeyValue{
						attribute.String(LinkPurposeAttr, LinkPurposeErrorOrigin),
					},
				})
			}
		} else {
			tracked := originTrackedError{
				original:    err,
				propagation: AnyMapCarrier{},
			}
			Propagator.Inject(
				trace.ContextWithSpanContext(
					context.Background(),
					span.SpanContext(),
				),
				tracked.propagation,
			)
			*errPtr = tracked
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

// ErrorOrigin extracts the origin span context from an error, if any.
func ErrorOrigin(err error) trace.SpanContext {
	var extErr ExtendedError
	if errors.As(err, &extErr) {
		return trace.SpanContextFromContext(
			Propagator.Extract(
				context.Background(),
				AnyMapCarrier(extErr.Extensions()),
			),
		)
	}
	return trace.SpanContext{}
}

type originTrackedError struct {
	original    error
	propagation AnyMapCarrier
}

func (e originTrackedError) Unwrap() error {
	return e.original
}

var _ ExtendedError = originTrackedError{}

func (e originTrackedError) Error() string {
	return e.original.Error()
}

func (e originTrackedError) Extensions() map[string]any {
	return e.propagation
}
