package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestCacheConcurrent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c, err := NewCache[string, int](ctx, "")
	assert.NilError(t, err)

	commonKey := "42"
	initialized := map[int]bool{}

	wg := new(sync.WaitGroup)
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: commonKey, ConcurrencyKey: commonKey}, func(_ context.Context) (int, error) {
				initialized[i] = true
				return i, nil
			})
			assert.NilError(t, err)
			assert.Assert(t, initialized[res.Result()])
		}()
	}

	wg.Wait()

	// only one of them should have initialized
	assert.Assert(t, is.Len(initialized, 1))
}

func TestCacheErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c, err := NewCache[string, int](ctx, "")
	assert.NilError(t, err)

	commonKey := "42"

	myErr := errors.New("nope")
	_, err = c.GetOrInitialize(ctx, CacheKey[string]{CallKey: commonKey}, func(_ context.Context) (int, error) {
		return 0, myErr
	})
	assert.Assert(t, is.ErrorIs(err, myErr))

	otherErr := errors.New("nope 2")
	_, err = c.GetOrInitialize(ctx, CacheKey[string]{CallKey: commonKey}, func(_ context.Context) (int, error) {
		return 0, otherErr
	})
	assert.Assert(t, is.ErrorIs(err, otherErr))

	res, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: commonKey}, func(_ context.Context) (int, error) {
		return 1, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, res.Result())

	res, err = c.GetOrInitialize(ctx, CacheKey[string]{CallKey: commonKey}, func(_ context.Context) (int, error) {
		return 0, errors.New("ignored")
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, res.Result())
}

func TestCacheRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	c, err := NewCache[string, int](ctx, "")
	assert.NilError(t, err)

	// recursive calls that are guaranteed to result in deadlock should error out
	_, err = c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "1"}, func(ctx context.Context) (int, error) {
		_, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "1"}, func(ctx context.Context) (int, error) {
			return 2, nil
		})
		return 0, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))

	// verify same cachemap can be called recursively w/ different keys
	v, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "10"}, func(ctx context.Context) (int, error) {
		res, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "11"}, func(ctx context.Context) (int, error) {
			return 12, nil
		})
		return res.Result(), err
	})
	assert.NilError(t, err)
	assert.Equal(t, 12, v.Result())

	// verify other cachemaps can be called w/ same keys
	c2, err := NewCache[string, int](ctx, "")
	assert.NilError(t, err)
	v, err = c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "100"}, func(ctx context.Context) (int, error) {
		res, err := c2.GetOrInitialize(ctx, CacheKey[string]{CallKey: "100"}, func(ctx context.Context) (int, error) {
			return 101, nil
		})
		return res.Result(), err
	})
	assert.NilError(t, err)
	assert.Equal(t, 101, v.Result())
}

func TestCacheContextCancel(t *testing.T) {
	t.Run("cancels after all are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		c, err := NewCache[string, int](ctx, "")
		assert.NilError(t, err)

		ctx1, cancel1 := context.WithCancel(ctx)
		ctx2, cancel2 := context.WithCancel(ctx)
		ctx3, cancel3 := context.WithCancel(ctx)

		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		go func() {
			defer close(errCh1)
			_, err := c.GetOrInitialize(ctx1, CacheKey[string]{CallKey: "1", ConcurrencyKey: "1"}, func(ctx context.Context) (int, error) {
				close(started1)
				<-ctx.Done()
				return 0, fmt.Errorf("oh no 1")
			})
			errCh1 <- err
		}()
		select {
		case <-started1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for started1")
		}

		errCh2 := make(chan error, 1)
		go func() {
			defer close(errCh2)
			_, err := c.GetOrInitialize(ctx2, CacheKey[string]{CallKey: "1", ConcurrencyKey: "1"}, func(ctx context.Context) (int, error) {
				<-ctx.Done()
				return 1, fmt.Errorf("oh no 2")
			})
			errCh2 <- err
		}()

		errCh3 := make(chan error, 1)
		go func() {
			defer close(errCh3)
			_, err := c.GetOrInitialize(ctx3, CacheKey[string]{CallKey: "1", ConcurrencyKey: "1"}, func(ctx context.Context) (int, error) {
				return 2, fmt.Errorf("oh no 3")
			})
			errCh3 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			is.ErrorIs(err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}
		select {
		case err := <-errCh1:
			t.Fatal("unexpected error from 1st client", err)
		case err := <-errCh3:
			t.Fatal("unexpected error from 3rd client", err)
		default:
		}

		cancel3()
		select {
		case err := <-errCh3:
			is.ErrorIs(err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh3")
		}
		select {
		case err := <-errCh1:
			t.Fatal("unexpected error from 1st client", err)
		default:
		}

		cancel1()
		select {
		case err := <-errCh1:
			is.ErrorIs(err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})

	t.Run("succeeds if others are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		c, err := NewCache[string, int](ctx, "")
		assert.NilError(t, err)

		ctx1, cancel1 := context.WithCancel(ctx)
		t.Cleanup(cancel1)
		ctx2, cancel2 := context.WithCancel(ctx)

		resCh1 := make(chan Result[string, int], 1)
		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		stop1 := make(chan struct{})
		go func() {
			defer close(resCh1)
			defer close(errCh1)
			res, err := c.GetOrInitialize(ctx1, CacheKey[string]{CallKey: "1"}, func(ctx context.Context) (int, error) {
				close(started1)
				<-stop1
				return 0, nil
			})
			resCh1 <- res
			errCh1 <- err
		}()
		select {
		case <-started1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for started1")
		}

		errCh2 := make(chan error, 1)
		go func() {
			defer close(errCh2)
			_, err := c.GetOrInitialize(ctx2, CacheKey[string]{CallKey: "1"}, func(ctx context.Context) (int, error) {
				<-ctx.Done()
				return 1, fmt.Errorf("oh no")
			})
			errCh2 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			is.ErrorIs(err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}

		close(stop1)
		select {
		case res := <-resCh1:
			assert.Equal(t, 0, res.Result())
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for resCh1")
		}
		select {
		case err := <-errCh1:
			assert.NilError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})
}

func TestCacheResultRelease(t *testing.T) {
	t.Parallel()
	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache[string, int](ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache[string, int])
		assert.Assert(t, ok)

		res1A, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "1"}, func(_ context.Context) (int, error) {
			return 1, nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "1"}, func(_ context.Context) (int, error) {
			return 1, nil
		})
		assert.NilError(t, err)

		res2, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "2"}, func(_ context.Context) (int, error) {
			return 2, nil
		})
		assert.NilError(t, err)

		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 2, len(c.completedCalls))

		err = res2.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.completedCalls))

		err = res1A.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.completedCalls))

		err = res1B.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 0, len(c.completedCalls))
	})

	t.Run("onRelease", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache[string, int](ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache[string, int])
		assert.Assert(t, ok)

		releaseCalledCh := make(chan struct{})
		res1A, err := c.GetOrInitializeWithCallbacks(ctx, CacheKey[string]{CallKey: "1", ConcurrencyKey: "1"}, func(_ context.Context) (*ValueWithCallbacks[int], error) {
			return &ValueWithCallbacks[int]{Value: 1, OnRelease: func(ctx context.Context) error {
				close(releaseCalledCh)
				return nil
			}}, nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitialize(ctx, CacheKey[string]{CallKey: "1"}, func(_ context.Context) (int, error) {
			return 1, nil
		})
		assert.NilError(t, err)

		err = res1A.Release(ctx)
		assert.NilError(t, err)
		select {
		case <-releaseCalledCh:
			// shouldn't be called until every result is released
			t.Fatal("unexpected release call")
		default:
		}

		err = res1B.Release(ctx)
		assert.NilError(t, err)
		select {
		case <-releaseCalledCh:
			// it was called now that every result is released
		default:
			t.Fatal("expected release call")
		}

		// test error in onRelease
		res2, err := c.GetOrInitializeWithCallbacks(ctx, CacheKey[string]{CallKey: "2", ConcurrencyKey: "1"}, func(_ context.Context) (*ValueWithCallbacks[int], error) {
			return &ValueWithCallbacks[int]{Value: 2, OnRelease: func(ctx context.Context) error {
				return fmt.Errorf("oh no")
			}}, nil
		})
		assert.NilError(t, err)

		err = res2.Release(ctx)
		assert.ErrorContains(t, err, "oh no")
	})
}

func TestSkipDedupe(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c, err := NewCache[string, int](ctx, "")
	assert.NilError(t, err)

	var eg errgroup.Group

	valCh1 := make(chan int, 1)
	started1 := make(chan struct{})
	stop1 := make(chan struct{})
	eg.Go(func() error {
		_, err := c.GetOrInitializeWithCallbacks(ctx, CacheKey[string]{CallKey: "1"}, func(_ context.Context) (*ValueWithCallbacks[int], error) {
			defer close(valCh1)
			close(started1)
			valCh1 <- 1
			<-stop1
			return &ValueWithCallbacks[int]{Value: 1}, nil
		})
		if err != nil {
			return err
		}
		return nil
	})

	valCh2 := make(chan int, 1)
	started2 := make(chan struct{})
	stop2 := make(chan struct{})
	eg.Go(func() error {
		_, err := c.GetOrInitializeWithCallbacks(ctx, CacheKey[string]{CallKey: "1"}, func(_ context.Context) (*ValueWithCallbacks[int], error) {
			defer close(valCh2)
			close(started2)
			valCh2 <- 2
			<-stop2
			return &ValueWithCallbacks[int]{Value: 2}, nil
		})
		if err != nil {
			return err
		}
		return nil
	})

	select {
	case <-started1:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for started1")
	}
	select {
	case <-started2:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for started2")
	}

	close(stop1)
	close(stop2)

	select {
	case val := <-valCh1:
		assert.Equal(t, 1, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for resCh1")
	}
	select {
	case val := <-valCh2:
		assert.Equal(t, 2, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for resCh2")
	}
}
