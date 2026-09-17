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

type testCallPayloadSeenKeyStore struct {
	keys sync.Map
}

func (s *testCallPayloadSeenKeyStore) CallPayloadNeedsEmission(digest string) bool {
	_, seen := s.keys.LoadOrStore(digest, struct{}{})
	return !seen
}

func (s *testCallPayloadSeenKeyStore) CallPayloadDelivered(digest string) {
	s.keys.Store(digest, struct{}{})
}

// The two telemetry dedupe stores must be blind to each other. Payload
// decisions cover a chain's whole closure and must neither suppress spans nor
// be suppressed by the session-wide span cache.
func TestCallPayloadDedupeCannotSuppressSpans(t *testing.T) {
	ctx := context.Background()
	spanStore := &testSeenKeyStore{}
	payloadStore := &testCallPayloadSeenKeyStore{}

	if !payloadStore.CallPayloadNeedsEmission("xxh3:a") {
		t.Fatal("first payload decision must emit")
	}
	if !ShouldEmitTelemetry(ctx, spanStore, "xxh3:a", false) {
		t.Fatal("payload decision suppressed the call's span")
	}

	if !ShouldEmitTelemetry(ctx, spanStore, "xxh3:b", false) {
		t.Fatal("first span must emit")
	}
	if ShouldEmitTelemetry(ctx, spanStore, "xxh3:b", false) {
		t.Fatal("second span must be deduplicated")
	}
	if !payloadStore.CallPayloadNeedsEmission("xxh3:b") {
		t.Fatal("span dedupe suppressed the payload of a call it hid")
	}
}
