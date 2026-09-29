package dagql

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// A shared callback keeps its producer's metadata even when another session
// joins it. Releasing that producer must not fail the other live session.
func TestCacheEvaluateRetriesReleasedProducerSession(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "admission"
		if late {
			name = "completion"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gate := make(chan struct{})
				release := sync.OnceFunc(func() { close(gate) })
				defer release()
				started := make(chan struct{})
				var calls, childCalls atomic.Int32
				var cache *Cache
				var child AnyResult
				ctx, c, producer, result := newLazyRetryTestResult(t, func(ctx context.Context) error {
					calls.Add(1)
					if !late && calls.Load() == 1 {
						close(started)
						<-gate
					}
					return cache.Evaluate(ctx, child)
				})
				cache = c
				srv := cacheTestServer(t)
				frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&cacheTestObject{}).Type()), Field: "lazy-session-child"}
				var err error
				child, err = c.GetOrInitCall(ctx, producer, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
					return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{Value: 2, lazyEval: func(context.Context) error {
						childCalls.Add(1)
						if late {
							close(started)
							<-gate
						}
						return nil
					}}), nil
				})
				require.NoError(t, err)
				foreign := producer + "-healthy"
				foreignCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{SessionID: foreign, ClientID: "healthy"})
				joined, err := c.AttachResult(foreignCtx, foreign, srv, result)
				require.NoError(t, err)
				_, err = c.AttachResult(foreignCtx, foreign, srv, child)
				require.NoError(t, err)
				producerCtx, cancelProducer := context.WithCancel(ctx)
				joinerCtx, cancelJoiner := context.WithCancel(foreignCtx)
				producerDone, joinerDone := make(chan struct{}), make(chan struct{})
				producerError, joinerError := make(chan error, 1), make(chan error, 1)
				defer func() {
					release()
					cancelProducer()
					cancelJoiner()
					waitLazyRetrySignal(t, producerDone, "producer return during cleanup")
					waitLazyRetrySignal(t, joinerDone, "joiner return during cleanup")
					synctest.Wait()
					cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					require.NoError(t, c.ReleaseSession(cleanupCtx, producer))
					require.NoError(t, c.ReleaseSession(cleanupCtx, foreign))
					require.NoError(t, c.WaitSessionRelease(cleanupCtx, producer))
					require.NoError(t, c.WaitSessionRelease(cleanupCtx, foreign))
					require.Zero(t, c.Size())
				}()
				go func() { defer close(producerDone); producerError <- c.Evaluate(producerCtx, result) }()
				// Start the joiner after the producer owns the shared callback.
				go func() {
					defer close(joinerDone)
					select {
					case <-started:
					case <-joinerCtx.Done():
						joinerError <- joinerCtx.Err()
						return
					}
					joinerError <- c.Evaluate(joinerCtx, joined)
				}()
				waitLazyRetrySignal(t, started, "producer callback")
				synctest.Wait()
				_, waiters := currentLazyAttempt(result.cacheSharedResult())
				require.Equal(t, 2, waiters)
				require.NoError(t, c.ReleaseSession(ctx, producer))
				release()
				producerErr := waitLazyRetryError(t, producerError, "released producer")
				joinerErr := waitLazyRetryError(t, joinerError, "healthy foreign joiner")
				require.ErrorIs(t, producerErr, ErrCacheSessionReleased)
				require.NoError(t, joinerErr, "live caller must not inherit the released producer's session error")
				require.Equal(t, int32(2), calls.Load())
				require.Equal(t, int32(1), childCalls.Load(), "already completed dependency must not execute twice")
				require.False(t, errors.Is(joinerErr, ErrCacheSessionReleased))
			})
		})
	}
}
