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

func lazySessionScope(t *testing.T, ctx context.Context, session string, acquired, released *atomic.Int32) context.Context {
	t.Helper()
	var root *engine.ClientLifecycleLease
	var clone func(engine.ClientLeaseKind, string) (*engine.ClientLifecycleLease, error)
	clone = func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
		if !root.Held() {
			return nil, errors.New("test client scope released")
		}
		acquired.Add(1)
		return engine.NewClientLifecycleLease(kind, owner, func() { released.Add(1) }, clone), nil
	}
	root = engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "test request", func() {}, clone)
	t.Cleanup(root.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: session, ClientID: session + "-client"}, root)
	require.NoError(t, err)
	scoped, err := engine.ContextWithClientScope(ctx, scope)
	require.NoError(t, err)
	return scoped
}

func TestCacheLazySessionRetryContracts(t *testing.T) {
	for _, mode := range []string{"parts", "task", "bookkeeping", "canceled joiner", "released joiner", "unrelated error", "stale callback", "producer error chain"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, c, producer, ready := newLazyRetryTestResult(t, nil)
				srv := cacheTestServer(t)
				foreign := producer + "-healthy"
				var acquired, released, calls, bodies, running, maxRunning atomic.Int32
				ctx = lazySessionScope(t, ctx, producer, &acquired, &released)
				foreignCtx := lazySessionScope(t, ctx, foreign, &acquired, &released)
				gate, started := make(chan struct{}), make(chan struct{})
				release := sync.OnceFunc(func() { close(gate) })
				signalStarted := sync.OnceFunc(func() { close(started) })
				defer release()
				extraCause := errors.New("producer extra cause")
				var originalProducerError error
				injected := fmt.Errorf("genuine dependency failure: %w", ErrCacheSessionReleased)
				callback := func(cb context.Context) error {
					n := calls.Add(1)
					updateLazyRetryMax(&maxRunning, running.Add(1))
					defer running.Add(-1)
					signalStarted()
					<-gate
					md, err := engine.ClientMetadataFromContext(cb)
					if err != nil {
						return err
					}
					expected := producer
					if n > 1 {
						expected = foreign
					}
					if md.SessionID != expected {
						return fmt.Errorf("callback session %s, expected %s", md.SessionID, expected)
					}
					if mode == "unrelated error" {
						return injected
					}
					if mode == "stale callback" {
						cb = ctx
					}
					err = c.Evaluate(cb, ready)
					if mode == "producer error chain" && n == 1 {
						originalProducerError = fmt.Errorf("producer callback: %w", errors.Join(err, extraCause))
						return originalProducerError
					}
					return err
				}
				var receiver AnyResult
				var group LazyGroupKey
				var evaluate func(context.Context, AnyResult) error
				if mode == "parts" {
					receiver = newPartsTestResult(t, c, ctx, &cacheTestPartsObject{Value: 1, resolveFn: partsTestDirectResolve, groupEval: map[LazyGroupKey]LazyEvalFunc{partsTestGroupOut: callback}})
					srv = cacheTestPartsServer(t)
					group = partsTestGroupOut
					evaluate = func(ctx context.Context, r AnyResult) error { return c.EvaluateParts(ctx, r, partsTestPartFS) }
				} else {
					frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&cacheTestObject{}).Type()), Field: "session-contract"}
					var err error
					receiver, err = c.GetOrInitCall(ctx, producer, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
						return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{Value: 1, lazyEval: callback}), nil
					})
					require.NoError(t, err)
					group = LazyGroupWhole
					evaluate = func(ctx context.Context, r AnyResult) error { return c.Evaluate(ctx, r) }
					if mode == "task" || mode == "bookkeeping" {
						group = "obtain:session-test"
						spec := LazyTaskSpec{Body: callback}
						if mode == "bookkeeping" {
							spec = LazyTaskSpec{Body: func(ctx context.Context) error {
								bodies.Add(1)
								PartTaskFromContext(ctx).installed.Store(&InstalledOutputs{})
								return nil
							}, AfterOwnerSync: callback}
						}
						evaluate = func(ctx context.Context, r AnyResult) error { return c.RunLazyTask(ctx, r, group, spec) }
					}
				}
				joined, err := c.AttachResult(foreignCtx, foreign, srv, receiver)
				require.NoError(t, err)
				_, err = c.AttachResult(foreignCtx, foreign, cacheTestServer(t), ready)
				require.NoError(t, err)
				leaderCtx, cancelLeader := context.WithCancel(ctx)
				joinCtx, cancelJoin := context.WithCancelCause(foreignCtx)
				leaderDone, joinDone := make(chan struct{}), make(chan struct{})
				leaderErr, joinErr := make(chan error, 1), make(chan error, 1)
				defer func() {
					release()
					cancelLeader()
					cancelJoin(context.Canceled)
					waitLazyRetrySignal(t, leaderDone, "contract leader cleanup")
					waitLazyRetrySignal(t, joinDone, "contract joiner cleanup")
					synctest.Wait()
					clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					for _, id := range []string{producer, foreign} {
						require.NoError(t, c.ReleaseSession(clean, id))
						require.NoError(t, c.WaitSessionRelease(clean, id))
						require.Zero(t, c.sessionLifecycle(id).lifecycle.Load()&^cacheSessionReleasedBit)
					}
					require.Zero(t, c.activeGlobalOperations.Load())
					require.Zero(t, c.Size())
					require.Positive(t, acquired.Load())
					require.Equal(t, acquired.Load(), released.Load())
				}()
				go func() { defer close(leaderDone); leaderErr <- evaluate(leaderCtx, receiver) }()
				go func() {
					defer close(joinDone)
					select {
					case <-started:
					case <-joinCtx.Done():
						joinErr <- context.Cause(joinCtx)
						return
					}
					joinErr <- evaluate(joinCtx, joined)
				}()
				waitLazyRetrySignal(t, started, "contract callback")
				synctest.Wait()
				shared := receiver.cacheSharedResult()
				shared.lazyMu.Lock()
				attempt := shared.lazyGroupStateLocked(group).attempt
				waiters := 0
				if attempt != nil {
					waiters = attempt.waiters
				}
				shared.lazyMu.Unlock()
				require.NotNil(t, attempt)
				require.Equal(t, 2, waiters)
				cancelCause := errors.New("own joiner canceled")
				switch mode {
				case "canceled joiner":
					cancelJoin(cancelCause)
					synctest.Wait()
				case "released joiner":
					require.NoError(t, c.ReleaseSession(foreignCtx, foreign))
				}
				if mode != "unrelated error" {
					require.NoError(t, c.ReleaseSession(ctx, producer))
				}
				release()
				a := waitLazyRetryError(t, leaderErr, "contract producer")
				b := waitLazyRetryError(t, joinErr, "contract joiner")
				require.ErrorIs(t, a, ErrCacheSessionReleased)
				if mode == "producer error chain" {
					require.ErrorIs(t, a, originalProducerError)
					require.ErrorIs(t, a, extraCause)
				}
				switch mode {
				case "canceled joiner":
					require.ErrorIs(t, b, cancelCause)
					require.EqualValues(t, 1, calls.Load())
				case "released joiner":
					require.ErrorIs(t, b, ErrCacheSessionReleased)
					require.ErrorContains(t, b, foreign)
					require.EqualValues(t, 1, calls.Load())
				case "unrelated error":
					require.ErrorIs(t, a, injected)
					require.ErrorIs(t, b, injected)
					require.EqualValues(t, 1, calls.Load())
				case "stale callback":
					require.ErrorIs(t, b, ErrCacheSessionReleased)
					require.NotErrorIs(t, b, ErrLazySessionRetryExhausted)
					require.EqualValues(t, 2, calls.Load(), "live successor's stale captured dependency is a genuine error, not another takeover")
				default:
					require.NoError(t, b)
					require.EqualValues(t, 2, calls.Load())
				}
				if mode == "bookkeeping" {
					require.EqualValues(t, 1, bodies.Load(), "successful body must not be replayed")
				}
				require.EqualValues(t, 1, maxRunning.Load())
				synctest.Wait()
				shared.lazyMu.Lock()
				remaining := attempt.waiters
				current := shared.lazyGroupStateLocked(group).attempt
				shared.lazyMu.Unlock()
				require.Zero(t, remaining)
				require.Nil(t, current)
			})
		})
	}
}
