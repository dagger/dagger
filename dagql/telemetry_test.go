package dagql

import (
	"context"
	"sync"
	"testing"
)

type testSeenKeyStore struct {
	keys sync.Map
}

func (s *testSeenKeyStore) LoadOrStoreTelemetrySeenKey(key string) bool {
	_, seen := s.keys.LoadOrStore(key, struct{}{})
	return seen
}

func (s *testSeenKeyStore) StoreTelemetrySeenKey(key string) {
	s.keys.Store(key, struct{}{})
}

// A payload crosses the wire at most once per session, whoever asks.
func TestShouldEmitCallPayloadIsOncePerSession(t *testing.T) {
	store := &testSeenKeyStore{}
	if !ShouldEmitCallPayload(store, "xxh3:a") {
		t.Fatal("first claim of a digest must emit")
	}
	if ShouldEmitCallPayload(store, "xxh3:a") {
		t.Fatal("second claim of the same digest must not emit")
	}
	if !ShouldEmitCallPayload(store, "xxh3:b") {
		t.Fatal("a different digest must still emit")
	}
	// No store means no session to dedupe against; emitting unboundedly is
	// worse than not emitting, since the span channel still carries payloads.
	if ShouldEmitCallPayload(nil, "xxh3:a") {
		t.Fatal("emitted without a seen-key store")
	}
}

// The two users of the seen-key store must be blind to each other. Payload
// claims cover a chain's WHOLE closure, so a shared key space would suppress
// the spans of every frame in it; and the span dedupe spending a digest must
// not spend that digest's payload, which is precisely the gap the payload
// channel exists to fill.
func TestCallPayloadDedupeCannotSuppressSpans(t *testing.T) {
	ctx := context.Background()
	store := &testSeenKeyStore{}

	// A payload claimed first (e.g. as part of some other chain's closure)
	// leaves the span decision untouched.
	if !ShouldEmitCallPayload(store, "xxh3:a") {
		t.Fatal("first payload claim must emit")
	}
	if !ShouldEmitTelemetry(ctx, store, "xxh3:a", false) {
		t.Fatal("payload claim suppressed the call's span")
	}

	// And the span dedupe spending a digest leaves the payload claimable.
	if !ShouldEmitTelemetry(ctx, store, "xxh3:b", false) {
		t.Fatal("first span must emit")
	}
	if ShouldEmitTelemetry(ctx, store, "xxh3:b", false) {
		t.Fatal("second span must be deduplicated")
	}
	if !ShouldEmitCallPayload(store, "xxh3:b") {
		t.Fatal("span dedupe suppressed the payload of a call it hid")
	}
}
