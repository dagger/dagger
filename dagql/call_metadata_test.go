package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func metadataTestOrigin(t *testing.T, ctx context.Context, c *Cache, srv *Server, name string, content digest.Digest) (*CallRequest, AnyResult) {
	t.Helper()
	frame := cacheTestIntCall(name)
	req := &CallRequest{ResultCall: frame, IsPersistable: true}
	result, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(ctx context.Context) (AnyResult, error) {
		value := cacheTestIntResult(frame, 1).(Result[Int])
		if content != "" {
			return value.WithContentDigest(ctx, content)
		}
		return value, nil
	})
	require.NoError(t, err)
	return req, result
}

func TestCallMetadataKeepsOriginsAcrossContentReuseAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	content := digest.FromString("same bytes from different paths")
	a, aResult := metadataTestOrigin(t, ctx, cache, srv, "source-a", content)
	b, _ := metadataTestOrigin(t, ctx, cache, srv, "source-b", content)
	aPayload := json.RawMessage(`{"paths":["/a"]}`)
	bPayload := json.RawMessage(`{"paths":["/b"]}`)
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, a, "source-test", "candidate", aPayload))
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, b, "source-test", "candidate", bPayload))
	alias := &CallRequest{ResultCall: cacheTestIntCall("alias"), IsPersistable: true}
	aliasResult, err := cache.GetOrInitCall(ctx, "test-session", srv, alias, func(context.Context) (AnyResult, error) {
		return aResult, nil
	})
	require.NoError(t, err)
	require.Same(t, aResult.cacheSharedResult(), aliasResult.cacheSharedResult())
	aliasPayload := json.RawMessage(`{"paths":["/alias"]}`)
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, alias, "source-test", "candidate", aliasPayload))
	resultID, err := cache.PersistedResultID(aResult)
	require.NoError(t, err)

	for cycle := range 2 {
		got, ok, err := cache.CallMetadata(ctx, "test-session", "source-test", a.ResultCall)
		require.NoError(t, err)
		require.True(t, ok)
		require.JSONEq(t, string(aPayload), string(got))
		got[0] = 'x'
		got, ok, err = cache.CallMetadata(ctx, "test-session", "source-test", b.ResultCall)
		require.NoError(t, err)
		require.True(t, ok)
		require.JSONEq(t, string(bPayload), string(got))

		// Output equality must not manufacture an observation of an unseen call.
		unseen := cacheTestIntCall("unseen", call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: content})
		_, ok, err = cache.CallMetadata(ctx, "test-session", "source-test", unseen)
		require.NoError(t, err)
		require.False(t, ok)
		records, err := cache.CallMetadataCandidates(ctx, "test-session", "source-test", "candidate")
		require.NoError(t, err)
		require.Len(t, records, 3)
		origins, err := cache.CallMetadataForResult(ctx, "test-session", "source-test", &ResultCallRef{ResultID: resultID})
		require.NoError(t, err)
		require.Len(t, origins, 3, "an adopted producer retains all recorded alias and equivalent origins")
		assertCacheOwnershipExact(t, cache)
		if cycle == 0 {
			require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
			require.NoError(t, cache.Close(ctx))
			ctx, cache, srv = persistedListTestCache(t, path)
		}
	}
}

func TestCallMetadataPreservesSessionResourceRequirements(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	origin, source := metadataTestOrigin(t, ctx, cache, srv, "resource", "")
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, origin, "test", "key", json.RawMessage(`{"path":"/a"}`)))
	handle := cacheTestVolatileSessionResourceHandle("metadata-resource")
	require.NoError(t, cache.BindSessionResource(ctx, "test-session", "dagql-test-client", handle, "present"))
	currentEntryTestRequireHandle(t, ctx, cache, srv, "test-session", source, handle)
	_, ok, err := cache.CallMetadata(ctx, "test-session", "test", origin.ResultCall)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = cache.CallMetadata(ctx, "other-session", "test", origin.ResultCall)
	require.NoError(t, err)
	require.False(t, ok, "metadata must not bypass its source's grown resource requirements")
	assertCacheOwnershipExact(t, cache)
}

func TestCallMetadataMissingConflictingAndUncached(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	origin, _ := metadataTestOrigin(t, ctx, cache, srv, "origin", "")
	_, ok, err := cache.CallMetadata(ctx, "test-session", "test", origin.ResultCall)
	require.NoError(t, err)
	require.False(t, ok, "older results without observations stay unknown")
	origin.DoNotCache = true
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, origin, "test", "key", json.RawMessage(`{"path":"/a"}`)))
	origin.DoNotCache = false
	_, ok, err = cache.CallMetadata(ctx, "test-session", "test", origin.ResultCall)
	require.NoError(t, err)
	require.False(t, ok)
	for _, payload := range []json.RawMessage{json.RawMessage(`{"path":"/a"}`), json.RawMessage(`{"path":"/b"}`)} {
		require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, origin, "test", "key", payload))
	}
	_, ok, err = cache.CallMetadata(ctx, "test-session", "test", origin.ResultCall)
	require.NoError(t, err)
	require.False(t, ok, "opaque conflicting observations cannot prove coverage")
	records, err := cache.CallMetadataCandidates(ctx, "test-session", "test", "key")
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestCallMetadataCannotRenewOriginExpiry(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	origin, source := metadataTestOrigin(t, ctx, cache, srv, "expiry", "")
	origin.TTL = 60
	deadline := time.Now().Unix() + 5
	cache.egraphMu.Lock()
	source.cacheSharedResult().expiresAtUnix = deadline
	cache.egraphMu.Unlock()
	payload := json.RawMessage(`{"path":"/a"}`)
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, origin, "test", "key", payload))
	records, err := cache.CallMetadataCandidates(ctx, "test-session", "test", "key")
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, deadline, records[0].ExpiresAtUnix)
	cache.egraphMu.Lock()
	for _, row := range cache.resultsByID {
		if frame := row.loadResultCall(); frame != nil && frame.SyntheticOp == callMetadataOp {
			row.expiresAtUnix = time.Now().Unix() - 1
		}
	}
	cache.egraphMu.Unlock()
	_, ok, err := cache.CallMetadata(ctx, "test-session", "test", origin.ResultCall)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestCallObserverSeesHitsErrorsAndHiddenTelemetry(t *testing.T) {
	ctx, _, srv := persistedListTestCache(t, "")
	type observerKey struct{}
	type observation struct {
		field string
		hit   bool
		err   error
	}
	var mu sync.Mutex
	var seen []observation
	srv.ObserveCalls(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, error)) {
		return context.WithValue(ctx, observerKey{}, req.Field), func(result AnyResult, err error) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, observation{field: req.Field, hit: result != nil && result.HitCache(), err: err})
		}
	})
	value := Func("value", func(ctx context.Context, _ *persistCodecRoot, _ struct{}) (Int, error) {
		require.Equal(t, "value", ctx.Value(observerKey{}))
		return NewInt(42), nil
	})
	value.Spec.NoTelemetry = true
	sentinel := errors.New("missing source")
	Fields[*persistCodecRoot]{
		value,
		Func("missing", func(context.Context, *persistCodecRoot, struct{}) (Int, error) { return 0, sentinel }),
	}.Install(srv)
	for range 2 {
		var result Int
		require.NoError(t, srv.Select(ctx, srv.Root(), &result, Selector{Field: "value"}))
		require.Equal(t, NewInt(42), result)
	}
	var result Int
	require.ErrorIs(t, srv.Select(ctx, srv.Root(), &result, Selector{Field: "missing"}), sentinel)
	fork, err := srv.Fork(ctx, &persistCodecRoot{})
	require.NoError(t, err)
	require.NoError(t, fork.Select(ctx, fork.Root(), &result, Selector{Field: "value"}))
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 4)
	require.False(t, seen[0].hit)
	require.True(t, seen[1].hit)
	require.Equal(t, "missing", seen[2].field)
	require.ErrorIs(t, seen[2].err, sentinel)
	require.True(t, seen[3].hit, "forks inherit the observer")
}
