package dagql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func waitForSessionResult(t *testing.T, c *Cache, sessionID string) *sharedResult {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		c.egraphMu.RLock()
		c.sessionMu.Lock()
		var resultID sharedResultID
		for id := range c.sessionResultIDsBySession[sessionID] {
			resultID = id
			break
		}
		c.sessionMu.Unlock()
		res := c.resultsByID[resultID]
		c.egraphMu.RUnlock()
		if res != nil {
			return res
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("timed out waiting for session %q to claim a result", sessionID)
		}
	}
}

func TestCachePersistableAttachmentFailureDoesNotRetain(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)

	call := cacheTestIntCall("persistable-attachment-failure")
	attachErr := errors.New("attachment failed")
	var initCalls atomic.Int32
	_, err = c.GetOrInitCall(t.Context(), "failing-publication", noopTypeResolver{}, &CallRequest{
		ResultCall:     call,
		ConcurrencyKey: "failing-publication",
		IsPersistable:  true,
	}, func(context.Context) (AnyResult, error) {
		initCalls.Add(1)
		return cacheTestDetachedResult(call, cacheTestLeaseCheckedInt{
			Int: NewInt(1),
			onAttach: func(context.Context) error {
				return attachErr
			},
		}), nil
	})
	assert.ErrorIs(t, err, attachErr)

	c.egraphMu.RLock()
	assert.Equal(t, 0, len(c.resultsByID))
	assert.Equal(t, 0, len(c.persistedEdgesByResult))
	c.egraphMu.RUnlock()

	res, err := c.GetOrInitCall(t.Context(), "replacement", noopTypeResolver{}, &CallRequest{
		ResultCall:     call,
		ConcurrencyKey: "replacement",
		IsPersistable:  true,
	}, func(context.Context) (AnyResult, error) {
		initCalls.Add(1)
		return cacheTestIntResult(call, 2), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache())
	assert.Equal(t, int32(2), initCalls.Load())
	c.egraphMu.RLock()
	_, persisted := c.persistedEdgesByResult[res.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, persisted)
}

func TestCachePersistableHitWaitsForCleanAttachmentBeforePersisting(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)

	call := cacheTestIntCall("persistable-hit-waits-for-attachment")
	attachmentStarted := make(chan struct{})
	allowAttachment := make(chan struct{})
	leaderResCh := make(chan AnyResult, 1)
	leaderErrCh := make(chan error, 1)
	go func() {
		res, err := c.GetOrInitCall(t.Context(), "leader", noopTypeResolver{}, &CallRequest{
			ResultCall:     call,
			ConcurrencyKey: "leader",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestDetachedResult(call, cacheTestLeaseCheckedInt{
				Int: NewInt(1),
				onAttach: func(context.Context) error {
					close(attachmentStarted)
					<-allowAttachment
					return nil
				},
			}), nil
		})
		leaderResCh <- res
		leaderErrCh <- err
	}()
	<-attachmentStarted

	hitResCh := make(chan AnyResult, 1)
	hitErrCh := make(chan error, 1)
	go func() {
		res, err := c.GetOrInitCall(t.Context(), "persistable-hit", noopTypeResolver{}, &CallRequest{
			ResultCall:     call,
			ConcurrencyKey: "persistable-hit",
			IsPersistable:  true,
			TTL:            73,
		}, func(context.Context) (AnyResult, error) {
			return nil, errors.New("persistable hit unexpectedly executed")
		})
		hitResCh <- res
		hitErrCh <- err
	}()

	shared := waitForSessionResult(t, c, "persistable-hit")
	c.egraphMu.RLock()
	hitTimeExpiry := shared.expiresAtUnix
	_, persistedBeforeAttachment := c.persistedEdgesByResult[shared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, hitTimeExpiry != 0)
	assert.Assert(t, !persistedBeforeAttachment)

	close(allowAttachment)
	leaderRes := <-leaderResCh
	assert.NilError(t, <-leaderErrCh)
	hitRes := <-hitResCh
	assert.NilError(t, <-hitErrCh)
	assert.Assert(t, !leaderRes.HitCache())
	assert.Assert(t, hitRes.HitCache())

	c.egraphMu.RLock()
	edge, persistedAfterAttachment := c.persistedEdgesByResult[shared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, persistedAfterAttachment)
	assert.Equal(t, hitTimeExpiry, edge.expiresAtUnix)
}

func TestCacheErroredAttachmentIsSkippedAndReexecuted(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	srv := cacheTestServer(t)
	call := cacheTestIntCall("errored-attachment-reexecute")
	attachErr := errors.New("concurrent attachment failed")
	attachmentStarted := make(chan struct{})
	allowFailure := make(chan struct{})
	var initCalls atomic.Int32

	leaderErrCh := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(t.Context(), "leader", srv, &CallRequest{
			ResultCall:     call,
			ConcurrencyKey: "leader",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			return cacheTestDetachedResult(call, cacheTestLeaseCheckedInt{
				Int: NewInt(1),
				onAttach: func(context.Context) error {
					close(attachmentStarted)
					<-allowFailure
					return attachErr
				},
			}), nil
		})
		leaderErrCh <- err
	}()
	<-attachmentStarted

	hitErrCh := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(t.Context(), "persistable-hit", srv, &CallRequest{
			ResultCall:     call,
			ConcurrencyKey: "persistable-hit",
			IsPersistable:  true,
		}, func(context.Context) (AnyResult, error) {
			return nil, errors.New("persistable hit unexpectedly executed")
		})
		hitErrCh <- err
	}()

	poisoned := waitForSessionResult(t, c, "persistable-hit")
	c.egraphMu.RLock()
	_, persistedBeforeFailure := c.persistedEdgesByResult[poisoned.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !persistedBeforeFailure)

	close(allowFailure)
	assert.ErrorIs(t, <-leaderErrCh, attachErr)
	assert.ErrorContains(t, <-hitErrCh, attachErr.Error())
	assert.Equal(t, resultAttachmentFailed, poisoned.attachmentState())

	c.egraphMu.Lock()
	assert.Assert(t, c.resultsByID[poisoned.id] == poisoned)
	_, persistedAfterFailure := c.persistedEdgesByResult[poisoned.id]
	canonical := c.canonicalEquivalentSharedResultLocked("canonical", poisoned, time.Now().Unix())
	c.egraphMu.Unlock()
	assert.Assert(t, !persistedAfterFailure)
	assert.Assert(t, canonical == nil)

	adopt := &ongoingCall{val: Result[Typed]{shared: poisoned}}
	err = c.initCompletedResult(t.Context(), srv, adopt, &CallRequest{ResultCall: call}, "adopt")
	assert.ErrorContains(t, err, "no clean canonical result")

	replacement, err := c.GetOrInitCall(t.Context(), "replacement", srv, &CallRequest{
		ResultCall:     call,
		ConcurrencyKey: "replacement",
	}, func(context.Context) (AnyResult, error) {
		initCalls.Add(1)
		return cacheTestIntResult(call, 2), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !replacement.HitCache())
	assert.Equal(t, int32(2), initCalls.Load())
	assert.Assert(t, replacement.cacheSharedResult().id != poisoned.id)

	resolvedID, err := c.resultIDForCall(call)
	assert.NilError(t, err)
	assert.Equal(t, replacement.cacheSharedResult().id, resolvedID)

	digestHit, hit, err := c.lookupCacheForDigests(t.Context(), "digest", srv, cacheTestCallDigest(call), nil)
	assert.NilError(t, err)
	assert.Assert(t, hit)
	assert.Equal(t, replacement.cacheSharedResult().id, digestHit.cacheSharedResult().id)

	idHit, err := c.LoadResultByResultID(t.Context(), "id-load", srv, uint64(poisoned.id))
	assert.NilError(t, err)
	assert.Equal(t, replacement.cacheSharedResult().id, idHit.cacheSharedResult().id)

	assert.NilError(t, c.ReleaseSession(t.Context(), "persistable-hit"))
	c.egraphMu.RLock()
	assert.Assert(t, c.resultsByID[poisoned.id] == nil)
	c.egraphMu.RUnlock()
	assert.NilError(t, c.ReleaseSession(t.Context(), "replacement"))
	assert.NilError(t, c.ReleaseSession(t.Context(), "digest"))
	assert.NilError(t, c.ReleaseSession(t.Context(), "id-load"))
}

func TestCachePersistedEdgeInstallRejectsCollectedResult(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	call := cacheTestIntCall("collected-persisted-edge")
	res, err := c.GetOrInitCall(t.Context(), "owner", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)
	shared := res.cacheSharedResult()
	assert.NilError(t, c.ReleaseSession(t.Context(), "owner"))

	err = c.MakeResultUnpruneable(t.Context(), res)
	assert.ErrorContains(t, err, "already collected")
	c.egraphMu.RLock()
	_, persisted := c.persistedEdgesByResult[shared.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !persisted)
	assert.Equal(t, int64(0), shared.incomingOwnershipCount)
}

func TestCacheOperationLeaseFailureCancelsSharedContext(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	assert.NilError(t, err)
	leaseErr := errors.New("operation lease unavailable")
	capturedCtx := make(chan context.Context, 1)
	ctx := ContextWithOperationLeaseProvider(t.Context(), OperationLeaseProviderFunc(func(ctx context.Context) (context.Context, func(context.Context) error, error) {
		capturedCtx <- ctx
		return nil, nil, leaseErr
	}))
	call := cacheTestIntCall("lease-failure-cancel")
	_, err = c.GetOrInitCall(ctx, "lease-failure", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.ErrorIs(t, err, leaseErr)

	sharedCtx := <-capturedCtx
	select {
	case <-sharedCtx.Done():
		assert.ErrorIs(t, context.Cause(sharedCtx), leaseErr)
	case <-time.After(5 * time.Second):
		t.Fatal("shared call context was not canceled after operation-lease failure")
	}
}
