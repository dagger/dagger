package dagql

import (
	"context"
	"sync"
)

type TelemetrySeenKeyStore interface {
	LoadOrStoreTelemetrySeenKey(string) bool
	StoreTelemetrySeenKey(string)
}

type seenKeysCtxKey struct{}

// WithRepeatedTelemetry resets the state of seen cache keys so that we emit
// telemetry for spans that we've already seen within the session.
//
// This is useful in scenarios where we want to see actions performed, even if
// they had been performed already (e.g. an LLM running tools).
//
// Additionally, it explicitly sets the internal flag to false, to prevent
// Server.Select from marking its spans internal.
func WithRepeatedTelemetry(ctx context.Context) context.Context {
	return WithNonInternalTelemetry(
		context.WithValue(ctx, seenKeysCtxKey{}, &sync.Map{}),
	)
}

// WithNonInternalTelemetry marks telemetry within the context as non-internal,
// so that Server.Select does not mark its spans internal.
func WithNonInternalTelemetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalKey{}, false)
}

func telemetryKeys(ctx context.Context) *sync.Map {
	if v := ctx.Value(seenKeysCtxKey{}); v != nil {
		return v.(*sync.Map)
	}
	return nil
}

func ShouldEmitTelemetry(ctx context.Context, store TelemetrySeenKeyStore, callKey string, doNotCache bool) bool {
	keys := telemetryKeys(ctx)
	seen := false
	switch {
	case keys != nil:
		_, seen = keys.LoadOrStore(callKey, struct{}{})
	case store != nil:
		seen = store.LoadOrStoreTelemetrySeenKey(callKey)
	}
	if seen && !doNotCache {
		return false
	}
	if keys != nil && store != nil {
		store.StoreTelemetrySeenKey(callKey)
	}
	return true
}
