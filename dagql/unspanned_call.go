package dagql

import "context"

// PROTOTYPE (check-lazy-eval-proto).
//
// WithUnspannedCall asks the next call of field selected under ctx to emit no
// span of its own: a planning select whose result stands for work that runs
// later, under whoever forces it. Only that call is affected: the calls its
// preparation makes (loading contextual arguments, say) and the calls its
// resolver makes keep their telemetry.
//
// With unspannedCallNoProducer, the call also records no lazy-evaluation
// producer span for its result (see Cache.captureSessionLazySpanContext), so
// forcing a lazy result of it takes beginOTelLazyOp's no-producer branch: the
// deferred work is reported under the span that forces it. Without it, the
// result's producer is whatever span was current, provisionally, as for any
// call with no span of its own.
func WithUnspannedCall(ctx context.Context, field string) context.Context {
	return context.WithValue(ctx, unspannedCallKey{}, unspannedCall{field: field, noProducer: unspannedCallNoProducer})
}

// unspannedCallNoProducer selects the prototype's variant: false keeps the
// provisional producer capture (the work resumes under the planner's span).
var unspannedCallNoProducer = true

type unspannedCallKey struct{}

type unspannedCall struct {
	field      string
	noProducer bool
	// call is the call that took the request; set once taken, so nothing
	// beneath it matches.
	call *ResultCall
}

func isUnspannedCall(ctx context.Context, field string) bool {
	v, ok := ctx.Value(unspannedCallKey{}).(unspannedCall)
	return ok && v.call == nil && v.field == field
}

func withUnspannedCallDone(ctx context.Context, call *ResultCall) context.Context {
	v, _ := ctx.Value(unspannedCallKey{}).(unspannedCall)
	v.call = call
	return context.WithValue(ctx, unspannedCallKey{}, v)
}

// recordsNoLazyProducer reports whether the current call asked to record no
// lazy-evaluation producer for its result.
func recordsNoLazyProducer(ctx context.Context) bool {
	v, ok := ctx.Value(unspannedCallKey{}).(unspannedCall)
	return ok && v.noProducer && v.call != nil && v.call == CurrentCall(ctx)
}
