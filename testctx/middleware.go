package testctx

import (
	"time"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type Middleware = func(*T) *T

func WithParallel(t *T) *T {
	t.Parallel()
	return t.
		BeforeEach(func(t *T) *T {
			t.Parallel()
			return t
		})
}

func WithOTelTracing(tracer trace.Tracer) Middleware {
	wrapSpan := func(t *T) *T {
		ctx, span := tracer.Start(t.Context(),
			// Set the _base_ name of the test, since the span hierarchy chains them.
			t.BaseName(),
			// Correlate the cause/effect relation between tests and subtests.
			trace.WithAttributes(attribute.String(telemetry.EffectIDAttr, t.id())),
		)
		t.AfterSelf(func(t *T) {
			if t.Failed() {
				span.SetStatus(codes.Error, "test failed")
			}
			var effects []string
			for _, st := range t.subtests {
				effects = append(effects, st.id())
			}
			// Correlates to EffectIDAttr set on each subtest span.
			span.SetAttributes(attribute.StringSlice(telemetry.EffectIDsAttr, effects))
			span.End()
		})
		return t.WithContext(ctx)
	}
	return func(t *T) *T {
		return t.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func WithOTelLogging(logger log.Logger) Middleware {
	return func(t *T) *T {
		return t.WithLogger(func(t2 *T, msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(t2.Context(), rec)
		})
	}
}

func Combine(middleware ...Middleware) Middleware {
	return func(t *T) *T {
		for _, m := range middleware {
			t = m(t)
		}
		return t
	}
}
