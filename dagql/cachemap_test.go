package dagql

import (
	"context"
	"sync"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestCacheMapConcurrent(t *testing.T) {
	t.Parallel()
	c := NewCacheMap[int, int]()
	ctx := context.Background()

	commonKey := 42
	initialized := map[int]bool{}

	wg := new(sync.WaitGroup)
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, err := c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
				initialized[i] = true
				return i, nil
			})
			require.NoError(t, err)
			require.True(t, initialized[val])
		}()
	}

	wg.Wait()

	// only one of them should have initialized
	require.Len(t, initialized, 1)
}

func TestCacheMapErrors(t *testing.T) {
	t.Parallel()
	c := NewCacheMap[int, int]()
	ctx := context.Background()

	commonKey := 42

	myErr := errors.New("nope")
	_, err := c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 0, myErr
	})
	require.Equal(t, myErr, err)

	otherErr := errors.New("nope 2")
	_, err = c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 0, otherErr
	})
	require.Equal(t, otherErr, err)

	res, err := c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, res)

	res, err = c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 0, errors.New("ignored")
	})
	require.NoError(t, err)
	require.Equal(t, 1, res)
}

func TestCacheMapRecursiveCall(t *testing.T) {
	t.Parallel()
	c := NewCacheMap[int, int]()
	ctx := context.Background()

	// recursive calls that are guaranteed to result in deadlock should error out
	_, err := c.GetOrInitialize(ctx, 1, func(ctx context.Context) (int, error) {
		return c.GetOrInitialize(ctx, 1, func(ctx context.Context) (int, error) {
			return 2, nil
		})
	})
	require.ErrorIs(t, err, ErrCacheMapRecursiveCall)

	// verify same cachemap can be called recursively w/ different keys
	v, err := c.GetOrInitialize(ctx, 10, func(ctx context.Context) (int, error) {
		return c.GetOrInitialize(ctx, 11, func(ctx context.Context) (int, error) {
			return 12, nil
		})
	})
	require.NoError(t, err)
	require.Equal(t, 12, v)

	// verify other cachemaps can be called w/ same keys
	c2 := NewCacheMap[int, int]()
	v, err = c.GetOrInitialize(ctx, 100, func(ctx context.Context) (int, error) {
		return c2.GetOrInitialize(ctx, 100, func(ctx context.Context) (int, error) {
			return 101, nil
		})
	})
	require.NoError(t, err)
	require.Equal(t, 101, v)
}
