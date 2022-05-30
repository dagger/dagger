package telemetry

import (
	"context"
)

var contextKey struct{}

func Ctx(ctx context.Context) *Telemetry {
	// Return the contextual telemetry
	if t, ok := ctx.Value(contextKey).(*Telemetry); ok {
		return t
	}

	// If not available, return a brand new *disabled* telemetry
	return New(Config{
		Enable: false,
	})
}

func (t *Telemetry) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey, t)
}
