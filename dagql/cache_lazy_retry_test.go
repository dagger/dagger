package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func newLazyRetryTestResult(
	t *testing.T,
	lazyEval LazyEvalFunc,
) (context.Context, *Cache, string, ObjectResult[*cacheTestObject]) {
	t.Helper()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx = ContextWithCache(ctx, c)
	srv := cacheTestServer(t)
	sessionID := cacheTestSessionID(t, ctx)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "lazy-attempt-retry",
	}
	resAny, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
			Value:    1,
			lazyEval: lazyEval,
		}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, c, sessionID, resAny.(ObjectResult[*cacheTestObject])
}

func waitLazyRetrySignal(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitLazyRetryError(t *testing.T, ch <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

func updateLazyRetryMax(maxRunning *atomic.Int32, running int32) {
	for {
		previous := maxRunning.Load()
		if running <= previous || maxRunning.CompareAndSwap(previous, running) {
			return
		}
	}
}

func currentLazyAttempt(shared *sharedResult) (*lazyEvalAttempt, int) {
	shared.lazyMu.Lock()
	defer shared.lazyMu.Unlock()
	if shared.lazyEvalAttempt == nil {
		return nil, 0
	}
	return shared.lazyEvalAttempt, shared.lazyEvalAttempt.waiters
}

func TestCacheEvaluateRetriesForeignCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var running atomic.Int32
		var maxRunning atomic.Int32
		firstStarted := make(chan struct{})
		firstCanceled := make(chan struct{})
		allowFirstFinish := make(chan struct{})

		ctx, c, sessionID, result := newLazyRetryTestResult(t, func(ctx context.Context) error {
			call := calls.Add(1)
			nowRunning := running.Add(1)
			updateLazyRetryMax(&maxRunning, nowRunning)
			defer running.Add(-1)
			if call == 1 {
				close(firstStarted)
				<-ctx.Done()
				close(firstCanceled)
				<-allowFirstFinish
				return context.Cause(ctx)
			}
			return nil
		})
		shared := result.cacheSharedResult()

		leaderCtx, cancelLeader := context.WithCancelCause(ctx)
		leaderDone := make(chan error, 1)
		go func() {
			leaderDone <- c.Evaluate(leaderCtx, result)
		}()
		waitLazyRetrySignal(t, firstStarted, "the first lazy callback to start")

		abandonErr := errors.New("first waiter abandoned")
		cancelLeader(abandonErr)
		if err := waitLazyRetryError(t, leaderDone, "the abandoning leader"); !errors.Is(err, abandonErr) {
			t.Fatalf("leader error = %v, want its own cancellation %v", err, abandonErr)
		}
		waitLazyRetrySignal(t, firstCanceled, "the first callback to observe cancellation")
		oldAttempt, waiters := currentLazyAttempt(shared)
		if oldAttempt == nil || waiters != 0 {
			t.Fatalf("canceled attempt = %p with %d waiters, want published with zero waiters", oldAttempt, waiters)
		}

		healthyDone := make(chan error, 1)
		go func() {
			healthyDone <- c.Evaluate(ctx, result)
		}()
		synctest.Wait()
		joinedAttempt, waiters := currentLazyAttempt(shared)
		if joinedAttempt != oldAttempt || waiters != 1 {
			t.Fatalf("healthy caller joined attempt %p with %d waiters, want canceled attempt %p with one waiter", joinedAttempt, waiters, oldAttempt)
		}

		close(allowFirstFinish)
		if err := waitLazyRetryError(t, healthyDone, "the healthy retrying caller"); err != nil {
			t.Fatalf("healthy caller inherited another waiter's cancellation: %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("lazy callback calls = %d, want two attempts", got)
		}
		if got := maxRunning.Load(); got != 1 {
			t.Fatalf("maximum concurrent lazy callbacks = %d, want one", got)
		}
		if err := c.ReleaseSession(ctx, sessionID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCacheEvaluateRetiresFinishedAttemptBeforeWaitersDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var running atomic.Int32
		var maxRunning atomic.Int32
		firstStarted := make(chan struct{})
		firstCanceled := make(chan struct{})
		allowFirstFinish := make(chan struct{})

		ctx, c, sessionID, result := newLazyRetryTestResult(t, func(ctx context.Context) error {
			call := calls.Add(1)
			nowRunning := running.Add(1)
			updateLazyRetryMax(&maxRunning, nowRunning)
			defer running.Add(-1)
			if call == 1 {
				close(firstStarted)
				<-ctx.Done()
				close(firstCanceled)
				<-allowFirstFinish
				return context.Cause(ctx)
			}
			return nil
		})
		shared := result.cacheSharedResult()

		leaderCtx, cancelLeader := context.WithCancelCause(ctx)
		leaderDone := make(chan error, 1)
		go func() {
			leaderDone <- c.Evaluate(leaderCtx, result)
		}()
		waitLazyRetrySignal(t, firstStarted, "the first lazy callback to start")
		leaderCause := errors.New("leader canceled the first attempt")
		cancelLeader(leaderCause)
		if err := waitLazyRetryError(t, leaderDone, "the first leader"); !errors.Is(err, leaderCause) {
			t.Fatalf("leader error = %v, want %v", err, leaderCause)
		}
		waitLazyRetrySignal(t, firstCanceled, "the callback to observe leader cancellation")
		oldAttempt, _ := currentLazyAttempt(shared)
		if oldAttempt == nil {
			t.Fatal("canceled attempt was unpublished before its callback finished")
		}

		lateCtx, cancelLate := context.WithCancelCause(ctx)
		lateDone := make(chan error, 1)
		go func() {
			lateDone <- c.Evaluate(lateCtx, result)
		}()
		synctest.Wait()
		joinedAttempt, waiters := currentLazyAttempt(shared)
		if joinedAttempt != oldAttempt || waiters != 1 {
			t.Fatalf("late caller joined attempt %p with %d waiters, want canceled attempt %p with one waiter", joinedAttempt, waiters, oldAttempt)
		}

		finished := make(chan struct{})
		allowDoneClose := make(chan struct{})
		c.testAfterLazyEvalFinish = func(attempt *lazyEvalAttempt) {
			if attempt != oldAttempt {
				return
			}
			close(finished)
			<-allowDoneClose
		}
		close(allowFirstFinish)
		waitLazyRetrySignal(t, finished, "the canceled callback to retire its attempt")
		if current, _ := currentLazyAttempt(shared); current != nil {
			t.Fatalf("finished attempt remains published as %p", current)
		}

		lateCause := errors.New("late waiter canceled after callback completion")
		cancelLate(lateCause)
		if err := waitLazyRetryError(t, lateDone, "the late canceled waiter"); !errors.Is(err, lateCause) {
			t.Fatalf("late waiter error = %v, want its own cancellation %v", err, lateCause)
		}

		healthyDone := make(chan error, 1)
		go func() {
			healthyDone <- c.Evaluate(ctx, result)
		}()
		if err := waitLazyRetryError(t, healthyDone, "the caller after completed-attempt retirement"); err != nil {
			t.Fatalf("caller after retirement inherited a stale error: %v", err)
		}
		close(allowDoneClose)
		waitLazyRetrySignal(t, oldAttempt.done, "the retired attempt's completion channel to close")

		if got := calls.Load(); got != 2 {
			t.Fatalf("lazy callback calls = %d, want two attempts", got)
		}
		if got := maxRunning.Load(); got != 1 {
			t.Fatalf("maximum concurrent lazy callbacks = %d, want one", got)
		}
		if err := c.ReleaseSession(ctx, sessionID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCacheEvaluateOwnCancellationOnlyCancelsOwnWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		started := make(chan struct{})
		release := make(chan struct{})

		ctx, c, sessionID, result := newLazyRetryTestResult(t, func(ctx context.Context) error {
			calls.Add(1)
			close(started)
			<-release
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("callback canceled while a healthy waiter remained: %w", context.Cause(ctx))
			}
			return nil
		})
		shared := result.cacheSharedResult()

		leaderDone := make(chan error, 1)
		go func() {
			leaderDone <- c.Evaluate(ctx, result)
		}()
		waitLazyRetrySignal(t, started, "the shared callback to start")

		joinerCtx, cancelJoiner := context.WithCancelCause(ctx)
		joinerDone := make(chan error, 1)
		go func() {
			joinerDone <- c.Evaluate(joinerCtx, result)
		}()
		synctest.Wait()
		_, waiters := currentLazyAttempt(shared)
		if waiters != 2 {
			t.Fatalf("lazy waiters = %d, want two", waiters)
		}

		joinerCause := errors.New("joiner canceled only its own wait")
		cancelJoiner(joinerCause)
		if err := waitLazyRetryError(t, joinerDone, "the canceled joiner"); !errors.Is(err, joinerCause) {
			t.Fatalf("joiner error = %v, want %v", err, joinerCause)
		}
		close(release)
		if err := waitLazyRetryError(t, leaderDone, "the healthy leader"); err != nil {
			t.Fatalf("healthy leader failed: %v", err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("lazy callback calls = %d, want one", got)
		}
		if err := c.ReleaseSession(ctx, sessionID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCacheEvaluatePropagatesCallbackFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// A cancellation-shaped error from a healthy callback context is still a
		// genuine callback failure. Retry attribution must use the attempt context,
		// not error type alone.
		callbackErr := fmt.Errorf("genuine lazy callback failure: %w", context.Canceled)
		var calls atomic.Int32
		var running atomic.Int32
		var maxRunning atomic.Int32
		started := make(chan struct{})
		releaseFailure := make(chan struct{})

		ctx, c, sessionID, result := newLazyRetryTestResult(t, func(context.Context) error {
			call := calls.Add(1)
			nowRunning := running.Add(1)
			updateLazyRetryMax(&maxRunning, nowRunning)
			defer running.Add(-1)
			if call == 1 {
				close(started)
				<-releaseFailure
				return callbackErr
			}
			return nil
		})
		shared := result.cacheSharedResult()

		leaderDone := make(chan error, 1)
		go func() {
			leaderDone <- c.Evaluate(ctx, result)
		}()
		waitLazyRetrySignal(t, started, "the failing callback to start")
		joinerDone := make(chan error, 1)
		go func() {
			joinerDone <- c.Evaluate(ctx, result)
		}()
		synctest.Wait()
		_, waiters := currentLazyAttempt(shared)
		if waiters != 2 {
			t.Fatalf("lazy waiters = %d, want two", waiters)
		}

		close(releaseFailure)
		if err := waitLazyRetryError(t, leaderDone, "the failing leader"); !errors.Is(err, callbackErr) {
			t.Fatalf("leader error = %v, want callback error %v", err, callbackErr)
		}
		if err := waitLazyRetryError(t, joinerDone, "the failing joiner"); !errors.Is(err, callbackErr) {
			t.Fatalf("joiner error = %v, want callback error %v", err, callbackErr)
		}
		if err := c.Evaluate(ctx, result); err != nil {
			t.Fatalf("retry after genuine callback failure: %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("lazy callback calls = %d, want failed attempt and successful retry", got)
		}
		if got := maxRunning.Load(); got != 1 {
			t.Fatalf("maximum concurrent lazy callbacks = %d, want one", got)
		}
		if err := c.ReleaseSession(ctx, sessionID); err != nil {
			t.Fatal(err)
		}
	})
}
