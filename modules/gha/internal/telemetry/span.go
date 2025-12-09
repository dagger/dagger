package telemetry

import (
	"context"
	"fmt"
	"regexp"

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
	if errPtr == nil || *errPtr == nil {
		span.SetStatus(codes.Ok, "")
		span.End()
		return
	}
	err := *errPtr

	// Look for error origin regex matches and attach them as span links
	//
	// NOTE: this is technically redundant, since the frontend also parses from
	// the span's error description for maximum compatibility across SDKs and
	// transports. But for the sake of clean OTel data, we'll do it here too.
	origins := ParseErrorOrigins(err.Error())
	if len(origins) > 0 {
		for _, origin := range origins {
			if origin.IsValid() && origin.SpanID() != span.SpanContext().SpanID() {
				span.AddLink(trace.Link{
					SpanContext: origin,
					Attributes: []attribute.KeyValue{
						attribute.String(LinkPurposeAttr, LinkPurposeErrorOrigin),
					},
				})
			}
		}

		// Set the cleaned-up error message as the span error description
		cleaned := ErrorOriginRegex.ReplaceAllString(err.Error(), "")
		span.SetStatus(codes.Error, cleaned)
	} else {
		// If there are no origins tracked in the error already, stamp it with this
		// span as the origin
		*errPtr = TrackOrigin(err, span.SpanContext())

		// NB: recording the inner un-stamped error here, not really sure if we
		// benefit from using this at all but might as well avoid recording wrapped
		// ones
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	span.End()
}

// ErrorOrigin extracts the origin span context from an error, if any.
func ParseErrorOrigins(errMsg string) []trace.SpanContext {
	// Look for error origin regex matches and attach them as span links
	matches := ErrorOriginRegex.FindAllStringSubmatch(errMsg, -1)
	if len(matches) == 0 {
		return nil
	}
	var origins []trace.SpanContext
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		traceID, err := trace.TraceIDFromHex(match[1])
		if err != nil {
			continue
		}
		spanID, err := trace.SpanIDFromHex(match[2])
		if err != nil {
			continue
		}
		originCtx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID,
			SpanID:  spanID,
		})
		origins = append(origins, originCtx)
	}
	return origins
}

type OriginTrackedError struct {
	original error
	origin   trace.SpanContext
}

func TrackOrigin(err error, origin trace.SpanContext) OriginTrackedError {
	return OriginTrackedError{
		original: err,
		origin:   origin,
	}
}

func (e OriginTrackedError) Unwrap() error {
	return e.original
}

var ErrorOriginRegex = regexp.MustCompile(`\s*\[traceparent:([0-9a-f]{32})-([0-9a-f]{16})\]`)

func (e OriginTrackedError) Error() string {
	return fmt.Sprintf("%s [traceparent:%s-%s]", e.original.Error(), e.origin.TraceID(), e.origin.SpanID())
}
