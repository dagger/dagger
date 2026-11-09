package dagql

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/dagger/dagger/dagql/call"
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

func TestResultFactsStayWithProducerAcrossCollectionRestartAndImport(t *testing.T) {
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
	aFrame, bFrame := cacheTestIntCall("facts-a"), cacheTestIntCall("facts-b")
	fact := func(path string) ResultFact {
		encoded, err := json.Marshal(map[string]string{"path": path})
		require.NoError(t, err)
		return ResultFact{Namespace: "source-test", Payload: encoded}
	}
	makeValue := func(frame *ResultCall, path string) AnyResult {
		value, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{
			ResultCall: frame, IsPersistable: true, ResultFacts: []ResultFact{fact(path)},
		}, func(context.Context) (AnyResult, error) { return NewResultForCall(Int(1), frame) })
		require.NoError(t, err)
		return value
	}
	a, b := makeValue(aFrame, "/a"), makeValue(bFrame, "/b")
	aID, bID := a.cacheSharedResult().id, b.cacheSharedResult().id
	content := digest.FromString("equal source contents")
	require.NoError(t, cache.TeachContentDigest(ctx, a, content))
	require.NoError(t, cache.TeachContentDigest(ctx, b, content))
	check := func(ctx context.Context, c *Cache, id uint64, paths ...string) {
		t.Helper()
		facts, complete, err := c.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: id}, "source-test")
		require.NoError(t, err)
		require.True(t, complete)
		var expected []ResultFact
		for _, path := range paths {
			expected = append(expected, fact(path))
		}
		require.ElementsMatch(t, expected, facts)
		total, sum := cacheTestPayloadTotalConsistent(c)
		require.Equal(t, sum, total)
		if len(facts) != 0 {
			facts[0].Payload[0] = 'x'
			again, _, err := c.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: id}, "source-test")
			require.NoError(t, err)
			require.True(t, json.Valid(again[0].Payload))
		}
	}
	check(ctx, cache, uint64(aID), "/a")
	check(ctx, cache, uint64(bID), "/b")
	preparedValueReq := &CallRequest{
		ResultCall:  cacheTestIntCall("facts-prepared-value", call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: content}),
		ResultFacts: []ResultFact{fact("/prepared-value")}, ResultFactsPrepared: true,
	}
	preparedValue, err := cache.GetOrInitCall(ctx, "test-session", srv, preparedValueReq, func(context.Context) (AnyResult, error) {
		t.Fatal("equal payload should be reused without running this operation")
		return nil, nil
	})
	require.NoError(t, err)
	check(ctx, cache, uint64(preparedValue.cacheSharedResult().id), "/prepared-value")

	// The caller combines the facts of both actual inputs, even when their
	// values are equal. Merely teaching that equality must combine nothing.
	var inputFacts []ResultFact
	consumerFrame := cacheTestIntCall("facts-consumer")
	for i, value := range []AnyResult{a, b} {
		id := uint64(value.cacheSharedResult().id)
		facts, complete, err := cache.ResultFacts(ctx, "test-session", &ResultCallRef{ResultID: id}, "source-test")
		require.NoError(t, err)
		require.True(t, complete)
		inputFacts = append(inputFacts, facts...)
		consumerFrame.Args = append(consumerFrame.Args, &ResultCallArg{Name: string(rune('a' + i)), Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: &ResultCallRef{ResultID: id}}})
	}
	consumer, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: consumerFrame, ResultFacts: inputFacts}, func(context.Context) (AnyResult, error) { return NewResultForCall(Int(2), consumerFrame) })
	require.NoError(t, err)
	check(ctx, cache, uint64(consumer.cacheSharedResult().id), "/a", "/b")
	aliasFrame := cacheTestIntCall("facts-alias")
	aliasReq := &CallRequest{ResultCall: aliasFrame, IsPersistable: true, ResultFacts: []ResultFact{fact("/alias")}}
	alias, err := cache.GetOrInitCall(ctx, "test-session", srv, aliasReq, func(context.Context) (AnyResult, error) { return b, nil })
	require.NoError(t, err)
	aliasID := uint64(alias.cacheSharedResult().id)
	require.NotEqual(t, uint64(bID), aliasID, "an adopted payload retains the requesting call identity")
	check(ctx, cache, aliasID, "/b", "/alias")
	check(ctx, cache, uint64(bID), "/b")
	aliasHit, err := cache.GetOrInitCall(ctx, "test-session", srv, aliasReq, func(context.Context) (AnyResult, error) { t.Fatal("alias must hit"); return nil, nil })
	require.NoError(t, err)
	require.Equal(t, aliasID, uint64(aliasHit.cacheSharedResult().id))
	list := persistedListTestResult(t, ctx, cache, srv, "facts-list", DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{b}})
	cache.egraphMu.Lock()
	cache.setResultFactsLocked(list.cacheSharedResult(), []ResultFact{fact("/list")}, false)
	cache.egraphMu.Unlock()
	item, err := list.NthValue(ctx, 1)
	require.NoError(t, err)
	itemID := uint64(item.cacheSharedResult().id)
	require.NotEqual(t, uint64(bID), itemID)
	check(ctx, cache, itemID, "/b", "/list")
	check(ctx, cache, uint64(bID), "/b")
	nullable := persistedListTestResult(t, ctx, cache, srv, "facts-nullable", DynamicNullable{Elem: Int(0), Value: b, Valid: true})
	cache.egraphMu.Lock()
	cache.setResultFactsLocked(nullable.cacheSharedResult(), []ResultFact{fact("/nullable")}, false)
	cache.egraphMu.Unlock()
	deref, valid := nullable.DerefValue()
	require.True(t, valid)
	require.Same(t, nullable.cacheSharedResult(), deref.cacheSharedResult())
	nullableID := uint64(deref.cacheSharedResult().id)
	require.Equal(t, Int(1), deref.Unwrap(), "the outer ID exposes the inner typed payload")
	check(ctx, cache, nullableID, "/nullable")
	check(ctx, cache, uint64(bID), "/b")
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	_, removed, _, err := cache.removePrunedEdge(ctx, aID)
	require.NoError(t, err)
	require.True(t, removed)
	require.NotContains(t, cache.resultsByID, aID)
	require.NoError(t, cache.Close(ctx))
	ctx, cache, srv = persistedListTestCache(t, dbPath)
	alias, err = cache.LoadResultByResultID(ctx, "test-session", srv, aliasID)
	require.NoError(t, err)
	require.Equal(t, Int(1), alias.Unwrap())
	check(ctx, cache, aliasID, "/b", "/alias")
	check(ctx, cache, uint64(bID), "/b")
	nullable, err = cache.LoadResultByResultID(ctx, "test-session", srv, nullableID)
	require.NoError(t, err)
	deref, valid = nullable.DerefValue()
	require.True(t, valid)
	require.Equal(t, Int(1), deref.Unwrap())
	require.Equal(t, nullableID, uint64(deref.cacheSharedResult().id))
	check(ctx, cache, nullableID, "/nullable")

	bundle := exportTestBundle(t, ctx, cache, alias)
	require.Len(t, bundle.Values, 2, "alias retains one shared payload row")
	otherCtx, other, otherSrv := transferTestCache(t)
	reply, err := other.MergeValues(otherCtx, cloudCacheID, bundle)
	require.NoError(t, err)
	importedID := reply.Imported()[0].ResultID
	imported, err := other.LoadResultByResultID(otherCtx, "test-session", otherSrv, importedID)
	require.NoError(t, err)
	require.Equal(t, Int(1), imported.Unwrap())
	check(otherCtx, other, importedID, "/b", "/alias")
	// Legacy values are usable, but union-based coverage proves no call's
	// individual provenance. Loading them must remain conservative.
	legacy, err := cloneValueBundle(bundle)
	require.NoError(t, err)
	for i := range legacy.Values {
		legacy.Values[i].Record.Envelope.FactsCoverageVersion = 1
		legacy.Values[i].Record.Envelope.Version = 5
	}
	legacyCtx, legacyCache, legacySrv := transferTestCache(t)
	legacyReply, err := legacyCache.MergeValues(legacyCtx, cloudCacheID, legacy)
	require.NoError(t, err)
	legacyID := legacyReply.Imported()[0].ResultID
	legacyValue, err := legacyCache.LoadResultByResultID(legacyCtx, "test-session", legacySrv, legacyID)
	require.NoError(t, err)
	require.Equal(t, Int(1), legacyValue.Unwrap())
	_, complete, err = legacyCache.ResultFacts(legacyCtx, "test-session", &ResultCallRef{ResultID: legacyID}, "source-test")
	require.NoError(t, err)
	require.False(t, complete)
}

func TestResultFactsFollowValueGenerationAndPruning(t *testing.T) {
	otherCtx, other, otherSrv := transferTestCache(t)
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

func TestResultFactsAliasesShareLazyValueAndReleaseOnce(t *testing.T) {
	ctx, cache, srv := persistedListTestCache(t, "")
	srv.InstallObject(NewClass(srv, ClassOpts[*captureTestValue]{}))
	var evaluations, attachments, releases atomic.Int32
	value := &captureTestValue{
		lazy:    func(context.Context) error { evaluations.Add(1); return nil },
		attach:  func(context.Context) error { attachments.Add(1); return nil },
		release: func(context.Context) error { releases.Add(1); return nil },
	}
	sourceFrame := &ResultCall{Kind: ResultCallKindField, Field: "fact-lazy-source", Type: NewResultCallType(value.Type())}
	source, err := cache.GetOrInitCall(ctx, "source-session", srv, &CallRequest{ResultCall: sourceFrame}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, sourceFrame)
	})
	require.NoError(t, err)
	cache.testBeforePublicationIndex = func(oc *ongoingCall) {
		if oc.res.valueSource == nil {
			return
		}
		cache.testBeforePublicationIndex = nil
		require.NoError(t, cache.ReleaseSession(ctx, "source-session"))
		require.Zero(t, releases.Load(), "the alias handoff pins its payload before publication")
	}
	var aliases []AnyResult
	for _, name := range []string{"first", "second"} {
		frame := &ResultCall{Kind: ResultCallKindSynthetic, SyntheticOp: name, Type: NewResultCallType(value.Type())}
		alias, err := cache.GetOrInitCall(ctx, "test-session", srv, &CallRequest{
			ResultCall: frame, ResultFacts: []ResultFact{{Namespace: "source-test", Payload: json.RawMessage(`{"prepared":true}`)}},
		}, func(context.Context) (AnyResult, error) { return source, nil })
		require.NoError(t, err)
		require.NotSame(t, source.cacheSharedResult(), alias.cacheSharedResult())
		aliases = append(aliases, alias)
	}
	require.NoError(t, cache.Evaluate(ctx, aliases...))
	require.Equal(t, int32(1), evaluations.Load(), "aliases reuse the source's lazy task")
	require.Equal(t, int32(1), attachments.Load(), "aliases do not reattach shared mutable payloads")
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.CloseDiscardingPersistence())
	require.Equal(t, int32(1), releases.Load(), "only the payload owner runs its release hook")
}
