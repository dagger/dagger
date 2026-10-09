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
// be delivered to ANY target on the route, CLAIMING those targets for the
// caller when so. It returns true at most once per digest per target until
// the claim is released, so concurrent closure walks over a shared chain
// build and encode each frame once rather than once per walk. The claimant
// then delivers the frame on exactly one carrier — its recording span's
// dagger.io/dag.call attribute, or a payload log record — and the engine's
// exporter for that carrier settles the claim per target after persistence:
// a successful write marks the target delivered for good, a failed one
// releases it so a retry or a later walk can repair the gap.
//
// Unlike ShouldEmitTelemetry this is deliberately NOT sensitive to
// WithRepeatedTelemetry or to DoNotCache. Both exist so the same work can be
// SHOWN again — a re-run tool call is a new span worth seeing — but a
// payload is immutable data keyed by its own digest, so a second copy tells a
// client nothing it does not already have.
type CallPayloadSeenKeyStore interface {
	ClaimCallPayload(string) bool
}

// CallPayloadClosureStore is an optional extension of CallPayloadSeenKeyStore
// that remembers which digests have had their whole recipe closure claimed,
// so a closure walk can stop there instead of re-walking a deep chain on
// every new call that extends it. Without it, every walk visits the whole
// closure and claims frame by frame.
type CallPayloadClosureStore interface {
	CallPayloadSeenKeyStore

	// CallPayloadReleaseEpoch identifies the current set of claims: it changes
	// whenever a claim is released, which can leave a covered closure with a
	// gap again.
	CallPayloadReleaseEpoch() uint64

	// CallPayloadClosureCovered reports whether a completed walk has claimed
	// the digest and its whole closure for every target of the route, with no
	// claim released since.
	CallPayloadClosureCovered(digest string) bool

	// CoverCallPayloadClosures records that a walk that started at epoch has
	// claimed every digest and its whole closure for every target of the
	// route. It records nothing and returns false if a claim was released
	// since epoch, so a walk that skipped covered closures knows to claim
	// over them again.
	CoverCallPayloadClosures(digests []string, epoch uint64) bool

	// StartCallPayloadRepair reports whether a walk whose root someone else
	// already claimed must walk the root's closure anyway, and if so counts
	// that walk as started. A root's claim normally proves its claimant
	// walked the closure, but a payload the exporter gave up on may sit
	// inside it; until something claims that payload again, each root walks
	// once per loss.
	StartCallPayloadRepair(root string) bool
}
