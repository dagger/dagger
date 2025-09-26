package dagql

import (
	"context"
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
