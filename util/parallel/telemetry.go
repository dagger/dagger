package parallel

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// [shykes] the following code is copy-pasted from //sdk/go/telemetry/span.go,
//  to avoid unextricable go.mod replace issues when trying to import dagger.io/dagger/telemetry from here

// endWithCause is a helper for ending a span and tracking the span as the error
// origin if errPtr points to an error that does not already have an origin.
//
// It is optimized for use as a defer one-liner with a function that has a
// named error return value, conventionally `rerr`.
//
//	defer telemetry.endWithCause(span, &rerr)
func endSpanWithCause(span trace.Span, errPtr *error) {
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

// Propagator is a composite propagator of everything we could possibly want.
//
// Do not rely on otel.GetTextMapPropagator() - it's prone to change from a
// random import.
var Propagator = propagation.NewCompositeTextMapPropagator(
	propagation.Baggage{},
	propagation.TraceContext{},
)

// ExtendedError is an error that can provide extra data in an error response.
type ExtendedError interface {
	error
	Extensions() map[string]any
}

// AnyMapCarrier is a utility for propagating via a map[string]any instead of a
// map[string]string.
type AnyMapCarrier map[string]any

var _ propagation.TextMapCarrier = AnyMapCarrier{}

func (c AnyMapCarrier) Get(key string) string {
	str, _ := c[key].(string)
	return str
}

func (c AnyMapCarrier) Set(key, value string) {
	c[key] = value
}

func (c AnyMapCarrier) Keys() []string {
	var keys []string
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

var (
	// Clarifies the meaning of a link between two spans.
	LinkPurposeAttr = "dagger.io/link.purpose"
	// The linked span caused the current span to run - in other words, this span
	// is a continuation, or effect, of the other one.
	//
	// This is the default if no explicit purpose is given.
	LinkPurposeCause = "cause"
	// The linked span is the origin of the error bubbled up by the current span.
	LinkPurposeErrorOrigin = "error_origin"
)

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
