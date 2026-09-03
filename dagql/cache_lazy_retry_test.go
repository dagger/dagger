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

// lazyBookkeepingSnapshotManager blocks AttachLease until the test supplies
// an outcome, so the window between a callback body finishing and the
// attempt's bookkeeping settling can be held open deterministically.
type lazyBookkeepingSnapshotManager struct {
	fakeSnapshotManager
	attachEntered chan struct{}
	attachResult  chan error
}

func (m *lazyBookkeepingSnapshotManager) AttachLease(ctx context.Context, leaseID, snapshotID string) error {
	_ = m.fakeSnapshotManager.AttachLease(ctx, leaseID, snapshotID)
	m.attachEntered <- struct{}{}
	return <-m.attachResult
}

func lazySyncState(shared *sharedResult) (pending, complete bool) {
	shared.lazyMu.Lock()
	defer shared.lazyMu.Unlock()
	return shared.lazySyncPending, shared.lazyEvalComplete
}

// A callback body consumes its object-side lazy state (mirroring how core
// types clear their Lazy pointer) before the attempt's snapshot-lease
// bookkeeping settles. During that window a second Evaluate must join the
// running attempt rather than observe the nil object-side callback and
// report success. A failed bookkeeping step must stay retryable instead of
// being swallowed as completed evaluation, and the retry must not re-run
// the already-consumed body.
func TestCacheEvaluateSettlesBookkeepingBeforeReportingComplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := &lazyBookkeepingSnapshotManager{
			attachEntered: make(chan struct{}),
			attachResult:  make(chan error),
		}

		ctx := cacheTestContext(t.Context())
		c, err := NewCache(ctx, "", mgr, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx = ContextWithCache(ctx, c)
		srv := cacheTestServer(t)
		sessionID := cacheTestSessionID(t, ctx)

		var bodyRuns atomic.Int32
		var obj *cacheTestObject
		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-bookkeeping-settle",
		}
		resAny, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			obj = &cacheTestObject{Value: 1}
			obj.lazyEval = func(context.Context) error {
				bodyRuns.Add(1)
				obj.snapshotLinks = []PersistedSnapshotRefLink{{
					RefKey: "lazy-produced-snapshot",
					Role:   "snapshot",
				}}
				obj.lazyEval = nil
				return nil
			}
			return cacheTestObjectResultWithValue(t, srv, frame, obj), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		res := resAny.(ObjectResult[*cacheTestObject])
		shared := res.cacheSharedResult()

		eval1 := make(chan error, 1)
		go func() { eval1 <- c.Evaluate(ctx, res) }()

		// The body has finished and cleared the object-side callback; the
		// attempt is now blocked inside its snapshot-lease bookkeeping.
		waitLazyRetrySignal(t, mgr.attachEntered, "first bookkeeping attach")

		eval2 := make(chan error, 1)
		go func() { eval2 <- c.Evaluate(ctx, res) }()
		synctest.Wait()
		select {
		case err := <-eval2:
			t.Fatalf("second Evaluate returned (%v) while the attempt's bookkeeping was still in flight", err)
		case err := <-eval1:
			t.Fatalf("first Evaluate returned (%v) while its bookkeeping was still in flight", err)
		default:
		}

		injected := errors.New("attach owner lease failed")
		mgr.attachResult <- injected
		if err := waitLazyRetryError(t, eval1, "first Evaluate outcome"); !errors.Is(err, injected) {
			t.Fatalf("first Evaluate error = %v, want the injected bookkeeping failure", err)
		}
		if err := waitLazyRetryError(t, eval2, "second Evaluate outcome"); !errors.Is(err, injected) {
			t.Fatalf("second Evaluate error = %v, want the injected bookkeeping failure", err)
		}

		pending, complete := lazySyncState(shared)
		if !pending || complete {
			t.Fatalf("after failed bookkeeping: pending=%v complete=%v, want pending and not complete", pending, complete)
		}
		if !HasPendingLazyEvaluation(res) {
			t.Fatal("pending bookkeeping must still report as pending lazy evaluation")
		}

		// The retry leads a bookkeeping-only attempt: no body re-run, one
		// more attach, then completion.
		eval3 := make(chan error, 1)
		go func() { eval3 <- c.Evaluate(ctx, res) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "retried bookkeeping attach")
		mgr.attachResult <- nil
		if err := waitLazyRetryError(t, eval3, "third Evaluate outcome"); err != nil {
			t.Fatal(err)
		}

		if got := bodyRuns.Load(); got != 1 {
			t.Fatalf("callback body ran %d times, want exactly 1", got)
		}
		if got := len(mgr.fakeSnapshotManager.attachCalls); got != 2 {
			t.Fatalf("AttachLease called %d times, want 2 (failed then retried)", got)
		}
		pending, complete = lazySyncState(shared)
		if pending || !complete {
			t.Fatalf("after settled bookkeeping: pending=%v complete=%v, want complete and not pending", pending, complete)
		}
		if HasPendingLazyEvaluation(res) {
			t.Fatal("settled evaluation must not report as pending")
		}
	})
}

// A previous attempt's body succeeded but its bookkeeping failed, and the
// value keeps exposing a non-nil callback: nothing forces HasLazyEvaluation
// implementations to clear their callback on success. Pending bookkeeping
// must take precedence over the re-read object-side callback, so the retry
// runs bookkeeping only and the body executes exactly once.
func TestCacheEvaluatePendingBookkeepingSkipsUnclearedCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := &lazyBookkeepingSnapshotManager{
			attachEntered: make(chan struct{}),
			attachResult:  make(chan error),
		}

		ctx := cacheTestContext(t.Context())
		c, err := NewCache(ctx, "", mgr, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx = ContextWithCache(ctx, c)
		srv := cacheTestServer(t)
		sessionID := cacheTestSessionID(t, ctx)

		var bodyRuns atomic.Int32
		var obj *cacheTestObject
		frame := &ResultCall{
			Kind:  ResultCallKindField,
			Type:  NewResultCallType((&cacheTestObject{}).Type()),
			Field: "lazy-uncleared-callback",
		}
		resAny, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			obj = &cacheTestObject{Value: 1}
			// The body deliberately leaves obj.lazyEval armed after success.
			obj.lazyEval = func(context.Context) error {
				bodyRuns.Add(1)
				obj.snapshotLinks = []PersistedSnapshotRefLink{{
					RefKey: "lazy-produced-snapshot",
					Role:   "snapshot",
				}}
				return nil
			}
			return cacheTestObjectResultWithValue(t, srv, frame, obj), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		res := resAny.(ObjectResult[*cacheTestObject])
		shared := res.cacheSharedResult()

		eval1 := make(chan error, 1)
		go func() { eval1 <- c.Evaluate(ctx, res) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "first bookkeeping attach")
		injected := errors.New("attach owner lease failed")
		mgr.attachResult <- injected
		if err := waitLazyRetryError(t, eval1, "first Evaluate outcome"); !errors.Is(err, injected) {
			t.Fatalf("first Evaluate error = %v, want the injected bookkeeping failure", err)
		}
		if pending, complete := lazySyncState(shared); !pending || complete {
			t.Fatalf("after failed bookkeeping: pending=%v complete=%v, want pending and not complete", pending, complete)
		}

		// The retry must lead a bookkeeping-only attempt even though the
		// value still exposes its callback.
		eval2 := make(chan error, 1)
		go func() { eval2 <- c.Evaluate(ctx, res) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "retried bookkeeping attach")
		mgr.attachResult <- nil
		if err := waitLazyRetryError(t, eval2, "second Evaluate outcome"); err != nil {
			t.Fatal(err)
		}

		if got := bodyRuns.Load(); got != 1 {
			t.Fatalf("callback body ran %d times, want exactly 1", got)
		}
		if got := len(mgr.fakeSnapshotManager.attachCalls); got != 2 {
			t.Fatalf("AttachLease called %d times, want 2 (failed then retried)", got)
		}
		if pending, complete := lazySyncState(shared); pending || !complete {
			t.Fatalf("after settled bookkeeping: pending=%v complete=%v, want complete and not pending", pending, complete)
		}

		// Completed evaluation short-circuits before the still-armed
		// callback can be observed again.
		if err := c.Evaluate(ctx, res); err != nil {
			t.Fatal(err)
		}
		if got := bodyRuns.Load(); got != 1 {
			t.Fatalf("callback body ran %d times after completion, want exactly 1", got)
		}
	})
}

// An ordinary cache hit re-registers lazy evaluation through
// ensurePersistedHitValueLoaded while another caller's callback body may be
// clearing the same object-side lazy pointer. Registration must therefore
// consult the published attempt under lazyMu before reading any object-side
// state. This test drives hits concurrently with a running callback and is
// meaningful under the race detector: with the object-side read unordered,
// -race reports the write in the callback against the read in registration.
func TestCacheHitRegistrationDoesNotRaceLazyCallback(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx = ContextWithCache(ctx, c)
	srv := cacheTestServer(t)
	sessionID := cacheTestSessionID(t, ctx)

	blockCallback := make(chan struct{})
	var obj *cacheTestObject
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "lazy-hit-registration-race",
	}
	resAny, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		obj = &cacheTestObject{Value: 1}
		obj.lazyEval = func(context.Context) error {
			obj.lazyEval = nil
			<-blockCallback
			return nil
		}
		return cacheTestObjectResultWithValue(t, srv, frame, obj), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resAny.(ObjectResult[*cacheTestObject])

	evalDone := make(chan error, 1)
	go func() { evalDone <- c.Evaluate(ctx, res) }()

	hit := func() {
		hitRes, hitErr := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			t.Error("hit-path call unexpectedly re-executed")
			return nil, errors.New("unexpected re-execution")
		})
		if hitErr != nil {
			t.Error(hitErr)
		}
		_ = hitRes
	}
	// Hits race the callback body's object-side clear while it runs, then
	// keep going through completion so registration is exercised against
	// every attempt state.
	for range 50 {
		hit()
	}
	close(blockCallback)
	if err := waitLazyRetryError(t, evalDone, "evaluate outcome"); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		hit()
	}
}
