package dagql

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestCacheArbitraryLateCleanupBlocksClose(t *testing.T) {
	for _, failRelease := range []bool{false, true} {
		name := "success"
		if failRelease {
			name = "release error"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cache, err := NewCache(ctx, "", nil, nil)
				require.NoError(t, err)
				entered, allowReturn := make(chan struct{}), make(chan struct{})
				releasing, allowRelease := make(chan struct{}), make(chan struct{})
				finishInit := sync.OnceFunc(func() { close(allowReturn) })
				defer finishInit()
				finishRelease := sync.OnceFunc(func() { close(allowRelease) })
				defer finishRelease()
				var releases atomic.Int64
				releaseErr := errors.New("late arbitrary release failed")
				caller := make(chan error, 1)
				go func() {
					_, err := cache.GetOrInitArbitrary(ctx, "abandoned", "late", func(context.Context) (any, error) {
						close(entered)
						<-allowReturn
						return cacheTestOpaqueValue{value: "late", onRelease: func(ctx context.Context) error {
							require.NoError(t, ctx.Err())
							cache.callsMu.Lock()
							cache.callsMu.Unlock()
							releases.Add(1)
							close(releasing)
							<-allowRelease
							if failRelease {
								return releaseErr
							}
							return nil
						}}, nil
					})
					caller <- err
				}()
				<-entered
				cancel()
				require.ErrorIs(t, <-caller, context.Canceled)
				closing := make(chan struct{})
				cache.testAfterCacheClosing = func() { close(closing) }
				closed := make(chan error, 1)
				go func() { closed <- cache.Close(context.Background()) }()
				<-closing
				synctest.Wait()
				select {
				case err := <-closed:
					t.Fatalf("Close finished before initializer cleanup: %v", err)
				default:
				}
				finishInit()
				<-releasing
				synctest.Wait()
				select {
				case err := <-closed:
					t.Fatalf("Close finished while release was blocked: %v", err)
				default:
				}
				finishRelease()
				err = <-closed
				if failRelease {
					require.ErrorIs(t, err, releaseErr)
				} else {
					require.NoError(t, err)
				}
				require.EqualValues(t, 1, releases.Load())
			})
		})
	}
}

func TestCacheArbitraryCancellationKeepsOtherWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cache, err := NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		entered, proceed := make(chan struct{}), make(chan struct{})
		var releases atomic.Int64
		first := make(chan error, 1)
		go func() {
			_, err := cache.GetOrInitArbitrary(ctx, "first", "shared", func(context.Context) (any, error) {
				close(entered)
				<-proceed
				return cacheTestOpaqueValue{value: "shared", onRelease: func(context.Context) error { releases.Add(1); return nil }}, nil
			})
			first <- err
		}()
		<-entered
		type result struct {
			value ArbitraryCachedResult
			err   error
		}
		second := make(chan result, 1)
		go func() {
			value, err := cache.GetOrInitArbitrary(context.Background(), "second", "shared", ArbitraryValueFunc("unexpected"))
			second <- result{value, err}
		}()
		synctest.Wait()
		cache.callsMu.Lock()
		waiters := cache.ongoingArbitraryCalls["shared"].waiters
		cache.callsMu.Unlock()
		require.Equal(t, 2, waiters)
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		close(proceed)
		got := <-second
		require.NoError(t, got.err)
		require.Equal(t, "shared", got.value.Value().(cacheTestOpaqueValue).value)
		require.Zero(t, releases.Load())
		require.NoError(t, cache.ReleaseSession(context.Background(), "second"))
		require.EqualValues(t, 1, releases.Load())
		require.NoError(t, cache.Close(context.Background()))
	})
}

func TestCacheArbitraryLateCleanupKeepsReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cache, err := NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		entered, proceed := make(chan struct{}), make(chan struct{})
		var oldReleases, newReleases atomic.Int64
		first := make(chan error, 1)
		go func() {
			_, err := cache.GetOrInitArbitrary(ctx, "old", "same-key", func(context.Context) (any, error) {
				close(entered)
				<-proceed
				return cacheTestOpaqueValue{value: "old", onRelease: func(context.Context) error { oldReleases.Add(1); return nil }}, nil
			})
			first <- err
		}()
		<-entered
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		_, err = cache.GetOrInitArbitrary(context.Background(), "new", "same-key", ArbitraryValueFunc(cacheTestOpaqueValue{value: "new", onRelease: func(context.Context) error { newReleases.Add(1); return nil }}))
		require.NoError(t, err)
		close(proceed)
		synctest.Wait()
		require.EqualValues(t, 1, oldReleases.Load())
		require.Zero(t, newReleases.Load())
		hit, err := cache.GetOrInitArbitrary(context.Background(), "new", "same-key", ArbitraryValueFunc("unexpected"))
		require.NoError(t, err)
		require.True(t, hit.HitCache())
		require.Equal(t, "new", hit.Value().(cacheTestOpaqueValue).value)
		require.NoError(t, cache.ReleaseSession(context.Background(), "new"))
		require.EqualValues(t, 1, newReleases.Load())
		require.NoError(t, cache.Close(context.Background()))
	})
}

// This context pauses after waitArbitrary selected cancellation, before it
// takes callsMu. An initializer can publish success during that interval.
type arbitraryCancellationContext struct {
	context.Context
	done     chan struct{}
	selected chan struct{}
	proceed  chan struct{}
}

func (c *arbitraryCancellationContext) Done() <-chan struct{} { return c.done }
func (c *arbitraryCancellationContext) Err() error {
	close(c.selected)
	<-c.proceed
	return context.Canceled
}

func TestCacheArbitraryCanceledWaiterTakesPublishedCleanup(t *testing.T) {
	ctx := &arbitraryCancellationContext{Context: context.Background(), done: make(chan struct{}), selected: make(chan struct{}), proceed: make(chan struct{})}
	close(ctx.done)
	cache, err := NewCache(context.Background(), "", nil, nil)
	require.NoError(t, err)
	_, stop := context.WithCancelCause(context.Background())
	res := &sharedArbitraryResult{id: 1, callKey: "published", waitCh: make(chan struct{}), waiters: 1, cancel: stop}
	cache.ongoingArbitraryCalls = map[string]*sharedArbitraryResult{"published": res}
	caller := make(chan error, 1)
	go func() { _, err := cache.waitArbitrary(ctx, "canceled", res, true); caller <- err }()
	<-ctx.selected
	var releases atomic.Int64
	cache.callsMu.Lock()
	res.value = "published"
	res.onRelease = func(ctx context.Context) error {
		require.NoError(t, ctx.Err())
		cache.callsMu.Lock()
		cache.callsMu.Unlock()
		releases.Add(1)
		return nil
	}
	close(res.waitCh)
	cache.callsMu.Unlock()
	close(ctx.proceed)
	require.ErrorIs(t, <-caller, context.Canceled)
	require.EqualValues(t, 1, releases.Load())
	require.NoError(t, cache.Close(context.Background()))
}
