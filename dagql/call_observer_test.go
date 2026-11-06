package dagql

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallObserverPreparesHitsAndHiddenTelemetry(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	type observerKey struct{}
	var observed, bodies int
	fact := ResultFact{Namespace: "test", Payload: json.RawMessage(`{"path":"/input"}`)}
	srv.ObserveCalls(func(ctx context.Context, req *CallRequest) context.Context {
		observed++
		req.ResultFacts = append(req.ResultFacts, fact)
		return context.WithValue(ctx, observerKey{}, req.Field)
	})
	value := Func("value", func(ctx context.Context, _ *persistCodecRoot, _ struct{}) (Int, error) {
		bodies++
		require.Equal(t, "value", ctx.Value(observerKey{}))
		return NewInt(42), nil
	})
	value.Spec.NoTelemetry = true
	Fields[*persistCodecRoot]{value}.Install(srv)
	for range 2 {
		result, err := srv.Root().Select(ctx, srv, Selector{Field: "value"})
		require.NoError(t, err)
		facts, complete, err := cache.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: uint64(result.cacheSharedResult().id)}, "test")
		require.NoError(t, err)
		require.True(t, complete)
		require.Equal(t, []ResultFact{fact}, facts)
	}
	fork, err := srv.Fork(ctx, &persistCodecRoot{})
	require.NoError(t, err)
	var result Int
	require.NoError(t, fork.Select(ctx, fork.Root(), &result, Selector{Field: "value"}))
	require.Equal(t, 3, observed, "cache hits and forks use the same preparation hook")
	require.Equal(t, 1, bodies)
}
