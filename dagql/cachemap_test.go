package dagql

import (
	"context"
	"sync"
	"testing"

	"github.com/pkg/errors"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestCacheMapConcurrent(t *testing.T) {
	t.Parallel()
	c := newCacheMap[int, int]()
	ctx := context.Background()

	commonKey := 42
	initialized := map[int]bool{}

	wg := new(sync.WaitGroup)
	for i := 0; i < 100; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, _, err := c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
				initialized[i] = true
				return i, nil
			})
			assert.NilError(t, err)
			assert.Assert(t, initialized[val])
		}()
	}

	wg.Wait()

	// only one of them should have initialized
	assert.Assert(t, is.Len(initialized, 1))
}

func TestCacheMapErrors(t *testing.T) {
	t.Parallel()
	c := newCacheMap[int, int]()
	ctx := context.Background()

	commonKey := 42

	myErr := errors.New("nope")
	_, _, err := c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 0, myErr
	})
	assert.Assert(t, is.ErrorIs(err, myErr))

	otherErr := errors.New("nope 2")
	_, _, err = c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 0, otherErr
	})
	assert.Assert(t, is.ErrorIs(err, otherErr))

	res, cached, err := c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 1, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !cached)
	assert.Equal(t, 1, res)

	res, cached, err = c.GetOrInitialize(ctx, commonKey, func(_ context.Context) (int, error) {
		return 0, errors.New("ignored")
	})
	assert.NilError(t, err)
	assert.Assert(t, cached)
	assert.Equal(t, 1, res)
}

func TestCacheMapRecursiveCall(t *testing.T) {
	t.Parallel()
	c := newCacheMap[int, int]()
	ctx := context.Background()

	// recursive calls that are guaranteed to result in deadlock should error out
	_, _, err := c.GetOrInitialize(ctx, 1, func(ctx context.Context) (int, error) {
		res, _, err := c.GetOrInitialize(ctx, 1, func(ctx context.Context) (int, error) {
			return 2, nil
		})
		return res, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheMapRecursiveCall))

	// verify same cachemap can be called recursively w/ different keys
	v, _, err := c.GetOrInitialize(ctx, 10, func(ctx context.Context) (int, error) {
		res, _, err := c.GetOrInitialize(ctx, 11, func(ctx context.Context) (int, error) {
			return 12, nil
		})
		return res, err
	})
	assert.NilError(t, err)
	assert.Equal(t, 12, v)

	// verify other cachemaps can be called w/ same keys
	c2 := newCacheMap[int, int]()
	v, cached, err := c.GetOrInitialize(ctx, 100, func(ctx context.Context) (int, error) {
		res, _, err := c2.GetOrInitialize(ctx, 100, func(ctx context.Context) (int, error) {
			return 101, nil
		})
		return res, err
	})
	assert.NilError(t, err)
	assert.Equal(t, 101, v)
	assert.Assert(t, !cached)
}
