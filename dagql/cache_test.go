package dagql

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"

	"github.com/dagger/dagger/dagql/call"
)

func cacheTestID(key string) *call.ID {
	return call.New().Append(Int(0).Type(), key)
}

func cacheTestIntResult(id *call.ID, v int) AnyResult {
	return newDetachedResult(id, NewInt(v))
}

type cacheTestOnReleaseInt struct {
	Int
	onRelease func(context.Context) error
}

func (v cacheTestOnReleaseInt) OnRelease(ctx context.Context) error {
	if v.onRelease == nil {
		return nil
	}
	return v.onRelease(ctx)
}

func cacheTestIntResultWithOnRelease(id *call.ID, v int, onRelease func(context.Context) error) AnyResult {
	return newDetachedResult(id, cacheTestOnReleaseInt{
		Int:       NewInt(v),
		onRelease: onRelease,
	})
}

type cacheTestOpaqueValue struct {
	value     string
	onRelease func(context.Context) error
}

func (v cacheTestOpaqueValue) OnRelease(ctx context.Context) error {
	if v.onRelease == nil {
		return nil
	}
	return v.onRelease(ctx)
}

func cacheTestUnwrapInt(t *testing.T, res AnyResult) int {
	t.Helper()
	v, ok := UnwrapAs[Int](res)
	assert.Assert(t, ok, "expected Int result, got %T", res)
	return int(v)
}

type cacheTestQuery struct{}

func (cacheTestQuery) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

type cacheTestObject struct {
	Value     int
	onRelease func(context.Context) error
}

func (*cacheTestObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CacheTestObject",
		NonNull:   true,
	}
}

func (obj *cacheTestObject) OnRelease(ctx context.Context) error {
	if obj.onRelease == nil {
		return nil
	}
	return obj.onRelease(ctx)
}

func cacheTestServer(t *testing.T, base Cache) *Server {
	t.Helper()
	srv := NewServer(cacheTestQuery{}, NewSessionCache(base))
	Fields[*cacheTestObject]{
		Func("value", func(_ context.Context, self *cacheTestObject, _ struct{}) (Int, error) {
			return NewInt(self.Value), nil
		}),
	}.Install(srv)
	return srv
}

func cacheTestObjectResult(
	t *testing.T,
	srv *Server,
	id *call.ID,
	value int,
	onRelease func(context.Context) error,
) ObjectResult[*cacheTestObject] {
	t.Helper()
	res, err := NewObjectResultForID(&cacheTestObject{
		Value:     value,
		onRelease: onRelease,
	}, srv, id)
	assert.NilError(t, err)
	return res
}

func TestCacheConcurrent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("42")
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100
	const concurrencyKey = "42"

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	callConcKeys := callConcurrencyKeys{
		callKey:        keyID.Digest().String(),
		concurrencyKey: concurrencyKey,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := cacheIface.GetOrInitCall(ctx, CacheKey{
			ID:             keyID,
			ConcurrencyKey: concurrencyKey,
		}, func(_ context.Context) (AnyResult, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return cacheTestIntResult(keyID, 0), nil
		})
		assert.NilError(t, err)
		assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
	}()

	select {
	case <-firstCallEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first caller to enter init callback")
	}

	for i := 1; i < totalCallers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := cacheIface.GetOrInitCall(ctx, CacheKey{
				ID:             keyID,
				ConcurrencyKey: concurrencyKey,
			}, func(_ context.Context) (AnyResult, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return cacheTestIntResult(keyID, i), nil
			})
			assert.NilError(t, err)
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.mu.Lock()
		oc := c.ongoingCalls[callConcKeys]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.mu.Unlock()

		if oc != nil && lastObservedWaiters == totalCallers {
			waiterCountReached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, waiterCountReached, "expected %d waiters, last observed %d", totalCallers, lastObservedWaiters)

	close(unblockFirstCall)

	ongoingCleared := false
	clearPollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(clearPollDeadline) {
		c.mu.Lock()
		_, exists := c.ongoingCalls[callConcKeys]
		c.mu.Unlock()
		if !exists {
			ongoingCleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, ongoingCleared, "ongoing call was not cleared")

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
	assert.Assert(t, initialized[0])
	assert.Equal(t, 1, cacheIface.Size())
}

func TestCacheErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyID := cacheTestID("42")

	myErr := errors.New("nope")
	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return nil, myErr
	})
	assert.Assert(t, is.ErrorIs(err, myErr))

	otherErr := errors.New("nope 2")
	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return nil, otherErr
	})
	assert.Assert(t, is.ErrorIs(err, otherErr))

	res, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return cacheTestIntResult(keyID, 1), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))

	res, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(_ context.Context) (AnyResult, error) {
		return nil, errors.New("ignored")
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res))
}

func TestCacheRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	key1 := cacheTestID("1")

	// recursive calls that are guaranteed to result in deadlock should error out
	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: key1}, func(ctx context.Context) (AnyResult, error) {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(cacheTestID("2"), 2), nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))

	// verify same cache can be called recursively with different keys
	key10 := cacheTestID("10")
	key11 := cacheTestID("11")
	v, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: key10}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: key11}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key11, 12), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key10, cacheTestUnwrapInt(t, res)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 12, cacheTestUnwrapInt(t, v))

	// verify other cache instances can be called with same keys
	cacheIface2, err := NewCache(ctx, "")
	assert.NilError(t, err)
	key100 := cacheTestID("100")
	v, err = cacheIface.GetOrInitCall(ctx, CacheKey{ID: key100}, func(ctx context.Context) (AnyResult, error) {
		res, err := cacheIface2.GetOrInitCall(ctx, CacheKey{ID: key100}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key100, 101), nil
		})
		if err != nil {
			return nil, err
		}
		return cacheTestIntResult(key100, cacheTestUnwrapInt(t, res)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 101, cacheTestUnwrapInt(t, v))
}

func TestCacheContextCancel(t *testing.T) {
	t.Run("cancels after all are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)

		keyID := cacheTestID("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		ctx2, cancel2 := context.WithCancel(ctx)
		ctx3, cancel3 := context.WithCancel(ctx)

		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		go func() {
			defer close(errCh1)
			_, err := cacheIface.GetOrInitCall(ctx1, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(ctx context.Context) (AnyResult, error) {
				close(started1)
				<-ctx.Done()
				return nil, fmt.Errorf("oh no 1")
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
			_, err := cacheIface.GetOrInitCall(ctx2, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(ctx context.Context) (AnyResult, error) {
				<-ctx.Done()
				return nil, fmt.Errorf("oh no 2")
			})
			errCh2 <- err
		}()

		errCh3 := make(chan error, 1)
		go func() {
			defer close(errCh3)
			_, err := cacheIface.GetOrInitCall(ctx3, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("oh no 3")
			})
			errCh3 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
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
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
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
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh1")
		}
	})

	t.Run("succeeds if others are canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)

		keyID := cacheTestID("1")
		ctx1, cancel1 := context.WithCancel(ctx)
		t.Cleanup(cancel1)
		ctx2, cancel2 := context.WithCancel(ctx)

		resCh1 := make(chan AnyResult, 1)
		errCh1 := make(chan error, 1)
		started1 := make(chan struct{})
		stop1 := make(chan struct{})
		go func() {
			defer close(resCh1)
			defer close(errCh1)
			res, err := cacheIface.GetOrInitCall(ctx1, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				close(started1)
				<-stop1
				return cacheTestIntResult(keyID, 0), nil
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
			_, err := cacheIface.GetOrInitCall(ctx2, CacheKey{
				ID:             keyID,
				ConcurrencyKey: "1",
			}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("unexpected initializer call")
			})
			errCh2 <- err
		}()

		cancel2()
		select {
		case err := <-errCh2:
			assert.Assert(t, is.ErrorIs(err, context.Canceled))
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for errCh2")
		}

		close(stop1)
		select {
		case res := <-resCh1:
			assert.Equal(t, 0, cacheTestUnwrapInt(t, res))
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
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache)
		assert.Assert(t, ok)

		key1 := cacheTestID("1")
		key2 := cacheTestID("2")

		res1A, err := c.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1, 1), nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1, 1), nil
		})
		assert.NilError(t, err)

		res2, err := c.GetOrInitCall(ctx, CacheKey{ID: key2}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key2, 2), nil
		})
		assert.NilError(t, err)

		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 2, len(c.egraphResultTerms))

		err = res2.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.egraphResultTerms))

		err = res1A.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 1, len(c.egraphResultTerms))

		err = res1B.Release(ctx)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(c.ongoingCalls))
		assert.Equal(t, 0, len(c.egraphResultTerms))
	})

	t.Run("onRelease", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		cacheIface, err := NewCache(ctx, "")
		assert.NilError(t, err)
		c, ok := cacheIface.(*cache)
		assert.Assert(t, ok)

		key1 := cacheTestID("1")
		key2 := cacheTestID("2")

		releaseCalledCh := make(chan struct{})
		res1A, err := c.GetOrInitCall(ctx, CacheKey{
			ID:             key1,
			ConcurrencyKey: "1",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResultWithOnRelease(key1, 1, func(context.Context) error {
				close(releaseCalledCh)
				return nil
			}), nil
		})
		assert.NilError(t, err)
		res1B, err := c.GetOrInitCall(ctx, CacheKey{ID: key1}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(key1, 1), nil
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
		res2, err := c.GetOrInitCall(ctx, CacheKey{
			ID:             key2,
			ConcurrencyKey: "1",
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResultWithOnRelease(key2, 2, func(context.Context) error {
				return fmt.Errorf("oh no")
			}), nil
		})
		assert.NilError(t, err)

		err = res2.Release(ctx)
		assert.ErrorContains(t, err, "oh no")
	})
}

func TestSkipDedupe(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyID := cacheTestID("1")
	var eg errgroup.Group

	valCh1 := make(chan int, 1)
	started1 := make(chan struct{})
	stop1 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
			defer close(valCh1)
			close(started1)
			valCh1 <- 1
			<-stop1
			return cacheTestIntResult(keyID, 1), nil
		})
		return err
	})

	valCh2 := make(chan int, 1)
	started2 := make(chan struct{})
	stop2 := make(chan struct{})
	eg.Go(func() error {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
			defer close(valCh2)
			close(started2)
			valCh2 <- 2
			<-stop2
			return cacheTestIntResult(keyID, 2), nil
		})
		return err
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
		t.Fatal("timed out waiting for valCh1")
	}
	select {
	case val := <-valCh2:
		assert.Equal(t, 2, val)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for valCh2")
	}

	assert.NilError(t, eg.Wait())
}

func TestCacheNilKeyIDRejected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	_, err = cacheIface.GetOrInitCall(ctx, CacheKey{}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.ErrorContains(t, err, "cache key ID is nil")
}

func TestCacheDifferentConcurrencyKeysDoNotDedupe(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	keyID := cacheTestID("different-concurrency")
	release := make(chan struct{})
	startedA := make(chan struct{})
	startedB := make(chan struct{})
	errCh := make(chan error, 2)
	var initCalls atomic.Int32

	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{
			ID:             keyID,
			ConcurrencyKey: "a",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedA)
			<-release
			return cacheTestIntResult(keyID, 1), nil
		})
		errCh <- err
	}()
	go func() {
		_, err := cacheIface.GetOrInitCall(ctx, CacheKey{
			ID:             keyID,
			ConcurrencyKey: "b",
		}, func(context.Context) (AnyResult, error) {
			initCalls.Add(1)
			close(startedB)
			<-release
			return cacheTestIntResult(keyID, 2), nil
		})
		errCh <- err
	}()

	select {
	case <-startedA:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startedA")
	}
	select {
	case <-startedB:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for startedB")
	}

	close(release)
	assert.NilError(t, <-errCh)
	assert.NilError(t, <-errCh)
	assert.Equal(t, int32(2), initCalls.Load())
}

func TestCacheNilResultIsCached(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("nil-result")
	initCalls := 0

	res, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		initCalls++
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	valueRes, ok := res.(cacheValueResult)
	assert.Assert(t, ok)
	assert.Assert(t, !valueRes.cacheHasValue())

	res, err = c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyID, 42), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res != nil)
	valueRes, ok = res.(cacheValueResult)
	assert.Assert(t, ok)
	assert.Assert(t, !valueRes.cacheHasValue())
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())
}

func TestCacheDoNotCacheSkipsStorage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("do-not-cache")

	for i := 1; i <= 2; i++ {
		res, err := c.GetOrInitCall(ctx, CacheKey{
			ID:         keyID,
			DoNotCache: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(keyID, i), nil
		})
		assert.NilError(t, err)
		assert.Assert(t, !res.HitCache())
		assert.Equal(t, i, cacheTestUnwrapInt(t, res))
	}

	assert.Equal(t, 0, c.Size())
}

func TestCacheContentDigestDoesNotLookupAcrossDistinctRecipeIDs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	contentDigest := digest.FromString("shared-content-digest")
	keyA := cacheTestID("content-a").
		With(call.WithContentDigest(contentDigest))
	keyB := cacheTestID("content-b").
		With(call.WithContentDigest(contentDigest))

	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyA, NewInt(11)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, keyA.Digest().String(), resA.ID().Digest().String())

	initCalls := 0
	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyB, NewInt(22)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, keyB.Digest().String(), resB.ID().Digest().String())
	assert.Equal(t, 22, cacheTestUnwrapInt(t, resB))

	resAHit, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, resAHit.HitCache())
	assert.Equal(t, keyA.Digest().String(), resAHit.ID().Digest().String())
	assert.Equal(t, keyA.Digest().String(), resA.ID().Digest().String())

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.NilError(t, resAHit.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEgraphRepairsExistingTermsOnInputMerge(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv1 := cacheTestID("egraph-recv-1")
	recv2 := cacheTestID("egraph-recv-2")

	f1Key := recv1.Append(Int(0).Type(), "egraph-f")
	f2Key := recv2.Append(Int(0).Type(), "egraph-f")
	g1Key := f1Key.Append(Int(0).Type(), "egraph-g")
	g2Key := f2Key.Append(Int(0).Type(), "egraph-g")

	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(700)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	sharedContent := digest.FromString("egraph-shared-content")
	f1Out := f1Key.
		With(call.WithContentDigest(sharedContent))
	f2Out := f2Key.
		With(call.WithContentDigest(sharedContent))

	fInitCalls := 0
	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f1Out, NewInt(1)), nil
	})
	assert.NilError(t, err)
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f2Out, NewInt(2)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, fInitCalls)

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(701)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 700, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEgraphOutputEquivalenceDigestUnionsAcrossRecipeDigests(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv1 := cacheTestID("egraph-outputeq-recv-1")
	recv2 := cacheTestID("egraph-outputeq-recv-2")

	f1Key := recv1.Append(Int(0).Type(), "egraph-outputeq-f")
	f2Key := recv2.Append(Int(0).Type(), "egraph-outputeq-f")
	g1Key := f1Key.Append(Int(0).Type(), "egraph-outputeq-g")
	g2Key := f2Key.Append(Int(0).Type(), "egraph-outputeq-g")

	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(710)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	sharedOutputEq := digest.FromString("egraph-shared-output-equivalence")
	f1Out := f1Key.With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))
	f2Out := f2Key.With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))

	fInitCalls := 0
	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f1Out, NewInt(1)), nil
	})
	assert.NilError(t, err)
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f2Out, NewInt(2)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, fInitCalls)

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(711)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 710, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEgraphExtraDigestUnionsAcrossRecipeDigests(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv1 := cacheTestID("egraph-aux-recv-1")
	recv2 := cacheTestID("egraph-aux-recv-2")

	f1Key := recv1.Append(Int(0).Type(), "egraph-aux-f")
	f2Key := recv2.Append(Int(0).Type(), "egraph-aux-f")
	g1Key := f1Key.Append(Int(0).Type(), "egraph-aux-g")
	g2Key := f2Key.Append(Int(0).Type(), "egraph-aux-g")

	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(810)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	sharedAux := digest.FromString("egraph-shared-aux-only")
	auxDigest := call.ExtraDigest{
		Digest: sharedAux,
		Label:  "aux-shared",
	}
	f1Out := f1Key.With(call.WithExtraDigest(auxDigest))
	f2Out := f2Key.With(call.WithExtraDigest(auxDigest))

	fInitCalls := 0
	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f1Out, NewInt(1)), nil
	})
	assert.NilError(t, err)
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f2Out, NewInt(2)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, fInitCalls)

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(811)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 810, cacheTestUnwrapInt(t, g2Res))

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEgraphMixedExtraDigestSetsUnionOnSharedDigest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv1 := cacheTestID("egraph-mixed-recv-1")
	recv2 := cacheTestID("egraph-mixed-recv-2")
	recv3 := cacheTestID("egraph-mixed-recv-3")

	f1Key := recv1.Append(Int(0).Type(), "egraph-mixed-f")
	f2Key := recv2.Append(Int(0).Type(), "egraph-mixed-f")
	f3Key := recv3.Append(Int(0).Type(), "egraph-mixed-f")

	g1Key := f1Key.Append(Int(0).Type(), "egraph-mixed-g")
	g2Key := f2Key.Append(Int(0).Type(), "egraph-mixed-g")
	g3Key := f3Key.Append(Int(0).Type(), "egraph-mixed-g")

	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(910)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	outputEqA := digest.FromString("egraph-mixed-output-a")
	outputEqB := digest.FromString("egraph-mixed-output-b")
	auxShared := digest.FromString("egraph-mixed-aux-shared")
	auxOther := digest.FromString("egraph-mixed-aux-other")

	f1Out := f1Key.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: outputEqA})).
		With(call.WithExtraDigest(call.ExtraDigest{
			Digest: auxShared,
			Label:  "aux-shared",
		}))
	f2Out := f2Key.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: outputEqB})).
		With(call.WithExtraDigest(call.ExtraDigest{
			Digest: auxShared,
			Label:  "aux-shared",
		}))
	f3Out := f3Key.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: outputEqA})).
		With(call.WithExtraDigest(call.ExtraDigest{
			Digest: auxOther,
			Label:  "aux-other",
		}))

	fInitCalls := 0
	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f1Out, NewInt(1)), nil
	})
	assert.NilError(t, err)
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f2Out, NewInt(2)), nil
	})
	assert.NilError(t, err)
	f3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f3Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f3Out, NewInt(3)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 3, fInitCalls)

	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(911)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 910, cacheTestUnwrapInt(t, g2Res))

	g3InitCalls := 0
	g3Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g3Key}, func(context.Context) (AnyResult, error) {
		g3InitCalls++
		return newDetachedResult(g3Key, NewInt(912)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g3InitCalls)
	assert.Assert(t, g3Res.HitCache())
	assert.Equal(t, 910, cacheTestUnwrapInt(t, g3Res))

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.NilError(t, g3Res.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.NilError(t, f3Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheZeroInputCallsStillParticipateInUnifiedLookup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)

	key := cacheTestID("egraph-zero-input")
	c := cacheIface.(*cache)

	// Acceptance criteria:
	// 1. A zero-input call can be cached and looked up without requiring any
	//    separate lookup branch.
	// 2. A repeat request for the same recipe returns a normal cache hit (not an
	//    output-equivalence hit), and preserves request recipe identity.
	initCalls := 0
	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: key}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(key, NewInt(1)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res1))

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: key}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(key, NewInt(2)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, cacheTestUnwrapInt(t, res2))
	assert.Equal(t, key.Digest().String(), res2.ID().Digest().String())

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheStructuralLookupCanReuseEquivalentResultAcrossStorageKeys(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv1 := cacheTestID("egraph-prefer-exact-recv-1")
	recv2 := cacheTestID("egraph-prefer-exact-recv-2")

	f1Key := recv1.Append(Int(0).Type(), "egraph-prefer-exact-f")
	f2Key := recv2.Append(Int(0).Type(), "egraph-prefer-exact-f")
	g1Key := f1Key.Append(Int(0).Type(), "egraph-prefer-exact-g")
	g2Key := f2Key.Append(Int(0).Type(), "egraph-prefer-exact-g")

	sharedContent := digest.FromString("egraph-prefer-exact-shared-content")
	f1Out := f1Key.With(call.WithContentDigest(sharedContent))
	f2Out := f2Key.With(call.WithContentDigest(sharedContent))

	fInitCalls := 0
	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f1Out, NewInt(1)), nil
	})
	assert.NilError(t, err)
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f2Out, NewInt(2)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, fInitCalls)

	g1InitCalls := 0
	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		g1InitCalls++
		return newDetachedResult(g1Key, NewInt(100)).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, g1InitCalls)
	assert.Assert(t, !g1Res.HitCache())

	g2InitCalls := 0
	g2MissRes, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key, TTL: 1}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(200)).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2MissRes.HitCache())
	assert.Equal(t, 100, cacheTestUnwrapInt(t, g2MissRes))

	g2HitRes, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, g2HitRes.HitCache())
	assert.Equal(t, 100, cacheTestUnwrapInt(t, g2HitRes))
	assert.Equal(t, g2Key.Digest().String(), g2HitRes.ID().Digest().String())

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2MissRes.Release(ctx))
	assert.NilError(t, g2HitRes.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheStructuralLookupReturnsAnyLiveEquivalentCandidate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv1 := cacheTestID("egraph-prefer-exact-recipe-recv-1")
	recv2 := cacheTestID("egraph-prefer-exact-recipe-recv-2")

	f1Key := recv1.Append(Int(0).Type(), "egraph-prefer-exact-recipe-f")
	f2Key := recv2.Append(Int(0).Type(), "egraph-prefer-exact-recipe-f")
	g1Key := f1Key.Append(Int(0).Type(), "egraph-prefer-exact-recipe-g")
	g2Key := f2Key.Append(Int(0).Type(), "egraph-prefer-exact-recipe-g")

	// Acceptance criteria:
	// 1. After structural equivalence is discovered, a request may hit any
	//    live equivalent candidate.
	// 2. Request recipe identity is preserved on the returned ID.
	gInitCalls := 0
	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		gInitCalls++
		return newDetachedResult(g1Key, NewInt(700)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		gInitCalls++
		return newDetachedResult(g2Key, NewInt(701)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g2Res.HitCache())
	assert.Equal(t, 2, gInitCalls)

	sharedOutputEq := digest.FromString("egraph-prefer-exact-recipe-shared-output-eq")
	f1Out := f1Key.With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))
	f2Out := f2Key.With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))

	fInitCalls := 0
	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f1Out, NewInt(1)), nil
	})
	assert.NilError(t, err)
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2Key}, func(context.Context) (AnyResult, error) {
		fInitCalls++
		return newDetachedResult(f2Out, NewInt(2)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, fInitCalls)

	g2HitRes, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, g2HitRes.HitCache())
	g2Val := cacheTestUnwrapInt(t, g2HitRes)
	assert.Assert(t, g2Val == 700 || g2Val == 701)
	assert.Equal(t, g2Key.Digest().String(), g2HitRes.ID().Digest().String())

	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.NilError(t, g2HitRes.Release(ctx))
	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEgraphAdditionalDigestDoesNotAffectTermIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	recv := cacheTestID("egraph-custom-recv")
	base := recv.Append(Int(0).Type(), "egraph-custom")
	keyA := base.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("egraph-custom-a")})).
		With(call.WithContentDigest(digest.FromString("egraph-custom-content-a")))
	keyB := base.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("egraph-custom-b")})).
		With(call.WithContentDigest(digest.FromString("egraph-custom-content-b")))

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyA, NewInt(10)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyB, NewInt(20)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 10, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheAdditionalDigestKeysDoNotAffectStructuralLookupIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	base := cacheTestID("additional-cache-key")
	keyA := base.With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("additional-key-a")}))
	keyB := base.With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("additional-key-b")}))

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyA, NewInt(41)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyB, NewInt(42)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 41, cacheTestUnwrapInt(t, resA))
	assert.Equal(t, 41, cacheTestUnwrapInt(t, resB))

	resAHit, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyA, NewInt(99)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, resAHit.HitCache())
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 41, cacheTestUnwrapInt(t, resAHit))

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.NilError(t, resAHit.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheAdditionalDigestDoesNotLookupAcrossDistinctRecipeIDs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedAdditional := digest.FromString("additional-lookup-shared")
	keyA := cacheTestID("additional-lookup-a").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedAdditional}))
	keyB := cacheTestID("additional-lookup-b").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedAdditional}))

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("additional-lookup-result-a"), NewInt(81)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, 81, cacheTestUnwrapInt(t, resA))

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("additional-lookup-result-b"), NewInt(82)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 2, initCalls)
	assert.Equal(t, 82, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheOutputEquivalenceLookupRespectsImplicitInputScope(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedOutputEq := digest.FromString("implicit-scope-shared-output-eq")
	base := cacheTestID("implicit-scope")
	keyA := base.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq})).
		With(call.WithImplicitInputs(
			call.NewArgument("cachePerClient", call.NewLiteralString("client-a"), false),
		))
	keyB := base.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq})).
		With(call.WithImplicitInputs(
			call.NewArgument("cachePerClient", call.NewLiteralString("client-b"), false),
		))

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyA, NewInt(101)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyB, NewInt(202)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 2, initCalls)
	assert.Equal(t, 202, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheOutputEquivalenceLookupSeparatesDagOpExecutionMode(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedOutputEq := digest.FromString("dagop-mode-shared-output-eq")
	base := cacheTestID("dagop-mode")
	normalKey := base.With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))
	dagOpKey := base.
		With(call.WithArgs(
			call.NewArgument("isDagOp", call.NewLiteralBool(true), false),
		)).
		With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))

	initCalls := 0
	normalRes, err := c.GetOrInitCall(ctx, CacheKey{ID: normalKey}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(normalKey, NewInt(301)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !normalRes.HitCache())

	dagOpRes, err := c.GetOrInitCall(ctx, CacheKey{ID: dagOpKey}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(dagOpKey, NewInt(302)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !dagOpRes.HitCache())
	assert.Equal(t, 2, initCalls)
	assert.Equal(t, 302, cacheTestUnwrapInt(t, dagOpRes))

	assert.NilError(t, normalRes.Release(ctx))
	assert.NilError(t, dagOpRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheOutputEquivalenceDigestDoesNotBypassStructuralLookup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedOutputEq := digest.FromString("output-eq-class-only")
	keyA := cacheTestID("output-eq-class-a").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))
	keyB := cacheTestID("output-eq-class-b").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyA, NewInt(501)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyB, NewInt(502)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, initCalls)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 502, cacheTestUnwrapInt(t, resB))
	assert.Equal(t, keyB.Digest().String(), resB.ID().Digest().String())

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheStructuralLookupByIDReturnsLiveTerm(t *testing.T) {
	t.Parallel()
	c := &cache{}
	requestID := cacheTestID("structural-term-request")
	selfDigest, inputDigests, err := requestID.SelfDigestAndInputs()
	assert.NilError(t, err)
	inputEqIDs := c.ensureTermInputEqIDsLocked(inputDigests)
	termDigest := calcEgraphTermDigest(selfDigest, inputEqIDs)

	older := &sharedResult{storageKey: "storage-older", self: NewInt(1), hasValue: true}
	other := &sharedResult{storageKey: "storage-other", self: NewInt(2), hasValue: true}

	c.initEgraphLocked()
	olderTerm := newEgraphTerm(1, selfDigest, inputEqIDs, 0, nil, older)
	otherTerm := newEgraphTerm(2, selfDigest, inputEqIDs, 0, nil, other)
	c.egraphTerms[olderTerm.id] = olderTerm
	c.egraphTerms[otherTerm.id] = otherTerm
	c.egraphTermsByDigest[termDigest] = map[egraphTermID]struct{}{
		olderTerm.id: {},
		otherTerm.id: {},
	}

	hit, ok, err := c.lookupCacheForID(t.Context(), requestID)
	assert.NilError(t, err)
	assert.Assert(t, ok)
	assert.Assert(t, hit != nil)
	hitVal := cacheTestUnwrapInt(t, hit)
	assert.Assert(t, hitVal == 1 || hitVal == 2)
}

func TestCacheStructuralLookupByIDSkipsStaleTerms(t *testing.T) {
	t.Parallel()

	c := &cache{}
	requestID := cacheTestID("structural-term-deterministic-fallback-request")
	selfDigest, inputDigests, err := requestID.SelfDigestAndInputs()
	assert.NilError(t, err)
	inputEqIDs := c.ensureTermInputEqIDsLocked(inputDigests)
	termDigest := calcEgraphTermDigest(selfDigest, inputEqIDs)

	live := &sharedResult{storageKey: "storage-live", self: NewInt(3), hasValue: true}

	c.initEgraphLocked()
	staleTerm := newEgraphTerm(9, selfDigest, inputEqIDs, 0, nil, nil)
	liveTerm := newEgraphTerm(2, selfDigest, inputEqIDs, 0, nil, live)
	c.egraphTerms[staleTerm.id] = staleTerm
	c.egraphTerms[liveTerm.id] = liveTerm
	c.egraphTermsByDigest[termDigest] = map[egraphTermID]struct{}{
		staleTerm.id: {},
		liveTerm.id:  {},
	}

	hit, ok, err := c.lookupCacheForID(t.Context(), requestID)
	assert.NilError(t, err)
	assert.Assert(t, ok)
	assert.Assert(t, hit != nil)
	assert.Equal(t, 3, cacheTestUnwrapInt(t, hit))
}

func TestCacheAdditionalDigestDirectHitPreservesRequestID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	base := cacheTestID("additional-direct-hit")
	key := base.With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("additional-direct-hit-key")}))

	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: key}, func(context.Context) (AnyResult, error) {
		// Constructor intentionally omits the additional digest key.
		return newDetachedResult(base, NewInt(5)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())
	assert.Equal(t, key.Digest().String(), res1.ID().Digest().String())

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: key}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 5, cacheTestUnwrapInt(t, res2))
	assert.Equal(t, key.Digest().String(), res2.ID().Digest().String())

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEquivalentHitMergesRequestDigestIntoOutputEqClass(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	base := cacheTestID("equivalent-hit-merge")
	requestA := base.With(call.WithExtraDigest(call.ExtraDigest{
		Digest: digest.FromString("equivalent-hit-request-a"),
	}))
	requestB := base.With(call.WithExtraDigest(call.ExtraDigest{
		Digest: digest.FromString("equivalent-hit-request-b"),
	}))

	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: requestA}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(base, NewInt(41)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: requestB}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, resB.HitCache())
	assert.Equal(t, 41, cacheTestUnwrapInt(t, resB))

	c.mu.Lock()
	classA := c.findEqClassLocked(c.egraphDigestToClass[requestA.Digest().String()])
	classB := c.findEqClassLocked(c.egraphDigestToClass[requestB.Digest().String()])
	c.mu.Unlock()
	assert.Assert(t, classA != 0)
	assert.Assert(t, classB != 0)
	assert.Equal(t, classA, classB)

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheSemanticConstructorDoesNotEnableCrossRecipeLookup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedAdditional := digest.FromString("presentation-shared-additional")
	requestA := cacheTestID("presentation-request-a").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedAdditional}))
	requestB := cacheTestID("presentation-request-b").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedAdditional}))
	semantic := cacheTestID("presentation-semantic-result")

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: requestA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(semantic, NewInt(91)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, requestA.Digest().String(), resA.ID().Digest().String())
	assert.Equal(t, requestA.Digest().String(), resA.ID().Digest().String())

	cacheBacked, ok := resA.(cacheBackedResult)
	assert.Assert(t, ok)
	cached := cacheBacked.cacheSharedResult()
	assert.Assert(t, cached != nil)

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: requestB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("presentation-unexpected"), NewInt(92)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 2, initCalls)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 92, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheExtraDigestDoesNotLookupAcrossDistinctRecipeIDs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedAux := digest.FromString("aux-shared")
	keyA := cacheTestID("aux-lookup-a").With(call.WithExtraDigest(call.ExtraDigest{
		Digest: sharedAux,
		Label:  "aux-shared",
	}))
	keyB := cacheTestID("aux-lookup-b").With(call.WithExtraDigest(call.ExtraDigest{
		Digest: sharedAux,
		Label:  "aux-shared",
	}))

	initCalls := 0
	resA, err := c.GetOrInitCall(ctx, CacheKey{ID: keyA}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("aux-lookup-result-a"), NewInt(81)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())

	resB, err := c.GetOrInitCall(ctx, CacheKey{ID: keyB}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("aux-lookup-result-b"), NewInt(82)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resB.HitCache())
	assert.Equal(t, 2, initCalls)
	assert.Equal(t, 82, cacheTestUnwrapInt(t, resB))

	assert.NilError(t, resA.Release(ctx))
	assert.NilError(t, resB.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheKnownDigestLookupDoesNotBypassDistinctRecipes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	shared := digest.FromString("kind-shared")
	auxKey := cacheTestID("kind-lookup-aux").With(call.WithExtraDigest(call.ExtraDigest{
		Digest: shared,
		Label:  "aux-shared",
	}))
	outputKey := cacheTestID("kind-lookup-output").With(call.WithExtraDigest(call.ExtraDigest{
		Digest: shared,
		Label:  "output-shared",
	}))

	initCalls := 0
	resAux, err := c.GetOrInitCall(ctx, CacheKey{ID: auxKey}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("kind-lookup-result-aux"), NewInt(71)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resAux.HitCache())

	resOutput, err := c.GetOrInitCall(ctx, CacheKey{ID: outputKey}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(cacheTestID("kind-lookup-result-output"), NewInt(72)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resOutput.HitCache())
	assert.Equal(t, 2, initCalls)
	assert.Equal(t, 72, cacheTestUnwrapInt(t, resOutput))

	assert.NilError(t, resAux.Release(ctx))
	assert.NilError(t, resOutput.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheEgraphTermDigestPreservesInputOrder(t *testing.T) {
	t.Parallel()
	selfDigest := digest.FromString("egraph-term-order-self")

	ab := calcEgraphTermDigest(selfDigest, []eqClassID{9, 4})
	ba := calcEgraphTermDigest(selfDigest, []eqClassID{4, 9})
	assert.Assert(t, ab != ba)
}

func TestCacheStructuralTermDigestSeparatesDistinctBoundaryInputs(t *testing.T) {
	t.Parallel()
	base := cacheTestID("structural-boundary-separation")

	idA := base.With(call.WithImplicitInputs(
		call.NewArgument("cachePerClient", call.NewLiteralString("client-a"), false),
	))
	idB := base.With(call.WithImplicitInputs(
		call.NewArgument("cachePerClient", call.NewLiteralString("client-b"), false),
	))

	selfA, _, err := idA.SelfDigestAndInputs()
	assert.NilError(t, err)
	selfB, _, err := idB.SelfDigestAndInputs()
	assert.NilError(t, err)
	termDigestA := calcEgraphTermDigest(selfA, nil)
	termDigestB := calcEgraphTermDigest(selfB, nil)

	assert.Assert(t, termDigestA != termDigestB)

	normalID := base
	dagOpID := base.With(call.WithArgs(
		call.NewArgument("isDagOp", call.NewLiteralBool(true), false),
	))
	selfNormal, _, err := normalID.SelfDigestAndInputs()
	assert.NilError(t, err)
	selfDagOp, _, err := dagOpID.SelfDigestAndInputs()
	assert.NilError(t, err)
	termDigestNormal := calcEgraphTermDigest(selfNormal, nil)
	termDigestDagOp := calcEgraphTermDigest(selfDagOp, nil)

	assert.Assert(t, termDigestNormal != termDigestDagOp)
}

func TestCacheEgraphConcurrentRepairStress(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	const count = 48
	sharedContent := digest.FromString("egraph-stress-shared-content")
	fKeys := make([]*call.ID, 0, count)
	fOuts := make([]*call.ID, 0, count)
	gKeys := make([]*call.ID, 0, count)
	for i := 0; i < count; i++ {
		recv := cacheTestID(fmt.Sprintf("egraph-stress-recv-%d", i))
		fKey := recv.Append(Int(0).Type(), "egraph-stress-f")
		fOut := fKey.
			With(call.WithContentDigest(sharedContent))
		gKey := fKey.Append(Int(0).Type(), "egraph-stress-g")
		fKeys = append(fKeys, fKey)
		fOuts = append(fOuts, fOut)
		gKeys = append(gKeys, gKey)
	}

	f0Res, err := c.GetOrInitCall(ctx, CacheKey{ID: fKeys[0]}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(fOuts[0], NewInt(10)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f0Res.HitCache())

	const canonicalG = 777
	g0Res, err := c.GetOrInitCall(ctx, CacheKey{ID: gKeys[0]}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(gKeys[0], NewInt(canonicalG)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g0Res.HitCache())

	var gInitCalls atomic.Int32
	var eg errgroup.Group
	for i := 1; i < count; i++ {
		i := i
		eg.Go(func() error {
			fRes, err := c.GetOrInitCall(ctx, CacheKey{ID: fKeys[i]}, func(context.Context) (AnyResult, error) {
				return newDetachedResult(fOuts[i], NewInt(i)), nil
			})
			if err != nil {
				return fmt.Errorf("init f[%d]: %w", i, err)
			}

			gRes, err := c.GetOrInitCall(ctx, CacheKey{ID: gKeys[i]}, func(context.Context) (AnyResult, error) {
				gInitCalls.Add(1)
				return newDetachedResult(gKeys[i], NewInt(1000+i)), nil
			})
			if err != nil {
				_ = fRes.Release(ctx)
				return fmt.Errorf("get g[%d]: %w", i, err)
			}

			if !gRes.HitCache() {
				_ = gRes.Release(ctx)
				_ = fRes.Release(ctx)
				return fmt.Errorf("expected cache hit for g[%d]", i)
			}
			v, ok := UnwrapAs[Int](gRes)
			if !ok || int(v) != canonicalG {
				_ = gRes.Release(ctx)
				_ = fRes.Release(ctx)
				return fmt.Errorf("unexpected g[%d] value: %v (ok=%v)", i, gRes.Unwrap(), ok)
			}

			if err := gRes.Release(ctx); err != nil {
				_ = fRes.Release(ctx)
				return fmt.Errorf("release g[%d]: %w", i, err)
			}
			if err := fRes.Release(ctx); err != nil {
				return fmt.Errorf("release f[%d]: %w", i, err)
			}
			return nil
		})
	}
	assert.NilError(t, eg.Wait())
	assert.Equal(t, int32(0), gInitCalls.Load())

	assert.NilError(t, g0Res.Release(ctx))
	assert.NilError(t, f0Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func BenchmarkCacheEgraphConcurrentRepair(b *testing.B) {
	ctx := context.Background()
	cacheIface, err := NewCache(ctx, "")
	if err != nil {
		b.Fatalf("new cache: %v", err)
	}
	c := cacheIface.(*cache)

	sharedContent := digest.FromString("egraph-bench-shared-content")
	recv0 := cacheTestID("egraph-bench-recv-0")
	f0Key := recv0.Append(Int(0).Type(), "egraph-bench-f")
	f0Out := f0Key.
		With(call.WithContentDigest(sharedContent))
	g0Key := f0Key.Append(Int(0).Type(), "egraph-bench-g")

	f0Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f0Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f0Out, NewInt(100)), nil
	})
	if err != nil {
		b.Fatalf("seed f0: %v", err)
	}
	g0Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g0Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g0Key, NewInt(777)), nil
	})
	if err != nil {
		_ = f0Res.Release(ctx)
		b.Fatalf("seed g0: %v", err)
	}
	b.Cleanup(func() {
		_ = g0Res.Release(ctx)
		_ = f0Res.Release(ctx)
	})

	var seq atomic.Uint64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := seq.Add(1)

			recv := cacheTestID(fmt.Sprintf("egraph-bench-recv-%d", i))
			fKey := recv.Append(Int(0).Type(), "egraph-bench-f")
			fOut := fKey.
				With(call.WithContentDigest(sharedContent))
			gKey := fKey.Append(Int(0).Type(), "egraph-bench-g")

			fRes, err := c.GetOrInitCall(ctx, CacheKey{ID: fKey}, func(context.Context) (AnyResult, error) {
				return newDetachedResult(fOut, NewInt(int(i))), nil
			})
			if err != nil {
				panic(fmt.Errorf("bench f[%d]: %w", i, err))
			}

			gRes, err := c.GetOrInitCall(ctx, CacheKey{ID: gKey}, func(context.Context) (AnyResult, error) {
				return newDetachedResult(gKey, NewInt(-1)), nil
			})
			if err != nil {
				_ = fRes.Release(ctx)
				panic(fmt.Errorf("bench g[%d]: %w", i, err))
			}
			if !gRes.HitCache() {
				_ = gRes.Release(ctx)
				_ = fRes.Release(ctx)
				panic(fmt.Errorf("bench g[%d]: expected cache hit", i))
			}

			_ = gRes.Release(ctx)
			_ = fRes.Release(ctx)
		}
	})
}

func TestCacheEgraphDownstreamReuseAfterPostExecContentUnion(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedContent := digest.FromString("egraph-content-hit-alias")
	f1Key := cacheTestID("egraph-alias-f1")
	f2Key := cacheTestID("egraph-alias-f2")
	f1Out := f1Key.With(call.WithContentDigest(sharedContent))
	f2LookupID := f2Key.With(call.WithContentDigest(sharedContent))

	f1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(f1Out, NewInt(11)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !f1Res.HitCache())

	g1Key := f1Key.Append(Int(0).Type(), "egraph-alias-g")
	g1Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g1Key}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(g1Key, NewInt(111)), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !g1Res.HitCache())

	f2InitCalls := 0
	f2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: f2LookupID}, func(context.Context) (AnyResult, error) {
		f2InitCalls++
		return newDetachedResult(f2LookupID, NewInt(22)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, f2InitCalls)
	assert.Assert(t, !f2Res.HitCache())
	assert.Equal(t, f2LookupID.Digest().String(), f2Res.ID().Digest().String())

	g2Key := f2Key.Append(Int(0).Type(), "egraph-alias-g")
	g2InitCalls := 0
	g2Res, err := c.GetOrInitCall(ctx, CacheKey{ID: g2Key}, func(context.Context) (AnyResult, error) {
		g2InitCalls++
		return newDetachedResult(g2Key, NewInt(222)), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, g2InitCalls)
	assert.Assert(t, g2Res.HitCache())
	assert.Equal(t, 111, cacheTestUnwrapInt(t, g2Res))
	assert.Equal(t, g2Key.Digest().String(), g2Res.ID().Digest().String())

	assert.NilError(t, f1Res.Release(ctx))
	assert.NilError(t, g1Res.Release(ctx))
	assert.NilError(t, f2Res.Release(ctx))
	assert.NilError(t, g2Res.Release(ctx))
	assert.Equal(t, 0, c.Size())
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.egraphDigestToClass))
}

func TestCachePostCallAndSafeToPersistMetadataPreserved(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("metadata")
	postCallCount := 0

	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyID, NewInt(7)).
			ResultWithPostCall(func(context.Context) error {
				postCallCount++
				return nil
			}).
			WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res1.IsSafeToPersistCache())
	assert.NilError(t, res1.PostCall(ctx))
	assert.Equal(t, 1, postCallCount)

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	assert.Assert(t, res2.IsSafeToPersistCache())
	assert.NilError(t, res2.PostCall(ctx))
	assert.Equal(t, 2, postCallCount)

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestDerefValuePropagatesSafeToPersistMetadataForNullables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    Typed
		expected int
	}{
		{
			name: "dynamic-nullable",
			value: DynamicNullable{
				Elem:  NewInt(0),
				Value: NewInt(21),
				Valid: true,
			},
			expected: 21,
		},
		{
			name: "nullable-generic",
			value: Nullable[Int]{
				Value: NewInt(42),
				Valid: true,
			},
			expected: 42,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := cacheTestID(tc.name + "-safe")
			outer := newDetachedResult(id, tc.value).WithSafeToPersistCache(true)

			deref, ok := outer.DerefValue()
			assert.Assert(t, ok)
			assert.Assert(t, deref.IsSafeToPersistCache())
			assert.Equal(t, tc.expected, cacheTestUnwrapInt(t, deref))
		})
	}
}

func TestCacheDoNotCacheNormalizesNestedHitMetadata(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	innerID := cacheTestID("inner")
	innerRes, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(innerID, 9), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !innerRes.HitCache())

	outerID := cacheTestID("outer")
	outerRes, err := c.GetOrInitCall(ctx, CacheKey{
		ID:         outerID,
		DoNotCache: true,
	}, func(ctx context.Context) (AnyResult, error) {
		nested, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
			return nil, fmt.Errorf("unexpected nested initializer call")
		})
		if err != nil {
			return nil, err
		}
		assert.Assert(t, nested.HitCache())
		defer nested.Release(ctx)
		return nested, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !outerRes.HitCache())
	assert.Equal(t, 9, cacheTestUnwrapInt(t, outerRes))

	assert.NilError(t, outerRes.Release(ctx))
	assert.Equal(t, 1, c.Size())
	assert.NilError(t, innerRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheNestedReturnTransfersInnerRefOwnership(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	innerID := cacheTestID("inner-transfer")
	innerRes, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(innerID, 13), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !innerRes.HitCache())

	outerID := cacheTestID("outer-transfer")
	outerRes, err := c.GetOrInitCall(ctx, CacheKey{ID: outerID}, func(ctx context.Context) (AnyResult, error) {
		nested, err := c.GetOrInitCall(ctx, CacheKey{ID: innerID}, func(context.Context) (AnyResult, error) {
			return nil, fmt.Errorf("unexpected nested initializer call")
		})
		if err != nil {
			return nil, err
		}
		assert.Assert(t, nested.HitCache())
		// Returning nested transfers ownership of its cache ref to outerRes.
		return nested, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 13, cacheTestUnwrapInt(t, outerRes))

	assert.NilError(t, outerRes.Release(ctx))
	assert.Equal(t, 1, c.Size())
	assert.NilError(t, innerRes.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheSecondaryIndexesCleanedOnRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	storageID := cacheTestID("storage-key")
	resultID := storageID.
		With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("result-digest")})).
		With(call.WithContentDigest(digest.FromString("result-content")))

	res, err := c.GetOrInitCall(ctx, CacheKey{ID: storageID}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(resultID, NewInt(44)), nil
	})
	assert.NilError(t, err)

	storageKey := storageID.Digest().String()
	resultOutputEq := resultID.OutputEquivalentDigest().String()

	assert.Assert(t, storageKey != resultOutputEq)
	assert.Equal(t, 1, len(c.egraphResultTerms))
	assert.Assert(t, len(c.egraphTerms) > 0)
	assert.Assert(t, c.Size() > 0)

	assert.NilError(t, res.Release(ctx))
	assert.Equal(t, 0, len(c.ongoingCalls))
	assert.Equal(t, 0, len(c.egraphResultTerms))
	assert.Equal(t, 0, len(c.egraphTerms))
	assert.Equal(t, 0, len(c.egraphResultTerms))
}

func TestCacheArrayResultRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("array-result")
	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyID, NewIntArray(1, 2, 3)), nil
	})
	assert.NilError(t, err)
	enum1, ok := res1.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Equal(t, 3, enum1.Len())
	nth2, err := enum1.Nth(2)
	assert.NilError(t, err)
	v2, ok := nth2.(Int)
	assert.Assert(t, ok)
	assert.Equal(t, 2, int(v2))

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	enum2, ok := res2.Unwrap().(Enumerable)
	assert.Assert(t, ok)
	assert.Equal(t, 3, enum2.Len())

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, 0, c.Size())
}

func TestCacheObjectResultRoundTripAndRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)
	srv := cacheTestServer(t, c)

	keyID := cacheTestID("object-result")
	var releaseCalls atomic.Int32

	res1, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResult(t, srv, keyID, 42, func(context.Context) error {
			releaseCalls.Add(1)
			return nil
		}), nil
	})
	assert.NilError(t, err)
	obj1, ok := res1.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj1.Self().Value)
	assert.Assert(t, !obj1.HitCache())

	res2, err := c.GetOrInitCall(ctx, CacheKey{ID: keyID}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("unexpected initializer call")
	})
	assert.NilError(t, err)
	obj2, ok := res2.(ObjectResult[*cacheTestObject])
	assert.Assert(t, ok)
	assert.Equal(t, 42, obj2.Self().Value)
	assert.Assert(t, obj2.HitCache())

	assert.NilError(t, res1.Release(ctx))
	assert.Equal(t, int32(0), releaseCalls.Load())
	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, int32(1), releaseCalls.Load())
	assert.Equal(t, 0, c.Size())
}

func TestCacheTTLWithDBUsesStorageAndCallIndexes(t *testing.T) {
	t.Parallel()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "cache-test-client",
		SessionID: "cache-test-session",
	})
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheIface, err := NewCache(ctx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	keyID := cacheTestID("ttl-key")
	initCalls := 0

	res1, err := c.GetOrInitCall(ctx, CacheKey{
		ID:  keyID,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return newDetachedResult(keyID, NewInt(5)).WithSafeToPersistCache(true), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)

	res2, err := c.GetOrInitCall(ctx, CacheKey{
		ID:  keyID,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		initCalls++
		return cacheTestIntResult(keyID, 6), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, initCalls)
	assert.Assert(t, res2.HitCache())
	assert.Equal(t, 1, len(c.egraphResultTerms))

	assert.NilError(t, res1.Release(ctx))
	assert.NilError(t, res2.Release(ctx))
	// Persist-safe only affects DB metadata persistence; in-memory cache entries are
	// released when refs drain.
	assert.Equal(t, 0, len(c.egraphResultTerms))
	assert.Equal(t, 0, c.Size())
}

func TestCacheTTLNonPersistableEquivalentIDsCanCrossRecipeLookup(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	cacheIface, err := NewCache(baseCtx, dbPath)
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	sharedOutputEq := digest.FromString("ttl-nonpersist-session-scoped")
	keyA := cacheTestID("ttl-nonpersist-a").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))
	keyB := cacheTestID("ttl-nonpersist-b").With(call.WithExtraDigest(call.ExtraDigest{Digest: sharedOutputEq}))

	ctxSessionA := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "cache-test-client-a",
		SessionID: "cache-test-session-a",
	})
	ctxSessionB := engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{
		ClientID:  "cache-test-client-b",
		SessionID: "cache-test-session-b",
	})

	resA, err := c.GetOrInitCall(ctxSessionA, CacheKey{
		ID:  keyA,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		return newDetachedResult(keyA, NewInt(11)).WithSafeToPersistCache(false), nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !resA.HitCache())
	assert.Equal(t, 11, cacheTestUnwrapInt(t, resA))

	sameSessionInits := 0
	resSameSession, err := c.GetOrInitCall(ctxSessionA, CacheKey{
		ID:  keyB,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		sameSessionInits++
		return newDetachedResult(keyB, NewInt(22)).WithSafeToPersistCache(false), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 1, sameSessionInits)
	assert.Assert(t, !resSameSession.HitCache())
	assert.Equal(t, 22, cacheTestUnwrapInt(t, resSameSession))

	crossSessionInits := 0
	resCrossSession, err := c.GetOrInitCall(ctxSessionB, CacheKey{
		ID:  keyB,
		TTL: 60,
	}, func(context.Context) (AnyResult, error) {
		crossSessionInits++
		return newDetachedResult(keyB, NewInt(33)).WithSafeToPersistCache(false), nil
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, crossSessionInits)
	assert.Assert(t, resCrossSession.HitCache())
	assert.Equal(t, 22, cacheTestUnwrapInt(t, resCrossSession))

	assert.NilError(t, resA.Release(ctxSessionA))
	assert.NilError(t, resSameSession.Release(ctxSessionA))
	assert.NilError(t, resCrossSession.Release(ctxSessionB))
}

func TestCacheArbitraryRoundTripAndRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := "arbitrary-round-trip"
	var releaseCalls atomic.Int32
	initCalls := 0

	res1, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
		initCalls++
		return cacheTestOpaqueValue{
			value: "hello",
			onRelease: func(context.Context) error {
				releaseCalls.Add(1)
				return nil
			},
		}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, !res1.HitCache())
	v1, ok := res1.Value().(cacheTestOpaqueValue)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", v1.value)

	res2, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
		initCalls++
		return cacheTestOpaqueValue{value: "ignored"}, nil
	})
	assert.NilError(t, err)
	assert.Assert(t, res2.HitCache())
	v2, ok := res2.Value().(cacheTestOpaqueValue)
	assert.Assert(t, ok)
	assert.Equal(t, "hello", v2.value)
	assert.Equal(t, 1, initCalls)
	assert.Equal(t, 1, c.Size())

	assert.NilError(t, res1.Release(ctx))
	assert.Equal(t, int32(0), releaseCalls.Load())
	assert.Equal(t, 1, c.Size())

	assert.NilError(t, res2.Release(ctx))
	assert.Equal(t, int32(1), releaseCalls.Load())
	assert.Equal(t, 0, c.Size())
}

func TestCacheArbitraryConcurrent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := "arbitrary-concurrent"
	initialized := map[int]bool{}
	var initMu sync.Mutex
	const totalCallers = 100

	firstCallEntered := make(chan struct{})
	unblockFirstCall := make(chan struct{})

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
			initMu.Lock()
			initialized[0] = true
			initMu.Unlock()
			close(firstCallEntered)
			<-unblockFirstCall
			return "value", nil
		})
		assert.NilError(t, err)
		assert.Equal(t, "value", res.Value())
		assert.NilError(t, res.Release(ctx))
	}()

	select {
	case <-firstCallEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first caller to enter init callback")
	}

	for i := 1; i < totalCallers; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			res, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
				initMu.Lock()
				initialized[i] = true
				initMu.Unlock()
				return "value", nil
			})
			assert.NilError(t, err)
			assert.Equal(t, "value", res.Value())
			assert.NilError(t, res.Release(ctx))
		}()
	}

	waiterCountReached := false
	waiterPollDeadline := time.Now().Add(3 * time.Second)
	lastObservedWaiters := -1
	for time.Now().Before(waiterPollDeadline) {
		c.mu.Lock()
		oc := c.ongoingArbitraryCalls[key]
		if oc != nil {
			lastObservedWaiters = oc.waiters
		}
		c.mu.Unlock()

		if oc != nil && lastObservedWaiters == totalCallers {
			waiterCountReached = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, waiterCountReached, "expected %d waiters, last observed %d", totalCallers, lastObservedWaiters)

	close(unblockFirstCall)

	ongoingCleared := false
	clearPollDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(clearPollDeadline) {
		c.mu.Lock()
		_, exists := c.ongoingArbitraryCalls[key]
		c.mu.Unlock()
		if !exists {
			ongoingCleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Assert(t, ongoingCleared, "ongoing arbitrary call was not cleared")

	wg.Wait()

	initMu.Lock()
	defer initMu.Unlock()
	assert.Assert(t, is.Len(initialized, 1))
	assert.Assert(t, initialized[0])
	assert.Equal(t, 0, c.Size())
}

func TestCacheArbitraryRecursiveCall(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cacheIface, err := NewCache(ctx, "")
	assert.NilError(t, err)
	c := cacheIface.(*cache)

	key := "arbitrary-recursive"
	_, err = c.GetOrInitArbitrary(ctx, key, func(ctx context.Context) (any, error) {
		_, err := c.GetOrInitArbitrary(ctx, key, func(context.Context) (any, error) {
			return "should-not-run", nil
		})
		return nil, err
	})
	assert.Assert(t, is.ErrorIs(err, ErrCacheRecursiveCall))
}
