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

// callPayloadSeenKeyPrefix namespaces call-payload dedupe keys inside the
// session's seen-key store. The two uses of that store must never touch each
// other's keys: ShouldEmitTelemetry's keys are bare call digests and decide
// whether a SPAN is emitted at all, while these decide whether a call PAYLOAD
// still needs to cross the log channel. Claiming a payload digest is done for
// every frame of a chain's transitive closure, so sharing the key space would
// suppress the spans of every one of those frames; conversely span dedupe must
// remain independent because a suppressed span still needs its log payload.
// A prefix that cannot occur in a digest keeps the two disjoint by
// construction.
const callPayloadSeenKeyPrefix = "dag.call.payload:"

// ShouldEmitCallPayload reports whether the payload for callDigest still has
// to reach clients through the given claim store, CLAIMING it for the caller
// when so: it returns true at most once per digest per store scope, to
// whoever asks first. The store defines the scope — the engine hands this a
// store scoped to the emitting client's delivery domain (the client and its
// ancestors, i.e. the DBs the record actually fans out to), so a claim never
// outlives the set of clients it was delivered to.
//
// Producers claim every root and transitive frame through this function before
// carrying it on a span or emitting its log record. Consumers may also populate
// the same payload store from legacy dagger.io/dag.call span attributes, but
// those ingested attributes do not participate in producer-side claims.
//
// Unlike ShouldEmitTelemetry this is deliberately NOT sensitive to
// WithRepeatedTelemetry or to DoNotCache. Both exist so the same work can be
// SHOWN again — a re-run tool call is a new span worth seeing — but a
// payload is immutable data keyed by its own digest, so a second copy tells a
// client nothing it does not already have.
//
// With no store there is no session to dedupe against, so nothing is emitted
// rather than emitting unboundedly.
func ShouldEmitCallPayload(store TelemetrySeenKeyStore, callDigest string) bool {
	if store == nil || callDigest == "" {
		return false
	}
	return !store.LoadOrStoreTelemetrySeenKey(callPayloadSeenKeyPrefix + callDigest)
}
