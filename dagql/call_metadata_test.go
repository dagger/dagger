package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
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

func TestCallMetadataConflictAcrossCandidateKeys(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	origin, _ := metadataTestOrigin(t, ctx, cache, srv, "cross-key-conflict", "")
	for _, key := range []string{"a", "b"} {
		require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, origin, "test", key, json.RawMessage(`{"path":"/a"}`)))
	}
	for _, key := range []string{"a", "b"} {
		records, err := cache.CallMetadataCandidates(ctx, "test-session", "test", key)
		require.NoError(t, err)
		require.Empty(t, records, "a different candidate key must not hide a conflicting origin")
	}
}

func TestCallMetadataRetainsAdoptedCallInputsAndPrunesIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	_, output := metadataTestOrigin(t, ctx, cache, srv, "adopted-output", "")
	baselineFrame := cacheTestIntCall("unpersisted-baseline")
	baseline, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: baselineFrame}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(baselineFrame, 9), nil
	})
	require.NoError(t, err)
	baselineID := baseline.cacheSharedResult().id
	origin := &CallRequest{ResultCall: cacheTestIntCall("adopted-with-input"), IsPersistable: true}
	origin.Args = []*ResultCallArg{{Name: "baseline", Value: &ResultCallLiteral{
		Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: uint64(baselineID)},
	}}}
	adopted, err := cache.GetOrInitCall(ctx, "test-session", srv, origin, func(context.Context) (AnyResult, error) { return output, nil })
	require.NoError(t, err)
	require.Same(t, output.cacheSharedResult(), adopted.cacheSharedResult())
	require.NoError(t, cache.StoreCallMetadata(ctx, "test-session", srv, origin, "test", "key", json.RawMessage(`{"complete":true}`), adopted))
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NotNil(t, cache.resultsByID[baselineID], "metadata retains discarded inputs even when its output was adopted")
	require.NoError(t, cache.Close(ctx))
	ctx, cache, _ = persistedListTestCache(t, path)
	require.NotNil(t, cache.resultsByID[baselineID], "the retained baseline survives persistence")
	records, err := cache.CallMetadataCandidates(ctx, "test-session", "test", "key")
	require.NoError(t, err)
	require.Len(t, records, 1, "restore installs metadata indexes before values are decoded")
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	report, err := cache.PruneMetadataEstimate(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, report.Triggered)
	cache.egraphMu.RLock()
	defer cache.egraphMu.RUnlock()
	require.Empty(t, cache.callMetadata.records)
	require.Empty(t, cache.callMetadata.byOrigin)
	require.Empty(t, cache.callMetadata.byCandidate)
	require.Empty(t, cache.callMetadata.byResult)
}

func TestCallMetadataIndexesImportedRows(t *testing.T) {
	ctx, source, srv := transferTestCache(t)
	origin, _ := metadataTestOrigin(t, ctx, source, srv, "imported-observation", "")
	require.NoError(t, source.StoreCallMetadata(ctx, "test-session", srv, origin, "test", "key", json.RawMessage(`{"path":"/a"}`)))
	var metadataID sharedResultID
	source.egraphMu.RLock()
	for id := range source.callMetadata.records {
		metadataID = id
	}
	source.egraphMu.RUnlock()
	metadata, err := source.LoadResultByResultID(ctx, "test-session", srv, uint64(metadataID))
	require.NoError(t, err)
	bundle := exportTestBundle(t, ctx, source, metadata)
	targetCtx, target, targetSrv := transferTestCache(t)
	for _, name := range []string{"padding-a", "padding-b", "padding-c"} {
		_, _ = metadataTestOrigin(t, targetCtx, target, targetSrv, name, "")
	}
	reply, err := target.MergeValues(targetCtx, cloudCacheID, bundle)
	require.NoError(t, err)
	records, err := target.CallMetadataCandidates(targetCtx, "test-session", "test", "key")
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.JSONEq(t, `{"path":"/a"}`, string(records[0].Payload))
	frame, err := target.ResultCallForRef(&ResultCallRef{ResultID: reply.Imported()[0].ResultID})
	require.NoError(t, err)
	records, err = target.CallMetadataForResult(targetCtx, "test-session", "test", frame.Receiver)
	require.NoError(t, err)
	require.Len(t, records, 1, "result lookup uses the relocated output ID")

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
		field    string
		hit      bool
		executed bool
		err      error
	}
	var mu sync.Mutex
	var seen []observation
	srv.ObserveCalls(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, error)) {
		return context.WithValue(ctx, observerKey{}, req.Field), func(result AnyResult, executed bool, err error) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, observation{field: req.Field, hit: result != nil && result.HitCache(), executed: executed, err: err})
		}
	})
	value := Func("value", func(ctx context.Context, _ *persistCodecRoot, _ struct{}) (Int, error) {
		require.Equal(t, "value", ctx.Value(observerKey{}))
		return NewInt(42), nil
	})
	value.Spec.NoTelemetry = true
	sentinel := errors.New("missing source")
	var cached AnyResult
	Fields[*persistCodecRoot]{
		value,
		Func("missing", func(context.Context, *persistCodecRoot, struct{}) (Int, error) { return 0, sentinel }),
		{
			Spec: &FieldSpec{Name: "adopted", Type: NewInt(0)},
			Func: func(context.Context, ObjectResult[*persistCodecRoot], map[string]Input, call.View) (AnyResult, error) {
				return cached, nil
			},
		},
	}.Install(srv)
	for range 2 {
		var err error
		cached, err = srv.Root().Select(ctx, srv, Selector{Field: "value"})
		require.NoError(t, err)
		require.Equal(t, NewInt(42), cached.Unwrap())
	}
	require.True(t, cached.HitCache())
	adopted, err := srv.Root().Select(ctx, srv, Selector{Field: "adopted"})
	require.NoError(t, err)
	require.Same(t, cached.cacheSharedResult(), adopted.cacheSharedResult())
	var result Int
	require.ErrorIs(t, srv.Select(ctx, srv.Root(), &result, Selector{Field: "missing"}), sentinel)
	fork, err := srv.Fork(ctx, &persistCodecRoot{})
	require.NoError(t, err)
	require.NoError(t, fork.Select(ctx, fork.Root(), &result, Selector{Field: "value"}))
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 5)
	require.False(t, seen[0].hit)
	require.True(t, seen[0].executed)
	require.True(t, seen[1].hit)
	require.False(t, seen[1].executed)
	require.Equal(t, "adopted", seen[2].field)
	require.True(t, seen[2].executed, "a fresh resolver can return an already cached result")
	require.Equal(t, "missing", seen[3].field)
	require.True(t, seen[3].executed)
	require.ErrorIs(t, seen[3].err, sentinel)
	require.True(t, seen[4].hit, "forks inherit the observer")
	require.False(t, seen[4].executed)
}

func TestCallObserverDistinguishesJoinedCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, _, srv := persistedListTestCache(t, "")
		started, release := make(chan struct{}), make(chan struct{})
		Fields[*persistCodecRoot]{Func("shared", func(context.Context, *persistCodecRoot, struct{}) (Int, error) {
			close(started)
			<-release
			return NewInt(42), nil
		})}.Install(srv)
		var mu sync.Mutex
		var executions []bool
		var hits []bool
		srv.ObserveCalls(func(ctx context.Context, _ *CallRequest) (context.Context, func(AnyResult, bool, error)) {
			return ctx, func(result AnyResult, executed bool, _ error) {
				mu.Lock()
				defer mu.Unlock()
				executions = append(executions, executed)
				hits = append(hits, result != nil && result.HitCache())
			}
		})
		results := make(chan error, 2)
		selectShared := func() {
			var result Int
			results <- srv.Select(ctx, srv.Root(), &result, Selector{Field: "shared"})
		}
		go selectShared()
		<-started
		go selectShared()
		synctest.Wait()
		close(release)
		require.NoError(t, <-results)
		require.NoError(t, <-results)
		mu.Lock()
		defer mu.Unlock()
		require.ElementsMatch(t, []bool{true, false}, executions)
		require.Equal(t, []bool{false, false}, hits, "a joined call need not report a cache hit")
	})
}
