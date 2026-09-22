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

// withoutNonInternalTelemetry removes the internal flag from the context,
// so that the one-shot non-internal override does not leak into deeper selects.
func withoutNonInternalTelemetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalKey{}, nil)
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

// CallPayloadSeenKeyStore tracks immutable call payload delivery per target in
// the current route — the emitting client and its ancestors, i.e. the DBs its
// telemetry fans out to. The engine hands producers a store scoped to that
// route rather than to the session, so a claim never outlives the set of
// clients the payload was actually delivered to; a client attaching later
// still receives every frame on its first closure walk.
//
// ClaimCallPayload reports whether the payload for a digest still has to
// cross the log channel for ANY target on the route, CLAIMING those targets
// for the caller when so. It returns true at most once per digest per target
// until the claim is released, so concurrent closure walks over a shared
// chain build and encode each frame once rather than once per walk. The
// engine's log exporter settles the claim per target after persistence: a
// successful write marks the target delivered for good, a failed one releases
// it so a later walk can repair the gap.
//
// CallPayloadDelivered marks the digest delivered to every route target
// outright, for a payload that rode a recording span rather than a log.
//
// Unlike ShouldEmitTelemetry this is deliberately NOT sensitive to
// WithRepeatedTelemetry or to DoNotCache. Both exist so the same work can be
// SHOWN again — a re-run tool call is a new span worth seeing — but a
// payload is immutable data keyed by its own digest, so a second copy tells a
// client nothing it does not already have.
type CallPayloadSeenKeyStore interface {
	ClaimCallPayload(string) bool
	CallPayloadDelivered(string)
}
