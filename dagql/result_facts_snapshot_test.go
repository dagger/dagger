package dagql

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type mutatingFactDecoder struct{}

func (*mutatingFactDecoder) UnmarshalJSON(data []byte) error {
	data[0] = '!'
	return nil
}

func TestResultFactsSnapshotGenerations(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	frame := cacheTestIntCall("facts-snapshot")
	fact := ResultFact{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/old"}`)}
	value, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, ResultFacts: []ResultFact{fact}}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(Int(1), frame)
	})
	require.NoError(t, err)
	row := value.cacheSharedResult()
	ref := &ResultCallRef{ResultID: uint64(row.id)}
	snapshot, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	require.True(t, snapshot.Available())
	require.True(t, snapshot.Complete())
	require.Equal(t, ref.ResultID, snapshot.ResultID())
	require.Equal(t, 1, snapshot.Len())
	require.NoError(t, snapshot.Decode(0, &mutatingFactDecoder{}))
	var decoded map[string]string
	require.NoError(t, snapshot.Decode(0, &decoded))
	require.Equal(t, "/old", decoded["path"], "application decoders cannot mutate shared JSON")
	require.Error(t, snapshot.Decode(-1, &decoded))
	require.Error(t, snapshot.Decode(1, &decoded))
	copy := snapshot.Call()
	copy.Field = "changed-by-reader"
	require.Equal(t, frame.Field, snapshot.Call().Field)

	check := func(want bool, tokens ...ResultFactsToken) {
		t.Helper()
		valid, err := cache.ValidateResultFactsSnapshots(ctx, "test-session", tokens)
		require.NoError(t, err)
		require.Equal(t, want, valid)
	}
	check(true, snapshot.Token(), snapshot.Token())
	again, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	require.Equal(t, snapshot.Token(), again.Token())
	empty, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "other")
	require.NoError(t, err)
	require.Zero(t, empty.Len())
	require.True(t, empty.Complete(), "coverage is independent of namespace contents")
	require.Equal(t, snapshot.Token(), empty.Token())

	cache.egraphMu.Lock()
	cache.setResultFactsLocked(row, []ResultFact{fact}, false)
	cache.egraphMu.Unlock()
	check(true, snapshot.Token())
	cache.egraphMu.Lock()
	cache.setResultFactsLocked(row, []ResultFact{fact}, true)
	cache.egraphMu.Unlock()
	check(false, snapshot.Token())
	incomplete, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	require.False(t, incomplete.Complete())
	check(true, incomplete.Token())

	cache.egraphMu.Lock()
	cache.setResultFactsLocked(row, []ResultFact{{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/new"}`)}}, false)
	cache.egraphMu.Unlock()
	check(false, incomplete.Token())
	require.True(t, snapshot.Complete(), "old views remain immutable after replacement")
	require.NoError(t, snapshot.Decode(0, &decoded))
	require.Equal(t, "/old", decoded["path"])
	current, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	require.NoError(t, current.Decode(0, &decoded))
	require.Equal(t, "/new", decoded["path"])

	// Restoring the same pointer must not restore an old frame token.
	original := row.loadResultCall()
	row.storeResultCall(original.clone())
	row.storeResultCall(original)
	check(false, current.Token())
	current, err = cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	row.payloadMu.Lock()
	row.payloadRevision++
	row.payloadMu.Unlock()
	check(false, current.Token())
	current, err = cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	cache.egraphMu.Lock()
	cache.resultsByID[row.id] = &sharedResult{id: row.id}
	cache.egraphMu.Unlock()
	check(false, current.Token())
	cache.egraphMu.Lock()
	cache.resultsByID[row.id] = row
	cache.egraphMu.Unlock()
	_, other, _ := persistedListTestCache(t, "")
	valid, err := other.ValidateResultFactsSnapshots(ctx, "test-session", []ResultFactsToken{current.Token()})
	require.NoError(t, err)
	require.False(t, valid, "tokens belong to one cache")
	check(false, ResultFactsToken{})
}

func TestResultFactsSnapshotAccessAndLifecycle(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	frame := cacheTestIntCall("facts-snapshot-access")
	value, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(Int(1), frame)
	})
	require.NoError(t, err)
	row := value.cacheSharedResult()
	ref := &ResultCallRef{ResultID: uint64(row.id)}
	snapshot, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	cache.egraphMu.Lock()
	row.sessionResourceHandle = "facts-socket"
	_, err = cache.recomputeRequiredSessionResourcesLocked(row)
	cache.egraphMu.Unlock()
	require.NoError(t, err)
	_, err = cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.ErrorContains(t, err, "unavailable to session")
	valid, err := cache.ValidateResultFactsSnapshots(ctx, "test-session", []ResultFactsToken{snapshot.Token()})
	require.ErrorContains(t, err, "unavailable to session")
	require.False(t, valid)
	require.NoError(t, cache.BindSessionResource(ctx, "test-session", "facts-client", "facts-socket", "bound"))
	valid, err = cache.ValidateResultFactsSnapshots(ctx, "test-session", []ResultFactsToken{snapshot.Token()})
	require.NoError(t, err)
	require.True(t, valid, "access is checked now, not cached in the token")
	_, err = cache.ResultFactsSnapshot(ctx, "test-session", &ResultCallRef{Call: frame}, "source-test")
	require.ErrorIs(t, err, ErrResultFactsSnapshotUnsupported)
	_, err = cache.ResultFactsSnapshot(ctx, "test-session", &ResultCallRef{ResultID: 1 << 60}, "source-test")
	require.ErrorContains(t, err, "unavailable to session")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = cache.ResultFactsSnapshot(canceled, "test-session", ref, "source-test")
	require.ErrorIs(t, err, context.Canceled)
	_, err = cache.ValidateResultFactsSnapshots(canceled, "test-session", []ResultFactsToken{snapshot.Token()})
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	_, err = cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.ErrorIs(t, err, ErrCacheSessionReleased)
	_, err = cache.ValidateResultFactsSnapshots(ctx, "test-session", []ResultFactsToken{snapshot.Token()})
	require.ErrorIs(t, err, ErrCacheSessionReleased)
}

func TestResultFactsSnapshotNullAndPruning(t *testing.T) {
	ctx, cache, srv := transferTestCache(t)
	frame := cacheTestIntCall("facts-snapshot-null")
	frame.Type.NonNull = false
	fact := ResultFact{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/null"}`)}
	value, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true, ResultFacts: []ResultFact{fact}}, func(context.Context) (AnyResult, error) {
		return nil, nil
	})
	require.NoError(t, err)
	ref := &ResultCallRef{ResultID: uint64(value.cacheSharedResult().id)}
	snapshot, err := cache.ResultFactsSnapshot(ctx, "test-session", ref, "source-test")
	require.NoError(t, err)
	require.True(t, snapshot.Available(), "a cached null is an available value")
	require.True(t, snapshot.Complete())
	require.Equal(t, 1, snapshot.Len())

	bundle := exportTestBundle(t, ctx, cache, value)
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cloudCtx, "facts-snapshot-engine", bundle)
	require.NoError(t, err)
	cloudRef := &ResultCallRef{ResultID: reply.Imported()[0].ResultID}
	before, err := cloud.ResultFactsSnapshot(cloudCtx, "facts-inspect", cloudRef, "source-test")
	require.NoError(t, err)
	require.True(t, before.Available())
	_, removed, dropped, err := cloud.removePrunedEdge(cloudCtx, sharedResultID(cloudRef.ResultID))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 1, dropped)
	valid, err := cloud.ValidateResultFactsSnapshots(cloudCtx, "facts-inspect", []ResultFactsToken{before.Token()})
	require.NoError(t, err)
	require.False(t, valid)
	holding, err := cloud.ResultFactsSnapshot(cloudCtx, "facts-inspect", cloudRef, "source-test")
	require.NoError(t, err)
	require.False(t, holding.Available())
	require.False(t, holding.Complete())
	require.Zero(t, holding.Len())
	valid, err = cloud.ValidateResultFactsSnapshots(cloudCtx, "facts-inspect", []ResultFactsToken{holding.Token()})
	require.NoError(t, err)
	require.True(t, valid)
	_, err = cloud.MergeValues(cloudCtx, "facts-snapshot-engine", bundle)
	require.NoError(t, err)
	valid, err = cloud.ValidateResultFactsSnapshots(cloudCtx, "facts-inspect", []ResultFactsToken{holding.Token()})
	require.NoError(t, err)
	require.False(t, valid, "restoring availability invalidates negative coverage")
}
