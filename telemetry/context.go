package telemetry

import (
	"context"
)

var contextKey struct{}

func Ctx(ctx context.Context) *Telemetry {
	// Return the contextual telemetry
	if tm, ok := ctx.Value(contextKey).(*Telemetry); ok {
		return tm
	}

	// If not available, return a brand new *disabled* telemetry
	tm := New()
	tm.Disable()
	return tm
}

func (t *Telemetry) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey, t)
}
