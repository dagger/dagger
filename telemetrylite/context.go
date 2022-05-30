package telemetrylite

import (
	"context"
)

var contextKey struct{}

func Ctx(ctx context.Context) *TelemetryLite {
	// Return the contextual telemetry
	if t, ok := ctx.Value(contextKey).(*TelemetryLite); ok {
		return t
	}

	// If not available, return a brand new *disabled* telemetry
	t := New()
	t.Disable()
	return t
}

func (t *TelemetryLite) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey, t)
}
