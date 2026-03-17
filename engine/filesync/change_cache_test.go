package filesync

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func mkChange(kind ChangeKind) *ChangeWithStat {
	return &ChangeWithStat{kind: kind}
}

func TestChangeCacheConcurrent(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c := newChangeCache()

	var initCount atomic.Int32
	initStarted := make(chan struct{})
	initDone := make(chan struct{})

	results := make([]*cachedChange, 0, 100)
	var resultsMu sync.Mutex

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.getOrInit(ctx, "42", func(_ context.Context) (*ChangeWithStat, error) {
				if initCount.Add(1) == 1 {
					close(initStarted)
				}
				<-initDone
				return mkChange(ChangeKindModify), nil
			})
			assert.NilError(t, err)
			assert.Equal(t, ChangeKindModify, res.result().kind)

			resultsMu.Lock()
			results = append(results, res)
			resultsMu.Unlock()
		}()
	}

	select {
	case <-initStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initializer")
	}
	close(initDone)

	wg.Wait()
	assert.Equal(t, int32(1), initCount.Load())

	for _, res := range results {
		res.release()
	}
}

func TestChangeCacheErrors(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c := newChangeCache()

	err1 := errors.New("nope 1")
	_, err := c.getOrInit(ctx, "42", func(_ context.Context) (*ChangeWithStat, error) {
		return nil, err1
	})
	assert.Assert(t, is.ErrorIs(err, err1))

	err2 := errors.New("nope 2")
	_, err = c.getOrInit(ctx, "42", func(_ context.Context) (*ChangeWithStat, error) {
		return nil, err2
	})
	assert.Assert(t, is.ErrorIs(err, err2))

	res1, err := c.getOrInit(ctx, "42", func(_ context.Context) (*ChangeWithStat, error) {
		return mkChange(ChangeKindAdd), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, ChangeKindAdd, res1.result().kind)

	var called atomic.Int32
	res2, err := c.getOrInit(ctx, "42", func(_ context.Context) (*ChangeWithStat, error) {
		called.Add(1)
		return mkChange(ChangeKindDelete), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, ChangeKindAdd, res2.result().kind)
	assert.Equal(t, int32(0), called.Load())

	res1.release()
	res2.release()
}

func TestChangeCacheContextCancel(t *testing.T) {
	t.Parallel()

	t.Run("waiter cancel does not cancel in-flight call", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		c := newChangeCache()

		initStarted := make(chan struct{})
		unblockInit := make(chan struct{})

		resCh1 := make(chan *cachedChange, 1)
		errCh1 := make(chan error, 1)
		go func() {
			res, err := c.getOrInit(ctx, "1", func(context.Context) (*ChangeWithStat, error) {
				close(initStarted)
				<-unblockInit
				return mkChange(ChangeKindModify), nil
			})
			resCh1 <- res
			errCh1 <- err
		}()

		select {
		case <-initStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for initializer")
		}

		ctx2, cancel2 := context.WithCancel(ctx)
		errCh2 := make(chan error, 1)
		go func() {
			_, err := c.getOrInit(ctx2, "1", func(context.Context) (*ChangeWithStat, error) {
				return mkChange(ChangeKindAdd), nil
			})
			errCh2 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for canceled waiter")
		}

		close(unblockInit)

		var res1 *cachedChange
		select {
		case res1 = <-resCh1:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for primary result")
		}
		select {
		case err := <-errCh1:
			assert.NilError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for primary error")
		}
		assert.Equal(t, ChangeKindModify, res1.result().kind)

		var called atomic.Int32
		res3, err := c.getOrInit(ctx, "1", func(context.Context) (*ChangeWithStat, error) {
			called.Add(1)
			return mkChange(ChangeKindNone), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, ChangeKindModify, res3.result().kind)
		assert.Equal(t, int32(0), called.Load())

		res1.release()
		res3.release()
	})

	t.Run("last waiter cancel cancels call and does not cache error", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		c := newChangeCache()

		ctx1, cancel1 := context.WithCancel(ctx)
		started := make(chan struct{})
		errCh := make(chan error, 1)
		go func() {
			_, err := c.getOrInit(ctx1, "1", func(ctx context.Context) (*ChangeWithStat, error) {
				close(started)
				<-ctx.Done()
				return nil, context.Cause(ctx)
			})
			errCh <- err
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for initializer")
		}

		cancel1()
		select {
		case err := <-errCh:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for canceled call")
		}

		res, err := c.getOrInit(ctx, "1", func(context.Context) (*ChangeWithStat, error) {
			return mkChange(ChangeKindAdd), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, ChangeKindAdd, res.result().kind)
		res.release()
	})
}

func TestChangeCacheRelease(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c := newChangeCache()

	size := func() int {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.ongoingCalls) + len(c.completedCalls)
	}

	res1A, err := c.getOrInit(ctx, "1", func(context.Context) (*ChangeWithStat, error) {
		return mkChange(ChangeKindAdd), nil
	})
	assert.NilError(t, err)
	res1B, err := c.getOrInit(ctx, "1", func(context.Context) (*ChangeWithStat, error) {
		return mkChange(ChangeKindAdd), nil
	})
	assert.NilError(t, err)
	res2, err := c.getOrInit(ctx, "2", func(context.Context) (*ChangeWithStat, error) {
		return mkChange(ChangeKindModify), nil
	})
	assert.NilError(t, err)

	assert.Equal(t, 2, size())

	res2.release()
	assert.Equal(t, 1, size())

	res1A.release()
	assert.Equal(t, 1, size())

	res1B.release()
	assert.Equal(t, 0, size())
}

func TestChangeCacheEmptyKey(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c := newChangeCache()

	_, err := c.getOrInit(ctx, "", func(context.Context) (*ChangeWithStat, error) {
		return nil, nil
	})
	assert.ErrorContains(t, err, "cache call key is empty")
}
