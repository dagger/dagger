package dagql

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

type currentEntryTestOutcome struct {
	res AnyResult
	err error
}

// currentEntryTestSession returns a context for one client session of c.
func currentEntryTestSession(ctx context.Context, c *Cache, sessionID string) context.Context {
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  sessionID + "-client",
		SessionID: sessionID,
	})
	return ContextWithCache(ctx, c)
}

// currentEntryTestIndexed returns the entry the recipe index names for frame.
func currentEntryTestIndexed(t *testing.T, c *Cache, frame *ResultCall) sharedResultID {
	t.Helper()
	recipe, err := frame.deriveRecipeDigest(c)
	assert.NilError(t, err)
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.entriesByRecipe[recipe]
}

// A session that cannot use the recipe's current entry, because a late
// explicit dependency raised the entry's requirements, publishes its own
// value without indexing it, and hits that value on its next call. A session
// that covers the requirements still adopts the current entry.
func TestCachePublicationUnindexedWhenSessionLacksRequirements(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	srv := cacheTestServer(t)
	handle := cacheTestVolatileSessionResourceHandle("CURRENT_ENTRY_LATE_DEP")
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "current-entry-late-dep",
	}

	aCtx := currentEntryTestSession(baseCtx, c, "session-a")
	assert.NilError(t, c.BindSessionResource(aCtx, "session-a", "session-a-client", handle, "a"))
	eCtx := currentEntryTestSession(baseCtx, c, "session-e")
	assert.NilError(t, c.BindSessionResource(eCtx, "session-e", "session-e-client", handle, "e"))
	bCtx := currentEntryTestSession(baseCtx, c, "session-b")

	// Session E starts computing before the entry exists, so its publication
	// finds the current entry and adopts it.
	eStarted, eFinish := make(chan struct{}), make(chan struct{})
	eDone := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(eCtx, "session-e", srv, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
			close(eStarted)
			<-eFinish
			return NewResultForCall(&cacheTestObject{Value: 5}, frame.clone())
		})
		eDone <- currentEntryTestOutcome{res, err}
	}()
	<-eStarted

	parent, err := c.GetOrInitCall(aCtx, "session-a", srv, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&cacheTestObject{Value: 1}, frame.clone())
	})
	assert.NilError(t, err)
	parentID := parent.cacheSharedResult().id
	assert.Equal(t, parentID, currentEntryTestIndexed(t, c, frame))

	leaf, err := cacheTestSessionResourceLeaf(aCtx, handle)
	assert.NilError(t, err)
	dep, err := c.AttachResult(aCtx, "session-a", srv, leaf)
	assert.NilError(t, err)
	assert.NilError(t, c.AddExplicitDependency(aCtx, parent, dep, "test_late_dep"))

	// B cannot use the current entry: it computes, and its value is
	// registered beside it, not indexed.
	var bCalls int
	bFn := func(context.Context) (AnyResult, error) {
		bCalls++
		return NewResultForCall(&cacheTestObject{Value: 2}, frame.clone())
	}
	first, err := c.GetOrInitCall(bCtx, "session-b", srv, &CallRequest{ResultCall: frame.clone()}, bFn)
	assert.NilError(t, err)
	assert.Equal(t, 1, bCalls)
	assert.Assert(t, !first.HitCache())
	bID := first.cacheSharedResult().id
	assert.Assert(t, bID != parentID)
	assert.Equal(t, parentID, currentEntryTestIndexed(t, c, frame), "the index keeps naming the current entry")

	// B's next call of the recipe hits its own entry and runs nothing.
	second, err := c.GetOrInitCall(bCtx, "session-b", srv, &CallRequest{ResultCall: frame.clone()}, bFn)
	assert.NilError(t, err)
	assert.Equal(t, 1, bCalls, "session B must not recompute the recipe on every call")
	assert.Assert(t, second.HitCache())
	assert.Equal(t, bID, second.cacheSharedResult().id)

	// E covers the requirements, so its publication adopts the current entry.
	close(eFinish)
	e := <-eDone
	assert.NilError(t, e.err)
	assert.Equal(t, parentID, e.res.cacheSharedResult().id)
	assert.Equal(t, parentID, currentEntryTestIndexed(t, c, frame))

	for _, s := range []struct {
		ctx context.Context
		id  string
	}{{aCtx, "session-a"}, {bCtx, "session-b"}, {eCtx, "session-e"}} {
		assert.NilError(t, c.ReleaseSession(s.ctx, s.id))
	}
}

// Two sessions compute one recipe at once: the per-session in-flight map
// lets both run. The first to finish registers the recipe's entry; the other
// adopts it and releases its own value (D1).
func TestCachePublicationAdoptsTheRecipesLiveEntry(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	key := cacheTestIntCall("current-entry-adopt")

	var releasedA, releasedB atomic.Bool
	aStarted, aFinish := make(chan struct{}), make(chan struct{})
	aDone := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
			close(aStarted)
			<-aFinish
			return cacheTestIntResultWithOnRelease(key, 1, func(context.Context) error {
				releasedA.Store(true)
				return nil
			}), nil
		})
		aDone <- currentEntryTestOutcome{res, err}
	}()
	<-aStarted

	resB, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResultWithOnRelease(key, 2, func(context.Context) error {
			releasedB.Store(true)
			return nil
		}), nil
	})
	assert.NilError(t, err)
	close(aFinish)
	a := <-aDone
	assert.NilError(t, a.err)

	bID := resB.cacheSharedResult().id
	assert.Equal(t, bID, a.res.cacheSharedResult().id, "both callers get the same entry")
	adopted, ok := UnwrapAs[cacheTestOnReleaseInt](a.res)
	assert.Assert(t, ok)
	assert.Equal(t, Int(2), adopted.Int)
	assert.Assert(t, releasedA.Load(), "the adopting publication releases its own value")
	assert.Assert(t, !releasedB.Load())
	assert.Equal(t, bID, currentEntryTestIndexed(t, c, key))
	c.egraphMu.RLock()
	assert.Equal(t, 1, len(c.resultsByID), "one entry for the recipe")
	c.egraphMu.RUnlock()

	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
	assert.Assert(t, releasedB.Load())
}

// A publication that finds its recipe's entry still attaching waits for that
// attachment, then adopts the entry; when the attachment fails, the entry
// leaves the index and the waiting publication registers its own value.
func TestCachePublicationWaitsForTheCurrentEntrysAttachment(t *testing.T) {
	t.Parallel()

	for _, attachFails := range []bool{false, true} {
		name := "settles"
		if attachFails {
			name = "fails"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := cacheTestContext(t.Context())
			c, err := NewCache(ctx, "", nil, nil)
			assert.NilError(t, err)
			frame := &ResultCall{
				Kind:  ResultCallKindField,
				Type:  NewResultCallType((&captureTestValue{}).Type()),
				Field: "current-entry-attach",
			}

			attachStarted, attachRelease := make(chan struct{}), make(chan struct{})
			bValue := &captureTestValue{text: "b", attach: func(context.Context) error {
				close(attachStarted)
				<-attachRelease
				if attachFails {
					return errors.New("attachment failed")
				}
				return nil
			}}
			var releasedA atomic.Bool
			aValue := &captureTestValue{text: "a", release: func(context.Context) error {
				releasedA.Store(true)
				return nil
			}}
			waitedOn := make(chan sharedResultID, 1)
			c.testPublicationWaitsOnAttachment = func(res *sharedResult) { waitedOn <- res.id }

			aStarted, aFinish := make(chan struct{}), make(chan struct{})
			aDone := make(chan currentEntryTestOutcome, 1)
			go func() {
				res, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
					close(aStarted)
					<-aFinish
					return NewResultForCall(aValue, frame.clone())
				})
				aDone <- currentEntryTestOutcome{res, err}
			}()
			<-aStarted
			bDone := make(chan currentEntryTestOutcome, 1)
			go func() {
				res, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
					return NewResultForCall(bValue, frame.clone())
				})
				bDone <- currentEntryTestOutcome{res, err}
			}()
			<-attachStarted
			close(aFinish)
			bID := <-waitedOn
			close(attachRelease)

			b := <-bDone
			a := <-aDone
			assert.NilError(t, a.err)
			aID := a.res.cacheSharedResult().id
			if attachFails {
				assert.ErrorContains(t, b.err, "attachment failed")
				assert.Assert(t, aID != bID, "the failed entry is not adopted")
				assert.Assert(t, !releasedA.Load())
				assert.Equal(t, aID, currentEntryTestIndexed(t, c, frame))
			} else {
				assert.NilError(t, b.err)
				assert.Equal(t, bID, aID)
				assert.Assert(t, releasedA.Load())
				assert.Equal(t, bID, currentEntryTestIndexed(t, c, frame))
			}
			assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
			assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
		})
	}
}

// A publication that waits on its recipe's entry decides again with the time
// the wait ended: an entry that expired during the wait, and that its own
// session still holds, is retired, and the waiting publication registers its
// own value instead of adopting the expired one.
func TestCachePublicationDecidesAgainAfterTheCurrentEntryExpiresDuringItsWait(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&captureTestValue{}).Type()),
		Field: "current-entry-expires-during-wait",
	}

	attachStarted, attachRelease := make(chan struct{}), make(chan struct{})
	bValue := &captureTestValue{text: "b", attach: func(context.Context) error {
		close(attachStarted)
		<-attachRelease
		return nil
	}}
	var releasedA atomic.Bool
	aValue := &captureTestValue{text: "a", release: func(context.Context) error {
		releasedA.Store(true)
		return nil
	}}
	waitedOn := make(chan sharedResultID, 1)
	c.testPublicationWaitsOnAttachment = func(res *sharedResult) { waitedOn <- res.id }

	aStarted, aFinish := make(chan struct{}), make(chan struct{})
	aDone := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
			close(aStarted)
			<-aFinish
			return NewResultForCall(aValue, frame.clone())
		})
		aDone <- currentEntryTestOutcome{res, err}
	}()
	<-aStarted
	bDone := make(chan currentEntryTestOutcome, 1)
	go func() {
		// B's entry expires two seconds after its publication: after A's
		// publication starts, and before B's attachment is let go.
		res, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone(), TTL: 2}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(bValue, frame.clone())
		})
		bDone <- currentEntryTestOutcome{res, err}
	}()
	<-attachStarted
	close(aFinish)
	bID := <-waitedOn
	for {
		c.egraphMu.RLock()
		expired := c.resultExpiredAtLocked(c.resultsByID[bID], time.Now().Unix())
		c.egraphMu.RUnlock()
		if expired {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(attachRelease)

	b := <-bDone
	a := <-aDone
	assert.NilError(t, b.err)
	assert.NilError(t, a.err)
	assert.Equal(t, bID, b.res.cacheSharedResult().id)
	aID := a.res.cacheSharedResult().id
	assert.Assert(t, aID != bID, "the expired entry is not adopted")
	assert.Assert(t, !releasedA.Load(), "A's value is published, not released")
	assert.Equal(t, aID, currentEntryTestIndexed(t, c, frame))
	c.egraphMu.RLock()
	assert.Assert(t, len(c.resultsByID[bID].recipeKeys) == 0, "B's entry is retired")
	c.egraphMu.RUnlock()
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}

// Waiting for the graph lock is a wait before the decision too: an entry of
// the recipe that expires while a publication waits for the lock is not
// adopted. Its session still holds it, so it is retired, and the publication
// registers its own value. The entry's attachment has already settled, and
// another graph operation holds the lock past its expiry.
func TestCachePublicationDecidesWithTheTimeItHoldsTheLock(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	frame := cacheTestIntCall("current-entry-expires-before-the-lock")
	detached := cacheTestIntResult(frame, 1)
	parked, resume := make(chan struct{}), make(chan struct{})
	c.testBeforePublicationIndex = func(oc *ongoingCall) {
		if oc.val != nil && oc.val.cacheSharedResult() == detached.cacheSharedResult() {
			close(parked)
			<-resume
		}
	}
	done := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			return detached, nil
		})
		done <- currentEntryTestOutcome{res, err}
	}()
	<-parked
	b, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: frame, TTL: 2}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 2), nil
	})
	require.NoError(t, err)
	bID := b.cacheSharedResult().id
	// Another graph operation holds the lock until B's entry has expired;
	// A's publication is let go meanwhile and waits for the lock.
	c.egraphMu.Lock()
	expires := c.resultsByID[bID].expiresAtUnix
	close(resume)
	if delay := time.Until(time.Unix(expires, 0)) + 50*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	c.egraphMu.Unlock()
	a := <-done
	require.NoError(t, a.err)
	require.NotEqual(t, bID, a.res.cacheSharedResult().id, "B's entry expired before A's decision")
	require.Equal(t, 1, cacheTestUnwrapInt(t, a.res))
	require.Equal(t, a.res.cacheSharedResult().id, currentEntryTestIndexed(t, c, frame))
	require.NoError(t, c.ReleaseSession(ctx, "session-a"))
	require.NoError(t, c.ReleaseSession(ctx, "session-b"))
}

// An attachment whose publication adopts the recipe's live entry releases its
// own detached value. When that release fails, the call fails and gives up its
// hold on the adopted entry, which is collected once its sessions are gone.
func TestCacheAttachResultReleasesTheAdoptedEntryWhenTheDiscardFails(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	key := cacheTestIntCall("current-entry-attach-discard-fails")
	detached := cacheTestIntResultWithOnRelease(key, 1, func(context.Context) error {
		return errors.New("discard cleanup failed")
	})
	parked, resume := make(chan struct{}), make(chan struct{})
	c.testBeforePublicationIndex = func(oc *ongoingCall) {
		if oc.val != nil && oc.val.cacheSharedResult() == detached.cacheSharedResult() {
			close(parked)
			<-resume
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.AttachResult(ctx, "session-b", noopTypeResolver{}, detached)
		done <- err
	}()
	<-parked
	winner, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 2), nil
	})
	require.NoError(t, err)
	close(resume)
	require.ErrorContains(t, <-done, "discard cleanup failed")
	require.NoError(t, c.ReleaseSession(ctx, "session-a"))
	require.NoError(t, c.ReleaseSession(ctx, "session-b"))
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	_, kept := c.resultsByID[winner.cacheSharedResult().id]
	require.False(t, kept, "the adopted entry is collected once both sessions are released")
}

// A sharing pass reports its parts after it has let its entries go, so a
// replacement can come between a part's settlement and the report. The
// report keeps the count the part settled at: the old value's part is never
// labelled with the new value's count. The test parks a real replacement in
// the old value's release and runs the production report on the pass's real
// receipt.
func TestSnapshotShareReportKeepsTheCountItsPartsSettledAt(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	var (
		mu      sync.Mutex
		reports []SnapshotSharedPart
	)
	c.shareReport = func(parts []SnapshotSharedPart) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, parts...)
	}
	var receipt *ReadyPartReceipt
	c.testBeforeShareFinish = func(r *ReadyPartReceipt) { receipt = r }
	barrier := newSharePassBarrier(c)
	receiver := currentEntryTestPublish(t, ctx, c, srv, "test-session", "report-settled-receiver", &transferTestValue{Text: "pending"})
	donor := persistedListTestResult(t, ctx, c, srv, "report-settled-donor", &transferTestValue{Text: "old-snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "old-donor-snapshot"}}})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	attemptReleased := armLazyAttemptReleased(c)
	shareTestUnite(t, ctx, c, "report-settled", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t))
	require.NotNil(t, receipt)
	// The installing task's completion queues one empty successor pass, which
	// takes its own member holds, and the lazy attempt holds the entry until
	// it is released. Wait for both, so the replacement below finds the entry
	// unused and replaces it rather than retiring it.
	require.Equal(t, 0, barrier.awaitPass(t), "the completion trigger queued an empty successor")
	waitLazyAttemptReleased(t, attemptReleased)
	mu.Lock()
	require.NotEmpty(t, reports)
	reports = nil
	mu.Unlock()

	row := receiver.cacheSharedResult()
	releasing, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	defer unblock()
	row.payloadMu.Lock()
	row.onRelease = joinOnRelease(row.onRelease, func(context.Context) error {
		close(releasing)
		<-resume
		return nil
	})
	row.payloadMu.Unlock()
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	// Both expire, so the donor cannot answer the new call's lookup.
	currentEntryTestExpire(c, receiver)
	currentEntryTestExpire(c, donor)
	done := make(chan currentEntryTestOutcome, 1)
	frame := row.loadResultCall().clone()
	go func() {
		res, err := c.GetOrInitCall(ctx, "fresh-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "new-pending"}, frame)
		})
		done <- currentEntryTestOutcome{res, err}
	}()
	select {
	case <-releasing:
	case out := <-done:
		t.Fatalf("the replacement did not reach the old value's release: %+v", out)
	case <-time.After(10 * time.Second):
		t.Fatal("the replacement did not reach the old value's release")
	}
	require.Equal(t, resultAttachmentOpen, row.attachmentState())
	c.reportSharedParts([]*ReadyPartReceipt{receipt})
	mu.Lock()
	require.NotEmpty(t, reports, "the settled part is still reported")
	for _, report := range reports {
		require.Zero(t, report.Replacements, "the old value's part keeps its count: %+v", report)
	}
	mu.Unlock()
	unblock()
	out := <-done
	require.NoError(t, out.err)
	require.Equal(t, row.id, out.res.cacheSharedResult().id)
	require.NoError(t, c.ReleaseSession(ctx, "fresh-session"))
}

// currentEntryTestExpire makes res's value and its retention edge expire.
func currentEntryTestExpire(c *Cache, res AnyResult) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	shared := c.resultsByID[res.cacheSharedResult().id]
	past := time.Now().Add(-time.Hour).Unix()
	shared.expiresAtUnix = past
	if edge, ok := c.persistedEdgesByResult[shared.id]; ok {
		edge.expiresAtUnix = past
		c.persistedEdgesByResult[shared.id] = edge
	}
}

// An expired entry that only its retention edge owns takes a new value of
// its recipe in place (D9): same number, the replacement count goes to 1, the
// old value is released, and the retention edge's expiry restarts from the
// new value's, so the next prune keeps the recently used entry.
func TestCachePublicationReplacesAnUnusedExpiredEntryInPlace(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	key := cacheTestIntCall("current-entry-replace")

	var releasedOld atomic.Bool
	old, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResultWithOnRelease(key, 1, func(context.Context) error {
			releasedOld.Store(true)
			return nil
		}), nil
	})
	assert.NilError(t, err)
	oldID := old.cacheSharedResult().id
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	currentEntryTestExpire(c, old)

	var calls int
	res, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true, TTL: 3600}, func(context.Context) (AnyResult, error) {
		calls++
		return cacheTestIntResult(key, 2), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, calls)
	assert.Assert(t, !res.HitCache())
	assert.Equal(t, oldID, res.cacheSharedResult().id, "the entry keeps its number")
	assert.Equal(t, 2, cacheTestUnwrapInt(t, res))
	assert.Assert(t, releasedOld.Load(), "the old value is released")

	c.egraphMu.RLock()
	shared := c.resultsByID[oldID]
	assert.Equal(t, uint64(1), shared.replacements)
	assert.Assert(t, !shared.imported)
	assert.Assert(t, shared.expiresAtUnix > time.Now().Unix())
	assert.Assert(t, c.persistedEdgesByResult[oldID].expiresAtUnix > time.Now().Unix(), "the retention edge's expiry restarts")
	c.egraphMu.RUnlock()
	assert.Equal(t, oldID, currentEntryTestIndexed(t, c, key))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))

	_, err = c.Prune(ctx, []CachePrunePolicy{{All: true, KeepDuration: time.Hour}})
	assert.NilError(t, err)
	c.egraphMu.RLock()
	_, kept := c.resultsByID[oldID]
	c.egraphMu.RUnlock()
	assert.Assert(t, kept, "the replaced value survives the next prune")
}

// A call answered with nothing replaces an expired entry like any value: the
// entry is left as a fresh nil publication leaves one, with no value, and is
// served as a nil hit.
func TestCachePublicationReplacesAnExpiredEntryWithNothing(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	key := cacheTestIntCall("current-entry-replace-nil")

	old, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 1), nil
	})
	assert.NilError(t, err)
	oldID := old.cacheSharedResult().id
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	currentEntryTestExpire(c, old)

	var calls int
	nothing := func(context.Context) (AnyResult, error) {
		calls++
		return nil, nil
	}
	res, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true, TTL: 3600}, nothing)
	assert.NilError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, oldID, res.cacheSharedResult().id, "the entry keeps its number")
	assert.Assert(t, res.Unwrap() == nil)
	state := c.resultsByID[oldID].loadPayloadState()
	assert.Assert(t, !state.hasValue, "no value, as a fresh nil publication")
	assert.Assert(t, state.self == nil)
	c.egraphMu.RLock()
	assert.Equal(t, uint64(1), c.resultsByID[oldID].replacements)
	c.egraphMu.RUnlock()

	hit, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: key, IsPersistable: true, TTL: 3600}, nothing)
	assert.NilError(t, err)
	assert.Equal(t, 1, calls, "the nil value is a hit")
	assert.Assert(t, hit.HitCache())
	assert.Equal(t, oldID, hit.cacheSharedResult().id)
	assert.Assert(t, hit.Unwrap() == nil)
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}

// An imported entry that expires takes a computed value in place: it is no
// longer imported, and its offers, which belonged to the old value, are
// dropped with their owners' holds.
func TestCachePublicationReplacementClearsImportAndOffers(t *testing.T) {
	actx, a, asrv := transferTestCache(t)
	exported := persistedListTestResult(t, actx, a, asrv, "current-entry-imported", String("old"))
	bundle := exportTestBundle(t, actx, a, exported)

	bctx, b, bsrv := transferTestCache(t)
	mapping, err := b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	require.Len(t, mapping, 1)
	importedID := sharedResultID(mapping[0].ResultID)
	child := persistedListTestResult(t, bctx, b, bsrv, "current-entry-offer-owner", String("child"))
	imported, err := b.LoadResultByResultID(bctx, "", bsrv, uint64(importedID))
	require.NoError(t, err)
	transferTestOffer(t, b, bctx, imported, child)
	b.egraphMu.RLock()
	childOwners := b.resultsByID[child.cacheSharedResult().id].incomingOwnershipCount
	require.Len(t, b.resultsByID[importedID].partOffers, 1)
	b.egraphMu.RUnlock()
	currentEntryTestExpire(b, imported)

	replaced := persistedListTestResult(t, bctx, b, bsrv, "current-entry-imported", String("new"))
	require.Equal(t, importedID, replaced.cacheSharedResult().id)
	require.Equal(t, String("new"), replaced.Unwrap())
	b.egraphMu.RLock()
	defer b.egraphMu.RUnlock()
	shared := b.resultsByID[importedID]
	require.Equal(t, uint64(1), shared.replacements)
	require.False(t, shared.imported)
	require.Empty(t, shared.partOffers)
	if childShared := b.resultsByID[child.cacheSharedResult().id]; childShared != nil {
		require.Equal(t, childOwners-1, childShared.incomingOwnershipCount, "the dropped offer's owner no longer holds its dependency")
	}
}

// An expired entry that a live session still holds is retired, not replaced:
// it leaves the index and keeps serving that session, and the new value
// registers as the recipe's current entry (D9).
func TestCachePublicationRetiresAnExpiredEntryASessionHolds(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	key := cacheTestIntCall("current-entry-retire-session")

	held, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 1), nil
	})
	assert.NilError(t, err)
	heldID := held.cacheSharedResult().id
	currentEntryTestExpire(c, held)

	fresh, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 2), nil
	})
	assert.NilError(t, err)
	freshID := fresh.cacheSharedResult().id
	assert.Assert(t, freshID != heldID)
	assert.Equal(t, freshID, currentEntryTestIndexed(t, c, key))
	c.egraphMu.RLock()
	retired := c.resultsByID[heldID]
	assert.Assert(t, retired != nil, "the retired entry keeps serving its session")
	assert.Equal(t, 0, len(retired.recipeKeys))
	assert.Equal(t, uint64(0), retired.replacements)
	c.egraphMu.RUnlock()
	assert.Equal(t, 1, cacheTestUnwrapInt(t, held))

	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	c.egraphMu.RLock()
	_, stillThere := c.resultsByID[heldID]
	c.egraphMu.RUnlock()
	assert.Assert(t, !stillThere, "the retired entry goes with its last user")
	assert.Equal(t, freshID, currentEntryTestIndexed(t, c, key))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}

// An expired entry that another entry depends on is retired, even while the
// dependent's value is still an encoded envelope (the implementation's rule
// for D9). A later hit on the dependent decodes it against the retired
// entry's own value. Holding the dependent's decode open while the recipe is
// published again pins the order: the decode reaches the dependency only
// through egraphMu, where the dependent's edge already counts as a use.
func TestCachePublicationRetiresAnExpiredEntryWithADependent(t *testing.T) {
	for _, decoding := range []bool{false, true} {
		name := "undecoded"
		if decoding {
			name = "decoding"
		}
		t.Run(name, func(t *testing.T) {
			actx, a, asrv := transferTestCache(t)
			dep := persistedListTestResult(t, actx, a, asrv, "current-entry-dependency", String("old"))
			list := persistedListTestResult(t, actx, a, asrv, "current-entry-dependent", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{dep}})
			bundle := exportTestBundle(t, actx, a, list)

			bctx, b, bsrv := transferTestCache(t)
			mapping, err := b.ImportValues(bctx, bundle)
			require.NoError(t, err)
			require.Len(t, mapping, 1)
			listID := sharedResultID(mapping[0].ResultID)
			b.egraphMu.RLock()
			var depID sharedResultID
			for id := range b.resultsByID[listID].deps {
				depID = id
			}
			depRow := b.resultsByID[depID]
			b.egraphMu.RUnlock()
			require.NotZero(t, depID)
			require.Nil(t, depRow.loadPayloadState().self, "the dependent and its dependency are still envelopes")
			currentEntryTestExpire(b, Result[Typed]{shared: depRow})

			decodeParked, decodeResume := make(chan struct{}), make(chan struct{})
			hitDone := make(chan currentEntryTestOutcome, 1)
			if decoding {
				b.testPersistDecodeLeadPublished = func(id uint64) {
					if sharedResultID(id) == listID {
						close(decodeParked)
						<-decodeResume
					}
				}
				go func() {
					res, err := b.LoadResultByResultID(bctx, "hit-session", bsrv, uint64(listID))
					hitDone <- currentEntryTestOutcome{res, err}
				}()
				<-decodeParked
			}

			fresh := persistedListTestResult(t, bctx, b, bsrv, "current-entry-dependency", String("new"))
			freshID := fresh.cacheSharedResult().id
			require.NotEqual(t, depID, freshID)
			b.egraphMu.RLock()
			require.Empty(t, depRow.recipeKeys, "the dependency is retired")
			require.Equal(t, uint64(0), depRow.replacements)
			require.Equal(t, freshID, b.entriesByRecipe[depRow.recipeKeysForTest(t, b)])
			b.egraphMu.RUnlock()

			var hit currentEntryTestOutcome
			if decoding {
				close(decodeResume)
				hit = <-hitDone
			} else {
				res, err := b.LoadResultByResultID(bctx, "hit-session", bsrv, uint64(listID))
				hit = currentEntryTestOutcome{res, err}
			}
			require.NoError(t, hit.err)
			item, err := hit.res.Unwrap().(Enumerable).NthValue(1, nil)
			require.NoError(t, err)
			require.Equal(t, depID, item.cacheSharedResult().id, "the dependent decodes against the retired entry")
			require.Equal(t, String("old"), item.Unwrap())
		})
	}
}

// recipeKeysForTest returns the recipe of res's stored frame. Requires
// egraphMu for reading.
func (res *sharedResult) recipeKeysForTest(t *testing.T, c *Cache) digest.Digest {
	t.Helper()
	frame := res.loadResultCall()
	require.NotNil(t, frame)
	recipe, err := frame.recipeDigestWithVisiting(c, map[sharedResultID]struct{}{})
	require.NoError(t, err)
	return recipe
}

// An expired entry whose lazy evaluation is running is retired: its
// evaluating session holds it.
func TestCachePublicationRetiresAnExpiredEntryUnderLazyEvaluation(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&captureTestValue{}).Type()),
		Field: "current-entry-lazy",
	}
	started, finish := make(chan struct{}), make(chan struct{})
	value := &captureTestValue{text: "lazy"}
	value.lazy = func(context.Context) error {
		close(started)
		<-finish
		value.lazy = nil
		return nil
	}
	sessionA := cacheTestSessionID(t, ctx)
	held, err := c.GetOrInitCall(ctx, sessionA, noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame.clone())
	})
	assert.NilError(t, err)
	evaluated := make(chan error, 1)
	go func() { evaluated <- c.Evaluate(ctx, held) }()
	<-started
	currentEntryTestExpire(c, held)

	fresh, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&captureTestValue{text: "fresh"}, frame.clone())
	})
	assert.NilError(t, err)
	assert.Assert(t, fresh.cacheSharedResult().id != held.cacheSharedResult().id)
	assert.Equal(t, fresh.cacheSharedResult().id, currentEntryTestIndexed(t, c, frame))
	close(finish)
	assert.NilError(t, <-evaluated)
	c.egraphMu.RLock()
	assert.Equal(t, uint64(0), c.resultsByID[held.cacheSharedResult().id].replacements)
	c.egraphMu.RUnlock()
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
	cacheTestReleaseSession(t, c, ctx)
}

// A replacement in place whose new value then fails to attach leaves the
// entry collected, like any failed publication: its retention edge, which
// kept the old value, and its index key go with it. The recipe's next
// publication registers afresh.
func TestCachePublicationFailedReplacementCollectsTheEntry(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&captureTestValue{}).Type()),
		Field: "current-entry-failed-replacement",
	}
	publish := func(session string, value *captureTestValue) (AnyResult, error) {
		return c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame.clone())
		})
	}

	var releasedOld atomic.Bool
	old, err := publish("session-a", &captureTestValue{text: "old", release: func(context.Context) error {
		releasedOld.Store(true)
		return nil
	}})
	assert.NilError(t, err)
	oldID := old.cacheSharedResult().id
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	currentEntryTestExpire(c, old)

	_, err = publish("session-b", &captureTestValue{text: "failing", attach: func(context.Context) error {
		return errors.New("attachment failed")
	}})
	assert.ErrorContains(t, err, "attachment failed")
	assert.Assert(t, releasedOld.Load(), "the replaced value was released")
	c.egraphMu.RLock()
	_, registered := c.resultsByID[oldID]
	_, retained := c.persistedEdgesByResult[oldID]
	c.egraphMu.RUnlock()
	assert.Assert(t, !registered, "the failed entry is collected")
	assert.Assert(t, !retained, "its retention edge is gone")
	assert.Equal(t, sharedResultID(0), currentEntryTestIndexed(t, c, frame))

	next, err := publish("session-c", &captureTestValue{text: "next"})
	assert.NilError(t, err)
	assert.Assert(t, next.cacheSharedResult().id != oldID)
	assert.Equal(t, next.cacheSharedResult().id, currentEntryTestIndexed(t, c, frame))
	for _, s := range []string{"session-b", "session-c"} {
		assert.NilError(t, c.ReleaseSession(ctx, s))
	}
}

// attachResult publishes a detached child like any other publication: when
// the child's recipe already has a live entry, the entry is adopted and the
// detached value released (D1).
func TestCacheAttachResultAdoptsTheRecipesLiveEntry(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	key := cacheTestIntCall("current-entry-attach-result")

	var releasedDetached atomic.Bool
	detached := cacheTestIntResultWithOnRelease(key, 1, func(context.Context) error {
		releasedDetached.Store(true)
		return nil
	})
	// The attach misses, then parks before publishing; another session
	// publishes the recipe meanwhile.
	parked, resume := make(chan struct{}), make(chan struct{})
	c.testBeforePublicationIndex = func(oc *ongoingCall) {
		if oc.val != nil && oc.val.cacheSharedResult() == detached.cacheSharedResult() {
			close(parked)
			<-resume
		}
	}
	attached := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.AttachResult(ctx, "session-b", noopTypeResolver{}, detached)
		attached <- currentEntryTestOutcome{res, err}
	}()
	<-parked
	published, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: key}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(key, 2), nil
	})
	assert.NilError(t, err)
	close(resume)
	out := <-attached
	assert.NilError(t, out.err)
	assert.Equal(t, published.cacheSharedResult().id, out.res.cacheSharedResult().id)
	assert.Assert(t, releasedDetached.Load(), "the detached value is released")
	assert.Equal(t, 2, cacheTestUnwrapInt(t, out.res))
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}

// currentEntryTestPublish publishes a persistable value for field in session.
func currentEntryTestPublish(t *testing.T, ctx context.Context, c *Cache, srv *Server, session, field string, value Typed) AnyResult {
	t.Helper()
	return currentEntryTestPublishRetained(t, ctx, c, srv, session, field, value, true)
}

// currentEntryTestPublishRetained publishes a value for field in session,
// persistable or not.
func currentEntryTestPublishRetained(t *testing.T, ctx context.Context, c *Cache, srv *Server, session, field string, value Typed, persistable bool) AnyResult {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
	res, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame, IsPersistable: persistable}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame)
	})
	require.NoError(t, err)
	return res
}

// currentEntryTestIndexedField returns the entry the recipe index names for
// the field frames currentEntryTestPublish builds.
func currentEntryTestIndexedField(t *testing.T, c *Cache, field string, value Typed) sharedResultID {
	t.Helper()
	return currentEntryTestIndexed(t, c, &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())})
}

// currentEntryTestRequireHandle retains under parent a session-resource leaf
// for handle, as a late explicit dependency: parent's stored requirements now
// include handle, with no change to its recipe.
func currentEntryTestRequireHandle(t *testing.T, ctx context.Context, c *Cache, srv *Server, session string, parent AnyResult, handle SessionResourceHandle) {
	t.Helper()
	leaf, err := cacheTestSessionResourceLeaf(ctx, handle)
	require.NoError(t, err)
	dep, err := c.AttachResult(ctx, session, srv, leaf)
	require.NoError(t, err)
	require.NoError(t, c.AddExplicitDependency(ctx, parent, dep, "test_late_dep"))
}

// A restart restores whether the recipe index named each saved entry: with
// the recipe's current entry and a session's unindexed entry both retained,
// only the current one is indexed after boot.
func TestCacheRestartKeepsTheRecipeIndexState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	handle := cacheTestVolatileSessionResourceHandle("CURRENT_ENTRY_RESTART")
	aCtx := currentEntryTestSession(ctx, c, "session-a")
	bCtx := currentEntryTestSession(ctx, c, "session-b")
	require.NoError(t, c.BindSessionResource(aCtx, "session-a", "session-a-client", handle, "a"))

	current := currentEntryTestPublish(t, aCtx, c, srv, "session-a", "current-entry-restart-filter", String("a"))
	currentEntryTestRequireHandle(t, aCtx, c, srv, "session-a", current, handle)
	unindexed := currentEntryTestPublish(t, bCtx, c, srv, "session-b", "current-entry-restart-filter", String("b"))
	currentID, unindexedID := current.cacheSharedResult().id, unindexed.cacheSharedResult().id
	require.NotEqual(t, currentID, unindexedID)
	require.Equal(t, currentID, currentEntryTestIndexedField(t, c, "current-entry-restart-filter", String("")))

	require.NoError(t, c.ReleaseSession(aCtx, "session-a"))
	require.NoError(t, c.ReleaseSession(bCtx, "session-b"))
	require.NoError(t, c.Close(ctx))
	_, c, _ = persistedListTestCache(t, path)

	c.egraphMu.RLock()
	restoredCurrent, restoredUnindexed := c.resultsByID[currentID], c.resultsByID[unindexedID]
	c.egraphMu.RUnlock()
	require.NotNil(t, restoredCurrent)
	require.NotNil(t, restoredUnindexed, "the unindexed entry is restored")
	require.Equal(t, currentID, currentEntryTestIndexedField(t, c, "current-entry-restart-filter", String("")))
	c.egraphMu.RLock()
	require.Empty(t, restoredUnindexed.recipeKeys, "only the current entry is indexed after boot")
	c.egraphMu.RUnlock()
}

// When the recipe's current entry is collected, an entry a session registered
// beside it stays live and unindexed; the recipe's next publication registers
// fresh and becomes the current entry.
func TestCachePublicationAfterTheCurrentEntryIsCollected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	srv := cacheTestServer(t)
	handle := cacheTestVolatileSessionResourceHandle("CURRENT_ENTRY_COLLECTED")
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "current-entry-collected",
	}
	aCtx := currentEntryTestSession(ctx, c, "session-a")
	bCtx := currentEntryTestSession(ctx, c, "session-b")
	cCtx := currentEntryTestSession(ctx, c, "session-c")
	for _, s := range []struct {
		ctx context.Context
		id  string
	}{{aCtx, "session-a"}, {cCtx, "session-c"}} {
		assert.NilError(t, c.BindSessionResource(s.ctx, s.id, s.id+"-client", handle, s.id))
	}

	// Session C starts computing before the recipe has any entry.
	cStarted, cFinish := make(chan struct{}), make(chan struct{})
	cDone := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(cCtx, "session-c", srv, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
			close(cStarted)
			<-cFinish
			return NewResultForCall(&cacheTestObject{Value: 3}, frame.clone())
		})
		cDone <- currentEntryTestOutcome{res, err}
	}()
	<-cStarted

	current, err := c.GetOrInitCall(aCtx, "session-a", srv, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&cacheTestObject{Value: 1}, frame.clone())
	})
	assert.NilError(t, err)
	currentEntryTestRequireHandle(t, aCtx, c, srv, "session-a", current, handle)
	beside, err := c.GetOrInitCall(bCtx, "session-b", srv, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&cacheTestObject{Value: 2}, frame.clone())
	})
	assert.NilError(t, err)
	currentID, besideID := current.cacheSharedResult().id, beside.cacheSharedResult().id
	assert.Assert(t, besideID != currentID)

	assert.NilError(t, c.ReleaseSession(aCtx, "session-a"))
	c.egraphMu.RLock()
	_, currentLive := c.resultsByID[currentID]
	besideRow := c.resultsByID[besideID]
	c.egraphMu.RUnlock()
	assert.Assert(t, !currentLive, "the current entry is collected")
	assert.Assert(t, besideRow != nil, "the entry beside it stays live")
	c.egraphMu.RLock()
	assert.Equal(t, 0, len(besideRow.recipeKeys), "and unindexed")
	c.egraphMu.RUnlock()
	assert.Equal(t, sharedResultID(0), currentEntryTestIndexed(t, c, frame))

	close(cFinish)
	out := <-cDone
	assert.NilError(t, out.err)
	cID := out.res.cacheSharedResult().id
	assert.Assert(t, cID != besideID)
	assert.Equal(t, cID, currentEntryTestIndexed(t, c, frame), "the next publication becomes the current entry")
	assert.NilError(t, c.ReleaseSession(bCtx, "session-b"))
	assert.NilError(t, c.ReleaseSession(cCtx, "session-c"))
}

// A restart with a retained entry R that depends on a retired entry P, and
// P's recipe's current entry P' expired before the restart: after boot the
// index names P'; releasing R collects P while P' stays indexed; a
// replacement count survives the restart.
func TestCacheRestartWithARetiredDependency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	s1 := currentEntryTestSession(ctx, c, "session-1")
	s2 := currentEntryTestSession(ctx, c, "session-2")

	// P is kept only by R, which depends on it.
	p := currentEntryTestPublishRetained(t, s1, c, srv, "session-1", "current-entry-restart-p", String("p"), false)
	r := currentEntryTestPublish(t, s1, c, srv, "session-1", "current-entry-restart-r", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{p}})
	x := currentEntryTestPublish(t, s1, c, srv, "session-1", "current-entry-restart-x", String("x"))
	pID, rID, xID := p.cacheSharedResult().id, r.cacheSharedResult().id, x.cacheSharedResult().id
	require.NoError(t, c.ReleaseSession(s1, "session-1"))
	currentEntryTestExpire(c, p)
	currentEntryTestExpire(c, x)

	pNext := currentEntryTestPublish(t, s2, c, srv, "session-2", "current-entry-restart-p", String("p2"))
	xNext := currentEntryTestPublish(t, s2, c, srv, "session-2", "current-entry-restart-x", String("x2"))
	pNextID := pNext.cacheSharedResult().id
	require.NotEqual(t, pID, pNextID, "P is retired: R depends on it")
	require.Equal(t, xID, xNext.cacheSharedResult().id, "X is replaced in place")
	require.NoError(t, c.ReleaseSession(s2, "session-2"))
	currentEntryTestExpire(c, pNext)
	require.NoError(t, c.Close(ctx))

	ctx, c, _ = persistedListTestCache(t, path)
	require.Equal(t, pNextID, currentEntryTestIndexedField(t, c, "current-entry-restart-p", String("")))
	c.egraphMu.RLock()
	require.NotNil(t, c.resultsByID[pID], "the retired entry is restored for its dependent")
	require.Empty(t, c.resultsByID[pID].recipeKeys)
	require.Equal(t, uint64(1), c.resultsByID[xID].replacements, "the replacement count survives the restart")
	c.egraphMu.RUnlock()

	_, removed, err := c.removePersistedEdge(ctx, rID)
	require.NoError(t, err)
	require.True(t, removed)
	c.egraphMu.RLock()
	_, rLive := c.resultsByID[rID]
	_, pLive := c.resultsByID[pID]
	_, pNextLive := c.resultsByID[pNextID]
	c.egraphMu.RUnlock()
	require.False(t, rLive)
	require.False(t, pLive, "releasing R collects the retired P")
	require.True(t, pNextLive)
	require.Equal(t, pNextID, currentEntryTestIndexedField(t, c, "current-entry-restart-p", String("")))
}

// currentEntryTestLazySpanAttrs returns the attributes of the lazy spans rec
// ended for entry id.
func currentEntryTestLazySpanAttrs(rec *tracetest.SpanRecorder, id sharedResultID) []map[attribute.Key]attribute.Value {
	var out []map[attribute.Key]attribute.Value
	for _, span := range rec.Ended() {
		attrs := map[attribute.Key]attribute.Value{}
		for _, kv := range span.Attributes() {
			attrs[kv.Key] = kv.Value
		}
		if attrs[telemetryattrs.CacheResultIDAttr].AsString() == strconv.FormatUint(uint64(id), 10) {
			out = append(out, attrs)
		}
	}
	return out
}

// The replacement count names the value that a report of parts describes: the
// call that replaced a value reports count 1 and the new value's parts, not
// the old value's; the new value's lazy span reports the count too; a value
// never replaced reports none.
func TestCachePublicationReplacementIsReported(t *testing.T) {
	ctx, c, srv := statePartCache(t, "")
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { assert.NilError(t, tp.Shutdown(context.Background())) })
	spanCtx, demand := tp.Tracer("test").Start(ctx, "demand")
	defer demand.End()
	const field = "current-entry-reported"
	snapshot, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)

	publish := func(session string) (AnyResult, CacheResultState) {
		req := cacheTestArmedRequest(statePartFrame(field))
		req.IsPersistable = true
		res, err := c.GetOrInitCall(ctx, session, srv, req, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&statePartValue{lazy: true}, statePartFrame(field))
		})
		assert.NilError(t, err)
		state, ok := req.CacheEvidence.ResultState(ctx, res)
		assert.Assert(t, ok)
		return res, state
	}

	first, state := publish("session-a")
	assert.Equal(t, uint64(0), state.Replacements)
	assert.NilError(t, c.EvaluateParts(spanCtx, first, "snapshot"))
	firstID := first.cacheSharedResult().id
	for _, attrs := range currentEntryTestLazySpanAttrs(rec, firstID) {
		_, has := attrs[telemetryattrs.CacheReplacementsAttr]
		assert.Assert(t, !has, "a value never replaced reports no count")
	}
	_, parts := statePartHit(t, ctx, c, srv, field)
	assert.DeepEqual(t, []string{snapshot}, parts)
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "test-session"))
	currentEntryTestExpire(c, first)

	replaced, state := publish("session-b")
	assert.Equal(t, firstID, replaced.cacheSharedResult().id)
	assert.Equal(t, uint64(1), state.Replacements)
	assert.Equal(t, 0, len(state.Parts), "the new value's part is pending; the old value's is gone")

	bSpanCtx := engine.ContextWithClientMetadata(spanCtx, &engine.ClientMetadata{ClientID: "session-b-client", SessionID: "session-b"})
	assert.NilError(t, c.EvaluateParts(bSpanCtx, replaced, "snapshot"))
	lazy := currentEntryTestLazySpanAttrs(rec, firstID)
	last := lazy[len(lazy)-1]
	assert.Equal(t, "1", last[telemetryattrs.CacheReplacementsAttr].AsString())
	assert.DeepEqual(t, []string{snapshot}, last[telemetryattrs.CachePartsAttr].AsStringSlice())
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}

// A snapshot-sharing pass that completes a part of a replaced value reports
// the entry's replacement count with it, so the part is the new value's.
func TestSnapshotShareReportsTheReplacementCount(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	var (
		mu      sync.Mutex
		reports [][]SnapshotSharedPart
	)
	c.shareReport = func(parts []SnapshotSharedPart) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, slices.Clone(parts))
	}
	barrier := newSharePassBarrier(c)
	old := currentEntryTestPublish(t, ctx, c, srv, "old-session", "report-receiver", &transferTestValue{Text: "old"})
	require.NoError(t, c.ReleaseSession(ctx, "old-session"))
	currentEntryTestExpire(c, old)
	receiver := currentEntryTestPublish(t, ctx, c, srv, "test-session", "report-receiver", &transferTestValue{Text: "pending"})
	receiverID := receiver.cacheSharedResult().id
	require.Equal(t, old.cacheSharedResult().id, receiverID, "the expired entry takes the new value in place")
	donor := persistedListTestResult(t, ctx, c, srv, "report-donor", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "donor-snapshot"}}})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	shareTestUnite(t, ctx, c, "share-replaced", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t), "one planned slot")

	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	c.egraphMu.RLock()
	deps := sortedResultIDs(receiver.cacheSharedResult().deps)
	c.egraphMu.RUnlock()
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, [][]SnapshotSharedPart{{{
		ResultID:     uint64(receiverID),
		Part:         snapshotKey,
		Deps:         deps,
		Replacements: 1,
	}}}, reports)
}

// A cached nil result has neither a value nor an envelope, but it is a value:
// the Cloud holding it does not make it an entry known only through holdings.
// Only another engine cache's holding does.
func TestCacheNilResultWithACloudHoldingIsStillServed(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	key := cacheTestIntCall("current-entry-nil-cloud")
	var calls int
	fn := func(context.Context) (AnyResult, error) {
		calls++
		return nil, nil
	}
	first, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key}, fn)
	assert.NilError(t, err)
	recipe, err := key.deriveRecipeDigest(c)
	assert.NilError(t, err)
	_, err = c.AttachRemoteHolding(ctx, HolderKey{Cache: cloudCacheID, Number: 1}, RemoteHolding{Recipe: recipe, Request: recipe, Field: key.Field, TypeName: "Int"})
	assert.NilError(t, err)

	second, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: key}, fn)
	assert.NilError(t, err)
	assert.Equal(t, 1, calls, "the nil result is still a hit")
	assert.Assert(t, second.HitCache())
	assert.Equal(t, first.cacheSharedResult().id, second.cacheSharedResult().id)
	cacheTestReleaseSession(t, c, ctx)
}

// A call answered with nothing is indexed under its request's recipe, which
// indexing stores as its frame: nil results of different recipes are
// different entries.
func TestCacheNilResultsOfDifferentRecipesStaySeparate(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	nothing := func(context.Context) (AnyResult, error) { return nil, nil }
	keyA, keyB := cacheTestIntCall("current-entry-nil-a"), cacheTestIntCall("current-entry-nil-b")
	a, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{ResultCall: keyA}, nothing)
	assert.NilError(t, err)
	b, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: keyB}, nothing)
	assert.NilError(t, err)
	aID, bID := a.cacheSharedResult().id, b.cacheSharedResult().id
	assert.Assert(t, aID != bID)
	assert.Equal(t, aID, currentEntryTestIndexed(t, c, keyA))
	assert.Equal(t, bID, currentEntryTestIndexed(t, c, keyB))
	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
}
