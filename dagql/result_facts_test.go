package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestCompletedResultFactsSurviveProducerCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cache, srv := persistedListTestCache(t, "")
		frame := cacheTestIntCall("completed-source-facts")
		prepared := ResultFact{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/producer"}`)}
		queried := ResultFact{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/queried"}`)}
		waiterFact := ResultFact{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/waiter"}`)}
		producer := &CallRequest{ResultCall: frame, ConcurrencyKey: "completed-source-facts", ResultFacts: []ResultFact{prepared}}
		started, release := make(chan struct{}), make(chan struct{})
		producerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		producerDone := make(chan error, 1)
		go func() {
			_, err := cache.GetOrInitCall(producerCtx, "test-session", srv, producer, func(context.Context) (AnyResult, error) {
				close(started)
				<-release
				producer.ResultFacts = append(producer.ResultFacts, queried)
				return cacheTestIntResult(frame, 42), nil
			})
			producerDone <- err
		}()
		<-started

		type outcome struct {
			result AnyResult
			err    error
		}
		waiter := producer.Clone()
		waiter.ResultFacts = []ResultFact{waiterFact}
		waiterDone := make(chan outcome, 1)
		go func() {
			result, err := cache.GetOrInitCall(ctx, "test-session", srv, waiter, func(context.Context) (AnyResult, error) {
				t.Error("waiter must reuse the producer's execution")
				return nil, nil
			})
			waiterDone <- outcome{result, err}
		}()
		synctest.Wait()
		cache.callsMu.Lock()
		var waiters int
		for _, call := range cache.ongoingCalls {
			waiters += call.waiters
		}
		cache.callsMu.Unlock()
		require.Equal(t, 2, waiters)
		cancel()
		require.ErrorIs(t, <-producerDone, context.Canceled)
		close(release)
		got := <-waiterDone
		require.NoError(t, got.err)
		facts, complete, err := cache.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: uint64(got.result.cacheSharedResult().id)}, "source-test")
		require.NoError(t, err)
		require.True(t, complete)
		require.ElementsMatch(t, []ResultFact{prepared, queried, waiterFact}, facts)
	})
}

func TestResultFactsFollowEquivalentValuesAcrossCollectionRestartAndImport(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "facts.db")
	ctx, cache, srv := persistedListTestCache(t, dbPath)
	preparedFrame := cacheTestIntCall("facts-prepared")
	prepared := &CallRequest{ResultCall: preparedFrame, IsPersistable: true, ResultFacts: []ResultFact{{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/prepared"}`)}}}
	var completedRecord PersistedRecord
	var completedCaptureErr error
	completionSeen := false
	cache.testBeforeSnapshotShareCompletion = func(row *sharedResult) {
		if row.loadResultCall().Field == preparedFrame.Field {
			completionSeen = true
			completedRecord, completedCaptureErr = cache.CapturePersistedRecord(ctx, Result[Typed]{shared: row})
		}
	}
	preparedResult, err := cache.GetOrInitCall(ctx, "test-session", srv, prepared, func(context.Context) (AnyResult, error) {
		return NewResultForCall(Int(17), preparedFrame)
	})
	require.NoError(t, err)
	cache.testBeforeSnapshotShareCompletion = nil
	require.True(t, completionSeen)
	require.NoError(t, completedCaptureErr)
	require.Equal(t, resultFactsCoverageVersion, completedRecord.Envelope.FactsCoverageVersion)
	require.Equal(t, prepared.ResultFacts, completedRecord.Envelope.ResultFacts, "sharing sees prepared facts before publication returns")
	hitRequest := prepared.Clone()
	hitRequest.ResultFacts = []ResultFact{{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/prepared-hit"}`)}}
	hitRequest.ResultFactsIncomplete = true
	hit, err := cache.GetOrInitCall(ctx, "test-session", srv, hitRequest, func(context.Context) (AnyResult, error) {
		t.Error("prepared annotations must not change cache identity")
		return nil, nil
	})
	require.NoError(t, err)
	require.Same(t, preparedResult.cacheSharedResult(), hit.cacheSharedResult())
	hitFacts, complete, err := cache.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: uint64(hit.cacheSharedResult().id)}, "source-test")
	require.NoError(t, err)
	require.Len(t, hitFacts, 2)
	require.False(t, complete)
	a := persistedListTestResult(t, ctx, cache, srv, "facts-a", Int(1))
	b := persistedListTestResult(t, ctx, cache, srv, "facts-b", Int(1))
	aID, bID := a.cacheSharedResult().id, b.cacheSharedResult().id
	require.NotEqual(t, aID, bID)
	applyFacts := func(ctx context.Context, cache *Cache, result AnyResult, frame *ResultCall, facts []ResultFact, incomplete bool) {
		t.Helper()
		_, err := cache.GetOrInitCall(ctx, "test-session", CurrentDagqlServer(ctx), &CallRequest{
			ResultCall: frame.clone(), IsPersistable: true, ResultFacts: facts, ResultFactsIncomplete: incomplete,
		}, func(context.Context) (AnyResult, error) { return result, nil })
		require.NoError(t, err)
	}
	annotate := func(ctx context.Context, cache *Cache, result AnyResult, frame *ResultCall, source string) {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"path": source})
		require.NoError(t, err)
		applyFacts(ctx, cache, result, frame, []ResultFact{{Namespace: "source-test", Payload: payload}}, false)
	}
	markIncomplete := func(ctx context.Context, cache *Cache, result AnyResult) {
		t.Helper()
		origin, err := result.ResultCall()
		require.NoError(t, err)
		applyFacts(ctx, cache, result, origin, nil, true)
	}
	check := func(ctx context.Context, cache *Cache, id sharedResultID, count int, complete bool) {
		t.Helper()
		facts, covered, err := cache.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: uint64(id)}, "source-test")
		require.NoError(t, err)
		require.Len(t, facts, count)
		require.Equal(t, complete, covered)
		total, sum := cacheTestPayloadTotalConsistent(cache)
		require.Equal(t, sum, total, "fact changes preserve payload accounting")
		if len(facts) != 0 {
			facts[0].Payload[0] = 'x'
			again, _, err := cache.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: uint64(id)}, "source-test")
			require.NoError(t, err)
			require.True(t, json.Valid(again[0].Payload), "readers cannot mutate stored facts")
		}
	}
	aFrame, err := a.ResultCall()
	require.NoError(t, err)
	bFrame, err := b.ResultCall()
	require.NoError(t, err)
	annotate(ctx, cache, a, aFrame, "/a")
	annotate(ctx, cache, b, bFrame, "/b")
	content := digest.FromString("equal source contents")
	require.NoError(t, cache.TeachContentDigest(ctx, a, content))
	require.NoError(t, cache.TeachContentDigest(ctx, b, content))
	aliasFrame := cacheTestIntCall("facts-alias")
	alias, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: aliasFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return b, nil
	})
	require.NoError(t, err)
	annotate(ctx, cache, alias, aliasFrame, "/alias")
	// A conflicting report is another fact, and an identical report is a no-op.
	annotate(ctx, cache, alias, aliasFrame, "/alias-extra")
	annotate(ctx, cache, alias, aliasFrame, "/alias")
	// A different request recipe with the same static fact does not add a
	// duplicate, even when it adopts the same value.
	annotate(ctx, cache, b, cacheTestIntCall("facts-alias-same-payload"), "/alias")
	check(ctx, cache, bID, 4, true)
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	_, removed, _, err := cache.removePrunedEdge(ctx, aID)
	require.NoError(t, err)
	require.True(t, removed)
	require.NotContains(t, cache.resultsByID, aID, "fact origin can be collected while its equivalent survives")
	require.NoError(t, cache.Close(ctx))
	ctx, cache, srv = persistedListTestCache(t, dbPath)
	b, err = cache.LoadResultByResultID(ctx, "test-session", srv, uint64(bID))
	require.NoError(t, err)
	check(ctx, cache, bID, 4, true)
	bundle := exportTestBundle(t, ctx, cache, b)
	require.Len(t, bundle.Values, 1, "facts do not require separate retained rows")

	otherCtx, other, otherSrv := transferTestCache(t)
	kept := persistedListTestResult(t, otherCtx, other, otherSrv, "facts-b", Int(1))
	keptFrame, err := kept.ResultCall()
	require.NoError(t, err)
	annotate(otherCtx, other, kept, keptFrame, "/local")
	reply, err := other.MergeValues(otherCtx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.Equal(t, uint64(kept.cacheSharedResult().id), reply.Imported()[0].ResultID)
	check(otherCtx, other, kept.cacheSharedResult().id, 5, true)
	legacy, err := cloneValueBundle(bundle)
	require.NoError(t, err)
	legacy.Values[0].Record.Envelope.FactsCoverageVersion = 0
	legacy.Values[0].Record.Envelope.ResultFacts = nil
	_, err = other.MergeValues(otherCtx, cloudCacheID, legacy)
	require.NoError(t, err)
	check(otherCtx, other, kept.cacheSharedResult().id, 5, false)
	annotate(otherCtx, other, kept, keptFrame, "/later")
	check(otherCtx, other, kept.cacheSharedResult().id, 6, false)
	markIncomplete(ctx, cache, b)
	check(ctx, cache, bID, 4, false)

	// A list can return an existing core value with an independent row. Its
	// selected item's ID must retain the function/list input facts.
	child := persistedListTestResult(t, otherCtx, other, otherSrv, "facts-item", Int(2))
	list := persistedListTestResult(t, otherCtx, other, otherSrv, "facts-list", DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{child}})
	listFrame, err := list.ResultCall()
	require.NoError(t, err)
	annotate(otherCtx, other, list, listFrame, "/list")
	check(otherCtx, other, child.cacheSharedResult().id, 0, true)
	item, err := list.NthValue(otherCtx, 1)
	require.NoError(t, err)
	require.Same(t, child.cacheSharedResult(), item.cacheSharedResult())
	check(otherCtx, other, child.cacheSharedResult().id, 1, true)
	nullable := persistedListTestResult(t, otherCtx, other, otherSrv, "facts-nullable", DynamicNullable{Elem: Int(0), Value: child, Valid: true})
	nullableFrame, err := nullable.ResultCall()
	require.NoError(t, err)
	annotate(otherCtx, other, nullable, nullableFrame, "/nullable")
	deref, valid := nullable.DerefValue()
	require.True(t, valid)
	require.Same(t, child.cacheSharedResult(), deref.cacheSharedResult())
	check(otherCtx, other, child.cacheSharedResult().id, 2, true)
	markIncomplete(otherCtx, other, nullable)
	check(otherCtx, other, child.cacheSharedResult().id, 2, false)

	// An in-place recomputation starts a new value generation. Old source
	// paths and incomplete coverage must not survive into its fresh value.
	generationFrame := cacheTestIntCall("facts-generation")
	oldRequest := &CallRequest{ResultCall: generationFrame, IsPersistable: true, ResultFactsIncomplete: true,
		ResultFacts: []ResultFact{{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/old-generation"}`)}}}
	oldValue, err := other.GetOrInitCall(otherCtx, "test-session", otherSrv, oldRequest, func(context.Context) (AnyResult, error) {
		return NewResultForCall(Int(31), generationFrame)
	})
	require.NoError(t, err)
	require.NoError(t, other.ReleaseSession(otherCtx, "test-session"))
	currentEntryTestExpire(other, oldValue)
	nextCtx := currentEntryTestSession(otherCtx, other, "facts-next-session")
	newRequest := &CallRequest{ResultCall: generationFrame, IsPersistable: true,
		ResultFacts: []ResultFact{{Namespace: "source-test", Payload: json.RawMessage(`{"path":"/new-generation"}`)}}}
	newValue, err := other.GetOrInitCall(nextCtx, "facts-next-session", otherSrv, newRequest, func(context.Context) (AnyResult, error) {
		return NewResultForCall(Int(32), generationFrame)
	})
	require.NoError(t, err)
	require.Same(t, oldValue.cacheSharedResult(), newValue.cacheSharedResult(), "expired unowned value is replaced in place")
	newFacts, newComplete, err := other.ResultFacts(nextCtx, "facts-next-session", &ResultCallRef{ResultID: uint64(newValue.cacheSharedResult().id)}, "source-test")
	require.NoError(t, err)
	require.Equal(t, newRequest.ResultFacts, newFacts)
	require.True(t, newComplete)

	// Cloud pruning keeps a holder-only row but releases the value's facts
	// and their bytes. Importing a definitive record restores fresh coverage.
	freshBundle := exportTestBundle(t, nextCtx, other, newValue)
	cloudCtx, cloud := storedPartTestCache(t, WithBlobStore())
	cloudReply, err := cloud.MergeValues(cloudCtx, "facts-engine", freshBundle)
	require.NoError(t, err)
	cloudID := cloudReply.Imported()[0].ResultID
	require.Positive(t, cloud.MetadataEstimate().PayloadBytes)
	_, removed, dropped, err := cloud.removePrunedEdge(cloudCtx, sharedResultID(cloudID))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 1, dropped)
	require.Zero(t, cloud.MetadataEstimate().PayloadBytes)
	cloud.egraphMu.RLock()
	holderOnly := cloud.resultsByID[sharedResultID(cloudID)]
	cloud.egraphMu.RUnlock()
	require.NotNil(t, holderOnly)
	prunedFacts, prunedComplete, err := cloud.ResultFacts(cloudCtx, "facts-inspect", &ResultCallRef{ResultID: cloudID}, "source-test")
	require.NoError(t, err)
	require.Empty(t, prunedFacts)
	require.False(t, prunedComplete)
	_, err = cloud.MergeValues(cloudCtx, "facts-engine", freshBundle)
	require.NoError(t, err)
	restoredFacts, restoredComplete, err := cloud.ResultFacts(cloudCtx, "facts-inspect", &ResultCallRef{ResultID: cloudID}, "source-test")
	require.NoError(t, err)
	require.Equal(t, newRequest.ResultFacts, restoredFacts)
	require.True(t, restoredComplete)
}
