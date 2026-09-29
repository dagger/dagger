package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// Retire each producer before releasing its waiters, then let the next producer
// claim the shared row. The healthy joiner must cross successive foreign
// tombstones without resetting its per-invocation takeover budget.
func TestCacheLazySessionTakeoverBudget(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprintf("final_success=%t", success), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, c, producer, ready := newLazyRetryTestResult(t, nil)
				srv := cacheTestServer(t)
				shutdown := make(chan struct{})
				stop := sync.OnceFunc(func() { close(shutdown) })
				defer stop()
				type bodyEvent struct {
					session string
					allow   chan struct{}
				}
				type finishEvent struct {
					attempt *lazyEvalAttempt
					allow   chan struct{}
				}
				started := make(chan bodyEvent, 16)
				finished := make(chan finishEvent, 16)
				joined := make(chan *lazyEvalAttempt, 16)
				var calls, running, maxRunning atomic.Int32
				frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&cacheTestObject{}).Type()), Field: "session-budget"}
				result, err := c.GetOrInitCall(ctx, producer, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
					return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{Value: 1, lazyEval: func(cb context.Context) error {
						calls.Add(1)
						updateLazyRetryMax(&maxRunning, running.Add(1))
						defer running.Add(-1)
						md, err := engine.ClientMetadataFromContext(cb)
						if err != nil {
							return err
						}
						e := bodyEvent{md.SessionID, make(chan struct{})}
						started <- e
						select {
						case <-e.allow:
						case <-shutdown:
						}
						return c.Evaluate(cb, ready)
					}}), nil
				})
				require.NoError(t, err)
				c.testAfterLazyEvalJoin = func(a *lazyEvalAttempt) { joined <- a }
				c.testAfterLazyEvalFinish = func(a *lazyEvalAttempt) {
					e := finishEvent{a, make(chan struct{})}
					finished <- e
					select {
					case <-e.allow:
					case <-shutdown:
					}
				}
				sessions := []string{producer}
				var cancels []context.CancelFunc
				var completions []chan struct{}
				var producerErrors []chan error
				defer func() {
					stop()
					for _, cancel := range cancels {
						cancel()
					}
					for _, done := range completions {
						waitLazyRetrySignal(t, done, "budget goroutine cleanup")
					}
					synctest.Wait()
					clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					for _, id := range sessions {
						require.NoError(t, c.ReleaseSession(clean, id))
						require.NoError(t, c.WaitSessionRelease(clean, id))
						require.Zero(t, c.sessionLifecycle(id).lifecycle.Load()&^cacheSessionReleasedBit)
					}
					require.Zero(t, c.activeGlobalOperations.Load())
					require.Zero(t, c.Size())
				}()
				startProducer := func(id string) bodyEvent {
					own := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: id, ClientID: id})
					attached, err := c.AttachResult(own, id, srv, result)
					require.NoError(t, err)
					_, err = c.AttachResult(own, id, srv, ready)
					require.NoError(t, err)
					own, cancel := context.WithCancel(own)
					cancels = append(cancels, cancel)
					done, out := make(chan struct{}), make(chan error, 1)
					completions = append(completions, done)
					producerErrors = append(producerErrors, out)
					go func() { defer close(done); out <- c.Evaluate(own, attached) }()
					select {
					case e := <-started:
						require.Equal(t, id, e.session)
						return e
					case <-time.After(5 * time.Second):
						t.Fatal("producer did not start")
						return bodyEvent{}
					}
				}
				body := startProducer(producer)
				foreign := producer + "-healthy"
				sessions = append(sessions, foreign)
				own := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: foreign, ClientID: foreign})
				attached, err := c.AttachResult(own, foreign, srv, result)
				require.NoError(t, err)
				_, err = c.AttachResult(own, foreign, srv, ready)
				require.NoError(t, err)
				own, cancel := context.WithCancel(own)
				cancels = append(cancels, cancel)
				done, out := make(chan struct{}), make(chan error, 1)
				completions = append(completions, done)
				go func() { defer close(done); out <- c.Evaluate(own, attached) }()
				waitJoin := func() {
					select {
					case a := <-joined:
						current, waiters := currentLazyAttempt(result.cacheSharedResult())
						require.Same(t, current, a)
						require.Equal(t, 2, waiters)
					case <-time.After(5 * time.Second):
						t.Fatal("healthy caller did not join")
					}
				}
				waitJoin()
				var retired []*lazyEvalAttempt
				for round := 0; round <= maxLazySessionRetries; round++ {
					if round < maxLazySessionRetries || !success {
						require.NoError(t, c.ReleaseSession(ctx, body.session))
					}
					close(body.allow)
					var finish finishEvent
					select {
					case finish = <-finished:
					case <-time.After(5 * time.Second):
						t.Fatal("producer did not retire")
					}
					retired = append(retired, finish.attempt)
					if round < maxLazySessionRetries {
						next := fmt.Sprintf("%s-%d", producer, round+1)
						sessions = append(sessions, next)
						body = startProducer(next)
						close(finish.allow)
						waitJoin()
					} else {
						close(finish.allow)
					}
				}
				err = waitLazyRetryError(t, out, "bounded healthy caller")
				if success {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrLazySessionRetryExhausted)
					require.ErrorIs(t, err, ErrCacheSessionReleased)
					require.ErrorContains(t, err, "after 3 foreign-session retries")
				}
				for i, errCh := range producerErrors {
					err := waitLazyRetryError(t, errCh, "budget producer")
					if success && i == maxLazySessionRetries {
						require.NoError(t, err)
					} else {
						require.ErrorIs(t, err, ErrCacheSessionReleased)
					}
				}
				synctest.Wait()
				shared := result.cacheSharedResult()
				shared.lazyMu.Lock()
				waiters := make([]int, len(retired))
				for i, attempt := range retired {
					waiters[i] = attempt.waiters
				}
				current := shared.lazyWhole.attempt
				shared.lazyMu.Unlock()
				require.Len(t, retired, maxLazySessionRetries+1)
				for i, count := range waiters {
					require.Zero(t, count, "retired attempt %d", i)
				}
				require.Nil(t, current)
				require.EqualValues(t, maxLazySessionRetries+1, calls.Load())
				require.EqualValues(t, 1, maxRunning.Load())
			})
		})
	}
}

func TestCacheLazyCancellationDoesNotConsumeSessionBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var shared *sharedResult
		cause := errors.New("abandoned callback")
		ctx, c, session, result := newLazyRetryTestResult(t, func(cb context.Context) error {
			if calls.Add(1) > maxLazySessionRetries+1 {
				return nil
			}
			shared.lazyMu.Lock()
			cancel := shared.lazyWhole.attempt.cancel
			shared.lazyMu.Unlock()
			cancel(cause)
			return context.Cause(cb)
		})
		shared = result.cacheSharedResult()
		require.NoError(t, c.Evaluate(ctx, result))
		require.EqualValues(t, maxLazySessionRetries+2, calls.Load(), "existing cancellation retry is not capped by foreign-session budget")
		synctest.Wait()
		require.NoError(t, c.ReleaseSession(ctx, session))
		require.NoError(t, c.WaitSessionRelease(ctx, session))
		require.Zero(t, c.Size())
	})
}
