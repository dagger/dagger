package dagql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/cache"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestSessionCacheReleaseAndClose(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		ctx := t.Context()

		c, err := cache.NewCache[string, AnyResult](ctx, "")
		require.NoError(t, err)
		sc1 := NewSessionCache(c)
		sc2 := NewSessionCache(c)

		_, err = sc1.GetOrInitializeValue(ctx, cache.CacheKey[string]{CallKey: "1"}, nil)
		require.NoError(t, err)

		_, err = sc1.GetOrInitializeValue(ctx, cache.CacheKey[string]{CallKey: "2"}, nil)
		require.NoError(t, err)

		require.Equal(t, 2, c.Size())

		_, err = sc2.GetOrInitializeValue(ctx, cache.CacheKey[string]{CallKey: "2"}, nil)
		require.NoError(t, err)

		_, err = sc2.GetOrInitializeValue(ctx, cache.CacheKey[string]{CallKey: "3"}, nil)
		require.NoError(t, err)

		require.Equal(t, 3, c.Size())

		err = sc1.ReleaseAndClose(ctx)
		require.NoError(t, err)

		require.Equal(t, 2, c.Size())

		_, err = sc1.GetOrInitializeValue(ctx, cache.CacheKey[string]{CallKey: "x"}, nil)
		require.Error(t, err)

		require.Equal(t, 2, c.Size())

		err = sc2.ReleaseAndClose(ctx)
		require.NoError(t, err)

		require.Equal(t, 0, c.Size())
	})

	t.Run("close while running", func(t *testing.T) {
		ctx := t.Context()

		c, err := cache.NewCache[string, AnyResult](ctx, "")
		require.NoError(t, err)
		sc := NewSessionCache(c)

		_, err = sc.GetOrInitializeValue(ctx, cache.CacheKey[string]{CallKey: "1"}, nil)
		require.NoError(t, err)
		require.Equal(t, 1, c.Size())

		var eg errgroup.Group
		startCh := make(chan struct{})
		stopCh := make(chan struct{})
		eg.Go(func() error {
			_, err := sc.GetOrInitialize(ctx, cache.CacheKey[string]{CallKey: "2"}, func(ctx context.Context) (AnyResult, error) {
				close(startCh)
				<-stopCh
				return nil, nil
			})
			return err
		})

		select {
		case <-startCh:
		case <-time.After(10 * time.Second): // just don't block forever if there's a bug
			t.Fatal("timeout waiting for goroutine to start")
			return
		}

		err = sc.ReleaseAndClose(ctx)
		require.NoError(t, err)
		close(stopCh)

		err = eg.Wait()
		require.Error(t, err, "expected error when closing session cache while a call is running")

		require.Equal(t, 0, c.Size())
	})
}

func TestSessionCacheErrorThenSuccessIsCached(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	base, err := cache.NewCache[CacheKeyType, AnyResult](ctx, "")
	require.NoError(t, err)

	sc := NewSessionCache(base)
	key := cache.CacheKey[CacheKeyType]{CallKey: "session-cache-error-then-success"}

	// We simulate this sequence for a single CallKey:
	//  1) error
	//  2) error
	//  3) success
	//  4) (must be served from cache, NOT call the underlying fn)
	type step struct {
		err error
	}
	steps := []step{
		{err: fmt.Errorf("boom 1")},
		{err: fmt.Errorf("boom 2")},
		{err: nil}, // first success
		{err: fmt.Errorf("should never be used if caching works")},
	}

	callCount := 0
	fn := func(ctx context.Context) (*CacheValWithCallbacks, error) {
		require.Less(t, callCount, len(steps), "underlying fn called too many times")
		s := steps[callCount]
		callCount++
		if s.err != nil {
			return nil, s.err
		}
		// Successful call: we don't care about the actual value for this test,
		// only that the SessionCache / engine cache will reuse it.
		return &CacheValWithCallbacks{Value: nil}, nil
	}

	// 1) First call: error, underlying fn must run once.
	_, err = sc.GetOrInitializeWithCallbacks(ctx, key, fn)
	require.Error(t, err)
	require.Equal(t, 1, callCount)

	// 2) Second call: another error, underlying fn must run again.
	_, err = sc.GetOrInitializeWithCallbacks(ctx, key, fn)
	require.Error(t, err)
	require.Equal(t, 2, callCount)

	// 3) Third call: first success, underlying fn must run a third time.
	_, err = sc.GetOrInitializeWithCallbacks(ctx, key, fn)
	require.NoError(t, err)
	require.Equal(t, 3, callCount)

	// 4) Fourth call: MUST hit cache, not run fn again.
	// If caching is broken, this call will increment
	// callCount to 4 and return an error.
	_, err = sc.GetOrInitializeWithCallbacks(ctx, key, fn)
	require.NoError(t, err, "after a successful call for a key, subsequent calls must reuse the cached success")
	require.Equal(t, 3, callCount, "underlying fn should not be called again after success is cached")
}
