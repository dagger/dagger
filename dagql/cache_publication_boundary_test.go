package dagql

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
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
	c.egraphMu.Unlock()
	assert.Assert(t, !persistedAfterFailure)

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

// A persistable call whose fn returns an already-attached result must not
// adopt a canonical sibling whose attachment is still open: adoption skips
// the attach barrier and the handoff commits the persisted edge without
// re-checking attachment, so an open sibling that later fails would end up
// registered, persisted, and errored.
func TestCacheAdoptionSkipsOpenAttachmentSibling(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)

	contentDigest := digest.FromString("adoption-open-sibling-content")

	// Session A publishes the open sibling first, so it has the lowest shared
	// result ID and would win the canonical pick if open results were
	// eligible. Its attachment blocks until the test releases it.
	attachErr := errors.New("open sibling attachment failed")
	attachmentStarted := make(chan struct{})
	allowFailure := make(chan struct{})
	openCall := cacheTestIntCall("adoption-open-sibling-open")
	leaderErrCh := make(chan error, 1)
	go func() {
		_, err := c.GetOrInitCall(ctx, "session-a", noopTypeResolver{}, &CallRequest{
			ResultCall: openCall,
		}, func(ctx context.Context) (AnyResult, error) {
			detached := cacheTestDetachedResult(openCall, cacheTestLeaseCheckedInt{
				Int: NewInt(1),
				onAttach: func(context.Context) error {
					close(attachmentStarted)
					<-allowFailure
					return attachErr
				},
			})
			return detached.WithContentDigest(ctx, contentDigest)
		})
		leaderErrCh <- err
	}()
	<-attachmentStarted
	openID, err := c.resultIDForCall(openCall)
	assert.NilError(t, err)
	c.egraphMu.RLock()
	open := c.resultsByID[openID]
	c.egraphMu.RUnlock()
	assert.Assert(t, open != nil)

	// Session B publishes a clean equivalent: same content digest, different
	// recipe, higher shared result ID.
	cleanCall := cacheTestIntCall("adoption-open-sibling-clean")
	cleanRes, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{
		ResultCall: cleanCall,
	}, func(ctx context.Context) (AnyResult, error) {
		detached := cacheTestDetachedResult(cleanCall, NewInt(2))
		return detached.WithContentDigest(ctx, contentDigest)
	})
	assert.NilError(t, err)
	clean := cleanRes.cacheSharedResult()
	assert.Assert(t, open.id < clean.id, "open sibling must be the lowest-ID candidate")

	// A persistable call whose fn returns the clean result. Adoption must pick
	// the clean result even though the open sibling has the lower ID. If it
	// adopted the open sibling instead, this call would block on its barrier
	// and later persist the errored result.
	adoptCall := cacheTestIntCall("adoption-open-sibling-adopt")
	type adoptOutcome struct {
		res AnyResult
		err error
	}
	adoptDone := make(chan adoptOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(ctx, "session-b", noopTypeResolver{}, &CallRequest{
			ResultCall:    adoptCall,
			IsPersistable: true,
		}, func(ctx context.Context) (AnyResult, error) {
			return cleanRes, nil
		})
		adoptDone <- adoptOutcome{res, err}
	}()

	var adopted adoptOutcome
	select {
	case adopted = <-adoptDone:
	case <-time.After(10 * time.Second):
		t.Fatal("adopting call did not complete; it is blocked on the open sibling's attach barrier")
	}
	assert.NilError(t, adopted.err)
	assert.Equal(t, clean.id, adopted.res.cacheSharedResult().id)

	c.egraphMu.RLock()
	_, openPersisted := c.persistedEdgesByResult[open.id]
	_, cleanPersisted := c.persistedEdgesByResult[clean.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !openPersisted, "open sibling must not gain a persisted edge")
	assert.Assert(t, cleanPersisted, "adopted clean result must carry the persisted edge")

	close(allowFailure)
	assert.ErrorIs(t, <-leaderErrCh, attachErr)

	c.egraphMu.RLock()
	_, openPersistedAfterFailure := c.persistedEdgesByResult[open.id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !openPersistedAfterFailure, "errored sibling must not gain a persisted edge")

	assert.NilError(t, c.ReleaseSession(ctx, "session-a"))
	assert.NilError(t, c.ReleaseSession(ctx, "session-b"))
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
