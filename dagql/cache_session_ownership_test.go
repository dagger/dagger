package dagql

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"gotest.tools/v3/assert"
)

func assertCacheOwnershipExact(t *testing.T, c *Cache, activeHolds ...*ongoingCall) {
	t.Helper()
	c.egraphMu.RLock()
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	defer c.egraphMu.RUnlock()

	expected := make(map[sharedResultID]int64, len(c.resultsByID))
	for sessionID, resultIDs := range c.sessionResultIDsBySession {
		for resultID := range resultIDs {
			_, found := c.resultsByID[resultID]
			assert.Assert(t, found, "session %q references missing result %d", sessionID, resultID)
			expected[resultID]++
		}
	}
	for resultID, shared := range c.resultsByID {
		if shared.depParents != nil {
			expected[resultID] += int64(shared.depParents.Size())
			for parentID := range shared.depParents.Items() {
				parent := c.resultsByID[parentID]
				assert.Assert(t, parent != nil, "result %d references missing parent %d", resultID, parentID)
				_, found := parent.deps[resultID]
				assert.Assert(t, found, "parent %d is missing dependency %d", parentID, resultID)
			}
		}
		if _, found := c.persistedEdgesByResult[resultID]; found {
			expected[resultID]++
		}
		for _, oc := range activeHolds {
			if oc != nil && oc.res == shared && oc.handoffHoldActive {
				expected[resultID]++
			}
		}
		assert.Equal(t, shared.incomingOwnershipCount, expected[resultID],
			"result %d ownership count", resultID)

		for depID := range shared.deps {
			dep := c.resultsByID[depID]
			assert.Assert(t, dep != nil, "result %d references missing dependency %d", resultID, depID)
			assert.Assert(t, dep.depParents != nil && dep.depParents.Contains(resultID),
				"dependency %d is missing parent %d", depID, resultID)
		}
	}
}

func assertArbitraryOwnershipExact(t *testing.T, c *Cache) {
	t.Helper()
	c.callsMu.Lock()
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	defer c.callsMu.Unlock()

	expected := map[string]int{}
	for _, callKeys := range c.sessionArbitraryCallKeysBySession {
		for callKey := range callKeys {
			expected[callKey]++
		}
	}
	seen := map[*sharedArbitraryResult]struct{}{}
	registered := map[string]struct{}{}
	for _, calls := range []map[string]*sharedArbitraryResult{c.ongoingArbitraryCalls, c.completedArbitraryCalls} {
		for callKey, shared := range calls {
			registered[callKey] = struct{}{}
			if _, found := seen[shared]; found {
				continue
			}
			seen[shared] = struct{}{}
			assert.Equal(t, shared.ownerSessionCount, expected[callKey],
				"arbitrary value %q ownership count", callKey)
		}
	}
	for callKey := range expected {
		_, found := registered[callKey]
		assert.Assert(t, found, "session references missing arbitrary value %q", callKey)
	}
}

func newSessionOwnershipTestResult(t *testing.T, c *Cache, sessionID string) AnyResult {
	t.Helper()
	call := cacheTestIntCall("session-ownership-" + sessionID)
	res, err := c.GetOrInitCall(t.Context(), sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)
	return res
}

func TestCacheSessionResultClaimAndReleaseAreAtomic(t *testing.T) {
	t.Run("claim wins", func(t *testing.T) {
		c, err := NewCache(t.Context(), "", nil, nil)
		assert.NilError(t, err)
		res := newSessionOwnershipTestResult(t, c, "seed")

		recorded := make(chan struct{})
		allowClaim := make(chan struct{})
		c.testAfterSessionResultRecord = func() {
			close(recorded)
			<-allowClaim
		}
		claimErr := make(chan error, 1)
		go func() {
			claimErr <- c.trackSessionResult(t.Context(), "racing", res, true)
		}()
		<-recorded

		releaseStarted := make(chan struct{})
		releaseErr := make(chan error, 1)
		go func() {
			close(releaseStarted)
			releaseErr <- c.ReleaseSession(t.Context(), "racing")
		}()
		<-releaseStarted
		close(allowClaim)
		assert.NilError(t, <-claimErr)
		assert.NilError(t, <-releaseErr)
		c.testAfterSessionResultRecord = nil

		assertCacheOwnershipExact(t, c)
		assert.NilError(t, c.ReleaseSession(t.Context(), "seed"))
	})

	t.Run("release wins", func(t *testing.T) {
		c, err := NewCache(t.Context(), "", nil, nil)
		assert.NilError(t, err)
		res := newSessionOwnershipTestResult(t, c, "seed")

		recorded := make(chan struct{})
		allowRelease := make(chan struct{})
		c.testAfterSessionReleaseRecord = func() {
			close(recorded)
			<-allowRelease
		}
		releaseErr := make(chan error, 1)
		go func() {
			releaseErr <- c.ReleaseSession(t.Context(), "racing")
		}()
		<-recorded

		err = c.trackSessionResult(t.Context(), "racing", res, true)
		assert.ErrorIs(t, err, ErrCacheSessionReleased)
		close(allowRelease)
		assert.NilError(t, <-releaseErr)
		c.testAfterSessionReleaseRecord = nil

		assertCacheOwnershipExact(t, c)
		assert.NilError(t, c.ReleaseSession(t.Context(), "seed"))
	})
}

func TestCacheRefusedFinalWaiterCommitsPersistenceBeforeHandoffRelease(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)

	const sessionID = "refused-final-waiter"
	call := cacheTestIntCall("refused-final-waiter")
	publicationBlocked := make(chan struct{})
	allowPublication := make(chan struct{})
	holdCaptured := make(chan *ongoingCall, 1)
	c.testAfterHandoffHoldAcquired = func(oc *ongoingCall) {
		holdCaptured <- oc
	}

	resultCh := make(chan AnyResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := c.GetOrInitCall(t.Context(), sessionID, noopTypeResolver{}, &CallRequest{
			ResultCall:     call,
			ConcurrencyKey: sessionID,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestDetachedResult(call, cacheTestLeaseCheckedInt{
				Int: NewInt(42),
				onAttach: func(context.Context) error {
					close(publicationBlocked)
					<-allowPublication
					return nil
				},
			}), nil
		})
		resultCh <- res
		errCh <- err
	}()

	oc := <-holdCaptured
	<-publicationBlocked
	c.callsMu.Lock()
	oc.waiters++
	oc.isPersistable.Store(true)
	oc.persistedEdgeExpiresAtUnix = time.Now().Unix() + 60
	c.callsMu.Unlock()
	close(allowPublication)

	res := <-resultCh
	assert.NilError(t, <-errCh)
	assert.Assert(t, res != nil)
	assertCacheOwnershipExact(t, c, oc)

	assert.NilError(t, c.ReleaseSession(t.Context(), sessionID))
	assertCacheOwnershipExact(t, c, oc)

	_, err = c.wait(t.Context(), sessionID, noopTypeResolver{}, oc, &CallRequest{
		ResultCall:     call,
		ConcurrencyKey: sessionID,
		IsPersistable:  true,
	}, true)
	assert.ErrorIs(t, err, ErrCacheSessionReleased)
	assert.Assert(t, !oc.handoffHoldActive)
	assertCacheOwnershipExact(t, c)

	shared := res.cacheSharedResult()
	c.egraphMu.RLock()
	_, persisted := c.persistedEdgesByResult[shared.id]
	ownershipCount := shared.incomingOwnershipCount
	c.egraphMu.RUnlock()
	assert.Assert(t, persisted)
	assert.Equal(t, ownershipCount, int64(1))
}

func TestCacheArbitrarySessionClaimAndReleaseAreAtomic(t *testing.T) {
	newCacheWithSeed := func(t *testing.T) (*Cache, string) {
		t.Helper()
		c, err := NewCache(t.Context(), "", nil, nil)
		assert.NilError(t, err)
		const key = "arbitrary-session-ownership"
		_, err = c.GetOrInitArbitrary(t.Context(), "seed", key, ArbitraryValueFunc("value"))
		assert.NilError(t, err)
		return c, key
	}

	t.Run("claim wins", func(t *testing.T) {
		c, key := newCacheWithSeed(t)
		recorded := make(chan struct{})
		allowClaim := make(chan struct{})
		c.testAfterSessionArbitraryRecord = func() {
			close(recorded)
			<-allowClaim
		}
		claimErr := make(chan error, 1)
		go func() {
			_, err := c.GetOrInitArbitrary(t.Context(), "racing", key, ArbitraryValueFunc("ignored"))
			claimErr <- err
		}()
		<-recorded

		releaseStarted := make(chan struct{})
		releaseErr := make(chan error, 1)
		go func() {
			close(releaseStarted)
			releaseErr <- c.ReleaseSession(t.Context(), "racing")
		}()
		<-releaseStarted
		close(allowClaim)
		assert.ErrorIs(t, <-claimErr, ErrCacheSessionReleased)
		assert.NilError(t, <-releaseErr)
		c.testAfterSessionArbitraryRecord = nil

		assertArbitraryOwnershipExact(t, c)
		assert.NilError(t, c.ReleaseSession(t.Context(), "seed"))
		assertArbitraryOwnershipExact(t, c)
		assert.Equal(t, c.Size(), 0)
	})

	t.Run("release wins", func(t *testing.T) {
		c, key := newCacheWithSeed(t)
		recorded := make(chan struct{})
		allowRelease := make(chan struct{})
		c.testAfterSessionReleaseRecord = func() {
			close(recorded)
			<-allowRelease
		}
		releaseErr := make(chan error, 1)
		go func() {
			releaseErr <- c.ReleaseSession(t.Context(), "racing")
		}()
		<-recorded

		_, err := c.GetOrInitArbitrary(t.Context(), "racing", key, ArbitraryValueFunc("ignored"))
		assert.ErrorIs(t, err, ErrCacheSessionReleased)
		close(allowRelease)
		assert.NilError(t, <-releaseErr)
		c.testAfterSessionReleaseRecord = nil

		assertArbitraryOwnershipExact(t, c)
		assert.NilError(t, c.ReleaseSession(t.Context(), "seed"))
		assertArbitraryOwnershipExact(t, c)
		assert.Equal(t, c.Size(), 0)
	})
}

func TestCacheArbitraryReleasedSessionDoesNotInitializeValue(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	assert.NilError(t, c.ReleaseSession(t.Context(), "released"))

	var releaseCalls atomic.Int32
	_, err = c.GetOrInitArbitrary(t.Context(), "released", "refused-unowned", func(context.Context) (any, error) {
		return cacheTestOpaqueValue{
			value: "value",
			onRelease: func(context.Context) error {
				releaseCalls.Add(1)
				return nil
			},
		}, nil
	})
	assert.ErrorIs(t, err, ErrCacheSessionReleased)
	assert.Equal(t, releaseCalls.Load(), int32(0))
	assert.Equal(t, c.Size(), 0)
	assertArbitraryOwnershipExact(t, c)
}

func TestCacheReleasedSessionErrorIncludesSessionID(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	assert.NilError(t, c.ReleaseSession(t.Context(), "released-session"))

	_, err = c.GetOrInitCall(t.Context(), "released-session", noopTypeResolver{}, &CallRequest{
		ResultCall: cacheTestIntCall("released-session-error"),
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(cacheTestIntCall("released-session-error"), 1), nil
	})
	assert.ErrorIs(t, err, ErrCacheSessionReleased)
	assert.ErrorContains(t, err, fmt.Sprintf("%q", "released-session"))
}

func TestCacheSessionReleaseRefusesClaimedResultAtOperationExit(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const sessionID = "release-inflight"
	call := cacheTestIntCall("release-inflight")
	var releaseCalls atomic.Int32
	_, err = c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return Result[Typed]{shared: &sharedResult{
			self: cacheTestOnReleaseInt{Int: NewInt(42), onRelease: func(context.Context) error {
				releaseCalls.Add(1)
				return nil
			}},
			resultCall: call,
			hasValue:   true,
		}}, nil
	})
	assert.NilError(t, err)

	atExit := make(chan struct{})
	allowExit := make(chan struct{})
	c.testBeforeSessionOperationExit = func(gotSessionID string) {
		if gotSessionID != sessionID {
			return
		}
		close(atExit)
		<-allowExit
	}
	type callResult struct {
		res AnyResult
		err error
	}
	resultCh := make(chan callResult, 1)
	go func() {
		res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
			return nil, errors.New("cache hit unexpectedly executed initializer")
		})
		resultCh <- callResult{res: res, err: err}
	}()
	<-atExit

	assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	assert.Equal(t, releaseCalls.Load(), int32(0), "release ran while the cache operation was active")

	close(allowExit)
	got := <-resultCh
	assert.Assert(t, got.res == nil)
	assert.ErrorIs(t, got.err, ErrCacheSessionReleased)
	assert.Equal(t, releaseCalls.Load(), int32(1))
	assert.Equal(t, c.Size(), 0)
}

func TestCacheArbitraryReleaseRefusesValueAtOperationExit(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const sessionID = "arbitrary-release-inflight"
	const callKey = "arbitrary-release-inflight"
	var releaseCalls atomic.Int32
	_, err = c.GetOrInitArbitrary(ctx, sessionID, callKey, func(context.Context) (any, error) {
		return cacheTestOpaqueValue{value: "value", onRelease: func(context.Context) error {
			releaseCalls.Add(1)
			return nil
		}}, nil
	})
	assert.NilError(t, err)

	atExit := make(chan struct{})
	allowExit := make(chan struct{})
	c.testBeforeSessionOperationExit = func(gotSessionID string) {
		if gotSessionID != sessionID {
			return
		}
		close(atExit)
		<-allowExit
	}
	type arbitraryCallResult struct {
		res ArbitraryCachedResult
		err error
	}
	resultCh := make(chan arbitraryCallResult, 1)
	go func() {
		res, err := c.GetOrInitArbitrary(ctx, sessionID, callKey, ArbitraryValueFunc("unexpected"))
		resultCh <- arbitraryCallResult{res: res, err: err}
	}()
	<-atExit

	assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	assert.Equal(t, releaseCalls.Load(), int32(0))
	close(allowExit)

	got := <-resultCh
	assert.Assert(t, got.res == nil)
	assert.ErrorIs(t, got.err, ErrCacheSessionReleased)
	assert.Equal(t, releaseCalls.Load(), int32(1))
	assert.Equal(t, c.Size(), 0)
}

func TestCacheDeferredReleaseErrorSurfacesFromClose(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const sessionID = "deferred-release-error"
	call := cacheTestIntCall("deferred-release-error")
	releaseErr := errors.New("deferred release failed")
	_, err = c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return Result[Typed]{shared: &sharedResult{
			self:       cacheTestOnReleaseInt{Int: NewInt(1), onRelease: func(context.Context) error { return releaseErr }},
			resultCall: call,
			hasValue:   true,
		}}, nil
	})
	assert.NilError(t, err)

	atExit := make(chan struct{})
	allowExit := make(chan struct{})
	c.testBeforeSessionOperationExit = func(gotSessionID string) {
		if gotSessionID == sessionID {
			close(atExit)
			<-allowExit
		}
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, ValueFunc(nil))
		callDone <- err
	}()
	<-atExit
	assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	close(allowExit)
	assert.ErrorIs(t, <-callDone, ErrCacheSessionReleased)
	assert.ErrorIs(t, c.Close(ctx), releaseErr)
}

func TestCacheSynchronousReleaseErrorFailsCloseDirty(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const sessionID = "synchronous-release-error"
	call := cacheTestIntCall("synchronous-release-error")
	releaseErr := errors.New("synchronous release failed")
	_, err = c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return Result[Typed]{shared: &sharedResult{
			self:       cacheTestOnReleaseInt{Int: NewInt(1), onRelease: func(context.Context) error { return releaseErr }},
			resultCall: call,
			hasValue:   true,
		}}, nil
	})
	assert.NilError(t, err)

	assert.ErrorIs(t, c.ReleaseSession(ctx, sessionID), releaseErr)
	assert.ErrorIs(t, c.Close(ctx), releaseErr)
}

func TestCacheCloseCancellationFailsDirtyWhileOperationActive(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)

	const sessionID = "close-active"
	call := cacheTestIntCall("close-active")
	_, err = c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)

	atExit := make(chan struct{})
	allowExit := make(chan struct{})
	c.testBeforeSessionOperationExit = func(gotSessionID string) {
		if gotSessionID == sessionID {
			close(atExit)
			<-allowExit
		}
	}
	callDone := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, ValueFunc(nil))
		callDone <- err
	}()
	<-atExit

	closing := make(chan struct{})
	c.testAfterCacheClosing = func() { close(closing) }
	closeCtx, cancelClose := context.WithCancel(ctx)
	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close(closeCtx) }()
	<-closing
	closeRefusedCall := cacheTestIntCall("close-refused")
	_, err = c.GetOrInitCall(ctx, "close-refused", noopTypeResolver{}, &CallRequest{ResultCall: closeRefusedCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(closeRefusedCall, 2), nil
	})
	assert.ErrorIs(t, err, ErrCacheClosed)
	cancelClose()
	assert.ErrorIs(t, <-closeDone, context.Canceled)

	close(allowExit)
	assert.NilError(t, <-callDone)

	reopened, err := NewCache(ctx, dbPath, nil, nil)
	assert.NilError(t, err)
	assert.Equal(t, reopened.PersistenceResetReason(), CachePersistenceResetUncleanShutdown)
	assert.NilError(t, reopened.CloseDiscardingPersistence())
}

// A detached writer that captures telemetry spans after its session was
// released must not recreate the per-session span maps: session IDs are
// single-use, so a recreated entry would be retained for the engine
// lifetime.
func TestCacheReleasedSessionDoesNotRecreateSpanMaps(t *testing.T) {
	ctx := t.Context()
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	const sessionID = "span-capture-released"
	call := cacheTestIntCall("span-capture-released")
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{7},
		SpanID:  trace.SpanID{7},
	})
	spanIn := trace.ContextWithSpanContext(ctx, spanCtx)

	res, err := c.GetOrInitCall(spanIn, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)

	// Positive control: before release, both writers record state, so a
	// post-release no-op below cannot be explained by broken test inputs.
	c.captureSessionLazySpanContext(spanIn, sessionID, res)
	c.captureSessionResultInstallSpan(spanIn, sessionID, res)
	c.sessionMu.Lock()
	_, lazyBefore := c.sessionLazySpansBySession[sessionID]
	_, installBefore := c.sessionResultInstallSpans[sessionID]
	c.sessionMu.Unlock()
	assert.Assert(t, lazyBefore)
	assert.Assert(t, installBefore)

	assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	c.sessionMu.Lock()
	_, lazyAfterRelease := c.sessionLazySpansBySession[sessionID]
	_, installAfterRelease := c.sessionResultInstallSpans[sessionID]
	c.sessionMu.Unlock()
	assert.Assert(t, !lazyAfterRelease)
	assert.Assert(t, !installAfterRelease)

	c.captureSessionLazySpanContext(spanIn, sessionID, res)
	c.captureSessionResultInstallSpan(spanIn, sessionID, res)

	c.sessionMu.Lock()
	_, lazyRecreated := c.sessionLazySpansBySession[sessionID]
	_, installRecreated := c.sessionResultInstallSpans[sessionID]
	c.sessionMu.Unlock()
	assert.Assert(t, !lazyRecreated)
	assert.Assert(t, !installRecreated)
}
